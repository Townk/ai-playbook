package ui

import "testing"

func TestCode_FileBlockRecognized(t *testing.T) {
	lines, _, blocks := Render("```go {id=new file=cmd/x/main.go}\npackage main\n```\n", 80, RenderOpts{})
	_ = lines
	var b *Block
	for i := range blocks {
		if blocks[i].ID == "new" {
			b = &blocks[i]
		}
	}
	if b == nil || b.File != "cmd/x/main.go" || b.Type != "create" {
		t.Fatalf("file= block not recognized: %+v", b)
	}
}
