package cmd

import (
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// subcommandNames returns the (sorted) Use names of c's immediate subcommands.
func subcommandNames(c *cobra.Command) []string {
	out := make([]string, 0, len(c.Commands()))
	for _, sub := range c.Commands() {
		// Use the first whitespace-delimited word so e.g. "get <id>" -> "get".
		name := strings.Fields(sub.Use)
		if len(name) > 0 {
			out = append(out, name[0])
		}
	}
	sort.Strings(out)
	return out
}

// findSubcommand returns the immediate subcommand of c whose first Use word
// equals name, or nil if not found.
func findSubcommand(c *cobra.Command, name string) *cobra.Command {
	for _, sub := range c.Commands() {
		fields := strings.Fields(sub.Use)
		if len(fields) > 0 && fields[0] == name {
			return sub
		}
	}
	return nil
}

// TestNewMonitorsCmd_Structure verifies the monitors command tree is wired up
// with the expected subcommands, aliases, and flags.
func TestNewMonitorsCmd_Structure(t *testing.T) {
	cmd := newMonitorsCmd()
	if cmd.Use != "monitors" {
		t.Errorf("Use = %q, want %q", cmd.Use, "monitors")
	}
	wantAliases := []string{"mon", "monitor"}
	gotAliases := append([]string{}, cmd.Aliases...)
	sort.Strings(gotAliases)
	if strings.Join(gotAliases, ",") != strings.Join(wantAliases, ",") {
		t.Errorf("Aliases = %v, want %v", cmd.Aliases, wantAliases)
	}

	wantSubs := []string{
		"create", "delete", "get", "list",
		"state", "template-files", "triggers", "update",
	}
	got := subcommandNames(cmd)
	if strings.Join(got, ",") != strings.Join(wantSubs, ",") {
		t.Errorf("subcommands = %v, want %v", got, wantSubs)
	}

	// `create` must require -f/--file.
	create := findSubcommand(cmd, "create")
	if create == nil {
		t.Fatalf("create subcommand missing")
	}
	if create.Flag("file") == nil {
		t.Error("create: -f/--file flag missing")
	}
	if a := create.Flag("file").Annotations["cobra_annotation_bash_completion_one_required_flag"]; len(a) == 0 || a[0] != "true" {
		t.Error("create: --file should be required")
	}

	// `update` must accept exactly 1 positional and require -f.
	update := findSubcommand(cmd, "update")
	if update == nil {
		t.Fatalf("update subcommand missing")
	}
	if update.Flag("file") == nil {
		t.Error("update: -f/--file flag missing")
	}

	// `delete` must expose --ids.
	del := findSubcommand(cmd, "delete")
	if del == nil {
		t.Fatalf("delete subcommand missing")
	}
	if del.Flag("ids") == nil {
		t.Error("delete: --ids flag missing")
	}

	// `state` must expose --history-range.
	state := findSubcommand(cmd, "state")
	if state == nil {
		t.Fatalf("state subcommand missing")
	}
	if state.Flag("history-range") == nil {
		t.Error("state: --history-range flag missing")
	}
}

// TestMonitorsDelete_RequiresIDOrIDs verifies the delete subcommand rejects
// invocations without either a positional id or --ids.
func TestMonitorsDelete_RequiresIDOrIDs(t *testing.T) {
	cmd := newMonitorsCmd()
	del := findSubcommand(cmd, "delete")
	if del == nil {
		t.Fatalf("delete subcommand missing")
	}
	// Both empty: should error before any API call.
	err := del.RunE(del, []string{})
	if err == nil {
		t.Fatal("expected error when no id and no --ids")
	}
	if !strings.Contains(err.Error(), "required") && !strings.Contains(err.Error(), "either") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestParseHistoryRange verifies the raw --history-range form is validated
// locally, so a typo surfaces as a CLI error rather than an opaque server one.
func TestParseHistoryRange(t *testing.T) {
	t.Run("Valid", func(t *testing.T) {
		got, err := parseHistoryRange("1705036708-1705123108")
		if err != nil {
			t.Fatal(err)
		}
		if got != "1705036708-1705123108" {
			t.Errorf("got %q, want it returned unchanged", got)
		}
	})

	t.Run("TrimsSurroundingSpace", func(t *testing.T) {
		got, err := parseHistoryRange("  1705036708-1705123108  ")
		if err != nil {
			t.Fatal(err)
		}
		if got != "1705036708-1705123108" {
			t.Errorf("got %q, want trimmed value", got)
		}
	})

	t.Run("Invalid", func(t *testing.T) {
		for _, v := range []string{
			"bogus",                            // no separator
			"1705036708",                       // only one bound
			"abc-def",                          // non-numeric bounds
			"-7d-now",                          // relative syntax belongs on --start/--end
			"1705036708-",                      // missing end
			"1705036708-1705123108-1705123109", // trailing junk
		} {
			if _, err := parseHistoryRange(v); err == nil {
				t.Errorf("expected error for %q", v)
			}
		}
	})

	t.Run("StartAfterEnd", func(t *testing.T) {
		_, err := parseHistoryRange("1705123108-1705036708")
		if err == nil {
			t.Fatal("expected error when start is after end")
		}
		if !strings.Contains(err.Error(), "after end") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

// TestMonitorsState_TimeFlags verifies `monitors state` exposes the CLI-wide
// --start/--end syntax alongside the raw --history-range, and that the two
// forms are mutually exclusive.
func TestMonitorsState_TimeFlags(t *testing.T) {
	state := findSubcommand(newMonitorsCmd(), "state")
	if state == nil {
		t.Fatal("state subcommand missing")
	}
	for _, name := range []string{"history-range", "start", "end"} {
		if state.Flags().Lookup(name) == nil {
			t.Errorf("missing --%s flag", name)
		}
	}

	// --history-range together with --start must be rejected by cobra's
	// mutually-exclusive group rather than silently preferring one. Drive
	// this from the root command: Execute on a subcommand reroutes to the
	// root, so the args must include the "state" path themselves.
	root := newMonitorsCmd()
	root.SetArgs([]string{"state", "m1", "--history-range", "1-2", "--start", "-1h"})
	root.SilenceUsage, root.SilenceErrors = true, true
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "none of the others") {
		t.Errorf("expected mutual-exclusion error, got %v", err)
	}
}

func TestNewNotifiersCmd_Structure(t *testing.T) {
	cmd := newNotifiersCmd()
	if cmd.Use != "notifiers" {
		t.Errorf("Use = %q, want %q", cmd.Use, "notifiers")
	}
	if len(cmd.Aliases) != 1 || cmd.Aliases[0] != "notifier" {
		t.Errorf("Aliases = %v, want [notifier]", cmd.Aliases)
	}
	wantSubs := []string{"create", "delete", "get", "list", "update"}
	got := subcommandNames(cmd)
	if strings.Join(got, ",") != strings.Join(wantSubs, ",") {
		t.Errorf("subcommands = %v, want %v", got, wantSubs)
	}
}

func TestNewNotificationPoliciesCmd_Structure(t *testing.T) {
	cmd := newNotificationPoliciesCmd()
	if cmd.Use != "notification-policies" {
		t.Errorf("Use = %q", cmd.Use)
	}
	wantAliases := []string{"notification-policy", "np"}
	gotAliases := append([]string{}, cmd.Aliases...)
	sort.Strings(gotAliases)
	if strings.Join(gotAliases, ",") != strings.Join(wantAliases, ",") {
		t.Errorf("Aliases = %v, want %v", cmd.Aliases, wantAliases)
	}
	wantSubs := []string{"create", "delete", "get", "list", "update"}
	got := subcommandNames(cmd)
	if strings.Join(got, ",") != strings.Join(wantSubs, ",") {
		t.Errorf("subcommands = %v, want %v", got, wantSubs)
	}
}

func TestNewMutingRulesCmd_Structure(t *testing.T) {
	cmd := newMutingRulesCmd()
	if cmd.Use != "muting-rules" {
		t.Errorf("Use = %q", cmd.Use)
	}
	wantAliases := []string{"mr", "muting-rule"}
	gotAliases := append([]string{}, cmd.Aliases...)
	sort.Strings(gotAliases)
	if strings.Join(gotAliases, ",") != strings.Join(wantAliases, ",") {
		t.Errorf("Aliases = %v, want %v", cmd.Aliases, wantAliases)
	}
	// Note: no `update` for muting rules.
	wantSubs := []string{"create", "delete", "get", "list"}
	got := subcommandNames(cmd)
	if strings.Join(got, ",") != strings.Join(wantSubs, ",") {
		t.Errorf("subcommands = %v, want %v", got, wantSubs)
	}
}

// TestRootCmd_RegistersAlertingCommands ensures NewRootCmd wires in the four
// new command groups.
func TestRootCmd_RegistersAlertingCommands(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"monitors", "notifiers", "notification-policies", "muting-rules"} {
		if findSubcommand(root, name) == nil {
			t.Errorf("root: missing subcommand %q", name)
		}
	}
}
