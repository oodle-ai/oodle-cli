package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

var datasetColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "ITEMS", Field: "ItemCount"},
	{Header: "RUNS", Field: "RunCount"},
	{Header: "DESCRIPTION", Field: "Description"},
	{Header: "UPDATED", Field: "UpdatedAt"},
}

var datasetItemColumns = []output.Column{
	{Header: "ID", Field: "Id"},
	{Header: "STATUS", Field: "Status"},
	{Header: "VERSION", Field: "Version"},
	{Header: "SOURCE TRACE", Field: "SourceTraceId"},
	{Header: "UPDATED", Field: "UpdatedAt"},
}

func newGenAIDatasetsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "datasets",
		Aliases: []string{"dataset", "ds"},
		Short:   "Manage evaluation datasets and their items",
		Long: `Manage evaluation datasets and their items.

Datasets are versioned by time rather than by number: every item
edit opens a new validity window, so ` + "`items --at`" + ` can recover
exactly the inputs a past experiment ran against.`,
	}

	cmd.AddCommand(newGenAIDatasetsListCmd())
	cmd.AddCommand(newGenAIDatasetsGetCmd())
	cmd.AddCommand(newGenAIDatasetsCreateCmd())
	cmd.AddCommand(newGenAIDatasetsDeleteCmd())
	cmd.AddCommand(newGenAIDatasetItemsCmd())

	return cmd
}

func newGenAIDatasetsListCmd() *cobra.Command {
	var page pageFlags
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List datasets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			limit, pageNum := page.values()

			resp, err := c.Inner.ListGenaiDatasetsWithResponse(
				cmd.Context(), getInstance(cmd),
				&client.ListGenaiDatasetsParams{
					Limit: limit,
					Page:  pageNum,
				},
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
				cmd, deref(resp.JSON200.Data), datasetColumns,
			)
		},
	}
	page.addTo(cmd)
	return cmd
}

func newGenAIDatasetsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <name>",
		Short: "Get a dataset by name",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.GetGenaiDatasetWithResponse(
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
			return printGenAI(cmd, resp.JSON200, datasetColumns)
		},
	}
}

func newGenAIDatasetsCreateCmd() *cobra.Command {
	var (
		file        string
		name        string
		description string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a dataset",
		Long: `Create a dataset, from flags or from a JSON/YAML file.

  oodle genai datasets create --name support-eval

  # or, to attach metadata:
  oodle genai datasets create -f dataset.json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.CreateGenaiDatasetJSONRequestBody
			if file != "" {
				if err := readInputFile(file, &body); err != nil {
					return err
				}
			} else {
				if name == "" {
					return fmt.Errorf(
						"either --name or --file is required",
					)
				}
				body.Name = name
				body.Description = optStr(description)
			}

			resp, err := c.Inner.CreateGenaiDatasetWithResponse(
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
			return printGenAI(cmd, resp.JSON201, datasetColumns)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the dataset",
	)
	cmd.Flags().StringVar(
		&name, "name", "", "Dataset name",
	)
	cmd.Flags().StringVar(
		&description, "description", "", "Dataset description",
	)
	return cmd
}

func newGenAIDatasetsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a dataset and all of its items",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			if !confirmAction(fmt.Sprintf(
				"Delete dataset %q and all its items?", args[0],
			), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteGenaiDatasetWithResponse(
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
				cmd.OutOrStdout(), "Deleted dataset %s\n", args[0],
			)
			return nil
		},
	}
}

// newGenAIDatasetItemsCmd returns `oodle genai datasets items`,
// the CRUD surface for the rows inside a dataset.
func newGenAIDatasetItemsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "items",
		Aliases: []string{"item"},
		Short:   "Manage the items inside a dataset",
	}
	cmd.AddCommand(newGenAIDatasetItemsListCmd())
	cmd.AddCommand(newGenAIDatasetItemsGetCmd())
	cmd.AddCommand(newGenAIDatasetItemsCreateCmd())
	cmd.AddCommand(newGenAIDatasetItemsUpdateCmd())
	cmd.AddCommand(newGenAIDatasetItemsDeleteCmd())
	return cmd
}

func newGenAIDatasetItemsListCmd() *cobra.Command {
	var (
		page pageFlags
		at   string
	)
	cmd := &cobra.Command{
		Use:   "list <dataset-name>",
		Short: "List a dataset's items",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			limit, pageNum := page.values()

			atTime, err := toRFC3339(at)
			if err != nil {
				return fmt.Errorf("--at: %w", err)
			}

			resp, err := c.Inner.ListGenaiDatasetItemsWithResponse(
				cmd.Context(), getInstance(cmd), args[0],
				&client.ListGenaiDatasetItemsParams{
					Limit:   limit,
					Page:    pageNum,
					Version: optStr(atTime),
				},
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
				cmd, deref(resp.JSON200.Data), datasetItemColumns,
			)
		},
	}
	page.addTo(cmd)
	cmd.Flags().StringVar(
		&at, "at", "",
		"Return the dataset as it stood at this time: RFC3339, "+
			"'now', or a relative duration like -7d",
	)
	return cmd
}

func newGenAIDatasetItemsGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <item-id>",
		Short: "Get a dataset item",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			resp, err := c.Inner.GetGenaiDatasetItemWithResponse(
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
			return printGenAI(
				cmd, resp.JSON200, datasetItemColumns,
			)
		},
	}
}

func newGenAIDatasetItemsCreateCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Add an item to a dataset from a JSON or YAML file",
		Long: `Add an item to a dataset from a JSON or YAML file.

  {
    "datasetName": "support-eval",
    "input": {"question": "How do I reset my password?"},
    "expectedOutput": {"answer": "Use the reset link."},
    "sourceTraceId": "0af7651916cd43dd8448eb211c80319c"
  }

Setting sourceTraceId links the row back to the production trace
it was captured from, so a regression can be traced to the
request that produced it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.CreateGenaiDatasetItemJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.CreateGenaiDatasetItemWithResponse(
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
			return printGenAI(
				cmd, resp.JSON201, datasetItemColumns,
			)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the item (required)",
	)
	_ = cmd.MarkFlagRequired("file")
	return cmd
}

func newGenAIDatasetItemsUpdateCmd() *cobra.Command {
	var (
		file   string
		status string
	)
	cmd := &cobra.Command{
		Use:   "update <item-id>",
		Short: "Update a dataset item",
		Long: `Update a dataset item.

Fields left out of the file are untouched. Use --status archived
to retire a row without deleting it, which keeps past runs that
referenced it intact.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			var body client.UpdateGenaiDatasetItemJSONRequestBody
			if file != "" {
				if err := readInputFile(file, &body); err != nil {
					return err
				}
			}
			if status != "" {
				body.Status = &status
			}
			if file == "" && status == "" {
				return fmt.Errorf(
					"either --file or --status is required",
				)
			}

			resp, err := c.Inner.UpdateGenaiDatasetItemWithResponse(
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
			return printGenAI(
				cmd, resp.JSON200, datasetItemColumns,
			)
		},
	}
	cmd.Flags().StringVarP(
		&file, "file", "f", "",
		"Path to JSON or YAML file with the updated fields",
	)
	cmd.Flags().StringVar(
		&status, "status", "",
		"Item status, e.g. active or archived",
	)
	return cmd
}

func newGenAIDatasetItemsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <item-id>",
		Short: "Delete a dataset item",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)

			if !confirmAction(fmt.Sprintf(
				"Delete dataset item %q?", args[0],
			), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteGenaiDatasetItemWithResponse(
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
				cmd.OutOrStdout(),
				"Deleted dataset item %s\n", args[0],
			)
			return nil
		},
	}
}
