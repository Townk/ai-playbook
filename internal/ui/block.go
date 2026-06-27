package ui

import (
	"fmt"
	"strings"
)

type Block struct {
	ID      string
	Type    string // "shell" | "run" | "diff" | "static"
	Lang    string
	Needs   []string
	Static  bool
	Payload string
}

// parseFenceInfo splits a fence info string "<lang> {k=v flag …}" into the lang
// word, key=value attrs, and bare flags. Braces are optional.
func parseFenceInfo(info string) (string, map[string]string, map[string]bool) {
	attrs := map[string]string{}
	flags := map[string]bool{}
	info = strings.TrimSpace(info)
	lang := info
	rest := ""
	if sp := strings.IndexByte(info, ' '); sp >= 0 {
		lang, rest = info[:sp], info[sp+1:]
	}
	rest = strings.TrimSpace(rest)
	rest = strings.TrimPrefix(rest, "{")
	rest = strings.TrimSuffix(rest, "}")
	for _, tok := range strings.Fields(rest) {
		if eq := strings.IndexByte(tok, '='); eq >= 0 {
			attrs[tok[:eq]] = tok[eq+1:]
		} else {
			flags[tok] = true
		}
	}
	return lang, attrs, flags
}

func nonExecLang(lang string) bool {
	switch lang {
	case "", "text", "console", "output", "log", "json":
		return true
	}
	return false
}

func classifyType(lang string, static bool) string {
	if static || nonExecLang(lang) {
		return "static"
	}
	switch lang {
	case "bash", "sh", "zsh":
		return "shell"
	case "diff", "patch":
		return "diff"
	default:
		return "run" // python, node, ruby, …
	}
}

func assignIDs(blocks []Block) []Block {
	used := map[string]bool{}
	for _, b := range blocks {
		if b.ID != "" {
			used[b.ID] = true
		}
	}
	n := 0
	for i := range blocks {
		if blocks[i].ID == "" {
			n++
			for used[fmt.Sprintf("b%d", n)] {
				n++
			}
			blocks[i].ID = fmt.Sprintf("b%d", n)
			used[blocks[i].ID] = true
		}
	}
	return blocks
}

// validateNeeds errors on a need referencing an unknown id, or a dependency cycle.
func validateNeeds(blocks []Block) error {
	ids := map[string]bool{}
	for _, b := range blocks {
		ids[b.ID] = true
	}
	deps := map[string][]string{}
	for _, b := range blocks {
		for _, n := range b.Needs {
			if !ids[n] {
				return fmt.Errorf("block %q needs unknown id %q", b.ID, n)
			}
			deps[b.ID] = append(deps[b.ID], n)
		}
	}
	state := map[string]int{} // 0 unvisited, 1 in-progress, 2 done
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("dependency cycle at %q", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, d := range deps[id] {
			if err := visit(d); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, b := range blocks {
		if err := visit(b.ID); err != nil {
			return err
		}
	}
	return nil
}
