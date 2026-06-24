package triage

import (
	"testing"

	"ai-playbook/cache"
	"ai-playbook/capture"
)

func TestRoute_Hit(t *testing.T) {
	dir := t.TempDir()
	c := &cache.Cache{Root: dir}

	req := capture.Request{
		ProjectRoot: "/p",
		Command:     "make",
		Exit:        "1",
		Scrollback:  "boom: error\n",
		UserRequest: "fix make",
	}
	cr := cache.Request{ProjectRoot: "/p", CommandText: "make", CommandExit: "1", Scrollback: "boom: error\n"}
	ctx := cache.ContextHash(cr)
	reqHash := cache.RequestHash("fix make")
	if _, err := c.Store(ctx, reqHash, "playbook", "# fix\n", nil, ""); err != nil {
		t.Fatal(err)
	}

	d := Route(req, c, false)
	if d.Outcome != Hit {
		t.Fatalf("outcome = %s, want hit (%s)", d.Outcome, d.Reason)
	}
	if d.CtxHash != ctx || d.ReqHash != reqHash {
		t.Fatalf("hashes wrong: %+v", d)
	}
	if _, ok := c.Lookup(ctx, reqHash); !ok || d.Path == "" {
		t.Fatalf("path missing: %q", d.Path)
	}
}

func TestRoute_Miss(t *testing.T) {
	dir := t.TempDir()
	c := &cache.Cache{Root: dir}
	req := capture.Request{ProjectRoot: "/p", UserRequest: "anything", Exit: "0"}
	d := Route(req, c, false)
	if d.Outcome != Escalate {
		t.Fatalf("outcome = %s, want escalate", d.Outcome)
	}
	if d.CtxHash == "" || d.ReqHash == "" {
		t.Fatalf("miss should still compute keys: %+v", d)
	}
	if d.Disabled {
		t.Fatal("miss should not be disabled")
	}
}

func TestRoute_CacheDisableGuard(t *testing.T) {
	dir := t.TempDir()
	c := &cache.Cache{Root: dir}

	// Failure (exit != 0) with EMPTY scrollback → cache disabled, escalate, and
	// keys cleared (never looked up). Even if an entry happened to exist at the
	// collapsed key, it must NOT be served.
	req := capture.Request{
		ProjectRoot: "/p",
		Command:     "make",
		Exit:        "1",
		Scrollback:  "   \n\t ", // whitespace only
		UserRequest: "fix make",
	}
	d := Route(req, c, false)
	if d.Outcome != Escalate {
		t.Fatalf("outcome = %s, want escalate", d.Outcome)
	}
	if !d.Disabled {
		t.Fatal("guard should mark Disabled")
	}
	if d.CtxHash != "" || d.ReqHash != "" {
		t.Fatalf("disabled cache must clear keys: %+v", d)
	}
}

func TestRoute_FailureWithScrollbackNotDisabled(t *testing.T) {
	dir := t.TempDir()
	c := &cache.Cache{Root: dir}
	req := capture.Request{ProjectRoot: "/p", Command: "make", Exit: "1", Scrollback: "real error", UserRequest: "q"}
	d := Route(req, c, false)
	if d.Disabled {
		t.Fatal("failure WITH scrollback must not disable cache")
	}
	if d.Outcome != Escalate { // miss, but keys valid
		t.Fatalf("outcome = %s", d.Outcome)
	}
	if d.CtxHash == "" {
		t.Fatal("keys should be computed")
	}
}

func TestRoute_NoCacheForcesMiss(t *testing.T) {
	dir := t.TempDir()
	c := &cache.Cache{Root: dir}
	req := capture.Request{ProjectRoot: "/p", Exit: "0", UserRequest: "q"}
	ctx := cache.ContextHash(cache.Request{ProjectRoot: "/p"})
	reqHash := cache.RequestHash("q")
	c.Store(ctx, reqHash, "answer", "x", nil, "")
	d := Route(req, c, true)
	if d.Outcome != Escalate {
		t.Fatalf("no-cache must force escalate even with an entry, got %s", d.Outcome)
	}
}
