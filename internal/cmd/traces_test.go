package cmd

import (
	"strings"
	"testing"
)

var testTraceLabels = []string{
	"parent_span_id",
	"resource::env",
	"resource::service.name",
	"resource::service.version",
	"span::env",
	"span::http.method",
}

func TestResolveTraceLabel(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"resource::service.name", "resource::service.name"},
		{"parent_span_id", "parent_span_id"},
		{"service.name", "resource::service.name"},
		{"service", "resource::service.name"},
		{"http.method", "span::http.method"},
	}
	for _, tt := range tests {
		got, err := resolveTraceLabel(tt.name, testTraceLabels)
		if err != nil {
			t.Errorf("resolveTraceLabel(%q): %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("resolveTraceLabel(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestResolveTraceLabel_Ambiguous(t *testing.T) {
	_, err := resolveTraceLabel("env", testTraceLabels)
	if err == nil {
		t.Fatal("expected an error for an ambiguous label")
	}
	for _, want := range []string{"ambiguous", "resource::env", "span::env"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestResolveTraceLabel_UnknownSuggests(t *testing.T) {
	_, err := resolveTraceLabel("service.nam", testTraceLabels)
	if err == nil {
		t.Fatal("expected an error for an unknown label")
	}
	msg := err.Error()
	if !strings.Contains(msg, `unknown trace label "service.nam"`) {
		t.Errorf("unexpected error: %q", msg)
	}
	if !strings.Contains(msg, "did you mean resource::service.name") {
		t.Errorf("expected suggestion of resource::service.name, got: %q", msg)
	}
}

func TestResolveTraceLabel_UnknownNoSuggestion(t *testing.T) {
	_, err := resolveTraceLabel("zzzzzzzz", testTraceLabels)
	if err == nil {
		t.Fatal("expected an error for an unknown label")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("expected no suggestion, got: %q", err)
	}
	if !strings.Contains(err.Error(), "oodle traces labels") {
		t.Errorf("expected a hint to run 'oodle traces labels', got: %q", err)
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
		{"service.name", "service.name", 0},
	}
	for _, tt := range tests {
		if got := levenshtein(tt.a, tt.b); got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
