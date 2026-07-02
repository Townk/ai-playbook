// validatecmd.go — the `ai-playbook validate` subcommand entrypoint.
//
// `validate` accepts a single playbook source, expressed one of two ways:
//
//   - validate <slug>            a bare positional ⇒ a saved playbook, resolved
//     through the store
//   - validate --file <path>     a raw markdown file, validated as-is
//
// Exactly one source must be given; zero or more than one is a usage error.
//
// The check runs in two passes: a deterministic structural pass
// (internal/validate.Check — front matter, duplicate ids, needs/cycle,
// unbalanced fences, runnable/lang warnings) that drives the exit code, and an
// optional AI review pass (author.ReviewOnce, via the reviewFn seam) that
// surfaces prose-level feedback but never affects the exit code and never
// aborts the command — a missing/failing model backend degrades to a note in
// the report, not an error.
package launcher

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/frontmatter"
	"github.com/Townk/ai-playbook/internal/ui"
	"github.com/Townk/ai-playbook/internal/validate"
)

// reviewFn is the author.ReviewOnce seam: the AI review pass. Tests inject a
// fake so ValidateMain is exercised without a live model backend.
var reviewFn = author.ReviewOnce

// reviewSystemPrompt is the AI review pass's system prompt: a concise reviewer
// instruction, not a rewrite request.
const reviewSystemPrompt = "You are reviewing a playbook (a runnable markdown document) for quality. " +
	"Point out prose inconsistencies, missing or needed callouts, and any " +
	"non-idempotent, destructive, or non-reversible steps that lack a warning. " +
	"Be brief — a few bullet points at most. If the playbook looks good, say so " +
	"plainly instead of inventing nitpicks."

// aiSkipNote is printed in place of the AI review's text when no model backend
// is available (F24-style degrade, never an abort).
const aiSkipNote = "AI review skipped — no model backend (install + authenticate the Claude CLI, or set AI_PLAYBOOK_MODEL)"

// ValidateMain is the `ai-playbook validate` subcommand: it resolves the single
// playbook source (a slug via the store, or --file), runs the deterministic
// structural check (internal/validate.Check), an optional AI review pass, and
// prints a plain-text report to stdout. The exit code reflects ONLY the
// structural check — the AI pass is advisory.
func ValidateMain() int {
	kind, value, noAI, err := resolveValidateArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook validate: %v\n", err)
		return 2
	}

	var content, name string
	switch kind {
	case "file":
		data, rerr := os.ReadFile(value)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook validate: %v\n", rerr)
			return 2
		}
		content = string(data)
		name = value
	case "playbook":
		_, body, lerr := storeLoadFn(value)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook validate: %v\n", lerr)
			return 2
		}
		content = body
		name = value
	}

	fm, body, ok := frontmatter.Parse(content)

	_, _, uiBlocks := ui.Render(body, 80, nil, "")
	blocks := make([]validate.Block, 0, len(uiBlocks))
	for _, b := range uiBlocks {
		blocks = append(blocks, validate.Block{
			ID:     b.ID,
			Type:   b.Type,
			Lang:   b.Lang,
			Needs:  b.Needs,
			Static: b.Static,
		})
	}

	findings := validate.Check(body, fm, ok, blocks)

	var aiText string
	ranAI := !noAI
	if ranAI {
		cfg, _ := config.Load()
		text, aerr := reviewFn(cfg, reviewSystemPrompt, body)
		switch {
		case aerr == nil:
			aiText = text
		case isNoBackend(aerr):
			aiText = aiSkipNote
		default:
			aiText = fmt.Sprintf("AI review failed: %v", aerr)
		}
	}

	printValidateReport(os.Stdout, name, findings, ranAI, aiText)

	if validate.HasError(findings) {
		return 1
	}
	return 0
}

// printValidateReport writes the plain-text report: a header, then every
// Error finding, then every Warning finding, then (when the AI pass ran) an
// "AI review:" block with its text or skip/fail note.
func printValidateReport(w io.Writer, name string, findings []validate.Finding, ranAI bool, aiText string) {
	var errs, warns []validate.Finding
	for _, f := range findings {
		if f.Severity == validate.Error {
			errs = append(errs, f)
		} else {
			warns = append(warns, f)
		}
	}

	switch {
	case len(errs) > 0:
		fmt.Fprintf(w, "✗ %d problem(s) in %s\n", len(errs), name)
	case len(warns) > 0:
		fmt.Fprintf(w, "✓ %s: structurally valid (%d warnings)\n", name, len(warns))
	default:
		fmt.Fprintf(w, "✓ %s: structurally valid\n", name)
	}

	for _, f := range errs {
		fmt.Fprintf(w, "  ERROR    %-12s %s  (%s)\n", f.Check, f.Message, f.Where)
	}
	for _, f := range warns {
		fmt.Fprintf(w, "  WARNING  %-12s %s  (%s)\n", f.Check, f.Message, f.Where)
	}

	if ranAI {
		fmt.Fprintln(w, "\nAI review:")
		for _, line := range strings.Split(strings.TrimRight(aiText, "\n"), "\n") {
			fmt.Fprintf(w, "  %s\n", line)
		}
	}
}

// isNoBackend reports whether err indicates the AI harness/backend is missing
// or unusable — the binary isn't on PATH, the harness isn't supported/built, or
// no backend could be resolved. Mirrors internal/ui/results.go's
// looksLikeNoBackend. Best-effort substring match: a false negative merely
// shows the generic "AI review failed" note instead of the skip note.
func isNoBackend(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, needle := range []string{
		"executable file not found",
		"no such file or directory",
		"not found",
		"not yet supported",
		"no backend",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// resolveValidateArgs resolves the single playbook source from the `validate`
// arguments. Exactly one of {bare positional, --file} must be present:
//
//   - --file <path>      → ("file", path, noAI, nil)
//   - a bare positional   → ("playbook", slug, noAI, nil)
//
// Zero sources or both is a usage error. --no-ai skips the AI review pass.
func resolveValidateArgs(args []string) (kind, value string, noAI bool, err error) {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var file string
	fs.StringVar(&file, "file", "", "path to a markdown file to validate")
	fs.BoolVar(&noAI, "no-ai", false, "skip the AI review pass (structural check only)")
	if perr := fs.Parse(args); perr != nil {
		return "", "", false, perr
	}

	rest := fs.Args()
	if len(rest) > 1 {
		return "", "", false, fmt.Errorf("specify exactly one of <slug> or --file")
	}
	positional := ""
	if len(rest) == 1 {
		positional = rest[0]
	}

	count := 0
	for _, s := range []string{file, positional} {
		if s != "" {
			count++
		}
	}
	switch {
	case count == 0:
		return "", "", false, fmt.Errorf("specify a playbook: validate <slug> | --file <path>")
	case count > 1:
		return "", "", false, fmt.Errorf("specify exactly one of <slug> or --file")
	}

	if file != "" {
		return "file", file, noAI, nil
	}
	return "playbook", positional, noAI, nil
}
