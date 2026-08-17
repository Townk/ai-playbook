// Command docgen generates the man pages and zsh completions for the CLI
// this repo ships: ai-playbook, from the internal/climeta registry —
// docs/man/ai-playbook.1 (the overview page) plus one
// docs/man/ai-playbook-<cmd>.1 per climeta.DocumentedCommands entry, and
// completions/_ai-playbook. (The standalone `ask` dialog binary and its
// docs retired with the troupe consolidation — `troupe` ships its own.)
//
// It takes an optional man-page output-directory argument (default:
// "docs/man", relative to the current working directory — run it from the
// repo root, or via `make docs`); completion scripts are always written to
// "completions/", relative to the current working directory. Output is
// deterministic: re-running docgen against unchanged registries never
// changes the generated files (see internal/climeta/man.go and
// internal/climeta/zsh.go).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Townk/ai-playbook/internal/climeta"
)

func main() {
	outDir := "docs/man"
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}

	if err := run(outDir); err != nil {
		fmt.Fprintln(os.Stderr, "docgen:", err)
		os.Exit(1)
	}
}

func run(outDir string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	if err := writePage(outDir, "ai-playbook.1", climeta.ManOverview()); err != nil {
		return err
	}

	for _, cmd := range climeta.DocumentedCommands() {
		name := fmt.Sprintf("ai-playbook-%s.1", cmd.Name)
		if err := writePage(outDir, name, climeta.Man(cmd)); err != nil {
			return err
		}
	}

	if err := writeCompletion("_ai-playbook", climeta.Zsh()); err != nil {
		return err
	}

	return nil
}

// writeCompletion writes a zsh completion script (content) to
// completions/<name>, relative to the current working directory.
func writeCompletion(name, content string) error {
	const compDir = "completions"
	if err := os.MkdirAll(compDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(compDir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writePage(outDir, name, content string) error {
	path := filepath.Join(outDir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
