package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// MarshalYAML encodes v as YAML, and writes each multi-line
// string value as a literal block ("key: |").
//
// yaml.v3 writes a multi-line string as a literal block only when
// no line ends in a space. For other strings it writes one
// double-quoted line with "\n" escapes. Source code often has
// such lines, and a quoted line of code cannot be read or edited
// in the file that the create and update commands read back. The
// YAML specification permits trailing spaces in a literal block,
// so this function writes those blocks itself. The decoded value
// does not change.
//
// Some strings stay quoted, because a literal block cannot hold
// them exactly: a carriage return or other control character, a
// Unicode line separator, or text whose first line with content
// starts with a space or tab or comes after a line of only
// spaces.
func MarshalYAML(v any) ([]byte, error) {
	out, err := marshalYAMLBlocks(v)
	if err == nil {
		return out, nil
	}
	// yaml.v3 writes some multi-line strings as blocks that it cannot
	// read back, for example a string that starts with a line break in
	// a sequence. Its value encoder then fails, so the fallback builds
	// the node tree itself and double-quotes every string, which holds
	// every value exactly. The output is harder to edit but correct.
	return marshalYAMLQuoted(v)
}

// marshalYAMLBlocks is MarshalYAML without the fallback.
func marshalYAMLBlocks(v any) ([]byte, error) {
	var root yaml.Node
	if err := root.Encode(v); err != nil {
		return nil, err
	}
	token, err := blockToken(v)
	if err != nil {
		return nil, err
	}
	var blocks []string
	replaceBlocks(&root, token, &blocks)
	out, err := yaml.Marshal(&root)
	if err != nil || len(blocks) == 0 {
		return out, err
	}
	return insertBlocks(out, token, blocks)
}

// blockToken returns a plain word that the encoded value does not
// contain, used to mark where each block goes.
func blockToken(v any) (string, error) {
	plain, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	token := "oodleliteralblock"
	for strings.Contains(string(plain), token) {
		token += "x"
	}
	return token, nil
}

// replaceBlocks puts a marker in place of each string value that
// needs a literal block that yaml.v3 does not write. Mapping keys
// are left as they are.
func replaceBlocks(n *yaml.Node, token string, blocks *[]string) {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, c := range n.Content {
			replaceBlocks(c, token, blocks)
		}
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			replaceBlocks(n.Content[i], token, blocks)
		}
	case yaml.ScalarNode:
		if n.Tag == "!!str" && needsForcedBlock(n.Value) {
			*blocks = append(*blocks, n.Value)
			n.Value = fmt.Sprintf("%s%d", token, len(*blocks)-1)
			n.Style = 0
		}
	}
}

// needsForcedBlock is true for a multi-line string that yaml.v3
// writes quoted but a literal block can hold exactly.
func needsForcedBlock(s string) bool {
	if !strings.Contains(s, "\n") {
		return false
	}
	if !strings.Contains(s, " \n") && !strings.HasSuffix(s, " ") {
		// yaml.v3 writes a literal block for this itself.
		return false
	}
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if !unicode.IsPrint(r) || r == '\u0085' || r == '\u2028' ||
			r == '\u2029' || r == '\ufeff' {
			return false
		}
	}
	// The first line with content sets the indentation of the
	// block. Leading whitespace on it, or a line of only spaces
	// before it, would change the indentation that a reader finds.
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			continue
		}
		if strings.TrimLeft(line, " \t") != line {
			return false
		}
		break
	}
	return true
}

// insertBlocks replaces each marker with its literal block. The
// block is indented four columns past the start of its key or
// sequence item, which is deeper than every parent node.
func insertBlocks(out []byte, token string, blocks []string) ([]byte, error) {
	lines := strings.Split(string(out), "\n")
	var b strings.Builder
	for i, line := range lines {
		at := strings.Index(line, token)
		if at < 0 {
			b.WriteString(line)
			if i < len(lines)-1 {
				b.WriteByte('\n')
			}
			continue
		}
		var idx int
		if _, err := fmt.Sscanf(line[at+len(token):], "%d", &idx); err != nil ||
			idx < 0 || idx >= len(blocks) {
			return nil, fmt.Errorf("encoding yaml: bad block marker in %q", line)
		}
		prefix := line[:at]
		indent := len(prefix) - len(strings.TrimLeft(prefix, " "))
		rest := strings.TrimLeft(prefix, " ")
		for strings.HasPrefix(rest, "- ") {
			indent += 2
			rest = rest[2:]
		}
		b.WriteString(prefix)
		writeLiteralBlock(&b, blocks[idx], strings.Repeat(" ", indent+4))
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return []byte(b.String()), nil
}

// writeLiteralBlock writes the header ("|", "|-" or "|+") and the
// lines of s. The chomping indicator keeps the exact number of
// line breaks at the end of s.
func writeLiteralBlock(b *strings.Builder, s, pad string) {
	trailing := len(s) - len(strings.TrimRight(s, "\n"))
	switch {
	case trailing == 0:
		b.WriteString("|-")
	case trailing == 1:
		b.WriteString("|")
	default:
		b.WriteString("|+")
	}
	body := s
	if trailing > 0 {
		body = s[:len(s)-1]
	}
	for _, line := range strings.Split(body, "\n") {
		b.WriteByte('\n')
		if line != "" {
			b.WriteString(pad + line)
		}
	}
}

// marshalYAMLQuoted encodes v through its JSON form, so that the
// tree has only maps, lists and scalars, and writes every string
// double-quoted.
func marshalYAMLQuoted(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic any
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	return yaml.Marshal(quotedNode(generic))
}

func quotedNode(v any) *yaml.Node {
	switch x := v.(type) {
	case map[string]any:
		n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			n.Content = append(n.Content, quotedNode(k), quotedNode(x[k]))
		}
		return n
	case []any:
		n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, c := range x {
			n.Content = append(n.Content, quotedNode(c))
		}
		return n
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: x, Style: yaml.DoubleQuotedStyle}
	case json.Number:
		tag := "!!int"
		if _, err := x.Int64(); err != nil {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: x.String()}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(x)}
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
	}
}
