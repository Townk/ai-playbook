package author

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/config"
)

// shippedHarnesses lists every SHIPPED harness the contract suite runs over
// (the multi-harness spec's Testing bullet 1). H2/H3 add rows ("pi", "cursor"),
// not files — every entry must satisfy the whole contract below.
var shippedHarnesses = []string{"claude", "pi", "cursor"}

// sessionFlags are session-resume/continuation flags NO harness may emit: the
// codified contract (ADR-0012 decision 6) is fresh-process one-shot invocation,
// never session state. (A flag that DISABLES sessions, like pi's --no-session,
// is fine and not listed here.)
var sessionFlags = []string{"--resume", "--continue", "--session-id", "--fork-session", "-r", "-c"}

// TestHarnessContract runs the capability-contract checks over every shipped
// harness: registration, non-empty names, a registered stream adapter, argv
// sanity in both prompt modes, env hygiene, and ToolTransport behavior
// consistent with the declared Capabilities.
func TestHarnessContract(t *testing.T) {
	for _, name := range shippedHarnesses {
		t.Run(name, func(t *testing.T) {
			h, ok := harnessFor(name)
			if !ok {
				t.Fatalf("harness %q is not registered", name)
			}

			// Names: both labels non-empty; the adapter name resolves in the
			// agentstream registry (the matched invocation/parser contract).
			if h.DisplayName() == "" {
				t.Error("DisplayName() must be non-empty")
			}
			if h.AdapterName() == "" {
				t.Error("AdapterName() must be non-empty")
			}
			if _, ok := agentstream.Get(h.AdapterName()); !ok {
				t.Errorf("AdapterName() %q has no registered agentstream adapter", h.AdapterName())
			}

			// Argv sanity, both modes: the system prompt and user message must
			// appear (append mode for authoring, replace mode for bare), and no
			// session-resume flags may ever be emitted.
			const sys, user = "SYS-SENTINEL", "USER-SENTINEL"
			for _, mode := range []struct {
				label string
				inv   Invocation
			}{
				{"authoring (append)", Invocation{}},
				{"bare (replace)", Invocation{Bare: true}},
			} {
				args := h.Argv(sys, user, mode.inv)
				if len(args) == 0 {
					t.Fatalf("%s: empty argv", mode.label)
				}
				joined := strings.Join(args, "\x00")
				if !strings.Contains(joined, sys) {
					t.Errorf("%s: argv missing the system prompt: %v", mode.label, args)
				}
				if !strings.Contains(joined, user) {
					t.Errorf("%s: argv missing the user message: %v", mode.label, args)
				}
				for _, a := range args {
					for _, banned := range sessionFlags {
						if a == banned {
							t.Errorf("%s: argv carries session flag %q (one-shot contract): %v", mode.label, banned, args)
						}
					}
				}
			}

			// Env hygiene: every extra entry is a well-formed KEY=VALUE with a
			// non-empty key, and PATH is never overridden (the harness must stay
			// resolvable the way the user installed it).
			for _, inv := range []Invocation{{}, {Thinking: "off"}, {Thinking: "high"}} {
				for _, kv := range h.Env(inv) {
					i := strings.IndexByte(kv, '=')
					if i <= 0 {
						t.Errorf("Env(%+v): malformed entry %q (want KEY=VALUE)", inv, kv)
						continue
					}
					if key := kv[:i]; key == "PATH" {
						t.Errorf("Env(%+v): must not override PATH", inv)
					}
				}
			}

			// Capabilities consistency: Tools ⇒ ToolTransport succeeds, writes
			// ONLY into the given dir, and returns a non-empty attachment argv.
			if h.Capabilities().Tools {
				dir := t.TempDir()
				files, argv, err := h.ToolTransport(Invocation{SelfExe: "/path/to/ai-playbook"}, "/tmp/tools.sock", dir)
				if err != nil {
					t.Fatalf("Capabilities().Tools is set but ToolTransport failed: %v", err)
				}
				if len(argv) == 0 {
					t.Error("ToolTransport returned an empty attachment argv")
				}
				if len(files) == 0 {
					t.Error("ToolTransport reported no written files")
				}
				for _, f := range files {
					rel, rerr := filepath.Rel(dir, f)
					if rerr != nil || strings.HasPrefix(rel, "..") {
						t.Errorf("ToolTransport wrote outside its dir: %s (dir %s)", f, dir)
					}
					if _, serr := os.Stat(f); serr != nil {
						t.Errorf("ToolTransport reported %s but it does not exist: %v", f, serr)
					}
				}
			}
		})
	}
}

// TestHarnessContract_ClaudeRow pins claude's own contract values: the labels,
// the FULL tier, the defaults row (triage "haiku", thinking "medium", model
// harness-default), and the mcp-config transport attachment shape.
func TestHarnessContract_ClaudeRow(t *testing.T) {
	h, ok := harnessFor("claude")
	if !ok {
		t.Fatal("claude harness not registered")
	}
	if got := h.DisplayName(); got != "Claude Code" {
		t.Errorf("DisplayName = %q, want Claude Code", got)
	}
	if !h.Capabilities().Tools {
		t.Error("claude must be a FULL harness (Tools)")
	}
	d := HarnessDefaults("claude")
	if d != (Defaults{Model: "", TriageModel: "haiku", Thinking: "medium"}) {
		t.Errorf("claude defaults row = %+v, want {\"\", haiku, medium}", d)
	}

	dir := t.TempDir()
	files, argv, err := h.ToolTransport(Invocation{SelfExe: "/path/to/ai-playbook"}, "/tmp/tools.sock", dir)
	if err != nil {
		t.Fatalf("ToolTransport: %v", err)
	}
	if len(argv) != 2 || argv[0] != "--mcp-config" || argv[1] != files[0] {
		t.Errorf("claude transport argv = %v, want [--mcp-config %s]", argv, files[0])
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("read transport artifact: %v", err)
	}
	for _, want := range []string{`"mcpServers"`, `"ai-playbook"`, `"/path/to/ai-playbook"`, `"mcp"`, `"/tmp/tools.sock"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("mcp-config missing %s:\n%s", want, b)
		}
	}

	// SelfExe is load-bearing for the transport: without it claude has no way to
	// point its MCP wiring back at us, so the transport must fail loudly.
	if _, _, err := h.ToolTransport(Invocation{}, "/tmp/tools.sock", t.TempDir()); err == nil {
		t.Error("ToolTransport with no SelfExe must fail (never a silent broken config)")
	}
}

// TestHarnessResolution pins the config→harness resolution helpers the launcher
// and the no-backend messages share: the default selection (claude), the raw
// name fallback for an unknown harness, and the bin resolution (cfg [agent].bin
// else the harness name — the same rule RunHarnessEvents applies).
func TestHarnessResolution(t *testing.T) {
	if got := HarnessDisplayName(nil); got != "Claude Code" {
		t.Errorf("HarnessDisplayName(nil) = %q, want Claude Code (the default harness)", got)
	}
	cfg := config.Default()
	if got := HarnessDisplayName(cfg); got != "Claude Code" {
		t.Errorf("HarnessDisplayName(default cfg) = %q, want Claude Code", got)
	}
	cfg.Agent.Harness = "someharness"
	if got := HarnessDisplayName(cfg); got != "someharness" {
		t.Errorf("HarnessDisplayName(unknown) = %q, want the raw configured name", got)
	}
	if _, err := ConfiguredHarness(cfg); err == nil || !strings.Contains(err.Error(), "someharness") {
		t.Errorf("ConfiguredHarness(unknown) error = %v, want it to name the harness", err)
	}

	if got := HarnessBin(nil); got != "claude" {
		t.Errorf("HarnessBin(nil) = %q, want claude (the default harness name)", got)
	}
	cfg = config.Default()
	if got := HarnessBin(cfg); got != "claude" {
		t.Errorf("HarnessBin(default cfg) = %q, want claude", got)
	}
	cfg.Agent.Bin = "/opt/claude-custom"
	if got := HarnessBin(cfg); got != "/opt/claude-custom" {
		t.Errorf("HarnessBin(bin override) = %q, want the explicit bin", got)
	}
	cfg = config.Default()
	cfg.Agent.Harness = "pi"
	if got := HarnessBin(cfg); got != "pi" {
		t.Errorf("HarnessBin(harness pi) = %q, want the harness name", got)
	}
}

// TestRegisterHarness_DuplicatePanics pins the registry's duplicate guard: a
// second registration under an existing name PANICS (the http.Handle
// convention) instead of silently shadowing the earlier harness for the whole
// process — the exact hazard an H2/H3 init (or a careless test fake) could
// introduce. A UNIQUE name registers fine (the fake-harness test seam relies
// on that).
func TestRegisterHarness_DuplicatePanics(t *testing.T) {
	// A unique name registers without incident.
	RegisterHarness("dup-guard-probe", claudeHarness{}, Defaults{})
	if _, ok := harnessFor("dup-guard-probe"); !ok {
		t.Fatal("unique registration must land in the registry")
	}

	// Re-registering the SAME name must panic, naming the harness.
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("duplicate harness registration must panic")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "dup-guard-probe") {
			t.Errorf("panic message %v should name the duplicate harness", r)
		}
	}()
	RegisterHarness("dup-guard-probe", claudeHarness{}, Defaults{})
}

// TestWriteToolTransport pins the shared wiring helper: a private dir per call,
// the harness's attachment argv, and a cleanup that removes everything.
func TestWriteToolTransport(t *testing.T) {
	h, _ := harnessFor("claude")
	argv, cleanup, err := WriteToolTransport(h, "/path/to/ai-playbook", "/tmp/tools.sock")
	if err != nil {
		t.Fatalf("WriteToolTransport: %v", err)
	}
	if len(argv) != 2 || argv[0] != "--mcp-config" {
		t.Fatalf("argv = %v, want [--mcp-config <path>]", argv)
	}
	if _, err := os.Stat(argv[1]); err != nil {
		t.Fatalf("transport artifact missing: %v", err)
	}
	cleanup()
	if _, err := os.Stat(argv[1]); !os.IsNotExist(err) {
		t.Errorf("cleanup did not remove the transport artifact %s", argv[1])
	}
	cleanup() // idempotent, never panics
}
