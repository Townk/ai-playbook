package playbook

import "testing"

func TestParseFenceInfo(t *testing.T) {
	lang, attrs, flags := ParseFenceInfo("bash {id=fix needs=diag,prep}")
	if lang != "bash" || attrs["id"] != "fix" || attrs["needs"] != "diag,prep" {
		t.Fatalf("bad parse: %q %v %v", lang, attrs, flags)
	}
	_, _, flags = ParseFenceInfo("console {static}")
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

func TestNormalizeFences_WellFormedUntouched(t *testing.T) {
	cases := []string{
		"```bash\ngg build\n```\nprose after\n",
		"```\nplain code\n```\n",
		"```python\nx = 1  # uses `backticks` in a comment\n```\n",
		"no fences at all\njust prose\n",
		"~~~\ntilde fence\n~~~\nprose\n",
	}
	for _, in := range cases {
		if got := NormalizeFences(in); got != in {
			t.Errorf("NormalizeFences altered well-formed input:\n in: %q\nout: %q", in, got)
		}
	}
}

func TestNormalizeFences_Repairs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```bash\ngg build\n```SDK is here.\n", "```bash\ngg build\n```\nSDK is here.\n"},
		{"```\ncode\n``` trailing\n", "```\ncode\n```\ntrailing\n"},
		// Longer opener; closer must be >= opener length.
		{"````\ncode\n````tail\n", "````\ncode\n````\ntail\n"},
		// A "```" run shorter than the opener is content, not a closer.
		{"````\n```\nstill code\n````\nprose\n", "````\n```\nstill code\n````\nprose\n"},
	}
	for _, c := range cases {
		if got := NormalizeFences(c.in); got != c.want {
			t.Errorf("NormalizeFences(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
