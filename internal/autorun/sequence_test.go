package autorun

import "testing"

func TestSequence_SkipsStaticAndRollbackTargets(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun, Rollback: "undo-a"},
		{ID: "undo-a", Kind: KindRun}, // rollback target → skipped
		{ID: "note", Static: true},    // static → skipped
		{ID: "b", Kind: KindRun, Needs: []string{"a"}},
	}
	got := Sequence(blocks)
	var ids []string
	for _, b := range got {
		ids = append(ids, b.ID)
	}
	want := []string{"a", "b"}
	if len(ids) != 2 || ids[0] != want[0] || ids[1] != want[1] {
		t.Fatalf("Sequence ids = %v, want %v", ids, want)
	}
}

func TestNextRunnable_RespectsNeedsAndStatus(t *testing.T) {
	blocks := []Block{
		{ID: "a", Kind: KindRun},
		{ID: "b", Kind: KindRun, Needs: []string{"a"}},
	}
	// a not yet run → next is a.
	if b, ok := NextRunnable(blocks, map[string]string{}); !ok || b.ID != "a" {
		t.Fatalf("first NextRunnable = %v,%v want a,true", b.ID, ok)
	}
	// a ok → next is b.
	if b, ok := NextRunnable(blocks, map[string]string{"a": StatusOK}); !ok || b.ID != "b" {
		t.Fatalf("after a ok NextRunnable = %v,%v want b,true", b.ID, ok)
	}
	// a skipped → b unrunnable → none.
	if _, ok := NextRunnable(blocks, map[string]string{"a": StatusSkipped}); ok {
		t.Fatal("b must be unrunnable when its need a is skipped")
	}
	// all ok → none.
	if _, ok := NextRunnable(blocks, map[string]string{"a": StatusOK, "b": StatusOK}); ok {
		t.Fatal("no runnable step when all ok")
	}
}

func TestRollbackPairs_ReverseOkOnly(t *testing.T) {
	blocks := []Block{
		{ID: "a", Rollback: "undo-a"},
		{ID: "b", Rollback: "undo-b"},
		{ID: "c"}, // no rollback
	}
	status := map[string]string{"a": StatusOK, "b": StatusOK, "c": StatusFailed}
	got := RollbackPairs(blocks, status)
	want := [][2]string{{"b", "undo-b"}, {"a", "undo-a"}} // reverse, ok-with-rollback only
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("RollbackPairs = %v, want %v", got, want)
	}
}
