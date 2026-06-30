package author

import (
	"strings"
	"testing"
)

func TestDriftRegenPrompt_NamesFileAndStalePatch(t *testing.T) {
	sys, user := DriftRegenPrompt("package main\n\nfunc main() {}\n", "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n+new\n")
	all := sys + "\n" + user
	for _, want := range []string{"no longer applies", "unified diff", "package main", "+new"} {
		if !strings.Contains(all, want) {
			t.Errorf("drift-regen prompt missing %q", want)
		}
	}
}
