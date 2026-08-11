package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/config"
)

// TestPromptForConfig_SkipsSuppliedValues verifies that values passed as flags
// (or env vars) are never prompted for: the integration docs tell users to run
// `oodle configure --instance <> --api-key <>`, and re-asking for them is both
// confusing and destructive when stdin has no answers.
func TestPromptForConfig_SkipsSuppliedValues(t *testing.T) {
	var out bytes.Buffer
	// Empty stdin: any prompt we do not expect would silently take a default
	// instead of the supplied value, which the assertions below catch.
	key, inst, url, err := promptForConfig(strings.NewReader(""), &out, "flagkey", "flaginst", "",
		suppliedValues{apiKey: true, instance: true})
	if err != nil {
		t.Fatalf("promptForConfig: %v", err)
	}
	if key != "flagkey" {
		t.Errorf("api key = %q, want flagkey", key)
	}
	if inst != "flaginst" {
		t.Errorf("instance = %q, want flaginst", inst)
	}
	if url != config.DefaultAPIURL {
		t.Errorf("api url = %q, want %q", url, config.DefaultAPIURL)
	}
	if strings.Contains(out.String(), "Instance ID") {
		t.Errorf("prompted for instance despite --instance; output: %q", out.String())
	}
	if strings.Contains(out.String(), "API key") {
		t.Errorf("prompted for API key despite --api-key; output: %q", out.String())
	}
	if !strings.Contains(out.String(), "Oodle API URL") {
		t.Errorf("expected a prompt for the missing API URL; output: %q", out.String())
	}
}

// TestPromptForConfig_PromptsForMissingValues confirms the partial case still
// asks for what was not supplied.
func TestPromptForConfig_PromptsForMissingValues(t *testing.T) {
	var out bytes.Buffer
	in := strings.NewReader("https://example.test\nmy-instance\n")
	key, inst, url, err := promptForConfig(in, &out, "flagkey", "", "",
		suppliedValues{apiKey: true})
	if err != nil {
		t.Fatalf("promptForConfig: %v", err)
	}
	if key != "flagkey" {
		t.Errorf("api key = %q, want flagkey (supplied via flag)", key)
	}
	if inst != "my-instance" {
		t.Errorf("instance = %q, want my-instance", inst)
	}
	if url != "https://example.test" {
		t.Errorf("api url = %q, want https://example.test", url)
	}
	if !strings.Contains(out.String(), "Instance ID") {
		t.Errorf("expected an instance prompt; output: %q", out.String())
	}
}

// TestRunConfigure_FlagsOnlyWritesConfig checks the end-to-end path used by the
// integration docs: both credentials on the command line, nothing prompted, and
// the config file written with exactly those values.
func TestRunConfigure_FlagsOnlyWritesConfig(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("OODLE_CONFIG", cfgPath)
	t.Setenv("OODLE_API_KEY", "")
	t.Setenv("OODLE_INSTANCE", "")
	t.Setenv("OODLE_DEPLOYMENT", "")
	t.Setenv("OODLE_API_URL", "")

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetIn(strings.NewReader(""))
	// Point at a closed port with no retries so validation fails fast; the
	// command warns and still succeeds.
	root.SetArgs([]string{"configure", "--api-key", "k1", "--instance", "inst1",
		"--api-url", "http://127.0.0.1:1", "--retries", "0"})
	if err := root.Execute(); err != nil {
		t.Fatalf("configure: %v", err)
	}

	if strings.Contains(out.String(), "Instance ID") || strings.Contains(out.String(), "API key:") {
		t.Errorf("configure prompted despite flags; output: %q", out.String())
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading saved config: %v", err)
	}
	saved := string(data)
	for _, want := range []string{"k1", "inst1", "http://127.0.0.1:1"} {
		if !strings.Contains(saved, want) {
			t.Errorf("saved config missing %q; got: %s", want, saved)
		}
	}
}
