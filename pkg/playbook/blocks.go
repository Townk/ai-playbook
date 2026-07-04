// Package playbook is the public, harness-agnostic owner of the playbook
// schema. It parses a Markdown playbook body into the canonical block model
// (the {id=…}/{rollback=…}/{static}/file=/needs=/from= grammar) and
// normalizes fences, and is the single source of truth for that grammar. It
// imports nothing beyond goldmark and the standard library, so any
// front-end, validator, or embedded runner can consume the schema without
// pulling in AI, rendering, or terminal concerns.
//
// Public API; pre-1.0, minor versions may still reshape it — see ADR-0009.
package playbook

import (
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Block is the canonical parsed representation of one playbook code block — the
// schema owner's view of a `{id=…}`/`{rollback=…}`/`{static}`/`file=`/`needs=`/
// `from=` fenced block (see docs/specifications/playbook-schema.md). The
// renderer, validate, launcher, and autorun all derive their per-block state
// from this.
type Block struct {
	ID       string
	Type     string // "shell" | "run" | "diff" | "static" | "create"
	Lang     string
	Needs    []string
	Static   bool
	File     string
	Rollback string // id of the block that undoes this one (rollback=<id>); "" if none
	From     string // id of the block whose retained stdout feeds this one's stdin (from=<id>, ADR-0010); "" if none
	Payload  string
}

// EffectiveNeeds returns the block's combined data+order dependency set: Needs
// plus From (when From is non-empty), deduped so an id already present in
// Needs is never listed twice. from= implies needs= (ADR-0010) for gating,
// --auto ordering, and dependent invalidation — callers that need the full
// dependency set use this instead of Needs directly.
func (b Block) EffectiveNeeds() []string {
	if b.From == "" {
		return b.Needs
	}
	for _, n := range b.Needs {
		if n == b.From {
			return b.Needs
		}
	}
	out := make([]string, 0, len(b.Needs)+1)
	out = append(out, b.Needs...)
	out = append(out, b.From)
	return out
}

// blockMD is the shared markdown parser used to enumerate blocks. It mirrors the
// renderer's goldmark configuration (the GFM extension bundle) so the parser and
// the renderer parse identically; it is stateless and reused across calls.
var blockMD = goldmark.New(goldmark.WithExtensions(extension.GFM))

// ParseBlocks is the SINGLE canonical playbook block parser (ADR-0009 step 1). It
// normalizes fences (identically to the renderer, via NormalizeFences), parses the
// document with the shared goldmark instance, and returns one Block per TOP-LEVEL
// fenced/indented code block in document order. Code nested inside a list or
// blockquote is prose-only — the renderer does not enumerate it as a block, so
// ParseBlocks must not either. It is a pure function: no styling, width, or
// presentation state.
func ParseBlocks(md string) []Block {
	src := []byte(NormalizeFences(md))
	doc := blockMD.Parser().Parse(text.NewReader(src))
	var blocks []Block
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		switch c.(type) {
		case *ast.FencedCodeBlock, *ast.CodeBlock:
			// The synthesized fallback id is the block's 1-based ordinal among ALL
			// code blocks seen so far, so the same document always yields the same ids.
			blocks = append(blocks, buildBlock(c, src, len(blocks)+1))
		}
	}
	return assignIDs(blocks)
}

// buildBlock extracts one Block from a fenced/indented code node. ordinal is the
// block's 1-based position among all code blocks, used to synthesize a stable
// "auto-<n>" id for a block with no explicit {id=…} tag.
func buildBlock(n ast.Node, src []byte, ordinal int) Block {
	var raw strings.Builder
	lines := n.Lines()
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		raw.Write(seg.Value(src))
	}
	payload := strings.TrimRight(raw.String(), "\n")

	info := ""
	if fc, ok := n.(*ast.FencedCodeBlock); ok && fc.Info != nil {
		info = string(fc.Info.Segment.Value(src))
	}
	lang, attrs, flags := ParseFenceInfo(info)
	blk := Block{
		ID:       attrs["id"],
		Lang:     lang,
		Needs:    splitNeeds(attrs["needs"]),
		Rollback: attrs["rollback"],
		From:     attrs["from"],
		Static:   flags["static"],
		Payload:  payload,
	}
	blk.Type = ClassifyType(lang, blk.Static)
	if f := attrs["file"]; f != "" {
		blk.File = f
		blk.Type = "create"
	}
	if blk.ID == "" {
		blk.ID = fmt.Sprintf("auto-%d", ordinal)
	}
	return blk
}

// ParseFenceInfo splits a fence info string "<lang> {k=v flag …}" into the lang
// word, key=value attrs, and bare flags. Braces are optional.
func ParseFenceInfo(info string) (string, map[string]string, map[string]bool) {
	attrs := map[string]string{}
	flags := map[string]bool{}
	info = strings.TrimSpace(info)
	lang := info
	rest := ""
	if sp := strings.IndexByte(info, ' '); sp >= 0 {
		lang, rest = info[:sp], info[sp+1:]
	}
	rest = strings.TrimSpace(rest)
	rest = strings.TrimPrefix(rest, "{")
	rest = strings.TrimSuffix(rest, "}")
	for _, tok := range strings.Fields(rest) {
		if eq := strings.IndexByte(tok, '='); eq >= 0 {
			attrs[tok[:eq]] = tok[eq+1:]
		} else {
			flags[tok] = true
		}
	}
	return lang, attrs, flags
}

func nonExecLang(lang string) bool {
	switch lang {
	case "", "text", "console", "output", "log", "json":
		return true
	}
	return false
}

// ClassifyType maps a fenced block's language (and its {static} flag) to the
// runner's block type. It is THE schema type rule — every layer that needs a
// block's type (the parser here, and any validator classifying
// not-yet-rendered block data, e.g. the AI-authoring DTO in internal/draft)
// must derive it from this one function so they can never disagree. Note the
// non-executable languages ("", text, console, output, log, json) classify
// as "static" even without the {static} flag. A create block (file= tag) is
// classified by the caller: file= always wins over this result (see
// buildBlock).
func ClassifyType(lang string, static bool) string {
	if static || nonExecLang(lang) {
		return "static"
	}
	switch lang {
	case "bash", "sh", "zsh":
		return "shell"
	case "diff", "patch":
		return "diff"
	default:
		return "run" // python, node, ruby, …
	}
}

// assignIDs fills the id of every block that still lacks one with a contiguous
// "b<n>" auto-id, skipping values already claimed by an explicit id. ParseBlocks
// synthesizes an "auto-<n>" id for every untagged block first, so in that path
// this is a defensive no-op; it stays a standalone helper (with direct unit
// coverage) as the schema's id-assignment rule.
func assignIDs(blocks []Block) []Block {
	used := map[string]bool{}
	for _, b := range blocks {
		if b.ID != "" {
			used[b.ID] = true
		}
	}
	n := 0
	for i := range blocks {
		if blocks[i].ID == "" {
			n++
			for used[fmt.Sprintf("b%d", n)] {
				n++
			}
			blocks[i].ID = fmt.Sprintf("b%d", n)
			used[blocks[i].ID] = true
		}
	}
	return blocks
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

// NormalizeFences repairs a malformed CLOSING code fence that the model emitted
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
//
// It is the SINGLE fence-normalization pass: both ParseBlocks and the renderer run
// input through it before parsing so they can never disagree on block boundaries.
func NormalizeFences(md string) string {
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
