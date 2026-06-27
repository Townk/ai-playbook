package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ai-playbook/internal/author"
	"ai-playbook/internal/config"
	"ai-playbook/internal/driver"
	"ai-playbook/internal/frontmatter"
	"ai-playbook/internal/orchestrator"
)

// finalizeDoc is the testable core of the `finalize` subcommand: it backfills
// front matter onto an existing playbook document, returning the assembled
// `---\n<front matter>\n---\n\n<body>` artifact. It reuses the SAME pieces the
// live commit path uses (orchestrator name-derivation + preamble strip,
// frontmatter.ScanEnvRefs/BuildEnv/Prepend) so the output matches a freshly
// committed playbook.
//
// Idempotent: any leading front matter on raw is parsed off and DROPPED before
// re-assembly, so re-finalizing a file that already carries front matter REPLACES
// it rather than nesting a second block.
//
// The model classification and the ground-truth env lookup are injected as seams
// (metaFn, lookup) so the core is fully deterministic in tests:
//   - metaFn classifies the body into description/category/tags + per-var why. On
//     error it is treated as empty model fields (the front matter still carries
//     name/env/provenance); the error is returned to the caller for logging and
//     NEVER aborts assembly — we must never lose the file.
//   - lookup supplies ground-truth env values (driver-backed in the CLI); a miss
//     omits that var (frontmatter.BuildEnv).
//
// created and projectRoot are the programmatic provenance fields. home anchors
// home-dir → "~" normalization (BuildEnv values + projectRoot) for portability;
// an empty home disables it.
func finalizeDoc(
	raw string,
	metaFn func(body string) (author.Metadata, error),
	lookup func(string) (string, bool),
	created, projectRoot, home string,
) (full string, err error) {
	// (1) Drop any existing front matter (idempotency) then strip preamble above
	// the first H1, so we re-assemble from the literate body only.
	_, body, _ := frontmatter.Parse(raw)
	body = orchestrator.StripPreamble(body)

	// (2) name: same derivation as the commit path (# Playbook — <t> else first H1).
	name := orchestrator.PlaybookName(body)

	// (3) model classification (best-effort): on error, continue with empty model
	// fields but surface the error to the caller for logging.
	var meta author.Metadata
	if metaFn != nil {
		m, mErr := metaFn(body)
		if mErr != nil {
			err = mErr
		} else {
			meta = m
		}
	}

	// (4) env (spec §C): union of body-referenced vars and the model's
	// importantEnvVars, values filled + redacted from the injected lookup.
	notes := make(map[string]string, len(meta.ImportantEnvVars))
	for _, ev := range meta.ImportantEnvVars {
		if ev.Name != "" {
			notes[ev.Name] = ev.Why
		}
	}
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	env := frontmatter.BuildEnv(frontmatter.ScanEnvRefs(body), notes, lookup, home)

	fm := frontmatter.FrontMatter{
		Name:        name,
		Description: meta.Description,
		Category:    meta.Category,
		Tags:        meta.Tags,
		Env:         env,
		Created:     created,
		ProjectRoot: frontmatter.NormalizeHome(projectRoot, home),
	}
	return frontmatter.Prepend(fm, body), err
}

// finalize is the `ai-playbook finalize [--dry-run] <file.md>` subcommand: it
// reads an EXISTING playbook .md, generates its front matter (idempotently —
// re-finalizing one that already has front matter replaces it), and rewrites the
// file in place. It is the manual entry point to the front-matter feature (the
// spec's deferred "manual run <file> finalization").
//
// The model call (author.PlaybookMetadata) and the ground-truth env lookup (a
// driver dumping the current shell's env) are wired here and passed to the
// testable finalizeDoc core. A metadata-classification failure is a WARNING, not
// a failure — the file is still written with a metadata-less front matter so the
// playbook is never lost. A genuinely unreadable/unwritable file exits non-zero.
//
// Cache caveat: finalize operates on the FILE only — it does not re-key or update
// any cache entry. It backfills the saved .md artifact in place.
func finalize() int {
	fs := flag.NewFlagSet("finalize", flag.ExitOnError)
	var dryRun bool
	fs.BoolVar(&dryRun, "dry-run", false, "print the assembled front matter block to stdout; do not write the file")
	fs.Parse(os.Args[2:])

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: ai-playbook finalize [--dry-run] <file.md>")
		return 2
	}
	file := fs.Arg(0)

	raw, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook finalize: read %s: %v\n", file, err)
		return 1
	}

	cfg, _ := config.Load()
	metaFn := func(body string) (author.Metadata, error) {
		return author.PlaybookMetadata(body, author.AuthorOptions{Cfg: cfg})
	}

	// Ground-truth env lookup: open a driver in the CURRENT cwd, dump `env`, parse
	// it into a map, and return a lookup closure. Nil-safe: if the driver fails to
	// open we proceed with no env values (a warning), never aborting the finalize.
	lookup, closeDrv := driverEnvLookup()
	defer closeDrv()

	created := time.Now().Format("2006-01-02")
	projectRoot, _ := os.Getwd()
	// home anchors home-dir → "~" normalization; an os.UserHomeDir error → "" → no
	// normalization (still safe).
	home, _ := os.UserHomeDir()

	full, metaErr := finalizeDoc(string(raw), metaFn, lookup, created, projectRoot, home)
	if metaErr != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook finalize: metadata classification failed (%v); writing front matter without model fields\n", metaErr)
	}

	// --dry-run: print the assembled front-matter block (the part above the body)
	// to stdout and do NOT write.
	if dryRun {
		fmt.Print(frontMatterBlock(full))
		return 0
	}

	if err := atomicWrite(file, []byte(full)); err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook finalize: write %s: %v\n", file, err)
		return 1
	}

	fmt.Println(finalizeSummary(file, full))
	return 0
}

// driverEnvLookup opens a shell driver in the current cwd, dumps its environment
// once, and returns a lookup closure plus a close func. It mirrors main.go's
// buildEnvLookup approach (dump `env`, parse KEY=VALUE). On a driver-open failure
// it prints a warning and returns an always-miss lookup with a no-op close, so the
// caller proceeds with no env values rather than aborting.
func driverEnvLookup() (lookup func(string) (string, bool), closeFn func()) {
	miss := func(string) (string, bool) { return "", false }
	d, err := driver.Open(driver.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ai-playbook finalize: driver.Open failed (%v); finalizing without env values\n", err)
		return miss, func() {}
	}
	envm := map[string]string{}
	res := d.Run("env", defaultEnvDumpTimeout)
	if res.Exit == 0 {
		for _, line := range strings.Split(res.Out, "\n") {
			if i := strings.IndexByte(line, '='); i > 0 {
				envm[line[:i]] = line[i+1:]
			}
		}
	}
	return func(name string) (string, bool) {
			v, ok := envm[name]
			return v, ok
		}, func() {
			d.Close()
		}
}

// frontMatterBlock returns the leading "---\n…\n---\n" front-matter block of a
// finalized artifact (everything up to and including the closing fence), for
// --dry-run. A document without a leading fence yields "".
func frontMatterBlock(full string) string {
	const fence = "---\n"
	if !strings.HasPrefix(full, fence) {
		return ""
	}
	rest := full[len(fence):]
	if end := strings.Index(rest, "\n"+fence); end >= 0 {
		return full[:len(fence)+end+len("\n"+fence)]
	}
	return full
}

// finalizeSummary builds the one-line summary printed after a successful write:
// `finalized <file> — name=… category=… tags=[…] env=N vars`. It reads the values
// back from the assembled artifact's front matter so the summary reflects exactly
// what was written.
func finalizeSummary(file, full string) string {
	fm, _, _ := frontmatter.Parse(full)
	tags := append([]string(nil), fm.Tags...)
	sort.Strings(tags)
	return fmt.Sprintf("finalized %s — name=%q category=%q tags=[%s] env=%d vars",
		file, fm.Name, fm.Category, strings.Join(tags, ","), len(fm.Env))
}

// atomicWrite writes data to path atomically: a temp file in the SAME directory
// (so rename is atomic on the same filesystem) followed by an os.Rename over the
// target. The temp file is removed on any failure before the rename.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".aapb-finalize-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
