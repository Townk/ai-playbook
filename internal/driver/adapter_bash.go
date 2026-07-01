package driver

// bashAdapter renders job scripts for an interactive bash (`bash -il`). The
// portable structure (errexit subshell + EXIT-trap cwd capture + 141→0 SIGPIPE
// remap + cd re-apply + sentinel) is identical to the zsh adapter; only the
// shell-specific tokens differ:
//
//   - sentinel:      printf '%s\n' (not zsh's `print -r --`)
//   - value-quoting: printf %q     (macOS bash 3.2 — ${var@Q} not available)
//   - conditionals:  [ ]           (POSIX; same form the sh adapter (Task 5) will use)
//   - file reads:    $(<file)      (bash supports this, same as zsh)
//   - cwd trap:      builtin pwd >| <path>  (>| is valid bash; matches zsh for parity)
//   - source:        source        (bash has source, same as zsh)
//   - cd/pwd:        builtin …     (bash supports builtin, same as zsh)
type bashAdapter struct{}

func (bashAdapter) name() string                    { return "bash" }
func (bashAdapter) spawnArgs() []string             { return []string{"-il"} }
func (bashAdapter) jobExt() string                  { return "bash" }
func (bashAdapter) sourceCmd(jobPath string) string { return "source " + jobPath }
func (bashAdapter) cdCmd(target string) string {
	return "builtin cd -- " + shquote(target) + " 2>/dev/null"
}

// historyOff disables on-disk history (HISTFILE=/dev/null, `set +o history`) and
// drops the DEBUG trap + PROMPT_COMMAND that bash-preexec (which atuin hooks into)
// uses to record commands. Best-effort: the driver's bash session is dedicated to
// running playbook steps, so clearing these prompt hooks is safe here.
func (bashAdapter) historyOff() string {
	return "HISTFILE=/dev/null; export HISTFILE; set +o history 2>/dev/null; " +
		"trap - DEBUG 2>/dev/null; PROMPT_COMMAND="
}

// historyShimFiles returns nil: bash uses the runtime historyOff path, not a
// ZDOTDIR-style shim (there is no atuin instant-prompt race for bash here).
func (bashAdapter) historyShimFiles() map[string]string { return nil }

func (bashAdapter) sentinelEcho() string {
	return "printf '%s\\n' " + shquote(sentinel+"0"+sentinel)
}

func (bashAdapter) job(p jobParams) string {
	qcwd := shquote(p.cwdf)
	trapBody := "builtin pwd >| " + qcwd
	qo := shquote(p.o)
	qe := shquote(p.e)
	// printf %q quotes a value so it re-expands word-split- and glob-safely.
	// macOS ships bash 3.2 which lacks ${var@Q}, so printf %q is the portable
	// bash quoting primitive. Numbers (exit codes) are their own printf %q output,
	// so __apb_rc is used bare there — printf %q of an integer is the integer.
	vp := "" +
		"export LAST_EXCODE=$__apb_rc\n" +
		"export LAST_STDOUT=\"$(printf %q \"$(<" + qo + ")\")\"" + "\n" +
		"export LAST_STDERR=\"$(printf %q \"$(<" + qe + ")\")\"" + "\n"
	if p.id != "" {
		key := p.key
		vp += "" +
			"export APB_OUT_" + key + "=\"$(printf %q \"$(<" + qo + ")\")\"" + "\n" +
			"export APB_ERR_" + key + "=\"$(printf %q \"$(<" + qe + ")\")\"" + "\n" +
			"export APB_EXIT_" + key + "=$__apb_rc\n"
	}
	return "( trap " + shquote(trapBody) + " EXIT\n" + p.cmdline + "\n) </dev/null >" + p.o + " 2>" + p.e + "\n" +
		"__apb_rc=$?\n" +
		"if [ $__apb_rc -eq 141 ]; then __apb_rc=0; fi\n" +
		"if [ -s " + qcwd + " ]; then builtin cd -- \"$(< " + qcwd + ")\" 2>/dev/null; fi\n" +
		vp +
		"printf '%s\\n' " + sentinel + "${__apb_rc}" + sentinel + "\n"
}
