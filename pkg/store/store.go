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
// The store performs no configuration lookup of its own: callers resolve the
// two store directories (from configuration or otherwise) and pass them in as
// a Dirs value, so the package stays free of config/environment concerns and
// tests drive it with plain temporary directories.
//
// Public API; pre-1.0, minor versions may still reshape it — see ADR-0009.
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Townk/ai-playbook/pkg/playbook/frontmatter"
)

// projSlugPrefix marks a slug as belonging to the project-local store.
const projSlugPrefix = "proj:"

// createdLayout is the front-matter "created" date format (see
// orchestrator.buildFrontMatter, which writes time.Now().Format("2006-01-02")).
const createdLayout = "2006-01-02"

// Dirs is the store's explicit configuration surface: the two directories the
// scanner operates on. Callers resolve them — ai-playbook wires the merged
// configuration's GlobalStoreDir/ProjectStoreDir — and pass them in; the store
// itself never reads configuration, the environment, or a git checkout. Either
// directory may be empty or missing; it then contributes no playbooks.
type Dirs struct {
	// Global is the global store directory (bare slugs).
	Global string
	// Project is the project-local store directory ("proj:"-prefixed slugs).
	Project string
}

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
	Slug         string
	Name         string
	Description  string
	Category     string
	Tags         []string
	DependsOn    []string
	Env          []EnvVar
	ProjectBound bool
	Path         string
	// Project is true when the playbook lives in the project-local store; its
	// Slug is then "proj:"-prefixed.
	Project bool
	Created time.Time
}

// Index scans BOTH store directories and returns their playbook metadata sorted
// newest-first (Created descending). A missing store directory is treated as
// empty, never an error. A file whose front matter fails to parse is skipped and
// logged to stderr, never fatal.
func (d Dirs) Index() []Meta {
	var metas []Meta
	metas = append(metas, scanDir(d.Global, false)...)
	metas = append(metas, scanDir(d.Project, true)...)

	sort.SliceStable(metas, func(i, j int) bool {
		return metas[i].Created.After(metas[j].Created)
	})
	return metas
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
		Slug:         slug,
		Name:         fm.Name,
		Description:  fm.Description,
		Category:     fm.Category,
		Tags:         fm.Tags,
		DependsOn:    fm.DependsOn,
		Env:          envFromFM(fm.Env),
		ProjectBound: fm.ProjectBound,
		Path:         path,
		Project:      project,
		Created:      createdFor(fm.Created, path),
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
func (d Dirs) Load(slug string) (Meta, string, error) {
	path, project, ok := d.pathFor(slug)
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
func (d Dirs) Search(query string) []Meta {
	metas := d.Index()
	q := strings.ToLower(query)
	if q == "" {
		return metas
	}
	var out []Meta
	for _, m := range metas {
		if matches(m, q) {
			out = append(out, m)
		}
	}
	return out
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
func (d Dirs) PathFor(slug string) (string, bool) {
	path, _, ok := d.pathFor(slug)
	return path, ok
}

// pathFor resolves slug to a file path and reports the store it lives in. A
// "proj:" prefix routes to the project store; a bare slug to the global store.
// ok is false when the resolved file does not exist.
func (d Dirs) pathFor(slug string) (path string, project bool, ok bool) {
	var dir, stem string
	if rest, found := strings.CutPrefix(slug, projSlugPrefix); found {
		project, dir, stem = true, d.Project, rest
	} else {
		project, dir, stem = false, d.Global, slug
	}
	path = filepath.Join(dir, stem+".md")
	if _, err := os.Stat(path); err != nil {
		return "", project, false
	}
	return path, project, true
}
