package diff

import (
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

	"github.com/Townk/ai-playbook/internal/theme"
)

const minSideBySide = 80

// Narrow reports whether a content width is too small for the two-column
// side-by-side/gutter layout, so callers fall back to the unified render.
func Narrow(width int) bool { return width < minSideBySide }

// Local diff background colours — same hex as ui/theme.go diffAdd/DelBgANSI.
const (
	diffAddBg = "#2a3b2e" // dark green tint  (RGB 42,59,46)
	diffDelBg = "#3b2a2e" // dark red tint    (RGB 59,42,46)
)

// Raw background SGR sequences re-asserted after chroma's per-token \x1b[0m
// resets (see the codeBgANSI comment in ui/theme.go). Every side-by-side cell
// is painted with exactly one of these so nothing (add/del tint OR the dialog
// background) leaks through the highlighter's resets or through unpainted cells:
//   - delBgANSI   — deleted line (left column)
//   - addBgANSI   — added line   (right column)
//   - dialogBgANSI — context/blank cells and the vertical " │ " divider; matches
//     ui.colMantle, the diff dialog's background, so context cells blend into it.
var (
	addBgANSI    = theme.BgANSI(diffAddBg)
	delBgANSI    = theme.BgANSI(diffDelBg)
	dialogBgANSI = theme.BgANSI(theme.Mantle)
	// gutterFgANSI is the dim foreground for the line-number gutters (colOverlay0,
	// the codebase's muted-label color). It is a foreground-only sequence: the
	// gutter's background is the row's own bg (dialog or add/del tint).
	gutterFgANSI = theme.FgANSI(theme.Overlay0)
	// Header foregrounds: the "--- old" path is red and the "+++ new" path green
	// (same hues as the unified view's del/add), so the header reads as a header —
	// not code — at a glance. Foreground-only; the cell keeps the dialog bg.
	headerDelFgANSI = theme.FgANSI("#f38ba8") // Catppuccin Red  — old-path header
	headerAddFgANSI = theme.FgANSI("#a6e3a1") // Catppuccin Green — new-path header
)

// Render turns parsed FileDiffs into a display string. When width is at least
// minSideBySide it produces a two-column side-by-side view; narrower terminals
// get a unified (single-column) view. highlightFn is injected so the package
// stays UI-decoupled; tests pass an identity function.
func Render(files []FileDiff, width int, highlightFn func(code, lang string) string) string {
	if width < minSideBySide {
		return renderUnified(files, highlightFn)
	}
	return renderSideBySide(files, width, highlightFn)
}

// renderSideBySide lays each hunk into two columns: left=old (context+del),
// right=new (context+add). A run of dels then adds is paired row-by-row; the
// shorter side is blank-padded. Content is highlighted via highlightFn then
// add/del-tinted; a file header line precedes each file.
func renderSideBySide(files []FileDiff, width int, highlightFn func(string, string) string) string {
	colWidth := (width - 3) / 2

	var sb strings.Builder

	// joinRow lays a left cell + divider + right cell into one full-width line.
	// The divider carries the dialog bg so the │ runs as one unbroken vertical
	// line down every row (header included); the trailing reset closes the row.
	joinRow := func(left, right string) {
		sb.WriteString(left)
		sb.WriteString(dialogBgANSI + " │ ")
		sb.WriteString(right)
		sb.WriteString("\x1b[0m\n")
	}

	for _, f := range files {
		lang := LangFromPath(f.NewPath)

		// Two-column header (F14): "--- old" and "+++ new" become the LEFT and
		// RIGHT panes so the divider is continuous from the top row down. Bold is
		// toggled with 1m/22m (not a reset) so the cell bg is preserved.
		bold := func(s, _ string) string { return "\x1b[1m" + s + "\x1b[22m" }
		joinRow(
			paintCell("--- "+f.OldPath, dialogBgANSI, "", colWidth, bold),
			paintCell("+++ "+f.NewPath, dialogBgANSI, "", colWidth, bold),
		)

		for _, hunk := range f.Hunks {
			type row struct {
				left, right string
				leftOp      Op
				rightOp     Op
			}

			var rows []row
			var dels, adds []string

			flushRun := func() {
				n := max(len(dels), len(adds))
				for i := range n {
					l, r := "", ""
					lop, rop := OpContext, OpContext
					if i < len(dels) {
						l = dels[i]
						lop = OpDel
					}
					if i < len(adds) {
						r = adds[i]
						rop = OpAdd
					}
					rows = append(rows, row{l, r, lop, rop})
				}
				dels = dels[:0]
				adds = adds[:0]
			}

			for _, ln := range hunk.Lines {
				// Tabs are expanded here, at row-build time (A6b, see expandTabs).
				text := expandTabs(ln.Text)
				switch ln.Op {
				case OpContext:
					flushRun()
					rows = append(rows, row{text, text, OpContext, OpContext})
				case OpDel:
					dels = append(dels, text)
				case OpAdd:
					adds = append(adds, text)
				}
			}
			flushRun()

			for _, r := range rows {
				// Del cells (left) get the red tint, add cells (right) the green
				// tint; every other cell gets the dialog bg so no cell is left with
				// the default background (F12/F13).
				leftBg := dialogBgANSI
				if r.leftOp == OpDel {
					leftBg = delBgANSI
				}
				rightBg := dialogBgANSI
				if r.rightOp == OpAdd {
					rightBg = addBgANSI
				}
				joinRow(
					paintCell(r.left, leftBg, lang, colWidth, highlightFn),
					paintCell(r.right, rightBg, lang, colWidth, highlightFn),
				)
			}
		}
	}

	return sb.String()
}

// paintCell renders one side-by-side column: it truncates text to colWidth (so
// the highlighter never wraps), highlights it, then forces the cell background
// so it survives chroma's per-token \x1b[0m resets. The bg is prefixed once and
// re-asserted after every reset in the highlighted text; the cell is padded to
// colWidth with bg-painted spaces so the whole column is filled. Foreground
// syntax colours are untouched — only the background is forced.
func paintCell(text, bg, lang string, colWidth int, highlightFn func(string, string) string) string {
	trunc := runewidth.Truncate(text, colWidth, "")
	hl := highlightFn(trunc, lang)
	// Re-assert the bg after every reset the highlighter emits.
	hl = strings.ReplaceAll(hl, "\x1b[0m", "\x1b[0m"+bg)
	pad := colWidth - lipgloss.Width(trunc)
	if pad < 0 {
		pad = 0
	}
	return bg + hl + strings.Repeat(" ", pad)
}

// DividerRow returns a full-width blank row painted with the dialog background and
// carrying the vertical " │ " pane divider, so a caller padding the side-by-side view to
// a fixed height keeps the divider running unbroken to the bottom. For widths below the
// side-by-side threshold (a unified render, no divider) it returns a plain dialog-bg row.
func DividerRow(width int) string {
	if width < minSideBySide {
		if width < 0 {
			width = 0
		}
		return dialogBgANSI + strings.Repeat(" ", width) + "\x1b[0m"
	}
	colWidth := (width - 3) / 2
	blank := dialogBgANSI + strings.Repeat(" ", colWidth)
	return blank + dialogBgANSI + " │ " + blank + "\x1b[0m"
}

// Row is one structured side-by-side line: the un-highlighted (but
// tab-expanded — see expandTabs) left/right text plus each side's op and
// gutter number. It is built once (Rows) and
// rendered per frame (RenderRow) so horizontal scroll and gutters can be applied
// at display time. A number of 0 means "no gutter number" (blank cell); Header
// marks the per-file "--- old" / "+++ new" row (rendered bold, no numbers).
type Row struct {
	LeftNo, RightNo int
	Left, Right     string
	LeftOp, RightOp Op
	Header          bool
}

// Rows flattens parsed FileDiffs into structured side-by-side rows, tracking the
// old/new line numbers from each hunk's @@ START. Context lines carry both
// numbers and both texts; a del/add run is paired row-by-row (as renderSideBySide
// does) with the shorter side left blank (number 0). Each file is preceded by a
// Header row.
func Rows(files []FileDiff) []Row {
	var rows []Row
	for _, f := range files {
		rows = append(rows, Row{
			Left:   "--- " + f.OldPath,
			Right:  "+++ " + f.NewPath,
			Header: true,
		})

		for _, hunk := range f.Hunks {
			oldNo, newNo := hunk.OldStart, hunk.NewStart
			var dels, adds []string

			flushRun := func() {
				n := max(len(dels), len(adds))
				for i := range n {
					r := Row{LeftOp: OpContext, RightOp: OpContext}
					if i < len(dels) {
						r.Left = dels[i]
						r.LeftOp = OpDel
						r.LeftNo = oldNo
						oldNo++
					}
					if i < len(adds) {
						r.Right = adds[i]
						r.RightOp = OpAdd
						r.RightNo = newNo
						newNo++
					}
					rows = append(rows, r)
				}
				dels = dels[:0]
				adds = adds[:0]
			}

			for _, ln := range hunk.Lines {
				// Tabs are expanded here, at row-build time, so every Row
				// consumer — width measurement included — sees no '\t' (A6b,
				// see expandTabs).
				text := expandTabs(ln.Text)
				switch ln.Op {
				case OpContext:
					flushRun()
					rows = append(rows, Row{
						LeftNo: oldNo, RightNo: newNo,
						Left: text, Right: text,
						LeftOp: OpContext, RightOp: OpContext,
					})
					oldNo++
					newNo++
				case OpDel:
					dels = append(dels, text)
				case OpAdd:
					adds = append(adds, text)
				}
			}
			flushRun()
		}
	}
	return rows
}

// RenderRow renders one structured Row into a full-width display line:
//
//	<left gutter> <left text> │ <right gutter> <right text>
//
// The gutters are gutterW wide, right-aligned dim numbers (or blanks) on the
// row's own bg, and NEVER scroll. Only the text columns scroll: each side's text
// is windowed by dropping its first leftXOff/rightXOff display columns BEFORE
// highlighting (so long lines reveal their tail), then painted/truncated to
// textCol via paintCell (bg survives the highlighter's resets). Header rows are
// bold with empty gutters. The line ends with a single reset.
func RenderRow(row Row, leftXOff, rightXOff, textCol, gutterW int, lang string, highlightFn func(string, string) string) string {
	leftBg, rightBg := dialogBgANSI, dialogBgANSI
	leftHl, rightHl := highlightFn, highlightFn
	if row.Header {
		// The header is PINNED — it never scrolls with the content, so the file
		// paths stay visible however far the code is scrolled. Each side is tinted
		// (red old / green new) via a bold foreground that resets fg+bold (not a
		// full reset) so the cell bg survives.
		leftXOff, rightXOff = 0, 0
		leftHl = headerHl(headerDelFgANSI)
		rightHl = headerHl(headerAddFgANSI)
	} else {
		if row.LeftOp == OpDel {
			leftBg = delBgANSI
		}
		if row.RightOp == OpAdd {
			rightBg = addBgANSI
		}
	}

	var sb strings.Builder
	// Left gutter + separating space, then the windowed+painted left text.
	sb.WriteString(gutterCell(row.LeftNo, gutterW, leftBg))
	sb.WriteString(leftBg + "\x1b[39m ")
	sb.WriteString(paintCell(dropCols(row.Left, leftXOff), leftBg, lang, textCol, leftHl))
	// The divider carries the dialog bg so │ runs unbroken down every row.
	sb.WriteString(dialogBgANSI + " │ ")
	// Right gutter + separating space, then the windowed+painted right text.
	sb.WriteString(gutterCell(row.RightNo, gutterW, rightBg))
	sb.WriteString(rightBg + "\x1b[39m ")
	sb.WriteString(paintCell(dropCols(row.Right, rightXOff), rightBg, lang, textCol, rightHl))
	sb.WriteString("\x1b[0m")
	return sb.String()
}

// headerHl returns a highlight function that renders a diff header path bold in the
// given foreground (red for "--- old", green for "+++ new"), resetting bold+fg after —
// 22m/39m, not a full reset — so the cell background survives.
func headerHl(fg string) func(string, string) string {
	return func(s, _ string) string { return "\x1b[1m" + fg + s + "\x1b[22m\x1b[39m" }
}

// gutterCell renders a fixed-width, right-aligned line number (or blanks when
// no==0) in the dim gutter foreground on the given bg. The trailing digits keep
// the dim fg; the caller resets the fg (\x1b[39m) before the text column so the
// dim colour never bleeds into the code.
func gutterCell(no, gutterW int, bg string) string {
	s := ""
	if no > 0 {
		s = strconv.Itoa(no)
	}
	if pad := gutterW - len(s); pad > 0 {
		s = strings.Repeat(" ", pad) + s
	}
	return bg + gutterFgANSI + s
}

// dropCols drops the first n display columns from a plain (un-highlighted)
// string, keeping every rune whose starting column is ≥ n — this windows a long
// line so its tail is revealed. n≤0 returns the string unchanged. Its input is
// tab-free (Rows expanded tabs at build time), so a rune's display column here
// is exactly its terminal column.
func dropCols(s string, n int) string {
	if n <= 0 {
		return s
	}
	col := 0
	for i, r := range s {
		if col >= n {
			return s[i:]
		}
		col += runewidth.RuneWidth(r)
	}
	return ""
}

// tabStop is the fixed column width tabs expand to (A6b). `runewidth.Truncate`/
// `RuneWidth` and `lipgloss.Width` all count '\t' as zero-width, but a real
// terminal advances the cursor to the next tab-stop column — left unexpanded, a
// tab-indented (e.g. Go) patch overflows its cell (the "│" divider drifts
// off-column) and dropCols mis-windows on horizontal scroll (its column count
// disagrees with what the terminal actually draws). 8 is the near-universal
// terminal default tab-stop width, so expanding to it reproduces what the
// user's terminal would render natively — a fixed 4- (or N-)space replacement
// would be simpler but would only coincidentally match real rendering.
const tabStop = 8

// expandTabs replaces every '\t' in s with spaces out to the next tabStop
// column, measured in display columns from the START of s (column 0) — i.e.
// relative to the actual line content, not any horizontal-scroll window, which
// matches how a terminal computes tab stops for the line. It is applied ONCE,
// at display BUILD time — Rows (the structured path: the UI overlay measures
// lipgloss.Width(Row.Left/Right) for its horizontal-scroll bound, then
// RenderRow windows/paints), renderSideBySide's row build, and renderUnified —
// so every downstream display consumer sees plain spaces, never '\t'.
// Deliberately NOT applied in Parse: ConflictMarkup matches parsed hunk text
// byte-for-byte against the raw (tab-containing) file and splices the patch's
// lines into it, so Parse-level expansion would break anchoring on
// tab-indented files and swap the user's tabs for spaces in the spliced
// conflict block.
func expandTabs(s string) string {
	if !strings.ContainsRune(s, '\t') {
		return s
	}
	var sb strings.Builder
	col := 0
	for _, r := range s {
		if r == '\t' {
			n := tabStop - col%tabStop
			sb.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		sb.WriteRune(r)
		col += runewidth.RuneWidth(r)
	}
	return sb.String()
}

// renderUnified emits a single column: a file header, then `-`/`+`/` ` prefixed,
// red/green-colored lines (the inline-diff look) — for narrow terminals.
func renderUnified(files []FileDiff, highlightFn func(string, string) string) string {
	addSty := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")) // Catppuccin Green
	delSty := lipgloss.NewStyle().Foreground(lipgloss.Color("#f38ba8")) // Catppuccin Red
	headerSty := lipgloss.NewStyle().Bold(true)

	var sb strings.Builder

	for _, f := range files {
		header := "--- " + f.OldPath + "  +++ " + f.NewPath
		sb.WriteString(headerSty.Render(header))
		sb.WriteByte('\n')

		lang := LangFromPath(f.NewPath)

		for _, hunk := range f.Hunks {
			for _, ln := range hunk.Lines {
				// Tabs expanded at build time here too (A6b, see expandTabs).
				hl := highlightFn(expandTabs(ln.Text), lang)
				switch ln.Op {
				case OpDel:
					sb.WriteString(delSty.Render("-" + hl))
				case OpAdd:
					sb.WriteString(addSty.Render("+" + hl))
				case OpContext:
					sb.WriteString(" " + hl)
				}
				sb.WriteByte('\n')
			}
		}
	}

	return sb.String()
}

// LangFromPath returns the file extension without the leading dot, used as a
// syntax-highlighting language hint.
func LangFromPath(p string) string {
	return strings.TrimPrefix(filepath.Ext(p), ".")
}
