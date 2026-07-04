package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// rcFileSpec is one rc file to drop into a sub-test's isolated temp dir.
type rcFileSpec struct{ name, content string }

// crossShell describes one row of the live matrix: the shell selector, how to
// point the shell at an isolated interactive rc via Options.Env, and the rc
// file(s) that define a `tfn` function (proving per-shell interactive rc
// sourcing). zsh uses ZDOTDIR, bash uses HOME (+ a .bash_profile that sources
// .bashrc, since login bash does not source .bashrc directly), POSIX sh uses ENV.
type crossShell struct {
	sel     string
	rcEnv   func(dir string) []string
	rcFiles func(dir string) []rcFileSpec
}

var crossShells = []crossShell{
	{
		sel:   "zsh",
		rcEnv: func(dir string) []string { return []string{"ZDOTDIR=" + dir} },
		rcFiles: func(dir string) []rcFileSpec {
			return []rcFileSpec{{".zshrc", "tfn() { print -r -- FN_OK }\n"}}
		},
	},
	{
		sel:   "bash",
		rcEnv: func(dir string) []string { return []string{"HOME=" + dir} },
		rcFiles: func(dir string) []rcFileSpec {
			// `bash -il` sources .bash_profile (not .bashrc); a profile that
			// sources .bashrc is the standard real-world bash setup and proves
			// interactive rc fidelity end-to-end.
			return []rcFileSpec{
				{".bashrc", "tfn() { printf 'FN_OK\\n'; }\n"},
				{".bash_profile", "[ -r ~/.bashrc ] && . ~/.bashrc\n"},
			}
		},
	},
	{
		sel:   "sh",
		rcEnv: func(dir string) []string { return []string{"ENV=" + filepath.Join(dir, "env.sh")} },
		rcFiles: func(dir string) []rcFileSpec {
			return []rcFileSpec{{"env.sh", "tfn() { printf 'FN_OK\\n'; }\n"}}
		},
	},
}

// envWith returns base with any keys present in overrides removed, then overrides
// appended — so a controlled HOME/ZDOTDIR/ENV deterministically wins over the
// ambient one inherited from os.Environ().
func envWith(base, overrides []string) []string {
	skip := make(map[string]bool, len(overrides))
	for _, o := range overrides {
		if i := strings.IndexByte(o, '='); i >= 0 {
			skip[o[:i]] = true
		}
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, e := range base {
		if i := strings.IndexByte(e, '='); i >= 0 && skip[e[:i]] {
			continue
		}
		out = append(out, e)
	}
	return append(out, overrides...)
}

// TestCrossShellSemantics drives a REAL shell (one sub-test per available shell;
// skip-if-absent via exec.LookPath) through the driver and asserts the cross-shell
// runtime contract is identical regardless of the per-shell adapter tokens: exit
// codes, stream capture, value-passing round-trip (decision (b) — the key one),
// errexit isolation + cwd persistence, timeout/kill survival, and interactive rc
// fidelity. zsh is always present; bash + sh are skipped when absent.
func TestCrossShellSemantics(t *testing.T) {
	const to = 10 * time.Second
	for _, cs := range crossShells {
		cs := cs
		t.Run(cs.sel, func(t *testing.T) {
			if _, err := exec.LookPath(cs.sel); err != nil {
				t.Skipf("%s not found on PATH; skipping", cs.sel)
			}
			dir := t.TempDir()
			for _, f := range cs.rcFiles(dir) {
				if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			d, err := Open(Options{Shell: cs.sel, Env: envWith(os.Environ(), cs.rcEnv(dir))})
			if err != nil {
				t.Fatalf("Open(%s): %v", cs.sel, err)
			}
			t.Cleanup(func() { d.Close() })

			// 1. exit-code propagation: a failing block carries its non-zero code;
			//    a success is 0.
			if r := d.Run("( exit 7 )", to); r.Exit != 7 || r.TimedOut {
				t.Errorf("[exit] ( exit 7 ) → %+v, want Exit=7", r)
			}
			if r := d.Run("true", to); r.Exit != 0 || r.TimedOut {
				t.Errorf("[exit] true → %+v, want Exit=0", r)
			}

			// 2. stream capture (portable printf): stdout, stderr, exit all split out.
			if r := d.Run("printf 'o\\n'; printf 'e\\n' 1>&2; exit 3", to); r.Out != "o" || r.Err != "e" || r.Exit != 3 {
				t.Errorf("[capture] → %+v, want Out=o Err=e Exit=3", r)
			}

			// 3. value-passing ROUND-TRIP (decision (b), the load-bearing one): a
			//    producer emits a special-char value (space, single quote, newline,
			//    glob); a later block recovers it from $APB_OUT_fix via the portable
			//    re-expansion idiom and must observe the ORIGINAL bytes. This proves
			//    the per-shell quoting (zsh ${(q)}, bash printf %q, sh __apb_q)
			//    round-trips — asserting the CONTRACT, not the stored escaping form.
			const orig = "a b'c\n*d"
			d.RunID("fix", "printf 'a b'\\''c\\n*d'", to)
			if r := d.Run(`eval "x=$APB_OUT_fix"; printf '%s' "$x"`, to); r.Out != orig {
				t.Errorf("[value round-trip] consumer saw %q, want %q (per-shell quoting failed)", r.Out, orig)
			}

			// 4. errexit isolation + cwd persistence + survival: a `set -eu` block
			//    that cds then fails must NOT time out, must report non-zero, must
			//    stop before the trailing print; the cd must persist to the next
			//    block; and the hosted shell must survive the failing block.
			r := d.Run("set -eu; cd /tmp; false; printf nope", to)
			if r.TimedOut {
				t.Fatalf("[errexit] failing set -e block timed out (not isolated): %+v", r)
			}
			if r.Exit == 0 {
				t.Errorf("[errexit] want non-zero exit from failing set -e block, got %+v", r)
			}
			if strings.Contains(r.Out, "nope") {
				t.Errorf("[errexit] errexit should stop before printf nope: %+v", r)
			}
			if r2 := d.Run("pwd", to); r2.Out != "/tmp" {
				t.Errorf("[cwd persist] pwd → %q, want /tmp", r2.Out)
			}
			if r3 := d.Run("printf 'alive\\n'", to); r3.Out != "alive" || r3.TimedOut {
				t.Errorf("[survive] shell didn't survive the failing block: %+v", r3)
			}

			// 5. timeout/kill + survival: a 30s sleep with a 2s budget times out;
			//    the shell remains drivable afterward.
			if r := d.Run("sleep 30", 2*time.Second); !r.TimedOut {
				t.Errorf("[timeout] sleep 30 should time out → %+v", r)
			}
			if r := d.Run("printf 'alive\\n'", to); r.Out != "alive" {
				t.Errorf("[timeout survive] shell should survive the kill → %+v", r)
			}

			// 6. interactive rc fidelity: the shell-appropriate rc (sourced per the
			//    rcEnv mechanism) defines tfn; invoking it proves rc sourcing works.
			if r := d.Run("tfn", to); r.Out != "FN_OK" || r.Exit != 0 {
				t.Errorf("[rc fidelity] tfn → %+v, want Out=FN_OK", r)
			}
		})
	}
}
