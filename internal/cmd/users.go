package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// newUsersCmd returns the `oodle users` command tree.
func newUsersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "users",
		Short: "Manage users and invitations",
	}
	cmd.AddCommand(newUsersListCmd())
	cmd.AddCommand(newUsersInvitationsCmd())
	return cmd
}

func newUsersListCmd() *cobra.Command {
	var query string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List users in the organization",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			params := &client.ListUsersOpParams{}
			if cmd.Flags().Changed("query") {
				v := query
				params.Query = &v
			}

			resp, err := c.Inner.ListUsersOpWithResponse(cmd.Context(), instance, params)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			columns := []output.Column{
				{Header: "USER_ID", Field: "UserId"},
				{Header: "NAME", Field: "Name"},
				{Header: "EMAIL", Field: "Email"},
			}
			// JSON200 is *ListUsersResponse with an Users *[]User field.
			if resp.JSON200.Users == nil {
				return output.Print(cmd.OutOrStdout(), format, []client.User{}, columns)
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200.Users, columns)
		},
	}
	cmd.Flags().StringVar(&query, "query", "", "Search query to filter users")
	return cmd
}

func newUsersInvitationsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "invitations",
		Aliases: []string{"inv"},
		Short:   "Manage user invitations",
	}
	cmd.AddCommand(newInvitationsListCmd())
	cmd.AddCommand(newInvitationsCreateCmd())
	cmd.AddCommand(newInvitationsDeleteCmd())
	cmd.AddCommand(newInvitationsBulkCmd())
	return cmd
}

func invitationColumns() []output.Column {
	return []output.Column{
		{Header: "ID", Field: "Id"},
		{Header: "EMAIL", Field: "Email"},
		{Header: "ROLES", Field: "Roles"},
		{Header: "INVITER", Field: "Inviter"},
		{Header: "CREATED_AT", Field: "CreatedAt"},
		{Header: "EXPIRES_AT", Field: "ExpiresAt"},
	}
}

func newInvitationsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List user invitations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			resp, err := c.Inner.ListInvitationsOpWithResponse(cmd.Context(), instance)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			columns := invitationColumns()
			if resp.JSON200.Invitations == nil {
				return output.Print(cmd.OutOrStdout(), format, []client.UserInvitation{}, columns)
			}
			return output.Print(cmd.OutOrStdout(), format, *resp.JSON200.Invitations, columns)
		},
	}
}

func newInvitationsCreateCmd() *cobra.Command {
	var (
		email       string
		rolesFlag   string
		senderEmail string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Send a user invitation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			roles := splitAndTrim(rolesFlag)
			body := client.CreateInvitationsJSONRequestBody{
				Email:       email,
				Roles:       &roles,
				SenderEmail: senderEmail,
			}

			resp, err := c.Inner.CreateInvitationsWithResponse(cmd.Context(), instance, body)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				return fmt.Errorf("unexpected empty response")
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, invitationColumns())
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "Email address of the invitee (required)")
	cmd.Flags().StringVar(&rolesFlag, "roles", "", "Comma-separated list of roles (required)")
	cmd.Flags().StringVar(&senderEmail, "sender-email", "", "Email address of the sender (required)")
	_ = cmd.MarkFlagRequired("email")
	_ = cmd.MarkFlagRequired("roles")
	_ = cmd.MarkFlagRequired("sender-email")
	return cmd
}

func newInvitationsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a user invitation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			if !confirmAction(fmt.Sprintf("Delete invitation %q?", args[0]), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteInvitationsByIdWithResponse(cmd.Context(), instance, args[0])
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if resp.StatusCode() >= 300 {
				return api.CheckResponse(resp.HTTPResponse, resp.Body)
			}
			if resp.JSON200 == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Deleted invitation %s\n", args[0])
				return nil
			}
			return output.Print(cmd.OutOrStdout(), format, resp.JSON200, nil)
		},
	}
}

func newInvitationsBulkCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "bulk",
		Short: "Send multiple user invitations from a JSON or YAML file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			instance := getInstance(cmd)
			format := getOutputFormat(cmd)

			var body client.CreateBulkJSONRequestBody
			if err := readInputFile(file, &body); err != nil {
				return err
			}
			resp, err := c.Inner.CreateBulkWithResponse(cmd.Context(), instance, body)
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
	cmd.Flags().StringVarP(&file, "file", "f", "", "Path to JSON or YAML file with the bulk invitation request (required)")
	_ = cmd.MarkFlagRequired("file")
	return cmd
}
