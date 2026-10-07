package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
)

// TestRawBodyRoundTrip checks that create and update send the file without
// change, and that get prints fields that the generated types do not know.
// Without this, get, edit and update removes those fields on the server.
func TestRawBodyRoundTrip(t *testing.T) {
	const unknown = `"future_field":{"keep":true}`
	tests := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
		resp string
	}{
		{"monitors update", newMonitorsUpdateCmd, []string{"m1"},
			`{"id":"00000000-0000-0000-0000-000000000001","name":"m","promql_query":"up",` + unknown + `}`},
		{"monitors create", newMonitorsCreateCmd, nil,
			`{"id":"00000000-0000-0000-0000-000000000001","name":"m","promql_query":"up",` + unknown + `}`},
		{"notifiers update", newNotifiersUpdateCmd, []string{"n1"}, `{"name":"n","type":1,` + unknown + `}`},
		{"notification-policies create", newNotificationPoliciesCreateCmd, nil, `{"name":"p",` + unknown + `}`},
		{"muting-rules create", newMutingRulesCreateCmd, nil, `{"id":"r","name":"r",` + unknown + `}`},
		{"drop-rules update", newDropRulesUpdateCmd, []string{"d1"},
			`{"id":"d1","rule_name":"r","type":"drop","metric_name":{"name":"__name__","value":"x"},` + unknown + `}`},
		{"synthetic-monitors update", newSyntheticMonitorsUpdateCmd, []string{"s1"}, `{"id":"s1","name":"s",` + unknown + `}`},
		{"dashboards create", newDashboardsCreateCmd, nil, `{"uid":"u","status":"success",` + unknown + `}`},
	}
	file := filepath.Join(t.TempDir(), "in.yaml")
	// YAML input, with a field the generated types do not have and no
	// send_resolved, which a typed encode would add as false.
	if err := os.WriteFile(file, []byte("name: x\nfuture_field:\n  keep: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, gotBody, _ := logMetricsServer(t, tt.resp)
			out, err := runLogMetricsCmd(t, srv.URL, tt.cmd(), output.FormatJSON, append(tt.args, "-f", file)...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !jsonEqual(t, *gotBody, []byte(`{"name":"x","future_field":{"keep":true}}`)) {
				t.Errorf("sent body = %s", *gotBody)
			}
			if !strings.Contains(out, `"future_field"`) {
				t.Errorf("output dropped an unknown field: %s", out)
			}
		})
	}
}

func TestRawBodyGetPrintsUnknownFields(t *testing.T) {
	tests := []struct {
		name string
		cmd  func() *cobra.Command
		resp string
	}{
		{"monitors get", newMonitorsGetCmd, `{"name":"m","promql_query":"up","future_field":1}`},
		{"notifiers get", newNotifiersGetCmd, `{"name":"n","type":1,"future_field":1}`},
		{"dashboards get", newDashboardsGetCmd, `{"dashboard":{"title":"t"},"meta":{"folderUid":"f","future_field":1}}`},
		{"traces get", newTracesGetCmd, `{"data":[],"limit":0,"offset":0,"total":0,"future_field":1}`},
	}
	for _, tt := range tests {
		for _, format := range []output.Format{output.FormatJSON, output.FormatYAML} {
			t.Run(tt.name+" "+string(format), func(t *testing.T) {
				srv, _, _ := logMetricsServer(t, tt.resp)
				args := []string{"id1"}
				if tt.name == "traces get" {
					args = append(args, "--start", "-1h", "--end", "now")
				}
				out, err := runLogMetricsCmd(t, srv.URL, tt.cmd(), format, args...)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !strings.Contains(out, "future_field") {
					t.Errorf("output dropped an unknown field: %s", out)
				}
			})
		}
	}
}

func TestRawBodyTableStillWorks(t *testing.T) {
	srv, _, _ := logMetricsServer(t, `[{"id":"00000000-0000-0000-0000-000000000001","name":"cpu-high","promql_query":"up"}]`)
	out, err := runLogMetricsCmd(t, srv.URL, newMonitorsListCmd(), output.FormatTable)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cpu-high") || !strings.Contains(out, "NAME") {
		t.Errorf("table output = %q", out)
	}
}
