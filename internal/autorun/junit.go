package autorun

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// junitSuite / junitTestCase model the minimal JUnit-XML shape CI
// test-reporters ingest (one <testsuite> of <testcase>s). Only the widely
// supported attributes are emitted.
type junitSuite struct {
	XMLName   xml.Name        `xml:"testsuite"`
	Name      string          `xml:"name,attr"`
	Tests     int             `xml:"tests,attr"`
	Failures  int             `xml:"failures,attr"`
	Skipped   int             `xml:"skipped,attr"`
	Time      string          `xml:"time,attr"`
	Timestamp string          `xml:"timestamp,attr,omitempty"`
	Cases     []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *junitSkipped `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Body    string `xml:",chardata"`
}

type junitSkipped struct {
	Message string `xml:"message,attr,omitempty"`
}

// WriteJUnit writes results as a JUnit-XML report to path (for CI
// test-reporter ingestion): one <testsuite> named after the playbook slug with
// one <testcase> per step. Status mapping: ok → plain pass; failed → <failure>
// (message carries the exit code and, for a hang-kill, the effective timeout;
// the body carries the step command and output path); skipped / cancelled /
// rolledback → <skipped> with the status as its message (they are not
// failures — the failing step itself is the failure). stamp, when parseable as
// the run-log's UTC form, becomes the suite timestamp.
func WriteJUnit(path, slug, stamp string, results []StepResult) error {
	suite := junitSuite{Name: slug, Tests: len(results)}
	if ts, err := time.Parse("20060102T150405Z", stamp); err == nil {
		suite.Timestamp = ts.UTC().Format(time.RFC3339)
	}
	var total time.Duration
	for _, r := range results {
		total += r.Duration
		tc := junitTestCase{
			Name:      r.ID,
			Classname: slug,
			Time:      fmt.Sprintf("%.3f", r.Duration.Seconds()),
		}
		switch r.Status {
		case StatusFailed:
			suite.Failures++
			msg := fmt.Sprintf("exit %d", r.Exit)
			if r.TimedOutAfter != "" {
				msg = fmt.Sprintf("timed out after %s (exit %d)", r.TimedOutAfter, r.Exit)
			}
			body := r.Command
			if r.OutputPath != "" {
				body += "\noutput: " + r.OutputPath
			}
			tc.Failure = &junitFailure{Message: msg, Body: body}
		case StatusOK:
			// plain pass
		default: // skipped / cancelled / rolledback
			suite.Skipped++
			tc.Skipped = &junitSkipped{Message: r.Status}
		}
		suite.Cases = append(suite.Cases, tc)
	}
	suite.Time = fmt.Sprintf("%.3f", total.Seconds())

	data, err := xml.MarshalIndent(suite, "", "  ")
	if err != nil {
		return err
	}
	data = append([]byte(xml.Header), append(data, '\n')...)
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644)
}
