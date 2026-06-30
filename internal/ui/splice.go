package ui

import "strings"

// replaceBlockBody replaces the body of the fenced block tagged {id=<id>…} in md with
// newBody, keeping the opening and closing fence lines. Returns ok=false if not found.
func replaceBlockBody(md, id, newBody string) (string, bool) {
	lines := strings.Split(md, "\n")
	open := -1
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") && (strings.Contains(t, "{id="+id+"}") || strings.Contains(t, "{id="+id+" ")) {
			open = i
			break
		}
	}
	if open == -1 {
		return md, false
	}
	closeIdx := -1
	for i := open + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "```" {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		return md, false
	}
	body := strings.Split(strings.TrimRight(newBody, "\n"), "\n")
	out := append([]string{}, lines[:open+1]...)
	out = append(out, body...)
	out = append(out, lines[closeIdx:]...)
	return strings.Join(out, "\n"), true
}
