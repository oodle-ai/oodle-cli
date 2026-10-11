package output

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Every string must decode to the same value that went in, in
// every place a string can be: a map value, a list item, and a
// map inside a list.
func TestMarshalYAMLRoundTrips(t *testing.T) {
	values := []string{
		"def f():\n    return 1\n",
		"x = 1   \ny = 2\n",           // trailing spaces on a line
		"x = 1\ny = 2   ",             // trailing spaces at the end
		"x = 1  \n",                   // trailing spaces, then a newline
		"if a:\n\treturn 1  \n",       // a tab indent and trailing spaces
		"a\t\nb \n",                   // a tab and a space at line ends
		"x = 1 \n\n\n",                // more than one final newline
		"x = 1 \ny = 2",               // no final newline
		"\nx = 1 \n",                  // a leading empty line
		"\nabsent(up == 1)\n",         // a leading line break, no trailing space
		"a \n\n  b \n",                // an empty line, then indented text
		"  indented first line \n",    // must stay quoted
		"   \nx = 1 \n",               // a line of spaces first: quoted
		"crlf \r\nline\r\n",           // a carriage return: quoted
		"no newline, trailing space ", // one line: yaml.v3 handles it
		"oodleliteralblock0 \nx\n",    // the marker word itself
		"",
	}
	for _, v := range values {
		data := map[string]any{
			"sourceCode": v,
			"list":       []any{v, map[string]any{"code": v}},
			"nested":     map[string]any{"inner": map[string]any{"code": v}},
		}
		out, err := MarshalYAML(data)
		if err != nil {
			t.Fatalf("MarshalYAML(%q): %v", v, err)
		}
		var got map[string]any
		if err := yaml.Unmarshal(out, &got); err != nil {
			t.Fatalf("output for %q does not parse: %v\n%s", v, err, out)
		}
		if !reflect.DeepEqual(got, data) {
			t.Errorf("round trip of %q changed the value:\n%s", v, out)
		}
	}
}

func TestMarshalYAMLWritesLiteralBlocks(t *testing.T) {
	for _, v := range []string{
		"x = 1   \ny = 2\n",
		"if a:\n\treturn 1  \n",
		"x = 1 \ny = 2",
		"x = 1 \n\n\n",
	} {
		out, err := MarshalYAML(map[string]any{"sourceCode": v})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(out), "sourceCode: |") {
			t.Errorf("%q is not a literal block:\n%s", v, out)
		}
		if strings.Contains(string(out), `\n`) {
			t.Errorf("%q has escaped newlines:\n%s", v, out)
		}
	}
}

// A struct with yaml tags keeps its field order and tags.
func TestMarshalYAMLKeepsStructs(t *testing.T) {
	type tpl struct {
		Name string `yaml:"name"`
		Code string `yaml:"sourceCode"`
	}
	out, err := MarshalYAML(tpl{Name: "demo", Code: "a  \nb\n"})
	if err != nil {
		t.Fatal(err)
	}
	want := "name: demo\nsourceCode: |\n    a  \n    b\n"
	if string(out) != want {
		t.Errorf("got:\n%q\nwant:\n%q", out, want)
	}
}
