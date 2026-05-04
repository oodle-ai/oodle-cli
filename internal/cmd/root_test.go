package cmd

import (
	"fmt"
	"testing"

	"github.com/spf13/cobra"
)

// TestShouldSkipConfig_Completion verifies the auto-generated `completion`
// subcommand bypasses LoadConfig so users can install shell completions
// before configuring the CLI.
func TestShouldSkipConfig_Completion(t *testing.T) {
	root := NewRootCmd()
	// Cobra registers the completion command lazily; force its creation so
	// we can locate it without invoking Execute.
	root.InitDefaultCompletionCmd()

	completion, _, err := root.Find([]string{"completion"})
	if err != nil {
		t.Fatalf("Find(completion): %v", err)
	}
	if completion.Name() != "completion" {
		t.Fatalf("Find returned %q, want completion", completion.Name())
	}
	if !shouldSkipConfig(completion) {
		t.Error("shouldSkipConfig(completion) = false, want true (completion must work without credentials)")
	}

	// Same for nested completion subcommands like `completion bash`.
	bash, _, err := root.Find([]string{"completion", "bash"})
	if err != nil {
		t.Fatalf("Find(completion bash): %v", err)
	}
	if !shouldSkipConfig(bash) {
		t.Error("shouldSkipConfig(completion bash) = false, want true")
	}
}

func TestShouldSkipConfig_AuthLogin(t *testing.T) {
	root := NewRootCmd()
	authLogin, _, err := root.Find([]string{"auth", "login"})
	if err != nil {
		t.Fatalf("Find(auth login): %v", err)
	}
	if !shouldSkipConfig(authLogin) {
		t.Error("shouldSkipConfig(auth login) = false, want true (auth login must work before credentials exist)")
	}
}

// TestShouldSkipConfig_OtherCommands sanity-checks that real API commands
// still go through config loading.
func TestShouldSkipConfig_OtherCommands(t *testing.T) {
	root := NewRootCmd()
	monitors, _, err := root.Find([]string{"monitors", "list"})
	if err != nil {
		t.Fatalf("Find(monitors list): %v", err)
	}
	if shouldSkipConfig(monitors) {
		t.Error("shouldSkipConfig(monitors list) = true, want false")
	}
}

// TestShouldSkipConfig_RootCommand verifies that the root command itself
// skips config loading so that bare "oodle" (no subcommand) shows help
// without requiring credentials.  This guards against the regression where
// adding RunE to the root caused PersistentPreRunE to fire and error out.
func TestShouldSkipConfig_RootCommand(t *testing.T) {
	root := NewRootCmd()
	if !shouldSkipConfig(root) {
		t.Error("shouldSkipConfig(root) = false, want true (bare 'oodle' must work without credentials)")
	}
}

// TestShouldSkipConfig_RootDoesNotAffectChildren confirms the root-command
// exemption above does not cascade: subcommands still load config.
func TestShouldSkipConfig_RootDoesNotAffectChildren(t *testing.T) {
	root := NewRootCmd()
	monitorsGet, _, err := root.Find([]string{"monitors", "get"})
	if err != nil {
		t.Fatalf("Find(monitors get): %v", err)
	}
	if shouldSkipConfig(monitorsGet) {
		t.Error("shouldSkipConfig(monitors get) = true, want false")
	}
}

func TestExactArgs_Correct(t *testing.T) {
	cmd := &cobra.Command{Use: "test <id>"}
	if err := exactArgs(1)(cmd, []string{"abc"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExactArgs_TooFew(t *testing.T) {
	cmd := &cobra.Command{Use: "test <id>"}
	err := exactArgs(1)(cmd, []string{})
	if err == nil {
		t.Fatal("expected error for too few args")
	}
	if got := err.Error(); got != "missing required argument(s). Usage: test <id>" {
		t.Errorf("unexpected message: %s", got)
	}
}

func TestExactArgs_TooMany(t *testing.T) {
	cmd := &cobra.Command{Use: "test <id>"}
	err := exactArgs(1)(cmd, []string{"a", "b"})
	if err == nil {
		t.Fatal("expected error for too many args")
	}
	if got := err.Error(); got != "too many arguments. Usage: test <id>" {
		t.Errorf("unexpected message: %s", got)
	}
}

func TestIsUsageError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{`unknown command "foo" for "oodle"`, true},
		{"missing required argument(s). Usage: oodle api-keys get <id>", true},
		{"too many arguments. Usage: oodle api-keys get <id>", true},
		{`required flag(s) "start" not set`, true},
		{"API request failed: connection refused", false},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			if got := isUsageError(fmt.Errorf("%s", tt.msg)); got != tt.want {
				t.Errorf("isUsageError(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}
