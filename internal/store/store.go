// Package store is an on-demand scanner over the two playbook stores (the
// global store and the project-local store). It owns no database: every call
// resolves the configured store directories, globs their "*.md" files, parses
// each one's front matter, and builds an in-memory []Meta sorted newest-first.
//
// The two stores are kept distinct by a slug convention: global playbooks carry
// a bare slug (their file stem) and project playbooks a "proj:"-prefixed slug.
// Lookups never shadow across stores — a bare slug resolves ONLY in the global
// store and a "proj:" slug ONLY in the project store.
//
// The store directories are resolved through package-level seams (cfg and
// projectRoot) so tests can inject fake directories without an env or a git
// checkout; production wires them to config.Load + capture.ProjectRoot.
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Townk/ai-playbook/internal/capture"
	"github.com/Townk/ai-playbook/internal/config"
	"github.com/Townk/ai-playbook/internal/frontmatter"
)

// projSlugPrefix marks a slug as belonging to the project-local store.
const projSlugPrefix = "proj:"

// createdLayout is the front-matter "created" date format (see
// orchestrator.buildFrontMatter, which writes time.Now().Format("2006-01-02")).
const createdLayout = "2006-01-02"

// Test seams: cfg resolves the merged configuration and projectRoot the current
// project root. Tests override these to point at temporary directories without
// touching the environment or a git checkout.
var (
	cfg         = func() (*config.Config, error) { return config.Load() }
	projectRoot = func() string { return capture.ProjectRoot() }
)

// EnvVar mirrors one front-matter env entry, flattened to include the variable
// name (the map key in the front matter).
type EnvVar struct {
	Name  string
	Value string
	Why   string
}

// Meta is the indexed metadata for one playbook plus the resolved path and the
// store it lives in.
type Meta struct {
	Slug        string
	Name        string
	Description string
	Category    string
	Tags        []string
	Env         []EnvVar
	Workdir     string
	Path        string
	// Project is true when the playbook lives in the project-local store; its
	// Slug is then "proj:"-prefixed.
	Project bool
	Created time.Time
}

// Index scans BOTH store directories and returns their playbook metadata sorted
// newest-first (Created descending). A missing store directory is treated as
// empty, never an error. A file whose front matter fails to parse is skipped and
// logged to stderr, never fatal.
func Index() ([]Meta, error) {
	c, err := cfg()
	if err != nil {
		return nil, err
	}
	var metas []Meta
	metas = append(metas, scanDir(c.GlobalStoreDir(), false)...)
	metas = append(metas, scanDir(c.ProjectStoreDir(projectRoot()), true)...)

	sort.SliceStable(metas, func(i, j int) bool {
		return metas[i].Created.After(metas[j].Created)
	})
	return metas, nil
}

// scanDir globs "*.md" in dir and returns one Meta per parseable file. A missing
// directory yields no entries (and no error). Unparseable files are skipped with
// a stderr note.
func scanDir(dir string, project bool) []Meta {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		// The only error filepath.Glob returns is ErrBadPattern, which our fixed
		// "*.md" pattern can never produce; treat anything here as "empty".
		return nil
	}
	sort.Strings(paths)

	var metas []Meta
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "store: skip %s: %v\n", path, err)
			continue
		}
		fm, _, ok := frontmatter.Parse(string(data))
		if !ok {
			fmt.Fprintf(os.Stderr, "store: skip %s: front matter failed to parse\n", path)
			continue
		}
		metas = append(metas, metaFromFM(fm, path, project))
	}
	return metas
}

// metaFromFM builds a Meta from parsed front matter and the file path. Created is
// taken from the front-matter "created" field (createdLayout); when absent or
// unparseable it falls back to the file's ModTime.
func metaFromFM(fm frontmatter.FrontMatter, path string, project bool) Meta {
	stem := strings.TrimSuffix(filepath.Base(path), ".md")
	slug := stem
	if project {
		slug = projSlugPrefix + stem
	}

	m := Meta{
		Slug:        slug,
		Name:        fm.Name,
		Description: fm.Description,
		Category:    fm.Category,
		Tags:        fm.Tags,
		Env:         envFromFM(fm.Env),
		Workdir:     fm.Workdir,
		Path:        path,
		Project:     project,
		Created:     createdFor(fm.Created, path),
	}
	return m
}

// envFromFM flattens the front-matter env map into a name-sorted []EnvVar for
// stable output.
func envFromFM(env map[string]frontmatter.EnvValue) []EnvVar {
	if len(env) == 0 {
		return nil
	}
	out := make([]EnvVar, 0, len(env))
	for name, ev := range env {
		out = append(out, EnvVar{Name: name, Value: ev.Value, Why: ev.Why})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// createdFor parses the front-matter created field, falling back to the file's
// ModTime when it is absent or unparseable (and to the zero time when even the
// stat fails).
func createdFor(created, path string) time.Time {
	if created != "" {
		if t, err := time.Parse(createdLayout, created); err == nil {
			return t
		}
	}
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

// Load returns the Meta and the markdown body (everything after the front
// matter) for the playbook identified by slug. A "proj:"-prefixed slug resolves
// in the project store; a bare slug resolves ONLY in the global store (no
// shadowing). An unknown slug is an error.
func Load(slug string) (Meta, string, error) {
	path, project, ok := pathFor(slug)
	if !ok {
		return Meta{}, "", fmt.Errorf("store: no playbook for slug %q", slug)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Meta{}, "", fmt.Errorf("store: read %s: %w", path, err)
	}
	fm, body, parsed := frontmatter.Parse(string(data))
	if !parsed {
		return Meta{}, "", fmt.Errorf("store: %s: front matter failed to parse", path)
	}
	return metaFromFM(fm, path, project), body, nil
}

// Search returns the indexed playbooks whose name, description, category, or any
// tag contains query as a case-insensitive substring, in the same newest-first
// order as Index. An empty query matches everything.
func Search(query string) ([]Meta, error) {
	metas, err := Index()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	if q == "" {
		return metas, nil
	}
	var out []Meta
	for _, m := range metas {
		if matches(m, q) {
			out = append(out, m)
		}
	}
	return out, nil
}

// matches reports whether m contains q (already lower-cased) in any searched
// field.
func matches(m Meta, q string) bool {
	if strings.Contains(strings.ToLower(m.Name), q) ||
		strings.Contains(strings.ToLower(m.Description), q) ||
		strings.Contains(strings.ToLower(m.Category), q) {
		return true
	}
	for _, tag := range m.Tags {
		if strings.Contains(strings.ToLower(tag), q) {
			return true
		}
	}
	return false
}

// PathFor returns the resolved file path for slug and whether it exists, using
// the same store-routing rules as Load.
func PathFor(slug string) (string, bool) {
	path, _, ok := pathFor(slug)
	return path, ok
}

// pathFor resolves slug to a file path and reports the store it lives in. A
// "proj:" prefix routes to the project store; a bare slug to the global store.
// ok is false when the resolved file does not exist.
func pathFor(slug string) (path string, project bool, ok bool) {
	c, err := cfg()
	if err != nil {
		return "", false, false
	}
	var dir, stem string
	if rest, found := strings.CutPrefix(slug, projSlugPrefix); found {
		project, dir, stem = true, c.ProjectStoreDir(projectRoot()), rest
	} else {
		project, dir, stem = false, c.GlobalStoreDir(), slug
	}
	path = filepath.Join(dir, stem+".md")
	if _, err := os.Stat(path); err != nil {
		return "", project, false
	}
	return path, project, true
}
