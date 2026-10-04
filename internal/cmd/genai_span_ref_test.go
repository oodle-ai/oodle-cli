package cmd

import "testing"

func TestSplitSpanRef(t *testing.T) {
	cases := []struct {
		in, trace, span string
		ok              bool
	}{
		{"4bf92f35:00f067aa", "4bf92f35", "00f067aa", true},
		// A trace id with a colon in it keeps it.
		{"a911762858:6ac0b307:8b12834766513be5", "a911762858:6ac0b307", "8b12834766513be5", true},
		{"no-colon", "", "", false},
		{":span", "", "", false},
		{"trace:", "", "", false},
	}
	for _, c := range cases {
		trace, span, ok := splitSpanRef(c.in)
		if trace != c.trace || span != c.span || ok != c.ok {
			t.Errorf("splitSpanRef(%q) = %q, %q, %v; want %q, %q, %v",
				c.in, trace, span, ok, c.trace, c.span, c.ok)
		}
	}
}
