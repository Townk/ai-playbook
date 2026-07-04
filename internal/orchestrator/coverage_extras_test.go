package orchestrator

// coverage_extras_test.go — targeted tests to lift internal/orchestrator coverage
// from ~80% toward ~90%.  These tests exercise:
//
//   • StripPreamble (exported wrapper)                     0% → 100%
//   • PlaybookName plain-H1 fallback + empty              80% → 100%
//   • playbookSlug "playbook" fallback                    83% → 100%
//   • Kind.String default case                            91% → 100%
//   • Do default kind                                     91% → 100%
//   • Reengage.dataRoot empty-DataRoot fallback           67% → 100%
//   • projectRoot nil-driver path                         75% → 100%
//   • writePatch trailing-newline addition                53% → 67%
//   • FinalPlaybook nil-Reengage + text-path + error      50% → 100%
//   • Regenerate  Events-error → text-path fallback       83% → 100%
//   • Followup    Events-error → text-path fallback       86% → 100%
//
// Untestable live shims (NOT covered here):
//   • writePatch OS-level write/close errors   (require injecting filesystem failures)
//   • viewDiff writePatch error path            (same OS-failure constraint)
//   • Real mux Float spawn + PTY signal paths  (live Zellij / interactive PTY)

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/cache"
)

// ── StripPreamble (exported wrapper) ────────────────────────────────────────

func TestStripPreamble_Exported(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"preamble stripped to H1",
			"some intro\nmore text\n\n# Title\nbody\n",
			"# Title\nbody\n",
		},
		{
			"already starts at H1 — idempotent",
			"# Title\nbody\n",
			"# Title\nbody\n",
		},
		{
			"no H1 — unchanged",
			"just prose, no heading\n",
			"just prose, no heading\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripPreamble(tc.in); got != tc.want {
				t.Errorf("StripPreamble(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// ── PlaybookName ─────────────────────────────────────────────────────────────

func TestPlaybookName_PlainH1Fallback(t *testing.T) {
	// A plain `# <title>` heading (no "Playbook —" prefix) is the fallback.
	body := "# My Playbook Title\n\nsome content\n"
	if got := PlaybookName(body); got != "My Playbook Title" {
		t.Errorf("PlaybookName plain H1 = %q, want %q", got, "My Playbook Title")
	}
}

func TestPlaybookName_NoHeading(t *testing.T) {
	// A body with no heading at all should return "".
	body := "just prose, no markdown heading here\n"
	if got := PlaybookName(body); got != "" {
		t.Errorf("PlaybookName no heading = %q, want empty", got)
	}
}

// ── playbookSlug ──────────────────────────────────────────────────────────────

func TestPlaybookSlug_FallbackPlaybook(t *testing.T) {
	// When both the title and ctxHash are empty, the slug falls back to "playbook".
	if got := playbookSlug("no heading here", ""); got != "playbook" {
		t.Errorf("playbookSlug with no title/hash = %q, want playbook", got)
	}
}

func TestPlaybookSlug_FallbackCtxHash(t *testing.T) {
	// When there is no title but a ctxHash, the hash is used.
	if got := playbookSlug("no heading here", "abc123"); got != "abc123" {
		t.Errorf("playbookSlug with hash = %q, want abc123", got)
	}
}

// ── Kind.String default case ─────────────────────────────────────────────────

func TestKindString_Unknown(t *testing.T) {
	if got := Kind(999).String(); got != "unknown" {
		t.Errorf("Kind(999).String() = %q, want unknown", got)
	}
}

// ── Do default case ──────────────────────────────────────────────────────────

func TestDo_DefaultKindNotImplemented(t *testing.T) {
	// An unrecognized Kind that falls through to the default: branch in Do.
	o := &Orchestrator{Mux: &recMux{}}
	_, err := o.Do(Action{Kind: Kind(999)})
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Do(Kind(999)) err = %v, want ErrNotImplemented", err)
	}
}

// ── Reengage.dataRoot ─────────────────────────────────────────────────────────

func TestDataRoot_FallsBackToDefaultRoot(t *testing.T) {
	// When DataRoot is empty, dataRoot() must return cache.DefaultRoot() (not "").
	re := &Reengage{DataRoot: ""}
	got := re.dataRoot()
	want := cache.DefaultRoot()
	if got != want {
		t.Errorf("dataRoot with empty DataRoot = %q, want cache.DefaultRoot() = %q", got, want)
	}
}

func TestDataRoot_UsesExplicit(t *testing.T) {
	re := &Reengage{DataRoot: "/my/data"}
	if got := re.dataRoot(); got != "/my/data" {
		t.Errorf("dataRoot = %q, want /my/data", got)
	}
}

// ── projectRoot ───────────────────────────────────────────────────────────────

func TestProjectRoot_NilDriverEnvVar(t *testing.T) {
	// nil Drv → fall through to AI_PLAYBOOK_PROJECT_ROOT.
	t.Setenv("AI_PLAYBOOK_PROJECT_ROOT", "/from/env")
	o := &Orchestrator{}
	if got := o.projectRoot(); got != "/from/env" {
		t.Errorf("projectRoot (nil driver, env set) = %q, want /from/env", got)
	}
}

func TestProjectRoot_NilDriverNoEnv(t *testing.T) {
	// nil Drv and unset env → empty string.
	t.Setenv("AI_PLAYBOOK_PROJECT_ROOT", "")
	o := &Orchestrator{}
	if got := o.projectRoot(); got != "" {
		t.Errorf("projectRoot (nil driver, no env) = %q, want empty", got)
	}
}

// ── writePatch ────────────────────────────────────────────────────────────────

func TestWritePatch_AddsTrailingNewline(t *testing.T) {
	// A diff without a trailing newline must get one appended.
	p, err := writePatch("no-trailing-newline")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "no-trailing-newline\n" {
		t.Errorf("writePatch added-newline content = %q, want trailing \\n", b)
	}
}

func TestWritePatch_EmptyDiff(t *testing.T) {
	// An empty diff results in a file containing just a newline.
	p, err := writePatch("")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "\n" {
		t.Errorf("writePatch empty diff content = %q, want \\n", b)
	}
}

// ── FinalPlaybook ─────────────────────────────────────────────────────────────

func TestFinalPlaybook_NoReengage(t *testing.T) {
	o := &Orchestrator{Mux: &recMux{}}
	_, _, _, err := o.FinalPlaybook("", "change", nil)
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("FinalPlaybook without Reengage: err = %v, want ErrNotImplemented", err)
	}
}

func TestFinalPlaybook_TextFallback(t *testing.T) {
	// Events is nil → text Agent path: FinalPlaybookText is called.
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	fa := &fakeAgent{canned: "# Playbook — Clean\nbody\n"}
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:   sampleReq(),
		Agent: fa.agent,
		// Events deliberately nil — exercises the text fallback.
	}}

	stream, activity, mode, err := o.FinalPlaybook("", "the resolved troubleshoot", nil)
	if err != nil {
		t.Fatalf("FinalPlaybook text path: %v", err)
	}
	if activity != nil {
		t.Error("text fallback must return nil activity channel")
	}
	if mode != ModeReplace {
		t.Errorf("mode = %v, want ModeReplace", mode)
	}
	got, _ := io.ReadAll(stream)
	_ = stream.Close()
	if string(got) != fa.canned {
		t.Errorf("FinalPlaybook text path stream = %q, want canned", got)
	}
	if fa.calls != 1 {
		t.Errorf("fakeAgent calls = %d, want 1", fa.calls)
	}
}

func TestFinalPlaybook_NoAgentNoEvents(t *testing.T) {
	// No Events and no Agent → ErrNotImplemented from the text path guard.
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req: sampleReq(),
		// Agent deliberately nil; Events deliberately nil.
	}}
	_, _, _, err := o.FinalPlaybook("", "change", nil)
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("FinalPlaybook no agent/events: err = %v, want ErrNotImplemented", err)
	}
}

// errorEvents returns an EventsFunc that always errors — used to trigger the
// fall-through to the text Agent path in Regenerate/Followup/FinalPlaybook.
func errorEvents() EventsFunc {
	return func(ReengageKind, string, string, []string) (<-chan agentstream.Event, func() error, error) {
		return nil, nil, errors.New("events producer: simulated start error")
	}
}

func TestFinalPlaybook_EventErrorFallsToText(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	fa := &fakeAgent{canned: "# Playbook — fallback\n"}
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:    sampleReq(),
		Events: errorEvents(), // returns error → fall through
		Agent:  fa.agent,
	}}

	stream, activity, mode, err := o.FinalPlaybook("", "change", nil)
	if err != nil {
		t.Fatalf("FinalPlaybook event-error→text: %v", err)
	}
	if activity != nil {
		t.Error("text fallback must return nil activity channel")
	}
	if mode != ModeReplace {
		t.Errorf("mode = %v, want ModeReplace", mode)
	}
	got, _ := io.ReadAll(stream)
	_ = stream.Close()
	if string(got) != fa.canned {
		t.Errorf("FinalPlaybook event-error stream = %q, want canned", got)
	}
}

// ── Regenerate ────────────────────────────────────────────────────────────────

func TestRegenerate_EventErrorFallsToText(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	fa := &fakeAgent{canned: "# Regenerated fallback\n"}
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:    sampleReq(),
		Events: errorEvents(), // returns error → fall through to text Agent
		Agent:  fa.agent,
	}}

	stream, activity, mode, err := o.Regenerate(nil)
	if err != nil {
		t.Fatalf("Regenerate event-error→text: %v", err)
	}
	if activity != nil {
		t.Error("text fallback must return nil activity channel")
	}
	if mode != ModeReplace {
		t.Errorf("mode = %v, want ModeReplace", mode)
	}
	got, _ := io.ReadAll(stream)
	_ = stream.Close()
	if string(got) != fa.canned {
		t.Errorf("Regenerate event-error stream = %q, want canned", got)
	}
	if fa.calls != 1 {
		t.Errorf("fakeAgent calls = %d, want 1", fa.calls)
	}
}

func TestRegenerate_EventErrorNoAgent(t *testing.T) {
	// Events returns error and no Agent → ErrNotImplemented.
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:    sampleReq(),
		Events: errorEvents(),
		// Agent deliberately nil.
	}}
	_, _, _, err := o.Regenerate(nil)
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Regenerate event-error + no agent: err = %v, want ErrNotImplemented", err)
	}
}

// ── Followup ──────────────────────────────────────────────────────────────────

func TestFollowup_EventErrorFallsToText(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	fa := &fakeAgent{canned: "# Revised fix\n"}
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:    sampleReq(),
		Events: errorEvents(),
		Agent:  fa.agent,
	}}

	const failedOut = "ld: symbol not found"
	stream, activity, mode, err := o.Followup(failedOut, nil)
	if err != nil {
		t.Fatalf("Followup event-error→text: %v", err)
	}
	if activity != nil {
		t.Error("text fallback must return nil activity channel")
	}
	if mode != ModeAppend {
		t.Errorf("mode = %v, want ModeAppend", mode)
	}
	got, _ := io.ReadAll(stream)
	_ = stream.Close()
	if string(got) != fa.canned {
		t.Errorf("Followup event-error stream = %q, want canned", got)
	}
}

func TestFollowup_EventErrorNoAgent(t *testing.T) {
	// Events returns error and no Agent → ErrNotImplemented.
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:    sampleReq(),
		Events: errorEvents(),
		// Agent deliberately nil.
	}}
	_, _, _, err := o.Followup("failed output", nil)
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Followup event-error + no agent: err = %v, want ErrNotImplemented", err)
	}
}

// ── ViewDiff with SpawnFloat error ────────────────────────────────────────────

func TestViewDiff_SpawnFloatError(t *testing.T) {
	// When SpawnFloat fails, viewDiff should propagate the error.
	rf := &recFloat{err: errors.New("mux: pane limit exceeded")}
	o := &Orchestrator{Mux: &recMux{}, Float: rf}
	_, err := o.Do(Action{Kind: KindViewDiff, ID: "x", Payload: "diff --git a/f b/f\n"})
	if err == nil {
		t.Error("viewDiff with SpawnFloat error should return error, got nil")
	}
}

// ── CommitPlaybook: slug via plain H1 + nil EnvLookup ─────────────────────────

// TestCommitPlaybook_PlainH1Slug verifies the slug/name derivation when the body
// has a plain `# Title` heading (no "Playbook —" prefix).
func TestCommitPlaybook_PlainH1Slug(t *testing.T) {
	root := t.TempDir()
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:      sampleReq(),
		DataRoot: root,
	}}

	body := "# My Plain Title\n\nbody content\n"
	path, err := o.CommitPlaybook(body)
	if err != nil {
		t.Fatalf("CommitPlaybook: %v", err)
	}
	wantPath := filepath.Join(root, "playbooks", "my-plain-title.md")
	if path != wantPath {
		t.Errorf("saved path = %q, want %q", path, wantPath)
	}
}

// TestCommitPlaybook_NilEnvLookup verifies that a nil EnvLookup (unknown env
// values) does not prevent the commit — the env map is simply empty for
// referenced vars (no value captured), per spec §C.
func TestCommitPlaybook_NilEnvLookup(t *testing.T) {
	root := t.TempDir()
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:       sampleReq(),
		DataRoot:  root,
		EnvLookup: nil, // nil lookup — no values captured
	}}
	body := "# Playbook — NilLookup\n\nUse $MY_VAR.\n"
	path, err := o.CommitPlaybook(body)
	if err != nil {
		t.Fatalf("CommitPlaybook nil EnvLookup: %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(saved), "---\n") {
		t.Errorf("saved file should begin with a front-matter block:\n%s", saved)
	}
}

// ── Regenerate: re-store skipped when body is whitespace-only ────────────────

func TestRegenerate_ReStoreSkippedOnEmptyBody(t *testing.T) {
	// If the cache/keys are present but the agent produces only whitespace, re-store
	// must be a no-op (the restore guard checks TrimSpace(body) == "").
	root := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", root)
	fa := &fakeAgent{canned: "   \n"}
	c := cache.Open()
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:     sampleReq(),
		Agent:   fa.agent,
		Cache:   c,
		CtxHash: "ctxhash2",
		ReqHash: "reqhash2",
	}}

	stream, _, _, err := o.Regenerate(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(stream)
	_ = stream.Close()

	// No cache entry should have been written for empty/whitespace body.
	entry := filepath.Join(root, "cache", "ctxhash2", "reqhash2.md")
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Errorf("re-store should be skipped for whitespace body (err=%v)", err)
	}
}

// ── Text agent error paths ────────────────────────────────────────────────────

// errAgent is an author.Agent that always returns an error, exercising the
// error-return branch in Regenerate/Followup/FinalPlaybook text paths.
func errAgent(_, _ string) (io.ReadCloser, error) {
	return nil, errors.New("agent: simulated quota exceeded")
}

func TestRegenerate_TextAgentError(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:   sampleReq(),
		Agent: errAgent,
	}}
	_, _, _, err := o.Regenerate(nil)
	if err == nil {
		t.Error("Regenerate with failing text agent should return error")
	}
}

func TestFollowup_TextAgentError(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:   sampleReq(),
		Agent: errAgent,
	}}
	_, _, _, err := o.Followup("failed output", nil)
	if err == nil {
		t.Error("Followup with failing text agent should return error")
	}
}

func TestFinalPlaybook_TextAgentError(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	o := &Orchestrator{Mux: &recMux{}, Reengage: &Reengage{
		Req:   sampleReq(),
		Agent: errAgent,
	}}
	_, _, _, err := o.FinalPlaybook("", "change", nil)
	if err == nil {
		t.Error("FinalPlaybook with failing text agent should return error")
	}
}
