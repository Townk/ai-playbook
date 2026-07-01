package diff

import (
	"sort"
	"strings"
)

// fenceWidth is the nominal column width the conflict-block label/rule lines are
// padded to with trailing dashes. It is cosmetic — the markers are recognised by
// their `-[current]` / `-[expected]` / `-[proposed]` prefixes, not their length.
const fenceWidth = 48

// Conflict-block marker prefixes (a 3-way view: what the file has now, what the patch
// expected to find, what it proposes). All three OPENER labels are what
// HasConflictMarkers keys off: while any survives in a file, resolution isn't finished.
const (
	markerCurrent  = "-[current]"
	markerExpected = "-[expected]"
	markerProposed = "-[proposed]"
)

// ConflictMarkup renders fileContent with a git-conflict-style block spliced in at
// every drifted hunk, so the user can see — in place — what the file currently has
// ("[current]"), what the patch expected to find ("[expected]"), and what it proposes
// ("[proposed]"), reconcile it by hand, and save. It anchors each hunk on its
// surrounding context lines rather than the (frequently miscounted) @@ line numbers.
//
// It returns ok=false when NOT ONE hunk could be located (the caller then falls back
// to opening the raw file). When some hunks locate and others don't, the locatable
// ones are marked and ok=true; the unlocatable ones are silently skipped.
//
// Location algorithm, per hunk:
//   - pre  = the run of leading context lines (before the first del/add).
//   - post = the run of trailing context lines (after the last del/add).
//   - oldChange/newChange = the del / add lines of the change region.
//   - p := index just AFTER where `pre` occurs as consecutive lines in the file
//     (when pre is empty, anchor at the hunk's OldStart, else file start).
//   - q := index where `post` begins at/after p (when post is empty or unfound,
//     fall back to p + len(oldChange), i.e. the del-count region).
//   - When pre can't be found but post can, anchor from post instead (q = post's
//     index, p = q - len(oldChange)). If neither anchors, the hunk is unlocatable.
//
// The drifted region is fileLines[p:q]; the block replaces it. A hunk with several
// separated change groups is treated as one region spanning first-change..last-change.
func ConflictMarkup(fileContent string, files []FileDiff) (marked string, ok bool) {
	fileLines := strings.Split(fileContent, "\n")

	type region struct {
		p, q  int
		block []string
	}
	var regions []region

	for _, f := range files {
		for _, h := range f.Hunks {
			pre, post, oldChange, newChange, hasChange := splitHunk(h)
			if !hasChange {
				continue // pure-context hunk: nothing to reconcile
			}

			p, q := -1, -1
			if len(pre) > 0 {
				if idx := indexOfSeq(fileLines, pre, 0); idx >= 0 {
					p = idx + len(pre)
				}
			} else {
				// Empty pre → anchor at the hunk's declared old start (1-based),
				// falling back to the file head.
				p = h.OldStart - 1
				if p < 0 || p > len(fileLines) {
					p = 0
				}
			}

			if p >= 0 && len(post) > 0 {
				if idx := indexOfSeq(fileLines, post, p); idx >= 0 {
					q = idx
				}
			}
			if q < 0 && p >= 0 {
				// post empty or unfound → take the del-count worth of lines.
				q = p + len(oldChange)
				if q > len(fileLines) {
					q = len(fileLines)
				}
			}
			// pre unfound but post found → anchor backwards from post.
			if p < 0 && len(post) > 0 {
				if idx := indexOfSeq(fileLines, post, 0); idx >= 0 {
					q = idx
					p = q - len(oldChange)
					if p < 0 {
						p = idx
					}
				}
			}

			if p < 0 || q < 0 || p > q || q > len(fileLines) {
				continue // unlocatable — contributes to ok=false
			}
			regions = append(regions, region{p: p, q: q, block: conflictBlock(fileLines[p:q], oldChange, newChange)})
		}
	}

	if len(regions) == 0 {
		return "", false
	}

	// Emit in file order; drop any region that overlaps one already emitted.
	sort.SliceStable(regions, func(i, j int) bool { return regions[i].p < regions[j].p })
	var out []string
	cursor := 0
	for _, r := range regions {
		if r.p < cursor {
			continue
		}
		out = append(out, fileLines[cursor:r.p]...) // unchanged lines up to & incl. pre
		out = append(out, r.block...)
		cursor = r.q
	}
	out = append(out, fileLines[cursor:]...) // remainder (incl. post)
	return strings.Join(out, "\n"), true
}

// splitHunk decomposes a hunk into its leading context run (pre), trailing context
// run (post), and the del/add lines of the change region between them. hasChange is
// false for a pure-context hunk (no del/add at all).
func splitHunk(h Hunk) (pre, post, oldChange, newChange []string, hasChange bool) {
	first, last := -1, -1
	for i, l := range h.Lines {
		if l.Op != OpContext {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return nil, nil, nil, nil, false
	}
	for _, l := range h.Lines[:first] {
		pre = append(pre, l.Text)
	}
	for _, l := range h.Lines[last+1:] {
		post = append(post, l.Text)
	}
	for _, l := range h.Lines[first : last+1] {
		switch l.Op {
		case OpDel:
			oldChange = append(oldChange, l.Text)
		case OpAdd:
			newChange = append(newChange, l.Text)
		}
	}
	return pre, post, oldChange, newChange, true
}

// conflictBlock builds the 3-way labelled-fence block: the file's current drifted
// region under `-[current]`, what the patch expected to find under `-[expected]`, and
// the patch's proposed replacement under `-[proposed]`, closed by a plain dashed rule.
// The `-[expected]` / `-[proposed]` sections are omitted when that side has no lines —
// a pure insertion has no expected lines, a pure deletion no proposed ones — so the
// block scales cleanly from a single-line tweak to a multi-line change.
func conflictBlock(current, oldChange, newChange []string) []string {
	block := []string{labelLine(markerCurrent)}
	block = append(block, current...)
	if len(oldChange) > 0 {
		block = append(block, labelLine(markerExpected))
		block = append(block, oldChange...)
	}
	if len(newChange) > 0 {
		block = append(block, labelLine(markerProposed))
		block = append(block, newChange...)
	}
	block = append(block, strings.Repeat("-", fenceWidth))
	return block
}

// labelLine pads a marker label to fenceWidth with trailing dashes (always at least
// three, so an over-long label still reads as a fence).
func labelLine(label string) string {
	if pad := fenceWidth - len(label); pad > 0 {
		return label + strings.Repeat("-", pad)
	}
	return label + "---"
}

// HasConflictMarkers reports whether content still carries an UNRESOLVED conflict
// block — i.e. any line begins with one of the three opener labels (`-[current]` /
// `-[expected]` / `-[proposed]`). The user resolves by deleting the openers and the
// sides they don't want; a stray plain `----` rule does NOT count.
func HasConflictMarkers(content string) bool {
	for _, ln := range strings.Split(content, "\n") {
		if strings.HasPrefix(ln, markerCurrent) ||
			strings.HasPrefix(ln, markerExpected) ||
			strings.HasPrefix(ln, markerProposed) {
			return true
		}
	}
	return false
}

// indexOfSeq returns the index of the first occurrence of needle as consecutive
// elements of hay at/after `from`, or -1. An empty needle matches at `from`.
func indexOfSeq(hay, needle []string, from int) int {
	if from < 0 {
		from = 0
	}
	if len(needle) == 0 {
		return from
	}
	for i := from; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
