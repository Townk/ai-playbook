// Package diff parses already-authored unified patches and renders them.
package diff

import (
	"regexp"
	"strconv"
	"strings"
)

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

// Hunk carries the @@ START line numbers (OldStart/NewStart) so the side-by-side
// renderer can number gutters; the @@ COUNTS are still ignored (agents miscount
// them) and line CONTENT is always driven off the body, never the header.
type Hunk struct {
	OldStart, NewStart int
	Lines              []Line
}

// hunkRe captures the two START numbers from `@@ -old[,count] +new[,count] @@`.
// The counts are optional (unified diffs omit them for single-line ranges) and
// deliberately uncaptured. A malformed header simply doesn't match, leaving the
// starts at their zero value.
var hunkRe = regexp.MustCompile(`^@@+ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

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
	for _, ln := range strings.Split(strings.TrimSuffix(patch, "\n"), "\n") {
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
			if mm := hunkRe.FindStringSubmatch(ln); mm != nil {
				hunk.OldStart, _ = strconv.Atoi(mm[1])
				hunk.NewStart, _ = strconv.Atoi(mm[2])
			}
		case hunk != nil && strings.HasPrefix(ln, "-"):
			hunk.Lines = append(hunk.Lines, Line{OpDel, ln[1:]})
		case hunk != nil && strings.HasPrefix(ln, "+"):
			hunk.Lines = append(hunk.Lines, Line{OpAdd, ln[1:]})
		case hunk != nil && strings.HasPrefix(ln, " "):
			hunk.Lines = append(hunk.Lines, Line{OpContext, ln[1:]})
		case hunk != nil && ln == "":
			// bare blank context line (no leading space) — common in agents' patches.
			hunk.Lines = append(hunk.Lines, Line{OpContext, ""})
		default:
			// `\ No newline at end of file`, index lines, etc. — ignore.
		}
	}
	return files
}
