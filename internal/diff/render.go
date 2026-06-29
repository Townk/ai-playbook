package diff

import (
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
)

const minSideBySide = 80

// Local diff background colours — same hex as ui/theme.go diffAdd/DelBgANSI.
const (
	diffAddBg = "#2a3b2e" // dark green tint  (RGB 42,59,46)
	diffDelBg = "#3b2a2e" // dark red tint    (RGB 59,42,46)
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

	addSty := lipgloss.NewStyle().Background(lipgloss.Color(diffAddBg))
	delSty := lipgloss.NewStyle().Background(lipgloss.Color(diffDelBg))
	plainSty := lipgloss.NewStyle()
	headerSty := lipgloss.NewStyle().Bold(true)

	var sb strings.Builder

	for _, f := range files {
		header := "--- " + f.OldPath + "  +++ " + f.NewPath
		sb.WriteString(headerSty.Render(header))
		sb.WriteByte('\n')

		lang := langFromPath(f.NewPath)

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
				switch ln.Op {
				case OpContext:
					flushRun()
					rows = append(rows, row{ln.Text, ln.Text, OpContext, OpContext})
				case OpDel:
					dels = append(dels, ln.Text)
				case OpAdd:
					adds = append(adds, ln.Text)
				}
			}
			flushRun()

			for _, r := range rows {
				// Truncate plain text to colWidth before highlighting so that
				// lipgloss .Width(colWidth) only pads (never wraps) the cell.
				leftTrunc := runewidth.Truncate(r.left, colWidth, "")
				rightTrunc := runewidth.Truncate(r.right, colWidth, "")
				leftHL := highlightFn(leftTrunc, lang)
				rightHL := highlightFn(rightTrunc, lang)

				var leftSty, rightSty lipgloss.Style
				if r.leftOp == OpDel {
					leftSty = delSty
				} else {
					leftSty = plainSty
				}
				if r.rightOp == OpAdd {
					rightSty = addSty
				} else {
					rightSty = plainSty
				}

				sb.WriteString(leftSty.Width(colWidth).Render(leftHL))
				sb.WriteString(" │ ")
				sb.WriteString(rightSty.Width(colWidth).Render(rightHL))
				sb.WriteByte('\n')
			}
		}
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

		lang := langFromPath(f.NewPath)

		for _, hunk := range f.Hunks {
			for _, ln := range hunk.Lines {
				hl := highlightFn(ln.Text, lang)
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

// langFromPath returns the file extension without the leading dot, used as a
// syntax-highlighting language hint.
func langFromPath(p string) string {
	return strings.TrimPrefix(filepath.Ext(p), ".")
}
