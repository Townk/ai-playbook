// Package climeta is the single source of truth for ai-playbook's CLI
// command metadata: one registry (Commands) driving comprehensive --help
// (Overview + Help), man pages, and zsh completion. Flag descriptions here
// are copied VERBATIM from each subcommand's flag.*Var call sites (see the
// doc comment on Commands for the exact source of each entry) — never
// paraphrased, so this package can never drift silently from the code it
// documents.
//
//go:generate go run ../../cmd/docgen
package climeta

import (
	"fmt"
	"strings"
)

// Flag describes one command-line flag: its long Name (without the leading
// "--"), an optional value Placeholder (empty for a boolean flag), its
// verbatim Desc (copied from the flag.*Var call site), and whether it is a
// boolean switch (Bool).
type Flag struct {
	Name        string
	Placeholder string
	Desc        string
	Bool        bool
}

// Command describes one ai-playbook subcommand for the help/man/completion
// surfaces. Summary is a short one-line description (used in Overview);
// Synopsis is the one-line "USAGE" form (e.g. "run [<slug>] [flags]"); Long
// is the extended prose shown by Help (e.g. mode mutual-exclusion rules);
// Args names the positional argument(s) for documentation purposes only.
// Internal marks a command that Overview groups separately and documents
// minimally (Summary + Synopsis + key flags only — see the Commands doc
// comment for which flags are "key" on each internal command). SlugArg marks
// a command whose positional resolves a playbook store <slug> (for
// completion wiring in a later task).
type Command struct {
	Name     string
	Aliases  []string
	Summary  string
	Synopsis string
	Long     string
	Args     string
	Flags    []Flag
	Examples []string
	Internal bool
	SlugArg  bool
}

// Commands is the full CLI command registry, populated verbatim from each
// subcommand's argument-parsing code:
//
//   - run       → resolveRunArgs, internal/launcher/runcmd.go
//   - validate  → resolveValidateArgs, internal/launcher/validatecmd.go
//   - env       → resolveEnvArgs, internal/launcher/envcmd.go
//   - list      → ListMain, internal/launcher/storecmd.go
//   - search    → SearchMain, internal/launcher/storecmd.go
//   - create    → CreateMain/parseCreateArgs, internal/launcher/createcmd.go
//   - show      → ShowMain, internal/launcher/storecmd.go
//   - edit      → EditMain, internal/launcher/storecmd.go
//   - assist    → Assist, internal/launcher/launcher.go (alias: troubleshoot)
//   - finalize  → finalize, cmd/ai-playbook/finalize.go
//   - session   → SessionMain, internal/launcher/session.go
//   - answer    → AnswerMain, internal/launcher/launcher.go
//   - mcp       → mcpMain, cmd/ai-playbook/main.go
//   - diff      → diff.Main, internal/diff/main.go
//   - input     → input.Main, internal/input/main.go (only --type/--out/--measure
//     are documented; the ~40 theme flags are deliberately omitted)
//   - selftest  → selftest, cmd/ai-playbook/main.go
//   - version   → cmd/ai-playbook/main.go's "version" case
var Commands = []Command{
	// ── user-facing ─────────────────────────────────────────────────────
	{
		Name:     "assist",
		Aliases:  []string{"troubleshoot"},
		Summary:  "AI producer: capture context, triage, and author or drive a playbook",
		Synopsis: "assist [<prompt>]",
		Long: "assist captures the bounded origin context (last command, exit code, scrollback,\n" +
			"cwd/project root), triages the request, and either types a suggested command\n" +
			"back into your shell, renders a short prose answer, or authors and drives a\n" +
			"full playbook. With no <prompt>, it prompts interactively (a float under a\n" +
			"multiplexer, an inline box otherwise). troubleshoot is a deprecated alias.",
		Args: "[<prompt>]",
		Examples: []string{
			"ai-playbook assist",
			"ai-playbook assist \"why did this build fail?\"",
		},
	},
	{
		Name:     "create",
		Summary:  "Author a playbook directly from a prompt (no triage, no cache serve)",
		Synopsis: "create <prompt> [--template <t>]",
		Long: "create force-authors a fresh playbook from <prompt>, bypassing triage entirely —\n" +
			"unlike assist, it never serves a cache hit. It persists the result to the store\n" +
			"(and a cache entry) so a later assist for the same context can hit it.",
		Args: "<prompt>",
		Flags: []Flag{
			{Name: "template", Placeholder: "<t>", Desc: "reserved (not yet implemented); parses but is a no-op"},
		},
		Examples: []string{
			"ai-playbook create \"set up a new Go module with golangci-lint\"",
		},
	},
	{
		Name:     "list",
		Summary:  "List every saved playbook",
		Synopsis: "list [--format human|fuzzy-data-source|json]",
		Long:     "list enumerates the full store index in the requested format.",
		Flags: []Flag{
			{Name: "format", Placeholder: "<fmt>", Desc: "output format: human|fuzzy-data-source|json"},
		},
		Examples: []string{
			"ai-playbook list",
			"ai-playbook list --format json",
		},
	},
	{
		Name:     "search",
		Summary:  "Search saved playbooks by substring",
		Synopsis: "search <query> [--format human|fuzzy-data-source|json]",
		Long:     "search filters the store by substring match against <query> and prints the matches.",
		Args:     "<query>",
		Flags: []Flag{
			{Name: "format", Placeholder: "<fmt>", Desc: "output format: human|fuzzy-data-source|json"},
		},
		Examples: []string{
			"ai-playbook search deploy",
		},
	},
	{
		Name:     "show",
		Summary:  "Render a saved playbook read-only",
		Synopsis: "show <slug>",
		Long:     "show renders a saved playbook read-only in the pager (no run, no edit affordance beyond [edit]).",
		Args:     "<slug>",
		SlugArg:  true,
		Examples: []string{
			"ai-playbook show deploy-staging",
		},
	},
	{
		Name:     "edit",
		Summary:  "Open a saved playbook in $EDITOR",
		Synopsis: "edit <slug>",
		Long:     "edit resolves <slug> to its store file and opens it in $EDITOR.",
		Args:     "<slug>",
		SlugArg:  true,
		Examples: []string{
			"ai-playbook edit deploy-staging",
		},
	},
	{
		Name:     "run",
		Summary:  "Run a playbook (interactive, headless, or guided)",
		Synopsis: "run [<slug>] [--playbook <slug>] [--file <path>] [--assisted] [--auto] [--auto-rollback] [--with-env <json>]",
		Long: "run accepts a single playbook source, expressed one of three ways: a bare\n" +
			"positional <slug> (implied --playbook), --playbook <slug> (resolved through\n" +
			"the store), or --file <path> (a raw markdown file rendered as-is). Exactly\n" +
			"one source must be given; zero or more than one is an error.\n\n" +
			"Mode mutual-exclusion: --auto (headless) and --assisted (GUIDED fullscreen)\n" +
			"are mutually exclusive with each other. --auto-rollback is the default-viewer\n" +
			"opt-in and is mutually exclusive with --auto (auto mode rolls back by default;\n" +
			"use --no-auto-rollback to opt out) and with --assisted (assisted mode owns\n" +
			"post-failure flow via its own manual \"Roll back\" button). --no-auto-rollback\n" +
			"and --with-env are only valid with --auto.",
		Args:    "[<slug>]",
		SlugArg: true,
		Flags: []Flag{
			{Name: "playbook", Placeholder: "<slug>", Desc: "slug of a saved playbook to run"},
			{Name: "file", Placeholder: "<path>", Desc: "path to a markdown file to run"},
			{Name: "auto-rollback", Bool: true, Desc: "on a step failure, automatically roll back applied steps (else a manual button)"},
			{Name: "auto", Bool: true, Desc: "run headless: execute every block in order with no viewer/driver pane"},
			{Name: "no-auto-rollback", Bool: true, Desc: "with --auto, do not roll back applied steps on a failure"},
			{Name: "assisted", Bool: true, Desc: "run GUIDED fullscreen: step-by-step confirmation in the same viewer/driver pane"},
			{Name: "with-env", Placeholder: "<json>", Desc: "with --auto, supply env var values as inline JSON or a JSON file path"},
		},
		Examples: []string{
			"ai-playbook run deploy-staging",
			"ai-playbook run --file ./scratch.md --assisted",
			"ai-playbook run deploy-staging --auto --with-env '{\"REGION\":\"us-east-1\"}'",
		},
	},
	{
		Name:     "validate",
		Summary:  "Check a playbook's structure and (optionally) get an AI review",
		Synopsis: "validate [<slug>] [--file <path>] [--no-ai] [--plain] [--quiet]",
		Long: "validate accepts a single playbook source, expressed one of two ways: a bare\n" +
			"positional <slug> (a saved playbook, resolved through the store) or --file\n" +
			"<path> (a raw markdown file, validated as-is). Exactly one source must be\n" +
			"given; zero or more than one is a usage error. The exit code reflects ONLY\n" +
			"the deterministic structural check; the AI review pass is advisory and never\n" +
			"affects it.",
		Args:    "[<slug>]",
		SlugArg: true,
		Flags: []Flag{
			{Name: "file", Placeholder: "<path>", Desc: "path to a markdown file to validate"},
			{Name: "no-ai", Bool: true, Desc: "skip the AI review pass (structural check only)"},
			{Name: "plain", Bool: true, Desc: "use plain dot progress instead of the spinner (default when not attached to a terminal)"},
			{Name: "quiet", Bool: true, Desc: "suppress all output; report the result only via the exit code"},
		},
		Examples: []string{
			"ai-playbook validate deploy-staging",
			"ai-playbook validate --file ./scratch.md --no-ai",
		},
	},
	{
		Name:     "env",
		Summary:  "Print a playbook's declared env vars, resolved and redacted",
		Synopsis: "env [<slug>] [--file <path>]",
		Long: "env accepts a single playbook source, expressed one of two ways: a bare\n" +
			"positional <slug> (a saved playbook, resolved through the store) or --file\n" +
			"<path> (a raw markdown file, read as-is). Exactly one source must be given;\n" +
			"zero or more than one is a usage error. It prints the declared env: map as a\n" +
			"--with-env-compatible JSON object, resolving each value against the current\n" +
			"process environment (falling back to the declared default) and redacting\n" +
			"sensitive values.",
		Args:    "[<slug>]",
		SlugArg: true,
		Flags: []Flag{
			{Name: "file", Placeholder: "<path>", Desc: "path to a markdown file"},
		},
		Examples: []string{
			"ai-playbook env deploy-staging",
		},
	},

	// ── internal / advanced ─────────────────────────────────────────────
	{
		Name:     "finalize",
		Summary:  "Backfill front matter onto an existing playbook file (manual)",
		Synopsis: "finalize [--dry-run] <file.md>",
		Args:     "<file.md>",
		Internal: true,
		Flags: []Flag{
			{Name: "dry-run", Bool: true, Desc: "print the assembled front matter block to stdout; do not write the file"},
		},
	},
	{
		Name:     "session",
		Summary:  "The persistent docked session pane (internal plumbing)",
		Synopsis: "session [--request <json>] [--debug-log <path>] [--title <t>]",
		Internal: true,
		Flags: []Flag{
			{Name: "request", Placeholder: "<json>", Desc: "path to the captured request JSON (written by the launcher)"},
			{Name: "debug-log", Placeholder: "<path>", Desc: "append a debug trace to this file (set by the launcher)"},
			{Name: "title", Placeholder: "<t>", Desc: "working pane-header title (the classify-supplied label)"},
		},
	},
	{
		Name:     "answer",
		Summary:  "The docked prose-answer pager (internal plumbing)",
		Synopsis: "answer --request <json> --content <file> [--cached <iso>] [--title <t>] [--cwd <dir>]",
		Internal: true,
		Flags: []Flag{
			{Name: "request", Placeholder: "<json>", Desc: "the capture.Request as JSON (for the reload re-classify)"},
			{Name: "content", Placeholder: "<file>", Desc: "path to the prose markdown to render"},
			{Name: "cached", Placeholder: "<iso>", Desc: "ISO-8601 timestamp: show the 'cached' badge (cache replay)"},
			{Name: "title", Placeholder: "<t>", Desc: "pager header title"},
			{Name: "cwd", Placeholder: "<dir>", Desc: "working dir for the pager"},
		},
	},
	{
		Name:     "mcp",
		Summary:  "MCP stdio server adapter for the session's tools backend (internal plumbing)",
		Synopsis: "mcp --socket <path>",
		Internal: true,
		Flags: []Flag{
			{Name: "socket", Placeholder: "<path>", Desc: "path to the session's tools-backend unix socket"},
		},
	},
	{
		Name:     "diff",
		Summary:  "Scrollable standalone diff viewer (internal plumbing)",
		Synopsis: "diff <patchfile>",
		Args:     "<patchfile>",
		Internal: true,
	},
	{
		Name:     "input",
		Summary:  "The internal input widget (internal plumbing)",
		Synopsis: "input [--type <t>] [--out <path>] [--measure] [...]",
		Internal: true,
		Flags: []Flag{
			{Name: "type", Placeholder: "<t>", Desc: "widget type: text|line|confirm|choose|form"},
			{Name: "out", Placeholder: "<path>", Desc: "path to a one-shot output FILE: on submit the value is written here and the process exits 0; on cancel nothing is written and the process exits 130. Lets a FLOATED input (whose stdout is detached) hand its answer back to a polling launcher."},
			{Name: "measure", Bool: true, Desc: "print the rendered height and exit (no TUI)"},
		},
	},
	{
		Name:     "selftest",
		Summary:  "Drive the user's real shell and report (validates the driver)",
		Synopsis: "selftest",
		Internal: true,
	},
	{
		Name:     "version",
		Summary:  "Print the binary's version",
		Synopsis: "version",
		Internal: true,
	},
}

// commandIndex resolves a command or alias name to its Commands entry.
// Built lazily (not at package init) so a future test can freely mutate
// Commands without needing to know about a cache.
func commandIndex() map[string]int {
	idx := make(map[string]int, len(Commands)*2)
	for i, cmd := range Commands {
		idx[cmd.Name] = i
		for _, alias := range cmd.Aliases {
			idx[alias] = i
		}
	}
	return idx
}

// Lookup resolves name (a canonical command name or an alias) to its
// Command. ok is false when name is not registered.
func Lookup(name string) (Command, bool) {
	idx := commandIndex()
	i, ok := idx[name]
	if !ok {
		return Command{}, false
	}
	return Commands[i], true
}

// Overview renders the top-level `ai-playbook --help` / `ai-playbook help`
// output: a one-line intro, the user-facing commands (each Name aligned to a
// common width, followed by its Summary), then the internal/advanced
// commands the same way, and a closing footer pointing at per-command help.
// Internal-command flag detail is deliberately never shown here.
func Overview() string {
	var user, internal []Command
	for _, cmd := range Commands {
		if cmd.Internal {
			internal = append(internal, cmd)
		} else {
			user = append(user, cmd)
		}
	}

	width := 0
	for _, cmd := range Commands {
		if len(cmd.Name) > width {
			width = len(cmd.Name)
		}
	}

	var b strings.Builder
	b.WriteString("ai-playbook — capture, author, and run terminal playbooks.\n")

	b.WriteString("\nCommands:\n")
	for _, cmd := range user {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, cmd.Name, cmd.Summary)
	}

	if len(internal) > 0 {
		b.WriteString("\nInternal / advanced (plumbing; not typically invoked by hand):\n")
		for _, cmd := range internal {
			fmt.Fprintf(&b, "  %-*s  %s\n", width, cmd.Name, cmd.Summary)
		}
	}

	b.WriteString("\nRun 'ai-playbook <command> --help' for details.\n")
	return b.String()
}

// Help renders the `ai-playbook <name> --help` output for name (resolved
// through Lookup, so an alias returns identical text to its canonical
// command). ok is false when name is not a registered command or alias.
//
// Sections are USAGE (always present), the Long description (omitted when
// empty), FLAGS (omitted when the command has none), and EXAMPLES (omitted
// when the command has none).
func Help(name string) (string, bool) {
	cmd, ok := Lookup(name)
	if !ok {
		return "", false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "USAGE\n  %s\n", cmd.Synopsis)

	if cmd.Long != "" {
		fmt.Fprintf(&b, "\n%s\n", cmd.Long)
	}

	if len(cmd.Flags) > 0 {
		fw := 0
		for _, f := range cmd.Flags {
			l := len(flagLabel(f))
			if l > fw {
				fw = l
			}
		}
		b.WriteString("\nFLAGS\n")
		for _, f := range cmd.Flags {
			fmt.Fprintf(&b, "  --%-*s   %s\n", fw, flagLabel(f), f.Desc)
		}
	}

	if len(cmd.Examples) > 0 {
		b.WriteString("\nEXAMPLES\n")
		for _, ex := range cmd.Examples {
			fmt.Fprintf(&b, "  %s\n", ex)
		}
	}

	return b.String(), true
}

// flagLabel returns a flag's "--name <placeholder>" label sans the leading
// "--" (Help/the flags-width computation add it), e.g. "file <path>" or
// "auto" for a boolean.
func flagLabel(f Flag) string {
	if f.Placeholder == "" {
		return f.Name
	}
	return f.Name + " " + f.Placeholder
}
