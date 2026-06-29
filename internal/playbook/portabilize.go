package playbook

import (
	"regexp"
	"strings"
)

// Portabilize rewrites machine-specific absolute path prefixes in the playbook's
// RUNNABLE (non-static) code blocks + the top-level verify into shell variables, so
// a project_bound playbook relocates without a model adapt: projectRoot → $PROJECT_ROOT
// (the host sets it at run), then home → $HOME. projectRoot is applied FIRST (most
// specific — a project under home variabilizes the project, not just home). Matching
// is path-component-boundary-safe (the prefix must be preceded by start/space/quote/
// =/( and followed by /, end, space, quote, :, )) so a coincidental substring is never
// mangled. Static blocks (literal output) and prose are left untouched. Mutates pb.
func Portabilize(pb *Playbook, projectRoot, home string) {
	rewrite := func(s string) string {
		s = replacePathPrefix(s, projectRoot, "$PROJECT_ROOT")
		s = replacePathPrefix(s, home, "$HOME")
		return s
	}
	for si := range pb.Sections {
		for ci := range pb.Sections[si].Content {
			it := &pb.Sections[si].Content[ci]
			if it.Kind == "code" && !it.Static {
				it.Code = rewrite(it.Code)
			}
		}
	}
	if pb.Verify != nil {
		pb.Verify.Code = rewrite(pb.Verify.Code)
	}
}

// replacePathPrefix replaces prefix with repl wherever prefix appears as a whole path
// component — preceded by a start/separator boundary and followed by a path boundary
// — preserving the surrounding boundary characters. Empty prefix → no change.
func replacePathPrefix(s, prefix, repl string) string {
	if prefix == "" {
		return s
	}
	re := regexp.MustCompile(`(^|[\s"'=(])` + regexp.QuoteMeta(prefix) + `(/|$|[\s"':)])`)
	// Escape $ in repl so Go's replacement engine doesn't interpret $HOME /
	// $PROJECT_ROOT as named-group references (which would expand to empty).
	escapedRepl := strings.ReplaceAll(repl, "$", "$$")
	return re.ReplaceAllString(s, `${1}`+escapedRepl+`${2}`)
}
