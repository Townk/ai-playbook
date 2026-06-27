package ui

import "testing"

func TestParseFenceInfo(t *testing.T) {
	lang, attrs, flags := parseFenceInfo("bash {id=fix needs=diag,prep}")
	if lang != "bash" || attrs["id"] != "fix" || attrs["needs"] != "diag,prep" {
		t.Fatalf("bad parse: %q %v %v", lang, attrs, flags)
	}
	_, _, flags = parseFenceInfo("console {static}")
	if !flags["static"] {
		t.Fatalf("static flag not parsed")
	}
}

func TestClassifyType(t *testing.T) {
	cases := map[string]string{"bash": "shell", "zsh": "shell", "python": "run",
		"node": "run", "diff": "diff", "patch": "diff", "console": "static",
		"text": "static", "json": "static", "": "static"}
	for lang, want := range cases {
		if got := classifyType(lang, false); got != want {
			t.Fatalf("classifyType(%q)=%q want %q", lang, got, want)
		}
	}
	if classifyType("bash", true) != "static" {
		t.Fatalf("static flag must force static")
	}
}

func TestAssignIDsAndValidate(t *testing.T) {
	bs := assignIDs([]Block{{Type: "shell"}, {ID: "x", Type: "run", Needs: []string{"b1"}}})
	if bs[0].ID != "b1" || bs[1].ID != "x" {
		t.Fatalf("auto-id wrong: %v", bs)
	}
	if err := validateNeeds(bs); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
	if validateNeeds([]Block{{ID: "a", Needs: []string{"missing"}}}) == nil {
		t.Fatalf("unknown need must error")
	}
	if validateNeeds([]Block{{ID: "a", Needs: []string{"b"}}, {ID: "b", Needs: []string{"a"}}}) == nil {
		t.Fatalf("cycle must error")
	}
}

// TestAssignIDsContiguousNoGap verifies that auto-ids are contiguous over the
// unnamed blocks only — named blocks do not consume a counter slot.
func TestAssignIDsContiguousNoGap(t *testing.T) {
	// [{}, {ID:"x"}, {}] → b1, x, b2  (no gap at b2)
	bs := assignIDs([]Block{{}, {ID: "x"}, {}})
	if bs[0].ID != "b1" || bs[1].ID != "x" || bs[2].ID != "b2" {
		t.Fatalf("gap in auto-ids: got %q %q %q, want b1 x b2", bs[0].ID, bs[1].ID, bs[2].ID)
	}
}

// TestAssignIDsNoCollisionWithExplicit verifies that auto-ids skip values
// already claimed by explicit ids, regardless of order.
func TestAssignIDsNoCollisionWithExplicit(t *testing.T) {
	// [{ID:"b1"}, {}] → the unnamed block must become b2, not b1
	bs := assignIDs([]Block{{ID: "b1"}, {}})
	if bs[0].ID != "b1" || bs[1].ID != "b2" {
		t.Fatalf("collision (explicit first): got %q %q, want b1 b2", bs[0].ID, bs[1].ID)
	}
	// [{}, {ID:"b1"}] → the unnamed block must NOT become b1 (explicit b1 exists)
	bs2 := assignIDs([]Block{{}, {ID: "b1"}})
	if bs2[0].ID == "b1" {
		t.Fatalf("collision (unnamed first): auto-id %q collides with explicit b1", bs2[0].ID)
	}
	if bs2[1].ID != "b1" {
		t.Fatalf("explicit id was mutated: got %q, want b1", bs2[1].ID)
	}
}
