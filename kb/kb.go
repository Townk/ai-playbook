// Package kb is the Go port of the shell knowledge-base reader
// (assist::kb_path / assist::kb_ensure in assist-agent-common.zsh): a per-project
// file of distilled facts the producer folds into the system prompt as the
// "## What we already know about this project" section.
//
// The on-disk layout mirrors the shell exactly so a Go reader and the legacy
// shell writer (the deferred `remember` tool) agree on the path:
//
//	$root/projects/<project_key>/knowledge.md
//
// where project_key = SHA-1 (lowercase hex) of the project-root path string and
// $root is the same data dir the cache uses: AI_PLAYBOOK_DATA_DIR, else
// ${XDG_DATA_HOME:-$HOME/.local/share}/ai-playbook.
//
// Append appends a distilled "- <fact>" line to the per-project knowledge.md
// (the `remember` tool's KB write).
package kb

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// KnowledgeBase is the per-project distilled-facts text, verbatim from the KB
// file. Empty when the project has no KB file or the file is empty.
type KnowledgeBase string

// DefaultRoot resolves the data-dir root: AI_PLAYBOOK_DATA_DIR, else
// ${XDG_DATA_HOME:-$HOME/.local/share}/ai-playbook. It matches cache.DefaultRoot
// so the cache and KB live under the same tree.
func DefaultRoot() string {
	if v := os.Getenv("AI_PLAYBOOK_DATA_DIR"); v != "" {
		return v
	}
	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg == "" {
		home, _ := os.UserHomeDir()
		xdg = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(xdg, "ai-playbook")
}

// projectKey reproduces assist::project_key: the lowercase hex SHA-1 of the
// project-root path string (no trailing newline — `print -rn`).
func projectKey(projectRoot string) string {
	sum := sha1.Sum([]byte(projectRoot))
	return hex.EncodeToString(sum[:])
}

// Path returns the KB file path for projectRoot under the given root, mirroring
// assist::kb_path: $root/projects/<project_key>/knowledge.md.
func Path(root, projectRoot string) string {
	return filepath.Join(root, "projects", projectKey(projectRoot), "knowledge.md")
}

// Load reads the per-project KB for projectRoot from the default data dir and
// returns its contents. Missing or empty file → "" (the shell only folds the KB
// block in when the file is non-empty: `[[ -s "$kb_path" ]]`). Unreadable file
// is treated as empty (best-effort, never fatal — matches the shell never
// crashing on a missing KB). A trailing newline is preserved verbatim, as the
// shell's `cat` would emit it.
func Load(projectRoot string) KnowledgeBase {
	return LoadFrom(DefaultRoot(), projectRoot)
}

// LoadFrom is Load against an explicit root (for tests / non-default data dirs).
func LoadFrom(root, projectRoot string) KnowledgeBase {
	b, err := os.ReadFile(Path(root, projectRoot))
	if err != nil {
		return ""
	}
	return KnowledgeBase(b)
}

// Append ports `ai-assist-remember`: it appends a distilled fact to the
// per-project knowledge base under the default data dir, as a "- <fact>" bullet
// line, creating the projects/<key>/ directory as needed. An empty (after-trim)
// fact is a no-op success (the shell rejected an empty fact; here the wrap-up
// distillation is best-effort, so a blank fact is simply skipped rather than an
// error). The fact's own newlines are flattened to spaces so one fact stays one
// bullet line.
func Append(projectRoot, fact string) error {
	return AppendTo(DefaultRoot(), projectRoot, fact)
}

// AppendTo is Append against an explicit root (for tests / non-default data dirs).
func AppendTo(root, projectRoot, fact string) error {
	fact = strings.TrimSpace(fact)
	if fact == "" {
		return nil
	}
	fact = strings.ReplaceAll(fact, "\n", " ")
	p := Path(root, projectRoot)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString("- " + fact + "\n")
	return err
}
