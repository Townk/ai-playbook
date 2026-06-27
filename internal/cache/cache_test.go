package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Golden values captured from the real shell `ai-assist-cache` (sha256), so the
// Go port is verified byte-for-byte against the original behavior.

func TestRequestHash_Golden(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		// sha256("hello world") — confirms surrounding-whitespace trim.
		{"basic_trim", "  hello world  ", "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"},
		// sha256("") — empty after trim.
		{"empty", "", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		// "\n\tfix the bug\n " trims to "fix the bug".
		{"newlines_tabs", "\n\tfix the bug\n ", "280fd7e3571b7c850d6fe771aee96db0ccdeef47910414e3a4a93bb06ea9f7c2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RequestHash(c.in); got != c.want {
				t.Fatalf("RequestHash(%q) = %s, want %s", c.in, got, c.want)
			}
		})
	}
}

func TestRequestHash_TrimEquivalence(t *testing.T) {
	// "fix the bug" with no surrounding whitespace hashes the same as the
	// padded form above.
	if RequestHash("fix the bug") != RequestHash("\n\tfix the bug\n ") {
		t.Fatal("trim not equivalent")
	}
}

func TestContextHash_Golden(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want string
	}{
		{
			"non_failure_exit0",
			Request{ProjectRoot: "/home/u/proj", CWD: "/home/u/proj", CommandExit: "0"},
			"f6e6bcc4cd93dc722ca900d38fe8722d19bb64f1ac763c1997e60cb5b5d5e16f",
		},
		{
			"absent_exit",
			Request{ProjectRoot: "/home/u/proj"},
			"f6e6bcc4cd93dc722ca900d38fe8722d19bb64f1ac763c1997e60cb5b5d5e16f",
		},
		{
			"failure_with_ansi_scrollback",
			Request{
				ProjectRoot: "/home/u/proj",
				CommandText: "make build",
				CommandExit: "2",
				Scrollback:  "\n\n  hello \x1b[31mworld\x1b[0m  \n\n\n  bye  \n\n",
			},
			"3ef1cbed6270b4bea4b645e0dafbc27e07a29002227430352ac4fb13b8ec0618",
		},
		{
			"failure_root_falls_back_to_cwd",
			Request{CWD: "/home/u/proj", CommandText: "make build", CommandExit: "2", Scrollback: "boom"},
			"f2d3d0c8f2ffb83cf64845e432c84309937d5017cd06e15deda02c716e48986e",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ContextHash(c.req); got != c.want {
				t.Fatalf("ContextHash = %s, want %s", got, c.want)
			}
		})
	}
}

func TestContextHash_ExitZeroVsAbsentIdentical(t *testing.T) {
	a := ContextHash(Request{ProjectRoot: "/p", CommandText: "ls", CommandExit: "0", Scrollback: "x"})
	b := ContextHash(Request{ProjectRoot: "/p"})
	if a != b {
		t.Fatalf("exit-0 and absent must key on project only: %s != %s", a, b)
	}
}

func TestNormalizeScrollback_Golden(t *testing.T) {
	// od -c of the real shell output was:
	//   "  hello world\n\n  bye\n"
	// (leading spaces preserved, trailing trimmed, leading/trailing blanks
	// dropped, blank run collapsed to one, ANSI stripped).
	in := "\n\n  hello \x1b[31mworld\x1b[0m  \n\n\n  bye  \n\n"
	want := "  hello world\n\n  bye\n"
	if got := NormalizeScrollback(in); got != want {
		t.Fatalf("NormalizeScrollback = %q, want %q", got, want)
	}
}

func TestNormalizeScrollback_Empty(t *testing.T) {
	if got := NormalizeScrollback(""); got != "" {
		t.Fatalf("empty in → %q", got)
	}
	if got := NormalizeScrollback("\n\n  \n\t\n"); got != "" {
		t.Fatalf("all-blank → %q", got)
	}
}

func TestStoreLookupRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := &Cache{Root: dir}

	ctx := ContextHash(Request{ProjectRoot: "/p", CommandText: "make", CommandExit: "1", Scrollback: "err"})
	req := RequestHash("why did make fail")
	body := "# Playbook\n\nstep one\n"
	extras := map[string]string{"harness": "claude", "project_name": "proj"}
	reqJSON := `{"version":1,"user_request":"why did make fail"}`

	// Before store: lookup misses.
	if _, ok := c.Lookup(ctx, req); ok {
		t.Fatal("expected miss before store")
	}

	entry, err := c.Store(ctx, req, "playbook", body, extras, reqJSON)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(dir, "cache", ctx, req+".md")
	if entry != wantPath {
		t.Fatalf("entry path = %s, want %s", entry, wantPath)
	}

	// After store: lookup hits, returns the same path.
	got, ok := c.Lookup(ctx, req)
	if !ok || got != wantPath {
		t.Fatalf("lookup after store: ok=%v path=%s", ok, got)
	}

	// Sidecar present.
	side, ok := c.RequestFile(ctx, req)
	if !ok {
		t.Fatal("sidecar missing")
	}
	sideData, _ := os.ReadFile(side)
	if string(sideData) != reqJSON {
		t.Fatalf("sidecar = %q", string(sideData))
	}

	// Entry content: front matter + body, body stripped correctly.
	raw, _ := os.ReadFile(entry)
	content := string(raw)
	if !strings.HasPrefix(content, "---\nschema: ai-playbook-cache/v1\n") {
		t.Fatalf("front matter prefix wrong:\n%s", content)
	}
	if k, ok := Field(content, "kind"); !ok || k != "playbook" {
		t.Fatalf("kind field = %q ok=%v", k, ok)
	}
	if h, ok := Field(content, "harness"); !ok || h != "claude" {
		t.Fatalf("harness field = %q ok=%v", h, ok)
	}
	if Body(content) != body {
		t.Fatalf("Body = %q, want %q", Body(content), body)
	}
}

func TestBody_NoFrontMatter(t *testing.T) {
	s := "just a plain command\n"
	if Body(s) != s {
		t.Fatalf("Body of plain content changed: %q", Body(s))
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Setenv("AI_PLAYBOOK_DATA_DIR", "/custom/root")
	if DefaultRoot() != "/custom/root" {
		t.Fatalf("AI_PLAYBOOK_DATA_DIR not honored: %s", DefaultRoot())
	}
	t.Setenv("AI_PLAYBOOK_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", "/xdg")
	if DefaultRoot() != "/xdg/ai-playbook" {
		t.Fatalf("XDG_DATA_HOME not honored: %s", DefaultRoot())
	}
}
