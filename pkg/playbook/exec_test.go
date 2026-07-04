package playbook

import (
	"os"
	"strings"
	"testing"
)

func TestExecCommand_ShellVerbatim(t *testing.T) {
	cmd, cleanup, err := ExecCommand(Block{ID: "s", Type: "shell", Lang: "bash", Payload: "ls -la"}, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if cmd != "ls -la" {
		t.Fatalf("shell cmd = %q, want raw payload", cmd)
	}
	if cleanup != nil {
		t.Fatal("shell block must return a nil cleanup (no script written)")
	}
}

func TestExecCommand_NonRunTypesVerbatim(t *testing.T) {
	for _, typ := range []string{"static", "diff", "create"} {
		cmd, cleanup, err := ExecCommand(Block{ID: "x", Type: typ, Payload: "BODY"}, t.TempDir())
		if err != nil {
			t.Fatalf("%s: unexpected err: %v", typ, err)
		}
		if cmd != "BODY" || cleanup != nil {
			t.Fatalf("%s: cmd=%q cleanup!=nil? %v — want verbatim + nil cleanup", typ, cmd, cleanup != nil)
		}
	}
}

func TestExecCommand_Interpreters(t *testing.T) {
	cases := []struct {
		lang, interp, ext string
	}{
		{"python", "python3", "py"},
		{"python3", "python3", "py"},
		{"py", "python3", "py"},
		{"node", "node", "js"},
		{"js", "node", "js"},
		{"javascript", "node", "js"},
		{"ruby", "ruby", "rb"},
		{"perl", "perl", "pl"},
	}
	for _, c := range cases {
		dir := t.TempDir()
		body := "print('hi')\n" // arbitrary body; must be written byte-exact
		cmd, cleanup, err := ExecCommand(Block{ID: "blk", Type: "run", Lang: c.lang, Payload: body}, dir)
		if err != nil {
			t.Fatalf("%s: unexpected err: %v", c.lang, err)
		}
		if cleanup == nil {
			t.Fatalf("%s: run block must return a non-nil cleanup", c.lang)
		}
		wantName := "apb_block_blk." + c.ext
		wantPath := dir + "/" + wantName
		wantCmd := c.interp + " '" + wantPath + "'" // path single-quoted (driver convention)
		if cmd != wantCmd {
			t.Fatalf("%s: cmd = %q, want %q", c.lang, cmd, wantCmd)
		}
		got, rerr := os.ReadFile(wantPath)
		if rerr != nil {
			t.Fatalf("%s: script file not written: %v", c.lang, rerr)
		}
		if string(got) != body {
			t.Fatalf("%s: script content = %q, want byte-exact %q", c.lang, string(got), body)
		}
		cleanup()
		if _, serr := os.Stat(wantPath); !os.IsNotExist(serr) {
			t.Fatalf("%s: cleanup did not remove the script file", c.lang)
		}
	}
}

func TestExecCommand_UnknownLangVerbatimInterpNoExt(t *testing.T) {
	dir := t.TempDir()
	cmd, cleanup, err := ExecCommand(Block{ID: "b", Type: "run", Lang: "lua", Payload: "print(1)"}, dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	defer cleanup()
	// Unknown lang: interpreter is the lang verbatim, filename has NO extension.
	wantPath := dir + "/apb_block_b"
	if cmd != "lua '"+wantPath+"'" {
		t.Fatalf("cmd = %q, want %q", cmd, "lua '"+wantPath+"'")
	}
	if _, rerr := os.ReadFile(wantPath); rerr != nil {
		t.Fatalf("script file not written at %q: %v", wantPath, rerr)
	}
}

func TestExecCommand_SanitizesID(t *testing.T) {
	dir := t.TempDir()
	cmd, cleanup, err := ExecCommand(Block{ID: "weird id/../x", Type: "run", Lang: "python", Payload: "x"}, dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	defer cleanup()
	// "weird id/../x": space + "/../" → five separators → underscores.
	if !strings.Contains(cmd, "apb_block_weird_id____x.py") {
		t.Fatalf("id not sanitized into the filename: %q", cmd)
	}
}

// TestExecCommand_RerunOverwritesNotAccumulates: assembling the SAME block id
// twice (a producer re-run, or a chain re-materializing) must truncate the
// script file to the new payload, not append to the old one — the second run's
// program text is what the interpreter should see, byte-exact.
func TestExecCommand_RerunOverwritesNotAccumulates(t *testing.T) {
	dir := t.TempDir()
	blk := Block{ID: "blk", Type: "run", Lang: "python"}

	blk.Payload = "print('first')\n"
	_, cleanup1, err := ExecCommand(blk, dir)
	if err != nil {
		t.Fatalf("first assembly: unexpected err: %v", err)
	}
	cleanup1() // mirrors a completed run: the file is removed after use

	blk.Payload = "print('second')\n"
	cmd, cleanup2, err := ExecCommand(blk, dir)
	if err != nil {
		t.Fatalf("second assembly: unexpected err: %v", err)
	}
	defer cleanup2()

	wantPath := dir + "/apb_block_blk.py"
	got, rerr := os.ReadFile(wantPath)
	if rerr != nil {
		t.Fatalf("script file not written: %v", rerr)
	}
	if string(got) != blk.Payload {
		t.Fatalf("re-run script content = %q, want ONLY the new payload %q (truncated, not accumulated)", got, blk.Payload)
	}
	if !strings.Contains(cmd, wantPath) {
		t.Fatalf("cmd = %q, want it to reference %q", cmd, wantPath)
	}
}

func TestExecCommand_NoScriptDirFallsBackToTemp(t *testing.T) {
	// An empty scriptDir (retention degraded / no session dir) must still work: the
	// script lands under os.TempDir() and the returned command is runnable.
	cmd, cleanup, err := ExecCommand(Block{ID: "b", Type: "run", Lang: "python", Payload: "x"}, "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	defer cleanup()
	if !strings.HasPrefix(cmd, "python3 ") {
		t.Fatalf("cmd = %q, want a python3 invocation", cmd)
	}
	path := strings.Trim(strings.TrimPrefix(cmd, "python3 "), "'")
	if _, rerr := os.Stat(path); rerr != nil {
		t.Fatalf("fallback script not written: %v", rerr)
	}
}
