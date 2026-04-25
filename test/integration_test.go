//go:build integration

package test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var oodleBin string

func TestMain(m *testing.M) {
	// Skip if credentials not set.
	if os.Getenv("OODLE_API_KEY") == "" || os.Getenv("OODLE_INSTANCE") == "" {
		os.Exit(0)
	}

	// Build the binary.
	tmpDir, err := os.MkdirTemp("", "oodle-test-*")
	if err != nil {
		panic("failed to create temp dir: " + err.Error())
	}
	oodleBin = filepath.Join(tmpDir, "oodle")
	cmd := exec.Command("go", "build", "-o", oodleBin, "./cmd/oodle")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		panic("build failed: " + string(out))
	}

	os.Exit(m.Run())
}

// runOodle runs the oodle binary with the given args and returns
// stdout, stderr, and the exit code. It uses the parent process's
// environment by default; pass extra setup via runOodleEnv.
func runOodle(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runOodleEnv(t, nil, args...)
}

// runOodleEnv is like runOodle but allows overriding the environment.
// If env is nil, the parent process's environment is inherited.
func runOodleEnv(t *testing.T, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(oodleBin, args...)
	if env != nil {
		cmd.Env = env
	}
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run oodle: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// assertValidJSON parses the given string as JSON and fails the test
// if it does not parse. Both arrays and objects are accepted.
func assertValidJSON(t *testing.T, stdout string) {
	t.Helper()
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		t.Fatalf("expected non-empty JSON output, got empty string")
	}
	var v any
	if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
		t.Fatalf("expected valid JSON, got error %v\noutput:\n%s", err, stdout)
	}
}

// envWithout returns a copy of os.Environ() with the given keys removed.
func envWithout(keys ...string) []string {
	skip := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		skip[k] = struct{}{}
	}
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			out = append(out, kv)
			continue
		}
		if _, drop := skip[kv[:eq]]; drop {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func TestVersion(t *testing.T) {
	stdout, stderr, code := runOodle(t, "version")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "oodle version") {
		t.Fatalf("expected stdout to contain 'oodle version', got: %q", stdout)
	}
}

func TestAuthError(t *testing.T) {
	stdout, stderr, code := runOodle(t,
		"monitors", "list",
		"--api-key", "invalid-key-12345",
		"--output", "json",
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit code, got 0\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	combined := strings.ToLower(stdout + stderr)
	// Match a few likely phrasings: "auth", "unauthor", "401", "forbidden", "invalid api key".
	keywords := []string{"auth", "unauthor", "401", "forbidden", "invalid"}
	found := false
	for _, kw := range keywords {
		if strings.Contains(combined, kw) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected stderr/stdout to mention an auth/authorization error, got\nstdout: %s\nstderr: %s", stdout, stderr)
	}
}

func TestMissingInstance(t *testing.T) {
	// Build an env that has the API key (so auth doesn't fail first) but no instance.
	// We also ensure no --instance flag is passed.
	env := envWithout("OODLE_INSTANCE")
	stdout, stderr, code := runOodleEnv(t, env,
		"monitors", "list",
		"--output", "json",
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit code when OODLE_INSTANCE is missing, got 0\nstdout: %s\nstderr: %s", stdout, stderr)
	}
	combined := strings.ToLower(stdout + stderr)
	if !strings.Contains(combined, "instance") {
		t.Fatalf("expected error mentioning 'instance', got\nstdout: %s\nstderr: %s", stdout, stderr)
	}
}

func TestOutputFormats(t *testing.T) {
	// JSON.
	stdout, stderr, code := runOodle(t, "monitors", "list", "--output", "json")
	if code != 0 {
		t.Fatalf("monitors list --output json failed: code=%d stderr=%s", code, stderr)
	}
	assertValidJSON(t, stdout)

	// YAML.
	stdout, stderr, code = runOodle(t, "monitors", "list", "--output", "yaml")
	if code != 0 {
		t.Fatalf("monitors list --output yaml failed: code=%d stderr=%s", code, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatalf("expected non-empty YAML output, got empty string")
	}

	// CSV.
	stdout, stderr, code = runOodle(t, "monitors", "list", "--output", "csv")
	if code != 0 {
		t.Fatalf("monitors list --output csv failed: code=%d stderr=%s", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("expected at least a CSV header line, got empty output")
	}
	if !strings.Contains(lines[0], ",") {
		t.Fatalf("expected CSV header to contain a comma, got: %q", lines[0])
	}
}

// listJSONTest is a helper for the many "<group> list --output json" tests.
func listJSONTest(t *testing.T, args ...string) {
	t.Helper()
	allArgs := append(args, "--output", "json")
	stdout, stderr, code := runOodle(t, allArgs...)
	if code != 0 {
		t.Fatalf("%v failed: code=%d stderr=%s", allArgs, code, stderr)
	}
	assertValidJSON(t, stdout)
}

func TestMonitorsList(t *testing.T) {
	listJSONTest(t, "monitors", "list")
}

func TestMonitorTriggers(t *testing.T) {
	listJSONTest(t, "monitors", "triggers")
}

func TestNotifiersList(t *testing.T) {
	listJSONTest(t, "notifiers", "list")
}

func TestNotificationPoliciesList(t *testing.T) {
	listJSONTest(t, "notification-policies", "list")
}

func TestMutingRulesList(t *testing.T) {
	listJSONTest(t, "muting-rules", "list")
}

func TestLogMetricsList(t *testing.T) {
	listJSONTest(t, "log-metrics", "list")
}

func TestSyntheticMonitorsList(t *testing.T) {
	listJSONTest(t, "synthetic-monitors", "list")
}

func TestDashboardsList(t *testing.T) {
	t.Skip("server returns 500 for grafana/dashboards on dev environment")
	listJSONTest(t, "dashboards", "list")
}

func TestFoldersList(t *testing.T) {
	t.Skip("server returns 500 for grafana/folders on dev environment")
	listJSONTest(t, "folders", "list")
}

func TestDropRulesList(t *testing.T) {
	listJSONTest(t, "drop-rules", "list")
}

func TestMetricsNames(t *testing.T) {
	t.Skip("server-side metrics endpoint does not yet read start/end query params")
	stdout, stderr, code := runOodle(t, "metrics", "names", "--start", "-1h", "--end", "now", "--output", "json")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\nstderr: %s", code, stderr)
	}
	assertValidJSON(t, stdout)
}

func TestTracesLabels(t *testing.T) {
	listJSONTest(t, "traces", "labels")
}

func TestApiKeysList(t *testing.T) {
	listJSONTest(t, "api-keys", "list")
}

func TestUsersList(t *testing.T) {
	t.Skip("API key lacks permission for users endpoint (403)")
	listJSONTest(t, "users", "list")
}

func TestNotFoundError(t *testing.T) {
	stdout, stderr, code := runOodle(t,
		"monitors", "get",
		"nonexistent-id-00000000-0000-0000-0000-000000000000",
		"--output", "json",
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit code for nonexistent monitor, got 0\nstdout: %s\nstderr: %s", stdout, stderr)
	}
}
