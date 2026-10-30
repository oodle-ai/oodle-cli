package skills

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const (
	// SkillsCLIVersion is the skills CLI release that installs the skills. It
	// is pinned so that a new release cannot change what users see. agentDirs
	// follows the agent checks of this release.
	SkillsCLIVersion = "1.7.4"

	// MinNodeVersion is the Node.js release that SkillsCLIVersion needs. On
	// an older one it stops with a stack trace that does not say what is wrong.
	MinNodeVersion = "22.20"
)

// envOr returns the value of the environment variable, or def when it is not
// set. Tests set these variables to install from a fork or another release.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// CheckInstallTools returns an error that tells the user what to install when
// Node.js (MinNodeVersion or later), npx or git is missing. The skills CLI
// clones the skills repository with git.
func CheckInstallTools() error {
	need := fmt.Sprintf("Node.js %s or later is necessary. Install the LTS release from https://nodejs.org, then run this command again.", MinNodeVersion)
	if _, err := exec.LookPath("npx"); err != nil {
		return fmt.Errorf("%s", need)
	}
	out, err := exec.Command("node", "-p", "process.versions.node").Output()
	if err != nil {
		return fmt.Errorf("%s", need)
	}
	have := strings.TrimSpace(string(out))
	if !versionAtLeast(have, MinNodeVersion) {
		return fmt.Errorf("%s This machine has Node.js v%s.", need, have)
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("Git is necessary. Install it from https://git-scm.com, then run this command again.")
	}
	return nil
}

// versionAtLeast reports whether the major.minor of have is at least the
// major.minor of need.
func versionAtLeast(have, need string) bool {
	part := func(v string, i int) int {
		fields := strings.Split(v, ".")
		if i >= len(fields) {
			return 0
		}
		n, _ := strconv.Atoi(fields[i])
		return n
	}
	if part(have, 0) != part(need, 0) {
		return part(have, 0) > part(need, 0)
	}
	return part(have, 1) >= part(need, 1)
}

// npxArgs returns the arguments of the npx command that installs all skills
// at user level for the agents.
func npxArgs(agents []string) []string {
	args := []string{
		"-y", "skills@" + envOr("OODLE_SKILLS_CLI_VERSION", SkillsCLIVersion),
		"add", envOr("OODLE_SKILLS_REPO", repoOwner+"/"+repoName),
		"--global", "--skill", "*", "--yes",
	}
	for _, a := range agents {
		args = append(args, "--agent", a)
	}
	return args
}

// InstallForAgents installs all Oodle skills at user level for the agents,
// with the skills CLI, so that the skills work in every project. With verbose,
// the skills CLI writes to out. Without it, out gets its output only when the
// install fails.
func InstallForAgents(ctx context.Context, agents []string, verbose bool, out io.Writer) error {
	cmd := exec.CommandContext(ctx, "npx", npxArgs(agents)...)
	// The skills CLI must not wait for an answer.
	cmd.Stdin = nil
	if verbose {
		cmd.Stdout, cmd.Stderr = out, out
		return cmd.Run()
	}
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	err := cmd.Run()
	// The skills CLI exits 0 when single skills fail, so look for its failure
	// mark too.
	if err != nil || strings.Contains(log.String(), "✗") {
		_, _ = out.Write(log.Bytes())
		return fmt.Errorf("the Oodle skills did not install. The output of the skills CLI is above")
	}
	return nil
}
