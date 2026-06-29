package ui

import (
	"reflect"
	"testing"

	"github.com/Townk/ai-playbook/internal/frontmatter"
)

func TestGroupSizes(t *testing.T) {
	cases := []struct {
		n    int
		want []int
	}{
		{0, nil},
		{1, []int{1}},
		{5, []int{5}},
		{6, []int{3, 3}},
		{11, []int{4, 4, 3}},
		{12, []int{4, 4, 4}},
		{13, []int{5, 5, 3}},
		{16, []int{4, 4, 4, 4}},
	}
	for _, c := range cases {
		if got := groupSizes(c.n); !reflect.DeepEqual(got, c.want) {
			t.Errorf("groupSizes(%d) = %v, want %v", c.n, got, c.want)
		}
		// every group ≤ 5
		for _, s := range groupSizes(c.n) {
			if s > 5 || s < 1 {
				t.Errorf("groupSizes(%d) produced out-of-range size %d", c.n, s)
			}
		}
	}
}

func TestBuildConfirmVars(t *testing.T) {
	env := map[string]frontmatter.EnvValue{
		"PROJECT_ROOT":     {Why: "the project directory"},
		"ANDROID_SDK_ROOT": {Why: "the SDK"},
		"UNSET_VAR":        {Why: "not in shell"},
	}
	getenv := func(k string) string {
		if k == "ANDROID_SDK_ROOT" {
			return "/live/sdk"
		}
		return ""
	}
	got := buildConfirmVars(env, "/new/proj", getenv)
	want := []confirmVar{
		{"ANDROID_SDK_ROOT", "/live/sdk", "the SDK"},
		{"PROJECT_ROOT", "/new/proj", "the project directory"},
		{"UNSET_VAR", "", "not in shell"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildConfirmVars = %v, want %v", got, want)
	}
}
