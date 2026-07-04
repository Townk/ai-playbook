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
// unbalanced fences, runnable/lang warnings), extended here with a
// depends_on chain check (dependsOnFindings, via resolveChain — the same
// resolver `run`/`env` use — since internal/validate itself stays a pure leaf
// with no store coupling) that together drive the exit code, and an
// optional AI review pass (author.ReviewStream, via the reviewStreamFn seam,
// fanned out with live progress — a TTY spinner + model activity, or a
// stderr heartbeat when there's no TTY, or --plain to force the heartbeat
// even on a TTY) that surfaces prose-level feedback but never affects the
// exit code and never aborts the command — a missing/failing model backend
// degrades to a note in the report, not an error. --quiet suppresses all
// output (report, AI review, progress) and skips the AI pass entirely, since
// only the exit code can carry any result.
package launcher

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Townk/ai-playbook/internal/agentstream"
	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/pkg/playbook"
	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
	"github.com/Townk/ai-playbook/pkg/playbook/validate"
)

// reviewStreamFn is the author.ReviewStream seam: the AI review pass. Tests
// inject a fake so ValidateMain is exercised without a live model backend.
var reviewStreamFn = author.ReviewStream

// reviewSystemPrompt is the AI review pass's system prompt: a concise reviewer
// instruction, not a rewrite request.
const reviewSystemPrompt = "You are reviewing a playbook (a runnable markdown document) for quality. " +
	"Point out prose inconsistencies, missing or needed callouts, and any " +
	"non-idempotent, destructive, or non-reversible steps that lack a warning. " +
	"Be brief — a few bullet points at most. If the playbook looks good, say so " +
	"plainly instead of inventing nitpicks."

// aiSkipNote is printed in place of the AI review's text when no model backend
// is available (F24-style degrade, never an abort).
const aiSkipNote = "AI review skipped — no model backend (install + authenticate the Claude CLI, or configure [agent] in ai-playbook config)"

// ValidateMain is the `ai-playbook validate` subcommand: it resolves the single
// playbook source (a slug via the store, or --file), runs the deterministic
// structural check (internal/validate.Check), an optional AI review pass, and
// prints a plain-text report to stdout. The exit code reflects ONLY the
// structural check — the AI pass is advisory. --plain forces the low-noise
// dot-heartbeat progress even on a terminal; --quiet suppresses all output
// (report, AI review, progress) and, since that output would be discarded,
// also skips the AI pass — only the exit code carries the result.
func ValidateMain() int {
	ra, err := resolveValidateArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook validate: %v\n", err)
		return 2
	}

	var content, name string
	switch ra.Kind {
	case "file":
		data, rerr := os.ReadFile(ra.Value)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook validate: %v\n", rerr)
			return 2
		}
		content = string(data)
		name = ra.Value
	case "playbook":
		meta, _, lerr := storeLoadFn(ra.Value)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook validate: %v\n", lerr)
			return 2
		}
		// Read the full file (store.Load's body is front-matter-stripped, so
		// re-parsing it would drop the front matter — a false "missing front
		// matter" error and no depends_on checks).
		data, rerr := os.ReadFile(meta.Path)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook validate: %v\n", rerr)
			return 2
		}
		content = string(data)
		name = ra.Value
	}

	fm, body, ok := frontmatter.Parse(content)

	pbBlocks := playbook.ParseBlocks(body)
	blocks := make([]validate.Block, 0, len(pbBlocks))
	for _, b := range pbBlocks {
		blocks = append(blocks, validate.Block{
			ID:     b.ID,
			Type:   b.Type,
			Lang:   b.Lang,
			Needs:  b.Needs,
			Static: b.Static,
		})
	}

	// bodyLineOffset: body is a pure suffix of content (frontmatter.Parse
	// strips only a leading --- block, if any), so the number of newlines in
	// the stripped prefix is exactly the number of file lines consumed before
	// body's line 1 — 0 when there's no front matter, since then body==content.
	bodyLineOffset := strings.Count(content[:len(content)-len(body)], "\n")

	findings := validate.Check(body, fm, ok, blocks, bodyLineOffset)
	findings = append(findings, dependsOnFindings(fm.DependsOn)...)

	var aiText string
	// runAI: the AI pass runs unless explicitly skipped via --no-ai, or
	// implicitly skipped by --quiet — quiet discards all output and can't
	// change the exit code, so paying for a model call would be pure waste.
	runAI := !ra.NoAI && !ra.Quiet
	if runAI {
		cfg, _ := config.Load()
		events, closeFn, serr := reviewStreamFn(cfg, reviewSystemPrompt, body)
		switch {
		case serr == nil:
			reader, activity, _ := agentstream.FanOut(events, closeFn, ActivityBuffer)

			done := make(chan struct{})
			go func() {
				b, _ := io.ReadAll(reader)
				aiText = strings.TrimSpace(string(b))
				close(done)
			}()

			if hasTTYFn() && !ra.Plain {
				runCreateProgressFn(activity, nil, done)
			} else {
				// Mandatory drain: FanOut's activity sends are best-effort/non-blocking,
				// but nothing else reads this channel in the dots path, so an unread
				// buffer would just sit there — draining it keeps the fan-out tidy.
				go func() {
					for range activity {
					}
				}()
				heartbeat(os.Stderr, done, 2*time.Second)
			}
			<-done
		case isNoBackend(serr):
			aiText = aiSkipNote
		default:
			aiText = fmt.Sprintf("AI review failed: %v", serr)
		}
	}

	if !ra.Quiet {
		printValidateReport(os.Stdout, name, findings, runAI, aiText)
	}

	if validate.HasError(findings) {
		return 1
	}
	return 0
}

// dependsOnFindings resolves rootDeps' whole depends_on chain (resolveChain,
// the same resolver `run`/`env` use) and turns each structural DepIssue into a
// validate.Finding: a dangling dependency names the missing slug; a cycle
// lists its participants joined by " → " (mirroring printDepIssues' own
// wording). Both are Error severity under the "depends_on" check, so a
// dep issue folds into ValidateMain's existing findings/exit-code/print path
// exactly like any structural finding. internal/validate itself stays a pure
// leaf — the resolver call lives here in the launcher, not there.
func dependsOnFindings(rootDeps []string) []validate.Finding {
	if len(rootDeps) == 0 {
		return nil
	}
	_, issues := resolveChain(rootDeps)
	findings := make([]validate.Finding, 0, len(issues))
	for _, issue := range issues {
		var msg string
		switch issue.Kind {
		case "dangling":
			msg = fmt.Sprintf("depends_on %q does not exist in the store", issue.Slug)
		case "cycle":
			msg = "depends_on cycle: " + strings.Join(issue.Path, " → ")
		}
		findings = append(findings, validate.Finding{
			Severity: validate.Error,
			Check:    "depends_on",
			Message:  msg,
			Where:    "front matter",
		})
	}
	return findings
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

// hasTTYFn is the hasTTY seam: tests stub it to force either progress branch
// (spinner vs. dots) without needing an actual controlling terminal.
var hasTTYFn = hasTTY

// hasTTY reports whether /dev/tty can be opened for read+write — mirroring
// runCreateProgress's own check so the AI-pass progress mechanism (inline
// spinner vs. stderr heartbeat) agrees with create's.
func hasTTY() bool {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// heartbeat writes a "." to w every `every` until done closes, then a trailing
// newline — the no-TTY (piped/CI) progress indicator for the AI review pass,
// kept off stdout (the report's channel) by always being called with
// os.Stderr.
func heartbeat(w io.Writer, done <-chan struct{}, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			fmt.Fprint(w, ".")
		case <-done:
			fmt.Fprintln(w)
			return
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

// validateArgs is resolveValidateArgs's parsed result: the single playbook
// source (Kind/Value) plus the validate subcommand's flags.
type validateArgs struct {
	// Kind is "file" or "playbook", selecting how Value is resolved.
	Kind, Value string
	// NoAI skips the AI review pass (structural check only).
	NoAI bool
	// Plain forces the low-noise dot-heartbeat progress even on a terminal,
	// where the animated spinner is otherwise the default.
	Plain bool
	// Quiet suppresses all output (report, warnings, AI review, progress);
	// only the exit code carries the result. Implies skipping the AI pass
	// (its output would be discarded and it can't affect the exit code) and
	// takes precedence over Plain.
	Quiet bool
}

// resolveValidateArgs resolves the single playbook source and flags from the
// `validate` arguments. Exactly one of {bare positional, --file} must be
// present:
//
//   - --file <path>      → Kind="file", Value=path
//   - a bare positional   → Kind="playbook", Value=slug
//
// Zero sources or both is a usage error. --no-ai skips the AI review pass;
// --plain forces plain dot progress; --quiet suppresses all output.
func resolveValidateArgs(args []string) (validateArgs, error) {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var noAI, plain, quiet bool
	fs.BoolVar(&noAI, "no-ai", false, "skip the AI review pass (structural check only)")
	fs.BoolVar(&plain, "plain", false, "use plain dot progress instead of the spinner (default when not attached to a terminal)")
	fs.BoolVar(&quiet, "quiet", false, "suppress all output; report the result only via the exit code")
	kind, value, err := resolveSource(fs, args, "validate", false)
	if err != nil {
		return validateArgs{}, err
	}
	return validateArgs{Kind: kind, Value: value, NoAI: noAI, Plain: plain, Quiet: quiet}, nil
}
