// kbcmd.go — the public `kb` verb: browse, search, and edit the two-set
// knowledge base (ADR-0011 / docs/specifications/knowledge-base.md).
//
//	kb show   [--project <path>] [--global]   print the knowledge sets.
//	kb edit   [--project <path>] [--global]   open a knowledge file in $EDITOR.
//	kb search <query> [--all]                 substring search over fact bullets.
//	kb list                                   the global file + every project KB.
//
// Package-level seams:
//   - kbConfigFn:      production resolves config.Load()'s KBDir()/KB.Budget;
//     tests inject a fixed (root, budget).
//   - kbProjectRootFn: production resolves capture.ProjectRoot(); tests inject
//     a fixed path.
//   - editorSpawn is the storecmd.go $EDITOR seam, shared verbatim.
package launcher

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/Townk/ai-playbook/internal/author"
	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/kb"
)

// kbConfigFn resolves the [kb] root + per-file budget from the merged
// configuration. Seam for tests (production: config.Load + KBDir/KB.Budget).
var kbConfigFn = func() (root string, budget int, err error) {
	c, err := config.Load()
	if err != nil {
		return "", 0, err
	}
	return c.KBDir(), c.KB.Budget, nil
}

// kbProjectRootFn resolves the cwd's project root. Seam for tests
// (production: capture.ProjectRoot()).
var kbProjectRootFn = capture.ProjectRoot

// KBMain is the `ai-playbook kb <show|edit|search|list>` dispatch: it reads
// the sub-subcommand from os.Args[2] and routes to the matching handler. A
// missing or unrecognized subcommand is a usage error (exit 2).
func KBMain() int {
	args := os.Args[2:]
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "ai-playbook kb: a subcommand is required (show|edit|search|list)")
		return 2
	}
	switch args[0] {
	case "show":
		return kbShowMain(args[1:])
	case "edit":
		return kbEditMain(args[1:])
	case "search":
		return kbSearchMain(args[1:])
	case "list":
		return kbListMain(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "ai-playbook kb: unknown subcommand %q (want show|edit|search|list)\n", args[0])
		return 2
	}
}

// kbShowMain implements `kb show [--project <path>] [--global]`: by default it
// prints BOTH knowledge sets — exactly what recall sees (global then project,
// for the cwd's project root). --global narrows to the global set only;
// --project <path> alone narrows to ONLY that project's set (the global set is
// suppressed), for <path> instead of the cwd's project root; passing both
// shows both sets with the project path overridden.
func kbShowMain(args []string) int {
	fs := flag.NewFlagSet("kb show", flag.ContinueOnError)
	var project string
	var global bool
	fs.StringVar(&project, "project", "", "show ONLY this project path's knowledge set (suppresses the global set unless --global is also given)")
	fs.BoolVar(&global, "global", false, "narrow to the global knowledge set only")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "ai-playbook kb show: unexpected argument(s)")
		return 2
	}

	root, budget, err := kbConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook kb show: %v\n", err)
		return 1
	}

	projectRoot := project
	if projectRoot == "" {
		projectRoot = kbProjectRootFn()
	}

	showBoth := !global && project == ""
	wantGlobal := showBoth || global
	wantProject := showBoth || project != ""

	globalKB, projectKB := author.LoadRecall(root, projectRoot, budget)

	var b strings.Builder
	if wantGlobal {
		b.WriteString(renderKBSection("global", string(globalKB)))
	}
	if wantProject {
		if wantGlobal {
			b.WriteString("\n")
		}
		b.WriteString(renderKBSection(fmt.Sprintf("project (%s)", projectRoot), string(projectKB)))
	}
	fmt.Print(b.String())
	return 0
}

// renderKBSection renders one labeled knowledge-set block: a "== label =="
// header followed by the file content verbatim, or "(empty)" when the set has
// no facts yet.
func renderKBSection(label, content string) string {
	if strings.TrimSpace(content) == "" {
		return fmt.Sprintf("== %s ==\n(empty)\n", label)
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return fmt.Sprintf("== %s ==\n%s", label, content)
}

// kbEditMain implements `kb edit [--project <path>] [--global]`: opens the
// resolved knowledge file in $EDITOR (the storecmd.go editorSpawn pattern).
// Default: the cwd's project file. --global edits the global file instead;
// --project <path> edits the project file for <path> instead of the cwd's
// project root. --global and --project are mutually exclusive — edit opens
// exactly one file.
func kbEditMain(args []string) int {
	fs := flag.NewFlagSet("kb edit", flag.ContinueOnError)
	var project string
	var global bool
	fs.StringVar(&project, "project", "", "edit the project knowledge file for this path instead of the cwd's project root")
	fs.BoolVar(&global, "global", false, "edit the global knowledge file instead of a project file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "ai-playbook kb edit: unexpected argument(s)")
		return 2
	}
	if global && project != "" {
		fmt.Fprintln(os.Stderr, "ai-playbook kb edit: --global and --project are mutually exclusive")
		return 2
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		fmt.Fprintln(os.Stderr, "ai-playbook kb edit: $EDITOR is not set")
		return 1
	}

	root, _, err := kbConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook kb edit: %v\n", err)
		return 1
	}

	var path string
	if global {
		path = kb.GlobalPath(root)
	} else {
		projectRoot := project
		if projectRoot == "" {
			projectRoot = kbProjectRootFn()
		}
		path = kb.Path(root, projectRoot)
	}

	// The knowledge file may not exist yet (nothing remembered so far); ensure
	// its parent directory does, so $EDITOR can save a brand-new file.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook kb edit: %v\n", err)
		return 1
	}
	if err := editorSpawn(editor, path); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook kb edit: %v\n", err)
		return 1
	}
	return 0
}

// kbSearchMain implements `kb search <query> [--all]`: a case-insensitive
// substring search over fact bullets. Default scope: the global file plus the
// cwd's project file; --all searches the global file plus EVERY project file.
// Results are grouped by set/project (real names via the project meta line,
// the sha1 key as fallback). A query with no hits prints a one-line note to
// stderr and exits 0 — not an error.
func kbSearchMain(args []string) int {
	fs := flag.NewFlagSet("kb search", flag.ContinueOnError)
	var all bool
	fs.BoolVar(&all, "all", false, "search every project's knowledge file, not just the cwd's")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "ai-playbook kb search: <query> is required")
		return 2
	}
	// Exactly ONE positional (the query). The Go flag package stops parsing at
	// the first positional, so `kb search docker --all` would otherwise
	// silently DROP --all (it lands in fs.Args() as a second "positional") —
	// make that a loud usage error teaching the ordering instead.
	if len(rest) > 1 {
		fmt.Fprintf(os.Stderr, "ai-playbook kb search: unexpected argument(s) after <query>: %s (flags go before the query: kb search [--all] <query>)\n", strings.Join(rest[1:], " "))
		return 2
	}
	query := rest[0]

	root, _, err := kbConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook kb search: %v\n", err)
		return 1
	}

	type group struct {
		label   string
		matches []string
	}
	var groups []group

	if m := matchBullets(string(kb.LoadGlobal(root)), query); len(m) > 0 {
		groups = append(groups, group{"global", m})
	}

	if all {
		for _, p := range kbAllProjects(root) {
			if m := matchBullets(p.Content, query); len(m) > 0 {
				groups = append(groups, group{p.Name, m})
			}
		}
	} else {
		projectRoot := kbProjectRootFn()
		if m := matchBullets(string(kb.LoadProject(root, projectRoot)), query); len(m) > 0 {
			groups = append(groups, group{projectRoot, m})
		}
	}

	if len(groups) == 0 {
		fmt.Fprintf(os.Stderr, "kb search: no matches for %q\n", query)
		return 0
	}

	var b strings.Builder
	for i, g := range groups {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "== %s ==\n", g.label)
		for _, m := range g.matches {
			fmt.Fprintf(&b, "- %s\n", m)
		}
	}
	fmt.Print(b.String())
	return 0
}

// matchBullets returns every `- ` fact bullet in content whose text contains
// query as a case-insensitive substring, in file order. An empty query
// matches nothing (there is no useful "list everything" reading of `kb
// search` with no query — that's `kb show`).
func matchBullets(content, query string) []string {
	if query == "" {
		return nil
	}
	q := strings.ToLower(query)
	var out []string
	for _, bullet := range kbBullets(content) {
		if strings.Contains(strings.ToLower(bullet), q) {
			out = append(out, bullet)
		}
	}
	return out
}

// kbBullets extracts every `- ` bullet's text from a knowledge file's raw
// content, across every section/subsection — section structure does not
// matter for search/list, only the bullets do.
func kbBullets(content string) []string {
	var out []string
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "- ") {
			out = append(out, strings.TrimSpace(t[len("- "):]))
		}
	}
	return out
}

// kbListMain implements `kb list`: the global file (size, fact count) plus
// every project that has a knowledge file (real name via the meta line, sha1
// key as fallback; path; size; fact count).
func kbListMain(args []string) int {
	fs := flag.NewFlagSet("kb list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "ai-playbook kb list: unexpected argument(s)")
		return 2
	}

	root, _, err := kbConfigFn()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook kb list: %v\n", err)
		return 1
	}

	globalContent := string(kb.LoadGlobal(root))
	projects := kbAllProjects(root)

	fmt.Print(formatKBList(kb.GlobalPath(root), globalContent, projects))
	if len(projects) == 0 {
		fmt.Fprintln(os.Stderr, "kb list: no project knowledge files yet.")
	}
	return 0
}

// kbProjectEntry is one project's on-disk knowledge file, as enumerated for
// `kb list`/`kb search --all`: its resolved display Name (the recorded
// project root via the meta line, else the sha1 key), file Path, and raw
// Content.
type kbProjectEntry struct {
	Name    string
	Path    string
	Content string
}

// kbAllProjects enumerates every project knowledge file under root
// (root/projects/*/knowledge.md), resolving each one's display name via
// kb.ProjectName with the sha1 key (the directory name) as fallback. Order is
// sorted by file path for deterministic output.
func kbAllProjects(root string) []kbProjectEntry {
	paths, _ := filepath.Glob(filepath.Join(root, "projects", "*", "knowledge.md"))
	sort.Strings(paths)
	out := make([]kbProjectEntry, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		content := string(raw)
		name, ok := kb.ProjectName(content)
		if !ok {
			name = filepath.Base(filepath.Dir(p)) // sha1-key fallback
		}
		out = append(out, kbProjectEntry{Name: name, Path: p, Content: content})
	}
	return out
}

// formatKBList renders the `kb list` table: the global row, then one row per
// project entry, aligned via tabwriter. The global row is always present
// (even 0 bytes/0 facts) so an empty KB still produces a well-formed table.
func formatKBList(globalPath, globalContent string, projects []kbProjectEntry) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SET\tSIZE\tFACTS\tPATH")
	fmt.Fprintf(tw, "global\t%d\t%d\t%s\n", len(globalContent), len(kbBullets(globalContent)), globalPath)
	for _, p := range projects {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%s\n", p.Name, len(p.Content), len(kbBullets(p.Content)), p.Path)
	}
	_ = tw.Flush()
	return b.String()
}
