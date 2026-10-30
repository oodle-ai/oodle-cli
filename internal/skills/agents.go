package skills

import (
	"os"
	"path/filepath"
	"strings"
)

// agentDirs lists, for each agent of the skills CLI (SkillsCLIVersion), a path
// that shows that the agent is installed. An agent with more than one such
// path has more than one row. When envDir is set and the environment has it,
// its value replaces dir. "~" is the home folder, and $XDG_CONFIG_HOME falls
// back to ~/.config.
//
// The list leaves out the agents that have no user-level skills folder and the
// checks that look at the current folder. `skills add -g -y` without --agent
// also selects agents with no user-level folder (for example PromptScript),
// and prints an install failure for each skill. Update the list when you
// change SkillsCLIVersion.
var agentDirs = []struct{ name, envDir, dir string }{
	{"adal", "", "~/.adal"},
	{"aider-desk", "", "~/.aider-desk"},
	{"amp", "", "$XDG_CONFIG_HOME/amp"},
	{"antigravity", "", "~/.gemini/antigravity"},
	{"antigravity-cli", "", "~/.gemini/antigravity-cli"},
	{"astrbot", "", "~/.astrbot"},
	{"augment", "", "~/.augment"},
	{"autohand-code", "AUTOHAND_HOME", "~/.autohand"},
	{"bob", "", "~/.bob"},
	{"claude-code", "CLAUDE_CONFIG_DIR", "~/.claude"},
	{"cline", "", "~/.cline"},
	{"codearts-agent", "", "~/.codeartsdoer"},
	{"codebuddy", "", "~/.codebuddy"},
	{"codemaker", "", "~/.codemaker"},
	{"codestudio", "", "~/.codestudio"},
	{"codex", "CODEX_HOME", "~/.codex"},
	{"codex", "", "/etc/codex"},
	{"command-code", "", "~/.commandcode"},
	{"continue", "", "~/.continue"},
	{"cortex", "", "~/.snowflake/cortex"},
	{"crush", "", "~/.config/crush"},
	{"cursor", "", "~/.cursor"},
	{"deepagents", "", "~/.deepagents"},
	{"devin", "", "$XDG_CONFIG_HOME/devin"},
	{"dexto", "", "~/.dexto"},
	{"droid", "", "~/.factory"},
	{"firebender", "", "~/.firebender"},
	{"forgecode", "", "~/.forge"},
	{"fx", "", "~/.fx"},
	{"gemini-cli", "", "~/.gemini"},
	{"github-copilot", "", "~/.copilot"},
	{"goose", "", "$XDG_CONFIG_HOME/goose"},
	{"grok", "GROK_HOME", "~/.grok"},
	{"hermes-agent", "HERMES_HOME", "~/.hermes"},
	{"iflow-cli", "", "~/.iflow"},
	{"inference-sh", "", "~/.inferencesh"},
	{"jazz", "", "~/.jazz"},
	{"junie", "", "~/.junie"},
	{"kilo", "", "~/.kilo"},
	{"kilo", "", "~/.kilocode"},
	{"kimchi", "", "~/.config/kimchi"},
	{"kimi-code-cli", "", "~/.kimi-code"},
	{"kimi-code-cli", "", "~/.kimi"},
	{"kiro-cli", "", "~/.kiro"},
	{"kode", "", "~/.kode"},
	{"lingma", "", "~/.lingma"},
	{"loaf", "", "~/.loaf"},
	{"mcpjam", "", "~/.mcpjam"},
	{"minimax-code", "", "~/.minimax"},
	{"minimax-code", "", "/Applications/MiniMax Code.app"},
	{"mistral-vibe", "VIBE_HOME", "~/.vibe"},
	{"moxby", "", "~/.moxby"},
	{"mux", "", "~/.mux"},
	{"neovate", "", "~/.neovate"},
	{"ona", "", "~/.ona"},
	{"openclaw", "", "~/.openclaw"},
	{"openclaw", "", "~/.clawdbot"},
	{"openclaw", "", "~/.moltbot"},
	{"opencode", "", "$XDG_CONFIG_HOME/opencode"},
	{"openhands", "", "~/.openhands"},
	{"pi", "", "~/.pi/agent"},
	{"pochi", "", "~/.pochi"},
	{"posit-assistant", "", "~/.posit/assistant"},
	{"posit-assistant", "", "~/.positai"},
	{"qoder", "", "~/.qoder"},
	{"qoder-cn", "", "~/.qoder-cn"},
	{"qwen-code", "", "~/.qwen"},
	{"reasonix", "", "~/.reasonix"},
	{"roo", "", "~/.roo"},
	{"rovodev", "", "~/.rovodev"},
	{"sarvam-code", "SARVAM_HOME", "~/.sarvam"},
	{"tabnine-cli", "", "~/.tabnine"},
	{"terramind", "", "~/.terramind"},
	{"tinycloud", "", "~/.tinycloud"},
	{"trae", "", "~/.trae"},
	{"trae-cn", "", "~/.trae-cn"},
	{"warp", "", "~/.warp"},
	{"windsurf", "", "~/.codeium/windsurf"},
	{"zcode", "", "~/.zcode"},
	{"zcode", "", "/Applications/ZCode.app"},
	{"zed", "", "$XDG_CONFIG_HOME/zed"},
	{"zencoder", "", "~/.zencoder"},
	{"zenflow", "", "~/.zencoder"},
}

// DetectInstalledAgents returns the agents that are installed on this machine,
// in the order of agentDirs and each one once. When it finds none, it returns
// codex: Codex uses the shared folder ~/.agents/skills, which Cursor, GitHub
// Copilot, Gemini CLI, OpenCode and others also read.
func DetectInstalledAgents() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{"codex"}
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}

	var found []string
	seen := map[string]bool{}
	for _, a := range agentDirs {
		if seen[a.name] {
			continue
		}
		dir := a.dir
		if v := os.Getenv(a.envDir); a.envDir != "" && v != "" {
			dir = v
		}
		dir = strings.Replace(dir, "$XDG_CONFIG_HOME", cfg, 1)
		if strings.HasPrefix(dir, "~/") {
			dir = filepath.Join(home, dir[2:])
		}
		if _, err := os.Stat(dir); err == nil {
			seen[a.name] = true
			found = append(found, a.name)
		}
	}
	if len(found) == 0 {
		return []string{"codex"}
	}
	return found
}
