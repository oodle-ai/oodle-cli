package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newFoldersCmd returns the `oodle folders` command tree.
func newFoldersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "folders",
		Aliases: []string{"folder"},
		Short:   "Manage dashboard folders",
	}
	cmd.AddCommand(newFoldersListCmd())
	cmd.AddCommand(newFoldersCreateCmd())
	return cmd
}

func folderColumns() []output.Column {
	return []output.Column{
		{Header: "TITLE", Field: "Title"},
		{Header: "UID", Field: "Uid"},
		{Header: "ID", Field: "Id"},
		{Header: "URL", Field: "Url"},
	}
}

func newFoldersListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List folders",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListFoldersWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200, folderColumns())
		},
	}
}

func newFoldersCreateCmd() *cobra.Command {
	var (
		title     string
		uid       string
		parentUID string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a folder",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			body := client.CreateFoldersJSONRequestBody{Title: title}
			if uid != "" {
				body.Uid = &uid
			}
			if parentUID != "" {
				body.ParentUid = &parentUID
			}
			resp, err := c.Inner.CreateFoldersWithResponse(cmd.Context(), instance, body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, folderColumns())
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "Folder title (required)")
	cmd.Flags().StringVar(&uid, "uid", "", "Folder UID (optional)")
	cmd.Flags().StringVar(&parentUID, "parent-uid", "", "Parent folder UID (optional)")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}
