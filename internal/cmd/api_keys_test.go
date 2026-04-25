package cmd

import (
	"reflect"
	"testing"
)

func TestSplitAndTrim(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", []string{}},
		{"single", "foo", []string{"foo"}},
		{"basic", "a,b,c", []string{"a", "b", "c"}},
		{"with spaces", " a , b ,c", []string{"a", "b", "c"}},
		{"empty entries", "a,,b,", []string{"a", "b"}},
		{"only commas", ",,,", []string{}},
		{"whitespace only", "   ", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitAndTrim(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("splitAndTrim(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
