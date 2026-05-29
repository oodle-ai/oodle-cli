package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/output"
	"github.com/oodle-ai/oodle-cli/internal/skills"
)

const skillsTimeout = 30 * time.Second

func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Manage AI agent skills for the Oodle CLI",
		Long: `Fetch and install Oodle CLI skills into your AI coding agent's skills directory.

Skills are fetched from https://github.com/oodle-ai/agent-skills at install time.
Supports Claude Code, Cursor, Codex, Windsurf, Gemini CLI, and others.
The target agent is auto-detected from environment variables.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newSkillsListCmd())
	cmd.AddCommand(newSkillsInstallCmd())
	cmd.AddCommand(newSkillsPathCmd())
	return cmd
}

func newSkillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available skills from oodle-ai/agent-skills",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), skillsTimeout)
			defer cancel()

			entries, err := skills.List(ctx)
			if err != nil {
				return fmt.Errorf("fetching skills list: %w", err)
			}

			format := getOutputFormat(cmd)
			if format == output.FormatJSON {
				type jsonEntry struct {
					Name        string `json:"name"`
					Description string `json:"description"`
				}
				out := make([]jsonEntry, len(entries))
				for i, e := range entries {
					out[i] = jsonEntry{Name: e.Name, Description: e.Description}
				}
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			// Table output
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "%-30s %s\n", "NAME", "DESCRIPTION")
			fmt.Fprintf(w, "%-30s %s\n", "----", "-----------")
			for _, e := range entries {
				fmt.Fprintf(w, "%-30s %s\n", e.Name, e.Description)
			}
			return nil
		},
	}
}

func newSkillsInstallCmd() *cobra.Command {
	var targetAgent string
	var dir string

	cmd := &cobra.Command{
		Use:   "install [name]",
		Short: "Fetch and install skills for the detected AI coding agent",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), skillsTimeout)
			defer cancel()

			agent := targetAgent
			if agent == "" {
				agent = skills.DetectAgent()
			}

			installDir := dir
			if installDir == "" {
				d, err := skills.SkillsDir(agent)
				if err != nil {
					return err
				}
				installDir = d
			}

			format := getOutputFormat(cmd)

			if len(args) == 1 {
				// Install a single named skill
				name := args[0]
				content, err := skills.FetchContent(ctx, name)
				if err != nil {
					return err
				}
				skillDir := filepath.Join(installDir, name)
				if err := os.MkdirAll(skillDir, 0755); err != nil {
					return fmt.Errorf("creating directory %s: %w", skillDir, err)
				}
				dest := filepath.Join(skillDir, "SKILL.md")
				if err := os.WriteFile(dest, []byte(content), 0644); err != nil {
					return fmt.Errorf("writing %s: %w", dest, err)
				}
				return printInstallResult(cmd, format, 1, installDir)
			}

			// Install all skills (fetched in parallel)
			entries, err := skills.List(ctx)
			if err != nil {
				return fmt.Errorf("fetching skills list: %w", err)
			}

			results := skills.FetchAllContents(ctx, entries)

			for _, r := range results {
				if r.Err != nil {
					return fmt.Errorf("fetching skill %q: %w", r.Name, r.Err)
				}
				skillDir := filepath.Join(installDir, r.Name)
				if err := os.MkdirAll(skillDir, 0755); err != nil {
					return fmt.Errorf("creating directory %s: %w", skillDir, err)
				}
				dest := filepath.Join(skillDir, "SKILL.md")
				if err := os.WriteFile(dest, []byte(r.Content), 0644); err != nil {
					return fmt.Errorf("writing %s: %w", dest, err)
				}
			}
			return printInstallResult(cmd, format, len(results), installDir)
		},
	}

	cmd.Flags().StringVar(&targetAgent, "target-agent", "", "Override agent detection (claude-code, cursor, codex, opencode, windsurf, gemini-code, aider, cline, github-copilot, amazon-q, sourcegraph-cody)")
	cmd.Flags().StringVar(&dir, "dir", "", "Override install directory")
	return cmd
}

func printInstallResult(cmd *cobra.Command, format output.Format, count int, dir string) error {
	if format == output.FormatJSON {
		type result struct {
			Installed int    `json:"installed"`
			Directory string `json:"directory"`
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(result{Installed: count, Directory: dir})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Installed %d skill(s) to %s\n", count, dir)
	return nil
}

func newSkillsPathCmd() *cobra.Command {
	var targetAgent string

	cmd := &cobra.Command{
		Use:   "path",
		Short: "Show where skills would be installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			agent := targetAgent
			if agent == "" {
				agent = skills.DetectAgent()
			}
			dir, err := skills.SkillsDir(agent)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), dir)
			return nil
		},
	}

	cmd.Flags().StringVar(&targetAgent, "target-agent", "", "Override agent detection")
	return cmd
}
