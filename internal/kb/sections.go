package kb

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Kind classifies a remembered fact and routes it to a file + section. The four
// kinds map to the two knowledge sets: system/user are GLOBAL (the machine and
// the user, shared across projects); environment/topic are PER-PROJECT (this
// project's setup and its problem-domain lessons). K2's `remember` tool exposes
// these values verbatim as its `kind` input.
type Kind string

const (
	KindSystem      Kind = "system"      // global · ## System
	KindUser        Kind = "user"        // global · ## User
	KindEnvironment Kind = "environment" // project · ## Environment
	KindTopic       Kind = "topic"       // project · ## Topics / ### <topic>
)

// Section headings are contract (see docs/specifications/knowledge-base.md and
// ADR-0011): the storage format, recall fold, and compaction all key on the
// exact heading text.
const (
	secSystem      = "## System"
	secUser        = "## User"
	secEnvironment = "## Environment"
	secTopics      = "## Topics"

	// The per-project meta comment, added on the first sectioned write (and
	// re-added if hand-deleted — write-if-absent) so `kb list`/`search` can show
	// a real project name instead of the sha1 key.
	metaPrefix = "<!-- meta: project-root: "
	metaSuffix = " -->"
)

// Append routes a distilled fact to the correct knowledge file and section:
//
//   - system/user   → the GLOBAL file (root/knowledge.md), ## System / ## User.
//     projectRoot is ignored for pathing and MAY be empty.
//   - environment   → the PROJECT file, ## Environment.
//   - topic         → the PROJECT file, ## Topics under ### <topic> (created as
//     needed; topic matching is case-insensitive, stored in the submitted casing).
//
// Contract violations return an error (so K2 maps them to tool errors): an
// unknown/empty kind, a non-empty topic with a non-topic kind, a missing topic
// with kind=topic, or an empty projectRoot with a project kind.
//
// Write-dedup: a bullet whose normalized text (case-insensitive,
// whitespace-collapsed) already exists in the target section/subsection is
// skipped silently and the file is left untouched (idempotent — returns nil).
//
// The first sectioned write to a legacy (unsectioned) project file rewrites it
// into sectioned form, preserving every legacy bullet under ## Environment and
// adding the meta line.
func Append(root, projectRoot string, kind Kind, topic, fact string) error {
	global, heading, err := route(kind, topic, projectRoot)
	if err != nil {
		return err
	}

	// Normalize the stored bullet: flatten newlines to spaces and trim, matching
	// the legacy writer. Internal spacing/casing is preserved verbatim; only the
	// dedup COMPARISON normalizes further.
	stored := strings.TrimSpace(strings.ReplaceAll(fact, "\n", " "))
	if stored == "" {
		return nil // empty fact is a silent no-op (no file created/rewritten)
	}

	path := Path(root, projectRoot)
	if global {
		path = GlobalPath(root)
	}
	raw, _ := os.ReadFile(path) // missing/unreadable → empty doc
	content := string(raw)
	d := parseDoc(content)

	// Lazy migration: the first sectioned write to a legacy project file folds
	// its flat content under ## Environment before the new write is applied.
	// Non-bullet legacy content is preserved verbatim in order, but blank-line
	// structure is not (parseDoc drops blanks; render re-emits canonical spacing).
	if !global && len(d.sections) == 0 && strings.TrimSpace(content) != "" {
		d.sections = []*docSection{{heading: secEnvironment, lines: d.preamble}}
		d.preamble = nil
	}

	// Meta line: write-if-absent, not literally write-once — a subsequent write
	// re-adds it when the user has hand-deleted it (self-healing), and an
	// existing meta line is never rewritten. Never on the global file.
	if !global && d.meta == "" {
		d.meta = metaPrefix + projectRoot + metaSuffix
	}

	sec := d.ensureSection(heading)
	target := &sec.lines
	if kind == KindTopic {
		target = &sec.ensureSub(topic).lines
	}

	// Write-dedup, scoped to the target section/subsection.
	norm := normalizeFact(stored)
	for _, ln := range *target {
		if b, ok := bulletText(ln); ok && normalizeFact(b) == norm {
			return nil
		}
	}
	*target = append(*target, "- "+stored)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(d.render()), 0o644)
}

// route validates the kind/topic/projectRoot contract and returns whether the
// write targets the global file plus the destination section heading.
func route(kind Kind, topic, projectRoot string) (global bool, heading string, err error) {
	switch kind {
	case KindSystem:
		global, heading = true, secSystem
	case KindUser:
		global, heading = true, secUser
	case KindEnvironment:
		global, heading = false, secEnvironment
	case KindTopic:
		global, heading = false, secTopics
	default:
		return false, "", fmt.Errorf("kb: unknown kind %q", kind)
	}
	if kind == KindTopic {
		if strings.TrimSpace(topic) == "" {
			return false, "", fmt.Errorf("kb: kind=topic requires a topic")
		}
	} else if topic != "" {
		return false, "", fmt.Errorf("kb: topic is only valid with kind=topic")
	}
	if !global && projectRoot == "" {
		return false, "", fmt.Errorf("kb: kind=%s requires a project root", kind)
	}
	return global, heading, nil
}

// ProjectName parses the `<!-- meta: project-root: <path> -->` line from a KB
// file's content, returning the recorded project root. ok is false when the file
// has no meta line (legacy files, or the global file). K5's list/search consume
// this to show real names with the sha1 key as a fallback.
func ProjectName(fileContent string) (root string, ok bool) {
	for _, ln := range strings.Split(fileContent, "\n") {
		t := strings.TrimSpace(ln)
		if !isMeta(t) {
			continue
		}
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(t, metaPrefix), metaSuffix))
		if inner != "" {
			return inner, true
		}
	}
	return "", false
}

// ── document model (used only for sectioned writes) ───────────────────────────

// doc is a parsed KB file: an optional meta line, any content before the first
// section (preamble — legacy bullets awaiting migration), and ordered sections.
type doc struct {
	meta     string
	preamble []string
	sections []*docSection
}

// docSection is a `## ` heading with its bullets/prose and any `### ` subsections
// (Topics uses subsections; the other sections do not).
type docSection struct {
	heading string
	lines   []string
	subs    []*docSub
}

// docSub is a `### <topic>` subsection with its bullets.
type docSub struct {
	heading string
	lines   []string
}

// parseDoc reads a KB file into the doc model. Blank lines are dropped (canonical
// spacing is re-emitted by render); the meta line is captured; `## ` starts a
// section, `### ` a subsection; every other non-blank line is preserved verbatim
// under the current section/subsection (or the preamble before any section).
func parseDoc(content string) *doc {
	d := &doc{}
	var cur *docSection
	var sub *docSub
	for _, ln := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(ln)
		switch {
		case trimmed == "":
			// dropped; render re-inserts canonical blank lines
		case isMeta(trimmed):
			if d.meta == "" {
				d.meta = trimmed
			}
		case strings.HasPrefix(ln, "## "):
			cur = &docSection{heading: strings.TrimRight(ln, " ")}
			d.sections = append(d.sections, cur)
			sub = nil
		case strings.HasPrefix(ln, "### ") && cur != nil:
			sub = &docSub{heading: strings.TrimRight(ln, " ")}
			cur.subs = append(cur.subs, sub)
		case cur == nil:
			d.preamble = append(d.preamble, ln)
		case sub != nil:
			sub.lines = append(sub.lines, ln)
		default:
			cur.lines = append(cur.lines, ln)
		}
	}
	return d
}

// render serializes the doc back to canonical markdown: the meta line, the
// preamble, then each section (heading + lines + subsections), all separated by
// a single blank line, terminated by a trailing newline.
func (d *doc) render() string {
	var blocks []string
	if d.meta != "" {
		blocks = append(blocks, d.meta)
	}
	if len(d.preamble) > 0 {
		blocks = append(blocks, strings.Join(d.preamble, "\n"))
	}
	for _, s := range d.sections {
		lines := append([]string{s.heading}, s.lines...)
		for _, sub := range s.subs {
			lines = append(lines, sub.heading)
			lines = append(lines, sub.lines...)
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	if len(blocks) == 0 {
		return ""
	}
	return strings.Join(blocks, "\n\n") + "\n"
}

// ensureSection returns the section with the given heading, appending it if
// absent (preserving existing order).
func (d *doc) ensureSection(heading string) *docSection {
	for _, s := range d.sections {
		if s.heading == heading {
			return s
		}
	}
	s := &docSection{heading: heading}
	d.sections = append(d.sections, s)
	return s
}

// ensureSub returns the subsection for topic, matched case-insensitively; a new
// subsection stores the submitted casing.
func (s *docSection) ensureSub(topic string) *docSub {
	for _, sub := range s.subs {
		if strings.EqualFold(strings.TrimPrefix(sub.heading, "### "), topic) {
			return sub
		}
	}
	sub := &docSub{heading: "### " + topic}
	s.subs = append(s.subs, sub)
	return sub
}

// isMeta reports whether a (trimmed) line is the project-root meta comment.
func isMeta(line string) bool {
	return strings.HasPrefix(line, metaPrefix) && strings.HasSuffix(line, metaSuffix)
}

// bulletText extracts the text of a `- ` bullet line, false for non-bullets.
func bulletText(line string) (string, bool) {
	t := strings.TrimSpace(line)
	if strings.HasPrefix(t, "- ") {
		return strings.TrimSpace(t[len("- "):]), true
	}
	return "", false
}

// normalizeFact lowercases and collapses whitespace runs for dedup comparison.
func normalizeFact(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
