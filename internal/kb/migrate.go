package kb

import "strings"

// isSectioned reports whether content is already in sectioned form — i.e. it has
// at least one `## ` heading. A non-empty file WITHOUT any section heading is a
// legacy flat file (bullets only), which LoadProject reads as ## Environment and
// the first sectioned Append rewrites in place.
func isSectioned(content string) bool {
	for _, ln := range strings.Split(content, "\n") {
		if strings.HasPrefix(ln, "## ") {
			return true
		}
	}
	return false
}
