// Package skills provides agent detection, install path resolution,
// and GitHub-based skill fetching for the oodle CLI.
package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	repoOwner  = "oodle-ai"
	repoName   = "agent-skills"
	repoBranch = "main"

	httpTimeout = 30 * time.Second
)

// contentsAPIURLOverride and rawContentURLOverride are package-level variables
// that can be set in tests to point at a mock HTTP server instead of GitHub.
// Empty string means use the real GitHub URLs.
var (
	contentsAPIURLOverride string
	rawContentURLOverride  string
)

// SetContentsAPIURLOverride sets the URL override for the GitHub contents API.
// Used in tests only. Pass "" to reset to the real GitHub URL.
func SetContentsAPIURLOverride(u string) { contentsAPIURLOverride = u }

// SetRawContentURLOverride sets the URL override for GitHub raw content.
// Used in tests only. Pass "" to reset to the real GitHub URL.
func SetRawContentURLOverride(u string) { rawContentURLOverride = u }

// Entry represents a single skill available in the agent-skills repo.
type Entry struct {
	Name        string // directory name, e.g. "oodle-cli"
	Description string // extracted from SKILL.md frontmatter description: field
}

// githubDirEntry is used to unmarshal the GitHub contents API response.
type githubDirEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "dir" or "file"
}

// newHTTPClient returns an *http.Client with a 30-second timeout.
func newHTTPClient() *http.Client {
	return &http.Client{Timeout: httpTimeout}
}

// contentsURL returns the URL to use for listing the skills directory.
func contentsURL() string {
	if contentsAPIURLOverride != "" {
		return contentsAPIURLOverride
	}
	return fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/skills", repoOwner, repoName)
}

// rawURL returns the URL to use for fetching a single SKILL.md.
func rawURL(name string) string {
	if rawContentURLOverride != "" {
		return fmt.Sprintf("%s/%s/SKILL.md", strings.TrimRight(rawContentURLOverride, "/"), name)
	}
	return fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/skills/%s/SKILL.md",
		repoOwner, repoName, repoBranch, name)
}

// List fetches the list of available skills from the GitHub contents API.
// Returns entries sorted alphabetically by Name.
// Only includes entries of type "dir" (skips files).
func List(ctx context.Context) ([]Entry, error) {
	client := newHTTPClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, contentsURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching skills list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching skills list: unexpected status %d", resp.StatusCode)
	}

	var dirEntries []githubDirEntry
	if err := json.NewDecoder(resp.Body).Decode(&dirEntries); err != nil {
		return nil, fmt.Errorf("decoding skills list: %w", err)
	}

	var entries []Entry
	for _, de := range dirEntries {
		if de.Type != "dir" {
			continue
		}
		entries = append(entries, Entry{Name: de.Name})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// FetchContent fetches the SKILL.md content for the named skill.
// Returns an error wrapping "skill not found: <name>" if the response is 404.
func FetchContent(ctx context.Context, name string) (string, error) {
	client := newHTTPClient()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL(name), nil)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching skill %q: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("skill not found: %s", name)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching skill %q: unexpected status %d", name, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading skill %q: %w", name, err)
	}
	return string(body), nil
}

// maxParallelFetches is the maximum number of concurrent HTTP requests when
// fetching all skills in parallel.
const maxParallelFetches = 5

// FetchResult holds the outcome of fetching a single skill's content.
type FetchResult struct {
	Name    string
	Content string
	Err     error
}

// FetchAllContents fetches SKILL.md content for all given entries concurrently
// using a bounded worker pool. It returns results in the same order as the
// input entries. If the context is cancelled, in-flight fetches are abandoned
// and the context error is returned in the remaining results.
func FetchAllContents(ctx context.Context, entries []Entry) []FetchResult {
	results := make([]FetchResult, len(entries))

	sem := make(chan struct{}, maxParallelFetches)
	var wg sync.WaitGroup

	for i, e := range entries {
		wg.Add(1)
		go func(idx int, name string) {
			defer wg.Done()

			// Acquire semaphore slot (or bail on context cancellation).
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				results[idx] = FetchResult{Name: name, Err: ctx.Err()}
				return
			}

			content, err := FetchContent(ctx, name)
			results[idx] = FetchResult{Name: name, Content: content, Err: err}
		}(i, e.Name)
	}

	wg.Wait()
	return results
}

// agentDetectors maps agent names to detection env vars (first truthy match wins).
// Env var names taken verbatim from github.com/datadog-labs/pup src/useragent.rs.
var agentDetectors = []struct {
	name    string
	envVars []string
}{
	{"claude-code", []string{"CLAUDECODE", "CLAUDE_CODE"}},
	{"cursor", []string{"CURSOR_AGENT"}},
	{"codex", []string{"CODEX", "OPENAI_CODEX"}},
	{"opencode", []string{"OPENCODE"}},
	{"aider", []string{"AIDER"}},
	{"cline", []string{"CLINE"}},
	{"windsurf", []string{"WINDSURF_AGENT"}},
	{"github-copilot", []string{"GITHUB_COPILOT"}},
	{"amazon-q", []string{"AMAZON_Q", "AWS_Q_DEVELOPER"}},
	{"gemini-code", []string{"GEMINI_CODE_ASSIST"}},
	{"sourcegraph-cody", []string{"SRC_CODY"}},
	{"generic-agent", []string{"AGENT"}},
}

// isEnvTruthy returns true if the env var is set to "1" or "true" (case-insensitive).
func isEnvTruthy(key string) bool {
	v := strings.ToLower(os.Getenv(key))
	return v == "1" || v == "true"
}

// DetectAgent returns the name of the running AI coding agent by checking
// env vars in priority order (first truthy match wins). Returns "" if none detected.
func DetectAgent() string {
	for _, d := range agentDetectors {
		for _, env := range d.envVars {
			if isEnvTruthy(env) {
				return d.name
			}
		}
	}
	return ""
}

// knownSkillsDirs is the ordered list of existing skills directories to check.
var knownSkillsDirs = []string{
	".agents/skills",
	".claude/skills",
	".cursor/skills",
	".windsurf/skills",
	".gemini/skills",
}

// SkillsDir returns the directory where skills should be installed under projectRoot.
// If any known skills directory already exists under projectRoot, that path is returned.
// Otherwise the agent-specific default is used.
func SkillsDir(agent, projectRoot string) string {
	for _, rel := range knownSkillsDirs {
		full := filepath.Join(projectRoot, rel)
		if _, err := os.Stat(full); err == nil {
			return full
		}
	}
	switch agent {
	case "claude-code":
		return filepath.Join(projectRoot, ".claude", "skills")
	case "cursor":
		return filepath.Join(projectRoot, ".cursor", "skills")
	case "windsurf":
		return filepath.Join(projectRoot, ".windsurf", "skills")
	case "gemini-code":
		return filepath.Join(projectRoot, ".gemini", "skills")
	default:
		return filepath.Join(projectRoot, ".agents", "skills")
	}
}

// FindProjectRoot walks up from cwd looking for a directory containing .git.
// Returns cwd if no .git ancestor is found.
func FindProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	cwd, _ := os.Getwd()
	return cwd
}

// extractDescription parses YAML frontmatter (between first and second "---" delimiters)
// and returns the value of the "description:" field. Returns "" if not found.
func extractDescription(content string) string {
	lines := strings.Split(content, "\n")
	inFrontmatter := false
	delimCount := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "---" {
			delimCount++
			if delimCount == 1 {
				inFrontmatter = true
				continue
			}
			if delimCount == 2 {
				break
			}
		}
		if inFrontmatter {
			if strings.HasPrefix(line, "description:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
				return strings.Trim(val, `"'`)
			}
		}
	}
	return ""
}
