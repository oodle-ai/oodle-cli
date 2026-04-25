package cmd

import (
	"testing"
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
