package validate

import (
	"strings"
	"testing"

	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
)

// fmEnv builds a complete front matter declaring the given env var names, so
// env-decl tests can toggle the declared set without repeating the required
// keys.
func fmEnv(names ...string) frontmatter.FrontMatter {
	f := fm("N", "D", "C", "2026-01-01")
	if len(names) > 0 {
		f.Env = map[string]frontmatter.EnvValue{}
		for _, n := range names {
			f.Env[n] = frontmatter.EnvValue{Value: "v"}
		}
	}
	return f
}

// findingsFor is the quality-table driver: a complete front matter (plus
// declared env names) and the given blocks, no raw body concerns.
func findingsFor(f frontmatter.FrontMatter, blocks []Block) []Finding {
	return Check("", f, true, blocks, 0)
}

// --- verify: fires only when no {id=verify} block exists ---

func TestCheck_VerifyWarning(t *testing.T) {
	// no verify block → warning
	blocks := []Block{{ID: "a", Type: "shell", Lang: "bash", Payload: "true"}}
	if fs := findingsFor(fmEnv(), blocks); !has(fs, "verify", Warning) {
		t.Fatal("a playbook without {id=verify} must warn")
	}
	// verify present → no warning
	blocks = []Block{
		{ID: "a", Type: "shell", Lang: "bash", Payload: "true"},
		{ID: "verify", Type: "shell", Lang: "bash", Needs: []string{"a"}, Payload: "true"},
	}
	if fs := findingsFor(fmEnv(), blocks); has(fs, "verify", Warning) {
		t.Fatalf("a playbook with {id=verify} must not warn: %+v", fs)
	}
}

// --- rollback: fires only with >=2 runnable non-verify steps and zero
// rollback declarations ---

func TestCheck_RollbackWarning(t *testing.T) {
	// two runnable steps, zero rollbacks → warning
	blocks := []Block{
		{ID: "a", Type: "shell", Lang: "bash", Payload: "true"},
		{ID: "b", Type: "shell", Lang: "bash", Payload: "true"},
	}
	if fs := findingsFor(fmEnv(), blocks); !has(fs, "rollback", Warning) {
		t.Fatal("two steps with no rollback= must warn")
	}
	// single-step playbook → no rollback warning
	blocks = []Block{{ID: "a", Type: "shell", Lang: "bash", Payload: "true"}}
	if fs := findingsFor(fmEnv(), blocks); has(fs, "rollback", Warning) {
		t.Fatalf("a single-step playbook must not warn rollback: %+v", fs)
	}
	// a step + verify only → verify does not count toward the step threshold
	blocks = []Block{
		{ID: "a", Type: "shell", Lang: "bash", Payload: "true"},
		{ID: "verify", Type: "shell", Lang: "bash", Payload: "true"},
	}
	if fs := findingsFor(fmEnv(), blocks); has(fs, "rollback", Warning) {
		t.Fatalf("step+verify must not warn rollback: %+v", fs)
	}
	// a rollback= declaration present → no warning
	blocks = []Block{
		{ID: "a", Type: "shell", Lang: "bash", Rollback: "undo-a", Payload: "true"},
		{ID: "undo-a", Type: "shell", Lang: "bash", Payload: "true"},
		{ID: "b", Type: "shell", Lang: "bash", Payload: "true"},
	}
	if fs := findingsFor(fmEnv(), blocks); has(fs, "rollback", Warning) {
		t.Fatalf("a playbook with a rollback= block must not warn: %+v", fs)
	}
	// static blocks are not steps
	blocks = []Block{
		{ID: "a", Type: "shell", Lang: "bash", Payload: "true"},
		{ID: "s1", Type: "static", Static: true},
		{ID: "s2", Type: "static", Static: true},
	}
	if fs := findingsFor(fmEnv(), blocks); has(fs, "rollback", Warning) {
		t.Fatalf("one step plus statics must not warn rollback: %+v", fs)
	}
}

// --- file-block: heredoc + redirection on the operator line of a shell/run
// block ---

func TestCheck_FileBlockWarning(t *testing.T) {
	cases := []struct {
		name  string
		block Block
		want  bool
	}{
		{"heredoc + > redirect", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "cat > /etc/app.conf <<EOF\nkey=value\nEOF"}, true},
		{"heredoc + >> append", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "cat <<'EOF' >> ~/.zshrc\nexport X=1\nEOF"}, true},
		{"heredoc + tee", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "cat <<EOF | tee /etc/app.conf\nkey=value\nEOF"}, true},
		{"heredoc WITHOUT redirect (stdin feed)", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "python3 <<EOF\nprint(1)\nEOF"}, false},
		{"heredoc + 2>&1 only (stream dup, no file write)", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "bash <<EOF 2>&1\ntrue\nEOF"}, false},
		{"heredoc body contains > (SQL comparison)", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "psql mydb <<EOF\nSELECT * FROM t WHERE a > 5;\nEOF"}, false},
		{"heredoc body embedded script redirects", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "bash <<'EOF'\nmake build > build.log\nEOF"}, false},
		{"heredoc body quoted prose >", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "cat <<'EOF' | less\n> quoted note\nEOF"}, false},
		{"heredoc + >/dev/null discard", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "bash <<EOF >/dev/null\ntrue\nEOF"}, false},
		{"heredoc + 2>/dev/null discard", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "bash <<EOF 2>/dev/null\ntrue\nEOF"}, false},
		{"redirect WITHOUT heredoc", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "echo hi > /tmp/x"}, false},
		{"redirect on a different line than the heredoc", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "psql mydb <<EOF\nSELECT 1;\nEOF\necho done > /tmp/marker"}, false},
		{"herestring is not a heredoc", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "grep x <<<\"input\" > /tmp/x"}, false},
		{"arithmetic shift is not a heredoc", Block{ID: "a", Type: "shell", Lang: "bash",
			Payload: "echo $((1<<8)) > /tmp/x"}, false},
		{"static block never fires", Block{ID: "a", Type: "static", Static: true,
			Payload: "cat > /etc/app.conf <<EOF\nkey=value\nEOF"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := findingsFor(fmEnv(), []Block{tc.block})
			if got := has(fs, "file-block", Warning); got != tc.want {
				t.Fatalf("file-block warning = %v, want %v (findings %+v)", got, tc.want, fs)
			}
		})
	}
}

// --- env-decl: a braced ${VAR} reference in a shell/run block, not declared
// in env:, not a builtin. Bare $VAR never fires — that form is everyday
// shell (loop variables, read targets, locals, awk fields, builtins), not a
// certain environment input. ---

func TestCheck_EnvDeclWarning(t *testing.T) {
	cases := []struct {
		name  string
		fm    frontmatter.FrontMatter
		block Block
		want  bool
	}{
		{"undeclared ${VAR}", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "echo ${APP_PORT}"}, true},
		{"undeclared ${VAR:-default} (parameter expansion)", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "echo ${APP_PORT:-8080}"}, true},
		{"bare $VAR never fires (unbraced form)", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "echo $APP_PORT"}, false},
		{"for-loop variable", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "for FILE in *.txt; do rm \"$FILE\"; done"}, false},
		{"read target", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "read -r ANSWER; echo $ANSWER"}, false},
		{"local assignment", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "f() { local RC=0; echo $RC; }"}, false},
		{"awk field ($NF, single-quoted)", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "ps aux | awk '{print $NF}'"}, false},
		{"single-quoted literal", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "echo '$LITERAL_VAR untouched'"}, false},
		{"shell builtin $RANDOM", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "echo $RANDOM"}, false},
		{"declared in env:", fmEnv("APP_PORT"),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "echo ${APP_PORT}"}, false},
		{"APB_ builtin prefix", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "echo ${APB_OUT_build}"}, false},
		{"LAST_STDOUT builtin", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "echo ${LAST_STDOUT}"}, false},
		{"LAST_STDERR/LAST_EXCODE builtins", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "echo ${LAST_STDERR}; test ${LAST_EXCODE} -eq 0"}, false},
		{"PROJECT_ROOT builtin", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "cd ${PROJECT_ROOT}"}, false},
		{"HOME/PATH/USER builtins", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "echo ${HOME} ${PATH} ${USER}"}, false},
		{"SHELL/TMPDIR/EDITOR/PWD builtins", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "echo ${SHELL} ${TMPDIR} ${EDITOR} ${PWD}"}, false},
		{"assigned in the same block", fmEnv(),
			Block{ID: "a", Type: "shell", Lang: "bash", Payload: "APP_PORT=8080\necho ${APP_PORT}"}, false},
		{"static block never fires", fmEnv(),
			Block{ID: "a", Type: "static", Static: true, Payload: "echo ${APP_PORT}"}, false},
		{"static bash block never fires", fmEnv(),
			Block{ID: "a", Type: "static", Lang: "bash", Static: true, Payload: "echo ${APP_PORT}"}, false},
		{"create/file= template text never fires", fmEnv(),
			Block{ID: "a", Type: "create", Lang: "toml", Payload: "port = ${APP_PORT}"}, false},
		{"diff block never fires", fmEnv(),
			Block{ID: "a", Type: "diff", Lang: "diff", Payload: "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-${APP_PORT}\n+y"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := findingsFor(tc.fm, []Block{tc.block})
			if got := has(fs, "env-decl", Warning); got != tc.want {
				t.Fatalf("env-decl warning = %v, want %v (findings %+v)", got, tc.want, fs)
			}
		})
	}
}

// TestCheck_EnvDeclWarning_NamesVar pins the message: it must name the block
// and the undeclared variable so the author knows exactly what to declare.
func TestCheck_EnvDeclWarning_NamesVar(t *testing.T) {
	blocks := []Block{{ID: "cfg", Type: "shell", Lang: "bash", Payload: "echo ${APP_PORT}"}}
	fs := findingsFor(fmEnv(), blocks)
	for _, f := range fs {
		if f.Check != "env-decl" {
			continue
		}
		if f.Where != "cfg" {
			t.Fatalf("env-decl Where = %q, want %q", f.Where, "cfg")
		}
		if want := "APP_PORT"; !strings.Contains(f.Message, want) {
			t.Fatalf("env-decl message %q must name %q", f.Message, want)
		}
		return
	}
	t.Fatalf("no env-decl finding: %+v", fs)
}

// --- severity + exit-code invariants: all four checks are Warning-only ---

// TestCheck_QualityWarningsNeverError fires all four quality checks at once
// and pins the tier contract: every finding is Warning severity and HasError
// stays false (the validate exit code is untouched by quality findings).
func TestCheck_QualityWarningsNeverError(t *testing.T) {
	blocks := []Block{
		{ID: "a", Type: "shell", Lang: "bash",
			Payload: "cat > /etc/app.conf <<EOF\nport=${APP_PORT}\nEOF"},
		{ID: "b", Type: "shell", Lang: "bash", Payload: "echo ${APP_PORT}"},
	}
	fs := findingsFor(fmEnv(), blocks)
	for _, check := range []string{"verify", "rollback", "file-block", "env-decl"} {
		if !has(fs, check, Warning) {
			t.Fatalf("expected %s warning to fire: %+v", check, fs)
		}
		if has(fs, check, Error) {
			t.Fatalf("quality check %s must never be Error severity: %+v", check, fs)
		}
	}
	if HasError(fs) {
		t.Fatalf("quality warnings alone must not report HasError: %+v", fs)
	}
}
