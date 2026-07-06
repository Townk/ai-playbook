package author

import (
	"fmt"
	"os"
	"sync"

	"github.com/Townk/ai-playbook/internal/config"
)

// Harness is the per-harness seam RunHarnessEvents drives — the ADR-0012
// capability contract. Config selects WHICH harness ([agent].harness); the
// Harness owns that harness's process argv, the agentstream adapter that parses
// its stdout, any extra process env, its human label, its capability tier, and
// its tool transport (the artifact + argv that attach our socket-backed tools to
// the invocation). Keeping all of that behind this interface keeps prompt
// assembly (system/user message construction, the tool-instruction fold)
// harness-free: a Harness never sees a *config.Config, only the already-resolved
// Invocation. Registration happens in each harness's own file (registerHarness
// from an init), so harness-agnostic code never names a concrete harness.
type Harness interface {
	// Argv builds the owned process argv for the final systemPrompt + userMessage
	// and the resolved per-call knobs.
	Argv(systemPrompt, userMessage string, inv Invocation) []string
	// AdapterName names the agentstream adapter that parses this harness's stdout.
	AdapterName() string
	// Env returns extra KEY=VALUE entries appended to the harness process env.
	Env(inv Invocation) []string
	// DisplayName is the human label ("Claude Code", "pi", "Cursor") used by the
	// streaming UI header and error strings.
	DisplayName() string
	// Capabilities describes the harness's tier (ADR-0012): FULL when Tools is
	// set, BASIC otherwise. Streaming a final answer is required of every
	// harness, so it is not a flag.
	Capabilities() Capabilities
	// ToolTransport writes the harness's tool-transport artifact(s) into dir
	// (claude: the mcp-config JSON) and returns the written file paths plus the
	// argv additions that attach them to the invocation ("--mcp-config <path>").
	// Called only when Capabilities().Tools and the caller wants tools; the
	// launcher never writes transport artifacts itself. inv carries the resolved
	// knobs a transport may need (SelfExe — the running ai-playbook binary the
	// transport points the harness back at).
	ToolTransport(inv Invocation, socketPath, dir string) (files []string, argv []string, err error)
}

// Capabilities is a Harness's tier descriptor (ADR-0012): Tools reports a
// schema-enforced tool loop plus a transport reaching our socket backend — what
// submit_playbook (structured output), run, ask, and remember ride on. A harness
// without it is BASIC: authoring degrades to the text path with a visible note.
type Capabilities struct {
	Tools bool
}

// Invocation carries the resolved per-call knobs a Harness needs, decoupled from
// AuthorOptions/config so a Harness implementation stays free of config types.
// RunHarnessEvents resolves these (model override, thinking preference, …) before
// handing them to the harness.
type Invocation struct {
	// Model is the resolved model id (cfg [agent].Model, or the per-call override);
	// empty means "harness default".
	Model string
	// ToolArgv, when non-empty, is the tool-transport argv addition returned by
	// Harness.ToolTransport (claude: ["--mcp-config", <path>]); the harness
	// splices it into the owned argv to wire the tools backend in.
	ToolArgv []string
	// Bare selects the stripped quick-model CLASSIFY invocation.
	Bare bool
	// Thinking is the resolved reasoning preference ("off" when NoThinking forced it).
	Thinking string
	// SelfExe is the running ai-playbook binary (os.Executable), resolved by the
	// caller. Only ToolTransport call sites set it — the transport points the
	// harness's tool wiring back at `<SelfExe> mcp --socket <path>` (or the
	// harness's equivalent).
	SelfExe string
	// Bin is the harness's resolved executable (HarnessBin), set on the
	// ToolTransport Invocation so a transport that needs to RUN the CLI at
	// wire-time can — cursor's isolation guard runs `<Bin> mcp list`/`status`
	// under the redirect. Empty means "no CLI available" (the CLI-free unit
	// contract path); a transport must then skip any live probe.
	Bin string
	// ToolDir is the per-invocation transport root (the WriteToolTransport temp
	// dir). Harnesses whose tool attachment is a REDIRECTED CONFIG ROOT rather
	// than an argv flag read it from Env — cursor returns HOME=<ToolDir> so
	// cursor-agent resolves its MCP config from the pristine root we populated.
	// Empty on tool-less invocations (classify/metadata/text), so Env stays
	// clean there.
	ToolDir string
}

// Defaults is a harness's per-harness config-default row (ADR-0012 decision 4):
// the values [agent] model / triage_model / thinking resolve to when the user
// left them unset. Explicit config values always win; these are consulted only
// for empty fields, where cfg meets harnessFor.
type Defaults struct {
	Model       string
	TriageModel string
	Thinking    string
	// Bin is the executable [agent].bin resolves to when unset — set ONLY when
	// the harness's CLI binary is not named after its registry row (cursor: the
	// CLI installs as `agent`/`cursor-agent`, never `cursor`). Empty means the
	// registry name IS the binary name (claude, pi).
	Bin string
}

// harnessRegistration pairs a Harness with its config-defaults row.
type harnessRegistration struct {
	h Harness
	d Defaults
}

// harnessRegistry maps a configured harness name to its implementation +
// defaults, guarded by harnessRegistryMu. Populated by registerHarness from
// each harness file's init, so the registry itself stays free of concrete
// harness names.
var (
	harnessRegistryMu sync.RWMutex
	harnessRegistry   = map[string]harnessRegistration{}
)

// registerHarness records a shipped harness under its config name. Each harness
// file (harness_claude.go; pi/cursor are additive later) calls it from init.
// A DUPLICATE name panics (the http.Handle convention): two registrations for
// one name is always a programming error, and silently shadowing the earlier
// harness would corrupt every resolution for the rest of the process.
func registerHarness(name string, h Harness, d Defaults) {
	harnessRegistryMu.Lock()
	defer harnessRegistryMu.Unlock()
	if _, dup := harnessRegistry[name]; dup {
		panic("author: duplicate harness registration for " + name)
	}
	harnessRegistry[name] = harnessRegistration{h: h, d: d}
}

// RegisterHarness is the exported registration seam for TESTS that drive the
// launcher through a fake harness (e.g. the BASIC-tier degradation suite, which
// lives outside this package). Shipped harnesses register in their own files via
// registerHarness; production code never calls this. Like registerHarness it is
// init/test-setup-only and panics on a duplicate name — pick a unique fake name
// (and register it once, e.g. behind a sync.Once).
func RegisterHarness(name string, h Harness, d Defaults) {
	registerHarness(name, h, d)
}

// harnessFor resolves a configured harness name to its implementation. The bool
// is false for an unknown/not-yet-shipped harness, letting RunHarnessEvents
// return a clear error instead of silently falling back to the default — the A5c
// fix (config selection is honored on EVERY path, not just the events path).
func harnessFor(name string) (Harness, bool) {
	harnessRegistryMu.RLock()
	defer harnessRegistryMu.RUnlock()
	r, ok := harnessRegistry[name]
	return r.h, ok
}

// HarnessDefaults returns the per-harness config-defaults row for name (the
// zero Defaults for an unknown name). Resolution rule: an explicit [agent]
// value always wins; only empty fields fall through to this row.
func HarnessDefaults(name string) Defaults {
	harnessRegistryMu.RLock()
	defer harnessRegistryMu.RUnlock()
	return harnessRegistry[name].d
}

// resolveHarnessName resolves the effective harness name: cfg [agent].harness,
// else the compiled-in default selection (defaultHarnessName, owned by the
// default harness's own file so this file stays harness-agnostic).
func resolveHarnessName(cfg *config.Config) string {
	if cfg != nil && cfg.Agent.Harness != "" {
		return cfg.Agent.Harness
	}
	return defaultHarnessName
}

// ConfiguredHarness resolves cfg's [agent].harness selection to its Harness
// implementation, with the same clear failure RunHarnessEvents reports for an
// unknown name. It is the launcher's entry point to the capability contract
// (DisplayName, Capabilities, ToolTransport) ahead of the invocation itself.
func ConfiguredHarness(cfg *config.Config) (Harness, error) {
	name := resolveHarnessName(cfg)
	h, ok := harnessFor(name)
	if !ok {
		return nil, fmt.Errorf("harness %q not yet supported", name)
	}
	return h, nil
}

// HarnessBin resolves the executable the configured harness runs as: cfg
// [agent].bin when set, else the harness's own default bin (the Defaults.Bin
// row, for a harness whose binary is not named after its registry row —
// cursor's CLI installs as `cursor-agent`), else the harness name looked up on
// PATH — the SAME resolution RunHarnessEvents uses for the real invocation,
// shared so no-backend messages (validate's AI-review skip note, the
// drift-regen note) and the debug env probe name the binary that would
// actually be launched.
func HarnessBin(cfg *config.Config) string {
	if cfg != nil && cfg.Agent.Bin != "" {
		return cfg.Agent.Bin
	}
	name := resolveHarnessName(cfg)
	if d := HarnessDefaults(name); d.Bin != "" {
		return d.Bin
	}
	return name
}

// HarnessDisplayName returns the configured harness's human label (its
// DisplayName), falling back to the raw configured name when the harness is
// unknown — an error path label is better than an empty header.
func HarnessDisplayName(cfg *config.Config) string {
	name := resolveHarnessName(cfg)
	if h, ok := harnessFor(name); ok {
		return h.DisplayName()
	}
	return name
}

// WriteToolTransport is the shared transport-wiring step: it creates a private
// per-invocation dir, asks h to write its transport artifact(s) into it, and
// returns the argv addition, the dir (some harnesses redirect the harness's
// config ROOT at it via Env — cursor sets HOME=<dir>; claude/pi ignore it), and
// a cleanup that removes the dir. The cleanup is always safe to call. bin is the
// harness's resolved executable, handed to the transport so a wire-time
// isolation guard can run the CLI (cursor); pass "" when no CLI is available.
// Callers gate on h.Capabilities().Tools — asking a BASIC harness for a
// transport is a caller bug and surfaces as ToolTransport's error.
func WriteToolTransport(h Harness, selfExe, bin, socketPath string) (argv []string, dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "ai-playbook-transport-")
	if err != nil {
		return nil, "", func() {}, err
	}
	_, argv, err = h.ToolTransport(Invocation{SelfExe: selfExe, Bin: bin}, socketPath, dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, "", func() {}, err
	}
	return argv, dir, func() { os.RemoveAll(dir) }, nil
}
