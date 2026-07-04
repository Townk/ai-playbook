package dialog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestHistoryRoundTripMultiline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request-history.jsonl")
	want := []string{"first", "line1\nline2", "third"}
	for _, e := range want {
		if err := AppendHistory(path, e, 0); err != nil {
			t.Fatalf("AppendHistory(%q): %v", e, err)
		}
	}
	got := LoadHistory(path)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadHistory = %#v, want %#v", got, want)
	}
}

func TestLoadHistoryMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.jsonl")
	if got := LoadHistory(path); len(got) != 0 {
		t.Fatalf("LoadHistory(missing) = %#v, want empty", got)
	}
}

func TestAppendHistoryCreatesFileAndParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "request-history.jsonl")
	if err := AppendHistory(path, "hello", 0); err != nil {
		t.Fatalf("AppendHistory: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file created: %v", err)
	}
	if got := LoadHistory(path); !reflect.DeepEqual(got, []string{"hello"}) {
		t.Fatalf("LoadHistory = %#v, want [hello]", got)
	}
}

func TestAppendHistoryDedupConsecutive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request-history.jsonl")
	for _, e := range []string{"a", "a"} {
		if err := AppendHistory(path, e, 0); err != nil {
			t.Fatalf("AppendHistory: %v", err)
		}
	}
	if got := LoadHistory(path); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("consecutive dup stored = %#v, want [a]", got)
	}

	// Non-consecutive repeat IS stored.
	for _, e := range []string{"b", "a"} {
		if err := AppendHistory(path, e, 0); err != nil {
			t.Fatalf("AppendHistory: %v", err)
		}
	}
	if got := LoadHistory(path); !reflect.DeepEqual(got, []string{"a", "b", "a"}) {
		t.Fatalf("non-consecutive repeat = %#v, want [a b a]", got)
	}
}

func TestAppendHistoryEmptyEntryNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request-history.jsonl")
	if err := AppendHistory(path, "", 0); err != nil {
		t.Fatalf("AppendHistory(empty): %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty entry should not create the file, stat err = %v", err)
	}

	// Existing file must be unchanged by an empty append.
	if err := AppendHistory(path, "x", 0); err != nil {
		t.Fatalf("AppendHistory: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := AppendHistory(path, "", 0); err != nil {
		t.Fatalf("AppendHistory(empty): %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("empty append changed file:\nbefore=%q\nafter=%q", before, after)
	}
}

func TestAppendHistoryCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request-history.jsonl")
	for _, e := range []string{"1", "2", "3", "4", "5"} {
		if err := AppendHistory(path, e, 3); err != nil {
			t.Fatalf("AppendHistory: %v", err)
		}
	}
	if got := LoadHistory(path); !reflect.DeepEqual(got, []string{"3", "4", "5"}) {
		t.Fatalf("cap=3 result = %#v, want [3 4 5]", got)
	}
}

func TestLoadHistorySkipsCorruptAndBlankLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request-history.jsonl")
	content := "\"good1\"\nnot json at all\n\n\"good2\"\n{bad\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := LoadHistory(path); !reflect.DeepEqual(got, []string{"good1", "good2"}) {
		t.Fatalf("LoadHistory = %#v, want [good1 good2]", got)
	}
}
