// envcmd.go — the `ai-playbook env` subcommand entrypoint.
//
// `env` accepts a single playbook source, expressed one of two ways:
//
//   - env <slug>            a bare positional ⇒ a saved playbook, resolved
//     through the store
//   - env --file <path>     a raw markdown file, read as-is
//
// Exactly one source must be given; zero or more than one is a usage error.
//
// It parses the playbook's front matter and prints the declared `env:` map as
// a --with-env-compatible JSON object on stdout, resolving each value against
// the current process environment (falling back to the declared default) and
// redacting sensitive values to "" — both secret-shaped values (via
// frontmatter.Redact) and build-time-masked defaults whose corresponding env
// var is unset (via frontmatter.IsRedactedMask).
//
// A playbook that declares depends_on gets the UNION of its own declared vars
// and every dependency's, transitively (resolveChain, mirroring the run
// chain): the parent's declaration wins on a name collision (envUnionChain).
// A dangling or cyclic depends_on chain is fatal — it prints the same
// structural diagnostics as `run` (printDepIssues) and exits 2 before any
// output is produced.
package launcher

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Townk/ai-playbook/internal/frontmatter"
)

// envArgs is resolveEnvArgs's parsed result: the single playbook source.
type envArgs struct {
	Kind, Value string // "file" | "playbook"
}

// resolveEnvArgs resolves the single playbook source from the `env` arguments —
// exactly one of {bare positional, --file}, mirroring resolveValidateArgs.
func resolveEnvArgs(args []string) (envArgs, error) {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	kind, value, err := resolveSource(fs, args, "env", false)
	if err != nil {
		return envArgs{}, err
	}
	return envArgs{Kind: kind, Value: value}, nil
}

// resolveEnvJSON resolves each declared var against getenv (env value when set,
// else the declared default) and redacts sensitive ones to "". Returns the
// name→value map and the sorted names of the redacted vars. Pure — getenv is
// injected so tests never touch the process environment.
//
// A var is redacted when its resolved value is sensitive (frontmatter.Redact:
// secret-looking name or high-entropy value) OR its DECLARED default is already
// the <redacted> mask — the latter means the value was masked at build time, so
// the var stays masked here even if the current environment supplies a benign
// override (once sensitive, always redacted; a safe default for a secrets dump).
func resolveEnvJSON(vars map[string]frontmatter.EnvValue, getenv func(string) string) (map[string]string, []string) {
	out := make(map[string]string, len(vars))
	var redacted []string
	for name, ev := range vars {
		declaredMasked := frontmatter.IsRedactedMask(ev.Value)
		raw := ev.Value
		if v := getenv(name); v != "" {
			raw = v
		}
		if _, isRedacted := frontmatter.Redact(name, raw); isRedacted || declaredMasked {
			out[name] = ""
			redacted = append(redacted, name)
			continue
		}
		out[name] = raw
	}
	sort.Strings(redacted)
	return out, redacted
}

// envUnionChain builds the union of declared env vars for `env`'s chain-aware
// output: the parent's parentEnv wins on a name collision (env shows the
// value a human running the parent would see), and each dependency in deps
// (the resolver's order) contributes any name not already present. Unlike
// unionDeclared (deps.go, dep-wins — it only feeds the --with-env undeclared
// check, where "known to any node in the chain" is all that matters), env
// must reflect what actually resolves, so the parent's own declaration always
// takes precedence.
func envUnionChain(parentEnv map[string]frontmatter.EnvValue, deps []depNode) map[string]frontmatter.EnvValue {
	union := make(map[string]frontmatter.EnvValue, len(parentEnv))
	for name, ev := range parentEnv {
		union[name] = ev
	}
	for _, dep := range deps {
		for name, ev := range dep.FM.Env {
			if _, exists := union[name]; !exists {
				union[name] = ev
			}
		}
	}
	return union
}

// EnvMain implements `ai-playbook env <source>`: it resolves a playbook's
// declared env: map against the current environment and prints it as a
// --with-env-compatible JSON object on stdout (sensitive values emitted empty and
// listed on stderr). Source-resolution errors return exit 2, mirroring validate.
func EnvMain() int {
	ra, err := resolveEnvArgs(os.Args[2:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook env: %v\n", err)
		return 2
	}

	var content string
	switch ra.Kind {
	case "file":
		data, rerr := os.ReadFile(ra.Value)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook env: %v\n", rerr)
			return 2
		}
		content = string(data)
	case "playbook":
		meta, _, lerr := storeLoadFn(ra.Value)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook env: %v\n", lerr)
			return 2
		}
		// Read the full file (store.Load's body is front-matter-stripped, so
		// re-parsing it would yield an empty FrontMatter — no env, no depends_on).
		data, rerr := os.ReadFile(meta.Path)
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "ai-playbook env: %v\n", rerr)
			return 2
		}
		content = string(data)
	}

	fm, _, _ := frontmatter.Parse(content)

	vars := fm.Env
	if len(fm.DependsOn) > 0 {
		order, issues := resolveChain(fm.DependsOn)
		if len(issues) > 0 {
			printDepIssues(os.Stderr, issues)
			return 2
		}
		vars = envUnionChain(fm.Env, order)
	}

	out, redacted := resolveEnvJSON(vars, os.Getenv)

	data, merr := json.MarshalIndent(out, "", "  ")
	if merr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook env: %v\n", merr)
		return 1
	}
	fmt.Fprintln(os.Stdout, string(data))
	if len(redacted) > 0 {
		fmt.Fprintf(os.Stderr, "env: redacted %d sensitive variable(s): %s\n", len(redacted), strings.Join(redacted, ", "))
	}
	return 0
}
