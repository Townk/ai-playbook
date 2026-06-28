package playbook

import (
	"errors"
	"fmt"
	"strings"
)

// Validate checks the semantic rules the JSON schema cannot express. It returns
// nil when valid, else one error joining every violation (so a re-submitting
// model sees all problems at once). requireVerify demands a top-level Verify
// (set for a troubleshooting/fix playbook; create passes false).
func Validate(pb Playbook, requireVerify bool) error {
	var errs []string
	if strings.TrimSpace(pb.Title) == "" {
		errs = append(errs, "title is required")
	}
	runnable := 0
	seen := map[string]bool{}
	if pb.Verify != nil {
		seen["verify"] = true
	}
	for si, sec := range pb.Sections {
		for ci, it := range sec.Content {
			switch it.Kind {
			case "text", "callout":
				// prose: nothing structural to check
			case "code":
				if strings.TrimSpace(it.Lang) == "" {
					errs = append(errs, fmt.Sprintf("section %d content %d: code block requires a lang", si, ci))
				}
				if !it.Static {
					runnable++
					if it.ID != "" {
						if seen[it.ID] {
							errs = append(errs, fmt.Sprintf("duplicate id %q", it.ID))
						}
						seen[it.ID] = true
					}
				}
			default:
				errs = append(errs, fmt.Sprintf("section %d content %d: unknown kind %q (want text|callout|code)", si, ci, it.Kind))
			}
		}
	}
	if pb.Verify != nil && strings.TrimSpace(pb.Verify.Lang) == "" {
		errs = append(errs, "verify command requires a lang")
	}
	if runnable == 0 {
		errs = append(errs, "at least one runnable (non-static) code block is required")
	}
	if requireVerify && pb.Verify == nil {
		errs = append(errs, "a top-level verify command is required for this playbook")
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.New("invalid playbook: " + strings.Join(errs, "; "))
}
