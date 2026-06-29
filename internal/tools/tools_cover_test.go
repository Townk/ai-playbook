package tools

// Additional coverage tests for the internal/tools package.
// These complement tools_test.go by exercising the branches that the original
// suite left at 0 % or below the target (~90 %):
//
//   - dialError.Error   (0 %)
//   - Server.Close      idempotent double-close branch (77.8 %)
//   - Serve             net.Listen failure path (88.9 %)
//   - doRemember        kb.AppendTo error path (77.8 %)
//   - doAsk             Ask func returns error (87.5 %)
//   - handleConn        empty-line skip + bad-JSON error reply (76.9 %)
//   - Dial              DialTimeout failure, errNoReply, bad-JSON reply (63.2 %)

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Townk/ai-playbook/internal/floatinput"
)

// TestServer_CloseIdempotent exercises the s.closed guard: a second Close call
// must return nil without panicking or double-removing the socket.
func TestServer_CloseIdempotent(t *testing.T) {
	d := newTestDriver(t)
	dir, err := os.MkdirTemp("", "tsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "t.sock")
	srv, err := Serve(socket, Deps{Driver: d})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Errorf("Close() first: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Errorf("Close() second (idempotent): %v", err)
	}
}

// TestServe_ListenError covers the net.Listen failure branch in Serve (a valid
// driver but an unreachable socket path).
func TestServe_ListenError(t *testing.T) {
	d := newTestDriver(t)
	_, err := Serve("/nonexistent-parent/t.sock", Deps{Driver: d})
	if err == nil {
		t.Error("Serve with unrooted socket path should return an error")
	}
}

// TestServe_DoRememberError exercises the kb.AppendTo error path in doRemember.
// Placing a regular file at KBRoot/projects blocks os.MkdirAll, which causes
// AppendTo to return an error that must propagate as a reply.Error.
func TestServe_DoRememberError(t *testing.T) {
	d := newTestDriver(t)
	root := t.TempDir()
	// A regular file at root/projects makes os.MkdirAll(root/projects/<hash>/) fail.
	if err := os.WriteFile(filepath.Join(root, "projects"), []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}
	socket := serveTest(t, Deps{Driver: d, ProjectRoot: "/some/proj", KBRoot: root})

	res, err := Dial(socket, Call{Tool: "remember", Fact: "something important"})
	if err != nil {
		t.Fatalf("Dial remember: %v", err)
	}
	if res.Error == "" {
		t.Errorf("doRemember error path: want non-empty Error, got %+v", res)
	}
}

// TestServe_DoAskError exercises the branch in doAsk where the Ask func itself
// returns an error (e.g. the float backend failed to spawn). The reply must
// carry Unavailable=true and the error message from the seam.
func TestServe_DoAskError(t *testing.T) {
	d := newTestDriver(t)
	askErr := errors.New("float spawn failed")
	ask := func(floatinput.Request) (floatinput.Result, error) {
		return floatinput.Result{}, askErr
	}
	socket := serveTest(t, Deps{Driver: d, Ask: ask})

	res, err := Dial(socket, Call{Tool: "ask", Prompt: "which env?"})
	if err != nil {
		t.Fatalf("Dial ask: %v", err)
	}
	if !res.Unavailable {
		t.Errorf("doAsk error path: want Unavailable=true, got %+v", res)
	}
	if res.Error != askErr.Error() {
		t.Errorf("doAsk error = %q, want %q", res.Error, askErr.Error())
	}
}

// TestHandleConn_EmptyAndBadJSON exercises two branches in handleConn using a
// raw net.Conn so we can send frames the Dial helper would never produce:
//   - an empty line (len(line)==0 → continue, no reply)
//   - a non-JSON line → error reply
//
// A subsequent valid run request confirms the connection remains alive.
func TestHandleConn_EmptyAndBadJSON(t *testing.T) {
	d := newTestDriver(t)
	socket := serveTest(t, Deps{Driver: d})

	conn, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatalf("net.Dial: %v", err)
	}
	defer conn.Close()

	// Send: empty line (no reply expected), bad JSON (error reply), valid run.
	if _, err := fmt.Fprintln(conn, ""); err != nil {
		t.Fatalf("write empty line: %v", err)
	}
	if _, err := fmt.Fprintln(conn, "{not valid json}"); err != nil {
		t.Fatalf("write bad JSON: %v", err)
	}
	if _, err := fmt.Fprintln(conn, `{"tool":"run","cmd":"print -r -- conn-ok"}`); err != nil {
		t.Fatalf("write valid request: %v", err)
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	// First reply must be the bad-JSON error (empty line produced none).
	if !sc.Scan() {
		t.Fatal("expected error reply for bad JSON, got EOF")
	}
	var errReply reply
	if err := json.Unmarshal(sc.Bytes(), &errReply); err != nil {
		t.Fatalf("unmarshal error reply: %v", err)
	}
	if errReply.Error == "" {
		t.Errorf("bad-JSON reply: want non-empty Error, got %+v", errReply)
	}

	// Second reply must be the run result.
	if !sc.Scan() {
		t.Fatal("expected run reply, got EOF")
	}
	var okReply reply
	if err := json.Unmarshal(sc.Bytes(), &okReply); err != nil {
		t.Fatalf("unmarshal run reply: %v", err)
	}
	if okReply.Out != "conn-ok" {
		t.Errorf("run reply out = %q, want %q", okReply.Out, "conn-ok")
	}
}

// TestDial_ConnectError covers the DialTimeout failure branch in Dial (no server
// listening at the given path).
func TestDial_ConnectError(t *testing.T) {
	_, err := Dial("/nonexistent.sock", Call{Tool: "run", Cmd: "echo hi"})
	if err == nil {
		t.Error("Dial to nonexistent socket should return an error")
	}
}

// newRawListener creates a unix socket listener in a temp dir for Dial error
// path tests that need a custom server behaviour. The listener is closed and
// the dir removed on test cleanup.
func newRawListener(t *testing.T) (ln net.Listener, socket string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "tsock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket = filepath.Join(dir, "raw.sock")
	ln, err = net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln, socket
}

// TestDial_ErrNoReply covers the errNoReply sentinel path: the server accepts
// then closes without sending a reply line.
func TestDial_ErrNoReply(t *testing.T) {
	ln, socket := newRawListener(t)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Drain the client's request line FIRST so its write completes, THEN
		// close with no reply. Closing mid-write races to a "broken pipe" on the
		// client instead of the clean EOF that yields errNoReply.
		_, _ = bufio.NewReader(conn).ReadString('\n')
		conn.Close()
	}()

	_, err := Dial(socket, Call{Tool: "run", Cmd: "echo hi"})
	if err != errNoReply {
		t.Errorf("Dial got err = %v, want errNoReply", err)
	}
}

// TestDial_BadJSONReply covers the json.Unmarshal error branch in Dial: the
// server replies with a non-JSON line.
func TestDial_BadJSONReply(t *testing.T) {
	ln, socket := newRawListener(t)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("not-valid-json\n"))
	}()

	_, err := Dial(socket, Call{Tool: "run", Cmd: "echo hi"})
	if err == nil {
		t.Error("Dial with bad-JSON reply should return an error")
	}
}

// TestDialError_Error directly exercises dialError.Error() to close out the 0 %
// function metric (it is returned from Dial but rarely inspected by its exact
// type in production code).
func TestDialError_Error(t *testing.T) {
	if msg := errNoReply.Error(); msg == "" {
		t.Error("errNoReply.Error() returned empty string")
	}
}
