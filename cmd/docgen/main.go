// Command docgen generates ai-playbook's man pages from the climeta
// registry: docs/man/ai-playbook.1 (the overview page) plus one
// docs/man/ai-playbook-<cmd>.1 per climeta.DocumentedCommands entry.
//
// It takes an optional output-directory argument (default: "docs/man",
// relative to the current working directory — run it from the repo root, or
// via `make docs` / `go generate ./internal/climeta`). Output is
// deterministic: re-running docgen against an unchanged registry never
// changes the generated files (see internal/climeta/man.go).
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

	return nil
}

func writePage(outDir, name, content string) error {
	path := filepath.Join(outDir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
