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

func (zshAdapter) job(p jobParams) string {
	qcwd := shquote(p.cwdf)
	trapBody := "builtin pwd >| " + qcwd
	qo := shquote(p.o)
	qe := shquote(p.e)
	vp := "" +
		"export LAST_EXCODE=${(q)__aapb_rc}\n" +
		"export LAST_STDOUT=${(q)\"$(<" + qo + ")\"}\n" +
		"export LAST_STDERR=${(q)\"$(<" + qe + ")\"}\n"
	if p.id != "" {
		key := p.key
		vp += "" +
			"export AAS_OUT_" + key + "=${(q)\"$(<" + qo + ")\"}\n" +
			"export AAS_ERR_" + key + "=${(q)\"$(<" + qe + ")\"}\n" +
			"export AAS_EXIT_" + key + "=${(q)__aapb_rc}\n"
	}
	return "( trap " + shquote(trapBody) + " EXIT\n" + p.cmdline + "\n) </dev/null >" + p.o + " 2>" + p.e + "\n" +
		"__aapb_rc=$?\n" +
		"if [[ $__aapb_rc -eq 141 ]]; then __aapb_rc=0; fi\n" +
		"if [[ -s " + qcwd + " ]]; then builtin cd -- \"$(< " + qcwd + ")\" 2>/dev/null; fi\n" +
		vp +
		"print -r -- " + sentinel + "${__aapb_rc}" + sentinel + "\n"
}
