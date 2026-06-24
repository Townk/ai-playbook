package input

import "strings"

const (
	recUS = "\x1f" // unit separator (between fields)
	recRS = "\x1e" // record separator (terminator)
)

func encodeRecord(cmd string, args ...string) string {
	parts := append([]string{cmd}, args...)
	return strings.Join(parts, recUS) + recRS
}

func decodeRecord(rec string) (string, []string) {
	rec = strings.TrimSuffix(rec, recRS)
	fields := strings.Split(rec, recUS)
	if len(fields) == 1 {
		return fields[0], nil
	}
	return fields[0], fields[1:]
}
