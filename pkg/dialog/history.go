package dialog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
)

// LoadHistory reads a JSONL request-history file: one JSON-encoded string per
// line, oldest first and newest last. A missing file yields an empty slice with
// no error. Lines that fail to decode (or are blank) are skipped, never fatal.
func LoadHistory(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var entries []string
	sc := bufio.NewScanner(f)
	// Allow long lines (multi-line entries encode to a single long JSON string).
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var s string
		if err := json.Unmarshal(line, &s); err != nil {
			continue
		}
		entries = append(entries, s)
	}
	return entries
}

// AppendHistory records entry as the newest item in the JSONL history at path.
//
// It is a no-op (returns nil) when entry is empty or equal to the current last
// entry (consecutive duplicates are skipped). Otherwise entry is appended; when
// cap > 0 and the total exceeds cap, only the last cap entries are kept (oldest
// dropped). cap <= 0 means no cap.
//
// The file is rewritten atomically: a temp sibling (path+".tmp", 0600) is
// written then renamed over path. The parent directory is created if missing.
func AppendHistory(path, entry string, cap int) error {
	if entry == "" {
		return nil
	}

	entries := LoadHistory(path)
	if n := len(entries); n > 0 && entries[n-1] == entry {
		return nil
	}
	entries = append(entries, entry)
	if cap > 0 && len(entries) > cap {
		entries = entries[len(entries)-cap:]
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := w.Flush(); err != nil {
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
