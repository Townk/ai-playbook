package playbook

import (
	"fmt"
	"strings"
)

// Render turns a structured Playbook into the canonical markdown BODY (no front
// matter — that is assembled at save from Meta). The output satisfies
// ui.ValidatePlaybook: an H1 "# <Title>" + ≥1 fenced block. Blank lines separate
// every element so goldmark parses each as its own block.
func Render(pb Playbook) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", pb.Title)
	writeProse(&b, pb.Intro)

	auto := 0
	for _, sec := range pb.Sections {
		fmt.Fprintf(&b, "\n## %s\n", sec.Heading)
		for _, it := range sec.Content {
			switch it.Kind {
			case "text":
				writeProse(&b, it.Text)
			case "callout":
				if s := strings.TrimSpace(it.Text); s != "" {
					typ := strings.ToUpper(strings.TrimSpace(it.Admonition))
					if typ == "" {
						typ = "NOTE"
					}
					b.WriteString("\n> [!" + typ + "]\n")
					b.WriteString(blockquote(s))
				}
			case "code":
				id := ""
				if !it.Static {
					if it.ID != "" {
						id = it.ID
					} else {
						auto++
						id = fmt.Sprintf("step-%d", auto)
					}
				}
				b.WriteString("\n")
				b.WriteString(fence(it.Lang, id, it.File, it.Needs, it.Rollback, it.Static, it.Code))
			}
		}
	}
	if pb.Verify != nil {
		b.WriteString("\n")
		b.WriteString(fence(pb.Verify.Lang, "verify", "", pb.Verify.Needs, "", false, pb.Verify.Code))
	}
	return b.String()
}

// writeProse appends a blank line + the trimmed prose + a newline (nothing when empty).
func writeProse(b *strings.Builder, s string) {
	if t := strings.TrimSpace(s); t != "" {
		b.WriteString("\n")
		b.WriteString(t)
		b.WriteString("\n")
	}
}

// fence renders one fenced code block with the {…} tag parseFenceInfo expects.
// Static blocks carry only {static}; runnable blocks carry {id=… file=… needs=… rollback=…}.
func fence(lang, id, file string, needs []string, rollback string, static bool, code string) string {
	var tag string
	if static {
		tag = "{static}"
	} else {
		tag = "{id=" + id
		if file != "" {
			tag += " file=" + file
		}
		if len(needs) > 0 {
			tag += " needs=" + strings.Join(needs, ",")
		}
		if rollback != "" {
			tag += " rollback=" + rollback
		}
		tag += "}"
	}
	body := strings.TrimRight(code, "\n")
	f := strings.Repeat("`", fenceLen(body))
	return f + lang + " " + tag + "\n" + body + "\n" + f + "\n"
}

// fenceLen returns the backtick-fence length safe to wrap code in: at least
// 3, and always one longer than the longest run of backticks appearing
// anywhere in code. CommonMark closes a fence at the first line consisting
// solely of a run of the fence char >= the OPENING run's length, so a payload
// containing its own 3+ backtick run (e.g. an embedded markdown/shell
// example) would otherwise close the block early — a fence one longer than
// the payload's longest run can never be matched by anything inside it.
func fenceLen(code string) int {
	longest, run := 0, 0
	for _, r := range code {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	if n := longest + 1; n > 3 {
		return n
	}
	return 3
}

// blockquote prefixes each line of a callout with "> ".
func blockquote(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = "> " + ln
	}
	return strings.Join(lines, "\n") + "\n"
}
