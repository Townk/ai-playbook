package driver

import (
	"strings"
	"testing"
)

// goldenZshJob independently reproduces, for the given params, the EXACT job-script
// bytes the pre-adapter runID emitted (the verbatim historical template). It is the
// byte-identity oracle: zshAdapter{}.job must equal this for any inputs.
func goldenZshJob(cmdline, o, e, cwdf, id, key, nonce string, retain bool) string {
	qcwd := Shquote(cwdf)
	trapBody := "builtin pwd >| " + qcwd
	qo := Shquote(o)
	qe := Shquote(e)
	vp := "" +
		"export LAST_EXCODE=${(q)__apb_rc}\n" +
		"export LAST_STDOUT=${(q)\"$(<" + qo + ")\"}\n" +
		"export LAST_STDERR=${(q)\"$(<" + qe + ")\"}\n"
	if id != "" {
		vp += "" +
			"export APB_OUT_" + key + "=${(q)\"$(<" + qo + ")\"}\n" +
			"export APB_ERR_" + key + "=${(q)\"$(<" + qe + ")\"}\n" +
			"export APB_EXIT_" + key + "=${(q)__apb_rc}\n"
		if retain {
			vp += "" +
				"export APB_OUT_FILE_" + key + "=" + qo + "\n" +
				"export APB_ERR_FILE_" + key + "=" + qe + "\n"
		}
	}
	return "( trap " + Shquote(trapBody) + " EXIT\n" + cmdline + "\n) </dev/null >|" + o + " 2>|" + e + "\n" +
		"__apb_rc=$?\n" +
		"if [[ $__apb_rc -eq 141 ]]; then __apb_rc=0; fi\n" +
		"if [[ -s " + qcwd + " ]]; then builtin cd -- \"$(< " + qcwd + ")\" 2>/dev/null; fi\n" +
		vp +
		"print -r -- " + sentinel + nonce + "_${__apb_rc}" + sentinel + "\n"
}

func TestZshAdapterJobBytesUnchanged(t *testing.T) {
	const nonce = "abcd1234"
	// With an id AND retention active: APB_* value exports + APB_*_FILE_ path exports present.
	got := zshAdapter{}.job(jobParams{cmdline: "echo hi", o: "/d/o", e: "/d/e", cwdf: "/d/cwd", id: "fix", key: "fix", nonce: nonce, retain: true})
	want := goldenZshJob("echo hi", "/d/o", "/d/e", "/d/cwd", "fix", "fix", nonce, true)
	if got != want {
		t.Errorf("job bytes drifted from golden template (id+retain case)\n got: %q\nwant: %q", got, want)
	}

	// Without an id: APB_* lines must be absent, but LAST_* still present.
	gotNo := zshAdapter{}.job(jobParams{cmdline: "echo hi", o: "/d/o", e: "/d/e", cwdf: "/d/cwd", id: "", key: "", nonce: nonce})
	wantNo := goldenZshJob("echo hi", "/d/o", "/d/e", "/d/cwd", "", "", nonce, false)
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

// TestFileExportsGatedOnRetention pins the degraded-mode contract across all three
// adapters: when a run is identified (id != "") but retention is OFF (retain=false —
// the session dir could not be created, so o/e are per-run temp paths deleted right
// after the sentinel), the job must export the value vars (APB_OUT_/APB_ERR_/
// APB_EXIT_) but NOT the path vars (APB_OUT_FILE_/APB_ERR_FILE_) — a FILE export
// would dangle at a path that vanishes with the temp dir. With retention active the
// FILE exports return.
func TestFileExportsGatedOnRetention(t *testing.T) {
	for _, a := range []shellAdapter{zshAdapter{}, bashAdapter{}, shAdapter{}} {
		base := jobParams{cmdline: "echo hi", o: "/d/o", e: "/d/e", cwdf: "/d/cwd", id: "fix", key: "fix", nonce: "n"}

		degraded := a.job(base) // retain=false
		for _, forbidden := range []string{"APB_OUT_FILE_", "APB_ERR_FILE_"} {
			if strings.Contains(degraded, forbidden) {
				t.Errorf("[%s] degraded (retain=false) job must NOT export %q (dangling temp path)\ngot: %q", a.name(), forbidden, degraded)
			}
		}
		for _, want := range []string{"APB_OUT_fix", "APB_ERR_fix", "APB_EXIT_fix"} {
			if !strings.Contains(degraded, want) {
				t.Errorf("[%s] degraded job must still export the value var %q\ngot: %q", a.name(), want, degraded)
			}
		}

		retained := base
		retained.retain = true
		got := a.job(retained)
		for _, want := range []string{"APB_OUT_FILE_fix", "APB_ERR_FILE_fix"} {
			if !strings.Contains(got, want) {
				t.Errorf("[%s] retention-active job must export %q\ngot: %q", a.name(), want, got)
			}
		}
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
