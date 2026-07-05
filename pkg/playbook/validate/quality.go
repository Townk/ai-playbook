// quality.go — the authoring-rubric quality checks (Surface 2 of
// docs/specifications/playbook-authoring.md): verify presence, rollback
// coverage, heredoc file writes, and undeclared env references. ALL findings
// here are Warning severity — quality is advisory, so these checks can never
// flip HasError or the validate exit code. Detection is deliberately
// conservative (certain signals only); the judgment calls — is THIS block too
// coarse, does THIS step need a rollback — belong to the AI review pass.
package validate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
)

// builtinEnv is the env-decl allowlist: env-var names a runnable block may
// reference without an `env:` front-matter declaration, because the runner or
// every target machine provides them — the session value-passing vars
// (LAST_*; APB_-prefixed names are matched by prefix in builtinEnvVar) plus
// universal shell/session variables. Adding a name is a one-line change.
var builtinEnv = map[string]struct{}{
	"LAST_STDOUT":  {},
	"LAST_STDERR":  {},
	"LAST_EXCODE":  {},
	"PROJECT_ROOT": {},
	"HOME":         {},
	"PATH":         {},
	"USER":         {},
	"SHELL":        {},
	"TMPDIR":       {},
	"EDITOR":       {},
	"PWD":          {},
}

// builtinEnvVar reports whether name needs no env: declaration: any
// APB_-prefixed runner variable (APB_OUT_<id>, APB_OUT_FILE_<id>, …) or a
// builtinEnv member.
func builtinEnvVar(name string) bool {
	if strings.HasPrefix(name, "APB_") {
		return true
	}
	_, ok := builtinEnv[name]
	return ok
}

// heredocRe matches a heredoc operator (<<EOF, <<-EOF, << 'EOF', <<"EOF").
// The delimiter must start with a letter/underscore and the << must not abut
// another <, so a herestring (<<<"str") and an arithmetic shift (a<<2) never
// match.
var heredocRe = regexp.MustCompile(`(?:^|[^<])<<-?[ \t]*["']?[A-Za-z_]`)

// fileWriteRe matches a file-writing redirection: a > or >> whose target is
// not a file descriptor (2>&1 is stream duplication, not a file write; the
// leading guard skips ->, =>, <>, and the middle of a longer > run), or a
// pipe into tee.
var fileWriteRe = regexp.MustCompile(`(?:^|[^-=<>])>>?[ \t]*[^&>\s]|\|[ \t]*tee\b`)

// devNullRe matches a discard redirection (>/dev/null, 2>/dev/null,
// &>/dev/null). heredocWritesFile strips these before the fileWriteRe scan —
// a discard is not a file the author should move to a file= block.
var devNullRe = regexp.MustCompile(`[0-9&]?>>?[ \t]*/dev/null`)

// heredocWritesFile reports whether any single payload LINE carries both the
// heredoc operator and a file-writing redirection / tee pipe. Line-scoping is
// the certainty guard: every real heredoc file write puts both signals on the
// operator line (`cat > f <<EOF`, `cat <<EOF >> f`, `cat <<EOF | tee f`),
// while a `>` inside the heredoc BODY (a SQL comparison, an embedded script's
// own redirect, quoted prose) says nothing about what this block writes.
func heredocWritesFile(payload string) bool {
	for _, line := range strings.Split(payload, "\n") {
		if heredocRe.MatchString(line) && fileWriteRe.MatchString(devNullRe.ReplaceAllString(line, "")) {
			return true
		}
	}
	return false
}

// bracedEnvRefRe matches a braced ${VAR} reference — the ONLY form env-decl
// flags. Bare $VAR is deliberately excluded: that is how loop variables, read
// targets, locals, awk fields ($NF), and shell builtins ($RANDOM) appear in
// everyday shell, and flagging those is exactly the false-positive noise the
// spec rejects ("detection must be certain"). Same name pattern as the braced
// alternative of frontmatter's envRefRe.
var bracedEnvRefRe = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)[^}]*\}`)

// envAssignRe matches shell assignment position — [export] VAR= at a command
// boundary — mirroring the assignment alternative of frontmatter's envRefRe.
// A name assigned inside a block is defined there, not an external input, so
// env-decl must not flag its later ${VAR} references in the same block.
var envAssignRe = regexp.MustCompile(`(?m)(?:^|[;&|]|\bexport\s+)\s*([A-Z_][A-Z0-9_]*)=`)

// qualityFindings runs the four rubric quality checks over the parsed blocks
// and the declared env: front matter. Every finding is Warning severity.
func qualityFindings(fm frontmatter.FrontMatter, blocks []Block) []Finding {
	var findings []Finding

	// verify (rubric rule 5): every playbook ends with an {id=verify} block
	// proving the goal state.
	hasVerify := false
	// rollback (rubric rule 4): a multi-step playbook with no rollback= at
	// all almost certainly mutates state it cannot undo. Steps are runnable
	// non-verify blocks; whether EACH step needs one is the AI review's call.
	steps := 0
	hasRollback := false
	for _, b := range blocks {
		if b.ID == "verify" {
			hasVerify = true
		}
		if b.Rollback != "" {
			hasRollback = true
		}
		if !b.Static && b.ID != "verify" {
			steps++
		}
	}
	if !hasVerify {
		findings = append(findings, Finding{
			Severity: Warning,
			Check:    "verify",
			Message:  "no {id=verify} block — end with one block proving the goal state",
			Where:    "",
		})
	}
	if steps >= 2 && !hasRollback {
		findings = append(findings, Finding{
			Severity: Warning,
			Check:    "rollback",
			Message:  fmt.Sprintf("%d runnable steps but no rollback= blocks — pair each state-mutating step with a rollback restoring the pre-step state", steps),
			Where:    "",
		})
	}

	// file-block (rubric rule 2) + env-decl (rubric rule 8): per shell/run
	// block, scanning the payload. Other types are skipped — a diff/create
	// payload is content, not commands, and static is illustration.
	for _, b := range blocks {
		if !runnableType(b.Type) {
			continue
		}

		// file-block: conservative — BOTH a heredoc marker AND a
		// file-writing redirection on the SAME line (the operator line).
		if heredocWritesFile(b.Payload) {
			findings = append(findings, Finding{
				Severity: Warning,
				Check:    "file-block",
				Message:  fmt.Sprintf("block %q writes a file via a heredoc — put the file's content in a file=<path> create block instead", b.ID),
				Where:    b.ID,
			})
		}

		// env-decl: every braced ${VAR} reference must be declared in env:,
		// be a builtin, or be assigned within the block itself. ScanEnvRefs
		// drives the scan (sorted, deduped names) and the braced set filters
		// it to the certain form — see bracedEnvRefRe.
		declared := fm.Env // nil-safe: indexing a nil map just misses
		braced := map[string]struct{}{}
		for _, m := range bracedEnvRefRe.FindAllStringSubmatch(b.Payload, -1) {
			braced[m[1]] = struct{}{}
		}
		assigned := map[string]struct{}{}
		for _, m := range envAssignRe.FindAllStringSubmatch(b.Payload, -1) {
			assigned[m[1]] = struct{}{}
		}
		for _, name := range frontmatter.ScanEnvRefs(b.Payload) {
			if _, ok := braced[name]; !ok {
				continue // bare $VAR / assignment-only: not a certain env input
			}
			if _, ok := declared[name]; ok {
				continue
			}
			if builtinEnvVar(name) {
				continue
			}
			if _, ok := assigned[name]; ok {
				continue
			}
			findings = append(findings, Finding{
				Severity: Warning,
				Check:    "env-decl",
				Message:  fmt.Sprintf("block %q references ${%s}, which is not declared in the env: front matter", b.ID, name),
				Where:    b.ID,
			})
		}
	}

	return findings
}
