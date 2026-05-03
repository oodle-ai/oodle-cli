package cmd

import (
	"testing"
)

func TestNewLogsCmd_Structure(t *testing.T) {
	cmd := newLogsCmd()
	if cmd.Use != "logs" {
		t.Errorf("expected Use='logs', got %q", cmd.Use)
	}
	if len(cmd.Aliases) == 0 || cmd.Aliases[0] != "log" {
		t.Errorf("expected alias 'log', got %v", cmd.Aliases)
	}

	// Verify expected subcommands are registered.
	subs := map[string]bool{}
	for _, c := range cmd.Commands() {
		subs[c.Use] = true
	}
	for _, want := range []string{"query", "index-patterns"} {
		if !subs[want] {
			t.Errorf("missing subcommand %q; have %v", want, subs)
		}
	}
}

func TestNewLogsQueryCmd_RequiresFileFlag(t *testing.T) {
	cmd := newLogsQueryCmd()
	f := cmd.Flags().Lookup("file")
	if f == nil {
		t.Fatal("expected --file / -f flag")
	}
	// The flag should be required.
	if ann := f.Annotations; ann == nil {
		t.Error("expected --file to be required (missing annotations)")
	}
}
