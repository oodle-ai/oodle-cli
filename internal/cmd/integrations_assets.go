package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// The server finds the dashboards of an integration by a directory
// name and the monitors by a fixed name, and both compare case. The
// setup specs and the integration types use other names for the same
// integration. Without this table an agent that passes the spec type
// gets "no dashboards" and thinks that the integration has none.
var integrationAssetNames = map[string]string{
	"ai":                     "AI",
	"aws":                    "AWS",
	"cloudwatch":             "AWS",
	"cloudwatch_dashboard":   "AWS",
	"cloudwatch_metric_pull": "AWS",
	"azure":                  "Azure",
	"azure_metrics":          "Azure",
	"cockroachdb":            "CockroachDB",
	"crdb_cloud":             "CockroachDB",
	"confluent-cloud":        "Confluent-Cloud",
	"confluent_cloud":        "Confluent-Cloud",
	"datadog":                "Datadog",
	"ebpf":                   "eBPF",
	"gcp":                    "GCP",
	"gpu":                    "GPU",
	"gpu_monitoring":         "GPU",
	"infrastructure":         "Infrastructure",
	"kubernetes":             "Kubernetes",
	"eks":                    "Kubernetes",
	"openlit":                "OpenLIT",
	"otel-demo":              "OTel-Demo",
	"otel_demo":              "OTel-Demo",
	"rum":                    "RUM",
	"temporal":               "Temporal",
}

// The import can take longer than a normal API call, because the
// server writes each dashboard of the integration one after another.
const integrationAssetsTimeout = 3 * time.Minute

// resolveIntegrationAssetName returns the name that the server knows
// for an integration. A name that is not in the table goes to the
// server without change, so a new integration works before the CLI
// knows it.
func resolveIntegrationAssetName(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if v, ok := integrationAssetNames[key]; ok {
		return v
	}
	return strings.TrimSpace(name)
}

// importAssetsResult is what `import-assets` prints. Each list is
// sorted, so that two runs give the same output.
type importAssetsResult struct {
	Integration string                 `json:"integration"`
	Dashboards  *importDashboardsState `json:"dashboards,omitempty"`
	Monitors    *importMonitorsState   `json:"monitors,omitempty"`
}

type importDashboardsState struct {
	// Status is "created", "updated" or "none".
	Status    string `json:"status"`
	FolderUID string `json:"folderUid,omitempty"`
}

type importMonitorsState struct {
	Created []string `json:"created"`
	Existed []string `json:"existed"`
	// NotRecreated holds the recommended monitors that are not on the
	// instance after the import. The server installs the monitors of
	// an integration one time only, so a monitor that a user deleted
	// stays deleted.
	NotRecreated []string `json:"notRecreated"`
}

func newIntegrationsImportAssetsCmd() *cobra.Command {
	var (
		dashboards      bool
		monitors        bool
		dashboardPrefix []string
	)
	cmd := &cobra.Command{
		Use:   "import-assets <integration>",
		Short: "Import the Oodle dashboards and monitors of an integration",
		Long: `Import the dashboards and the recommended monitors that Oodle provides
for an integration. This is the same operation as "Provision & View
Dashboards" and "Provision Recommended Monitors" on the integration card.

<integration> is the integration name (Kubernetes, Temporal, CockroachDB,
AWS, Azure, GCP, Confluent-Cloud, OpenLIT, ...) or its setup-spec type
(kubernetes, temporal, cloudwatch, azure_metrics, ...).

With no --dashboards or --monitors flag, the command imports both, and
skips a kind that the integration does not have.

You can run it again. The dashboards go to the folder of the integration,
and an import replaces them with the Oodle version, so changes to these
dashboards are lost. Monitors are matched by name, and an existing monitor
is not changed. Oodle installs the monitors of an integration one time
only: a monitor that a user deleted is not created again. The output
lists these monitors under "notRecreated".

Needs an Editor or Admin key.`,
		Example: `  oodle integrations import-assets kubernetes
  oodle integrations import-assets Temporal --dashboards
  oodle integrations import-assets cockroachdb --monitors -o json
  oodle integrations import-assets AWS --dashboard-prefix ec2.json --dashboard-prefix rds`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := resolveIntegrationAssetName(args[0])
			if name == "" {
				return fmt.Errorf("integration name is empty")
			}
			explicit := dashboards || monitors
			if !explicit {
				dashboards, monitors = true, true
			}
			a, err := newAssetsAPI(cmd)
			if err != nil {
				return err
			}

			result := importAssetsResult{Integration: name}
			if dashboards {
				state, err := a.importDashboards(name, dashboardPrefix)
				if err != nil {
					return err
				}
				if state.Status == "none" && explicit {
					return fmt.Errorf("Oodle has no dashboards for integration %q (names compare case; see --help)", name)
				}
				result.Dashboards = state
			}
			if monitors {
				state, err := a.importMonitors(name)
				if err != nil {
					return err
				}
				if state == nil && explicit {
					return fmt.Errorf("Oodle has no recommended monitors for integration %q (names compare case; see --help)", name)
				}
				result.Monitors = state
			}
			return printImportAssetsResult(cmd, result, monitors)
		},
	}
	cmd.Flags().BoolVar(&dashboards, "dashboards", false, "Import the dashboards")
	cmd.Flags().BoolVar(&monitors, "monitors", false, "Import the recommended monitors")
	cmd.Flags().StringArrayVar(&dashboardPrefix, "dashboard-prefix", nil, "Import only the dashboards whose file name starts with this prefix (repeat for more), e.g. ec2.json for AWS")
	return cmd
}

func newIntegrationsRemoveAssetsCmd() *cobra.Command {
	var (
		dashboards bool
		monitors   bool
	)
	cmd := &cobra.Command{
		Use:   "remove-assets <integration>",
		Short: "Delete the Oodle dashboards and monitors of an integration",
		Long: `Delete what import-assets created: the dashboard folder of the
integration with all the dashboards in it, and the monitors that carry the
label oodle_integration=<integration>. With no --dashboards or --monitors
flag, the command deletes both.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := resolveIntegrationAssetName(args[0])
			if !dashboards && !monitors {
				dashboards, monitors = true, true
			}
			if !confirmAction(fmt.Sprintf("Delete the Oodle assets of integration %q?", name), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			a, err := newAssetsAPI(cmd)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if dashboards {
				body, err := a.do(http.MethodDelete, a.integrationPath(name, "manage-dashboards"), nil)
				if err != nil {
					return err
				}
				var resp struct {
					DashboardFolderID string `json:"dashboardFolderId"`
				}
				_ = json.Unmarshal(body, &resp)
				fmt.Fprintf(out, "Deleted dashboard folder %s\n", resp.DashboardFolderID)
			}
			if monitors {
				body, err := a.do(http.MethodDelete, a.integrationPath(name, "manage-monitors"), nil)
				if err != nil {
					return err
				}
				var resp struct {
					DeletedCount int `json:"deleted_count"`
				}
				_ = json.Unmarshal(body, &resp)
				fmt.Fprintf(out, "Deleted %d monitors\n", resp.DeletedCount)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dashboards, "dashboards", false, "Delete the dashboard folder")
	cmd.Flags().BoolVar(&monitors, "monitors", false, "Delete the monitors")
	return cmd
}

// assetsAPI calls the integration asset routes. These routes are not in
// the OpenAPI spec that the generated client comes from.
type assetsAPI struct {
	cmd      *cobra.Command
	c        *api.Client
	http     *http.Client
	base     string
	instance string
}

func newAssetsAPI(cmd *cobra.Command) (*assetsAPI, error) {
	c := getClient(cmd)
	if c == nil || c.Config == nil {
		return nil, fmt.Errorf("no API client configured")
	}
	instance := getInstance(cmd)
	return &assetsAPI{
		cmd:      cmd,
		c:        c,
		http:     c.NewAuthedHTTPClient(integrationAssetsTimeout),
		base:     strings.TrimRight(c.Config.APIURL, "/") + "/v1/api/instance/" + url.PathEscape(instance),
		instance: instance,
	}, nil
}

func (a *assetsAPI) integrationPath(name, route string) string {
	return "/integrations/" + url.PathEscape(name) + "/" + route
}

// do sends one request and returns the body of a 2xx response.
func (a *assetsAPI) do(method, path string, query url.Values) ([]byte, error) {
	u := a.base + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(a.cmd.Context(), method, u, nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if err := api.CheckResponse(resp, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (a *assetsAPI) importDashboards(name string, prefixes []string) (*importDashboardsState, error) {
	// The folder list before the import tells a first import from a
	// repeated one. The server answers the same for both.
	before, err := a.folderUIDs()
	if err != nil {
		return nil, err
	}
	var query url.Values
	if len(prefixes) > 0 {
		query = url.Values{"dashboardPrefixesFilter": prefixes}
	}
	body, err := a.do(http.MethodPost, a.integrationPath(name, "manage-dashboards"), query)
	if err != nil {
		return nil, err
	}
	var resp struct {
		DashboardFolderID string `json:"dashboardFolderId"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing dashboard response: %w", err)
	}
	// An empty folder means that the server found no dashboard files
	// for this name (or for these prefixes).
	if resp.DashboardFolderID == "" {
		return &importDashboardsState{Status: "none"}, nil
	}
	status := "created"
	if before[resp.DashboardFolderID] {
		status = "updated"
	}
	return &importDashboardsState{Status: status, FolderUID: resp.DashboardFolderID}, nil
}

func (a *assetsAPI) folderUIDs() (map[string]bool, error) {
	resp, err := a.c.Inner.ListFoldersWithResponse(a.cmd.Context(), a.instance)
	if err != nil {
		return nil, fmt.Errorf("listing folders: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return nil, api.CheckResponse(resp.HTTPResponse, resp.Body)
	}
	uids := map[string]bool{}
	if resp.JSON200 != nil {
		for _, f := range *resp.JSON200 {
			uids[f.Uid] = true
		}
	}
	return uids, nil
}

type namedMonitor struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
}

// importMonitors returns nil when Oodle has no recommended monitors for
// the integration.
func (a *assetsAPI) importMonitors(name string) (*importMonitorsState, error) {
	path := a.integrationPath(name, "manage-monitors")
	body, err := a.do(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var recommended []namedMonitor
	if err := json.Unmarshal(body, &recommended); err != nil {
		return nil, fmt.Errorf("parsing recommended monitors: %w", err)
	}
	if len(recommended) == 0 {
		return nil, nil
	}

	before, err := a.integrationMonitorNames(name)
	if err != nil {
		return nil, err
	}
	body, err = a.do(http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}
	var installed []namedMonitor
	if err := json.Unmarshal(body, &installed); err != nil {
		return nil, fmt.Errorf("parsing installed monitors: %w", err)
	}
	return diffMonitors(recommended, before, installed), nil
}

// integrationMonitorNames returns the names of the monitors on the
// instance that carry the label of the integration.
func (a *assetsAPI) integrationMonitorNames(name string) (map[string]bool, error) {
	resp, err := a.c.Inner.ListMonitors(a.cmd.Context(), a.instance)
	if err != nil {
		return nil, fmt.Errorf("listing monitors: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading monitors: %w", err)
	}
	if err := api.CheckResponse(resp, body); err != nil {
		return nil, err
	}
	var all []namedMonitor
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, fmt.Errorf("parsing monitors: %w", err)
	}
	names := map[string]bool{}
	for _, m := range all {
		if m.Labels["oodle_integration"] == name {
			names[m.Name] = true
		}
	}
	return names, nil
}

func diffMonitors(recommended []namedMonitor, before map[string]bool, installed []namedMonitor) *importMonitorsState {
	state := &importMonitorsState{Created: []string{}, Existed: []string{}, NotRecreated: []string{}}
	after := map[string]bool{}
	for _, m := range installed {
		after[m.Name] = true
		if before[m.Name] {
			state.Existed = append(state.Existed, m.Name)
		} else {
			state.Created = append(state.Created, m.Name)
		}
	}
	for _, m := range recommended {
		if !after[m.Name] {
			state.NotRecreated = append(state.NotRecreated, m.Name)
		}
	}
	sort.Strings(state.Created)
	sort.Strings(state.Existed)
	sort.Strings(state.NotRecreated)
	return state
}

func printImportAssetsResult(cmd *cobra.Command, r importAssetsResult, monitorsAsked bool) error {
	format := getOutputFormat(cmd)
	switch format {
	case output.FormatJSON, output.FormatYAML:
		return output.Print(cmd.OutOrStdout(), format, r, nil)
	}
	var b bytes.Buffer
	if d := r.Dashboards; d != nil {
		switch d.Status {
		case "none":
			fmt.Fprintf(&b, "Dashboards: Oodle has no dashboards for %s.\n", r.Integration)
		case "updated":
			fmt.Fprintf(&b, "Dashboards: the folder %s existed. Its dashboards now have the Oodle version again.\n", d.FolderUID)
		default:
			fmt.Fprintf(&b, "Dashboards: imported into the new folder %s.\n", d.FolderUID)
		}
	}
	if monitorsAsked {
		if m := r.Monitors; m == nil {
			fmt.Fprintf(&b, "Monitors: Oodle has no recommended monitors for %s.\n", r.Integration)
		} else {
			fmt.Fprintf(&b, "Monitors: %d created, %d existed already.\n", len(m.Created), len(m.Existed))
			for _, n := range m.Created {
				fmt.Fprintf(&b, "  created: %s\n", n)
			}
			if len(m.NotRecreated) > 0 {
				fmt.Fprintf(&b, "  %d recommended monitors are not on the instance. They were installed before and deleted, and Oodle does not create them again:\n", len(m.NotRecreated))
				for _, n := range m.NotRecreated {
					fmt.Fprintf(&b, "  not created: %s\n", n)
				}
			}
		}
	}
	_, err := cmd.OutOrStdout().Write(b.Bytes())
	return err
}
