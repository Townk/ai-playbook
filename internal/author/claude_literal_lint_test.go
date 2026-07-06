package author

import (
	"fmt"
	"go/scanner"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// claudeLiteralAllowlist names the ONLY non-test .go sources that may carry a
// "claude" literal (identifier or string, case-insensitive; comments are exempt).
// Everything else is harness-agnostic code, where a claude mention is a LEAK
// (ADR-0012 / the multi-harness spec's leak-regression test): the harness is a
// pluggable detail, so claude specifics belong exclusively to claude's own
// harness/adapter files plus the two skill-install surfaces whose on-disk
// default (~/.claude/skills) is a Claude Code filesystem contract.
var claudeLiteralAllowlist = map[string]string{
	// The claude stream adapter: owns claude's stream-json wire format.
	"internal/agentstream/claude.go": "the claude agentstream adapter",
	// The claude harness: argv, thinking env mapping, mcp-config tool transport,
	// registry row, and the default-harness selection.
	"internal/author/harness_claude.go": "the claude harness implementation",
	// Skill install defaults to ~/.claude/skills (the Claude Code personal
	// skills directory) — a deliberate, documented default.
	"internal/skillcmd/skillcmd.go": "the ~/.claude/skills install default",
	// The skill-install CLI help rows name the same ~/.claude/skills default.
	"internal/climeta/climeta.go": "CLI help naming the ~/.claude/skills default",
}

// TestNoClaudeLiteralsOutsideAdapterFiles is the leak-regression lint (spec
// "Testing" bullet 5): it tokenizes every non-test .go source in the repo
// (comments skipped — prose may explain claude; code may not hardcode it) and
// fails on any token containing "claude" (case-insensitive) outside the
// commented allowlist above. testdata fixtures and _test.go files are exempt.
func TestNoClaudeLiteralsOutsideAdapterFiles(t *testing.T) {
	root := repoRoot(t)

	var hits []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "docs", ".superpowers", ".claude":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)
		if _, ok := claudeLiteralAllowlist[rel]; ok {
			return nil
		}
		for _, hit := range claudeTokens(t, path) {
			hits = append(hits, fmt.Sprintf("%s: %s", rel, hit))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(hits) > 0 {
		t.Errorf("claude literals leaked into harness-agnostic code (add the fix, not an allowlist entry):\n  %s",
			strings.Join(hits, "\n  "))
	}
}

// claudeTokens scans one Go source file and returns a "line:col: token" entry for
// every non-comment token whose text contains "claude" (case-insensitive).
func claudeTokens(t *testing.T, path string) []string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fset := token.NewFileSet()
	file := fset.AddFile(path, fset.Base(), len(src))
	var s scanner.Scanner
	// Mode 0: comments are NOT scanned — prose may mention claude freely.
	s.Init(file, src, nil, 0)

	var out []string
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		text := lit
		if text == "" {
			text = tok.String()
		}
		if strings.Contains(strings.ToLower(text), "claude") {
			p := fset.Position(pos)
			out = append(out, fmt.Sprintf("%d:%d: %s", p.Line, p.Column, text))
		}
	}
	return out
}

// repoRoot walks up from the package directory to the go.mod module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the package directory")
		}
		dir = parent
	}
}
