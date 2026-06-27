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

func (bashAdapter) job(p jobParams) string {
	qcwd := shquote(p.cwdf)
	trapBody := "builtin pwd >| " + qcwd
	qo := shquote(p.o)
	qe := shquote(p.e)
	// printf %q quotes a value so it re-expands word-split- and glob-safely.
	// macOS ships bash 3.2 which lacks ${var@Q}, so printf %q is the portable
	// bash quoting primitive. Numbers (exit codes) are their own printf %q output,
	// so __aapb_rc is used bare there — printf %q of an integer is the integer.
	vp := "" +
		"export LAST_EXCODE=$__aapb_rc\n" +
		"export LAST_STDOUT=\"$(printf %q \"$(<" + qo + ")\")\"" + "\n" +
		"export LAST_STDERR=\"$(printf %q \"$(<" + qe + ")\")\"" + "\n"
	if p.id != "" {
		key := p.key
		vp += "" +
			"export AAS_OUT_" + key + "=\"$(printf %q \"$(<" + qo + ")\")\"" + "\n" +
			"export AAS_ERR_" + key + "=\"$(printf %q \"$(<" + qe + ")\")\"" + "\n" +
			"export AAS_EXIT_" + key + "=$__aapb_rc\n"
	}
	return "( trap " + shquote(trapBody) + " EXIT\n" + p.cmdline + "\n) </dev/null >" + p.o + " 2>" + p.e + "\n" +
		"__aapb_rc=$?\n" +
		"if [ $__aapb_rc -eq 141 ]; then __aapb_rc=0; fi\n" +
		"if [ -s " + qcwd + " ]; then builtin cd -- \"$(< " + qcwd + ")\" 2>/dev/null; fi\n" +
		vp +
		"printf '%s\\n' " + sentinel + "${__aapb_rc}" + sentinel + "\n"
}
