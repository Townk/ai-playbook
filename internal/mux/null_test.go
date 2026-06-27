package mux

import (
	"errors"
	"testing"

	"github.com/Townk/ai-playbook/internal/config"
)

// Compile-time check: null satisfies Mux.
var _ Mux = Null()

func TestNull_DumpScreen(t *testing.T) {
	m := Null()
	text, err := m.DumpScreen("")
	if err != nil {
		t.Fatalf("DumpScreen: unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("DumpScreen: want empty string, got %q", text)
	}
}

func TestNull_DumpScreen_WithPane(t *testing.T) {
	m := Null()
	text, err := m.DumpScreen("terminal_3")
	if err != nil {
		t.Fatalf("DumpScreen with pane: unexpected error: %v", err)
	}
	if text != "" {
		t.Fatalf("DumpScreen with pane: want empty string, got %q", text)
	}
}

func TestNull_SpawnFloat_ReturnsErrUnavailable(t *testing.T) {
	m := Null()
	if err := m.SpawnFloat(SpawnOptions{Cmd: []string{"delta"}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SpawnFloat: want ErrUnavailable, got %v", err)
	}
}

func TestNull_SpawnInputFloat_ReturnsErrUnavailable(t *testing.T) {
	m := Null()
	if err := m.SpawnInputFloat(SpawnOptions{Cmd: []string{"ai-playbook"}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SpawnInputFloat: want ErrUnavailable, got %v", err)
	}
}

func TestNull_SpawnPane_ReturnsErrUnavailable(t *testing.T) {
	m := Null()
	if err := m.SpawnPane(SpawnOptions{Cmd: []string{"bash"}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SpawnPane: want ErrUnavailable, got %v", err)
	}
}

func TestNull_SpawnDocked_ReturnsErrUnavailable(t *testing.T) {
	m := Null()
	if err := m.SpawnDocked(SpawnOptions{Cmd: []string{"ai-playbook", "run"}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SpawnDocked: want ErrUnavailable, got %v", err)
	}
}

func TestNull_TypeInto_ReturnsErrUnavailable(t *testing.T) {
	m := Null()
	if err := m.TypeInto("terminal_3", "git status"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("TypeInto: want ErrUnavailable, got %v", err)
	}
}

func TestIsNull_TrueForNull(t *testing.T) {
	if !IsNull(Null()) {
		t.Fatal("IsNull(Null()) must be true")
	}
}

func TestIsNull_FalseForTemplated(t *testing.T) {
	m := FromConfig(config.Default())
	if IsNull(m) {
		t.Fatal("IsNull(FromConfig(config.Default())) must be false")
	}
}
