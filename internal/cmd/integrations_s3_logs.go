package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	s3LogsIntegrationType = "LOGS_S3"
	// Keep equal to the template URL of the S3 card in the Oodle UI. The
	// stack calls back with values that only this template sends.
	s3LogsCFTemplateURL = "https://s3.us-west-2.amazonaws.com/oodle-configs/logs/s3/log-ingestion-v4.yaml"
	// The template names its IAM roles and uses a transform, so
	// CloudFormation refuses it without both capabilities.
	s3LogsCFCapabilities = "CAPABILITY_NAMED_IAM CAPABILITY_AUTO_EXPAND"
)

// s3LogsStackSuffix makes the stack name unique in the same way as the
// UI. Tests replace it to get a fixed name.
var s3LogsStackSuffix = func() string {
	return fmt.Sprintf("%d", time.Now().UnixMicro())
}

func newS3LogsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "s3-logs",
		Short: "Manage S3 log pull integrations",
	}
	cmd.AddCommand(newS3LogsAddCmd())
	cmd.AddCommand(newS3LogsDeleteCmd())
	return cmd
}

// s3LogsSetup is what `s3-logs add` prints: the integration record and
// the CloudFormation values that go with it.
type s3LogsSetup struct {
	IntegrationID string `json:"integrationId"`
	// Reused is true when the command found an integration record that
	// waits for its stack, and did not make a new one.
	Reused         bool              `json:"reused"`
	Region         string            `json:"region"`
	StackName      string            `json:"stackName"`
	TemplateURL    string            `json:"templateUrl"`
	Capabilities   []string          `json:"capabilities"`
	Parameters     map[string]string `json:"parameters"`
	ConsoleURL     string            `json:"consoleUrl"`
	CreateStackCmd string            `json:"createStackCommand"`
}

func newS3LogsAddCmd() *cobra.Command {
	var (
		region         string
		bucketPatterns string
		snsTopicARN    string
		externalID     string
		forceNew       bool
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Create an S3 logs integration record and print its CloudFormation values",
		Long: `Create the S3 logs integration record, as the S3 card in the Oodle UI
does, and print the values for the CloudFormation stack: ExternalId, Nonce,
OodleCallbackUrl, S3BucketPatterns and SnsTopicArn. The output has the AWS
console launch URL and an "aws cloudformation create-stack" command.

When the stack completes, it calls Oodle back with the role and the queue,
and the integration becomes active. The callback refuses values that are
not in the record, so use the printed values without change.

The ExternalId of an existing S3 logs integration is used again, so that all
stacks of the instance trust the same value. A record of the same region
that waits for its stack (no role yet) is used again too, so a second run
does not add a record. Use --new to always add one.`,
		Example: `  oodle integrations s3-logs add --region us-east-1 --bucket-patterns my-alb-logs/AWSLogs/
  oodle integrations s3-logs add --region eu-west-1 --bucket-patterns 'bucket-a,bucket-b/app/' \
    --sns-topic-arn arn:aws:sns:eu-west-1:123456789012:s3-events -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireFlags(cmd, "region", "bucket-patterns"); err != nil {
				return err
			}
			patterns := strings.Join(splitAndTrim(bucketPatterns), ",")
			if patterns == "" {
				return fmt.Errorf("--bucket-patterns has no pattern")
			}
			c := getClient(cmd)
			if c == nil || c.Config == nil {
				return fmt.Errorf("no API client configured")
			}
			instance := getInstance(cmd)
			ctx := cmd.Context()

			existing, err := listS3LogsRecords(ctx, c, instance)
			if err != nil {
				return err
			}
			if externalID == "" {
				externalID = firstS3LogsExternalID(existing)
			}
			var rec *s3LogsRecord
			if !forceNew {
				rec = pendingS3LogsRecord(existing, region, externalID)
			}
			reused := rec != nil
			if rec == nil {
				rec, err = createS3LogsRecord(ctx, c, instance, region, externalID)
				if err != nil {
					return err
				}
			}

			setup := buildS3LogsSetup(c.Config.APIURL, instance, rec, patterns, strings.TrimSpace(snsTopicARN), s3LogsStackSuffix())
			setup.Reused = reused
			return printS3LogsSetup(cmd, setup)
		},
	}
	cmd.Flags().StringVar(&region, "region", "", "AWS region of the buckets; the stack goes into this region")
	cmd.Flags().StringVar(&bucketPatterns, "bucket-patterns", "", "Comma-separated buckets or bucket/prefix patterns, e.g. my-bucket,other-bucket/alb-logs/")
	cmd.Flags().StringVar(&snsTopicARN, "sns-topic-arn", "", "SNS topic that already gets the S3 event notifications of the buckets (needed for wildcards)")
	cmd.Flags().StringVar(&externalID, "external-id", "", "External ID for the IAM role trust. If empty, uses the one of an existing S3 logs integration, or makes a new UUIDv7")
	cmd.Flags().BoolVar(&forceNew, "new", false, "Always add a new integration record")
	return cmd
}

func newS3LogsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an S3 logs integration record",
		Long: `Delete an S3 logs integration record. This does not delete the
CloudFormation stack in your AWS account; delete it with
"aws cloudformation delete-stack".`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			id := args[0]
			if !confirmAction(fmt.Sprintf("Delete S3 logs integration %q?", id), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteIntegrationsByIdWithResponse(cmd.Context(), instance, id)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted S3 logs integration %s\n", id)
			return nil
		},
	}
}

// s3LogsRecord holds the fields of an S3 logs integration that the
// command reads.
type s3LogsRecord struct {
	ID               string `json:"id"`
	Type             string `json:"type"`
	TypeSpecificData struct {
		S3 struct {
			ExternalID string `json:"externalId"`
			Nonce      string `json:"nonce"`
			Region     string `json:"region"`
			RoleArn    string `json:"roleArn"`
		} `json:"s3LogPullIntegration"`
	} `json:"typeSpecificData"`
}

func listS3LogsRecords(ctx context.Context, c *api.Client, instance string) ([]s3LogsRecord, error) {
	resp, err := c.Inner.ListIntegrations(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("listing integrations: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading integrations: %w", err)
	}
	if err := api.CheckResponse(resp, body); err != nil {
		return nil, err
	}
	var all []s3LogsRecord
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, fmt.Errorf("parsing integrations: %w", err)
	}
	var out []s3LogsRecord
	for _, r := range all {
		if r.Type == s3LogsIntegrationType {
			out = append(out, r)
		}
	}
	return out, nil
}

// firstS3LogsExternalID returns the ExternalId of the first record, as
// the UI does. The roles of earlier stacks trust this value.
func firstS3LogsExternalID(records []s3LogsRecord) string {
	for _, r := range records {
		if r.TypeSpecificData.S3.ExternalID != "" {
			return r.TypeSpecificData.S3.ExternalID
		}
	}
	return ""
}

// pendingS3LogsRecord returns a record of the region that has no role
// yet, so its stack was never created or never called back.
func pendingS3LogsRecord(records []s3LogsRecord, region, externalID string) *s3LogsRecord {
	for i, r := range records {
		s3 := r.TypeSpecificData.S3
		if s3.RoleArn != "" || s3.Nonce == "" || s3.Region != region {
			continue
		}
		if externalID != "" && s3.ExternalID != externalID {
			continue
		}
		return &records[i]
	}
	return nil
}

func createS3LogsRecord(ctx context.Context, c *api.Client, instance, region, externalID string) (*s3LogsRecord, error) {
	if externalID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return nil, fmt.Errorf("making external id: %w", err)
		}
		externalID = id.String()
	}
	nonce, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("making nonce: %w", err)
	}
	// The same payload as the S3 card. The stack callback fills in the
	// bucket patterns, the role and the queue.
	var tsd client.CreateIntegrationRequest_TypeSpecificData
	if err := tsd.FromCreateIntegrationRequestTypeSpecificData4(client.CreateIntegrationRequestTypeSpecificData4{
		"s3LogPullIntegration": map[string]interface{}{
			"externalId": externalID,
			"nonce":      nonce.String(),
			"region":     region,
		},
	}); err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	body := client.CreateIntegrationsJSONRequestBody{
		Type:             s3LogsIntegrationType,
		Status:           stringPtr("NOT_CONNECTED"),
		TypeSpecificData: &tsd,
	}
	resp, err := c.Inner.CreateIntegrationsWithResponse(ctx, instance, body)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	if resp.StatusCode() >= 300 {
		return nil, api.CheckResponse(resp.HTTPResponse, resp.Body)
	}
	var rec s3LogsRecord
	if err := json.Unmarshal(resp.Body, &rec); err != nil || rec.ID == "" {
		return nil, fmt.Errorf("the response has no integration id")
	}
	// Use the values that the command sent. They are the ones that the
	// server saved, and the response can leave them out.
	rec.TypeSpecificData.S3.ExternalID = externalID
	rec.TypeSpecificData.S3.Nonce = nonce.String()
	rec.TypeSpecificData.S3.Region = region
	return &rec, nil
}

func buildS3LogsSetup(apiURL, instance string, rec *s3LogsRecord, patterns, snsTopicARN, suffix string) s3LogsSetup {
	s3 := rec.TypeSpecificData.S3
	callback := strings.TrimRight(apiURL, "/") + "/v1/api/integrations/s3-logs/callback?" +
		url.Values{"instance": {instance}, "integrationId": {rec.ID}}.Encode()
	stackName := "oodle-s3-logs-" + suffix
	params := map[string]string{
		"ExternalId":       s3.ExternalID,
		"Nonce":            s3.Nonce,
		"OodleCallbackUrl": callback,
		"S3BucketPatterns": patterns,
	}
	if snsTopicARN != "" {
		params["SnsTopicArn"] = snsTopicARN
	}

	consoleURL := "https://console.aws.amazon.com/cloudformation/home?region=" + url.QueryEscape(s3.Region) +
		"#/stacks/create/review?templateURL=" + url.QueryEscape(s3LogsCFTemplateURL) +
		"&stackName=" + url.QueryEscape(stackName)
	for _, k := range s3LogsParamOrder {
		if v, ok := params[k]; ok {
			consoleURL += "&param_" + k + "=" + url.QueryEscape(v)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "aws cloudformation create-stack \\\n")
	fmt.Fprintf(&b, "  --region %s \\\n", s3.Region)
	fmt.Fprintf(&b, "  --stack-name %s \\\n", stackName)
	fmt.Fprintf(&b, "  --template-url %s \\\n", s3LogsCFTemplateURL)
	fmt.Fprintf(&b, "  --capabilities %s \\\n", s3LogsCFCapabilities)
	fmt.Fprintf(&b, "  --parameters")
	for _, k := range s3LogsParamOrder {
		if v, ok := params[k]; ok {
			// The quotes keep the shell from reading "&" in the callback
			// URL. The inner quotes keep the CLI from reading the commas in
			// the patterns as a list.
			if k == "S3BucketPatterns" {
				v = `"` + v + `"`
			}
			fmt.Fprintf(&b, " \\\n    'ParameterKey=%s,ParameterValue=%s'", k, v)
		}
	}
	b.WriteString("\n")

	return s3LogsSetup{
		IntegrationID:  rec.ID,
		Region:         s3.Region,
		StackName:      stackName,
		TemplateURL:    s3LogsCFTemplateURL,
		Capabilities:   strings.Fields(s3LogsCFCapabilities),
		Parameters:     params,
		ConsoleURL:     consoleURL,
		CreateStackCmd: b.String(),
	}
}

var s3LogsParamOrder = []string{"ExternalId", "Nonce", "OodleCallbackUrl", "S3BucketPatterns", "SnsTopicArn"}

func printS3LogsSetup(cmd *cobra.Command, s s3LogsSetup) error {
	format := getOutputFormat(cmd)
	switch format {
	case output.FormatJSON, output.FormatYAML:
		return output.Print(cmd.OutOrStdout(), format, s, nil)
	}
	var b bytes.Buffer
	if s.Reused {
		fmt.Fprintf(&b, "Integration: %s (exists and waits for its stack; use --new for another)\n", s.IntegrationID)
	} else {
		fmt.Fprintf(&b, "Integration: %s (created)\n", s.IntegrationID)
	}
	fmt.Fprintf(&b, "Region:      %s\nStack name:  %s\n\nParameters:\n", s.Region, s.StackName)
	for _, k := range s3LogsParamOrder {
		if v, ok := s.Parameters[k]; ok {
			fmt.Fprintf(&b, "  %-17s %s\n", k+":", v)
		}
	}
	fmt.Fprintf(&b, "\nAWS console:\n  %s\n\nAWS CLI:\n%s", s.ConsoleURL, s.CreateStackCmd)
	fmt.Fprintf(&b, "aws cloudformation wait stack-create-complete --region %s --stack-name %s\n", s.Region, s.StackName)
	_, err := cmd.OutOrStdout().Write(b.Bytes())
	return err
}
