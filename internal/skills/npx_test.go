package skills

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// fakeHome makes an empty home folder with the given subfolders.
func fakeHome(t *testing.T, dirs ...string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	for _, a := range agentDirs {
		if a.envDir != "" {
			t.Setenv(a.envDir, "")
		}
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestDetectInstalledAgents_FindsAgentsInListOrder(t *testing.T) {
	fakeHome(t, ".cursor", ".claude", ".config/opencode")
	got := DetectInstalledAgents()
	want := []string{"claude-code", "cursor", "opencode"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDetectInstalledAgents_NoAgentGivesCodex(t *testing.T) {
	fakeHome(t)
	if got := DetectInstalledAgents(); !slices.Equal(got, []string{"codex"}) {
		t.Errorf("got %v, want [codex]", got)
	}
}

func TestDetectInstalledAgents_EnvFolderReplacesDefault(t *testing.T) {
	home := fakeHome(t, "elsewhere")
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "elsewhere"))
	if got := DetectInstalledAgents(); !slices.Equal(got, []string{"claude-code"}) {
		t.Errorf("got %v, want [claude-code]", got)
	}
}

func TestDetectInstalledAgents_XDGConfigHome(t *testing.T) {
	home := fakeHome(t, "xdg/goose")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	if got := DetectInstalledAgents(); !slices.Equal(got, []string{"goose"}) {
		t.Errorf("got %v, want [goose]", got)
	}
}

func TestDetectInstalledAgents_AgentWithTwoFoldersOnce(t *testing.T) {
	fakeHome(t, ".kilo", ".kilocode")
	if got := DetectInstalledAgents(); !slices.Equal(got, []string{"kilo"}) {
		t.Errorf("got %v, want [kilo]", got)
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, tc := range []struct {
		have string
		want bool
	}{
		{"22.20.0", true},
		{"22.21.1", true},
		{"23.0.0", true},
		{"22.19.9", false},
		{"20.18.0", false},
		{"", false},
	} {
		if got := versionAtLeast(tc.have, MinNodeVersion); got != tc.want {
			t.Errorf("versionAtLeast(%q) = %v, want %v", tc.have, got, tc.want)
		}
	}
}

func TestNpxArgs(t *testing.T) {
	t.Setenv("OODLE_SKILLS_CLI_VERSION", "")
	t.Setenv("OODLE_SKILLS_REPO", "")
	got := strings.Join(npxArgs([]string{"claude-code", "cursor"}), " ")
	want := "-y skills@" + SkillsCLIVersion + " add oodle-ai/agent-skills --global --skill * --yes --agent claude-code --agent cursor"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}

	t.Setenv("OODLE_SKILLS_REPO", "me/fork")
	if got := npxArgs(nil)[3]; got != "me/fork" {
		t.Errorf("repo override: got %q", got)
	}
}

// fakeTools puts shell scripts on PATH in place of real tools.
func fakeTools(t *testing.T, scripts map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tools are shell scripts")
	}
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func TestInstallForAgents(t *testing.T) {
	for _, tc := range []struct {
		name    string
		npx     string
		wantErr bool
		wantOut string
	}{
		{"success is quiet", `echo "✓ installed"`, false, ""},
		{"failure mark with exit 0", `echo "✗ oodle-logs failed"`, true, "✗ oodle-logs failed"},
		{"exit code", `echo "boom"; exit 3`, true, "boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeTools(t, map[string]string{"npx": tc.npx})
			var out bytes.Buffer
			err := InstallForAgents(context.Background(), []string{"codex"}, false, &out)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if strings.TrimSpace(out.String()) != tc.wantOut {
				t.Errorf("out = %q, want %q", out.String(), tc.wantOut)
			}
		})
	}
}

func TestCheckInstallTools(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tools   map[string]string
		wantErr string
	}{
		{"all present", map[string]string{"npx": "", "node": "echo 22.20.0", "git": ""}, ""},
		{"no node", map[string]string{"git": ""}, "Node.js 22.20 or later"},
		{"old node", map[string]string{"npx": "", "node": "echo 20.11.1", "git": ""}, "This machine has Node.js v20.11.1."},
		{"no git", map[string]string{"npx": "", "node": "echo 24.1.0"}, "Git is necessary"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeTools(t, tc.tools)
			err := CheckInstallTools()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}
