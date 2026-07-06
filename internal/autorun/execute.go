package autorun

import (
	"fmt"
	"io"
	"time"

	"github.com/Townk/ai-playbook/internal/runlog"
)

// Step is a unit handed to a StepRunner.
type Step struct {
	ID, Command string
	Lang        string        // fence language; a script block's runner assembles its interpreter invocation from this
	From        string        // id of the from= producer whose retained stdout feeds this step's stdin; "" if none
	Timeout     time.Duration // declared timeout= run ceiling; zero when undeclared (the orchestrator's default applies)
	Kind        StepKind
}

// StepRunner executes one step and returns its exit + captured output path +
// the formatted effective ceiling when the step was killed at its timeout
// ("" for every other outcome) + whether the step was aborted by an interrupt
// signal (as opposed to merely exiting non-zero on its own).
type StepRunner interface {
	RunStep(s Step) (exit int, outputPath, timedOutAfter string, cancelled bool)
}

// Config drives Execute. Out receives the streamed per-step headers + final summary.
type Config struct {
	Blocks       []Block
	AutoRollback bool
	Out          io.Writer
	LogDir       string // cache.DefaultRoot(); "" skips the log file
	Stamp        string // timestamp for the log filename
	Slug         string
	// Journal, when non-nil, receives every step result (incl. rollback
	// re-records) and the run finalize — the durable per-playbook run journal
	// (internal/runlog). nil = journaling off. Journal writes are advisory:
	// they can never fail the run.
	Journal *runlog.Journal
	// Preseed is the `--retry` pre-seed (RunConfig.RetrySeed): each listed
	// step starts StatusOK — skipped with a "↷ skipped (previous run)" line,
	// its needs= edges met — and its previous record is installed into the
	// journal skeleton so the first real result persists the complete run
	// (previous_run: true, previous duration). nil = a fresh run.
	Preseed map[string]runlog.BlockRecord
}

// Execute runs the forward loop over an injected runner and returns the process exit
// code: 0 iff every step ran ok; else the failed step's exit code (min 1).
func Execute(cfg Config, r StepRunner) int {
	status := map[string]string{}
	var results []StepResult
	failedExit := 0
	wasCancelled := false

	// `--retry` pre-seed: seeded steps are already done (previous run) — mark
	// them StatusOK so NextRunnable resumes at the first non-ok step in the
	// EXISTING order (demoted producers are simply absent here, so they re-run
	// via the same ordering), report each skip, and install the previous
	// records into the lazy journal skeleton (no write until a real result).
	if len(cfg.Preseed) > 0 {
		cfg.Journal.Preseed(cfg.Preseed)
		for _, b := range Sequence(cfg.Blocks) {
			if _, ok := cfg.Preseed[b.ID]; !ok {
				continue
			}
			status[b.ID] = StatusOK
			fmt.Fprintf(cfg.Out, "[%s] ↷ skipped (previous run)\n\n", b.ID)
			results = append(results, StepResult{ID: b.ID, Command: b.Command, Status: StatusSkipped})
		}
	}

	for {
		b, ok := NextRunnable(cfg.Blocks, status)
		if !ok {
			break
		}

		fmt.Fprintf(cfg.Out, "[%s] %s\n", b.ID, b.Command)
		stepStart := time.Now()
		exit, out, timedOutAfter, cancelled := r.RunStep(Step{ID: b.ID, Command: b.Command, Lang: b.Lang, From: b.From, Timeout: b.Timeout, Kind: b.Kind})
		stepDur := time.Since(stepStart)
		fmt.Fprintln(cfg.Out)

		st := statusFor(exit)
		if cancelled {
			st = StatusCancelled
		}
		results = append(results, StepResult{
			ID:            b.ID,
			Command:       b.Command,
			TimedOutAfter: timedOutAfter,
			Exit:          exit,
			Status:        st,
			OutputPath:    out,
			Duration:      stepDur,
		})
		cfg.Journal.Record(b.ID, runlog.BlockRecord{
			Outcome:       journalOutcome(exit, cancelled),
			Exit:          exit,
			Duration:      stepDur,
			TimedOutAfter: timedOutAfter,
		})

		if cancelled {
			status[b.ID] = StatusFailed
			failedExit = exit
			wasCancelled = true
			break
		}

		if exit == 0 {
			status[b.ID] = StatusOK
		} else {
			status[b.ID] = StatusFailed
			failedExit = exit
			break
		}
	}

	if failedExit != 0 && cfg.AutoRollback && !wasCancelled {
		pairs := RollbackPairs(cfg.Blocks, status)
		for _, pair := range pairs {
			origin, target := pair[0], pair[1]
			command := commandFor(cfg.Blocks, target)
			fmt.Fprintf(cfg.Out, "[%s] %s\n", target, command)
			stepStart := time.Now()
			exit, out, timedOutAfter, cancelled := r.RunStep(Step{ID: target, Command: command, Lang: langFor(cfg.Blocks, target), Timeout: timeoutFor(cfg.Blocks, target), Kind: KindRun})
			stepDur := time.Since(stepStart)
			fmt.Fprintln(cfg.Out)

			status[origin] = StatusRolledBack
			for i := range results {
				if results[i].ID == origin {
					results[i].Status = StatusRolledBack
					break
				}
			}
			results = append(results, StepResult{
				ID:            target,
				Command:       command,
				TimedOutAfter: timedOutAfter,
				Exit:          exit,
				Status:        statusFor(exit),
				OutputPath:    out,
				Duration:      stepDur,
			})
			// The undone origin is re-recorded rolled-back (NOT ok — a retry
			// re-runs it); the rollback TARGET's own execution is a normal
			// record, exactly like the viewer's rollback chain — including a
			// user-interrupted target journaling "stopped", same as the
			// forward loop.
			cfg.Journal.MarkRolledBack(origin)
			cfg.Journal.Record(target, runlog.BlockRecord{
				Outcome:       journalOutcome(exit, cancelled),
				Exit:          exit,
				Duration:      stepDur,
				TimedOutAfter: timedOutAfter,
			})
		}
	}

	cfg.Journal.Finalize()
	if cfg.LogDir != "" {
		_, _ = WriteRunLog(cfg.LogDir, cfg.Stamp, cfg.Slug, results)
	}
	fmt.Fprintln(cfg.Out)
	fmt.Fprint(cfg.Out, Summarize(results))

	if failedExit == 0 {
		return 0
	}
	if failedExit < 1 {
		return 1
	}
	return failedExit
}

// statusFor maps a step's exit code to its StepResult status.
func statusFor(exit int) string {
	if exit == 0 {
		return StatusOK
	}
	return StatusFailed
}

// journalOutcome maps a step's exit/cancelled pair onto the journal's block
// outcome vocabulary: a user-interrupted step is "stopped" (it resumes like a
// failure, but is not one), else ok/failed by exit.
func journalOutcome(exit int, cancelled bool) string {
	switch {
	case cancelled:
		return runlog.OutcomeStopped
	case exit == 0:
		return runlog.OutcomeOK
	default:
		return runlog.OutcomeFailed
	}
}

// commandFor looks up a block's command by id.
func commandFor(blocks []Block, id string) string {
	for _, b := range blocks {
		if b.ID == id {
			return b.Command
		}
	}
	return ""
}

// langFor looks up a block's fence language by id, so a rollback target runs
// through the same canonical payload assembly (interpreter for a script block)
// as a forward step. Empty when the id is unknown or the block has no language.
func langFor(blocks []Block, id string) string {
	for _, b := range blocks {
		if b.ID == id {
			return b.Lang
		}
	}
	return ""
}

// timeoutFor looks up a block's declared timeout= ceiling by id, so a rollback
// target runs under its OWN declared ceiling like a forward step. Zero when
// the id is unknown or the block declares none (the orchestrator's default
// applies).
func timeoutFor(blocks []Block, id string) time.Duration {
	for _, b := range blocks {
		if b.ID == id {
			return b.Timeout
		}
	}
	return 0
}
