package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var webhookColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "URL", Field: "Url"},
	{Header: "OUTPUT PATH", Field: "OutputPath"},
	{Header: "TIMEOUT", Field: "TimeoutSeconds"},
	{Header: "UPDATED", Field: "UpdatedAt"},
}

func newGenAIWebhooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "webhooks",
		Aliases: []string{"webhook"},
		Short:   "Manage the webhooks an experiment can run a dataset against",
		Long: `Manage experiment webhooks — endpoints you host that run
your own agent or workflow.

An experiment run against a webhook POSTs every dataset item
to it, using the webhook's request template, reads the output
out of the reply at its output path, and scores it. The
request carries a W3C traceparent header, so a service with
OpenTelemetry HTTP instrumentation links its trace to each
result.

The request template is JSON with {{path}} placeholders read
from the item: {{input}}, {{input.<field>}},
{{metadata.<field>}}, {{id}}. A placeholder is inserted as
JSON. The default, {{input}}, sends the item's input as the
body. The output path is a dot path over the reply, such as
answer or choices[0].message.content; empty stores the whole
reply.

Headers are encrypted at rest and never returned by list or
get; an update that omits them keeps the stored ones. Start a
run against a webhook with:

  oodle genai experiments run --dataset-id <id> --webhook-id <id>`,
	}

	cmd.AddCommand(newGenAIWebhooksListCmd())
	cmd.AddCommand(newGenAIWebhooksGetCmd())
	cmd.AddCommand(newGenAIWebhooksCreateCmd())
	cmd.AddCommand(newGenAIWebhooksUpdateCmd())
	cmd.AddCommand(newGenAIWebhooksDeleteCmd())
	cmd.AddCommand(newGenAIWebhooksTestCmd())

	return cmd
}

func newGenAIWebhooksListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List experiment webhooks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.ListGenaiWebhooksWithResponse(
				cmd.Context(), getInstance(cmd),
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return errEmptyResponse
			}
			return printGenAI(
				cmd, deref(resp.JSON200.Data), webhookColumns,
			)
		},
	}
}

func newGenAIWebhooksGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <webhook-id>",
		Short: "Get an experiment webhook",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.GetGenaiWebhookWithResponse(
				cmd.Context(), getInstance(cmd), args[0],
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return errEmptyResponse
			}
			return printGenAI(cmd, resp.JSON200, webhookColumns)
		},
	}
}

// webhookFlags are the fields of a webhook the create command
// takes as flags; anything else goes in a --file.
type webhookFlags struct {
	name            string
	description     string
	url             string
	headers         []string
	timeoutSeconds  int
	requestTemplate string
	outputPath      string
}

func (f *webhookFlags) addTo(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.name, "name", "", "Webhook name")
	cmd.Flags().StringVar(
		&f.description, "description", "", "What the endpoint runs",
	)
	cmd.Flags().StringVar(
		&f.url, "url", "",
		"URL each item is POSTed to (must be reachable from the internet)",
	)
	cmd.Flags().StringSliceVar(
		&f.headers, "header", nil,
		"Header to send, as Name=value (repeatable); put the "+
			"endpoint's credential here",
	)
	cmd.Flags().IntVar(
		&f.timeoutSeconds, "timeout", 0,
		"Seconds one item may take, 1 to 600 (default 60)",
	)
	cmd.Flags().StringVar(
		&f.requestTemplate, "request-template", "",
		"JSON body with {{path}} placeholders (default: {{input}})",
	)
	cmd.Flags().StringVar(
		&f.outputPath, "output-path", "",
		"Path to the output in the reply (default: the whole reply)",
	)
}

// parseHeaderFlags turns Name=value pairs into the header map.
func parseHeaderFlags(pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		name, value, ok := strings.Cut(pair, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf(
				"--header %q is not Name=value", pair,
			)
		}
		out[name] = value
	}
	return out, nil
}

func newGenAIWebhooksCreateCmd() *cobra.Command {
	var (
		file  string
		flags webhookFlags
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an experiment webhook",
		Long: `Create an experiment webhook.

  oodle genai webhooks create --name "Support agent" \
    --url https://agent.example.com/run \
    --header "Authorization=Bearer $AGENT_TOKEN" \
    --request-template '{"query": {{input.question}}}' \
    --output-path answer --timeout 120

Passing a credential on the command line puts it in your shell
history; prefer a --file, or read it from the environment as
above.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.CreateGenaiWebhookJSONRequestBody
			if file != "" {
				if err := readInputFile(file, &body); err != nil {
					return err
				}
			} else {
				if flags.name == "" || flags.url == "" {
					return fmt.Errorf(
						"--name and --url are required " +
							"unless --file is given",
					)
				}
				headers, err := parseHeaderFlags(flags.headers)
				if err != nil {
					return err
				}
				body.Name = flags.name
				body.Url = flags.url
				body.Description = optStr(flags.description)
				body.RequestTemplate = optStr(flags.requestTemplate)
				body.OutputPath = optStr(flags.outputPath)
				if headers != nil {
					body.Headers = &headers
				}
				if flags.timeoutSeconds != 0 {
					body.TimeoutSeconds = &flags.timeoutSeconds
				}
			}

			resp, err := c.Inner.CreateGenaiWebhookWithResponse(
				cmd.Context(), getInstance(cmd), body,
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			if resp.JSON201 == nil {
				return errEmptyResponse
			}
			return printGenAI(cmd, resp.JSON201, webhookColumns)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the webhook",
	)
	flags.addTo(cmd)
	return cmd
}

func newGenAIWebhooksUpdateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "update <webhook-id>",
		Short: "Update an experiment webhook from a JSON or YAML file",
		Long: `Update an experiment webhook.

Omitting headers keeps the stored ones; an empty object clears
them. Omitting requestTemplate or outputPath keeps the stored
value; an empty string resets it to the default.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.UpdateGenaiWebhookJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.UpdateGenaiWebhookWithResponse(
				cmd.Context(), getInstance(cmd), args[0], body,
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return errEmptyResponse
			}
			return printGenAI(cmd, resp.JSON200, webhookColumns)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the updates (required)",
	)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newGenAIWebhooksDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <webhook-id>",
		Short: "Delete an experiment webhook",
		Long: `Delete an experiment webhook.

A dataset schedule that runs against it will stop with an
error until it is pointed at another target.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			if !confirmAction(fmt.Sprintf(
				"Delete webhook %q?", args[0],
			), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteGenaiWebhookWithResponse(
				cmd.Context(), getInstance(cmd), args[0],
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			fmt.Fprintf(
				cmd.OutOrStdout(), "Deleted webhook %s\n", args[0],
			)
			return nil
		},
	}
}

func newGenAIWebhooksTestCmd() *cobra.Command {
	var (
		input         string
		datasetItemID string
	)
	cmd := &cobra.Command{
		Use:   "test <webhook-id>",
		Short: "Send one request to a webhook the way a run would",
		Long: `Send one request to a saved webhook and report the exchange:
the status, the output the path picked out, and the trace id
the request carried, so the trace can be found in Oodle once
the agent's spans arrive.

  oodle genai webhooks test <id> --input '{"question": "Is checkout slow?"}'
  oodle genai webhooks test <id> --dataset-item-id <item-id>

Nothing is stored. The reply is a JSON object; use -o json to
see the request and response in full.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			id := args[0]
			body := client.TestGenaiWebhookJSONRequestBody{
				WebhookId: &id,
			}
			if datasetItemID != "" {
				body.DatasetItemId = &datasetItemID
			}
			if input != "" {
				var value any
				if err := json.Unmarshal([]byte(input), &value); err != nil {
					// Not JSON: send it as a string, the way a
					// dataset item with a plain-text input is sent.
					value = input
				}
				body.Input = value
			}

			resp, err := c.Inner.TestGenaiWebhookWithResponse(
				cmd.Context(), getInstance(cmd), body,
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			if resp.JSON200 == nil {
				return errEmptyResponse
			}
			return printWebhookCall(cmd, resp.JSON200)
		},
	}
	cmd.Flags().StringVar(
		&input, "input", "",
		"Sample item input, as JSON or plain text",
	)
	cmd.Flags().StringVar(
		&datasetItemID, "dataset-item-id", "",
		"Send a real dataset item instead of --input",
	)
	return cmd
}

// printWebhookCall reports a test call. The table form is a
// summary a person reads; -o json carries everything.
func printWebhookCall(
	cmd *cobra.Command, call *client.WebhookCall,
) error {
	if getOutputFormat(cmd) != output.FormatTable {
		return printGenAI(cmd, call, nil)
	}
	w := cmd.OutOrStdout()
	if call.Ok {
		fmt.Fprintf(w, "OK: HTTP %d in %d ms\n",
			deref(call.StatusCode), deref(call.DurationMs))
		fmt.Fprintf(w, "Output:\n%s\n", deref(call.Output))
	} else {
		fmt.Fprintf(w, "FAILED: %s\n", deref(call.Error))
		if body := deref(call.ResponseBody); body != "" {
			fmt.Fprintf(w, "Response body:\n%s\n", body)
		}
	}
	fmt.Fprintf(w, "Trace: %s\n", call.TraceId)
	return nil
}
