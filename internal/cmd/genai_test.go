package cmd

import (
	"strings"
	"testing"
	"time"
)

// TestGenAICommandTree checks every documented subcommand is
// reachable. A group registered but never added to the tree
// compiles fine and simply does not exist at runtime.
func TestGenAICommandTree(t *testing.T) {
	paths := [][]string{
		{"genai", "prompts", "list"},
		{"genai", "prompts", "get"},
		{"genai", "prompts", "versions"},
		{"genai", "prompts", "create"},
		{"genai", "prompts", "label"},
		{"genai", "prompts", "delete"},
		{"genai", "datasets", "list"},
		{"genai", "datasets", "get"},
		{"genai", "datasets", "create"},
		{"genai", "datasets", "delete"},
		{"genai", "datasets", "items", "list"},
		{"genai", "datasets", "items", "get"},
		{"genai", "datasets", "items", "create"},
		{"genai", "datasets", "items", "update"},
		{"genai", "datasets", "items", "delete"},
		{"genai", "evaluators", "list"},
		{"genai", "evaluators", "get"},
		{"genai", "evaluators", "create"},
		{"genai", "evaluators", "update"},
		{"genai", "evaluators", "delete"},
		{"genai", "eval-rules", "list"},
		{"genai", "eval-rules", "create"},
		{"genai", "eval-rules", "update"},
		{"genai", "eval-rules", "delete"},
		{"genai", "scores", "list"},
		{"genai", "scores", "get"},
		{"genai", "scores", "create"},
		{"genai", "scores", "configs", "list"},
		{"genai", "scores", "configs", "create"},
		{"genai", "experiments", "list"},
		{"genai", "experiments", "items"},
		{"genai", "experiments", "run"},
		{"genai", "experiments", "status"},
		{"genai", "experiments", "cancel"},
		{"genai", "experiments", "jobs"},
		{"genai", "connections", "list"},
		{"genai", "connections", "create"},
		{"genai", "connections", "update"},
		{"genai", "connections", "delete"},
	}

	root := NewRootCmd()
	for _, path := range paths {
		cmd, _, err := root.Find(path)
		if err != nil {
			t.Errorf("Find(%v): %v", path, err)
			continue
		}
		want := path[len(path)-1]
		if cmd.Name() != want {
			t.Errorf(
				"Find(%v) resolved to %q, want %q "+
					"(cobra falls back to the nearest parent "+
					"when a subcommand is missing)",
				path, cmd.Name(), want,
			)
		}
	}
}

// TestGenAIAliases guards the short forms the docs advertise.
func TestGenAIAliases(t *testing.T) {
	root := NewRootCmd()
	for _, alias := range []string{"genai", "llmops", "ai"} {
		cmd, _, err := root.Find([]string{alias, "prompts", "list"})
		if err != nil {
			t.Errorf("Find(%s prompts list): %v", alias, err)
			continue
		}
		if cmd.Name() != "list" {
			t.Errorf(
				"alias %q did not resolve to the genai tree",
				alias,
			)
		}
	}
}

// TestGenAINeedsConfig verifies the genai commands are NOT in
// the config-skip list — they all call the API.
func TestGenAINeedsConfig(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"genai", "datasets", "list"})
	if err != nil {
		t.Fatalf("Find(genai datasets list): %v", err)
	}
	if shouldSkipConfig(cmd) {
		t.Error(
			"shouldSkipConfig(genai datasets list) = true, " +
				"want false",
		)
	}
}

func TestToRFC3339(t *testing.T) {
	t.Run("empty stays empty", func(t *testing.T) {
		got, err := toRFC3339("")
		if err != nil || got != "" {
			t.Fatalf("toRFC3339(\"\") = %q, %v", got, err)
		}
	})

	t.Run("explicit timestamp passes through", func(t *testing.T) {
		const ts = "2026-08-12T00:00:00Z"
		got, err := toRFC3339(ts)
		if err != nil {
			t.Fatalf("toRFC3339(%q): %v", ts, err)
		}
		if got != ts {
			t.Errorf("toRFC3339(%q) = %q, want unchanged", ts, got)
		}
	})

	t.Run("relative duration resolves", func(t *testing.T) {
		got, err := toRFC3339("-24h")
		if err != nil {
			t.Fatalf("toRFC3339(-24h): %v", err)
		}
		parsed, err := time.Parse(time.RFC3339, got)
		if err != nil {
			t.Fatalf("result %q is not RFC3339: %v", got, err)
		}
		delta := time.Since(parsed)
		if delta < 23*time.Hour || delta > 25*time.Hour {
			t.Errorf(
				"toRFC3339(-24h) = %q, which is %v ago",
				got, delta,
			)
		}
	})

	t.Run("day suffix resolves", func(t *testing.T) {
		got, err := toRFC3339("-7d")
		if err != nil {
			t.Fatalf("toRFC3339(-7d): %v", err)
		}
		parsed, err := time.Parse(time.RFC3339, got)
		if err != nil {
			t.Fatalf("result %q is not RFC3339: %v", got, err)
		}
		delta := time.Since(parsed)
		if delta < 6*24*time.Hour || delta > 8*24*time.Hour {
			t.Errorf(
				"toRFC3339(-7d) = %q, which is %v ago",
				got, delta,
			)
		}
	})

	t.Run("now resolves", func(t *testing.T) {
		got, err := toRFC3339("now")
		if err != nil {
			t.Fatalf("toRFC3339(now): %v", err)
		}
		if _, err := time.Parse(time.RFC3339, got); err != nil {
			t.Fatalf("result %q is not RFC3339: %v", got, err)
		}
	})

	// Passing junk through would let the server fall back to its
	// 15-minute default, so a --start the user thought covered a
	// day silently reports nothing.
	t.Run("junk is rejected", func(t *testing.T) {
		for _, bad := range []string{
			"yesterday", "1 hour ago", "1731628800", "-1x",
		} {
			if _, err := toRFC3339(bad); err == nil {
				t.Errorf(
					"toRFC3339(%q) succeeded, want an error",
					bad,
				)
			}
		}
	})
}

func TestPageFlagsValues(t *testing.T) {
	// Zero must stay unset: sending limit=0 fetches nothing
	// rather than falling back to the server's default of 50.
	var empty pageFlags
	if limit, page := empty.values(); limit != nil || page != nil {
		t.Errorf(
			"zero pageFlags produced limit=%v page=%v, want nil",
			limit, page,
		)
	}

	set := pageFlags{limit: 25, page: 2}
	limit, page := set.values()
	if limit == nil || *limit != 25 {
		t.Errorf("limit = %v, want 25", limit)
	}
	if page == nil || *page != 2 {
		t.Errorf("page = %v, want 2", page)
	}
}

func TestGenAIHelpListsEveryGroup(t *testing.T) {
	root := NewRootCmd()
	genai, _, err := root.Find([]string{"genai"})
	if err != nil {
		t.Fatalf("Find(genai): %v", err)
	}
	for _, group := range []string{
		"prompts", "datasets", "evaluators", "eval-rules",
		"scores", "experiments", "connections",
	} {
		if !strings.Contains(genai.Long, group) {
			t.Errorf(
				"`oodle genai --help` does not mention %q",
				group,
			)
		}
	}
}
