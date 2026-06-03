package cmd

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	awsIntegrationType            = "CLOUDWATCH_METRIC_PULL"
	defaultOodleAWSAccountID      = "052799302239"
	defaultCFStackRegion          = "us-west-2"
	defaultIAMRoleName            = "OodleIntegrationRole"
	cfTemplateURL                 = "https://s3.us-west-2.amazonaws.com/oodle-configs/aws/aws_integration_iam_role.yaml"
	cfStackNamePrefix             = "oodle-aws-integration-role-setup"
	cfStackSuffixAlphabet         = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	cfStackSuffixLen              = 6
)

// cfStackSuffix returns a random 6-char alphanumeric suffix appended to the
// generated stack name. Package-level var so tests can substitute a
// deterministic value.
var cfStackSuffix = func() string {
	b := make([]byte, cfStackSuffixLen)
	max := big.NewInt(int64(len(cfStackSuffixAlphabet)))
	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			// crypto/rand failure is unrecoverable here; surface via a
			// zero-suffix fallback rather than panicking the CLI.
			return ""
		}
		b[i] = cfStackSuffixAlphabet[n.Int64()]
	}
	return string(b)
}

// newAwsCmd returns the `oodle integrations aws` subcommand tree, exposing
// CRUD against AWS CloudWatch metric-pull integrations plus a helper to
// generate the CloudFormation launch URL for the IAM role.
func newAwsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Manage AWS CloudWatch metric-pull integrations",
	}
	cmd.AddCommand(newAwsAddCmd())
	cmd.AddCommand(newAwsListCmd())
	cmd.AddCommand(newAwsUpdateCmd())
	cmd.AddCommand(newAwsDeleteCmd())
	cmd.AddCommand(newAwsGenerateSetupURLCmd())
	return cmd
}

// awsListRow is the per-row projection rendered by `aws list`. Only fields
// useful at a glance; full payload is available via `-o json`.
type awsListRow struct {
	ID      string `json:"id"`
	Account string `json:"account"`
	Regions string `json:"regions"`
	Status  string `json:"status"`
}

func awsListColumns() []output.Column {
	return []output.Column{
		{Header: "ID", Field: "ID"},
		{Header: "ACCOUNT", Field: "Account"},
		{Header: "REGIONS", Field: "Regions"},
		{Header: "STATUS", Field: "Status"},
	}
}

func newAwsAddCmd() *cobra.Command {
	var (
		file             string
		accountID        string
		roleArn          string
		externalID       string
		regionsFlag      string
		namespacesFlag   string
		tagFlags         []string
		cfStackRegion    string
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create an AWS CloudWatch metric-pull integration",
		Long: `Register an AWS account with Oodle for CloudWatch metric pull.

The backend validates IAM role assumption synchronously before persisting,
so a misconfigured role surfaces immediately in the response. Pass --file
to provide a full JSON/YAML payload instead of the flag-based form.`,
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
				if err := requireFlags(cmd, "account-id", "role-arn", "regions", "namespaces"); err != nil {
					return err
				}
				resolvedExternalID, err := resolveExternalID(ctx, c, instance, externalID)
				if err != nil {
					return err
				}
				body, err = buildAwsCreateBody(awsBuildInputs{
					accountID:     accountID,
					roleArn:       roleArn,
					externalID:    resolvedExternalID,
					regions:       splitAndTrim(regionsFlag),
					namespaces:    splitAndTrim(namespacesFlag),
					tags:          tagFlags,
					cfStackRegion: cfStackRegion,
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
	cmd.Flags().StringVar(&accountID, "account-id", "", "12-digit AWS account ID")
	cmd.Flags().StringVar(&roleArn, "role-arn", "", "ARN of the IAM role Oodle should assume")
	cmd.Flags().StringVar(&externalID, "external-id", "", "External ID for the trust policy. If empty, reuses the workspace's existing AWS integration external ID; if none exists, a fresh UUIDv7 is generated.")
	cmd.Flags().StringVar(&regionsFlag, "regions", "", "Comma-separated AWS regions to scrape (e.g. us-west-2,us-east-1)")
	cmd.Flags().StringVar(&namespacesFlag, "namespaces", "", "Comma-separated CloudWatch namespaces (e.g. AWS/EC2,AWS/RDS)")
	cmd.Flags().StringArrayVar(&tagFlags, "tag", nil, "Tag filter applied to all namespaces, as key=value. Repeat for multiple tags.")
	cmd.Flags().StringVar(&cfStackRegion, "cf-stack-region", defaultCFStackRegion, "Region recorded as launchCFStackRegion on the integration")
	return cmd
}

func newAwsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List AWS CloudWatch metric-pull integrations",
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
				return output.Print(cmd.OutOrStdout(), format, []awsListRow{}, awsListColumns())
			}

			rows := make([]awsListRow, 0, len(*resp.JSON200))
			for _, entry := range *resp.JSON200 {
				if entry.Type == nil || *entry.Type != awsIntegrationType {
					continue
				}
				rows = append(rows, awsRowFromIntegration(entry))
			}
			return output.Print(cmd.OutOrStdout(), format, rows, awsListColumns())
		},
	}
}

func newAwsUpdateCmd() *cobra.Command {
	var (
		file             string
		roleArn          string
		externalID       string
		regionsFlag      string
		namespacesFlag   string
		tagFlags         []string
		cfStackRegion    string
		statusFlag       string
	)
	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an AWS CloudWatch metric-pull integration",
		Long: `Patch an existing AWS integration. Flag-based updates overlay the
provided fields on top of the integration's current configuration; omit a
flag to leave its value untouched. Pass --file to send a full PatchIntegration
payload instead.`,
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
				existing, err := fetchAwsIntegration(ctx, c, instance, id)
				if err != nil {
					return err
				}
				body, err = buildAwsPatchBody(existing, awsPatchInputs{
					roleArn:       flagValue(cmd, "role-arn", roleArn),
					externalID:    flagValue(cmd, "external-id", externalID),
					regions:       flagSlice(cmd, "regions", regionsFlag),
					namespaces:    flagSlice(cmd, "namespaces", namespacesFlag),
					tags:          tagFlags,
					cfStackRegion: flagValue(cmd, "cf-stack-region", cfStackRegion),
					status:        flagValue(cmd, "status", statusFlag),
					tagsProvided:  cmd.Flags().Changed("tag"),
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
	cmd.Flags().StringVar(&roleArn, "role-arn", "", "Replacement IAM role ARN")
	cmd.Flags().StringVar(&externalID, "external-id", "", "Replacement external ID for the trust policy")
	cmd.Flags().StringVar(&regionsFlag, "regions", "", "Replacement comma-separated AWS regions")
	cmd.Flags().StringVar(&namespacesFlag, "namespaces", "", "Replacement comma-separated CloudWatch namespaces")
	cmd.Flags().StringArrayVar(&tagFlags, "tag", nil, "Replacement tag filter as key=value (repeatable). Supplying --tag at all clears existing tags before applying these.")
	cmd.Flags().StringVar(&cfStackRegion, "cf-stack-region", "", "Replacement launchCFStackRegion value")
	cmd.Flags().StringVar(&statusFlag, "status", "", "Replacement integration status (e.g. ACTIVE, INACTIVE)")
	return cmd
}

func newAwsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an AWS CloudWatch metric-pull integration",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			id := args[0]

			if !confirmAction(fmt.Sprintf("Delete AWS integration %q?", id), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteIntegrationsByIdWithResponse(cmd.Context(), instance, id)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted AWS integration %s\n", id)
			return nil
		},
	}
}

func newAwsGenerateSetupURLCmd() *cobra.Command {
	var (
		accountID       string
		cfRegion        string
		roleName        string
		externalID      string
		oodleAccountID  string
	)
	cmd := &cobra.Command{
		Use:   "generate-setup-url",
		Short: "Print the CloudFormation launch URL for the Oodle IAM role",
		Long: `Construct the AWS console CloudFormation "Create Stack" URL that deploys
the Oodle integration IAM role into the caller's account.

If --external-id is omitted and an existing AWS integration is configured in
this workspace, that integration's externalId is reused so the resulting CF
stack trusts the same workspace as the existing accounts. If no integration
exists, a fresh UUIDv7 is generated.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			ctx := cmd.Context()

			resolvedExternalID, err := resolveExternalID(ctx, c, instance, externalID)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "External ID: %s\n", resolvedExternalID)
			fmt.Fprintf(out, "URL: %s\n", buildCFLaunchURL(resolvedExternalID, oodleAccountID, cfRegion, roleName))
			return nil
		},
	}
	cmd.Flags().StringVar(&accountID, "account-id", "", "12-digit AWS account ID (recorded for the operator; not embedded in the URL)")
	cmd.Flags().StringVar(&cfRegion, "cf-region", defaultCFStackRegion, "Region to deploy the CloudFormation stack into")
	cmd.Flags().StringVar(&roleName, "role-name", defaultIAMRoleName, "IAM role name CloudFormation should create")
	cmd.Flags().StringVar(&externalID, "external-id", "", "External ID to embed in the trust policy. If empty, reuses the workspace's existing AWS integration external ID; if none exists, a fresh UUIDv7 is generated.")
	cmd.Flags().StringVar(&oodleAccountID, "oodle-aws-account", defaultOodleAWSAccountID, "Oodle's AWS account ID that the role trusts")
	_ = cmd.MarkFlagRequired("account-id")
	return cmd
}

// --- helpers ---

type awsBuildInputs struct {
	accountID     string
	roleArn       string
	externalID    string
	regions       []string
	namespaces    []string
	tags          []string
	cfStackRegion string
}

func buildAwsCreateBody(in awsBuildInputs) (client.CreateIntegrationsJSONRequestBody, error) {
	cw, err := buildCloudWatchPayload(in)
	if err != nil {
		return client.CreateIntegrationsJSONRequestBody{}, err
	}
	wrapper := client.CloudWatchMetricPullIntegrationWrapper{
		CloudWatchMetricPullIntegration: &cw,
	}
	var tsd client.CreateIntegrationRequest_TypeSpecificData
	if err := tsd.FromCloudWatchMetricPullIntegrationWrapper(wrapper); err != nil {
		return client.CreateIntegrationsJSONRequestBody{}, fmt.Errorf("encoding typeSpecificData: %w", err)
	}
	status := "INACTIVE"
	return client.CreateIntegrationsJSONRequestBody{
		Type:             awsIntegrationType,
		Status:           &status,
		TypeSpecificData: &tsd,
	}, nil
}

func buildCloudWatchPayload(in awsBuildInputs) (client.CloudWatchMetricPullIntegration, error) {
	tagPairs, err := parseTagFlags(in.tags)
	if err != nil {
		return client.CloudWatchMetricPullIntegration{}, err
	}
	resourceTypes := []client.CloudWatchResourceTypeSearchTags{
		{
			ResourceTypes: stringSlicePtr(in.namespaces),
			SearchTags:    tagPairs,
		},
	}
	cw := client.CloudWatchMetricPullIntegration{
		AccountId:                   stringPtr(in.accountID),
		ExternalId:                  stringPtr(in.externalID),
		RoleArn:                     stringPtr(in.roleArn),
		Regions:                     stringSlicePtr(in.regions),
		ResourceTypesSearchTagsList: &resourceTypes,
	}
	if in.cfStackRegion != "" {
		cw.LaunchCFStackRegion = stringPtr(in.cfStackRegion)
	}
	return cw, nil
}

type awsPatchInputs struct {
	roleArn       *string
	externalID    *string
	regions       *[]string
	namespaces    *[]string
	tags          []string
	tagsProvided  bool
	cfStackRegion *string
	status        *string
}

// buildAwsPatchBody overlays user-provided patch flags onto the integration's
// existing CloudWatch configuration. A nil pointer on awsPatchInputs means
// "flag not supplied, keep the existing value".
func buildAwsPatchBody(existing client.CloudWatchMetricPullIntegration, in awsPatchInputs) (client.PatchIntegrationsByIdJSONRequestBody, error) {
	updated := existing
	if in.roleArn != nil {
		updated.RoleArn = in.roleArn
	}
	if in.externalID != nil {
		updated.ExternalId = in.externalID
	}
	if in.regions != nil {
		updated.Regions = in.regions
	}
	if in.cfStackRegion != nil {
		updated.LaunchCFStackRegion = in.cfStackRegion
	}

	// Resource type list is the trickiest field: it is a *list* of
	// (resourceTypes, searchTags) entries. The flag-based form models a
	// single entry. Refuse to patch via flags when the existing integration
	// has multiple entries — we can't preserve them without dropping data,
	// and the user almost certainly does not want a silent collapse.
	if in.namespaces != nil || in.tagsProvided {
		if in.namespaces == nil && existing.ResourceTypesSearchTagsList != nil && len(*existing.ResourceTypesSearchTagsList) > 1 {
			return client.PatchIntegrationsByIdJSONRequestBody{}, fmt.Errorf(
				"refusing to patch tags on an integration with %d namespace entries: re-specify the full set with --namespaces (or use -f file.json for per-namespace tag filters)",
				len(*existing.ResourceTypesSearchTagsList),
			)
		}
		tagPairs, err := parseTagFlags(in.tags)
		if err != nil {
			return client.PatchIntegrationsByIdJSONRequestBody{}, err
		}
		namespaces := derefStringSlice(in.namespaces)
		if in.namespaces == nil {
			namespaces = existingFirstResourceTypes(existing)
		}
		updated.ResourceTypesSearchTagsList = &[]client.CloudWatchResourceTypeSearchTags{
			{
				ResourceTypes: stringSlicePtr(namespaces),
				SearchTags:    tagPairs,
			},
		}
	}

	wrapper := client.CloudWatchMetricPullIntegrationWrapper{
		CloudWatchMetricPullIntegration: &updated,
	}
	var tsd client.PatchIntegration_TypeSpecificData
	if err := tsd.FromCloudWatchMetricPullIntegrationWrapper(wrapper); err != nil {
		return client.PatchIntegrationsByIdJSONRequestBody{}, fmt.Errorf("encoding typeSpecificData: %w", err)
	}
	typ := awsIntegrationType
	body := client.PatchIntegrationsByIdJSONRequestBody{
		Type:             &typ,
		TypeSpecificData: &tsd,
	}
	if in.status != nil {
		body.Status = in.status
	}
	return body, nil
}

func existingFirstResourceTypes(cw client.CloudWatchMetricPullIntegration) []string {
	if cw.ResourceTypesSearchTagsList == nil || len(*cw.ResourceTypesSearchTagsList) == 0 {
		return nil
	}
	first := (*cw.ResourceTypesSearchTagsList)[0]
	if first.ResourceTypes == nil {
		return nil
	}
	return *first.ResourceTypes
}

// fetchAwsIntegration GETs the integration and asserts it is a
// CLOUDWATCH_METRIC_PULL variant, returning the inner CloudWatch payload.
func fetchAwsIntegration(ctx context.Context, c *api.Client, instance, id string) (client.CloudWatchMetricPullIntegration, error) {
	resp, err := c.Inner.GetIntegrationsByIdWithResponse(ctx, instance, id)
	if err != nil {
		return client.CloudWatchMetricPullIntegration{}, fmt.Errorf("fetching integration %s: %w", id, err)
	}
	if resp.StatusCode() >= 300 {
		return client.CloudWatchMetricPullIntegration{}, api.CheckResponse(resp.HTTPResponse, resp.Body)
	}
	if resp.JSON200 == nil {
		return client.CloudWatchMetricPullIntegration{}, fmt.Errorf("integration %s: empty response", id)
	}
	got := resp.JSON200
	if got.Type == nil || *got.Type != awsIntegrationType {
		return client.CloudWatchMetricPullIntegration{}, fmt.Errorf("integration %s is not a %s integration", id, awsIntegrationType)
	}
	if got.TypeSpecificData == nil {
		return client.CloudWatchMetricPullIntegration{}, fmt.Errorf("integration %s: missing typeSpecificData", id)
	}
	wrapper, err := got.TypeSpecificData.AsCloudWatchMetricPullIntegrationWrapper()
	if err != nil {
		return client.CloudWatchMetricPullIntegration{}, fmt.Errorf("decoding typeSpecificData for %s: %w", id, err)
	}
	if wrapper.CloudWatchMetricPullIntegration == nil {
		return client.CloudWatchMetricPullIntegration{}, fmt.Errorf("integration %s: empty cloudWatchMetricPullIntegration", id)
	}
	return *wrapper.CloudWatchMetricPullIntegration, nil
}

// resolveExternalID mirrors the frontend's externalId reuse logic so that
// every AWS account in a workspace ends up trusting the same value.
func resolveExternalID(ctx context.Context, c *api.Client, instance, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if c != nil && instance != "" {
		resp, err := c.Inner.ListIntegrationsWithResponse(ctx, instance)
		if err == nil && resp.StatusCode() < 300 && resp.JSON200 != nil {
			for _, entry := range *resp.JSON200 {
				if entry.Type == nil || *entry.Type != awsIntegrationType || entry.TypeSpecificData == nil {
					continue
				}
				wrapper, werr := entry.TypeSpecificData.AsCloudWatchMetricPullIntegrationWrapper()
				if werr != nil || wrapper.CloudWatchMetricPullIntegration == nil {
					continue
				}
				if eid := wrapper.CloudWatchMetricPullIntegration.ExternalId; eid != nil && *eid != "" {
					return *eid, nil
				}
			}
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generating external id: %w", err)
	}
	return id.String(), nil
}

func buildCFLaunchURL(externalID, oodleAWSAccountID, region, roleName string) string {
	if region == "" {
		region = defaultCFStackRegion
	}
	if roleName == "" {
		roleName = defaultIAMRoleName
	}
	if oodleAWSAccountID == "" {
		oodleAWSAccountID = defaultOodleAWSAccountID
	}
	suffix := cfStackSuffix()
	stackName := cfStackNamePrefix
	if suffix != "" {
		stackName = cfStackNamePrefix + "-" + suffix
	}
	q := url.Values{}
	q.Set("templateURL", cfTemplateURL)
	q.Set("stackName", stackName)
	q.Set("param_ExternalId", externalID)
	q.Set("param_OodleAWSAccountId", oodleAWSAccountID)
	q.Set("param_RoleName", roleName)
	// AWS expects param_X separately from the path-fragment region; the
	// region itself is a query param OUTSIDE the URL fragment.
	return fmt.Sprintf(
		"https://console.aws.amazon.com/cloudformation/home?region=%s#/stacks/create/review?%s",
		url.QueryEscape(region),
		q.Encode(),
	)
}

func parseTagFlags(tags []string) (*[]client.CloudWatchSearchTag, error) {
	if len(tags) == 0 {
		return nil, nil
	}
	out := make([]client.CloudWatchSearchTag, 0, len(tags))
	for _, raw := range tags {
		k, v, ok := strings.Cut(raw, "=")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid --tag %q: expected key=value", raw)
		}
		key, val := k, v
		out = append(out, client.CloudWatchSearchTag{Key: &key, Value: &val})
	}
	return &out, nil
}

func awsRowFromIntegration(entry client.Integration) awsListRow {
	row := awsListRow{}
	if entry.Id != nil {
		row.ID = *entry.Id
	}
	if entry.Status != nil {
		row.Status = *entry.Status
	}
	if entry.TypeSpecificData == nil {
		return row
	}
	wrapper, err := entry.TypeSpecificData.AsCloudWatchMetricPullIntegrationWrapper()
	if err != nil || wrapper.CloudWatchMetricPullIntegration == nil {
		return row
	}
	cw := wrapper.CloudWatchMetricPullIntegration
	if cw.AccountId != nil {
		row.Account = *cw.AccountId
	}
	if cw.Regions != nil {
		row.Regions = strings.Join(*cw.Regions, ",")
	}
	return row
}

func requireFlags(cmd *cobra.Command, names ...string) error {
	var missing []string
	for _, n := range names {
		if !cmd.Flags().Changed(n) {
			missing = append(missing, "--"+n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required flag(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

// flagValue returns &val when the flag was explicitly set, else nil. Used by
// patch builders to distinguish "user wants empty string" from "user did not
// touch this flag".
func flagValue(cmd *cobra.Command, name, val string) *string {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	v := val
	return &v
}

func flagSlice(cmd *cobra.Command, name, val string) *[]string {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	s := splitAndTrim(val)
	return &s
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

func stringSlicePtr(s []string) *[]string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return &out
}

func derefStringSlice(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}
