package playbook

import "testing"

func TestPortabilize_ProjectAndHome(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "bash", ID: "fix", Code: "cd /Users/me/Proj/app && cat /Users/me/.sdkrc"},
		{Kind: "code", Lang: "console", Static: true, Code: "/Users/me/Proj/x"}, // static: untouched
	}}}, Verify: &Step{Lang: "bash", Code: "ls /Users/me/Proj"}}
	Portabilize(&pb, "/Users/me/Proj", "/Users/me")
	if got := pb.Sections[0].Content[0].Code; got != "cd $PROJECT_ROOT/app && cat $HOME/.sdkrc" {
		t.Fatalf("runnable block = %q", got)
	}
	if got := pb.Sections[0].Content[1].Code; got != "/Users/me/Proj/x" {
		t.Errorf("static block must be untouched, got %q", got)
	}
	if got := pb.Verify.Code; got != "ls $PROJECT_ROOT" {
		t.Errorf("verify = %q", got)
	}
}

func TestPortabilize_BoundaryAndNoMangle(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "code", Lang: "bash", ID: "a", Code: "echo /Users/me/Project2/x"}, // /Users/me/Proj is a substring — must NOT match
	}}}}
	Portabilize(&pb, "/Users/me/Proj", "/Users/me")
	if got := pb.Sections[0].Content[0].Code; got != "echo $HOME/Project2/x" {
		t.Fatalf("substring must not be mangled to PROJECT_ROOT; home prefix applies: %q", got)
	}
}

func TestPortabilize_LeavesProseUntouched(t *testing.T) {
	pb := Playbook{Title: "T", Sections: []Section{{Heading: "S", Content: []ContentItem{
		{Kind: "text", Text: "/Users/me/Proj/readme.md is the doc"},
	}}}}
	Portabilize(&pb, "/Users/me/Proj", "/Users/me")
	if got := pb.Sections[0].Content[0].Text; got != "/Users/me/Proj/readme.md is the doc" {
		t.Errorf("prose text must be untouched, got %q", got)
	}
}
