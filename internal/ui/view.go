package ui

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"

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

func (m model) header() string {
	label := "ai-playbook — " + m.harness
	if m.title != "" {
		label = m.title
	}
	styled := lipgloss.NewStyle().Foreground(lipgloss.Color(colMauve)).Bold(true).
		Render(strings.Repeat("▓", 3) + " " + label)
	if m.anyStepFailed() {
		// Playbook-level failure cue: a prior step failed, so a later health-check (e.g.
		// Verify) is not presented as if all is well. Left-flowing after the title, so it
		// never collides with the right-aligned [edit]/cached badges.
		styled += lipgloss.NewStyle().Foreground(lipgloss.Color(colRed)).Render("  ⚠ a step failed")
	}
	return styled
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

// subtitleRowString returns the styled subtitle row (the front-matter
// description) shown directly under the ▓▓▓ title for a finalized/served playbook
// that carries one, with the standard 2-col left margin and indented to align
// under the title text. It returns "" when there is no subtitle (so no extra
// header row is emitted). The text is dim (subtext) so it reads as a caption.
func (m model) subtitleRowString() string {
	if m.subtitle == "" {
		return ""
	}
	// 2-col pane margin + 4 cols (3 ▓ + 1 space) to align under the title text.
	return "  " + lipgloss.NewStyle().Foreground(lipgloss.Color(colOverlay0)).
		Render("    "+m.subtitle)
}

// subtitleRows returns the number of extra header rows the subtitle occupies: 1
// when a subtitle is present, 0 otherwise. Single source of truth for the layout
// delta the subtitle introduces (mirrors cachedRows()).
func (m *model) subtitleRows() int {
	if m.subtitle != "" {
		return 1
	}
	return 0
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
// anchors only to the reload glyph (handled in cachedBadge). Line is the pill's
// absolute screen row (bodyTop()-2 in the cached header layout). Col is 0 — the
// left cap, once buttonAt strips the 2-col left margin (the pill row's "  "
// indent IS that margin). Width is the pill's visible width minus the trailing
// space. Screen=true so buttonAt resolves it by absolute Y, not content line.
func (m *model) appendCachedButton() {
	// Only add the clickable regenerate button when regeneration is actually possible
	// (an orchestrator re-engagement OR the cached-answer seam). With neither wired the
	// reload glyph isn't rendered (see cachedBadge), so a click target would be dead.
	if !m.canRegenerate() {
		return
	}
	pillRow := m.bodyTop() - 2
	pillW := lipgloss.Width(m.cachedBadge()) - 1 // drop the trailing space
	if pillW < 1 {
		pillW = 1
	}
	m.buttons = append(m.buttons, Button{
		Line:    pillRow,
		Col:     0,
		Width:   pillW,
		Kind:    "regenerate",
		BlockID: "cached",
		Screen:  true,
	})
}

// reloadIconScreenCol returns the absolute screen column of the reload glyph in
// the cached pill: 2-col indent + left cap (1) + the pill prefix width (db icon
// + " cached · <age> "). Used to anchor the regenerate hint label above the glyph.
func (m model) reloadIconScreenCol() int {
	prefix := "\U0000F1C0 cached · " + relativeAge(m.cachedAt) + " "
	return 2 + 1 + lipgloss.Width(prefix)
}

// regenLabel returns the hint label assigned to the regenerate (cached pill)
// button in the current hint session, or "" if none is assigned.
func (m model) regenLabel() string {
	for lbl, b := range m.hintLabels {
		if b.Kind == "regenerate" {
			return lbl
		}
	}
	return ""
}

// editBadge powerline-pill styles: hoisted to package-level so they are allocated
// once rather than on every render frame.
var (
	editBadgeCapStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(colGreen))
	editBadgeBodyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(colBase)).Background(lipgloss.Color(colGreen))
)

// editBadge returns the styled powerline pill string for the file-backed [edit]
// affordance, followed by exactly 1 trailing space. The pill mirrors cachedBadge:
// capL (U+E0B6) + body (bg=colGreen, fg=colBase: " edit ") + capR (U+E0B4) + " ".
// Returns "" when sourcePath is empty (ephemeral playbook).
func (m model) editBadge() string {
	if m.sourcePath == "" {
		return ""
	}
	capL := editBadgeCapStyle.Render("\U0000E0B6")
	body := editBadgeBodyStyle.Render(" edit ")
	capR := editBadgeCapStyle.Render("\U0000E0B4")
	return capL + body + capR + " "
}

// appendEditButton adds the screen-fixed [edit] button to m.buttons when
// sourcePath is non-empty (file-backed playbook). The badge is right-aligned on
// the title row (screen row 1 — bodyTop()-2 in the non-cached, non-subtitle
// layout; always 1 since the title is the second row after the leading blank).
// Col is computed from the right edge so buttonAt (which strips the 2-col margin)
// hits the badge area. Screen=true so buttonAt resolves it by absolute Y.
func (m *model) appendEditButton() {
	if m.sourcePath == "" {
		return
	}
	badge := m.editBadge()
	badgeW := lipgloss.Width(badge) - 1 // drop the trailing space for the hit target
	if badgeW < 1 {
		badgeW = 1
	}
	// Title is always screen row 1 (row 0 is the leading blank line).
	// NOTE: titleRow=1 assumes the non-cached ShowMain render path — the only
	// path that sets sourcePath; cached/ephemeral playbooks never get this button.
	titleRow := 1
	// Badge starts at screen col (m.width - lipgloss.Width(badge)); subtract 2 for
	// the left margin that buttonAt strips via col := x-2.
	editCol := m.width - lipgloss.Width(badge) - 2
	if editCol < 0 {
		editCol = 0
	}
	m.buttons = append(m.buttons, Button{
		Line:    titleRow,
		Col:     editCol,
		Width:   badgeW,
		Kind:    "edit",
		BlockID: "edit",
		Screen:  true,
	})
}

// titleLine builds the full header line string for the given available width.
// When the playbook is file-backed (sourcePath non-empty), the [edit] pill is
// right-aligned on the title row with exactly 1 trailing space. The pill is
// omitted when w < 1 (zero-width pane) rather than overflowing.
// When the title is too long to share the row with the badge, the title is
// truncated (with an ellipsis) so title+badge always fits within w and the
// badge stays at the right edge — keeping its hit-box aligned with editCol.
func (m model) titleLine(w int) string {
	title := "  " + m.header()
	badge := m.editBadge()
	if badge == "" || w < 1 {
		return title
	}
	titleW := lipgloss.Width(title)
	badgeW := lipgloss.Width(badge)
	if titleW+badgeW > w {
		// Truncate title so title+badge fits within w; reserve room for the badge.
		maxTitleW := w - badgeW
		if maxTitleW < 1 {
			maxTitleW = 1
		}
		title = runewidth.Truncate(title, maxTitleW, "…")
		titleW = runewidth.StringWidth(title)
	}
	gap := w - titleW - badgeW
	if gap < 0 {
		gap = 0
	}
	return title + strings.Repeat(" ", gap) + badge
}

// cachedBadgeRow returns the header row shown directly BELOW the title (reusing
// the top-pad row): the left-aligned powerline pill on a cached replay, else ""
// (the normal blank top-pad).
func (m model) cachedBadgeRow() string {
	if !m.isCached {
		return ""
	}
	return "  " + m.cachedBadge()
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
		// above is scrolled off the top). Screen-fixed buttons (e.g. the cached
		// pill reload icon) are skipped here — they live in the header, not the
		// scrollable body; their label is floated on the blank line above the pill
		// in the cached-header block below (anchored to the reload-icon column).
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

		// Button glyph columns per tab line — given the hint-label dark-red bg.
		// Only body buttons (not Screen-fixed) are tracked for code-row highlighting.
		buttonColsByRow := map[int]map[int]bool{}
		for _, b := range m.buttons {
			if b.Screen {
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
		sb.WriteString(m.titleLine(m.width) + "\n")
		if m.subtitle != "" {
			sb.WriteString(m.subtitleRowString() + "\n") // description caption under the title
		}
		if m.isCached {
			// Float the regenerate button's hint label on the blank line above the
			// pill, anchored to the reload-icon column (the flash anchor) — mirroring
			// how body buttons float their label on the line above the glyph.
			above := padTo("", m.width)
			if lbl := m.regenLabel(); lbl != "" {
				above = spliceOver(above, lab.Render(lbl), m.reloadIconScreenCol())
			}
			sb.WriteString(above + "\n")              // blank above pill (+ hint label)
			sb.WriteString(m.cachedBadgeRow() + "\n") // cached pill (left-aligned)
			sb.WriteString("\n")                      // blank below pill
		} else {
			sb.WriteString("\n") // top-pad (single blank)
		}
		for i, row := range rows {
			idx := m.yOff + i
			row = m.spinRow(idx, row) // advance any run spinner at View time (B1c)
			var base string
			if idx >= 0 && idx < len(m.lines) && m.lines[idx].Code {
				base = hintCodeRow(row, cw, buttonColsByRow[idx]) // fill + dark-red button cells
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
	out = append(out, pad(""))                   // leading blank
	out = append(out, pad(m.titleLine(m.width))) // title
	if m.subtitle != "" {
		out = append(out, pad(m.subtitleRowString())) // description caption under the title
	}
	if m.isCached {
		out = append(out, pad(""))                 // blank above pill
		out = append(out, pad(m.cachedBadgeRow())) // cached pill (left-aligned)
		out = append(out, pad(""))                 // blank below pill
	} else {
		out = append(out, pad("")) // top-pad (single blank)
	}
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
	sb.WriteString(m.titleLine(m.width) + "\n")
	if m.subtitle != "" {
		sb.WriteString(m.subtitleRowString() + "\n") // description caption under the title
	}
	if m.isCached {
		sb.WriteString("\n")                      // blank above pill
		sb.WriteString(m.cachedBadgeRow() + "\n") // cached pill (left-aligned)
		sb.WriteString("\n")                      // blank below pill
	} else {
		sb.WriteString("\n") // top-pad (single blank)
	}
	for _, l := range lines {
		sb.WriteString("  " + l.Text + "\n")
	}
	return sb.String()
}
