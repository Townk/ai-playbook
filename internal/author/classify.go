package author

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
)

// Classification is the cheap-model triage decision for one request: a Kind plus
// its Content. Kind is one of:
//
//   - "command" — Content is the SINGLE shell command that fulfills the request,
//     ready for the user to review and run (it is never auto-run, never the failed
//     command verbatim).
//   - "answer"  — Content is a SHORT prose answer (a few lines, plain text).
//   - "escalate" — Content is empty; the request needs the full troubleshooting/
//     how-to PLAYBOOK (the capable authoring path).
type Classification struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
	// Title is a short (≤30-rune) human label for the request, used as the result
	// pane's header (the answer pager / escalate session). Title Case, no trailing
	// punctuation. Empty when the model omits it (the pane keeps its default header).
	Title string `json:"title"`
}

// maxTitleRunes bounds the pane-header title the classify returns (rune-safe).
const maxTitleRunes = 30

// Triage kinds.
const (
	KindCommand  = "command"
	KindAnswer   = "answer"
	KindEscalate = "escalate"
)

// ClassifyPrompt builds the JSON-only classification instruction: it tells the
// cheap model to classify req against the captured context (cwd / project / last
// command + exit / scrollback) and return ONLY a single JSON object
// {"kind": ..., "content": ...} — no prose, no markdown fence. The three kinds and
// their content rules (including the "never re-type the failed command" rule) are
// spelled out so the model can decide command vs. answer vs. escalate.
func ClassifyPrompt(req capture.Request) string {
	var b strings.Builder
	b.WriteString("You are a terminal assistant TRIAGE step. Classify the user's request\n")
	b.WriteString("against the captured context below and respond with ONLY a single JSON\n")
	b.WriteString("object matching this exact schema:\n\n")
	b.WriteString("{\n")
	b.WriteString(`  "kind": "command" | "answer" | "escalate",` + "\n")
	b.WriteString(`  "content": "...",` + "\n")
	b.WriteString(`  "title": "..."` + "\n")
	b.WriteString("}\n\n")
	b.WriteString("Decide the kind:\n")
	b.WriteString("- \"command\": the request is satisfied by ONE shell command. content is that\n")
	b.WriteString("  SINGLE command, ready to run as-is.\n")
	if isFailure(req) {
		// Only meaningful when the last command actually FAILED: re-running the
		// failure is not a fix. For a successful last command (a plain question)
		// the same command is a perfectly good suggestion, so omit this clause.
		b.WriteString("  NEVER return the FAILED command verbatim — re-running a failure is not a\n")
		b.WriteString("  fix; if the only command you'd give equals the failed command below,\n")
		b.WriteString("  classify as \"escalate\" instead.\n")
	}
	b.WriteString("- \"answer\": the request is satisfied by a SHORT prose answer. content is that\n")
	b.WriteString("  answer — a few lines of plain text, no code fences.\n")
	b.WriteString("- \"escalate\": the request needs a full troubleshooting/how-to PLAYBOOK\n")
	b.WriteString("  (multi-step, diagnosis, or anything beyond one command or a short answer).\n")
	b.WriteString("  content MUST be empty (\"\").\n\n")
	b.WriteString("Always set \"title\": a SHORT human label for the request, at most 30\n")
	b.WriteString("characters, in Title Case, with no trailing punctuation (e.g. \"List Recent\n")
	b.WriteString("Commits\", \"Merge Vs Rebase\", \"Fix Gradle Build\"). It is shown as the\n")
	b.WriteString("result pane's header, so keep it terse.\n\n")
	b.WriteString("Respond with the JSON object ONLY: no prose, no explanation, no markdown code\n")
	b.WriteString("fence — just the raw JSON.\n\n")
	b.WriteString("## Context\n")
	b.WriteString(classifyContext(req))
	b.WriteString("\n## User request\n")
	if strings.TrimSpace(req.UserRequest) != "" {
		b.WriteString(req.UserRequest)
	} else {
		b.WriteString("(no description given)")
	}
	b.WriteString("\n")
	return b.String()
}

// classifyContext renders the bounded origin context the model classifies against:
// project + cwd, the last command and its exit, and (for a failure) the sliced
// scrollback. It mirrors the fields BuildUserMessage folds in, kept compact for a
// cheap one-shot call.
func classifyContext(req capture.Request) string {
	var b strings.Builder
	projectName := req.Project.Name
	if projectName == "" {
		projectName = req.ProjectRoot
	}
	if projectName == "" {
		projectName = "unknown"
	}
	projectRoot := req.ProjectRoot
	if projectRoot == "" {
		projectRoot = "?"
	}
	fmt.Fprintf(&b, "Project: %s (%s)", projectName, projectRoot)
	if req.Project.Branch != "" {
		fmt.Fprintf(&b, " on branch %s", req.Project.Branch)
	}
	b.WriteString("\n")
	if req.CWD != "" {
		fmt.Fprintf(&b, "Working directory: %s\n", req.CWD)
	}
	if req.Command != "" {
		exit := req.Exit
		if exit == "" {
			exit = "?"
		}
		fmt.Fprintf(&b, "Last command: `%s` (exit %s)\n", req.Command, exit)
	}
	if req.Exit != "" && req.Exit != "0" {
		scroll := req.Scrollback
		if scroll == "" {
			scroll = "(none captured)"
		}
		b.WriteString("Relevant terminal output (the failure):\n")
		b.WriteString(scroll)
		b.WriteString("\n")
	}
	return b.String()
}

// classifyTrigger is the short user message; the load-bearing instruction is the
// system prompt (ClassifyPrompt).
const classifyTrigger = "Classify the request above. Respond with the JSON object only."

// ClassifyRequest runs the cheap-model TRIAGE classify over the OWNED harness and
// returns a Classification (command/answer/escalate). It is a PURE text→JSON call
// like PlaybookMetadata: NO tools backend (no tool transport), and it runs on the
// TRIAGE model — opts.Cfg.Agent.TriageModel, falling back to the configured
// harness's own defaults row (HarnessDefaults) — via the
// AuthorOptions.ModelOverride seam, so the authoring path's model is untouched.
//
// Parsing mirrors PlaybookMetadata: drain the event stream for the Final result
// text, tolerate a fence/whitespace by extracting the outer {...}, json.Unmarshal,
// retry ONCE — except on a Timeout (context.DeadlineExceeded), where the harness
// stalled and a retry would just stall again (doubling the worst case to
// 2×Timeout). Robustness contract (the caller never blocks — classify ALWAYS
// routes somewhere):
//
//   - an unknown/empty Kind is normalized to "escalate";
//   - the failed-command GUARD: a "command" whose Content (whitespace-collapsed)
//     equals req.Command (whitespace-collapsed) is downgraded to "escalate" — never
//     re-type the failed command;
//   - a parse/classification failure after the retry returns
//     Classification{Kind:"escalate"} together with the error (the caller logs it
//     and escalates).
func ClassifyRequest(req capture.Request, opts AuthorOptions) (Classification, error) {
	// A classify call needs no tools backend; never attach --mcp-config (nor
	// redirect cursor's config root — clear both halves of the tool wiring).
	opts.ToolArgv = nil
	opts.ToolDir = ""
	// Bound the call (A5a): classify gates every request and is meant to finish
	// in ~2-3s, so a stalled harness must not hang the caller forever. A caller
	// that already set a Timeout (e.g. a test forcing a short deadline) keeps it.
	if opts.Timeout <= 0 {
		opts.Timeout = defaultCallTimeout
	}
	// Run on the triage model, not the authoring model.
	opts.ModelOverride = triageModel(opts.Cfg)
	// BARE quick-model call: REPLACE the system prompt (--system-prompt) + strict
	// MCP + drop the dynamic machine sections, so the triage runs without CLAUDE.md
	// auto-discovery, auto-memory, global MCP, or cwd/env/git-status sections.
	opts.Bare = true
	// Triage is a one-shot JSON decision — no reasoning needed. Disabling thinking
	// (MAX_THINKING_TOKENS=0) cuts the classify from ~7s to ~2.6s (haiku thinks by
	// default; the budget value doesn't matter — only on-vs-off does).
	opts.NoThinking = true

	sys := ClassifyPrompt(req)

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		out, err := runMetadataOnce(sys, classifyTrigger, opts)
		if err != nil {
			lastErr = err
			// A stalled harness that blew its Timeout will stall again — retrying
			// just doubles the worst case to 2×Timeout for nothing. The one-retry
			// stays reserved for transient parse/JSON failures below.
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			continue
		}
		cls, perr := parseClassification(out)
		if perr != nil {
			lastErr = perr
			continue
		}
		return normalizeClassification(cls, req), nil
	}
	return Classification{Kind: KindEscalate},
		fmt.Errorf("request classification failed after retry: %w", lastErr)
}

// triageModel resolves the model id for the classify pass: cfg [agent].TriageModel
// when explicitly set, else the configured harness's own defaults row (ADR-0012
// decision 4 — claude: "haiku"; each harness names its own cheap alias).
func triageModel(cfg *config.Config) string {
	if cfg != nil && cfg.Agent.TriageModel != "" {
		return cfg.Agent.TriageModel
	}
	return HarnessDefaults(resolveHarnessName(cfg)).TriageModel
}

// parseClassification extracts the outer {...} (tolerating a fence/whitespace) and
// unmarshals into a Classification, mirroring parseMetadata.
func parseClassification(out string) (Classification, error) {
	jsonStr := extractJSONObject(out)
	if jsonStr == "" {
		return Classification{}, fmt.Errorf("no JSON object found in harness output: %q", strings.TrimSpace(out))
	}
	var cls Classification
	if err := json.Unmarshal([]byte(jsonStr), &cls); err != nil {
		return Classification{}, fmt.Errorf("unmarshal classification JSON: %w (raw: %q)", err, jsonStr)
	}
	return cls, nil
}

// isFailure reports whether the captured last command actually FAILED (a non-empty,
// non-zero exit) — the only case where re-suggesting that command is unhelpful. For
// a plain question (exit 0) the last command is just context, not a failure to avoid.
func isFailure(req capture.Request) bool {
	return req.Exit != "" && req.Exit != "0"
}

// normalizeClassification applies the routing-safety rules: unknown/empty kind →
// escalate; the failed-command guard (a command equal to a FAILED req.Command →
// escalate).
func normalizeClassification(cls Classification, req capture.Request) Classification {
	// Trim + rune-safe truncate the pane-header title before routing, so every
	// outcome (incl. the escalate downgrades, which still surface a header) carries
	// a clean ≤30-rune label.
	cls.Title = truncateTitle(cls.Title)
	switch cls.Kind {
	case KindCommand:
		// Never re-type the FAILED command: a command equal to the failed command is
		// no fix → escalate. ONLY when the last command actually failed — for a
		// successful last command (a plain question) the same command is a valid
		// suggestion (e.g. "how do I list 3 commits?" after running git log).
		if isFailure(req) && collapseWS(cls.Content) == collapseWS(req.Command) {
			return Classification{Kind: KindEscalate, Title: cls.Title}
		}
		return cls
	case KindAnswer:
		return cls
	default:
		// Unknown/empty kind (incl. "escalate"): escalate with empty content (the
		// title is kept for the session pane header).
		return Classification{Kind: KindEscalate, Title: cls.Title}
	}
}

// truncateTitle trims surrounding whitespace and truncates to maxTitleRunes runes
// (rune-safe, so a multi-byte label is never split mid-rune), then trims again so a
// truncation landing on a space doesn't leave a trailing blank.
func truncateTitle(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > maxTitleRunes {
		r = r[:maxTitleRunes]
	}
	return strings.TrimSpace(string(r))
}

// collapseWS trims and collapses internal whitespace runs to a single space, so
// the failed-command guard compares commands up to insignificant spacing.
func collapseWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
