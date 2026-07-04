// storecmd.go — list, search, show, and edit subcommand entrypoints over the
// playbook store.
//
// list/search share three output formats selected via --format:
//
//   - human (default): aligned tabwriter columns for terminal reading.
//   - fuzzy-data-source: US-delimited (\x1f) records for fzf --with-nth 1
//     piping; display \x1f slug \x1f path per line.
//   - json: indented JSON array of []store.Meta for scripting.
//
// show renders a saved playbook read-only by building a ui.Options (File +
// SourcePath) and delegating to ui.Run (via uiRunFn — seam for tests).
//
// edit opens a saved playbook in $EDITOR via the editorSpawn seam.
//
// Package-level seams:
//   - indexFn  / searchFn: production resolves the configured store.Dirs
//     (storeDirs) and calls Index / Search on it; tests inject fakes.
//   - pathForFn:           production wires storePathFor (Dirs.PathFor over the
//     configured dirs); tests inject fakes.
//   - uiRunFn:             production wires ui.Run; tests capture the Options / inject a no-op.
//   - editorSpawn:         production exec.Commands $EDITOR; tests inject a recorder.
package launcher

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/ui"
	"github.com/Townk/ai-playbook/pkg/store"
)

// storeDirs resolves the two store directories from the merged configuration
// and the captured project root. The launcher owns this wiring: the store
// package takes explicit directories (a store.Dirs) and performs no
// configuration lookup of its own.
func storeDirs() (store.Dirs, error) {
	c, err := config.Load()
	if err != nil {
		return store.Dirs{}, err
	}
	return store.Dirs{
		Global:  c.GlobalStoreDir(),
		Project: c.ProjectStoreDir(capture.ProjectRoot()),
	}, nil
}

// storePathFor is the shared production implementation behind the pathForFn and
// storePathForFn seams: resolve the configured store dirs and route the slug.
// A config-load failure reads as "not found", matching the pre-Dirs behavior.
func storePathFor(slug string) (string, bool) {
	d, err := storeDirs()
	if err != nil {
		return "", false
	}
	return d.PathFor(slug)
}

// indexFn is the Index seam: lists all playbooks from both stores.
var indexFn = func() ([]store.Meta, error) {
	d, err := storeDirs()
	if err != nil {
		return nil, err
	}
	return d.Index(), nil
}

// searchFn is the Search seam: filters playbooks by substring query.
var searchFn = func(query string) ([]store.Meta, error) {
	d, err := storeDirs()
	if err != nil {
		return nil, err
	}
	return d.Search(query), nil
}

// pathForFn is the PathFor seam: resolves a slug to its file path.
var pathForFn = storePathFor

// uiRunFn is the ui.Run seam: delegates to the real viewer; tests replace it to
// capture the ui.Options a call site builds (and to avoid a TTY). It is the
// single seam every launcher viewer-launch path now flows through, replacing the
// former ui.Main() + os.Args-reshaping + per-setter seams.
var uiRunFn = ui.Run

// editorSpawn is the seam for launching $EDITOR: production opens the editor
// with inherited stdio; tests inject a recorder.
var editorSpawn = func(editor, path string) error {
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ListMain is the `ai-playbook list [--format human|fuzzy-data-source|json]`
// subcommand: enumerate the full store index in the requested format.
func ListMain() int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	var format string
	fs.StringVar(&format, "format", "human", "output format: human|fuzzy-data-source|json")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return 2
	}
	if !validFormat(format) {
		fmt.Fprintf(os.Stderr, "ai-playbook list: unknown --format %q (want human|fuzzy-data-source|json)\n", format)
		return 2
	}
	metas, err := indexFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook list: %v\n", err)
		return 1
	}
	return printMetas(metas, format)
}

// SearchMain is the `ai-playbook search <query> [--format ...]` subcommand:
// filter the store by substring and print the matches. The positional <query>
// is required; missing it is a usage error (exit 2).
func SearchMain() int {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	var format string
	fs.StringVar(&format, "format", "human", "output format: human|fuzzy-data-source|json")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return 2
	}
	if !validFormat(format) {
		fmt.Fprintf(os.Stderr, "ai-playbook search: unknown --format %q (want human|fuzzy-data-source|json)\n", format)
		return 2
	}
	args := fs.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ai-playbook search: <query> is required")
		return 2
	}
	metas, err := searchFn(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook search: %v\n", err)
		return 1
	}
	return printMetas(metas, format)
}

// validFormat reports whether format is one of the three supported values.
func validFormat(format string) bool {
	switch format {
	case "human", "fuzzy-data-source", "json":
		return true
	}
	return false
}

// printMetas writes metas to stdout in the requested format. An empty slice
// emits "no saved playbooks yet." to stderr and returns 0 — not an error, and
// never an empty table.
func printMetas(metas []store.Meta, format string) int {
	if len(metas) == 0 {
		fmt.Fprintln(os.Stderr, "no saved playbooks yet.")
		return 0
	}
	switch format {
	case "fuzzy-data-source":
		fmt.Print(formatFuzzy(metas))
	case "json":
		s, err := formatJSON(metas)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook: json encode: %v\n", err)
			return 1
		}
		fmt.Println(s)
	default: // "human"
		fmt.Print(formatHuman(metas))
	}
	return 0
}

// formatHuman returns an aligned tabwriter table with columns name,
// description, category, and age (humanized time since Meta.Created).
func formatHuman(metas []store.Meta) string {
	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDESCRIPTION\tCATEGORY\tAGE")
	for _, m := range metas {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", m.Name, m.Description, m.Category, humanAge(m.Created))
	}
	_ = tw.Flush()
	return sb.String()
}

// formatFuzzy returns one line per meta for the fuzzy-data-source format:
//
//	<display>\x1f<slug>\x1f<path>\n
//
// display = "<name> — <description> · <category> · <tags>" with comma-joined
// tags; empty optional segments (description, category, tags) are omitted
// cleanly so there are no stray "—" or "·" characters.
//
// The \x1f (US) delimiter pairs with fzf's --delimiter $'\x1f' --with-nth 1
// so the picker searches display (field 1) while ENTER binds {2} (slug) and
// ALT+ENTER binds {3} (path).
func formatFuzzy(metas []store.Meta) string {
	var sb strings.Builder
	for _, m := range metas {
		sb.WriteString(fuzzyDisplay(m))
		sb.WriteByte('\x1f')
		sb.WriteString(m.Slug)
		sb.WriteByte('\x1f')
		sb.WriteString(m.Path)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// fuzzyDisplay builds the human-readable display field for one meta:
// "<name> — <description> · <category> · <tags>". Empty optional fields are
// skipped (no stray separators). When all optional fields are empty, only the
// name is returned.
func fuzzyDisplay(m store.Meta) string {
	var parts []string
	if m.Description != "" {
		parts = append(parts, m.Description)
	}
	if m.Category != "" {
		parts = append(parts, m.Category)
	}
	if len(m.Tags) > 0 {
		parts = append(parts, strings.Join(m.Tags, ", "))
	}
	if len(parts) == 0 {
		return m.Name
	}
	return m.Name + " — " + strings.Join(parts, " · ")
}

// formatJSON returns the metas array as indented JSON.
func formatJSON(metas []store.Meta) (string, error) {
	b, err := json.MarshalIndent(metas, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// resolveShow looks up slug via pathForFn and returns (path, true) on a hit or
// ("", false) on a miss. Extracted as a pure function so unit tests can verify
// slug resolution without launching ui.Run (which requires a TTY).
func resolveShow(slug string) (string, bool) {
	return pathForFn(slug)
}

// ShowMain is the `ai-playbook show <slug>` subcommand: renders a saved
// playbook read-only by building a ui.Options over the resolved store path and
// delegating to uiRunFn (ui.Run in production). No Cached flag is set, so the
// cached badge never appears. SourcePath threads the real store path into the
// viewer model so it can offer an [edit] button for this file-backed playbook.
func ShowMain() int {
	args := os.Args[2:]
	if len(args) == 0 || args[0] == "" {
		fmt.Fprintln(os.Stderr, "ai-playbook show: <slug> is required")
		return 2
	}
	slug := args[0]
	path, ok := resolveShow(slug)
	if !ok {
		fmt.Fprintf(os.Stderr, "ai-playbook show: no playbook for slug %q\n", slug)
		return 1
	}
	return uiRunFn(ui.Options{File: path, SourcePath: path})
}

// EditMain is the `ai-playbook edit <slug>` subcommand: resolves the playbook
// path and opens it in $EDITOR. Missing $EDITOR or an unknown slug are user
// errors (exit 1); a missing slug argument is a usage error (exit 2).
func EditMain() int {
	args := os.Args[2:]
	if len(args) == 0 || args[0] == "" {
		fmt.Fprintln(os.Stderr, "ai-playbook edit: <slug> is required")
		return 2
	}
	slug := args[0]
	editor := os.Getenv("EDITOR")
	if editor == "" {
		fmt.Fprintln(os.Stderr, "ai-playbook edit: $EDITOR is not set")
		return 1
	}
	path, ok := pathForFn(slug)
	if !ok {
		fmt.Fprintf(os.Stderr, "ai-playbook edit: no playbook for slug %q\n", slug)
		return 1
	}
	if err := editorSpawn(editor, path); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook edit: %v\n", err)
		return 1
	}
	return 0
}

// humanAge returns a short human-readable duration since t (e.g. "3d", "2h",
// "5m"). A zero time returns "—"; negative durations (future) return "now".
func humanAge(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	if d < 0 {
		return "now"
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}
