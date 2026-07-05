package driver

// jobParams carries the per-run, shell-agnostic values a shellAdapter needs to
// render a job script. cmdline is the user's command; o/e/cwdf are the temp-file
// paths for stdout/stderr/cwd capture; id is the value-passing id ("" disables
// the APB_* exports) and key is its sanitized form (sanitizeKey(id)); nonce is the
// per-run random token woven into the sentinel (__APB__<nonce>_<rc>__APB__) so a
// stale sentinel from another run can never satisfy this run's wait. stdinPath, when
// non-empty, is the file the block's subshell reads stdin from (a prior block's
// retained capture); empty means </dev/null. retain is true only when this run's
// o/e are the session-dir capture files that survive the run (identified run +
// retention active) — it gates the APB_OUT_FILE_/APB_ERR_FILE_ path exports so
// degraded mode (no session dir) never exports a path that vanishes with the
// per-run temp dir. All paths are driver-chosen (temp / session dir) and
// space-free, but the adapters single-quote them defensively.
type jobParams struct {
	cmdline, o, e, cwdf, id, key, nonce, stdinPath string
	retain                                         bool
}

// stdinRedir returns the subshell's stdin source token for p: /dev/null when no
// stdinPath is set (byte-identical to the historical default), else the shell-
// quoted capture path. Shared by all three adapters so the redirect is uniform.
func stdinRedir(p jobParams) string {
	if p.stdinPath == "" {
		return "/dev/null"
	}
	return Shquote(p.stdinPath)
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
	// historyOff is a one-time MAIN-context command (run via driver.runMain at
	// session start) that stops the driver's commands from polluting the user's
	// shell/atuin history — the driver spawns an interactive shell for fidelity,
	// but its `source <job>` lines should never be recorded. Empty = nothing to do.
	historyOff() string
	// historyShimFiles returns the ZDOTDIR-shim startup files (relative filename →
	// content) the driver writes into a temp dir and points the spawned shell at, so
	// recording is hard-disabled at shell INIT time — before atuin's preexec/precmd
	// hooks are ever armed and before any interactive command runs. The shim files
	// each source the user's real counterpart at top level (preserving env, aliases,
	// functions) and then disable history. Returns nil for shells that don't use a
	// shim (bash/sh keep the runtime historyOff path). When non-nil, the driver
	// skips the runtime historyOff call at Open.
	historyShimFiles() map[string]string
	// sentinelEcho prints the driver's sentinel (with exit 0) for the given per-run
	// nonce in the MAIN context, so runMain can sync on a raw command that is NOT
	// wrapped in job()'s subshell. The nonce must match the one runMain compiles its
	// wait regex from.
	sentinelEcho(nonce string) string
}
