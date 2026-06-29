package ui

import (
	"reflect"
	"testing"
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
