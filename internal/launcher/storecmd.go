// storecmd.go — list and search subcommand entrypoints over the playbook store.
//
// Both commands share three output formats selected via --format:
//
//   - human (default): aligned tabwriter columns for terminal reading.
//   - fuzzy-data-source: US-delimited (\x1f) records for fzf --with-nth 1
//     piping; display \x1f slug \x1f path per line.
//   - json: indented JSON array of []store.Meta for scripting.
//
// The indexFn / searchFn package-level vars are the store seams: production
// wires them to store.Index / store.Search; tests inject fakes that return
// canned []store.Meta without seeding any real directories.
package launcher

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Townk/ai-playbook/internal/store"
)

// indexFn is the Index seam: lists all playbooks from both stores.
var indexFn = func() ([]store.Meta, error) { return store.Index() }

// searchFn is the Search seam: filters playbooks by substring query.
var searchFn = func(query string) ([]store.Meta, error) { return store.Search(query) }

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
