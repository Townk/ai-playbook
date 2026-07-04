package reengage

// coverage_test.go — the re-engagement coverage + edge-case tests moved out of
// internal/orchestrator with the ADR-0009 step-2 split. They exercise the text
// fallback / event-error / no-agent branches, the pure helpers (StripPreamble /
// PlaybookName / playbookSlug / dataRoot), buildFrontMatter, and DriftRegen.

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
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
	body := "# My Playbook Title\n\nsome content\n"
	if got := PlaybookName(body); got != "My Playbook Title" {
		t.Errorf("PlaybookName plain H1 = %q, want %q", got, "My Playbook Title")
	}
}

func TestPlaybookName_NoHeading(t *testing.T) {
	body := "just prose, no markdown heading here\n"
	if got := PlaybookName(body); got != "" {
		t.Errorf("PlaybookName no heading = %q, want empty", got)
	}
}

// ── playbookSlug ──────────────────────────────────────────────────────────────

func TestPlaybookSlug_FallbackPlaybook(t *testing.T) {
	if got := playbookSlug("no heading here", ""); got != "playbook" {
		t.Errorf("playbookSlug with no title/hash = %q, want playbook", got)
	}
}

func TestPlaybookSlug_FallbackCtxHash(t *testing.T) {
	if got := playbookSlug("no heading here", "abc123"); got != "abc123" {
		t.Errorf("playbookSlug with hash = %q, want abc123", got)
	}
}

// ── Reengage.dataRoot ─────────────────────────────────────────────────────────

func TestDataRoot_FallsBackToDefaultRoot(t *testing.T) {
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

// ── FinalPlaybook ─────────────────────────────────────────────────────────────

func TestFinalPlaybook_NoReengage(t *testing.T) {
	e := New(nil, nil)
	_, _, _, err := e.FinalPlaybook("", "change", nil)
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("FinalPlaybook without Reengage: err = %v, want ErrNotImplemented", err)
	}
}

func TestFinalPlaybook_TextFallback(t *testing.T) {
	// Events is nil → text Agent path: FinalPlaybookText is called.
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	fa := &fakeAgent{canned: "# Playbook — Clean\nbody\n"}
	e := New(&Reengage{
		Req:   sampleReq(),
		Agent: fa.agent,
		// Events deliberately nil — exercises the text fallback.
	}, nil)

	stream, activity, mode, err := e.FinalPlaybook("", "the resolved troubleshoot", nil)
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
	e := New(&Reengage{
		Req: sampleReq(),
		// Agent deliberately nil; Events deliberately nil.
	}, nil)
	_, _, _, err := e.FinalPlaybook("", "change", nil)
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
	e := New(&Reengage{
		Req:    sampleReq(),
		Events: errorEvents(), // returns error → fall through
		Agent:  fa.agent,
	}, nil)

	stream, activity, mode, err := e.FinalPlaybook("", "change", nil)
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
	e := New(&Reengage{
		Req:    sampleReq(),
		Events: errorEvents(), // returns error → fall through to text Agent
		Agent:  fa.agent,
	}, nil)

	stream, activity, mode, err := e.Regenerate(nil)
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
	e := New(&Reengage{
		Req:    sampleReq(),
		Events: errorEvents(),
		// Agent deliberately nil.
	}, nil)
	_, _, _, err := e.Regenerate(nil)
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Regenerate event-error + no agent: err = %v, want ErrNotImplemented", err)
	}
}

// Regenerate re-store is a no-op when the cache/keys are present but the produced
// body is whitespace-only (the restore guard checks TrimSpace(body) == "").
func TestRegenerate_ReStoreSkippedOnEmptyBody(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_PLAYBOOK_DATA_DIR", root)
	fa := &fakeAgent{canned: "   \n"}
	c := cache.Open()
	e := New(&Reengage{
		Req:     sampleReq(),
		Agent:   fa.agent,
		Cache:   c,
		CtxHash: "ctxhash2",
		ReqHash: "reqhash2",
	}, nil)

	stream, _, _, err := e.Regenerate(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(stream)
	_ = stream.Close()

	entry := filepath.Join(root, "cache", "ctxhash2", "reqhash2.md")
	if _, err := os.Stat(entry); !os.IsNotExist(err) {
		t.Errorf("re-store should be skipped for whitespace body (err=%v)", err)
	}
}

// ── Followup ──────────────────────────────────────────────────────────────────

func TestFollowup_EventErrorFallsToText(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	fa := &fakeAgent{canned: "# Revised fix\n"}
	e := New(&Reengage{
		Req:    sampleReq(),
		Events: errorEvents(),
		Agent:  fa.agent,
	}, nil)

	const failedOut = "ld: symbol not found"
	stream, activity, mode, err := e.Followup(failedOut, nil)
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
	e := New(&Reengage{
		Req:    sampleReq(),
		Events: errorEvents(),
		// Agent deliberately nil.
	}, nil)
	_, _, _, err := e.Followup("failed output", nil)
	if !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Followup event-error + no agent: err = %v, want ErrNotImplemented", err)
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
	e := New(&Reengage{Req: sampleReq(), Agent: errAgent}, nil)
	_, _, _, err := e.Regenerate(nil)
	if err == nil {
		t.Error("Regenerate with failing text agent should return error")
	}
}

func TestFollowup_TextAgentError(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	e := New(&Reengage{Req: sampleReq(), Agent: errAgent}, nil)
	_, _, _, err := e.Followup("failed output", nil)
	if err == nil {
		t.Error("Followup with failing text agent should return error")
	}
}

func TestFinalPlaybook_TextAgentError(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", t.TempDir())
	e := New(&Reengage{Req: sampleReq(), Agent: errAgent}, nil)
	_, _, _, err := e.FinalPlaybook("", "change", nil)
	if err == nil {
		t.Error("FinalPlaybook with failing text agent should return error")
	}
}

// ── CommitPlaybook slug + env edge cases ──────────────────────────────────────

func TestCommitPlaybook_PlainH1Slug(t *testing.T) {
	root := t.TempDir()
	e := New(&Reengage{Req: sampleReq(), DataRoot: root}, nil)

	body := "# My Plain Title\n\nbody content\n"
	path, err := e.CommitPlaybook(body)
	if err != nil {
		t.Fatalf("CommitPlaybook: %v", err)
	}
	wantPath := filepath.Join(root, "playbooks", "my-plain-title.md")
	if path != wantPath {
		t.Errorf("saved path = %q, want %q", path, wantPath)
	}
}

func TestCommitPlaybook_NilEnvLookup(t *testing.T) {
	root := t.TempDir()
	e := New(&Reengage{
		Req:       sampleReq(),
		DataRoot:  root,
		EnvLookup: nil, // nil lookup — no values captured
	}, nil)
	body := "# Playbook — NilLookup\n\nUse $MY_VAR.\n"
	path, err := e.CommitPlaybook(body)
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

// TestCommitPlaybook_HonorsStoreDir asserts CommitPlaybook writes under
// Reengage.StoreDir when set, NOT under dataRoot/playbooks.
func TestCommitPlaybook_HonorsStoreDir(t *testing.T) {
	storeDir := t.TempDir()
	dataRoot := t.TempDir()

	e := New(&Reengage{
		StoreDir:  storeDir,
		DataRoot:  dataRoot,
		Req:       capture.Request{},
		EnvLookup: func(string) (string, bool) { return "", false },
	}, nil)

	body := "# Playbook — StoreDir Test\n\nVerify the injected store dir is used.\n"
	path, err := e.CommitPlaybook(body)
	if err != nil {
		t.Fatalf("CommitPlaybook: %v", err)
	}
	if !strings.HasPrefix(path, storeDir) {
		t.Errorf("CommitPlaybook path = %q, want prefix %q", path, storeDir)
	}
	badDir := filepath.Join(dataRoot, "playbooks")
	if strings.HasPrefix(path, badDir) {
		t.Errorf("CommitPlaybook used dataRoot fallback: path = %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("returned path does not exist: %v", err)
	}
}

// TestCommitPlaybook_NoStoreDir_FallsBackToDataRoot asserts the back-compat path:
// when StoreDir is empty, CommitPlaybook writes under dataRoot/playbooks.
func TestCommitPlaybook_NoStoreDir_FallsBackToDataRoot(t *testing.T) {
	dataRoot := t.TempDir()
	e := New(&Reengage{
		DataRoot:  dataRoot,
		Req:       capture.Request{},
		EnvLookup: func(string) (string, bool) { return "", false },
	}, nil)

	body := "# Playbook — Fallback Test\n\nVerify the dataRoot fallback.\n"
	path, err := e.CommitPlaybook(body)
	if err != nil {
		t.Fatalf("CommitPlaybook: %v", err)
	}
	wantPrefix := filepath.Join(dataRoot, "playbooks")
	if !strings.HasPrefix(path, wantPrefix) {
		t.Errorf("CommitPlaybook path = %q, want prefix %q", path, wantPrefix)
	}
}

// ── buildFrontMatter ──────────────────────────────────────────────────────────

// buildFrontMatter does not write a workdir field (removed in the dead-code sweep;
// portability is via PROJECT_ROOT instead).
func TestBuildFrontMatter_NoWorkdir(t *testing.T) {
	home, _ := os.UserHomeDir()
	projRoot := filepath.Join(home, "Projects", "myapp")
	re := &Reengage{
		Req: capture.Request{
			ProjectRoot: projRoot,
			UserRequest: "fix the build",
		},
		EnvLookup: func(string) (string, bool) { return "", false },
		Metadata:  nil,
	}
	body := "# Playbook — Fix Build\n\nDo the thing.\n"
	fm := re.buildFrontMatter(body)
	assembled := frontmatter.Assemble(fm)
	if strings.Contains(assembled, "workdir:") {
		t.Errorf("assembled FM must not carry a workdir: key:\n%s", assembled)
	}
}

func TestBuildFrontMatter_ProjectBound(t *testing.T) {
	re := &Reengage{
		Req:      capture.Request{},
		Metadata: func(string) (PlaybookMeta, error) { return PlaybookMeta{Description: "d", ProjectBound: true}, nil },
	}
	fm := re.buildFrontMatter("# Playbook — T\n\n```bash {id=fix}\nx\n```\n")
	if !fm.ProjectBound {
		t.Fatalf("buildFrontMatter must copy ProjectBound from the seam meta")
	}
	if fm.Description != "d" {
		t.Fatalf("description = %q, want d", fm.Description)
	}
}

// buildFrontMatter injects PROJECT_ROOT into the env map when the metadata seam
// returns ProjectBound: true.
func TestBuildFrontMatter_DeclaresProjectRoot(t *testing.T) {
	re := &Reengage{
		Req: capture.Request{},
		Metadata: func(string) (PlaybookMeta, error) {
			return PlaybookMeta{ProjectBound: true}, nil
		},
	}
	fm := re.buildFrontMatter("# Playbook — T\n\n```bash {id=fix}\ncd $PROJECT_ROOT\n```\n")
	if _, ok := fm.Env["PROJECT_ROOT"]; !ok {
		t.Fatalf("project_bound front matter must declare PROJECT_ROOT, got env=%v", fm.Env)
	}
}

// ── DriftRegen ────────────────────────────────────────────────────────────────

// fixedTarget is a drift-target resolver that always resolves to <dir>/x.txt — the
// executor's DriftTargetPath is injected as this seam so reengage never imports the
// orchestrator.
func fixedTarget(dir string) func(string) (string, error) {
	return func(string) (string, error) { return filepath.Join(dir, "x.txt"), nil }
}

// DriftRegen reads the current file, calls Events with KindReengageDriftRegen and the
// current content as base, and returns the fresh diff text emitted by the stub.
func TestDriftRegen_DrainsFreshDiff(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := "--- a/x.txt\n+++ b/x.txt\n@@ -1 +1 @@\n-current\n+fixed\n"
	e := New(&Reengage{
		Events: func(kind ReengageKind, base, change string, constraints []string) (<-chan agentstream.Event, func() error, error) {
			if kind != KindReengageDriftRegen {
				t.Fatalf("wrong kind %v", kind)
			}
			if !strings.Contains(base, "current") {
				t.Fatalf("base lacks current file content: %q", base)
			}
			ch := make(chan agentstream.Event, 1)
			ch <- agentstream.Event{Kind: agentstream.Final, Text: fresh}
			close(ch)
			return ch, func() error { return nil }, nil
		},
	}, fixedTarget(dir))
	stalePatch := "--- a/x.txt\n+++ b/x.txt\n@@ -1 +1 @@\n-stale\n+fixed\n"
	got, err := e.DriftRegen(stalePatch, nil)
	if err != nil {
		t.Fatalf("DriftRegen returned error: %v", err)
	}
	if strings.TrimSpace(got) != strings.TrimSpace(fresh) {
		t.Fatalf("DriftRegen = %q; want %q", got, fresh)
	}
}

// DriftRegen threads constraints through to the injected EventsFunc verbatim
// (refuse-solution spec §1: constraints reach ALL four kinds).
func TestDriftRegen_ThreadsConstraints(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"no docker", "no sudo"}
	var got []string
	e := New(&Reengage{
		Events: func(kind ReengageKind, base, change string, constraints []string) (<-chan agentstream.Event, func() error, error) {
			if kind != KindReengageDriftRegen {
				t.Fatalf("wrong kind %v", kind)
			}
			got = constraints
			ch := make(chan agentstream.Event, 1)
			ch <- agentstream.Event{Kind: agentstream.Final, Text: "--- a/x.txt\n+++ b/x.txt\n@@ -1 +1 @@\n-current\n+fixed\n"}
			close(ch)
			return ch, func() error { return nil }, nil
		},
	}, fixedTarget(dir))
	stalePatch := "--- a/x.txt\n+++ b/x.txt\n@@ -1 +1 @@\n-stale\n+fixed\n"
	if _, err := e.DriftRegen(stalePatch, want); err != nil {
		t.Fatalf("DriftRegen returned error: %v", err)
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("EventsFunc received constraints %q; want %q", got, want)
	}
}

// DriftRegen strips a wrapping ```diff ... ``` code fence from the model's output.
func TestDriftRegen_StripsFencedOutput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("current\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := "--- a/x.txt\n+++ b/x.txt\n@@ -1 +1 @@\n-current\n+fixed\n"
	fenced := "```diff\n" + fresh + "```"
	e := New(&Reengage{
		Events: func(kind ReengageKind, base, change string, constraints []string) (<-chan agentstream.Event, func() error, error) {
			if kind != KindReengageDriftRegen {
				t.Fatalf("wrong kind %v", kind)
			}
			ch := make(chan agentstream.Event, 1)
			ch <- agentstream.Event{Kind: agentstream.Final, Text: fenced}
			close(ch)
			return ch, func() error { return nil }, nil
		},
	}, fixedTarget(dir))
	stalePatch := "--- a/x.txt\n+++ b/x.txt\n@@ -1 +1 @@\n-stale\n+fixed\n"
	got, err := e.DriftRegen(stalePatch, nil)
	if err != nil {
		t.Fatalf("DriftRegen returned error: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(got), "```") || strings.HasSuffix(strings.TrimSpace(got), "```") {
		t.Fatalf("DriftRegen must strip wrapping code fence, got %q; want %q", got, fresh)
	}
	if !strings.Contains(got, "--- a/x.txt") || !strings.Contains(got, "+++ b/x.txt") || !strings.Contains(got, "+fixed") {
		t.Fatalf("DriftRegen stripped too much: got %q; want content like %q", got, fresh)
	}
}
