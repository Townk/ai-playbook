package author

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/draft"
	"github.com/Townk/ai-playbook/internal/harnesstest"
	"github.com/Townk/ai-playbook/internal/tools"
	"github.com/Townk/ai-playbook/pkg/driver"
)

// piLiveTimeout bounds one live pi call: the characterization probes completed
// in ~3-8s; minutes of headroom cover a slow provider without hanging CI.
const piLiveTimeout = 3 * time.Minute

// runPiLive drives RunHarnessEvents against the REAL pi CLI (skipped when it is
// not installed) with a tiny thinking-off prompt, from an empty working dir so
// append mode cannot pick up this repo's context files. It returns the drained
// events and wait()'s error.
func runPiLive(t *testing.T, bare bool) ([]agentstream.Event, error) {
	t.Helper()
	harnesstest.RequireHarness(t, "pi")

	cfg := config.Default()
	cfg.Agent.Harness = "pi"
	dir := t.TempDir()
	events, wait, err := RunHarnessEvents(
		"You are a test probe. Always reply with exactly: ok",
		"Reply with exactly: ok",
		AuthorOptions{
			Cfg:        cfg,
			Bare:       bare,
			NoThinking: true,
			Timeout:    piLiveTimeout,
			// The seam only pins the working directory; CommandContext keeps the
			// Timeout able to kill a stalled process (the seam's ctx contract).
			Command: func(ctx context.Context, bin string, args []string) *exec.Cmd {
				cmd := exec.CommandContext(ctx, bin, args...)
				cmd.Dir = dir
				return cmd
			},
		},
	)
	if err != nil {
		t.Fatalf("RunHarnessEvents: %v", err)
	}
	var drained []agentstream.Event
	for e := range events {
		drained = append(drained, e)
	}
	return drained, wait()
}

// assertPiFinal asserts the live stream terminated with a Final event carrying
// the probe answer — the BASIC floor every harness owes (a parseable terminal
// event with the full final text).
func assertPiFinal(t *testing.T, events []agentstream.Event, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no events from the live pi stream")
	}
	last := events[len(events)-1]
	if last.Kind != agentstream.Final {
		t.Fatalf("last event = %+v, want the Final (agent_end is pi's terminal envelope)", last)
	}
	if !strings.Contains(strings.ToLower(last.Text), "ok") {
		t.Errorf("Final = %q, want the probe answer", last.Text)
	}
}

// TestPiLive_BareFinalEvent is the live argv-composition probe for the BARE
// (classify-shaped) flag set: --system-prompt + --no-context-files --no-skills
// --no-prompt-templates --no-tools must compose on the installed CLI and the
// stream must end in a Final event.
func TestPiLive_BareFinalEvent(t *testing.T) {
	events, err := runPiLive(t, true)
	assertPiFinal(t, events, err)
}

// TestPiLive_AppendFinalEvent is the live argv-composition probe for the
// AUTHORING (append) flag set: --append-system-prompt + the shared base flags
// must compose on the installed CLI and the stream must end in a Final event.
func TestPiLive_AppendFinalEvent(t *testing.T) {
	events, err := runPiLive(t, false)
	assertPiFinal(t, events, err)
}

// TestPiLive_ToolLoopSubmitPlaybook is the FULL-tier acceptance test: the real
// pi CLI loads the embedded extension (ToolTransport artifact), the model calls
// submit_playbook, the extension forwards it over the unix socket to a REAL
// tools backend (real driver, real tools.Server), and the backend's OnPlaybook
// receives a draft.Validate-clean playbook — the whole structured-authoring
// tool loop, end to end, on the live harness.
func TestPiLive_ToolLoopSubmitPlaybook(t *testing.T) {
	harnesstest.RequireHarness(t, "pi")

	// A real tools backend: minimal controlled zsh (no user rc), temp KB root,
	// and an OnPlaybook capture.
	zdot := t.TempDir()
	if err := os.WriteFile(filepath.Join(zdot, ".zshrc"), []byte("# minimal rc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := driver.Open(driver.Options{Shell: "zsh", Env: append(os.Environ(), "ZDOTDIR="+zdot)})
	if err != nil {
		t.Fatalf("driver.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	var mu sync.Mutex
	var got *draft.Playbook
	// Short socket path: unix sun_path is ~104 bytes on darwin; a nested
	// t.TempDir() path can overflow it.
	sockDir, err := os.MkdirTemp("", "pisock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	socket := filepath.Join(sockDir, "t.sock")
	srv, err := tools.Serve(socket, tools.Deps{
		Driver:      d,
		ProjectRoot: t.TempDir(),
		KBRoot:      t.TempDir(),
		OnPlaybook: func(pb draft.Playbook) {
			mu.Lock()
			defer mu.Unlock()
			got = &pb
		},
	})
	if err != nil {
		t.Fatalf("tools.Serve: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	h, ok := harnessFor("pi")
	if !ok {
		t.Fatal("pi harness not registered")
	}
	// SelfExe empty on purpose: pi's transport dials the socket directly.
	argv, cleanup, err := WriteToolTransport(h, "", socket)
	if err != nil {
		t.Fatalf("WriteToolTransport: %v", err)
	}
	t.Cleanup(cleanup)

	cfg := config.Default()
	cfg.Agent.Harness = "pi"
	workDir := t.TempDir()
	events, wait, err := RunHarnessEvents(
		"You are a test probe with no shell access beyond the provided tools.",
		"Submit a playbook via the submit_playbook tool with: title 'Say hello', "+
			"one section headed 'Steps' whose content is a single item "+
			"{kind:\"code\", lang:\"bash\", code:\"echo hello\"}, and meta "+
			"{description:\"Print hello\", project_bound:false}. "+
			"Do not use the run or ask tools; submit_playbook is your only action.",
		AuthorOptions{
			Cfg:        cfg,
			ToolArgv:   argv,
			Structured: true,
			NoThinking: true,
			Timeout:    piLiveTimeout,
			Command: func(ctx context.Context, bin string, args []string) *exec.Cmd {
				cmd := exec.CommandContext(ctx, bin, args...)
				cmd.Dir = workDir
				return cmd
			},
		},
	)
	if err != nil {
		t.Fatalf("RunHarnessEvents: %v", err)
	}
	sawSubmitActivity := false
	for e := range events {
		if e.Kind == agentstream.ToolActivity && strings.Contains(e.Text, "submit_playbook") {
			sawSubmitActivity = true
		}
	}
	if err := wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if got == nil {
		t.Fatal("the backend never received a submit_playbook (the tool loop did not close)")
	}
	if !sawSubmitActivity {
		t.Error("no submit_playbook ToolActivity event streamed")
	}
	if got.Title != "Say hello" {
		t.Errorf("playbook title = %q, want 'Say hello'", got.Title)
	}
	if len(got.Sections) == 0 {
		t.Fatal("playbook has no sections")
	}
	if got.Meta.Description == "" {
		t.Error("playbook meta.description is empty")
	}
}
