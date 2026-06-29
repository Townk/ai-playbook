// Package diff parses already-authored unified patches and renders them.
package diff

import "strings"

type Op int

const (
	OpContext Op = iota
	OpDel
	OpAdd
)

type Line struct {
	Op   Op
	Text string
}
type Hunk struct{ Lines []Line }
type FileDiff struct {
	OldPath, NewPath string
	Hunks            []Hunk
}

// Parse turns a unified patch into structured file diffs. It drives off the body
// lines, never the @@ header counts (agents miscount them), so a wrong count never
// truncates a hunk.
func Parse(patch string) []FileDiff {
	var files []FileDiff
	var cur *FileDiff
	var hunk *Hunk
	for _, ln := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(ln, "--- "):
			files = append(files, FileDiff{OldPath: strings.TrimSpace(ln[4:])})
			cur, hunk = &files[len(files)-1], nil
		case strings.HasPrefix(ln, "+++ ") && cur != nil:
			cur.NewPath = strings.TrimSpace(ln[4:])
		case strings.HasPrefix(ln, "diff --git"):
			// tolerate a `diff --git` lead-in before ---/+++; ignore.
		case strings.HasPrefix(ln, "@@"):
			if cur == nil { // a hunk with no file header — synthesize one
				files = append(files, FileDiff{})
				cur = &files[len(files)-1]
			}
			cur.Hunks = append(cur.Hunks, Hunk{})
			hunk = &cur.Hunks[len(cur.Hunks)-1]
		case hunk != nil && strings.HasPrefix(ln, "-"):
			hunk.Lines = append(hunk.Lines, Line{OpDel, ln[1:]})
		case hunk != nil && strings.HasPrefix(ln, "+"):
			hunk.Lines = append(hunk.Lines, Line{OpAdd, ln[1:]})
		case hunk != nil && strings.HasPrefix(ln, " "):
			hunk.Lines = append(hunk.Lines, Line{OpContext, ln[1:]})
		default:
			// `\ No newline at end of file`, index lines, etc. — ignore.
		}
	}
	return files
}
