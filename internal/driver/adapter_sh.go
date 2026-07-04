package driver

// shAdapter renders job scripts for an interactive POSIX sh / dash (`sh -i`).
// The portable structure (errexit subshell + EXIT-trap cwd capture + 141→0
// SIGPIPE remap + cd re-apply + sentinel) is identical to the zsh/bash adapters;
// only the shell-specific tokens differ for strict POSIX sh (dash):
//
//   - spawn:         -i        (dash has no -l rc model like zsh/bash -il)
//   - source:        . job     (dash has no `source`; POSIX dot)
//   - sentinel:      printf '%s\n' (no zsh `print -r --`)
//   - conditionals:  [ ]       (dash has no `[[ ]]`)
//   - file reads:    $(cat f)  (dash's $(<f) yields EMPTY — must use cat)
//   - cd/pwd:        plain     (dash has no `builtin`)
//   - cwd trap:      pwd >| <path>  (>| verified to work in dash for noclobber override)
//   - value-quoting: a pure-shell single-quote quoter (decision (b)) — dash lacks
//     printf %q and ${(q)}; sed is avoided (BSD/GNU sed differ). __apb_q wraps a
//     value in '…' and rewrites each embedded ' as '\” using POSIX parameter
//     expansion + printf only, so $APB_OUT_<id>/$LAST_STDOUT are stored
//     shell-quoted (word-split/glob-safe to re-expand), matching zsh/bash.
type shAdapter struct{}

// shQuoterFunc is the pure-shell single-quote quoter emitted into the job script.
// It single-quote-escapes $1: open with ', append each chunk up to an embedded ',
// emit '\” for that ', then close with '. Uses only POSIX parameter expansion
// (%% / #) and printf — no external tools. Round-trip: eval "x=$(__apb_q "$v")"
// reproduces $v exactly (verified by TestShQuoterRoundTrip).
const shQuoterFunc = `__apb_q() { __r=$1; __o="'"; while [ -n "$__r" ]; do case "$__r" in *\'*) __o="$__o${__r%%\'*}'\''"; __r=${__r#*\'};; *) __o="$__o$__r"; __r=;; esac; done; printf '%s' "$__o'"; }`

func (shAdapter) name() string                    { return "sh" }
func (shAdapter) spawnArgs() []string             { return []string{"-i"} }
func (shAdapter) jobExt() string                  { return "sh" }
func (shAdapter) sourceCmd(jobPath string) string { return ". " + jobPath }
func (shAdapter) cdCmd(target string) string {
	return "cd -- " + shquote(target) + " 2>/dev/null"
}

// historyOff disables on-disk history. POSIX sh/dash has no atuin integration of
// its own and minimal interactive history, so HISTFILE=/dev/null suffices.
func (shAdapter) historyOff() string {
	return "HISTFILE=/dev/null; export HISTFILE"
}

// historyShimFiles returns nil: POSIX sh has no atuin integration and uses the
// runtime historyOff path.
func (shAdapter) historyShimFiles() map[string]string { return nil }

func (shAdapter) sentinelEcho(nonce string) string {
	return "printf '%s\\n' " + shquote(sentinel+nonce+"_0"+sentinel)
}

func (shAdapter) job(p jobParams) string {
	qcwd := shquote(p.cwdf)
	trapBody := "pwd >| " + qcwd
	qo := shquote(p.o)
	qe := shquote(p.e)
	// Value-passing: store the pure-shell single-quote-quoted capture so it
	// re-expands word-split- and glob-safely. Exit codes are bare integers.
	vp := shQuoterFunc + "\n" +
		"export LAST_EXCODE=$__apb_rc\n" +
		"export LAST_STDOUT=\"$(__apb_q \"$(cat " + qo + ")\")\"" + "\n" +
		"export LAST_STDERR=\"$(__apb_q \"$(cat " + qe + ")\")\"" + "\n"
	if p.id != "" {
		key := p.key
		vp += "" +
			"export APB_OUT_" + key + "=\"$(__apb_q \"$(cat " + qo + ")\")\"" + "\n" +
			"export APB_ERR_" + key + "=\"$(__apb_q \"$(cat " + qe + ")\")\"" + "\n" +
			"export APB_EXIT_" + key + "=$__apb_rc\n"
	}
	return "( trap " + shquote(trapBody) + " EXIT\n" + p.cmdline + "\n) </dev/null >" + p.o + " 2>" + p.e + "\n" +
		"__apb_rc=$?\n" +
		"if [ $__apb_rc -eq 141 ]; then __apb_rc=0; fi\n" +
		"if [ -s " + qcwd + " ]; then cd -- \"$(cat " + qcwd + ")\" 2>/dev/null; fi\n" +
		vp +
		"printf '%s\\n' " + sentinel + p.nonce + "_${__apb_rc}" + sentinel + "\n"
}
