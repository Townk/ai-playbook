package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// admon describes one admonition type: display title, nerd-font icon glyph, and
// palette color. The icon field is easy to tweak per-glyph if the user's font
// doesn't include a particular codepoint.
type admon struct {
	title string
	icon  string // nerd-font glyph; swap codepoint here if it renders as tofu
	color string // Catppuccin hex constant
}

var admonitions = map[string]admon{
	"note":      {"Note", "󰋽", colBlue},
	"tip":       {"Tip", "󰌶", colGreen},
	"important": {"Important", "󰀦", colMauve},
	"warning":   {"Warning", "󰀪", colPeach},
	"caution":   {"Caution", "󰳦", colRed},
	"quote":     {"Quote", "󱆨", colOverlay0},
}

// admonMarkerRe matches an optional leading [!TYPE] marker in a block-quote body.
var admonMarkerRe = regexp.MustCompile(`(?is)^\s*\[!(\w+)\]\s*`)

// strip removes ANSI/CSI escape sequences so callers can measure or assert on
// the visible text. ESC introduces a sequence; for CSI ("ESC [") it consumes
// the parameter/intermediate bytes and the final byte (0x40–0x7e).
func strip(s string) string {
	var b strings.Builder
	const (
		normal = iota
		sawESC
		inCSI
	)
	state := normal
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch state {
		case normal:
			if c == 0x1b {
				state = sawESC
			} else {
				b.WriteByte(c)
			}
		case sawESC:
			if c == '[' {
				state = inCSI
			} else {
				state = normal // non-CSI escape; sequence over
			}
		case inCSI:
			if c >= 0x40 && c <= 0x7e {
				state = normal // final byte, consumed
			}
		}
	}
	return b.String()
}

type renderer struct {
	src      []byte
	width    int
	lines    []Line
	buttons  []Button
	blocks   []Block
	states   map[string]blockRunState
	flashKey string // non-empty while a button is briefly highlighted; "<blockID>:<kind>"
}

// Render parses markdown and returns tagged, laid-out lines, a button
// registry, and the typed block list for a given pane width. states maps
// block IDs to their current run state; pass nil when not needed. flashKey
// is the identity of the button currently being flash-highlighted
// ("<blockID>:<kind>"); pass "" when no flash is active. Callers that don't
// need blocks can discard the third value with _.
func Render(md string, width int, states map[string]blockRunState, flashKey string) ([]Line, []Button, []Block) {
	if width < 1 {
		width = 1
	}
	src := []byte(md)
	gm := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := gm.Parser().Parse(text.NewReader(src))
	r := &renderer{src: src, width: width, states: states, flashKey: flashKey}
	r.block(doc, 0)
	r.blocks = assignIDs(r.blocks)
	return r.lines, r.buttons, r.blocks
}

// buttonGlyph renders a single button glyph either in its normal style or in
// the flash highlight style (bright bg, dark fg) when the button's identity
// key matches r.flashKey. bg is the base code-block background style used to
// keep the tab line uniform outside the glyph cell.
func (r *renderer) buttonGlyph(blockID, kind, glyph, fgColor string, bg lipgloss.Style) string {
	key := blockID + ":" + kind
	if r.flashKey != "" && r.flashKey == key {
		// Flash feedback WITHOUT a background. A background applied to the glyph cell
		// makes some terminals render the (nerd-font PUA) glyph shifted down a row —
		// so the flash is bold + a bright foreground on the SAME cell bg, giving a
		// visible pulse with no layout perturbation.
		return bg.Foreground(lipgloss.Color(colFlashOn)).Bold(true).Render(glyph)
	}
	return bg.Foreground(lipgloss.Color(fgColor)).Render(glyph)
}

// block walks the children of n, rendering each block-level node. indent is the
// current left indentation (used by nested lists / quotes).
func (r *renderer) block(n ast.Node, indent int) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch node := c.(type) {
		case *ast.Heading:
			prefix := strings.Repeat("▓", 3) + " "
			style := lipgloss.NewStyle().Foreground(lipgloss.Color(headingColor(node.Level))).Bold(true)
			r.emitProse(style.Render(prefix+r.inline(node)), indent)
			r.blank()
		case *ast.Paragraph, *ast.TextBlock:
			r.emitProse(r.inline(c), indent)
			if _, ok := c.(*ast.Paragraph); ok {
				r.blank()
			}
		case *ast.List:
			r.list(node, indent)
			r.blank()
		case *ast.ThematicBreak:
			rule := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0)).
				Render(strings.Repeat("─", r.width-indent))
			r.emitProse(rule, indent)
			r.blank()
		case *ast.FencedCodeBlock:
			r.code(node)
			r.blank()
		case *ast.CodeBlock:
			r.code(node)
			r.blank()
		case *ast.Blockquote:
			r.quote(node, indent)
			r.blank()
		case *extast.Table:
			r.table(node)
			r.blank()
		default:
			// Fallback for block types without an explicit case (e.g. HTML blocks):
			// render their inline text so nothing is silently dropped.
			if t := strings.TrimRight(r.inline(c), "\n"); t != "" {
				r.emitProse(t, indent)
			}
		}
	}
	r.trimTrailingBlank()
}

func headingColor(level int) string {
	switch level {
	case 1:
		return colMauve
	case 2:
		return colPeach
	case 3:
		return colYellow
	default:
		return colGreen
	}
}

// list renders an ast.List, one marker per item, recursing for nested blocks.
func (r *renderer) list(l *ast.List, indent int) {
	i := 0
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		marker := "• "
		if l.IsOrdered() {
			// l.Start is the first item's number; i is the zero-based offset, so
			// l.Start+i gives the correct display number for each item.
			marker = itoa(l.Start+i) + ". "
		}
		// First child of a list item is usually a paragraph/textblock.
		itemText := ""
		if fc := item.FirstChild(); fc != nil {
			itemText = r.inline(fc)
		}
		r.emitProse(marker+itemText, indent+2)
		// Nested lists inside this item.
		for sub := item.FirstChild(); sub != nil; sub = sub.NextSibling() {
			if nl, ok := sub.(*ast.List); ok {
				r.list(nl, indent+2)
			}
		}
		i++
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// inline renders the inline children of n into a single styled string.
func (r *renderer) inline(n ast.Node) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch node := c.(type) {
		case *ast.Text:
			b.WriteString(string(node.Segment.Value(r.src)))
			if node.HardLineBreak() || node.SoftLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.WriteString(string(node.Value))
		case *ast.Emphasis:
			st := lipgloss.NewStyle().Italic(true)
			if node.Level == 2 {
				st = lipgloss.NewStyle().Bold(true)
			}
			b.WriteString(st.Render(r.inline(node)))
		case *ast.CodeSpan:
			st := lipgloss.NewStyle().Foreground(lipgloss.Color(colPeach)).Background(lipgloss.Color(colCodeBg))
			b.WriteString(st.Render(" " + r.inlineText(node) + " "))
		case *ast.Link:
			st := lipgloss.NewStyle().Foreground(lipgloss.Color(colBlue)).Underline(true)
			b.WriteString(st.Render(r.inline(node)))
		case *extast.Strikethrough:
			b.WriteString(lipgloss.NewStyle().Strikethrough(true).Render(r.inline(node)))
		default:
			b.WriteString(r.inline(c))
		}
	}
	return b.String()
}

// inlineText extracts the raw text of an inline node (for code spans).
func (r *renderer) inlineText(n ast.Node) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			b.WriteString(string(t.Segment.Value(r.src)))
		} else {
			b.WriteString(r.inlineText(c))
		}
	}
	return b.String()
}

// emitProse wraps s to the pane width (minus indent) and appends Wide=false
// lines, each left-padded by indent spaces. lipgloss wraps ANSI-aware.
func (r *renderer) emitProse(s string, indent int) {
	w := r.width - indent
	if w < 1 {
		w = 1
	}
	wrapped := lipgloss.NewStyle().Width(w).Render(s)
	pad := strings.Repeat(" ", indent)
	for _, ln := range strings.Split(wrapped, "\n") {
		r.lines = append(r.lines, Line{Text: pad + ln, Wide: false})
	}
}

func (r *renderer) blank() { r.lines = append(r.lines, Line{Text: "", Wide: false}) }

func (r *renderer) trimTrailingBlank() {
	// An HBar row has empty Text but is NOT blank — it's a horizontal scrollbar
	// the View draws dynamically. Keep it even when it ends the document (an
	// overflowing code block as the last element), or its scrollbar is lost.
	for len(r.lines) > 0 {
		last := r.lines[len(r.lines)-1]
		if last.HBar > 0 || strings.TrimSpace(strip(last.Text)) != "" {
			break
		}
		r.lines = r.lines[:len(r.lines)-1]
	}
}

// table renders a GFM table via lipgloss/table at its natural width (no wrap),
// emitting Wide=true lines so it scrolls horizontally like a code block.
func (r *renderer) table(n *extast.Table) {
	var header []string
	var rows [][]string
	for row := n.FirstChild(); row != nil; row = row.NextSibling() {
		var cells []string
		for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
			cells = append(cells, strings.TrimSpace(strip(r.inline(cell))))
		}
		switch row.(type) {
		case *extast.TableHeader:
			header = cells
		default:
			rows = append(rows, cells)
		}
	}

	tbl := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0))).
		Headers(header...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colMauve)).Padding(0, 1)
			}
			return lipgloss.NewStyle().Foreground(lipgloss.Color(colText)).Padding(0, 1)
		})
	// Do NOT call .Width(): let the table take its natural width so wide tables
	// overflow the pane and scroll horizontally.
	for _, ln := range strings.Split(tbl.String(), "\n") {
		if ln == "" {
			continue
		}
		r.lines = append(r.lines, Line{Text: ln, Wide: true})
	}
}

// band wraps content in a continuous background sequence `bg` that survives
// embedded "\x1b[0m"/"\x1b[m" resets, padded to `width` visible columns.
// The bg sequence is re-applied after every reset so it never drops mid-line.
func band(content string, bg string, width int) string {
	s := bg + strings.ReplaceAll(content, "\x1b[0m", "\x1b[0m"+bg)
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+bg)
	if pad := width - lipgloss.Width(content); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s + "\x1b[0m"
}

const (
	glyphSep      = "❘"          // U+2758 buttons separator
	glyphRun      = "\U0000EACF" // U+EACF nf-md-run — execute block in agent shell
	glyphStop     = "\U0000EAD7" // U+EAD7 codicon-stop — interrupt a running block
	glyphPlay     = "⏵"          // U+23F5 — replay/send to terminal
	glyphCopy     = "\U0010F0C5" // copy
	glyphViewDiff = "\U0000EAE1" // U+EAE1 — open diff in float viewer
	glyphApply    = "\U0000EC0B" // U+EC0B — apply diff via git apply
	glyphUndo     = "\U000F054D" // nf-md undo-variant — undo an applied patch (git apply --reverse)
	glyphRetry    = "\U000F0450" // nf-md refresh/retry — re-engage the agent for a different fix
)

// code renders a (fenced) code block: chroma-highlighted, NOT wrapped, each
// line padded to the target width with a continuous code background. Wide=true.
// A decorative tab line (Wide=false) is emitted first: <leading pad><lang><" ❘ "><run? ><copy>.
func (r *renderer) code(n ast.Node) {
	var raw strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		raw.Write(seg.Value(r.src))
	}
	src := strings.TrimRight(raw.String(), "\n")

	info := ""
	if fc, ok := n.(*ast.FencedCodeBlock); ok && fc.Info != nil {
		info = string(fc.Info.Segment.Value(r.src))
	}
	lang, attrs, flags := parseFenceInfo(info)
	blk := Block{
		ID:      attrs["id"],
		Lang:    lang,
		Needs:   splitNeeds(attrs["needs"]), // helper: strings.Split on "," trimmed, nil if empty
		Static:  flags["static"],
		Payload: src,
	}
	blk.Type = classifyType(lang, blk.Static)
	// Synthesize a stable, position-based id for blocks that have no explicit
	// {id=…} attribute.  We use the block's 1-based ordinal among all code
	// blocks seen so far in this render pass so the same document always yields
	// the same ids and they are stable across re-renders within a session.
	// The "auto-" prefix is unlikely to collide with author-chosen ids; any
	// accidental collision with an explicit id is noted in the commit message.
	r.blocks = append(r.blocks, blk)
	if blk.ID == "" {
		blk.ID = fmt.Sprintf("auto-%d", len(r.blocks))
		r.blocks[len(r.blocks)-1].ID = blk.ID
	}

	width := r.width // content width

	// Decorative tab: <leading pad><lang><" ❘ "><run? ><copy>. Each cell on the
	// code bg. Buttons (run/copy) are the 2-cell <glyph>" " units; record their
	// columns for mouse/keyboard activation.
	lineIdx := len(r.lines)
	bg := lipgloss.NewStyle().Background(lipgloss.Color(colCodeBg))

	// Lang part: devicon glyph (mini.icons) in its color + label.
	// Unknown langs return an empty glyph — in that case we emit only the label
	// (no icon cell, no trailing space after the icon). Known langs get the icon
	// cell + trailing space + label. Empty lang (no fence info) shows no lang
	// part at all — just separator+buttons.
	var langPart string
	var langW int
	if lang != "" {
		glyph, color := langIconOrDefault(lang)
		if glyph != "" {
			langPart = bg.Foreground(lipgloss.Color(color)).Render(glyph+" ") +
				bg.Foreground(lipgloss.Color(color)).Render(lang)
			// glyph(1) + space(1) + label
			langW = lipgloss.Width(glyph) + 1 + lipgloss.Width(lang)
		} else {
			langPart = bg.Foreground(lipgloss.Color(color)).Render(lang)
			// label only — no icon column
			langW = lipgloss.Width(lang)
		}
	}

	// Compute unmet needs now so we can know which buttons to reserve space for.
	unmet := needsSatisfied(blk, r.states)

	// region width: leadpad(1) + langW + sep(" ❘ "=3) + run(2 if shell/run+unblocked) + play(2 if shell+unblocked) + diff(2 if diff) + apply-diff or undo-diff(2 if diff+unblocked or applied) + copy(2)
	regionW := 1 + langW + 3 + 2
	if (blk.Type == "shell" || blk.Type == "run") && len(unmet) == 0 {
		regionW += 2 // run(2)
	}
	if blk.Type == "shell" && len(unmet) == 0 {
		regionW += 2 // play(2)
	}
	if blk.Type == "diff" {
		regionW += 2 // diff(2) — always ungated; opens patch in float viewer
		// undo-diff is always shown when applied (Status=="ok"); apply-diff only when needs are met.
		if r.states[blk.ID].Status == "ok" || len(unmet) == 0 {
			regionW += 2 // undo-diff(2) or apply-diff(2)
		}
	}
	fillCols := width - regionW
	if fillCols < 0 {
		fillCols = 0
	}

	var sb strings.Builder
	sb.WriteString(codeFgANSI + strings.Repeat("▂", fillCols) + "\x1b[0m")
	col := fillCols

	sb.WriteString(bg.Render(" "))
	col++ // leading pad
	if langPart != "" {
		sb.WriteString(langPart)
		col += langW
	}
	// separator " ❘ "
	sb.WriteString(bg.Render(" "))
	col++
	sb.WriteString(bg.Foreground(lipgloss.Color(colOverlay0)).Render(glyphSep))
	col++
	sb.WriteString(bg.Render(" "))
	col++
	if len(unmet) == 0 {
		if blk.Type == "shell" || blk.Type == "run" {
			runActionCol := col
			if r.states[blk.ID].Status == "running" {
				sb.WriteString(r.buttonGlyph(blk.ID, "stop", glyphStop, colStop, bg))
				col++
				sb.WriteString(bg.Render(" "))
				col++
				r.buttons = append(r.buttons, Button{Line: lineIdx, Col: runActionCol, Width: 2, Kind: "stop", BlockID: blk.ID})
			} else {
				sb.WriteString(r.buttonGlyph(blk.ID, "run", glyphRun, colRun, bg))
				col++
				sb.WriteString(bg.Render(" "))
				col++
				r.buttons = append(r.buttons, Button{Line: lineIdx, Col: runActionCol, Width: 2, Kind: "run", Payload: runPayload(blk), BlockID: blk.ID})
			}
		}
		if blk.Type == "shell" {
			playCol := col
			sb.WriteString(r.buttonGlyph(blk.ID, "play", glyphPlay, colGreen, bg))
			col++
			sb.WriteString(bg.Render(" "))
			col++
			r.buttons = append(r.buttons, Button{Line: lineIdx, Col: playCol, Width: 2, Kind: "play", Payload: src, BlockID: blk.ID})
		}
	}
	if blk.Type == "diff" {
		// diff: always ungated — open patch in a float viewer.
		diffCol := col
		sb.WriteString(r.buttonGlyph(blk.ID, "diff", glyphViewDiff, colBlue, bg))
		col++
		sb.WriteString(bg.Render(" "))
		col++
		r.buttons = append(r.buttons, Button{Line: lineIdx, Col: diffCol, Width: 2, Kind: "diff", Payload: src, BlockID: blk.ID})
		// undo-diff when applied (Status=="ok") — always available, not needs-gated.
		// apply-diff otherwise — needs-gated (only when all needs are satisfied).
		if r.states[blk.ID].Status == "ok" {
			undoCol := col
			sb.WriteString(r.buttonGlyph(blk.ID, "undo-diff", glyphUndo, colPeach, bg))
			col++
			sb.WriteString(bg.Render(" "))
			col++
			r.buttons = append(r.buttons, Button{Line: lineIdx, Col: undoCol, Width: 2, Kind: "undo-diff", Payload: src, BlockID: blk.ID})
		} else if len(unmet) == 0 {
			applyDiffCol := col
			sb.WriteString(r.buttonGlyph(blk.ID, "apply-diff", glyphApply, colGreen, bg))
			col++
			sb.WriteString(bg.Render(" "))
			col++
			r.buttons = append(r.buttons, Button{Line: lineIdx, Col: applyDiffCol, Width: 2, Kind: "apply-diff", Payload: src, BlockID: blk.ID})
		}
	}
	copyCol := col
	sb.WriteString(r.buttonGlyph(blk.ID, "copy", glyphCopy, colYellow, bg))
	col++
	sb.WriteString(bg.Render(" "))
	col++
	r.buttons = append(r.buttons, Button{Line: lineIdx, Col: copyCol, Width: 2, Kind: "copy", Payload: src, BlockID: blk.ID})

	r.lines = append(r.lines, Line{Text: sb.String(), Wide: false, Code: true})

	var hlLines []string
	if blk.Type == "diff" {
		// Diff blocks: bypass chroma and apply hunk-style line coloring instead.
		// Each raw line is colored by its first character (add/del/hunk/file-header/context).
		hlLines = strings.Split(src, "\n")
	} else {
		hlLines = strings.Split(highlight(src, lang), "\n")
	}
	blockW := 0
	for _, hl := range hlLines {
		var body string
		var lineBg string
		if blk.Type == "diff" {
			fgHex, bg := diffLineStyle(hl)
			styled := lipgloss.NewStyle().Foreground(lipgloss.Color(fgHex)).Render(hl)
			body = " " + styled + " "
			lineBg = bg
		} else {
			body = " " + hl + " "
			lineBg = codeBgANSI
		}
		r.lines = append(r.lines, Line{Text: body, Wide: true, Bg: lineBg, Code: true})
		if w := lipgloss.Width(body); w > blockW {
			blockW = w
		}
	}

	// When the block overflows the viewport, the horizontal scrollbar row caps
	// it (and reads as the bottom padding) — so we skip the 🮂 bottom bar there,
	// which otherwise looks redundant/unpolished. Non-overflowing blocks keep
	// the normal 🮂 bottom edge.
	if blockW > width {
		r.lines = append(r.lines, Line{Wide: false, HBar: blockW, Code: true})
	} else {
		// Bottom edge bar: 🮂 characters in fg colCodeBg (#282C41), no background.
		// Total display width == width. Wide=false.
		bottomLine := codeFgANSI + strings.Repeat("🮂", width) + "\x1b[0m"
		r.lines = append(r.lines, Line{Text: bottomLine, Wide: false, Code: true})
	}

	if len(unmet) > 0 {
		r.emitBlocked(unmet)
	}

	if r.states != nil {
		if st, ok := r.states[blk.ID]; ok {
			r.runRegion(blk, st)
		}
	}
}

// runRegion appends per-block run-state lines after a code block.
// running → a spinner line; ok/failed → a summary line with toggle marker;
// when Expanded → the last 50 lines of the log file then a "view full log" line.
func (r *renderer) runRegion(blk Block, st blockRunState) {
	id := blk.ID
	const indentStr = "  " // 2-space left margin to sit cleanly under the block
	const indentW = 2
	switch st.Status {
	case "reviewing":
		sty := lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext))
		r.lines = append(r.lines, Line{Text: indentStr + sty.Render("⟳ Reviewing… (annotate in the hunk window)"), Wide: false, Code: true})
	case "running":
		sl := spinnerLine(st.SpinFrame, "running…", st.SpinFrame/10)
		r.lines = append(r.lines, Line{Text: indentStr + sl, Wide: false, Code: true})
	case "ok", "failed", "stopped":
		rawToggle := "▸"
		if st.Expanded {
			rawToggle = "▾"
		}
		var statusPart string
		var label string
		switch st.Status {
		case "ok":
			sty := lipgloss.NewStyle().Foreground(lipgloss.Color(colGreen))
			label = "✓ ran (exit " + itoa(st.Exit) + ")"
			statusPart = sty.Render(label)
		case "stopped":
			// Neutral state: the user deliberately stopped the block. Not a failure —
			// no red, no "try another fix" button (see followupCol guard below).
			sty := lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext))
			label = "■ stopped (exit " + itoa(st.Exit) + ")"
			statusPart = sty.Render(label)
		default: // "failed"
			sty := lipgloss.NewStyle().Foreground(lipgloss.Color(colRed))
			label = "✗ failed (exit " + itoa(st.Exit) + ")"
			statusPart = sty.Render(label)
		}
		// toggle column: indent(2) + visible width of label + space(1).
		// This mirrors how tab button columns are computed (tracking col after each
		// visible element). buttonAt subtracts the pager's 2-col left margin, so
		// Col here is the position within Line.Text (which already includes the
		// 2-space indentStr).
		toggleCol := indentW + lipgloss.Width(label) + 1
		// Render the toggle glyph with flash highlighting when active.
		var toggleRendered string
		toggleKey := id + ":toggle"
		if r.flashKey != "" && r.flashKey == toggleKey {
			toggleRendered = lipgloss.NewStyle().
				Foreground(lipgloss.Color(colFlashOn)).
				Bold(true).
				Render(rawToggle)
		} else {
			toggleRendered = rawToggle
		}
		summary := statusPart + " " + toggleRendered
		// On a failed run/shell block (other than the verify re-run, which auto-fires
		// a follow-up), offer a "↻ try another fix" button that re-engages the agent.
		// Its click payload is the block's raw command text.
		// The verify block normally hides this button (it auto-fires a follow-up),
		// but once the auto-follow-up cap is reached (st.FollowupExhausted) it shows
		// the button so the user can keep iterating by hand.
		followupCol := -1
		if st.Status == "failed" && (id != "verify" || st.FollowupExhausted) && (blk.Type == "run" || blk.Type == "shell") {
			sep := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0)).Render(glyphSep)
			followupCol = indentW + lipgloss.Width(label) + 1 + lipgloss.Width(rawToggle) + 1 + lipgloss.Width(glyphSep) + 1
			retryGlyph := glyphRetry
			if r.flashKey != "" && r.flashKey == id+":followup" {
				retryGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color(colFlashOn)).Bold(true).Render(retryGlyph)
			} else {
				retryGlyph = lipgloss.NewStyle().Foreground(lipgloss.Color(colPeach)).Render(retryGlyph)
			}
			summary += " " + sep + " " + retryGlyph + " " +
				lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext)).Render("try another fix")
		}
		summaryLineIdx := len(r.lines)
		r.lines = append(r.lines, Line{Text: indentStr + summary, Wide: false, Code: true})
		r.buttons = append(r.buttons, Button{Line: summaryLineIdx, Col: toggleCol, Width: 2, Kind: "toggle", BlockID: id})
		if followupCol >= 0 {
			r.buttons = append(r.buttons, Button{Line: summaryLineIdx, Col: followupCol, Width: 2, Kind: "followup", Payload: blk.Payload, BlockID: id})
		}
		if st.Expanded {
			tail := tailFile(st.Logpath, 50)
			if len(tail) == 0 {
				r.lines = append(r.lines, Line{Text: indentStr + "(log unavailable)", Wide: true, Bg: codeBgANSI, Code: true})
			} else {
				sub := lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext))
				for _, tl := range tail {
					r.lines = append(r.lines, Line{Text: indentStr + sub.Render(tl), Wide: true, Bg: codeBgANSI, Code: true})
				}
			}
			viewLog := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0)).Render("view full log: " + st.Logpath)
			r.lines = append(r.lines, Line{Text: indentStr + viewLog, Wide: true, Bg: codeBgANSI, Code: true})
		}
	}
}

// tailFile reads the file at path and returns the last n lines.
// Returns nil on any read error.
func tailFile(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var all []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		all = append(all, sc.Text())
	}
	if sc.Err() != nil {
		return nil
	}
	if len(all) <= n {
		return all
	}
	return all[len(all)-n:]
}

// langInterp maps a lang name to the interpreter command used to self-invoke a
// heredoc. python/python3/py → python3; node/js/javascript → node; else verbatim.
func langInterp(lang string) string {
	switch lang {
	case "python", "python3", "py":
		return "python3"
	case "node", "js", "javascript":
		return "node"
	case "ruby":
		return "ruby"
	case "perl":
		return "perl"
	default:
		return lang
	}
}

// runPayload returns the shell command the agent eval's when the run button is
// pressed. Shell blocks run raw; script blocks (Type=="run") self-invoke their
// interpreter via a quoted heredoc so the agent shell can eval the whole thing.
// Note: a script body containing a line that is exactly "__AAS_RUN__" would
// break the heredoc — that constraint is acceptable for troubleshooting snippets.
func runPayload(blk Block) string {
	if blk.Type != "run" {
		return blk.Payload
	}
	return langInterp(blk.Lang) + " <<'__AAS_RUN__'\n" + blk.Payload + "\n__AAS_RUN__"
}

// needsSatisfied returns the UNMET needs of blk (empty slice ⇔ all satisfied).
func needsSatisfied(blk Block, states map[string]blockRunState) []string {
	var unmet []string
	for _, n := range blk.Needs {
		if states[n].Status != "ok" {
			unmet = append(unmet, n)
		}
	}
	return unmet
}

// emitBlocked appends a single "⊘ needs: <ids>" indicator line styled with
// colSubtext, associated with the code block's tab/region (Code=true).
func (r *renderer) emitBlocked(unmet []string) {
	sty := lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext))
	text := sty.Render("⊘ needs: " + strings.Join(unmet, ", "))
	r.lines = append(r.lines, Line{Text: " " + text, Wide: false, Code: true})
}

// splitNeeds splits a comma-separated needs string into a slice of trimmed ids.
// Returns nil for an empty string.
func splitNeeds(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// highlight runs chroma over src; on any failure it returns src unchanged.
func highlight(src, lang string) string {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(src)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		return src
	}
	f := formatters.Get("terminal16m")
	if f == nil {
		return src
	}
	var buf bytes.Buffer
	if err := f.Format(&buf, codeStyle(), it); err != nil {
		return src
	}
	return strings.TrimRight(buf.String(), "\n")
}

// diffLineStyle classifies a single raw diff line and returns the foreground
// color hex string and the background ANSI sequence appropriate for hunk-style
// rendering. The returned bg is one of codeBgANSI, diffAddBgANSI, or
// diffDelBgANSI; fg is a Catppuccin Mocha hex constant.
//
// Classification rules (first match wins):
//
//	"+++ …"  / "--- …"  / "diff …" / "index …"  → file-header (dim grey)
//	starts with "+", not "+++ "                   → addition
//	starts with "-", not "--- "                   → deletion
//	starts with "@@"                              → hunk header (sky)
//	anything else                                 → context
func diffLineStyle(line string) (fg, bg string) {
	switch {
	case strings.HasPrefix(line, "+++ ") ||
		strings.HasPrefix(line, "--- ") ||
		strings.HasPrefix(line, "diff ") ||
		strings.HasPrefix(line, "index "):
		return colSubtext0, codeBgANSI
	case strings.HasPrefix(line, "+"):
		return colGreen, diffAddBgANSI
	case strings.HasPrefix(line, "-"):
		return colRed, diffDelBgANSI
	case strings.HasPrefix(line, "@@"):
		return colSky, codeBgANSI
	default:
		return colText, codeBgANSI
	}
}

// quote renders a block quote as a GitHub-style admonition.
// It collects the child block text, optionally detects a [!TYPE] marker, then
// emits a colored ▋ border on every line. A recognized [!TYPE] marker also
// emits a header line with icon + title. A bare quote (no marker) emits only
// the bordered body lines (no header) with a colOverlay0 border.
func (r *renderer) quote(n ast.Node, indent int) {
	// Step 1: collect body text from child blocks.
	var pieces []string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch c.(type) {
		case *ast.Paragraph, *ast.TextBlock:
			pieces = append(pieces, r.inline(c))
		default:
			if t := strings.TrimSpace(r.inline(c)); t != "" {
				pieces = append(pieces, t)
			}
		}
	}
	body := strings.Join(pieces, "\n")

	// Step 2: detect [!type] marker.
	var a *admon
	if m := admonMarkerRe.FindStringSubmatch(body); m != nil {
		key := strings.ToLower(m[1])
		if entry, ok := admonitions[key]; ok {
			a = &entry
			body = admonMarkerRe.ReplaceAllString(body, "")
		}
	}

	// Step 3: determine border color and dark background.
	color := colOverlay0
	if a != nil {
		color = a.color
	}
	bg := bgANSI(darken(color, 0.20))

	// Step 4: build styles.
	borderGlyph := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render("▋")
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colText)).Italic(true)

	// Step 5: emit header (only for recognized [!type] admonitions).
	if a != nil {
		headerText := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(a.icon + " " + a.title)
		inner := borderGlyph + " " + headerText
		r.lines = append(r.lines, Line{Text: band(inner, bg, r.width), Wide: false})
	}

	// Step 6: emit body lines. Wrap to width-3: border (1) + leading space (1) +
	// text + a reserved trailing column so the background always pads at least one
	// space past the text on the right (no text touching the band's right edge).
	trimmed := strings.TrimSpace(body)
	if trimmed != "" {
		w := r.width - 3
		if w < 1 {
			w = 1
		}
		wrapped := bodyStyle.Width(w).Render(trimmed)
		for _, ln := range strings.Split(wrapped, "\n") {
			inner := borderGlyph + " " + ln
			r.lines = append(r.lines, Line{Text: band(inner, bg, r.width), Wide: false})
		}
	}
}
