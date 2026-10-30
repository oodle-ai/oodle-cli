package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/oodle-ai/oodle-cli/internal/skills"
)

type setupOptions struct {
	deployment   string
	integration  string
	agents       []string
	noLogin      bool
	noOnboarding bool
	verbose      bool
}

// The integration type goes into the first message to the agent.
var integrationTypeRe = regexp.MustCompile(`^[a-z0-9_]*$`)

func newSetupCmd(flags *rootFlags) *cobra.Command {
	opts := &setupOptions{}
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up this machine for Oodle onboarding with a coding agent",
		Long: `Set up this machine for Oodle onboarding with a coding agent:

  1. Install the Oodle agent skills at user level for the coding agents on
     this machine, so that the skills work in every project.
  2. Log the Oodle CLI in to Oodle.
  3. Offer to start /oodle-onboarding in Claude Code or Codex.

It requires Node.js ` + skills.MinNodeVersion + ` or later and git.`,
		Example: `  oodle setup
  oodle setup --deployment eu1 --integration otel
  oodle setup --agent claude-code --agent cursor`,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{skipConfigAnnotation: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSetup(cmd, flags, opts)
		},
	}
	f := cmd.Flags()
	f.StringVarP(&opts.deployment, "deployment", "d", "", "Oodle deployment to log in to: us1, ap1, eu1, or a full deployment URL or host")
	f.StringVarP(&opts.integration, "integration", "i", "", "Start onboarding on this integration, for example otel. Without it, onboarding asks")
	f.StringArrayVarP(&opts.agents, "agent", "a", nil, "Install the skills for this agent only (an npx skills agent name). Use more than once for more agents")
	f.BoolVar(&opts.noLogin, "no-login", false, "Do not log the Oodle CLI in")
	f.BoolVar(&opts.noOnboarding, "no-onboarding", false, "Do not offer to start /oodle-onboarding")
	f.BoolVar(&opts.verbose, "verbose", false, "Show the full output of the skills CLI")
	return cmd
}

func runSetup(cmd *cobra.Command, flags *rootFlags, opts *setupOptions) error {
	out := cmd.OutOrStdout()
	in := bufio.NewReader(cmd.InOrStdin())
	if !integrationTypeRe.MatchString(opts.integration) {
		return fmt.Errorf("not an integration type: %s", opts.integration)
	}

	fmt.Fprintf(out, "\n◆ Setting up Oodle for your coding agent\n\n")

	// Onboarding reads the code of the project for some integrations, so ask
	// before any work when this is not a project. Going to the app's folder
	// costs less before the install than after it. Kubernetes and cloud
	// accounts need no project, so the user can continue.
	project, inProject := projectDir()
	if !opts.noOnboarding && !inProject && hasTerminal() {
		here := strings.Replace(mustGetwd(), homeDir(), "~", 1)
		if !confirm(in, out, fmt.Sprintf("Not in a project folder (%s). Continue here anyway?", here), false) {
			fmt.Fprintf(out, "\nGo to the folder of your app, then run this command again.\n")
			return nil
		}
		fmt.Fprintln(out)
		project = mustGetwd()
	}

	if err := skills.CheckInstallTools(); err != nil {
		return err
	}
	agents := opts.agents
	if len(agents) == 0 {
		agents = skills.DetectInstalledAgents()
	}
	fmt.Fprintf(out, "✓ Agents: %s\n", strings.Join(agents, ", "))
	if !opts.verbose {
		fmt.Fprintln(out, "  Installing Oodle skills. This can take a minute.")
	}
	if err := skills.InstallForAgents(cmd.Context(), agents, opts.verbose, cmd.ErrOrStderr()); err != nil {
		return err
	}
	fmt.Fprintln(out, "✓ Oodle skills installed")

	loginStep := ""
	if !opts.noLogin && !setupLogin(cmd, flags, opts.deployment) {
		loginStep = "Log in to Oodle: oodle auth login"
		if opts.deployment != "" {
			loginStep += " --deployment " + opts.deployment
		}
	}

	startable := startableAgents(agents)
	if !opts.noOnboarding && project != "" && len(startable) > 0 && hasTerminal() {
		if agent, ok := chooseAgent(in, out, startable, filepath.Base(project)); ok {
			return startAgent(agent, project, opts.integration)
		}
	}

	n := 1
	fmt.Fprintf(out, "\nNext steps:\n")
	if loginStep != "" {
		fmt.Fprintf(out, "  %d. %s\n", n, loginStep)
		n++
	}
	if project == "" {
		fmt.Fprintf(out, "  %d. Go to the folder of the app that you want Oodle to monitor:\n       cd <your-app>\n", n)
		n++
	}
	fmt.Fprintf(out, "  %d. Start onboarding:\n", n)
	if len(startable) > 0 {
		a := startable[0]
		fmt.Fprintf(out, "       %s %q\n\n", a.command, agentPrompt(a.name, opts.integration))
	} else {
		fmt.Fprintf(out, "       Open your coding agent and run %s\n\n", agentPrompt("claude-code", opts.integration))
	}
	return nil
}

// setupLogin logs the CLI in when it has no credentials that it can use, and
// reports whether the CLI is logged in after this step.
func setupLogin(cmd *cobra.Command, flags *rootFlags, deployment string) bool {
	out := cmd.OutOrStdout()
	existing, _ := loadExistingConfig()
	if loggedIn(computeAuthStatus(existing, flags)) {
		fmt.Fprintln(out, "✓ Oodle CLI logged in")
		return true
	}
	if !hasTerminal() {
		fmt.Fprintln(out, "- Oodle CLI login skipped, because there is no terminal")
		return false
	}
	fmt.Fprintf(out, "\nLog in to Oodle. Your browser opens for the login.\n")
	if err := runAuthLogin(cmd, flags, deployment); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "✕ The Oodle login did not complete: %v\n", err)
		return false
	}
	// The login saves the instance, unless it could not read the instances.
	// Commands need it, so try once more.
	existing, _ = loadExistingConfig()
	if existing == nil || existing.Instance == "" {
		_ = runAuthGetInstance(cmd, flags, deployment)
	}
	fmt.Fprintln(out, "✓ Oodle CLI logged in")
	return true
}

// loggedIn reports whether the CLI has credentials that it can use without a
// login. An expired access token is not a problem while a refresh token works.
func loggedIn(s authStatusRow) bool {
	if s.PreferredAuth == "none" {
		return false
	}
	return !s.OAuthExpired || s.RefreshEnabled
}

// projectDir returns the top folder of the git repository of the current
// folder. The home folder is not a project.
func projectDir() (string, bool) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", false
	}
	top := strings.TrimSpace(string(out))
	if top == "" || top == homeDir() {
		return "", false
	}
	return top, true
}

type startableAgent struct {
	name    string // the skills CLI agent name
	label   string
	command string
}

// startableAgents returns the agents in the list that can start in the
// terminal with a first message.
func startableAgents(agents []string) []startableAgent {
	var found []startableAgent
	for _, a := range []startableAgent{
		{"claude-code", "Claude Code", "claude"},
		{"codex", "Codex", "codex"},
	} {
		if !slices.Contains(agents, a.name) {
			continue
		}
		if _, err := exec.LookPath(a.command); err == nil {
			found = append(found, a)
		}
	}
	return found
}

// agentPrompt returns the first message to the agent, which starts onboarding.
func agentPrompt(agent, integration string) string {
	target := "Oodle"
	if integration != "" {
		target = "the " + integration + " integration"
	}
	if agent == "codex" {
		// Codex has no slash commands for skills. It picks the skill from the
		// request.
		return "Use the oodle-onboarding skill to set up " + target + "."
	}
	if integration != "" {
		return "/oodle-onboarding set up " + target
	}
	return "/oodle-onboarding"
}

func chooseAgent(in *bufio.Reader, out io.Writer, agents []startableAgent, project string) (startableAgent, bool) {
	if len(agents) == 1 {
		ok := confirm(in, out, fmt.Sprintf("\nStart Oodle onboarding in %s in %s?", agents[0].label, project), true)
		return agents[0], ok
	}
	fmt.Fprintf(out, "\nStart Oodle onboarding in %s with:\n", project)
	for i, a := range agents {
		fmt.Fprintf(out, "  %d. %s\n", i+1, a.label)
	}
	fmt.Fprintf(out, "  n. Not now\nChoose [1]: ")
	answer := readAnswer(in)
	if answer == "" {
		answer = "1"
	}
	i, err := strconv.Atoi(answer)
	if err != nil || i < 1 || i > len(agents) {
		return startableAgent{}, false
	}
	return agents[i-1], true
}

func startAgent(a startableAgent, project, integration string) error {
	c := exec.Command(a.command, agentPrompt(a.name, integration))
	c.Dir = project
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

// confirm asks a yes or no question. An empty answer gives def.
func confirm(in *bufio.Reader, out io.Writer, question string, def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Fprintf(out, "%s %s ", question, hint)
	switch strings.ToLower(readAnswer(in)) {
	case "":
		return def
	case "y", "yes":
		return true
	default:
		return false
	}
}

func readAnswer(in *bufio.Reader) string {
	line, _ := in.ReadString('\n')
	return strings.TrimSpace(line)
}

// hasTerminal reports whether a person can answer questions. The install
// script runs this command with the terminal as stdin.
func hasTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}
