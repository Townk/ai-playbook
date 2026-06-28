package driver

// zshAdapter renders job scripts for an interactive zsh (`zsh -il`). It reproduces
// byte-for-byte the script the driver emitted before the adapter seam existed: a
// `( trap … EXIT )` errexit-isolating subshell, $? capture, 141→0 SIGPIPE remap,
// EXIT-trap cwd re-apply, ${(q)}-quoted value-passing exports, and the own
// sentinel. See runID's historical comment for the rationale of each line.
type zshAdapter struct{}

func (zshAdapter) name() string                    { return "zsh" }
func (zshAdapter) spawnArgs() []string             { return []string{"-il"} }
func (zshAdapter) jobExt() string                  { return "zsh" }
func (zshAdapter) sourceCmd(jobPath string) string { return "source " + jobPath }
func (zshAdapter) cdCmd(target string) string {
	return "builtin cd -- " + shquote(target) + " 2>/dev/null"
}

// historyOff stops the driver's `source <job>` lines from being saved to the zsh
// history file or recorded by atuin: HISTFILE=/dev/null + SAVEHIST=0 disable the
// on-disk save; removing atuin's preexec/precmd hooks stops atuin from recording
// (atuin captures every interactive command via those hooks, independent of
// HISTFILE). Runs once in the MAIN context before any job is sourced, so even the
// first source-line isn't stored (atuin's precmd is gone before it would fire).
func (zshAdapter) historyOff() string {
	return "HISTFILE=/dev/null; SAVEHIST=0; " +
		"autoload -Uz add-zsh-hook 2>/dev/null; " +
		"add-zsh-hook -d preexec _atuin_preexec 2>/dev/null; " +
		"add-zsh-hook -d precmd _atuin_precmd 2>/dev/null"
}

func (zshAdapter) sentinelEcho() string {
	return "print -r -- " + sentinel + "0" + sentinel
}

func (zshAdapter) job(p jobParams) string {
	qcwd := shquote(p.cwdf)
	trapBody := "builtin pwd >| " + qcwd
	qo := shquote(p.o)
	qe := shquote(p.e)
	vp := "" +
		"export LAST_EXCODE=${(q)__apb_rc}\n" +
		"export LAST_STDOUT=${(q)\"$(<" + qo + ")\"}\n" +
		"export LAST_STDERR=${(q)\"$(<" + qe + ")\"}\n"
	if p.id != "" {
		key := p.key
		vp += "" +
			"export APB_OUT_" + key + "=${(q)\"$(<" + qo + ")\"}\n" +
			"export APB_ERR_" + key + "=${(q)\"$(<" + qe + ")\"}\n" +
			"export APB_EXIT_" + key + "=${(q)__apb_rc}\n"
	}
	return "( trap " + shquote(trapBody) + " EXIT\n" + p.cmdline + "\n) </dev/null >" + p.o + " 2>" + p.e + "\n" +
		"__apb_rc=$?\n" +
		"if [[ $__apb_rc -eq 141 ]]; then __apb_rc=0; fi\n" +
		"if [[ -s " + qcwd + " ]]; then builtin cd -- \"$(< " + qcwd + ")\" 2>/dev/null; fi\n" +
		vp +
		"print -r -- " + sentinel + "${__apb_rc}" + sentinel + "\n"
}
