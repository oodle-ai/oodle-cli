package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type sample struct {
	Foo string `json:"foo" yaml:"foo"`
	Bar int    `json:"bar" yaml:"bar"`
}

func TestReadInputFile_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "in.json")
	if err := os.WriteFile(path, []byte(`{"foo":"hi","bar":7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var s sample
	if err := readInputFile(path, &s); err != nil {
		t.Fatalf("readInputFile: %v", err)
	}
	if s.Foo != "hi" || s.Bar != 7 {
		t.Errorf("got %+v", s)
	}
}

func TestReadInputFile_YAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "in.yaml")
	if err := os.WriteFile(path, []byte("foo: hello\nbar: 42\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var s sample
	if err := readInputFile(path, &s); err != nil {
		t.Fatalf("readInputFile: %v", err)
	}
	if s.Foo != "hello" || s.Bar != 42 {
		t.Errorf("got %+v", s)
	}
}

func TestReadInputFile_UnknownExtensionFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(path, []byte(`{"foo":"x","bar":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var s sample
	if err := readInputFile(path, &s); err != nil {
		t.Fatalf("readInputFile: %v", err)
	}
	if s.Foo != "x" || s.Bar != 1 {
		t.Errorf("got %+v", s)
	}
}

func TestParseTimeFlag_Epoch(t *testing.T) {
	got, err := parseTimeFlag("1700000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if got != 1700000000000000 {
		t.Errorf("got %d", got)
	}
}

func TestParseTimeFlag_Now(t *testing.T) {
	before := time.Now().UnixMicro()
	got, err := parseTimeFlag("now")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().UnixMicro()
	if got < before || got > after {
		t.Errorf("now = %d not in [%d, %d]", got, before, after)
	}
}

func TestParseTimeFlag_Relative1h(t *testing.T) {
	now := time.Now().UnixMicro()
	got, err := parseTimeFlag("-1h")
	if err != nil {
		t.Fatal(err)
	}
	delta := now - got
	expected := int64(time.Hour / time.Microsecond)
	if delta < expected-int64(time.Second/time.Microsecond) || delta > expected+int64(time.Second/time.Microsecond) {
		t.Errorf("delta = %d, want ~%d", delta, expected)
	}
}

func TestParseTimeFlag_Relative7d(t *testing.T) {
	now := time.Now().UnixMicro()
	got, err := parseTimeFlag("-7d")
	if err != nil {
		t.Fatal(err)
	}
	delta := now - got
	expected := int64(7 * 24 * time.Hour / time.Microsecond)
	if delta < expected-int64(time.Second/time.Microsecond) || delta > expected+int64(time.Second/time.Microsecond) {
		t.Errorf("delta = %d, want ~%d", delta, expected)
	}
}

func TestParseTimeFlag_Invalid(t *testing.T) {
	if _, err := parseTimeFlag("garbage"); err == nil {
		t.Error("expected error")
	}
	if _, err := parseTimeFlag(""); err == nil {
		t.Error("expected error for empty")
	}
}

func TestConfirmAction_Force(t *testing.T) {
	if !confirmAction("delete?", true) {
		t.Error("force=true should return true")
	}
}
