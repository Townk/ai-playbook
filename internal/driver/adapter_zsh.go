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

// historyShimFiles returns the four zsh startup files for a temp ZDOTDIR shim.
// Each file sources the user's real counterpart AT TOP LEVEL (never inside a
// function — otherwise the user's `typeset`/non-exported vars would become
// function-locals and be lost) using temp globals __apb_real/__apb_shim and
// juggling $ZDOTDIR to the real dir while sourcing so the user's rc sees its own
// dir. APB_REAL_ZDOTDIR is injected by the driver; falling back to $HOME matches
// zsh's own default when ZDOTDIR is unset. Recording is hard-disabled at INIT —
// invisible to atuin's preexec and after atuin is fully armed regardless of
// powerlevel10k instant-prompt timing.
func (zshAdapter) historyShimFiles() map[string]string {
	return map[string]string{
		".zshenv": "" +
			"__apb_real=${APB_REAL_ZDOTDIR:-$HOME}; __apb_shim=$ZDOTDIR\n" +
			"ZDOTDIR=$__apb_real\n" +
			"[[ -r $__apb_real/.zshenv ]] && builtin source $__apb_real/.zshenv\n" +
			"ZDOTDIR=$__apb_shim\n" +
			"unset __apb_real __apb_shim\n",
		".zprofile": "" +
			"__apb_real=${APB_REAL_ZDOTDIR:-$HOME}; __apb_shim=$ZDOTDIR\n" +
			"ZDOTDIR=$__apb_real\n" +
			"[[ -r $__apb_real/.zprofile ]] && builtin source $__apb_real/.zprofile\n" +
			"ZDOTDIR=$__apb_shim\n" +
			"unset __apb_real __apb_shim\n",
		".zshrc": "" +
			"__apb_real=${APB_REAL_ZDOTDIR:-$HOME}\n" +
			"ZDOTDIR=$__apb_real\n" +
			"[[ -r $__apb_real/.zshrc ]] && builtin source $__apb_real/.zshrc\n" +
			"unset __apb_real\n" +
			"# The real rc (incl. atuin) is now loaded. Disable recording AT INIT — invisible to\n" +
			"# atuin's preexec, and after atuin is fully armed regardless of instant-prompt timing.\n" +
			"# ZDOTDIR is left = the real dir so a login shell's .zlogin is read from there.\n" +
			"HISTFILE=/dev/null\n" +
			"SAVEHIST=0\n" +
			"autoload -Uz add-zsh-hook 2>/dev/null\n" +
			"add-zsh-hook -d preexec _atuin_preexec 2>/dev/null\n" +
			"add-zsh-hook -d precmd _atuin_precmd 2>/dev/null\n" +
			"functions[_atuin_preexec]='' 2>/dev/null\n" +
			"functions[_atuin_precmd]='' 2>/dev/null\n",
		".zlogin": "" +
			"__apb_real=${APB_REAL_ZDOTDIR:-$HOME}\n" +
			"[[ -r $__apb_real/.zlogin ]] && ZDOTDIR=$__apb_real builtin source $__apb_real/.zlogin\n" +
			"unset __apb_real\n" +
			"HISTFILE=/dev/null\n" +
			"SAVEHIST=0\n" +
			"add-zsh-hook -d preexec _atuin_preexec 2>/dev/null\n" +
			"add-zsh-hook -d precmd _atuin_precmd 2>/dev/null\n" +
			"functions[_atuin_preexec]='' 2>/dev/null\n" +
			"functions[_atuin_precmd]='' 2>/dev/null\n",
	}
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
