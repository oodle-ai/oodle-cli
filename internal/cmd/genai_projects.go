package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/client"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

// defaultProjectName is the built-in project. Its id is "" and the
// API reads an absent projectId as this project.
const defaultProjectName = "default"

var projectColumns = []output.Column{
	{Header: "NAME", Field: "Name"},
	{Header: "ID", Field: "Id"},
	{Header: "DESCRIPTION", Field: "Description"},
	{Header: "UPDATED", Field: "UpdatedAt"},
}

func newGenAIProjectsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "projects",
		Aliases: []string{"project"},
		Short:   "Manage the projects that keep datasets and experiments apart",
		Long: `Manage GenAI projects.

A project keeps datasets and experiments apart from other
projects, for example one per application or test setup. The
"default" project holds everything that names no project.
Prompts, evaluators, LLM connections and webhooks are shared by
all projects.

Traces join a project by name, through the oodle.project
resource attribute or the X-Oodle-Project OTLP header, so a
project name cannot change after it is created.

Commands that work in a project take --project <name>:

  oodle genai datasets create --name golden --project checkout-evals`,
	}

	cmd.AddCommand(newGenAIProjectsListCmd())
	cmd.AddCommand(newGenAIProjectsCreateCmd())
	cmd.AddCommand(newGenAIProjectsUpdateCmd())
	cmd.AddCommand(newGenAIProjectsDeleteCmd())
	return cmd
}

// listProjects returns the projects of the instance, the default
// project first.
func listProjects(cmd *cobra.Command) ([]client.Project, error) {
	c := getClient(cmd)
	resp, err := c.Inner.ListGenaiProjectsWithResponse(
		cmd.Context(), getInstance(cmd),
	)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	if err := genaiCheck(
		resp.StatusCode(), resp.HTTPResponse, resp.Body,
	); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, errEmptyResponse
	}
	return deref(resp.JSON200.Data), nil
}

// projectIDByName finds the id of a named project. The default
// project has no id, so it returns "" for it; any other name that
// does not exist is an error that lists the names that do.
func projectIDByName(cmd *cobra.Command, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == defaultProjectName {
		return "", nil
	}
	projects, err := listProjects(cmd)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		if p.Name == name {
			return p.Id, nil
		}
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return "", fmt.Errorf(
		"no project named %q; projects: %s", name, strings.Join(names, ", "),
	)
}

// addProjectFlag adds --project to a command and every command
// under it.
func addProjectFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().String(
		"project", "",
		`Project name (default: the "default" project)`,
	)
}

// projectIDParam is the projectId to send for the command's
// --project flag: nil for the default project, so the request is
// the same as one from a CLI without projects.
func projectIDParam(cmd *cobra.Command) (*string, error) {
	name, _ := cmd.Flags().GetString("project")
	id, err := projectIDByName(cmd, name)
	if err != nil || id == "" {
		return nil, err
	}
	return &id, nil
}

func newGenAIProjectsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List projects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			projects, err := listProjects(cmd)
			if err != nil {
				return err
			}
			return printGenAI(cmd, projects, projectColumns)
		},
	}
}

func newGenAIProjectsCreateCmd() *cobra.Command {
	var description string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a project",
		Long: `Create a project. Creating a name that exists returns that
project. The name cannot change later.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			body := client.CreateGenaiProjectJSONRequestBody{Name: args[0]}
			if description != "" {
				body.Description = &description
			}
			resp, err := c.Inner.CreateGenaiProjectWithResponse(
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
			project := resp.JSON201
			if project == nil {
				project = resp.JSON200
			}
			if project == nil {
				return errEmptyResponse
			}
			return printGenAIObject(cmd, project, projectColumns)
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "What the project is for")
	return cmd
}

// namedProjectID is the id of the project an update or delete
// names. Neither applies to the default project.
func namedProjectID(cmd *cobra.Command, name string) (string, error) {
	id, err := projectIDByName(cmd, name)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf(
			"the %q project cannot be changed or deleted", defaultProjectName,
		)
	}
	return id, nil
}

func newGenAIProjectsUpdateCmd() *cobra.Command {
	var description string
	cmd := &cobra.Command{
		Use:   "update <name>",
		Short: "Change a project's description",
		Args:  exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			id, err := namedProjectID(cmd, args[0])
			if err != nil {
				return err
			}
			resp, err := c.Inner.UpdateGenaiProjectWithResponse(
				cmd.Context(), getInstance(cmd), id,
				client.UpdateGenaiProjectJSONRequestBody{
					Description: description,
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
			return printGenAIObject(cmd, resp.JSON200, projectColumns)
		},
	}
	cmd.Flags().StringVar(&description, "description", "", "The new description (required)")
	_ = cmd.MarkFlagRequired("description")
	return cmd
}

func newGenAIProjectsDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a project and everything in it",
		Long: `Delete a project. This also deletes its datasets,
experiments, experiment rows and their scores, and cannot be
undone. Traces stay until their retention ends.`,
		Args: exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := getClient(cmd)
			id, err := namedProjectID(cmd, args[0])
			if err != nil {
				return err
			}
			if !confirmAction(fmt.Sprintf(
				"Delete project %q and its datasets and experiments?", args[0],
			), forceFlag(cmd)) {
				return fmt.Errorf("aborted")
			}
			resp, err := c.Inner.DeleteGenaiProjectWithResponse(
				cmd.Context(), getInstance(cmd), id,
			)
			if err != nil {
				return fmt.Errorf("API request failed: %w", err)
			}
			if err := genaiCheck(
				resp.StatusCode(), resp.HTTPResponse, resp.Body,
			); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted project %s\n", args[0])
			return nil
		},
	}
}
