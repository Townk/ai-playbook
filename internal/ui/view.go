package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
)

// h1Heading matches the first markdown H1 line `# <title>` (one or more spaces or
// tabs after the hash), capturing the trimmed title text.
var h1Heading = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t]*$`)

// playbookHeading splits a finalized-playbook markdown body at its first H1 title.
// It returns the heading text (e.g. "Playbook — Compiling an Android Application")
// and the body from that H1 line onward, with any preamble prose ABOVE the title
// removed. When md has no H1, title is "" and body is md unchanged (a transcript,
// not a playbook — do NOT strip).
//
// Limitation: the scan is a simple first-`^# ` match and does NOT skip `#` lines
// inside fenced code blocks. A finalized playbook leads with its H1 title before
// any fence, so in practice the title is matched first; a leading fenced `# foo`
// would be a false positive, but that doesn't occur for our generated playbooks.
// loadPlaybookDocument parses a finalized/served playbook document for display:
// it strips any leading YAML front matter (frontmatter.Parse), then strips any
// preamble above the H1 (playbookHeading) from the remaining body. It returns
// the pager title (the front-matter `name` when present, else the H1 heading),
// the front-matter `description` as the subtitle (empty when there is no front
// matter / no description), and the front-matter-stripped body to render/stash.
//
// A document WITHOUT front matter degrades to the prior behavior: subtitle is
// empty and the title comes from the H1 (a transcript with no H1 keeps an empty
// title and an unchanged body).
func loadPlaybookDocument(content string) (title, subtitle, body string, env map[string]frontmatter.EnvValue) {
	fm, rest, ok := frontmatter.Parse(content)
	h1, stripped := playbookHeading(rest)
	body = stripped
	if ok {
		subtitle = fm.Description
		env = fm.Env
		if fm.Name != "" {
			title = fm.Name
			return title, subtitle, body, env
		}
	}
	title = h1
	return title, subtitle, body, env
}

// isValidPlaybook reports whether md is a REAL final playbook rather than a narration:
// it must carry an H1 title (playbookHeading finds one) AND at least one runnable block
// (blocks > 0, the count parsed by Render). Used to guard the final-playbook draft at
// stream-EOF and as a backstop before any commit, so a narrated non-playbook is never
// displayed in place of the troubleshoot nor saved/cached.
func isValidPlaybook(md string, blocks int) bool {
	title, _ := playbookHeading(md)
	return title != "" && blocks > 0
}

func playbookHeading(md string) (title, body string) {
	loc := h1Heading.FindStringSubmatchIndex(md)
	if loc == nil {
		return "", md
	}
	title = strings.TrimSpace(md[loc[2]:loc[3]])
	body = md[loc[0]:]
	return title, body
}

// renderBody is the document to RENDER in the scroll area. For a finalized playbook
// the H1 title is shown in the pager header (m.title), so drop that leading H1 line
// (and the blank lines after it) from the body to avoid a double title. m.md itself
// keeps the H1 — it is what gets committed/saved. No title → render m.md as-is.
func (m model) renderBody() string {
	if m.title == "" {
		return m.md
	}
	i := strings.IndexByte(m.md, '\n')
	if i < 0 {
		if h1Heading.MatchString(m.md) { // only the title, nothing below
			return ""
		}
		return m.md
	}
	if h1Heading.MatchString(m.md[:i]) {
		return strings.TrimLeft(m.md[i+1:], "\n")
	}
	return m.md // first line isn't the H1 (shouldn't happen for a finalized playbook)
}

// headerWrapWidth is the cell width the title and subtitle wrap at: header
// content never runs past 80 display columns even in a wider pane (and clamps
// to the pane width when narrower).
const headerWrapWidth = 80

// titleTextCol is the screen column where the title TEXT begins: the 2-col
// pane margin + the "▓▓▓ " prefix (3 decorative blocks + 1 space). Wrapped
// title continuation lines and every subtitle line align at this column.
const titleTextCol = 6

// headerLimit returns the effective header wrap width: headerWrapWidth cells,
// clamped to the pane width when the pane is narrower (m.width==0 — unsized
// tests/startup — keeps the 80-cell default).
func (m model) headerLimit() int {
	if m.width > 0 && m.width < headerWrapWidth {
		return m.width
	}
	return headerWrapWidth
}

// headerLabel is the plain (unstyled) title text: the playbook title when set,
// else the "ai-playbook — <harness>" default.
func (m model) headerLabel() string {
	if m.title != "" {
		return m.title
	}
	return "ai-playbook — " + m.harness
}

// titleLines returns the styled header rows: "  ▓▓▓ <title>" word-wrapped at
// headerLimit cells (display width via lipgloss.Width, word boundaries via
// wrapWithHardBreak), continuation lines aligned under the title's first text
// character (titleTextCol). When a step failed, the playbook-level "⚠ a step
// failed" cue flows after the title text — appended to the last title line
// when it fits within the limit, else on its own aligned continuation row.
func (m model) titleLines() []string {
	limit := m.headerLimit()
	avail := limit - titleTextCol
	if avail < 1 {
		avail = 1
	}
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color(colMauve)).Bold(true)
	wrapped := wrapWithHardBreak(m.headerLabel(), avail)
	rows := make([]string, 0, len(wrapped))
	for i, ln := range wrapped {
		if i == 0 {
			rows = append(rows, "  "+styled.Render(strings.Repeat("▓", 3)+" "+ln))
			continue
		}
		rows = append(rows, strings.Repeat(" ", titleTextCol)+styled.Render(ln))
	}
	if m.anyStepFailed() {
		// Failure cue: a prior step failed, so a later health-check (e.g. Verify)
		// is not presented as if all is well.
		const cueText = "⚠ a step failed"
		cue := lipgloss.NewStyle().Foreground(lipgloss.Color(colRed)).Render(cueText)
		last := len(rows) - 1
		if lipgloss.Width(rows[last])+2+lipgloss.Width(cueText) <= limit {
			rows[last] += "  " + cue
		} else {
			rows = append(rows, strings.Repeat(" ", titleTextCol)+cue)
		}
	}
	return rows
}

// titleRows returns the number of header rows the wrapped title occupies.
// Single source of truth for the title's layout height (>= 1).
func (m *model) titleRows() int {
	return len(m.titleLines())
}

// anyStepFailed reports whether any block ended in the failed state. Drives the
// header's playbook-level "a step failed" indicator (F11).
func (m model) anyStepFailed() bool {
	for _, st := range m.blockStates {
		if st.Status == "failed" {
			return true
		}
	}
	return false
}

// subtitleRowStrings returns the styled subtitle rows (the front-matter
// description) shown directly under the ▓▓▓ title for a finalized/served
// playbook that carries one: the text word-wraps at headerLimit cells and
// every line starts at titleTextCol, so the subtitle's first character aligns
// with the title's first text character. Nil when there is no subtitle (no
// extra header rows). The text is dim (overlay) so it reads as a caption.
func (m model) subtitleRowStrings() []string {
	if m.subtitle == "" {
		return nil
	}
	avail := m.headerLimit() - titleTextCol
	if avail < 1 {
		avail = 1
	}
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0))
	wrapped := wrapWithHardBreak(m.subtitle, avail)
	rows := make([]string, 0, len(wrapped))
	for _, ln := range wrapped {
		rows = append(rows, strings.Repeat(" ", titleTextCol)+st.Render(ln))
	}
	return rows
}

// subtitleRows returns the number of extra header rows the wrapped subtitle
// occupies (0 when there is none). Single source of truth for the layout
// delta the subtitle introduces (mirrors titleRows()).
func (m *model) subtitleRows() int {
	if m.subtitle == "" {
		return 0
	}
	return len(m.subtitleRowStrings())
}

// relativeAge formats the age of cachedAt relative to now as a short string:
// "just now" (<60s), "<N>m ago" (<60m), "<N>h ago" (<24h), else "<N>d ago".
func relativeAge(cachedAt time.Time) string {
	d := time.Since(cachedAt)
	if d < 0 {
		d = 0
	}
	switch {
	case d < 60*time.Second:
		return "just now"
	case d < 60*time.Minute:
		return itoa(int(d/time.Minute)) + "m ago"
	case d < 24*time.Hour:
		return itoa(int(d/time.Hour)) + "h ago"
	default:
		return itoa(int(d/(24*time.Hour))) + "d ago"
	}
}

// cachedBadge returns the styled powerline pill string for a cached-replay
// result, followed by exactly 1 trailing space. The pill is composed of:
//
//	capL (U+E0B6, fg=colPeach, no bg) +
//	body (bg=colPeach, fg=colBase: db-icon U+F1C0, " cached · <age> ", reload-icon U+10F1DA) +
//	capR (U+E0B4, fg=colPeach, no bg) +
//	" " (trailing space)
//
// The caps use only a foreground colour (colPeach) so their background is the
// terminal's pane background, creating the classic powerline blended-end look.
// The entire body (including both icons) uses one continuous colPeach background
// to avoid the PUA-glyph background-mismatch shift-down bug.
//
// When m.flashKey == "cached:regenerate" (the pill was clicked) the WHOLE pill
// highlights: caps + body switch to the bright flash colour (colFlashOn) as the
// background with dark bold text, so the entire button lights up. The background
// stays continuous across the whole body (both glyphs included), so there's no
// per-glyph background-mismatch row-shift.
func (m model) cachedBadge() string {
	if !m.isCached {
		return ""
	}
	capFg, bodyBg, bodyFg, bold := colPeach, colPeach, colBase, false
	if m.driverPending {
		// Async startup: the reload is inert until the orchestrator lands — render the
		// whole pill muted (grey caps + grey body with overlay text) so it reads as
		// disabled. Same geometry, so it doesn't jump when it enables.
		capFg, bodyBg, bodyFg = colSurface1, colSurface1, colOverlay0
	}
	if m.flashKey == "cached:regenerate" {
		capFg, bodyBg, bodyFg, bold = colFlashOn, colFlashOn, colBase, true
	}
	capStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(capFg))
	bodyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(bodyFg)).
		Background(lipgloss.Color(bodyBg)).
		Bold(bold)

	const reloadIcon = "\U0010F1DA"
	// The reload glyph renders only when regeneration is actually POSSIBLE (an
	// orchestrator re-engagement OR the cached-answer seam). With neither wired the
	// pill stays informational (db-icon + "cached · <age>") and shows no dead reload.
	prefix := "\U0000F1C0 cached · " + relativeAge(m.cachedAt) + " "
	bodyText := prefix
	if m.canRegenerate() {
		bodyText += reloadIcon
	} else {
		// Drop the single trailing pad space that separated the age from the glyph so
		// the pill isn't left with a dangling space when the reload is omitted.
		bodyText = strings.TrimRight(bodyText, " ")
	}
	capL := capStyle.Render("\U0000E0B6")
	body := bodyStyle.Render(bodyText)
	capR := capStyle.Render("\U0000E0B4")
	return capL + body + capR + " "
}

// appendCachedButton adds the screen-fixed regenerate button to m.buttons when
// isCached is true. The ENTIRE pill is the click target; the flash highlight
// covers the whole pill (handled in cachedBadge). Line is the shared badges
// row's absolute screen row (badgeRowIdx). The badges row is indented to
// titleTextCol (subtitle alignment); buttonAt strips the 2-col left margin,
// so Col is titleTextCol-2 for the left cap (the cached pill is always the
// row's first badge). Width is the pill's visible width minus the trailing
// space. Screen=true so buttonAt resolves it by absolute Y, not content line.
func (m *model) appendCachedButton() {
	// Only add the clickable regenerate button when regeneration is actually possible
	// (an orchestrator re-engagement OR the cached-answer seam). With neither wired the
	// reload glyph isn't rendered (see cachedBadge), so a click target would be dead.
	if !m.canRegenerate() {
		return
	}
	pillW := lipgloss.Width(m.cachedBadge()) - 1 // drop the trailing space
	if pillW < 1 {
		pillW = 1
	}
	m.buttons = append(m.buttons, Button{
		Line:    m.badgeRowIdx(),
		Col:     titleTextCol - 2,
		Width:   pillW,
		Kind:    "regenerate",
		BlockID: "cached",
		Screen:  true,
	})
}

// reloadIconScreenCol returns the absolute screen column of the reload glyph in
// the cached pill: the titleTextCol indent + left cap (1) + the pill prefix
// width (db icon + " cached · <age> "). The cached pill is always the badges
// row's first badge, so no cross-pill offset applies. Used to anchor the
// regenerate hint label over the glyph.
func (m model) reloadIconScreenCol() int {
	prefix := "\U0000F1C0 cached · " + relativeAge(m.cachedAt) + " "
	return titleTextCol + 1 + lipgloss.Width(prefix)
}

// editIcon is the pencil glyph shown inside the [edit] pill body.
const editIcon = "\U0010F304"

// editBadge returns the styled powerline pill string for the file-backed [edit]
// affordance, followed by exactly 1 trailing space. The pill mirrors cachedBadge:
// capL (U+E0B6) + body (bg=colGreen, fg=colBase: pencil icon + " edit") + capR
// (U+E0B4) + " ". When m.flashKey == "edit:edit" (the pill was just activated)
// the whole pill highlights with the flash colour, like every other pill button.
// Returns "" when sourcePath is empty (ephemeral playbook).
func (m model) editBadge() string {
	if m.sourcePath == "" {
		return ""
	}
	capFg, bodyBg, bodyFg, bold := colGreen, colGreen, colBase, false
	if m.flashKey == "edit:edit" {
		capFg, bodyBg, bodyFg, bold = colFlashOn, colFlashOn, colBase, true
	}
	capStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(capFg))
	bodyStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(bodyFg)).
		Background(lipgloss.Color(bodyBg)).
		Bold(bold)
	capL := capStyle.Render("\U0000E0B6")
	body := bodyStyle.Render(editIcon + " edit")
	capR := capStyle.Render("\U0000E0B4")
	return capL + body + capR + " "
}

// appendEditButton adds the screen-fixed [edit] button to m.buttons when
// sourcePath is non-empty (file-backed playbook). The pill sits on the shared
// badges row (badgeRowIdx), left-grouped after the cached pill — whose width
// (trailing space included) offsets the edit pill; 0 when not cached. The row
// is indented to titleTextCol (subtitle alignment); Col is content-relative
// (buttonAt strips the 2-col margin), so the base offset is titleTextCol-2.
// The WHOLE pill is the click target. Screen=true so buttonAt resolves it by
// absolute Y.
func (m *model) appendEditButton() {
	if m.sourcePath == "" {
		return
	}
	badgeW := lipgloss.Width(m.editBadge()) - 1 // drop the trailing space for the hit target
	if badgeW < 1 {
		badgeW = 1
	}
	m.buttons = append(m.buttons, Button{
		Line:    m.badgeRowIdx(),
		Col:     titleTextCol - 2 + lipgloss.Width(m.cachedBadge()),
		Width:   badgeW,
		Kind:    "edit",
		BlockID: "edit",
		Screen:  true,
	})
}

// editBadgeScreenCol returns the absolute screen column of the pencil icon in
// the edit pill: the titleTextCol indent + the cached pill's width (0 when not
// cached) + the left cap (1). Used to anchor the edit hint label over the icon.
func (m model) editBadgeScreenCol() int {
	return titleTextCol + lipgloss.Width(m.cachedBadge()) + 1
}

// badgesRowString returns the shared badges row shown directly BELOW the
// subtitle (or the title when there is no subtitle): the cached pill then the
// edit pill, left-grouped with a single-space gap (each badge helper carries
// its own trailing space). Empty when neither badge is present — no badges
// row is emitted then (the plain top-pad follows the header directly).
func (m model) badgesRowString() string {
	row := m.cachedBadge() + m.editBadge()
	if row == "" {
		return ""
	}
	return strings.Repeat(" ", titleTextCol) + row
}

// badgeRows returns the number of header rows the badges row occupies: 1 when
// either badge is present (cached replay and/or file-backed playbook), else 0.
// Single source of truth for the badges row's layout delta.
func (m *model) badgeRows() int {
	if m.isCached || m.sourcePath != "" {
		return 1
	}
	return 0
}

// badgeRowIdx returns the absolute screen row (0-based) of the shared badges
// row: the leading blank + the wrapped title rows + the wrapped subtitle rows.
// Only meaningful when badgeRows() == 1.
func (m *model) badgeRowIdx() int {
	return 1 + m.titleRows() + m.subtitleRows()
}

// statusBar is the slim, mode-aware bottom hint.
func (m model) statusBar() string {
	st := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0))
	ind := m.constraintIndicator()
	if m.status != "" && !m.hintMode && !m.helpMode && !m.diffMode {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(colPeach)).Render(m.status) + ind
	}
	if m.hintMode || m.helpMode || m.diffMode {
		return st.Render("\U000F12B7: cancel")
	}
	return st.Render("\U000F1050: action • \U000F12B7: close • ?: keys") + ind
}

// constraintIndicator is the persistent status-line segment shown while any
// session constraints (refused approaches, refuse-solution §3) are active: a
// pluralized `N constraint(s)` count. Empty when none are active.
func (m model) constraintIndicator() string {
	n := len(m.refusals)
	if n == 0 {
		return ""
	}
	noun := "constraint"
	if n != 1 {
		noun += "s"
	}
	seg := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0))
	return seg.Render(fmt.Sprintf("  • %d %s", n, noun))
}

// viewString assembles the full rendered frame as a plain string. View wraps
// this in tea.NewView so that tests can call viewString() directly without
// needing to extract Content from a tea.View.
func (m model) viewString() string {
	cw := m.contentWidth()
	var sb strings.Builder

	if m.askMode {
		// The ask overlay composites the dialog centered over the live document,
		// exactly like the help modal — the playbook keeps rendering behind it.
		sb.WriteString(m.askOverlay())
		return sb.String()
	}

	if m.hintMode {
		// Labels float on the line above each button (or below when the line
		// above is scrolled off the top). Screen-fixed buttons (the cached and
		// edit pills) are skipped here — they live in the header, not the
		// scrollable body; their labels are painted over the badges row in the
		// header block below (anchored to each pill's icon column).
		labelsByRow := map[int]map[int]string{}
		for label, b := range m.hintLabels {
			if b.Screen {
				continue // handled separately in the header region
			}
			// Labels normally float on the line ABOVE the button. But when that line
			// already holds text at the button's column (F20: the drift warning banner
			// sits directly above the resolve/regenerate buttons), floating there would
			// paint the letter over that text — so drop the label onto the button's OWN
			// line instead. When the line above is scrolled off the top, float below.
			row := b.Line - 1
			switch {
			case row < m.yOff:
				row = b.Line + 1
			case !m.lineBlank(row):
				row = b.Line
			}
			if labelsByRow[row] == nil {
				labelsByRow[row] = map[int]string{}
			}
			labelsByRow[row][b.Col] = label
		}
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0))
		lab := lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color(colHintLabelFg)).
			Background(lipgloss.Color(colHintLabelBg))

		// Button glyph columns per tab line — given the hint-label dark-red bg —
		// and pill-button spans per line — re-rendered with the inverted greyed
		// fill so a pill keeps its filled shape (hintCodeRow pillSpans). Only
		// body buttons (not Screen-fixed) are tracked for code-row highlighting.
		buttonColsByRow := map[int]map[int]bool{}
		pillSpansByRow := map[int][][2]int{}
		for _, b := range m.buttons {
			if b.Screen {
				continue
			}
			if b.Pill {
				pillSpansByRow[b.Line] = append(pillSpansByRow[b.Line], [2]int{b.Col, b.Width})
				continue
			}
			if buttonColsByRow[b.Line] == nil {
				buttonColsByRow[b.Line] = map[int]bool{}
			}
			buttonColsByRow[b.Line][b.Col] = true
		}

		rows := Window(m.lines, m.xOff, m.yOff, cw, m.body())
		pos, size := vthumb(len(m.lines), m.body(), m.yOff)
		sb.WriteString("\n")
		for _, tl := range m.titleLines() {
			sb.WriteString(tl + "\n")
		}
		for _, sl := range m.subtitleRowStrings() {
			sb.WriteString(sl + "\n") // description caption under the title
		}
		if badges := m.badgesRowString(); badges != "" {
			// The header pills' hint labels are painted directly OVER the badges row
			// (anchored at each pill's icon column): the row above is the subtitle /
			// title — never blank — so the F20 own-line fallback applies, exactly as
			// for the drift buttons under their warning banner.
			badges = padTo(badges, m.width)
			for lbl, b := range m.hintLabels {
				if !b.Screen {
					continue
				}
				switch b.Kind {
				case "regenerate":
					badges = spliceOver(badges, lab.Render(lbl), m.reloadIconScreenCol())
				case "edit":
					badges = spliceOver(badges, lab.Render(lbl), m.editBadgeScreenCol())
				}
			}
			sb.WriteString(badges + "\n") // badges row (cached + edit pills, + hint labels)
		}
		sb.WriteString("\n") // top-pad (single blank)
		for i, row := range rows {
			idx := m.yOff + i
			row = m.spinRow(idx, row) // advance any run spinner at View time (B1c)
			var base string
			if idx >= 0 && idx < len(m.lines) && m.lines[idx].Code {
				base = hintCodeRow(row, cw, buttonColsByRow[idx], pillSpansByRow[idx]) // fill + button cells + inverted pills
			} else if idx >= 0 && idx < len(m.lines) && m.lines[idx].Callout {
				base = hintCalloutRow(row, cw) // dimmed but keeps the framed-block look
			} else {
				base = dim.Render(padTo(strip(row), cw))
			}
			base = overlayLabels(base, labelsByRow[idx], lab)
			sb.WriteString("  " + base + vscrollCell(i, pos, size) + "\n")
		}
		sb.WriteString("\n")
		sb.WriteString("  " + m.statusBar())
	} else if m.helpMode {
		// The modal is an overlay: render the live document, then composite the
		// keybinding box over it (centered), so the markdown keeps showing and
		// updating behind the modal while help is open.
		base := m.normalLines()
		box := strings.Split(m.helpModal(), "\n")
		boxH := len(box)
		boxW := 0
		if boxH > 0 {
			boxW = lipgloss.Width(box[0])
		}
		left := (m.width - boxW) / 2
		if left < 0 {
			left = 0
		}
		top := 2 + (m.height-4-boxH)/2 // centered in the body region (below the 2 top rows)
		if top < 2 {
			top = 2
		}
		for i, bl := range box {
			if r := top + i; r >= 0 && r < len(base) {
				base[r] = spliceOver(base[r], bl, left)
			}
		}
		sb.WriteString(strings.Join(base, "\n"))
	} else if m.diffMode {
		// The diff overlay composites the side-by-side diff box centered over the
		// live document, exactly like the help modal — the playbook keeps rendering
		// behind it. The box is built by diffModal and scrolled via diffYOff/diffXOff.
		base := m.normalLines()
		box := strings.Split(m.diffModal(), "\n")
		boxH := len(box)
		boxW := 0
		if boxH > 0 {
			boxW = lipgloss.Width(box[0])
		}
		left := (m.width - boxW) / 2
		if left < 0 {
			left = 0
		}
		// The diff box is near-full-height (m.height-2), so center it over the
		// whole viewport — 1 blank line above and below — rather than nudging it
		// below the 2 top rows (which would push a full-height box off the bottom).
		top := (m.height - boxH) / 2
		if top < 0 {
			top = 0
		}
		for i, bl := range box {
			if r := top + i; r >= 0 && r < len(base) {
				base[r] = spliceOver(base[r], bl, left)
			}
		}
		sb.WriteString(strings.Join(base, "\n"))
	} else {
		sb.WriteString(strings.Join(m.normalLines(), "\n"))
	}

	return sb.String()
}

// normalLines renders the standard document view as m.height lines, each padded
// to the full pane width. It is the base layer both for normal mode and for the
// help overlay (which composites the modal box over these lines).
// spinRow returns the View-time text for the document line at idx. For a run-region
// spinner line (SpinID set) it regenerates the row from the block's CURRENT SpinFrame
// so the glyph and elapsed seconds advance without a reflow (B1c); the "  " prefix
// mirrors the runRegion indent baked into the original Line.Text. Any other line is
// returned unchanged. This is the whole no-reflow-per-tick mechanism: a spinTick only
// bumps SpinFrame, and the next View paints the advanced glyph here.
func (m model) spinRow(idx int, row string) string {
	if idx < 0 || idx >= len(m.lines) {
		return row
	}
	ln := m.lines[idx]
	if ln.SpinID == "" {
		return row
	}
	frame := m.blockStates[ln.SpinID].SpinFrame
	return "  " + spinnerLine(frame, ln.SpinLabel, frame/10)
}

func (m model) normalLines() []string {
	cw := m.contentWidth()
	rows := Window(m.lines, m.xOff, m.yOff, cw, m.body())
	pos, size := vthumb(len(m.lines), m.body(), m.yOff)
	pad := func(s string) string { return padTo(s, m.width) }
	out := make([]string, 0, m.height)
	out = append(out, pad("")) // leading blank
	for _, tl := range m.titleLines() {
		out = append(out, pad(tl)) // title (wrapped at headerLimit)
	}
	for _, sl := range m.subtitleRowStrings() {
		out = append(out, pad(sl)) // description caption under the title
	}
	if badges := m.badgesRowString(); badges != "" {
		out = append(out, pad(badges)) // badges row (cached + edit pills)
	}
	out = append(out, pad("")) // top-pad (single blank)
	spinRow := -1
	actRow := -1
	if m.thinking {
		// Spinner sits just below the last real content line visible from the top
		// of the body (or the first body row when empty), within the body region.
		spinRow = len(m.lines) - m.yOff
		if spinRow < 0 {
			spinRow = 0
		}
		// Issue #2: when there's content above the spinner (the natural row > 0, e.g.
		// the follow-up "_That didn't work…_" phrase), leave ONE blank body row between
		// that content and the "Working…" line so the spinner reads as a fresh section,
		// not glued to the prose. At the very top (initial authoring / empty doc) keep
		// the spinner on row 0 with no leading gap.
		if spinRow > 0 {
			spinRow++
		}
		if spinRow > m.body()-1 {
			spinRow = m.body() - 1
		}
		// The live agent-activity line (when any) sits on the row directly below the
		// spinner, as long as there's room in the body. claude --print is silent for
		// minutes during its tool-use phase, so this row shows the agent's latest tool
		// call (e.g. "⟳ run: gg build") next to the animating spinner.
		if m.progress.activity != "" && spinRow+1 <= m.body()-1 {
			actRow = spinRow + 1
		}
	}
	// Pre-render the progress widget so we split it once, using the two parts at
	// spinRow (spinner + elapsed) and actRow (activity line).
	var progressSpinRow, progressActRow string
	if spinRow >= 0 {
		rendered := m.progress.Render(cw)
		progressSpinRow, progressActRow, _ = strings.Cut(rendered, "\n")
	}
	for i := 0; i < m.body(); i++ {
		if i == spinRow {
			// Issue #3: use the dynamic working-progression label (workingLabel) via
			// the ProgressWidget. The widget resets per thinking session, so each
			// authoring/follow-up wait restarts at "Working…" and escalates on a 15s
			// cadence, holding the tail. The progression is the desired behavior even
			// when a non-default --thinking-label is configured — for the live wait we
			// intentionally prefer the escalating reassurance over a static custom label.
			out = append(out, pad("  "+padTo(progressSpinRow, cw)+vscrollCell(spinRow, pos, size)))
			continue
		}
		if i == actRow {
			out = append(out, pad("  "+padTo(progressActRow, cw)+vscrollCell(actRow, pos, size)))
			continue
		}
		if i < len(rows) {
			row := rows[i]
			idx := m.yOff + i
			if idx >= 0 && idx < len(m.lines) && m.lines[idx].HBar > 0 {
				row = hscrollbarRow(m.lines[idx].HBar, m.xOff, cw, colCodeBg)
			} else {
				row = m.spinRow(idx, row)
			}
			out = append(out, pad("  "+padTo(row, cw)+vscrollCell(i, pos, size)))
		} else {
			out = append(out, pad(""))
		}
	}
	// The confirm (when shown) occupies the questionLines+4 bottom rows directly above the
	// status bar (spec §A: inline rows in the pane, not a mux float): a blank, the wrapped
	// question (N lines, each with the body's 2-col left indent so it stays inside the
	// pane), a blank, the Yes / No buttons on their own row, then a blank — so the block
	// reads with breathing room and the buttons stay pinned at m.height-3. body() reserves
	// these rows so the confirm never overlaps real content. Otherwise a single bottom pad.
	if m.confirmResolved {
		out = append(out, pad("")) // blank above the question
		for _, q := range m.confirmQuestionRows() {
			out = append(out, pad("  "+q)) // wrapped question line(s)
		}
		out = append(out, pad(""))                               // blank   (m.height-4)
		out = append(out, pad("  "+m.confirmButtonsRowString())) // buttons (m.height-3)
		out = append(out, pad(""))                               // blank   (m.height-2)
	} else if m.assistedFooterActive() {
		// The GUIDED footer mirrors the confirm block's shape (blank, content,
		// blank, buttons, blank) so the buttons row lands on the SAME pinned
		// m.height-3 row appendAssistedFooter registers its Screen buttons on.
		rows := m.assistedFooterRows()
		var ctx, btns string
		if len(rows) == 2 {
			ctx, btns = rows[0], rows[1]
		}
		out = append(out, pad(""))        // blank above the context line
		out = append(out, pad("  "+ctx))  // context line
		out = append(out, pad(""))        // blank   (m.height-4)
		out = append(out, pad("  "+btns)) // buttons (m.height-3)
		out = append(out, pad(""))        // blank   (m.height-2)
	} else {
		out = append(out, pad("")) // bottom pad
	}
	out = append(out, pad("  "+m.statusBar())) // status bar
	return out
}

func (m model) View() tea.View {
	v := tea.NewView(m.viewString())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	// Issue #5 (cont.): receive tea.FocusMsg so we can re-assert the hide-cursor
	// when the pager regains focus (e.g. after the thinking float closes); some
	// terminals re-show the cursor on focus.
	v.ReportFocus = true
	// Issue #5: hide the hardware cursor in the pager. In bubbletea v2 the cursor is
	// shown ONLY when the View carries a non-nil Cursor (the cursed_renderer derives
	// showCursor := view.Cursor != nil and emits the hide-cursor sequence otherwise).
	// We render no editable field, so leaving Cursor nil keeps the blinking terminal
	// cursor hidden while scrolling. Set explicitly to document the intent.
	v.Cursor = nil
	return v
}

// staticRender returns the full rendered content (no scroll chrome) for
// printing to the pane on exit, so the docked pane parks showing the reply.
// Content is wrapped at contentWidth and left-padded with 2 spaces to match
// the interactive View().
func (m model) staticRender() string {
	cw := m.contentWidth()
	lines, _, _ := Render(m.renderBody(), cw, RenderOpts{States: m.blockStates})
	var sb strings.Builder
	for _, tl := range m.titleLines() {
		sb.WriteString(tl + "\n")
	}
	for _, sl := range m.subtitleRowStrings() {
		sb.WriteString(sl + "\n") // description caption under the title
	}
	if badges := m.badgesRowString(); badges != "" {
		sb.WriteString(badges + "\n") // badges row (cached + edit pills)
	}
	sb.WriteString("\n") // top-pad (single blank)
	for _, l := range lines {
		sb.WriteString("  " + l.Text + "\n")
	}
	return sb.String()
}
