package driver

// jobParams carries the per-run, shell-agnostic values a shellAdapter needs to
// render a job script. cmdline is the user's command; o/e/cwdf are the temp-file
// paths for stdout/stderr/cwd capture; id is the value-passing id ("" disables
// the AAPB_* exports) and key is its sanitized form (sanitizeKey(id)).
type jobParams struct {
	cmdline, o, e, cwdf, id, key string
}

// shellAdapter owns every shell-specific token the driver emits: how to spawn the
// shell, what extension/source-syntax its job script uses, how to `cd`, and the
// full job-script body. The portable structure (errexit subshell + EXIT-trap cwd
// capture + 141→0 remap + cd re-apply + sentinel) lives in job(); only the tokens
// differ per shell.
type shellAdapter interface {
	name() string                    // shell binary name, e.g. "zsh"
	spawnArgs() []string             // spawn flags, e.g. []string{"-il"}
	jobExt() string                  // job-file extension, e.g. "zsh"
	sourceCmd(jobPath string) string // command to source the job script
	cdCmd(target string) string      // command to cd into target
	job(p jobParams) string          // the full job-script body
}
