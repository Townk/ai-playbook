package askbridge

import "testing"

func TestBridge_AskBlocksUntilRespond(t *testing.T) {
	b := New()
	done := make(chan Answer, 1)
	go func() { done <- b.Ask("which env?", "line", nil) }()

	req := <-b.Requests()
	if req.Prompt != "which env?" || req.Type != "line" {
		t.Fatalf("got (%q,%q), want the prompt+type", req.Prompt, req.Type)
	}
	req.Respond(Answer{Value: "prod", Submitted: true})

	if a := <-done; a.Value != "prod" || !a.Submitted {
		t.Fatalf("Ask returned %+v, want {prod,true}", a)
	}
}

func TestBridge_AskCarriesChoices(t *testing.T) {
	b := New()
	done := make(chan Answer, 1)
	go func() { done <- b.Ask("pick one", "choose", []string{"a", "b", "c"}) }()

	req := <-b.Requests()
	if req.Type != "choose" || len(req.Choices) != 3 || req.Choices[1] != "b" {
		t.Fatalf("got type=%q choices=%v, want choose + [a b c]", req.Type, req.Choices)
	}
	req.Respond(Answer{Value: "b", Submitted: true})
	if a := <-done; a.Value != "b" || !a.Submitted {
		t.Fatalf("Ask returned %+v, want {b,true}", a)
	}
}

func TestBridge_CancelAnswer(t *testing.T) {
	b := New()
	done := make(chan Answer, 1)
	go func() { done <- b.Ask("which env?", "line", nil) }()

	req := <-b.Requests()
	req.Respond(Answer{Submitted: false})
	if a := <-done; a.Submitted || a.Value != "" {
		t.Fatalf("Ask returned %+v, want a cancel {\"\",false}", a)
	}
}
