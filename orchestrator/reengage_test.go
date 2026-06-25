package orchestrator

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ai-playbook/author"
	"ai-playbook/cache"
	"ai-playbook/capture"
)

// fakeAgent records the system prompt it was called with and returns a canned
// stream. It is the injected author.Agent for the re-engagement tests.
type fakeAgent struct {
	gotSystem string
	canned    string
	calls     int
}

func (f *fakeAgent) agent(systemPrompt, userMessage string) (io.ReadCloser, error) {
	f.calls++
	f.gotSystem = systemPrompt
	return io.NopCloser(strings.NewReader(f.canned)), nil
}

func sampleReq() capture.Request {
	return capture.Request{
		Kind:        "error",
		Command:     "make build",
		Exit:        "2",
		Scrollback:  "make: *** Error 2",
		UserRequest: "fix my build",
		ProjectRoot: "/home/me/proj",
		Project:     capture.Project{Name: "proj"},
	}
}

// Regenerate returns the fake agent's fresh stream (ModeReplace) and was called
// with the standard authoring prompt (cache-bypassed re-author).
func TestRegenerate_StreamsAndMode(t *testing.T) {
	t.Setenv("AI_ASSIST_DATA_DIR", t.TempDir()) // no KB folded in
	fa := &fakeAgent{canned: "# Fresh playbook\n"}
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:   sampleReq(),
		Agent: fa.agent,
	})

	stream, _, mode, err := o.Regenerate()
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeReplace {
		t.Errorf("mode = %v, want ModeReplace", mode)
	}
	got, _ := io.ReadAll(stream)
	stream.Close()
	if string(got) != fa.canned {
		t.Errorf("stream = %q, want canned", got)
	}
	if fa.calls != 1 {
		t.Errorf("agent calls = %d, want 1", fa.calls)
	}
	if fa.gotSystem != author.SystemPrompt(sampleReq(), "") {
		t.Errorf("regenerate did not use the standard authoring prompt")
	}
}

// Regenerate re-stores the fresh playbook under the original keys when the cache +
// keys are present (matching ai-assist-regenerate's re-store).
func TestRegenerate_ReStoresFreshPlaybook(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_ASSIST_DATA_DIR", root)
	fa := &fakeAgent{canned: "# Regenerated body\n"}
	c := cache.Open()
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:     sampleReq(),
		Agent:   fa.agent,
		Cache:   c,
		CtxHash: "ctxhash",
		ReqHash: "reqhash",
	})

	stream, _, _, err := o.Regenerate()
	if err != nil {
		t.Fatal(err)
	}
	// The re-store fires on Close (after the full body is read).
	_, _ = io.ReadAll(stream)
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}

	entry := filepath.Join(root, "cache", "ctxhash", "reqhash.md")
	b, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("fresh playbook was not re-stored: %v", err)
	}
	if !strings.Contains(string(b), "Regenerated body") {
		t.Errorf("re-stored entry missing the fresh body:\n%s", b)
	}
}

// Followup returns the fake stream (ModeAppend) and was called with a prompt that
// includes the failed output.
func TestFollowup_StreamsWithFailedOutput(t *testing.T) {
	fa := &fakeAgent{canned: "# Revised fix\n"}
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:   sampleReq(),
		Agent: fa.agent,
	})

	const failed = "ld: symbol not found"
	stream, _, mode, err := o.Followup(failed)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeAppend {
		t.Errorf("mode = %v, want ModeAppend", mode)
	}
	got, _ := io.ReadAll(stream)
	stream.Close()
	if string(got) != fa.canned {
		t.Errorf("stream = %q, want canned", got)
	}
	if !strings.Contains(fa.gotSystem, failed) {
		t.Errorf("followup prompt missing the failed output %q", failed)
	}
}

// CommitPlaybook (stage 3 / spec §E) cache-REPLACES this request's entry with the
// finalized body AND saves it to <DataRoot>/playbooks/<slug>.md, the slug derived
// from the `# Playbook — <title>` heading.
func TestCommitPlaybook_CacheReplaceAndFileSave(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AI_ASSIST_DATA_DIR", root)
	c := cache.Open()
	// Seed a stale cached troubleshoot so we can assert the commit REPLACES it.
	if _, err := c.Store("ctxhash", "reqhash", "playbook", "# stale troubleshoot\n", nil, ""); err != nil {
		t.Fatal(err)
	}

	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:         sampleReq(),
		Cache:       c,
		CtxHash:     "ctxhash",
		ReqHash:     "reqhash",
		RequestJSON: `{"command":"make build"}`,
		DataRoot:    root,
	})

	body := "# Playbook — Compile an Android Project\n\nSet up the SDK.\n"
	path, err := o.CommitPlaybook(body)
	if err != nil {
		t.Fatalf("CommitPlaybook: %v", err)
	}

	// (1) Cache entry REPLACED with the final body (stale content gone).
	entry := filepath.Join(root, "cache", "ctxhash", "reqhash.md")
	got, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("read cache entry: %v", err)
	}
	if !strings.Contains(string(got), "Compile an Android Project") {
		t.Errorf("cache entry not replaced with the final body:\n%s", got)
	}
	if strings.Contains(string(got), "stale troubleshoot") {
		t.Errorf("cache entry still holds the stale body:\n%s", got)
	}

	// (2) File saved at playbooks/<slug>.md with the body; slug from the title.
	wantPath := filepath.Join(root, "playbooks", "compile-an-android-project.md")
	if path != wantPath {
		t.Errorf("saved path = %q, want %q", path, wantPath)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved playbook: %v", err)
	}
	// The saved asset is now FM + body: a leading ---…--- front-matter block with the
	// programmatic name, followed by the body unchanged.
	if !strings.HasPrefix(string(saved), "---\n") {
		t.Errorf("saved file should begin with a front-matter block:\n%s", saved)
	}
	if !strings.Contains(string(saved), "name: Compile an Android Project") {
		t.Errorf("saved FM missing the name field:\n%s", saved)
	}
	if !strings.HasSuffix(string(saved), body) {
		t.Errorf("saved file should end with the body unchanged:\n%s", saved)
	}
}

// CommitPlaybook is graceful when the cache keys are absent (an unkeyed / cache-
// disabled request): it skips the cache-replace (no entry to replace) but STILL saves
// the .md file, falling back to the context hash for the slug when there's no title.
func TestCommitPlaybook_NoKeysSavesFileOnly(t *testing.T) {
	root := t.TempDir()
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
		Req:      sampleReq(),
		CtxHash:  "fallbackctx", // present so the slug can fall back to it
		DataRoot: root,
		// No Cache / ReqHash → cache-replace skipped.
	})

	body := "no title here, just prose\n"
	path, err := o.CommitPlaybook(body)
	if err != nil {
		t.Fatalf("CommitPlaybook: %v", err)
	}
	// No cache dir should have been created (cache-replace was skipped).
	if _, err := os.Stat(filepath.Join(root, "cache")); !os.IsNotExist(err) {
		t.Errorf("cache dir should not exist when keys absent (err=%v)", err)
	}
	// Slug falls back to the context hash when there's no title.
	wantPath := filepath.Join(root, "playbooks", "fallbackctx.md")
	if path != wantPath {
		t.Errorf("saved path = %q, want %q (ctx-hash slug fallback)", path, wantPath)
	}
	if saved, _ := os.ReadFile(path); !strings.HasSuffix(string(saved), body) ||
		!strings.HasPrefix(string(saved), "---\n") {
		t.Errorf("saved file should be FM + body, got %q", saved)
	}
}

// CommitPlaybook errors on an empty body (nothing to commit) and without a Reengage.
func TestCommitPlaybook_EmptyAndNoReengage(t *testing.T) {
	o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{DataRoot: t.TempDir()})
	if _, err := o.CommitPlaybook("   \n"); err == nil {
		t.Error("CommitPlaybook with an empty body should error")
	}
	bare := New(newTestDriver(t), &recMux{})
	if _, err := bare.CommitPlaybook("# Playbook — x\n"); err == nil {
		t.Error("CommitPlaybook without Reengage should error")
	}
}

// CommitPlaybook strips any preamble above the H1 before saving + caching, and is
// idempotent on a body that already starts at the H1.
func TestCommitPlaybook_StripsPreambleIdempotent(t *testing.T) {
	clean := "# Playbook — Compile an Android Project\n\nSet up the SDK.\n"

	commit := func(t *testing.T, body string) (savedPath, root string) {
		t.Helper()
		root = t.TempDir()
		t.Setenv("AI_ASSIST_DATA_DIR", root)
		c := cache.Open()
		o := New(newTestDriver(t), &recMux{}).WithReengage(&Reengage{
			Req:         sampleReq(),
			Cache:       c,
			CtxHash:     "ctxhash",
			ReqHash:     "reqhash",
			RequestJSON: `{"command":"make build"}`,
			DataRoot:    root,
		})
		p, err := o.CommitPlaybook(body)
		if err != nil {
			t.Fatalf("CommitPlaybook: %v", err)
		}
		return p, root
	}

	// (a) Preamble above the H1 is stripped from both the saved file and the cache.
	withPreamble := "preamble prose above the title\nmore preamble\n\n" + clean
	path, root := commit(t, withPreamble)
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// The saved asset is FM + the stripped body: the body portion (after the FM)
	// equals clean, with no preamble surviving.
	if !strings.HasSuffix(string(saved), clean) {
		t.Errorf("saved body not stripped to the H1:\n got %q\nwant suffix %q", saved, clean)
	}
	if strings.Contains(string(saved), "preamble prose") {
		t.Errorf("saved body still has preamble:\n%s", saved)
	}
	entry, err := os.ReadFile(filepath.Join(root, "cache", "ctxhash", "reqhash.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(entry), "preamble prose") {
		t.Errorf("cached body still has preamble:\n%s", entry)
	}

	// (b) Idempotent: a body that already starts at the H1 is unchanged.
	path2, _ := commit(t, clean)
	saved2, err := os.ReadFile(path2)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(saved2), clean) {
		t.Errorf("already-clean body should be unchanged:\n got %q\nwant suffix %q", saved2, clean)
	}
}

// Without a Reengage wired the re-engagement methods return ErrNotImplemented.
func TestReengageMethods_NoReengage(t *testing.T) {
	o := New(newTestDriver(t), &recMux{})
	if _, _, _, err := o.Regenerate(); err == nil {
		t.Error("Regenerate without Reengage should error")
	}
	if _, _, _, err := o.Followup(""); err == nil {
		t.Error("Followup without Reengage should error")
	}
}
