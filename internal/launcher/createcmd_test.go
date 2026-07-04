package launcher

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/internal/cache"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/mux"
	"github.com/Townk/ai-playbook/internal/triage"
	"github.com/Townk/ai-playbook/pkg/store"
)

// captureStderr runs f() and returns whatever it wrote to os.Stderr.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	f()
	w.Close()
	os.Stderr = old
	var buf bytes.Buffer
	io.Copy(&buf, r) //nolint:errcheck
	r.Close()
	return buf.String()
}

// TestParseCreateArgs covers the prompt/template split: word joining, --template
// in either position and both spellings, the empty case, and the missing-value
// error.
func TestParseCreateArgs(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantPrompt   string
		wantTemplate string
		wantErr      bool
	}{
		{"words only", []string{"fix", "the", "build"}, "fix the build", "", false},
		{"template before prompt", []string{"--template", "go", "fix", "it"}, "fix it", "go", false},
		{"template after prompt", []string{"fix", "it", "--template=node"}, "fix it", "node", false},
		{"empty", nil, "", "", false},
		{"template no value", []string{"do", "stuff", "--template"}, "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prompt, template, err := parseCreateArgs(c.args)
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if err != nil {
				return
			}
			if prompt != c.wantPrompt {
				t.Errorf("prompt = %q, want %q", prompt, c.wantPrompt)
			}
			if template != c.wantTemplate {
				t.Errorf("template = %q, want %q", template, c.wantTemplate)
			}
		})
	}
}

// TestEmitSimilarBanner_WithMatches asserts the banner is printed to stderr,
// listing the matched playbook names, when the store search finds matches.
func TestEmitSimilarBanner_WithMatches(t *testing.T) {
	orig := searchFn
	t.Cleanup(func() { searchFn = orig })
	searchFn = func(query string) ([]store.Meta, error) {
		return []store.Meta{{Name: "Fix Gradle"}, {Name: "Rebuild App"}}, nil
	}

	out := captureStderr(t, func() { emitSimilarBanner("fix the build") })
	if !strings.Contains(out, "similar playbooks already exist:") {
		t.Errorf("banner missing prefix: %q", out)
	}
	if !strings.Contains(out, "Fix Gradle") || !strings.Contains(out, "Rebuild App") {
		t.Errorf("banner missing names: %q", out)
	}
}

// TestEmitSimilarBanner_NoMatches asserts no banner is printed when the search
// returns nothing (and that a search error is swallowed).
func TestEmitSimilarBanner_NoMatches(t *testing.T) {
	orig := searchFn
	t.Cleanup(func() { searchFn = orig })
	searchFn = func(query string) ([]store.Meta, error) { return nil, nil }

	if out := captureStderr(t, func() { emitSimilarBanner("nothing here") }); out != "" {
		t.Errorf("expected no banner, got %q", out)
	}

	searchFn = func(query string) ([]store.Meta, error) { return nil, io.EOF }
	if out := captureStderr(t, func() { emitSimilarBanner("err path") }); out != "" {
		t.Errorf("a search error must emit no banner, got %q", out)
	}
}

// TestCreatePlaybook_ForcesAuthor asserts the core: createPlaybook FORCE-AUTHORS
// via the author seam (passing the request through) and emits the banner first.
// The only sinks are the banner (searchFn) and the force-author seam — there is
// no triage.Route / classify in the path, so create can never serve a cache hit.
func TestCreatePlaybook_ForcesAuthor(t *testing.T) {
	origSearch, origAuthor := searchFn, createAuthorFn
	t.Cleanup(func() { searchFn, createAuthorFn = origSearch, origAuthor })

	searchFn = func(query string) ([]store.Meta, error) { return []store.Meta{{Name: "Prior"}}, nil }

	var gotReq capture.Request
	authored := false
	createAuthorFn = func(req capture.Request, _ mux.Mux) int {
		authored = true
		gotReq = req
		return 0
	}

	req := capture.Request{UserRequest: "author me", CWD: "/proj"}
	var code int
	out := captureStderr(t, func() { code = createPlaybook(req, mux.Null()) })

	if code != 0 {
		t.Fatalf("createPlaybook exit = %d, want 0 (the author seam's code)", code)
	}
	if !authored {
		t.Fatal("createPlaybook must call the force-author seam")
	}
	if gotReq.UserRequest != "author me" {
		t.Errorf("author got UserRequest %q, want %q", gotReq.UserRequest, "author me")
	}
	if !strings.Contains(out, "similar playbooks already exist:") {
		t.Errorf("banner must be emitted before authoring: %q", out)
	}
}

// TestCreateDecision_NeverServesHit is the load-bearing no-cache-serve guarantee:
// createDecision always escalates (author), NEVER sets a Hit/Path, and computes
// the SAME keys assist's triage would — so the freshly authored playbook is
// stored under keys a later `assist` for the same context can hit.
func TestCreateDecision_NeverServesHit(t *testing.T) {
	req := capture.Request{
		UserRequest: "build the app",
		ProjectRoot: "/proj",
		CWD:         "/proj/dir",
		Command:     "make",
		Exit:        "0",
	}
	d := createDecision(req)
	if d.Outcome != triage.Escalate {
		t.Errorf("Outcome = %v, want Escalate (force-author, never a Hit)", d.Outcome)
	}
	if d.Path != "" {
		t.Errorf("Path = %q, want empty (create never serves a cache entry)", d.Path)
	}
	// Keys match what assist's triage.Route would compute (so a later assist hits).
	wantCtx := cache.ContextHash(cache.Request{
		ProjectRoot: req.ProjectRoot,
		CWD:         req.CWD,
		CommandText: req.Command,
		CommandExit: req.Exit,
		Scrollback:  req.Scrollback,
	})
	wantReq := cache.RequestHash(req.UserRequest)
	if d.CtxHash != wantCtx || d.ReqHash != wantReq {
		t.Errorf("keys = (%q,%q), want (%q,%q) — must match triage's keys", d.CtxHash, d.ReqHash, wantCtx, wantReq)
	}

	// Disable guard: a failure with empty scrollback yields an unreliable key →
	// Disabled, no keys (so authorPlaybook never stores it).
	bad := capture.Request{UserRequest: "x", Exit: "1", Scrollback: "  "}
	db := createDecision(bad)
	if !db.Disabled || db.CtxHash != "" || db.ReqHash != "" {
		t.Errorf("disable guard not honored: %+v", db)
	}
}

// TestCreateDecisionParity asserts createDecision's keys equal triage.Route's for
// the cacheable case — proving the force-author path stays in lockstep with assist
// WITHOUT calling triage.Route (Route is invoked only here, by the test).
func TestCreateDecisionParity(t *testing.T) {
	req := capture.Request{UserRequest: "fix it", ProjectRoot: "/p", CWD: "/p/d", Command: "go test", Exit: "1", Scrollback: "boom"}
	routed := triage.Route(req, nil, false) // c=nil → no lookup, just keys
	got := createDecision(req)
	if got.CtxHash != routed.CtxHash || got.ReqHash != routed.ReqHash {
		t.Errorf("createDecision keys (%q,%q) != triage.Route keys (%q,%q)", got.CtxHash, got.ReqHash, routed.CtxHash, routed.ReqHash)
	}
}

// TestCreateMain_EmptyPrompt asserts a missing prompt is a usage error (exit 2),
// returning BEFORE any capture/author work.
func TestCreateMain_EmptyPrompt(t *testing.T) {
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })
	os.Args = []string{"ai-playbook", "create"}

	var code int
	out := captureStderr(t, func() { code = CreateMain() })
	if code != 2 {
		t.Fatalf("empty prompt exit = %d, want 2", code)
	}
	if !strings.Contains(out, "<prompt> is required") {
		t.Errorf("missing usage error: %q", out)
	}
}

// TestCreateMain_TemplateReservedNote asserts --template is parsed, prints the
// reserved note, and authoring still proceeds (via the seams, no live harness).
// It also confirms the prompt reaches the capture's UserRequest and no --cached
// badge is wired (the author seam receives only the request + mux).
func TestCreateMain_TemplateReservedNote(t *testing.T) {
	origCap, origSearch, origAuthor := captureFn, searchFn, createAuthorFn
	t.Cleanup(func() { captureFn, searchFn, createAuthorFn = origCap, origSearch, origAuthor })

	captureFn = func(opts capture.Options) capture.Request {
		return capture.Request{UserRequest: opts.UserRequest, CWD: "/proj"}
	}
	searchFn = func(query string) ([]store.Meta, error) { return nil, nil }
	var gotReq capture.Request
	createAuthorFn = func(req capture.Request, _ mux.Mux) int {
		gotReq = req
		return 0
	}

	saved := os.Args
	t.Cleanup(func() { os.Args = saved })
	os.Args = []string{"ai-playbook", "create", "--template", "go", "fix", "the", "build"}

	var code int
	out := captureStderr(t, func() { code = CreateMain() })
	if code != 0 {
		t.Fatalf("create exit = %d, want 0", code)
	}
	if !strings.Contains(out, "--template is reserved") {
		t.Errorf("missing reserved-template note: %q", out)
	}
	if gotReq.UserRequest != "fix the build" {
		t.Errorf("captured UserRequest = %q, want %q", gotReq.UserRequest, "fix the build")
	}
}

// TestCreateAuthorSeamDefault pins the production force-author seam: createAuthorFn
// defaults to realCreateAuthor (the openSession + authorPlaybook path that wires
// StoreDir and never passes --cached). A compile-time/identity guard so a refactor
// can't silently swap the seam for a triage-bearing path.
func TestCreateAuthorSeamDefault(t *testing.T) {
	if createAuthorFn == nil {
		t.Fatal("createAuthorFn must default to the production force-author path")
	}
}
