package ui

import (
	"sort"

	"github.com/Townk/ai-playbook/internal/frontmatter"
)

// groupSizes returns the per-dialog variable counts for n variables: ceil(n/5)
// balanced groups, each ≤5, filled at size ceil(n/groups). n<=0 → nil.
// e.g. 6→[3,3], 13→[5,5,3], 12→[4,4,4].
func groupSizes(n int) []int {
	if n <= 0 {
		return nil
	}
	groups := (n + 4) / 5             // ceil(n/5)
	size := (n + groups - 1) / groups // ceil(n/groups)
	var sizes []int
	for n > 0 {
		s := size
		if s > n {
			s = n
		}
		sizes = append(sizes, s)
		n -= s
	}
	return sizes
}

// confirmVar is one variable shown in the confirmation gate.
type confirmVar struct {
	Name  string
	Value string
	Why   string
}

// buildConfirmVars builds the gate's variable list from the declared front-matter env,
// the heuristic project root, and a getenv func (injected for tests). PROJECT_ROOT takes
// the project root; every other var takes its live shell value (empty if unset). Sorted
// by name for stable dialog ordering.
func buildConfirmVars(env map[string]frontmatter.EnvValue, projectRoot string, getenv func(string) string) []confirmVar {
	out := make([]confirmVar, 0, len(env))
	for name, ev := range env {
		val := getenv(name)
		if name == "PROJECT_ROOT" {
			val = projectRoot
		}
		out = append(out, confirmVar{Name: name, Value: val, Why: ev.Why})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
