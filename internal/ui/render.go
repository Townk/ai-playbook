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

	"github.com/Townk/ai-playbook/internal/orchestrator"
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
	// shellDisabled dims the shell-action button glyphs (run/play/stop/diff/apply/undo)
	// to the muted overlay color during the async-startup window (model.driverPending),
	// when the background shell isn't open yet. Copy is never dimmed. The buttons keep
	// their positions/width (only the color changes), so nothing jumps when they enable.
	shellDisabled bool
	// reengageAvail gates the "try another fix" (followup) affordance: it only renders
	// when in-process re-engagement is wired (an authoring/troubleshoot session). A plain
	// `run --file` viewer has no agent to re-engage, so the button is suppressed. Defaults
	// to true so existing 4-arg callers / tests keep the prior always-show behavior.
	reengageAvail bool
	// rollbackAvail gates the "Rollback playbook" affordance on a failed step: it renders
	// only when at least one already-run block declares a rollback= target (so there is
	// something to undo). Defaults to false (no rollback button) for 4-arg callers/tests.
	rollbackAvail bool
	// muxActive gates the green "Play" button (run a shell block in the ORIGIN pane): it
	// types the command into the origin split, which only exists under a terminal
	// multiplexer. The default no-mux `run --file` viewer has no origin pane, so Play is
	// meaningless there and is suppressed. Defaults to false (no Play) for callers/tests
	// that don't pass the flag; model.reflow passes m.asker != nil (mux active).
	muxActive bool
	// rollbackTargets is the set of block IDs referenced by some block's rollback=
	// attribute. Such a block is rollback-only: it never gets its own run/play button
	// (it executes solely as part of a Rollback playbook chain), so it can't be run
	// independently — e.g. before its paired forward step ran.
	rollbackTargets map[string]bool
	// blockNum is the running visual-ID counter: each actionable (shell/run/diff/create)
	// non-rollback block gets the next circled number ①②③… at its tab's top-left.
	blockNum int
	// rollbackForNum maps a rollback-target block ID → the visual number of the forward
	// block that declares it (rollback=<id>), so the target's bottom border can read
	// "rollback ②". Filled as forward blocks render (which precede their targets).
	rollbackForNum map[string]int
	// nextNumAssigned records that the single green "next to run" visual number has been
	// claimed by the first un-run, needs-satisfied block — later un-run blocks render red.
	nextNumAssigned bool
}

// rollbackRef renders n (1-based) as the "(n)" reference shown in a rollback block's
// "rollback (n)" under-tab.
func rollbackRef(n int) string {
	return "(" + itoa(n) + ")"
}

// Render parses markdown and returns tagged, laid-out lines, a button
// registry, and the typed block list for a given pane width. states maps
// block IDs to their current run state; pass nil when not needed. flashKey
// is the identity of the button currently being flash-highlighted
// ("<blockID>:<kind>"); pass "" when no flash is active. Callers that don't
// need blocks can discard the third value with _.
// The optional trailing bool flags are, in order: shellDisabled (used by model.reflow
// on the async-startup path — dims the shell-action button glyphs while the background
// shell is still opening), reengageAvail (whether the "try another fix" affordance
// should render), rollbackAvail (whether the "Rollback playbook" affordance should
// render on a failed step), and muxActive (whether the green "Play" button renders — it
// needs an origin pane, so only under a terminal multiplexer). Existing 4-arg callers
// (tests, the static block-count path) leave shellDisabled false, reengageAvail true
// (prior always-show), rollbackAvail false, and muxActive false (no Play button).
func Render(md string, width int, states map[string]blockRunState, flashKey string, flags ...bool) ([]Line, []Button, []Block) {
	if width < 1 {
		width = 1
	}
	disabled := false
	if len(flags) >= 1 {
		disabled = flags[0]
	}
	reengageAvail := true
	if len(flags) >= 2 {
		reengageAvail = flags[1]
	}
	rollbackAvail := false
	if len(flags) >= 3 {
		rollbackAvail = flags[2]
	}
	muxActive := false
	if len(flags) >= 4 {
		muxActive = flags[3]
	}
	src := []byte(normalizeFences(md))
	gm := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := gm.Parser().Parse(text.NewReader(src))
	r := &renderer{src: src, width: width, states: states, flashKey: flashKey, shellDisabled: disabled, reengageAvail: reengageAvail, rollbackAvail: rollbackAvail, muxActive: muxActive, rollbackTargets: collectRollbackTargets(doc, src), rollbackForNum: map[string]int{}}
	r.block(doc, 0)
	r.blocks = assignIDs(r.blocks)
	return r.lines, r.buttons, r.blocks
}

// normalizeFences repairs a malformed CLOSING code fence that the model emitted
// without the required newline before following prose, e.g. a block whose closer
// is "```SDK is at …" on the same line as trailing text. Per CommonMark a closing
// fence may contain ONLY the fence characters plus optional trailing whitespace,
// so "```SDK…" does NOT close the block — goldmark keeps the block open and the
// rest of the document renders as code, nuking the whole render.
//
// While INSIDE a fenced block, when a line begins with a run of the open fence's
// character (>= the opening run length) but has further non-whitespace after that
// run, we split it: the closing fence becomes its own line and the trailing text
// is pushed to the following line as prose. Well-formed fences and fence content
// are left untouched; line endings (\n) and a missing final newline are preserved.
func normalizeFences(md string) string {
	// Split keeping track of whether the input ended with a newline so we can
	// reproduce it exactly (strings.Split on "x\n" yields ["x",""], so a trailing
	// "" sentinel marks the final newline).
	lines := strings.Split(md, "\n")
	var out []string

	inFence := false
	var fenceChar byte // '`' or '~'
	var fenceLen int   // opening run length; a closer must be >= this

	for _, line := range lines {
		if !inFence {
			if ch, n, ok := openFence(line); ok {
				inFence = true
				fenceChar = ch
				fenceLen = n
			}
			out = append(out, line)
			continue
		}
		// Inside a fence: look for the closing run at the start (after up to 3
		// spaces of indent, per CommonMark).
		runStart := 0
		for runStart < len(line) && runStart < 3 && line[runStart] == ' ' {
			runStart++
		}
		runLen := 0
		for runStart+runLen < len(line) && line[runStart+runLen] == fenceChar {
			runLen++
		}
		if runLen >= fenceLen && runStart+runLen == len(strings.TrimRight(line, " ")) {
			// Already a well-formed closer (only fence chars + trailing spaces).
			out = append(out, line)
			inFence = false
			continue
		}
		if runLen >= fenceLen {
			// Malformed closer: a valid-length fence run followed by more text on the
			// same line. Close the fence on its own line and emit the remainder as
			// prose on the next line. Preserve the leading indent on the fence line.
			fence := line[:runStart+runLen]
			rest := strings.TrimLeft(line[runStart+runLen:], " ")
			out = append(out, fence)
			inFence = false
			if rest != "" {
				out = append(out, rest)
			}
			continue
		}
		// Ordinary fence content.
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// openFence reports whether line is a CommonMark opening code fence and returns
// its fence character and run length. An opener is 3+ backticks or 3+ tildes
// after up to 3 leading spaces; a backtick fence's info string may not contain a
// backtick (which would make it not a fence). The info string is otherwise free.
func openFence(line string) (ch byte, n int, ok bool) {
	i := 0
	for i < len(line) && i < 3 && line[i] == ' ' {
		i++
	}
	if i >= len(line) || (line[i] != '`' && line[i] != '~') {
		return 0, 0, false
	}
	ch = line[i]
	start := i
	for i < len(line) && line[i] == ch {
		i++
	}
	n = i - start
	if n < 3 {
		return 0, 0, false
	}
	info := line[i:]
	if ch == '`' && strings.ContainsRune(info, '`') {
		return 0, 0, false // backtick fence info must not contain a backtick
	}
	return ch, n, true
}

// buttonGlyph renders a single button glyph either in its normal style or in
// the flash highlight style (bright bg, dark fg) when the button's identity
// key matches r.flashKey. bg is the base code-block background style used to
// keep the tab line uniform outside the glyph cell.
func (r *renderer) buttonGlyph(blockID, kind, glyph, fgColor string, bg lipgloss.Style) string {
	if r.shellDisabled && isShellActionKind(kind) {
		// Async startup: this shell-backed button is inert until the orchestrator lands.
		// Render it in the muted overlay color (the codebase's de-emphasis fg) and skip
		// the flash pulse. Same glyph/cell, so the position never moves when it enables.
		return bg.Foreground(lipgloss.Color(colOverlay0)).Render(glyph)
	}
	if r.states[blockID].Drifted && kind == "apply-diff" {
		// Drift: the patch no longer applies cleanly. Dim ONLY apply-diff to signal it
		// is inert; the drift region's resolve/regenerate buttons are the live paths. The
		// view-diff (diff) button stays live-colored — a drifted diff can still be VIEWED
		// read-only (F30).
		return bg.Foreground(lipgloss.Color(colOverlay0)).Render(glyph)
	}
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
		r.emitHanging(marker+itemText, indent+2, indent+2+lipgloss.Width(marker))
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

// emitHanging wraps s so the FIRST line gets firstIndent leading spaces and
// every wrapped continuation gets hangIndent (a hanging indent for list items).
func (r *renderer) emitHanging(s string, firstIndent, hangIndent int) {
	w := r.width - hangIndent
	if w < 1 {
		w = 1
	}
	wrapped := lipgloss.NewStyle().Width(w).Render(s)
	for i, ln := range strings.Split(wrapped, "\n") {
		pad := firstIndent
		if i > 0 {
			pad = hangIndent
		}
		r.lines = append(r.lines, Line{Text: strings.Repeat(" ", pad) + ln, Wide: false})
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
	glyphCreate   = "\U000F0224" // nf-md file-plus — create a new file
)

// Callout (admonition) bordered-frame glyphs — Symbols for Legacy Computing block.
// Top-left corner and top/bottom border use sextant codepoints; left bar is a
// half-block so it's always available in any Nerd Font.
const (
	calloutTL = "\U0001FB1E" // 🬞 U+1FB1E — top-left corner sextant
	calloutTB = "\U0001FB2D" // 🬭 U+1FB2D — top border sextant
	calloutCL = "▐"          // U+2590  — content left bar (right half-block)
	calloutBL = "\U0001FB01" // 🬁 U+1FB01 — bottom-left corner sextant
	calloutBB = "\U0001FB02" // 🬂 U+1FB02 — bottom border sextant
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
		ID:       attrs["id"],
		Lang:     lang,
		Needs:    splitNeeds(attrs["needs"]), // helper: strings.Split on "," trimmed, nil if empty
		Rollback: attrs["rollback"],
		Static:   flags["static"],
		Payload:  src,
	}
	blk.Type = classifyType(lang, blk.Static)
	if f := attrs["file"]; f != "" {
		blk.File = f
		blk.Type = "create"
	}
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
	// For create blocks the label is the file path (blk.File) rather than the
	// lang name so the tab reads "󰈙 cmd/x/main.go ❘ create" instead of "go".
	var langPart string
	var langW int
	if blk.Type == "create" {
		glyph, color := langIconOrDefault(lang)
		if glyph != "" {
			langPart = bg.Foreground(lipgloss.Color(color)).Render(glyph+" ") +
				bg.Foreground(lipgloss.Color(color)).Render(blk.File)
			// glyph(1) + space(1) + file path
			langW = lipgloss.Width(glyph) + 1 + lipgloss.Width(blk.File)
		} else {
			langPart = bg.Foreground(lipgloss.Color(color)).Render(blk.File)
			// file path only — no icon column
			langW = lipgloss.Width(blk.File)
		}
	} else if lang != "" {
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

	// Visual ID: an actionable (shell/run/diff/create) block that is NOT a rollback
	// target gets the next circled number ①②③… at its tab's top-left. Rollback targets
	// carry no number of their own (they show "rollback ⟨N⟩" on their bottom border, N
	// being the forward block's number, recorded here as the forward block renders).
	isRollbackTarget := r.rollbackTargets[blk.ID]
	actionable := blk.Type == "shell" || blk.Type == "run" || blk.Type == "diff" || blk.Type == "create"
	num := 0
	if actionable && !isRollbackTarget {
		r.blockNum++
		num = r.blockNum
		if blk.Rollback != "" {
			r.rollbackForNum[blk.Rollback] = num
		}
	}
	numGlyph := ""
	numW := 0
	if num > 0 {
		g := itoa(num) // top-left block ID: "1", "2", …
		// Foreground encodes run progress: grey (default) = already acted on / manually
		// resolved; among un-run blocks, THE next one (first needs-satisfied) is green and
		// the rest red. Grey is the init so the switch only overrides the un-run states.
		numColor := colSubtext
		if r.states[blk.ID].Status == "" && !r.states[blk.ID].Resolved {
			if !r.nextNumAssigned && len(unmet) == 0 {
				numColor = colGreen // the single "next to run"
				r.nextNumAssigned = true
			} else {
				numColor = colRed // pending — not run and not the next one
			}
		}
		// Leading + trailing space, all on the code-block background (a little top-left
		// tab mirroring the button tab on the right).
		numGlyph = bg.Render(" ") + bg.Foreground(lipgloss.Color(numColor)).Render(g) + bg.Render(" ")
		numW = 1 + lipgloss.Width(g) + 1
	}

	// region width: leadpad(1) + langW + sep(" ❘ "=3) + run(2 if shell/run+unblocked) + play(2 if shell+unblocked) + diff(2 if diff) + apply-diff or undo-diff(2 if diff+unblocked or applied) + copy(2)
	regionW := 1 + langW + 3 + 2
	if (blk.Type == "shell" || blk.Type == "run") && len(unmet) == 0 && !isRollbackTarget {
		regionW += 2 // run(2)
	}
	if blk.Type == "shell" && len(unmet) == 0 && !isRollbackTarget && r.muxActive {
		regionW += 2 // play(2) — only under a mux (needs an origin pane)
	}
	if blk.Type == "diff" {
		regionW += 2 // diff(2) — always ungated; opens patch in float viewer
		// One action button after view-diff: undo-resolve on a manually-resolved block,
		// else undo-diff when applied (Status=="ok") or apply-diff when needs are met.
		if r.states[blk.ID].Resolved || r.states[blk.ID].Status == "ok" || len(unmet) == 0 {
			regionW += 2
		}
	}
	if blk.Type == "create" {
		regionW += 2 // create(2) or undo-create(2) — always shown (no needs gate)
	}
	fillCols := width - regionW - numW
	if fillCols < 0 {
		fillCols = 0
	}

	var sb strings.Builder
	// Top-left visual number (on the document bg, left of the tab's ▂ edge), then the
	// ▂ fill. col is advanced past both so button columns stay put (numW is reclaimed
	// from the fill, keeping the total tab width constant).
	sb.WriteString(numGlyph)
	sb.WriteString(codeFgANSI + strings.Repeat("▂", fillCols) + "\x1b[0m")
	col := numW + fillCols

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
		// A rollback-only block gets NO run/play/stop button — it executes solely via a
		// Rollback playbook chain. Its running…/✓ ran feedback still shows in the run
		// region below the tab.
		if (blk.Type == "shell" || blk.Type == "run") && !isRollbackTarget {
			runActionCol := col
			switch r.states[blk.ID].Status {
			case "running":
				sb.WriteString(r.buttonGlyph(blk.ID, "stop", glyphStop, colStop, bg))
				col++
				sb.WriteString(bg.Render(" "))
				col++
				r.buttons = append(r.buttons, Button{Line: lineIdx, Col: runActionCol, Width: 2, Kind: "stop", BlockID: blk.ID})
			case "ok":
				// Already ran successfully → disable the run action: a dimmed "done" cue
				// with no button registered, so an idempotency-unsafe re-run can't happen
				// by accident. An undo/rollback of a dependency clears this state and the
				// active button returns.
				sb.WriteString(bg.Foreground(lipgloss.Color(colOverlay0)).Render(glyphRun))
				col++
				sb.WriteString(bg.Render(" "))
				col++
			default:
				sb.WriteString(r.buttonGlyph(blk.ID, "run", glyphRun, colRun, bg))
				col++
				sb.WriteString(bg.Render(" "))
				col++
				r.buttons = append(r.buttons, Button{Line: lineIdx, Col: runActionCol, Width: 2, Kind: "run", Payload: runPayload(blk), BlockID: blk.ID})
			}
		}
		if blk.Type == "shell" && !isRollbackTarget && r.muxActive {
			playCol := col
			if r.states[blk.ID].Status == "ok" {
				// Done → disabled play, matching the run action's dimmed "done" cue.
				sb.WriteString(bg.Foreground(lipgloss.Color(colOverlay0)).Render(glyphPlay))
				col++
				sb.WriteString(bg.Render(" "))
				col++
			} else {
				sb.WriteString(r.buttonGlyph(blk.ID, "play", glyphPlay, colGreen, bg))
				col++
				sb.WriteString(bg.Render(" "))
				col++
				r.buttons = append(r.buttons, Button{Line: lineIdx, Col: playCol, Width: 2, Kind: "play", Payload: src, BlockID: blk.ID})
			}
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
		// A manually-resolved block shows an undo-resolve button (restores the pre-resolve
		// file) — there's no git patch to reverse, so Undo means "revert my manual edit".
		if r.states[blk.ID].Resolved {
			undoCol := col
			sb.WriteString(r.buttonGlyph(blk.ID, "undo-resolve", glyphUndo, colPeach, bg))
			col++
			sb.WriteString(bg.Render(" "))
			col++
			r.buttons = append(r.buttons, Button{Line: lineIdx, Col: undoCol, Width: 2, Kind: "undo-resolve", Payload: src, BlockID: blk.ID})
		} else if r.states[blk.ID].Status == "ok" {
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
	if blk.Type == "create" {
		// create: always shown, not needs-gated. Flips to undo-create once applied.
		createCol := col
		filePayload := orchestrator.EncodeFileAction(blk.File, blk.Payload)
		if r.states[blk.ID].Status == "ok" {
			sb.WriteString(r.buttonGlyph(blk.ID, "undo-create", glyphUndo, colPeach, bg))
			col++
			sb.WriteString(bg.Render(" "))
			col++
			r.buttons = append(r.buttons, Button{Line: lineIdx, Col: createCol, Width: 2, Kind: "undo-create", Payload: filePayload, BlockID: blk.ID})
		} else {
			sb.WriteString(r.buttonGlyph(blk.ID, "create", glyphCreate, colGreen, bg))
			col++
			sb.WriteString(bg.Render(" "))
			col++
			r.buttons = append(r.buttons, Button{Line: lineIdx, Col: createCol, Width: 2, Kind: "create", Payload: filePayload, BlockID: blk.ID})
		}
	}
	copyCol := col
	sb.WriteString(r.buttonGlyph(blk.ID, "copy", glyphCopy, colYellow, bg))
	// copy is the last button on the row, so col is not tracked past it.
	sb.WriteString(bg.Render(" "))
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
	} else if isRollbackTarget {
		// Rollback-only block: annotate the bottom edge with "rollback ⟨N⟩" (N = the
		// forward block's visual number), right-aligned, so it's clear this block is the
		// undo for step N. The ⟨N⟩ is omitted if the forward block hasn't been numbered.
		lbl := "rollback"
		if n := r.rollbackForNum[blk.ID]; n > 0 {
			lbl += " " + rollbackRef(n)
		}
		label := " " + lbl + " " // leading + trailing space
		fill := width - lipgloss.Width(label)
		if fill < 0 {
			fill = 0
		}
		// Code-block bg, peach fg — matching the "Rollback playbook" button by the error.
		styled := bg.Foreground(lipgloss.Color(colPeach)).Render(label)
		bottomLine := codeFgANSI + strings.Repeat("🮂", fill) + "\x1b[0m" + styled
		r.lines = append(r.lines, Line{Text: bottomLine, Wide: false, Code: true})
	} else {
		// Bottom edge bar: 🮂 characters in fg colCodeBg (#282C41), no background.
		// Total display width == width. Wide=false.
		bottomLine := codeFgANSI + strings.Repeat("🮂", width) + "\x1b[0m"
		r.lines = append(r.lines, Line{Text: bottomLine, Wide: false, Code: true})
	}

	if len(unmet) > 0 {
		r.emitBlocked(unmet)
	}

	// Drift region: when the diff block's patch no longer applies, emit a warning
	// message line + a [resolve manually] tag-button that opens the FC1 diff view.
	// Placed after the diff body (and any blocked notice) but before runRegion so it
	// sits visually between the block content and the run-state summary.
	//
	// A block the user reconciled by hand to a custom state (Resolved) shows a terminal
	// "resolved manually" note instead — no drift region, no apply/undo.
	if blk.Type == "diff" && r.states != nil && r.states[blk.ID].Resolved {
		note := lipgloss.NewStyle().Foreground(lipgloss.Color(colGreen)).Render("✓ resolved manually")
		r.lines = append(r.lines, Line{Text: "  " + note, Wide: false, Code: true})
	}
	if blk.Type == "diff" && r.states != nil && r.states[blk.ID].Drifted {
		const indentStr = "  "
		const indentW = 2
		// Message line: ⚠ (colPeach) + explanation (colSubtext). When RegenFailed is
		// set the alternate message is shown instead of the plain drift message.
		driftNote := "this diff no longer applies — the target file changed since it was written"
		if r.states[blk.ID].RegenFailed {
			// A failed regenerate: show the specific cause when we have one (F24 — e.g.
			// "no AI backend available…"), else the generic "resolve manually" alternate.
			if note := r.states[blk.ID].RegenNote; note != "" {
				driftNote = note
			} else {
				driftNote = "regenerate didn't resolve it — resolve manually"
			}
		}
		msgLine := indentStr +
			lipgloss.NewStyle().Foreground(lipgloss.Color(colPeach)).Render("⚠ ") +
			lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext)).Render(driftNote)
		r.lines = append(r.lines, Line{Text: msgLine, Wide: false, Code: true})
		// Button line: glyph + space + label for each button, separated by glyphSep.
		// Col is the position of each glyph within Line.Text, accumulated as
		// indentW + widths of preceding visible elements. Mirrors the followup
		// tag-button formula (see runRegion's followupCol computation).
		resolveLineIdx := len(r.lines)
		resolveGlyph := lipgloss.NewStyle().Foreground(lipgloss.Color(colBlue)).Render(glyphViewDiff)
		resolveLabel := lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext)).Render("resolve manually")
		sep := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0)).Render(glyphSep)
		regenGlyph := lipgloss.NewStyle().Foreground(lipgloss.Color(colBlue)).Render(glyphRetry)
		regenLabel := lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext)).Render("regenerate")
		r.lines = append(r.lines, Line{
			Text: indentStr + resolveGlyph + " " + resolveLabel + " " + sep + " " + regenGlyph + " " + regenLabel,
			Wide: false,
			Code: true,
		})
		// drift-resolve: glyph starts right after the 2-space indent.
		r.buttons = append(r.buttons, Button{
			Line:    resolveLineIdx,
			Col:     indentW,
			Width:   2,
			Kind:    "drift-resolve",
			Payload: src,
			BlockID: blk.ID,
		})
		// drift-regen: glyph starts after resolve glyph + space + label + space + sep + space.
		regenCol := indentW + lipgloss.Width(glyphViewDiff) + 1 + lipgloss.Width("resolve manually") + 1 + lipgloss.Width(glyphSep) + 1
		r.buttons = append(r.buttons, Button{
			Line:    resolveLineIdx,
			Col:     regenCol,
			Width:   2,
			Kind:    "drift-regen",
			Payload: src,
			BlockID: blk.ID,
		})
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
	case "regenerating":
		sl := spinnerLine(st.SpinFrame, "regenerating…", st.SpinFrame/10)
		r.lines = append(r.lines, Line{Text: indentStr + sl, Wide: false, Code: true})
	case "ok", "failed", "stopped", "rolledback":
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
		case "rolledback":
			// A rollback-chain target that finished — undone, in the rollback (peach) hue.
			sty := lipgloss.NewStyle().Foreground(lipgloss.Color(colPeach))
			label = "↺ step rolled back"
			statusPart = sty.Render(label)
		default: // "failed"
			sty := lipgloss.NewStyle().Foreground(lipgloss.Color(colRed))
			label = "✗ failed (exit " + itoa(st.Exit) + ")"
			statusPart = sty.Render(label)
			// When a rollback chain triggered by this failure has finished, append a
			// peach suffix so the failure line reads "✗ failed (exit N) — all steps rolled back".
			if st.RolledBack {
				suffix := " — all steps rolled back"
				statusPart += lipgloss.NewStyle().Foreground(lipgloss.Color(colPeach)).Render(suffix)
				label += suffix // keep label width in sync for toggleCol
			}
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
		// On a failed run/shell block, offer ONE extra action after the toggle:
		//   - "↻ try another fix" (followup) when in-process re-engagement is wired — the
		//     verify block hides it while auto-firing, showing it only past the cap; OR
		//   - "⤺ Rollback playbook" when re-engagement is unavailable (a plain run --file)
		//     and some already-run step declares a rollback= target.
		// Re-engagement takes precedence when both are possible.
		extraCol := -1
		extraKind := ""
		extraLabel := ""
		extraGlyph := ""
		if st.Status == "failed" && (blk.Type == "run" || blk.Type == "shell") {
			switch {
			case (id != "verify" || st.FollowupExhausted) && r.reengageAvail:
				extraKind, extraLabel, extraGlyph = "followup", "try another fix", glyphRetry
			case r.rollbackAvail:
				extraKind, extraLabel, extraGlyph = "rollback", "Rollback playbook", glyphUndo
			}
		}
		if extraKind != "" {
			sep := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0)).Render(glyphSep)
			extraCol = indentW + lipgloss.Width(label) + 1 + lipgloss.Width(rawToggle) + 1 + lipgloss.Width(glyphSep) + 1
			glyph := extraGlyph
			if r.flashKey != "" && r.flashKey == id+":"+extraKind {
				glyph = lipgloss.NewStyle().Foreground(lipgloss.Color(colFlashOn)).Bold(true).Render(glyph)
			} else {
				glyph = lipgloss.NewStyle().Foreground(lipgloss.Color(colPeach)).Render(glyph)
			}
			summary += " " + sep + " " + glyph + " " +
				lipgloss.NewStyle().Foreground(lipgloss.Color(colSubtext)).Render(extraLabel)
		}
		summaryLineIdx := len(r.lines)
		r.lines = append(r.lines, Line{Text: indentStr + summary, Wide: false, Code: true})
		r.buttons = append(r.buttons, Button{Line: summaryLineIdx, Col: toggleCol, Width: 2, Kind: "toggle", BlockID: id})
		if extraCol >= 0 {
			r.buttons = append(r.buttons, Button{Line: summaryLineIdx, Col: extraCol, Width: 2, Kind: extraKind, Payload: blk.Payload, BlockID: id})
		}
		// While a rollback chain triggered by this failure is running, show a spinner
		// under the failure so the automatic undo has a stated cause.
		if st.RollingBack {
			sl := spinnerLine(st.SpinFrame, "rolling back applied steps…", st.SpinFrame/10)
			r.lines = append(r.lines, Line{Text: indentStr + sl, Wide: false, Code: true})
		}
		if st.Expanded {
			tail := tailFile(st.Logpath, 50)
			if len(tail) == 0 {
				// An empty log isn't necessarily a missing one. A SUCCESSFUL diff-apply /
				// file-create shows an affirmative message; a shell/run block that simply
				// produced no output reads as "(no output)" — not the error-ish
				// "(log unavailable)", which is reserved for a genuinely missing log
				// (empty Logpath → writeRunLog failed). (st.Action is cleared once the
				// result lands, so we key off the block type + status, not st.Action.)
				msg := "(log unavailable)"
				if st.Logpath != "" {
					msg = "(no output)"
				}
				if st.Status == "ok" {
					switch blk.Type {
					case "diff":
						msg = "Diff applied successfully"
					case "create":
						msg = "File created"
					}
				}
				r.lines = append(r.lines, Line{Text: indentStr + msg, Wide: true, Bg: codeBgANSI, Code: true})
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
// Note: a script body containing a line that is exactly "__APB_RUN__" would
// break the heredoc — that constraint is acceptable for troubleshooting snippets.
func runPayload(blk Block) string {
	if blk.Type != "run" {
		return blk.Payload
	}
	return langInterp(blk.Lang) + " <<'__APB_RUN__'\n" + blk.Payload + "\n__APB_RUN__"
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

// collectRollbackTargets pre-scans the parsed document for every fenced block's
// rollback= attribute and returns the set of referenced target IDs. A block in this set
// is rollback-only — it renders without its own run/play button.
func collectRollbackTargets(doc ast.Node, src []byte) map[string]bool {
	targets := map[string]bool{}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if fc, ok := n.(*ast.FencedCodeBlock); ok && fc.Info != nil {
			_, attrs, _ := parseFenceInfo(string(fc.Info.Segment.Value(src)))
			if rb := attrs["rollback"]; rb != "" {
				targets[rb] = true
			}
		}
		return ast.WalkContinue, nil
	})
	return targets
}

// resetDependents clears the run-state of every block that transitively depends (via
// needs=) on rootID, so undoing/rolling back rootID drops their stale results and
// re-locks them instead of leaving a leftover "✓ ran" beside a now-blocked block. The
// root block itself is left untouched. Deleting an entry resets it to the zero
// (idle) blockRunState.
func resetDependents(states map[string]blockRunState, blocks []Block, rootID string) {
	depend := map[string]bool{rootID: true}
	for changed := true; changed; {
		changed = false
		for _, b := range blocks {
			if depend[b.ID] {
				continue
			}
			for _, n := range b.Needs {
				if depend[n] {
					depend[b.ID] = true
					changed = true
					break
				}
			}
		}
	}
	for id := range depend {
		if id != rootID {
			delete(states, id)
		}
	}
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

// quote renders a block quote as a bordered admonition frame.
// It collects the child block text, optionally detects a [!TYPE] marker, then
// emits a 5-glyph bordered frame: top border row, content rows with a ▐ left
// bar, and a bottom border row. Corner and left-bar cells use the accent color
// on the document background; top/bottom sextant cells use the callout-bg tone
// as their foreground on the document background. Content sits on a
// darkened-accent background. No right-border glyph. A bare quote (no marker)
// is framed with the colOverlay0 fallback accent and no header row.
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

	// Step 3: determine accent color and dark background.
	color := colOverlay0
	if a != nil {
		color = a.color
	}
	calloutBgTone := darken(color, 0.20)
	bg := bgANSI(calloutBgTone)

	// Step 4: build per-cell styles.
	// Accent cells (TL corner, CL left bar, BL corner) — accent fg, inherit document bg.
	accentSty := lipgloss.NewStyle().Foreground(lipgloss.Color(color))
	// Tone cells (TB top border, BB bottom border) — callout-bg-tone fg, inherit document bg.
	toneSty := lipgloss.NewStyle().Foreground(lipgloss.Color(calloutBgTone))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(colText)).Italic(true)

	// Width available for content: total width minus the left-bar cell (1 col).
	contentWidth := r.width - 1
	if contentWidth < 1 {
		contentWidth = 1
	}

	// Step 5: top border row — TL corner + (contentWidth) TB sextants, all on doc bg.
	topRow := accentSty.Render(calloutTL) + strings.Repeat(toneSty.Render(calloutTB), contentWidth)
	r.lines = append(r.lines, Line{Text: topRow, Wide: false, Callout: true})

	// Step 6: content rows.
	// Left bar: accent fg on document bg (single cell). Content: band over callout bg.
	leftBar := accentSty.Render(calloutCL)

	// Helper: emit one content row with the left bar + bg-banded content.
	emitContent := func(text string) {
		content := band(" "+text, bg, contentWidth)
		r.lines = append(r.lines, Line{Text: leftBar + content, Wide: false, Callout: true})
	}

	// Header row (only for recognized [!type] admonitions).
	if a != nil {
		headerText := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(a.icon + " " + a.title)
		emitContent(headerText)
	}

	// Body rows. Wrap to contentWidth-2: leading space (1) + text + trailing pad (1).
	trimmed := strings.TrimSpace(body)
	if trimmed != "" {
		w := contentWidth - 2
		if w < 1 {
			w = 1
		}
		wrapped := bodyStyle.Width(w).Render(trimmed)
		for _, ln := range strings.Split(wrapped, "\n") {
			emitContent(ln)
		}
	}

	// Step 7: bottom border row — BL corner + (contentWidth) BB sextants, all on doc bg.
	botRow := accentSty.Render(calloutBL) + strings.Repeat(toneSty.Render(calloutBB), contentWidth)
	r.lines = append(r.lines, Line{Text: botRow, Wide: false, Callout: true})
}
