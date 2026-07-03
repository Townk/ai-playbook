// drift_test.go — the drift guard: for each user-facing command with its own
// flag.FlagSet, every flag name it actually parses must be registered here as
// a climeta.Flag. resolveRunArgs/resolveValidateArgs/resolveEnvArgs and the
// ListMain/SearchMain flagsets live in internal/launcher and are unexported,
// so their flag names are mirrored here by hand — each entry's comment points
// at the exact fs.*Var call site to keep this from drifting silently. If this
// test ever needs updating alongside a flagset change, that IS the point: it
// forces the registry update in the same commit.
//
// Internal/plumbing commands (session, answer, mcp, diff, input, finalize,
// selftest, version) are exempt — Commands' doc comment already notes input's
// flags are deliberately partial.
package climeta

import "testing"

// expectedFlags is the ground truth for each user-facing command's ACTUAL
// flag.FlagSet, kept in sync by hand with the fs.*Var call sites named below.
var expectedFlags = map[string][]string{
	// internal/launcher/runcmd.go, resolveRunArgs
	"run": {"playbook", "file", "auto-rollback", "auto", "no-auto-rollback", "assisted", "with-env"},
	// internal/launcher/validatecmd.go, resolveValidateArgs
	"validate": {"file", "no-ai", "plain", "quiet"},
	// internal/launcher/envcmd.go, resolveEnvArgs
	"env": {"file"},
	// internal/launcher/storecmd.go, ListMain
	"list": {"format"},
	// internal/launcher/storecmd.go, SearchMain
	"search": {"format"},
}

// TestDrift_RegistryCoversEveryParsedFlag asserts, for each command in
// expectedFlags, that every flag name it actually parses has a matching
// climeta.Flag entry in Commands. Fails loudly (naming the missing flag) so
// an added-but-undocumented flag cannot ship silently.
func TestDrift_RegistryCoversEveryParsedFlag(t *testing.T) {
	for name, flags := range expectedFlags {
		cmd, ok := Lookup(name)
		if !ok {
			t.Errorf("expectedFlags references unregistered command %q", name)
			continue
		}
		registered := make(map[string]bool, len(cmd.Flags))
		for _, f := range cmd.Flags {
			registered[f.Name] = true
		}
		for _, flagName := range flags {
			if !registered[flagName] {
				t.Errorf("climeta.Commands[%q] is missing a Flag for --%s (parsed by the real flagset but undocumented)", name, flagName)
			}
		}
	}
}
