package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	azureIntegrationType = "AZURE_METRICS"

	// azureSecretMask is what the API returns in place of the stored client
	// secret. Sending it back on update tells the server to keep the secret it
	// already has, so an update that does not touch credentials round-trips the
	// mask rather than requiring the operator to re-enter it.
	azureSecretMask = "********"

	// azureClientSecretEnv supplies the client secret without putting it in
	// argv, where it would be visible in shell history and `ps` output.
	azureClientSecretEnv = "OODLE_AZURE_CLIENT_SECRET"

	azureDocsURL = "https://docs.oodle.ai/integrations/metrics/azure"
)

// newAzureCmd returns the `oodle integrations azure` subcommand tree, exposing
// CRUD against Azure Monitor metric-pull integrations. One integration record
// covers one Azure subscription.
func newAzureCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "azure",
		Aliases: []string{"azure-metrics"},
		Short:   "Manage Azure Monitor metric-pull integrations",
	}
	cmd.AddCommand(newAzureAddCmd())
	cmd.AddCommand(newAzureListCmd())
	cmd.AddCommand(newAzureUpdateCmd())
	cmd.AddCommand(newAzureDeleteCmd())
	return cmd
}

// azureListRow is the per-row projection rendered by `azure list`. Only fields
// useful at a glance; full payload is available via `-o json`.
type azureListRow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Subscription string `json:"subscription"`
	Services     string `json:"services"`
	Status       string `json:"status"`
}

func azureListColumns() []output.Column {
	return []output.Column{
		{Header: "ID", Field: "ID"},
		{Header: "NAME", Field: "Name"},
		{Header: "SUBSCRIPTION", Field: "Subscription"},
		{Header: "SERVICES", Field: "Services"},
		{Header: "STATUS", Field: "Status"},
	}
}

func newAzureAddCmd() *cobra.Command {
	var (
		file              string
		subscriptionName  string
		tenantID          string
		subscriptionID    string
		clientID          string
		clientSecret      string
		servicesFlag      string
		tagFlags          []string
		resourceGroups    string
		resourceNameRegex string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create an Azure Monitor metric-pull integration",
		Long: `Register an Azure subscription with Oodle for Azure Monitor metric pull.

Requires an Entra app registration (client ID + secret) holding the Monitoring
Reader role on the subscription. The backend validates the credentials against
Azure Resource Graph synchronously before persisting, retrying for up to five
minutes while a freshly created role assignment propagates — so this command
can take a while to return on a brand-new assignment.

The client secret is read from --client-secret, else the ` + azureClientSecretEnv + `
environment variable, else an interactive prompt. Prefer the latter two: a
secret passed as a flag is visible in shell history and process listings.

Service IDs are validated server-side against the supported catalog; see
` + azureDocsURL + ` for the list. Omitting --services collects the default set.

Pass --file to provide a full JSON/YAML payload instead of the flag-based form,
which is also the way to configure per-service tag filters.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)
			ctx := cmd.Context()

			var body client.CreateIntegrationsJSONRequestBody
			if file != "" {
				if err := readInputFile(file, &body); err != nil {
					return err
				}
			} else {
				if err := requireFlags(cmd, "subscription-name", "tenant-id", "subscription-id", "client-id"); err != nil {
					return err
				}
				secret, err := resolveAzureClientSecret(cmd, clientSecret, true)
				if err != nil {
					return err
				}
				body, err = buildAzureCreateBody(azureBuildInputs{
					subscriptionName:  subscriptionName,
					tenantID:          tenantID,
					subscriptionID:    subscriptionID,
					clientID:          clientID,
					clientSecret:      secret,
					services:          splitAndTrim(servicesFlag),
					tags:              tagFlags,
					resourceGroups:    splitAndTrim(resourceGroups),
					resourceNameRegex: resourceNameRegex,
				})
				if err != nil {
					return err
				}
			}

			resp, err := c.Inner.CreateIntegrationsWithResponse(ctx, instance, body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON/YAML file with the full CreateIntegrationRequest payload (mutually exclusive with the structured flags below)")
	cmd.Flags().StringVar(&subscriptionName, "subscription-name", "", "Display name for this subscription in Oodle")
	cmd.Flags().StringVar(&tenantID, "tenant-id", "", "Entra directory (tenant) ID")
	cmd.Flags().StringVar(&subscriptionID, "subscription-id", "", "Azure subscription ID to collect from")
	cmd.Flags().StringVar(&clientID, "client-id", "", "App registration application (client) ID")
	cmd.Flags().StringVar(&clientSecret, "client-secret", "", "App registration client secret. Falls back to $"+azureClientSecretEnv+", then an interactive prompt.")
	cmd.Flags().StringVar(&servicesFlag, "services", "", "Comma-separated Azure service IDs to collect (e.g. compute_virtualmachines,storage_storageaccounts). Empty collects the defaults.")
	cmd.Flags().StringArrayVar(&tagFlags, "tag", nil, "Tag filter applied to the selected services, as key=value. Repeat for multiple tags; a resource must carry all of them.")
	cmd.Flags().StringVar(&resourceGroups, "resource-groups", "", "Comma-separated resource group names to restrict collection to")
	cmd.Flags().StringVar(&resourceNameRegex, "resource-name-regex", "", "RE2 regex restricting collection to matching resource names")
	return cmd
}

func newAzureListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List Azure Monitor metric-pull integrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListIntegrationsWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return output.Print(cmd.OutOrStdout(), format, []azureListRow{}, azureListColumns())
			}

			rows := make([]azureListRow, 0, len(*resp.JSON200))
			for _, entry := range *resp.JSON200 {
				if entry.Type == nil || *entry.Type != azureIntegrationType {
					continue
				}
				rows = append(rows, azureRowFromIntegration(entry))
			}
			return output.Print(cmd.OutOrStdout(), format, rows, azureListColumns())
		},
	}
}

func newAzureUpdateCmd() *cobra.Command {
	var (
		file              string
		subscriptionName  string
		tenantID          string
		subscriptionID    string
		clientID          string
		clientSecret      string
		servicesFlag      string
		tagFlags          []string
		resourceGroups    string
		resourceNameRegex string
		statusFlag        string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an Azure Monitor metric-pull integration",
		Long: `Patch an existing Azure integration. Flag-based updates overlay the
provided fields on top of the integration's current configuration; omit a flag
to leave its value untouched. Pass --file to send a full PatchIntegration
payload instead.

The client secret is left unchanged unless --client-secret or $` + azureClientSecretEnv + `
is supplied; this command never prompts for it. The backend re-validates the
credentials against Azure on every update, including when the secret is
unchanged.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)
			ctx := cmd.Context()
			id := args[0]

			var body client.PatchIntegrationsByIdJSONRequestBody
			if file != "" {
				if err := readInputFile(file, &body); err != nil {
					return err
				}
			} else {
				existing, err := fetchAzureIntegration(ctx, c, instance, id)
				if err != nil {
					return err
				}
				secret, err := resolveAzureClientSecret(cmd, clientSecret, false)
				if err != nil {
					return err
				}
				body, err = buildAzurePatchBody(existing, azurePatchInputs{
					subscriptionName:  flagValue(cmd, "subscription-name", subscriptionName),
					tenantID:          flagValue(cmd, "tenant-id", tenantID),
					subscriptionID:    flagValue(cmd, "subscription-id", subscriptionID),
					clientID:          flagValue(cmd, "client-id", clientID),
					clientSecret:      secret,
					services:          flagSlice(cmd, "services", servicesFlag),
					tags:              tagFlags,
					tagsProvided:      cmd.Flags().Changed("tag"),
					resourceGroups:    flagSlice(cmd, "resource-groups", resourceGroups),
					resourceNameRegex: flagValue(cmd, "resource-name-regex", resourceNameRegex),
					status:            flagValue(cmd, "status", statusFlag),
				})
				if err != nil {
					return err
				}
			}

			resp, err := c.Inner.PatchIntegrationsByIdWithResponse(ctx, instance, id, body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON/YAML file with a full PatchIntegration payload")
	cmd.Flags().StringVar(&subscriptionName, "subscription-name", "", "Replacement display name for this subscription")
	cmd.Flags().StringVar(&tenantID, "tenant-id", "", "Replacement Entra directory (tenant) ID")
	cmd.Flags().StringVar(&subscriptionID, "subscription-id", "", "Replacement Azure subscription ID")
	cmd.Flags().StringVar(&clientID, "client-id", "", "Replacement app registration application (client) ID")
	cmd.Flags().StringVar(&clientSecret, "client-secret", "", "Replacement client secret. Falls back to $"+azureClientSecretEnv+"; omit both to keep the stored secret.")
	cmd.Flags().StringVar(&servicesFlag, "services", "", "Replacement comma-separated Azure service IDs. Replaces the entire service filter set, including any per-service tag filters.")
	cmd.Flags().StringArrayVar(&tagFlags, "tag", nil, "Replacement tag filter as key=value (repeatable). Supplying --tag at all clears existing tags before applying these.")
	cmd.Flags().StringVar(&resourceGroups, "resource-groups", "", "Replacement comma-separated resource group names")
	cmd.Flags().StringVar(&resourceNameRegex, "resource-name-regex", "", "Replacement RE2 resource name regex")
	cmd.Flags().StringVar(&statusFlag, "status", "", "Replacement integration status (e.g. ACTIVE, INACTIVE)")
	return cmd
}

func newAzureDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an Azure Monitor metric-pull integration",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			id := args[0]

			if !confirmAction(fmt.Sprintf("Delete Azure integration %q?", id), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteIntegrationsByIdWithResponse(cmd.Context(), instance, id)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted Azure integration %s\n", id)
			return nil
		},
	}
}

// --- helpers ---

type azureBuildInputs struct {
	subscriptionName  string
	tenantID          string
	subscriptionID    string
	clientID          string
	clientSecret      string
	services          []string
	tags              []string
	resourceGroups    []string
	resourceNameRegex string
}

func buildAzureCreateBody(in azureBuildInputs) (client.CreateIntegrationsJSONRequestBody, error) {
	az, err := buildAzurePayload(in)
	if err != nil {
		return client.CreateIntegrationsJSONRequestBody{}, err
	}
	var tsd client.CreateIntegrationRequest_TypeSpecificData
	if err := tsd.FromAzureMetricsIntegrationWrapper(client.AzureMetricsIntegrationWrapper{
		AzureMetricsIntegration: &az,
	}); err != nil {
		return client.CreateIntegrationsJSONRequestBody{}, fmt.Errorf("encoding typeSpecificData: %w", err)
	}
	// Status is deliberately omitted: the server applies the type's initial
	// status (NOT_CONNECTED) and the collector promotes it to RECEIVING once
	// the first scrape lands.
	return client.CreateIntegrationsJSONRequestBody{
		Type:             azureIntegrationType,
		TypeSpecificData: &tsd,
	}, nil
}

func buildAzurePayload(in azureBuildInputs) (client.AzureMetricsIntegration, error) {
	az := client.AzureMetricsIntegration{
		SubscriptionName: stringPtr(in.subscriptionName),
		TenantId:         stringPtr(in.tenantID),
		SubscriptionId:   stringPtr(in.subscriptionID),
		ClientId:         stringPtr(in.clientID),
		ClientSecret:     stringPtr(in.clientSecret),
		ResourceGroups:   stringSlicePtr(in.resourceGroups),
	}
	if in.resourceNameRegex != "" {
		az.ResourceNameRegex = stringPtr(in.resourceNameRegex)
	}
	filters, err := buildAzureServiceFilters(in.services, in.tags)
	if err != nil {
		return client.AzureMetricsIntegration{}, err
	}
	az.ServiceFilters = filters
	return az, nil
}

// buildAzureServiceFilters models the flag form as a single service-filter
// group. The API accepts a list of groups so each can carry its own tags; that
// shape is only reachable via --file.
func buildAzureServiceFilters(services, tags []string) (*[]client.AzureServiceFilter, error) {
	tagMap, err := parseAzureTagFlags(tags)
	if err != nil {
		return nil, err
	}
	if len(services) == 0 && tagMap == nil {
		return nil, nil
	}
	return &[]client.AzureServiceFilter{
		{
			ServiceIds: stringSlicePtr(services),
			Tags:       tagMap,
		},
	}, nil
}

type azurePatchInputs struct {
	subscriptionName  *string
	tenantID          *string
	subscriptionID    *string
	clientID          *string
	clientSecret      string
	services          *[]string
	tags              []string
	tagsProvided      bool
	resourceGroups    *[]string
	resourceNameRegex *string
	status            *string
}

// buildAzurePatchBody overlays user-provided patch flags onto the integration's
// existing Azure configuration. A nil pointer on azurePatchInputs means "flag
// not supplied, keep the existing value".
//
// The whole merged config is sent because the server rewrites every Azure
// column from the request body rather than merging field by field.
func buildAzurePatchBody(existing client.AzureMetricsIntegration, in azurePatchInputs) (client.PatchIntegrationsByIdJSONRequestBody, error) {
	updated := existing
	if in.subscriptionName != nil {
		updated.SubscriptionName = in.subscriptionName
	}
	if in.tenantID != nil {
		updated.TenantId = in.tenantID
	}
	if in.subscriptionID != nil {
		updated.SubscriptionId = in.subscriptionID
	}
	if in.clientID != nil {
		updated.ClientId = in.clientID
	}
	if in.resourceGroups != nil {
		updated.ResourceGroups = in.resourceGroups
	}
	if in.resourceNameRegex != nil {
		updated.ResourceNameRegex = in.resourceNameRegex
	}
	// No new secret means round-tripping the mask the GET returned, which the
	// server reads as "keep the stored secret".
	if in.clientSecret != "" {
		updated.ClientSecret = stringPtr(in.clientSecret)
	} else if updated.ClientSecret == nil {
		mask := azureSecretMask
		updated.ClientSecret = &mask
	}

	// Service filters are a *list* of (serviceIds, tags) groups, and the flag
	// form models a single group. Passing --services is the explicit "replace
	// the whole set" action and is allowed to collapse the list. Passing --tag
	// alone is not: there is no way to tell which group the tags belong to, so
	// refuse rather than silently pick one and drop the rest.
	if in.services != nil || in.tagsProvided {
		if in.services == nil && updated.ServiceFilters != nil && len(*updated.ServiceFilters) > 1 {
			return client.PatchIntegrationsByIdJSONRequestBody{}, fmt.Errorf(
				"refusing to patch tags on an integration with %d service filter groups: re-specify the full set with --services (or use -f file.json for per-service tag filters)",
				len(*updated.ServiceFilters),
			)
		}
		services := derefStringSlice(in.services)
		if in.services == nil {
			services = existingFirstServiceIDs(updated)
		}
		filters, err := buildAzureServiceFilters(services, in.tags)
		if err != nil {
			return client.PatchIntegrationsByIdJSONRequestBody{}, err
		}
		updated.ServiceFilters = filters
	}

	var tsd client.PatchIntegration_TypeSpecificData
	if err := tsd.FromAzureMetricsIntegrationWrapper(client.AzureMetricsIntegrationWrapper{
		AzureMetricsIntegration: &updated,
	}); err != nil {
		return client.PatchIntegrationsByIdJSONRequestBody{}, fmt.Errorf("encoding typeSpecificData: %w", err)
	}
	typ := azureIntegrationType
	body := client.PatchIntegrationsByIdJSONRequestBody{
		Type:             &typ,
		TypeSpecificData: &tsd,
	}
	if in.status != nil {
		body.Status = in.status
	}
	return body, nil
}

func existingFirstServiceIDs(az client.AzureMetricsIntegration) []string {
	if az.ServiceFilters == nil || len(*az.ServiceFilters) == 0 {
		return nil
	}
	first := (*az.ServiceFilters)[0]
	if first.ServiceIds == nil {
		return nil
	}
	return *first.ServiceIds
}

// fetchAzureIntegration GETs the integration and asserts it is an AZURE_METRICS
// variant, returning the inner Azure payload. The ClientSecret it carries is
// the server's mask, not the real secret.
func fetchAzureIntegration(ctx context.Context, c *api.Client, instance, id string) (client.AzureMetricsIntegration, error) {
	resp, err := c.Inner.GetIntegrationsByIdWithResponse(ctx, instance, id)
	if err != nil {
		return client.AzureMetricsIntegration{}, fmt.Errorf("fetching integration %s: %w", id, err)
	}
	if resp.StatusCode() >= 300 {
		return client.AzureMetricsIntegration{}, api.CheckResponse(resp.HTTPResponse, resp.Body)
	}
	if resp.JSON200 == nil {
		return client.AzureMetricsIntegration{}, fmt.Errorf("integration %s: empty response", id)
	}
	got := resp.JSON200
	if got.Type == nil || *got.Type != azureIntegrationType {
		return client.AzureMetricsIntegration{}, fmt.Errorf("integration %s is not a %s integration", id, azureIntegrationType)
	}
	if got.TypeSpecificData == nil {
		return client.AzureMetricsIntegration{}, fmt.Errorf("integration %s: missing typeSpecificData", id)
	}
	wrapper, err := got.TypeSpecificData.AsAzureMetricsIntegrationWrapper()
	if err != nil {
		return client.AzureMetricsIntegration{}, fmt.Errorf("decoding typeSpecificData for %s: %w", id, err)
	}
	if wrapper.AzureMetricsIntegration == nil {
		return client.AzureMetricsIntegration{}, fmt.Errorf("integration %s: empty azureMetricsIntegration", id)
	}
	return *wrapper.AzureMetricsIntegration, nil
}

// resolveAzureClientSecret sources the client secret from --client-secret, then
// $OODLE_AZURE_CLIENT_SECRET, then an interactive prompt.
//
// The prompt only applies when the secret is required (create). On update an
// absent secret means "keep the stored one", so prompting would turn a routine
// patch into a credential re-entry.
func resolveAzureClientSecret(cmd *cobra.Command, flagVal string, required bool) (string, error) {
	if cmd.Flags().Changed("client-secret") {
		// An explicitly empty --client-secret is a mistake worth naming here:
		// left alone it reaches the server as an absent field and comes back as
		// a bare "clientSecret is required", which does not point at the flag.
		if flagVal == "" {
			return "", fmt.Errorf("--client-secret was given an empty value: omit it to keep the stored secret, or set %s", azureClientSecretEnv)
		}
		return flagVal, nil
	}
	if env := os.Getenv(azureClientSecretEnv); env != "" {
		return env, nil
	}
	if !required {
		return "", nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("missing client secret: pass --client-secret or set %s", azureClientSecretEnv)
	}
	fmt.Fprint(cmd.ErrOrStderr(), "Azure client secret: ")
	secret, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Fprintln(cmd.ErrOrStderr())
	if err != nil {
		return "", fmt.Errorf("reading client secret: %w", err)
	}
	entered := strings.TrimSpace(string(secret))
	if entered == "" {
		return "", fmt.Errorf("missing client secret: pass --client-secret or set %s", azureClientSecretEnv)
	}
	return entered, nil
}

func parseAzureTagFlags(tags []string) (*map[string]string, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(tags))
	for _, raw := range tags {
		k, v, ok := strings.Cut(raw, "=")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --tag %q: expected key=value", raw)
		}
		out[k] = v
	}
	return &out, nil
}

func azureRowFromIntegration(entry client.Integration) azureListRow {
	row := azureListRow{}
	if entry.Id != nil {
		row.ID = *entry.Id
	}
	if entry.Name != nil {
		row.Name = *entry.Name
	}
	if entry.Status != nil {
		row.Status = *entry.Status
	}
	if entry.TypeSpecificData == nil {
		return row
	}
	wrapper, err := entry.TypeSpecificData.AsAzureMetricsIntegrationWrapper()
	if err != nil || wrapper.AzureMetricsIntegration == nil {
		return row
	}
	az := wrapper.AzureMetricsIntegration
	if az.SubscriptionId != nil {
		row.Subscription = *az.SubscriptionId
	}
	if az.ServiceFilters != nil {
		var ids []string
		for _, f := range *az.ServiceFilters {
			if f.ServiceIds != nil {
				ids = append(ids, *f.ServiceIds...)
			}
		}
		row.Services = strings.Join(ids, ",")
	}
	return row
}
