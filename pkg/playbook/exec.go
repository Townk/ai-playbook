package playbook

import (
	"os"
	"path/filepath"
	"strings"
)

// ExecCommand is the canonical block→shell-command rule (ADR-0010): the single
// place that turns a runnable Block into the command a shell eval's. Both the
// interactive viewer (at dispatch) and the headless `--auto` runner consume it,
// so the two can never disagree about how a script block is invoked.
//
//   - shell blocks (and every non-"run" type) run their payload VERBATIM and
//     return a nil cleanup — nothing is written to disk.
//   - run (script) blocks — python/node/ruby/perl and any other interpreted
//     language — are written to a session temp script file and the command
//     becomes `<interpreter> '<script-path>'`. The program text reaches the
//     interpreter as a FILE, not through a stdin heredoc, so the block's stdin
//     stays free for `from=` data piping. The returned cleanup removes the
//     script file; callers invoke it once the run completes.
//
// The prior renderer path wrapped scripts in a single-quoted (unexpanded)
// `<<'__APB_RUN__'` heredoc; a script file invoked by its interpreter is exactly
// equivalent — the body is never shell-expanded either way — but frees stdin.
// It also incidentally fixes a payload/terminator collision the heredoc had: a
// script whose text happened to contain the literal line `__APB_RUN__` closed
// the heredoc early and corrupted the run. A file has no terminator to collide
// with.
//
// scriptDir is where run-block scripts are written — pass the driver's session
// dir so they survive until the session closes; the cleanup removes each file
// after its run so they don't accumulate. An empty scriptDir (retention
// degraded / no session) falls back to os.TempDir(). A write failure returns the
// error with an empty command and a nil cleanup.
func ExecCommand(b Block, scriptDir string) (cmd string, cleanup func(), err error) {
	if b.Type != "run" {
		return b.Payload, nil, nil
	}
	interp, ext := interpFor(b.Lang)
	dir := scriptDir
	if dir == "" {
		dir = os.TempDir()
	}
	name := "apb_block_" + sanitizeExecKey(b.ID)
	if ext != "" {
		name += "." + ext
	}
	path := filepath.Join(dir, name)
	if werr := os.WriteFile(path, []byte(b.Payload), 0o600); werr != nil {
		return "", nil, werr
	}
	cleanup = func() { _ = os.Remove(path) }
	return interp + " " + shquoteExec(path), cleanup, nil
}

// interpFor maps a fenced block's language to the interpreter command and the
// script-file extension used to invoke it. python/python3/py → python3 (.py);
// node/js/javascript → node (.js); ruby → ruby (.rb); perl → perl (.pl). Any
// other language is used VERBATIM as the interpreter with no extension (a stable,
// cosmetic filename choice). The interpreter mapping matches the renderer's prior
// langInterp exactly.
func interpFor(lang string) (interp, ext string) {
	switch lang {
	case "python", "python3", "py":
		return "python3", "py"
	case "node", "js", "javascript":
		return "node", "js"
	case "ruby":
		return "ruby", "rb"
	case "perl":
		return "perl", "pl"
	default:
		return lang, ""
	}
}

// sanitizeExecKey keeps a block id safe for a filename: non-[A-Za-z0-9_] → _.
// Mirrors the driver's sanitizeKey convention so a block's script and its
// retained captures share the same key shape.
func sanitizeExecKey(id string) string {
	b := []byte(id)
	for i, c := range b {
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			b[i] = '_'
		}
	}
	return string(b)
}

// shquoteExec single-quotes s for safe interpolation into a shell command,
// escaping embedded single quotes as '\”. Script paths are driver-chosen and
// space-free by construction, but quoting is defensive parity with the driver
// adapters (which single-quote retained paths for the same reason).
func shquoteExec(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
