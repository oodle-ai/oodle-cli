package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/oodle-ai/oodle-cli/internal/client"
)

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}

	return path
}

// A field of the API carries a `json` tag alone, thus a YAML decoder that
// matches the lowercased Go name drops every name that holds an underscore.
// The notifier arrived with a name and a type and no configuration at all.
func TestReadInputFileKeepsTheUnderscoredFields(t *testing.T) {
	path := writeTemp(t, "notifier.yaml", `
name: rootly-notifier
type: 7
rootly_config:
  url: https://example.com/hook
  send_resolved: true
  max_alerts: 0
  payload:
    summary: "{{ .CommonLabels.alertname }}"
    nested:
      key: value
`)

	var notifier client.Notifier
	if err := readInputFile(path, &notifier); err != nil {
		t.Fatalf("reading the file: %v", err)
	}

	if notifier.RootlyConfig == nil {
		t.Fatal("rootly_config was dropped")
	}
	if notifier.RootlyConfig.Url != "https://example.com/hook" {
		t.Fatalf("unexpected url %q", notifier.RootlyConfig.Url)
	}
	if !notifier.RootlyConfig.SendResolved {
		t.Fatal("send_resolved was dropped")
	}

	if notifier.RootlyConfig.Payload == nil {
		t.Fatal("payload was dropped")
	}

	payload := *notifier.RootlyConfig.Payload
	if payload["summary"] != "{{ .CommonLabels.alertname }}" {
		t.Fatalf("unexpected summary %v", payload["summary"])
	}

	// A nested object has to survive as an object, so that a payload of any
	// depth reaches the API as it was written.
	nested, ok := payload["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested is %T, not an object", payload["nested"])
	}
	if nested["key"] != "value" {
		t.Fatalf("unexpected nested value %v", nested["key"])
	}
}

func TestReadInputFileReadsJSON(t *testing.T) {
	path := writeTemp(t, "notifier.json", `{
  "name": "webhook-notifier",
  "type": 4,
  "webhook_config": {
    "url": "https://example.com/hook",
    "send_resolved": false,
    "max_alerts": 0,
    "payload": {"text": "{{ .Status }}"}
  }
}`)

	var notifier client.Notifier
	if err := readInputFile(path, &notifier); err != nil {
		t.Fatalf("reading the file: %v", err)
	}

	if notifier.WebhookConfig == nil || notifier.WebhookConfig.Payload == nil {
		t.Fatal("webhook_config or its payload was dropped")
	}
	if (*notifier.WebhookConfig.Payload)["text"] != "{{ .Status }}" {
		t.Fatal("the payload did not survive")
	}
}

func TestReadInputFileReportsBadInput(t *testing.T) {
	path := writeTemp(t, "notifier.json", `{"name": `)

	var notifier client.Notifier
	if err := readInputFile(path, &notifier); err == nil {
		t.Fatal("expected an error for a file that does not parse")
	}
}
