package driver

import (
	"strings"
	"testing"
)

// goldenZshJob independently reproduces, for the given params, the EXACT job-script
// bytes the pre-adapter runID emitted (the verbatim historical template). It is the
// byte-identity oracle: zshAdapter{}.job must equal this for any inputs.
func goldenZshJob(cmdline, o, e, cwdf, id, key string) string {
	qcwd := shquote(cwdf)
	trapBody := "builtin pwd >| " + qcwd
	qo := shquote(o)
	qe := shquote(e)
	vp := "" +
		"export LAST_EXCODE=${(q)__apb_rc}\n" +
		"export LAST_STDOUT=${(q)\"$(<" + qo + ")\"}\n" +
		"export LAST_STDERR=${(q)\"$(<" + qe + ")\"}\n"
	if id != "" {
		vp += "" +
			"export APB_OUT_" + key + "=${(q)\"$(<" + qo + ")\"}\n" +
			"export APB_ERR_" + key + "=${(q)\"$(<" + qe + ")\"}\n" +
			"export APB_EXIT_" + key + "=${(q)__apb_rc}\n"
	}
	return "( trap " + shquote(trapBody) + " EXIT\n" + cmdline + "\n) </dev/null >" + o + " 2>" + e + "\n" +
		"__apb_rc=$?\n" +
		"if [[ $__apb_rc -eq 141 ]]; then __apb_rc=0; fi\n" +
		"if [[ -s " + qcwd + " ]]; then builtin cd -- \"$(< " + qcwd + ")\" 2>/dev/null; fi\n" +
		vp +
		"print -r -- " + sentinel + "${__apb_rc}" + sentinel + "\n"
}

func TestZshAdapterJobBytesUnchanged(t *testing.T) {
	// With an id: APB_* exports present.
	got := zshAdapter{}.job(jobParams{cmdline: "echo hi", o: "/d/o", e: "/d/e", cwdf: "/d/cwd", id: "fix", key: "fix"})
	want := goldenZshJob("echo hi", "/d/o", "/d/e", "/d/cwd", "fix", "fix")
	if got != want {
		t.Errorf("job bytes drifted from golden template (id case)\n got: %q\nwant: %q", got, want)
	}

	// Without an id: APB_* lines must be absent, but LAST_* still present.
	gotNo := zshAdapter{}.job(jobParams{cmdline: "echo hi", o: "/d/o", e: "/d/e", cwdf: "/d/cwd", id: "", key: ""})
	wantNo := goldenZshJob("echo hi", "/d/o", "/d/e", "/d/cwd", "", "")
	if gotNo != wantNo {
		t.Errorf("job bytes drifted from golden template (no-id case)\n got: %q\nwant: %q", gotNo, wantNo)
	}
	for _, frag := range []string{"APB_OUT_", "APB_ERR_", "APB_EXIT_"} {
		if strings.Contains(gotNo, frag) {
			t.Errorf("no-id job must not contain %q, got: %q", frag, gotNo)
		}
	}
	if !strings.Contains(gotNo, "export LAST_EXCODE=") {
		t.Errorf("no-id job must still contain LAST_* exports, got: %q", gotNo)
	}
}

func TestZshAdapterTokens(t *testing.T) {
	a := zshAdapter{}
	if got := a.spawnArgs(); len(got) != 1 || got[0] != "-il" {
		t.Errorf("spawnArgs() = %v, want [-il]", got)
	}
	if got := a.sourceCmd("/x"); got != "source /x" {
		t.Errorf("sourceCmd(/x) = %q, want %q", got, "source /x")
	}
	if got := a.cdCmd("/p"); got != "builtin cd -- '/p' 2>/dev/null" {
		t.Errorf("cdCmd(/p) = %q, want %q", got, "builtin cd -- '/p' 2>/dev/null")
	}
	if got := a.jobExt(); got != "zsh" {
		t.Errorf("jobExt() = %q, want zsh", got)
	}
	if got := a.name(); got != "zsh" {
		t.Errorf("name() = %q, want zsh", got)
	}
}
