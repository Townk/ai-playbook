package author

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/kb"
)

// compactProcess overrides process construction for CompactKB — the test seam
// mirroring ReviewStream's reviewProcess (CompactKB's exported signature carries no
// AuthorOptions, so there is no per-call Command to inject; tests swap this package
// var). nil in production → RunHarnessEvents' default (exec.CommandContext).
var compactProcess func(ctx context.Context, bin string, args []string) *exec.Cmd

// compactPreReplace, when non-nil, runs after CompactKB returns and BEFORE
// compactFile's pre-replace re-read. It is a test-only seam (nil in production)
// that lets the cross-session race test mutate the file inside the compaction
// window — the least invasive way to simulate a concurrent `remember` landing
// between the initial read and the replace.
var compactPreReplace func(path string)

// CompactPrompt is the over-budget compaction instruction (ADR-0011 / spec "Fill
// and compact"): the model receives a plain-markdown knowledge file and rewrites it
// SMALLER — merging near-duplicates, generalizing overlaps, dropping stale topic
// entries — while PRESERVING the section structure and the meta comment. It returns
// ONLY the rewritten file, no prose.
func CompactPrompt(content string) string {
	var b strings.Builder
	b.WriteString("You are compacting a plain-markdown knowledge file of distilled facts.\n")
	b.WriteString("Rewrite it SMALLER while preserving its meaning:\n")
	b.WriteString("- Merge near-duplicate facts into one.\n")
	b.WriteString("- Generalize overlapping facts into a single broader fact.\n")
	b.WriteString("- Drop stale or superseded topic entries.\n\n")
	b.WriteString("PRESERVE the structure EXACTLY: keep every `## ` section heading that is\n")
	b.WriteString("present (## System / ## User / ## Environment / ## Topics), keep the\n")
	b.WriteString("`### <topic>` subsection headings, and keep the `<!-- meta: ... -->` comment\n")
	b.WriteString("line verbatim if present. Facts are `- ` bullets. Never invent new facts, and\n")
	b.WriteString("never add secrets or raw environment dumps.\n\n")
	b.WriteString("Return ONLY the rewritten file content — no prose, no explanation, and no code\n")
	b.WriteString("fence around it.\n\n")
	b.WriteString("## File to compact\n")
	b.WriteString(content)
	b.WriteString("\n")
	return b.String()
}

// compactTrigger is the short user message; the load-bearing content is the file in
// the system prompt (CompactPrompt).
const compactTrigger = "Rewrite the file above, smaller. Return only the rewritten file content."

// CompactKB runs the over-budget knowledge compaction over the OWNED harness as a
// QUICK STRUCTURED CALL — the same invocation class as classify/metadata: a bounded
// timeout (defaultCallTimeout), NoThinking, a BARE invocation (isolated from
// CLAUDE.md/auto-memory/dynamic sections), and NO MCP tools backend (no
// --mcp-config). It returns the model's rewritten file content (a stray wrapping
// code fence is stripped). The caller applies the rejection guards before replacing
// anything — CompactKB itself neither reads nor writes files.
func CompactKB(cfg *config.Config, content string) (string, error) {
	opts := AuthorOptions{
		Cfg:           cfg,
		MCPConfigPath: "", // a compaction call needs no tools backend
		Bare:          true,
		NoThinking:    true,
		Timeout:       defaultCallTimeout,
		Command:       compactProcess,
	}
	out, err := runMetadataOnce(CompactPrompt(content), compactTrigger, opts)
	if err != nil {
		return "", err
	}
	return stripFence(out), nil
}

// CompactOversized is the wrap-up completion hook: for EACH of the two knowledge
// files (global, then project) whose on-disk size exceeds budget, it runs ONE
// compaction pass (read → CompactKB → guards → .bak → replace). A file at or under
// budget triggers no call and no cost. Every failure mode (missing file, harness
// error, a rejected result) leaves the file untouched with a stderr note — the model
// can never destroy knowledge through a bad compaction, and the wrap-up is
// unaffected. An empty projectRoot compacts only the global file.
func CompactOversized(cfg *config.Config, root, projectRoot string, budget int) {
	compactFile(cfg, kb.GlobalPath(root), budget)
	if projectRoot != "" {
		compactFile(cfg, kb.Path(root, projectRoot), budget)
	}
}

// compactFile runs one over-budget compaction pass on a single KB file. It is a
// no-op when the file is missing/unreadable or at/under budget. On a harness error,
// a guard rejection, or a cross-session change detected in the compaction window it
// writes a stderr note and leaves the file (and any prior .bak) untouched. On
// success it writes the ORIGINAL content to <path>.bak (one level, overwritten each
// compaction) BEFORE replacing the file with the result.
func compactFile(cfg *config.Config, path string, budget int) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return // missing/unreadable → nothing to compact
	}
	input := string(raw)
	if len(input) <= budget {
		return // under budget → no call, no cost
	}

	out, err := CompactKB(cfg, input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook: knowledge compaction failed for %s (%v); file left unchanged\n", path, err)
		return
	}
	result := normalizeCompacted(out)

	if reason, reject := rejectCompaction(input, result); reject {
		fmt.Fprintf(os.Stderr, "ai-playbook: knowledge compaction rejected for %s (%s); file left unchanged\n", path, reason)
		return
	}

	if compactPreReplace != nil { // test-only seam; nil in production
		compactPreReplace(path)
	}

	// Cross-session race guard: the result was derived from `raw`, read up to a
	// full call-timeout earlier — a concurrent session's `remember` (especially on
	// the shared GLOBAL file) may have landed in that window, and replacing now
	// would clobber its fact into neither the result nor the .bak. Re-read
	// immediately before the replace and ABORT if the file changed (no replace,
	// no .bak); the next over-budget wrap-up simply compacts the fresh content.
	cur, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook: re-read failed for %s during compaction (%v); skipped (file left unchanged)\n", path, err)
		return
	}
	if !bytes.Equal(cur, raw) {
		fmt.Fprintf(os.Stderr, "ai-playbook: kb changed during compaction — skipped %s (file left unchanged)\n", path)
		return
	}

	// .bak BEFORE replace: preserve the prior content so a bad compaction is always
	// recoverable (one level, overwritten each compaction).
	if err := os.WriteFile(path+".bak", raw, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook: knowledge compaction backup failed for %s (%v); file left unchanged\n", path, err)
		return
	}
	if err := os.WriteFile(path, []byte(result), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook: knowledge compaction write failed for %s (%v)\n", path, err)
	}
}

// rejectCompaction applies the four CONTENT guards (spec "Fill and compact"): reject
// an empty result, a result that is NOT smaller than the input (>= — an identical
// rewrite is pointless to persist), a result that drops any `## ` section heading
// the input had, or a result that drops the input's `<!-- meta: project-root -->`
// line (kb list/search resolve project names through it). A rejected result leaves
// the file untouched (and no .bak is written). reason is a short note for stderr.
// These are the four guards over the compaction RESULT; a fifth, distinct check —
// the pre-replace cross-session race abort — lives in compactFile (it re-reads the
// file immediately before replacing and aborts if it changed under us).
func rejectCompaction(input, result string) (reason string, reject bool) {
	if strings.TrimSpace(result) == "" {
		return "empty result", true
	}
	if len(result) >= len(input) {
		return "result not smaller than input", true
	}
	for _, h := range topLevelHeadings(input) {
		if !hasHeadingLine(result, h) {
			return "missing required section " + h, true
		}
	}
	if _, ok := kb.ProjectName(input); ok {
		if _, ok := kb.ProjectName(result); !ok {
			return "missing meta line", true
		}
	}
	return "", false
}

// topLevelHeadings returns the `## ` section headings present in s (trailing spaces
// trimmed), excluding `### ` subsection headings.
func topLevelHeadings(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimRight(ln, " ")
		if strings.HasPrefix(t, "## ") { // "### " does not match "## " (third char is '#')
			out = append(out, t)
		}
	}
	return out
}

// hasHeadingLine reports whether s contains h as a standalone (trailing-space-
// trimmed) line.
func hasHeadingLine(s, h string) bool {
	for _, ln := range strings.Split(s, "\n") {
		if strings.TrimRight(ln, " ") == h {
			return true
		}
	}
	return false
}

// normalizeCompacted strips a stray wrapping code fence and surrounding whitespace
// from the model's output, re-terminating a non-empty result with a single trailing
// newline (matching the KB writer's canonical spacing). An all-blank result → "".
func normalizeCompacted(out string) string {
	s := strings.TrimSpace(stripFence(out))
	if s == "" {
		return ""
	}
	return s + "\n"
}

// stripFence removes a single wrapping ```...``` pair if the content is fenced (the
// compaction prompt forbids fences, but models sometimes add them). Content without
// a leading fence is returned unchanged.
func stripFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return s
	}
	lines := strings.Split(t, "\n")
	if len(lines) < 2 {
		return s
	}
	lines = lines[1:] // drop the opening ```[lang] line
	if strings.TrimSpace(lines[len(lines)-1]) == "```" {
		lines = lines[:len(lines)-1] // drop the closing fence
	}
	return strings.Join(lines, "\n")
}
