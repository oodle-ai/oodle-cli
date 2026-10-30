package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/oodle-ai/oodle-cli/internal/api"
	"github.com/oodle-ai/oodle-cli/internal/config"
)

// setupSpecDomainFields are the integration record fields that hold the
// collector domains of the instance. Every record carries all of them,
// whatever its type, so any record gives the values.
var setupSpecDomainFields = []string{
	"collectorDomain",
	"logsCollectorDomain",
	"otlpCollectorDomain",
	"apiDomain",
}

// missingDomainValues returns the domain fields that no integration
// record gave. The domains are stored per instance on the server, so the
// CLI cannot compute them, and the agent must hear that they are not
// filled.
func missingDomainValues(values map[string]string) []string {
	var missing []string
	for _, field := range setupSpecDomainFields {
		if values[field] == "" {
			missing = append(missing, field)
		}
	}
	return missing
}

// setupSpecPlaceholder matches a `{{name}}` placeholder, with the `$`
// before it if there is one. A `${{ ... }}` expression is GitHub Actions
// syntax in a CI snippet, so fillSetupSpec keeps a match that starts with
// `$` as it is.
var setupSpecPlaceholder = regexp.MustCompile(`\$?\{\{\s*([A-Za-z_]\w*)\s*\}\}`)

// loadSetupSpecClient returns an authenticated client and its config when
// the user is logged in, or an unauthenticated client and a nil config.
// The spec endpoints need no login, but with one the command can fill in
// the values of the instance.
func loadSetupSpecClient(cmd *cobra.Command) (*api.Client, *config.Config, error) {
	// A client that a caller put on the context (tests do) wins. It
	// comes with no config, so nothing is filled in.
	if c := getClient(cmd); c != nil {
		return c, nil, nil
	}
	apiKey, _ := cmd.Flags().GetString("api-key")
	instance, _ := cmd.Flags().GetString("instance")
	apiURL, _ := cmd.Flags().GetString("api-url")
	retries, _ := cmd.Flags().GetInt("retries")

	cfg, err := config.LoadConfig(apiKey, instance, apiURL)
	if err == nil && cfg.Instance != "" &&
		(cfg.APIKey != "" || cfg.OAuthAccessToken != "") {
		if c, err := api.NewClient(cfg, retries); err == nil {
			return c, cfg, nil
		}
	}
	c, err := newUnauthenticatedClient(cmd)
	return c, nil, err
}

// instanceSetupValues returns the Oodle values of the instance that a spec
// can name: the instance ID, the API URL and the collector domains. The API
// key is a secret and is not in the result, so a spec printed to a terminal
// or an agent transcript does not show it.
func instanceSetupValues(cmd *cobra.Command, c *api.Client, cfg *config.Config) (map[string]string, error) {
	values := map[string]string{
		"instanceId": cfg.Instance,
		"apiUrl":     strings.TrimRight(cfg.APIURL, "/"),
	}

	resp, err := c.Inner.ListIntegrations(cmd.Context(), cfg.Instance)
	if err != nil {
		return nil, fmt.Errorf("listing integrations: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading integrations: %w", err)
	}
	if resp.StatusCode >= 300 {
		return nil, api.CheckResponse(resp, body)
	}
	var records []map[string]interface{}
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, fmt.Errorf("parsing integrations: %w", err)
	}
	for _, record := range records {
		for _, field := range setupSpecDomainFields {
			if v, ok := record[field].(string); ok && v != "" && values[field] == "" {
				values[field] = v
			}
		}
	}
	return values, nil
}

// fillSetupSpec replaces each `{{name}}` that has a value, in every string
// of the spec. It returns the names it filled, sorted.
func fillSetupSpec(spec map[string]interface{}, values map[string]string) []string {
	filled := map[string]bool{}
	var walk func(v interface{}) interface{}
	walk = func(v interface{}) interface{} {
		switch t := v.(type) {
		case string:
			return setupSpecPlaceholder.ReplaceAllStringFunc(t, func(m string) string {
				if strings.HasPrefix(m, "$") {
					return m
				}
				name := setupSpecPlaceholder.FindStringSubmatch(m)[1]
				value, ok := values[name]
				if !ok || value == "" {
					return m
				}
				filled[name] = true
				return value
			})
		case map[string]interface{}:
			for k, item := range t {
				t[k] = walk(item)
			}
			return t
		case []interface{}:
			for i, item := range t {
				t[i] = walk(item)
			}
			return t
		}
		return v
	}
	walk(spec)

	names := make([]string, 0, len(filled))
	for name := range filled {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// summarizeSetupSpec keeps only what an agent needs to choose a method:
// each method's label, recommended flag, whenToUse and prerequisites. The
// steps are the large part of a spec, and an agent reads them only for
// the method it uses.
func summarizeSetupSpec(spec map[string]interface{}) {
	methods, _ := spec["setupMethods"].(map[string]interface{})
	for name, m := range methods {
		method, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		short := map[string]interface{}{}
		for _, key := range []string{"label", "recommended", "whenToUse", "prerequisites"} {
			if v, ok := method[key]; ok {
				short[key] = v
			}
		}
		methods[name] = short
	}
}

// selectSetupMethod keeps only the named method in the spec.
func selectSetupMethod(spec map[string]interface{}, name string) error {
	methods, _ := spec["setupMethods"].(map[string]interface{})
	method, ok := methods[name]
	if !ok {
		names := make([]string, 0, len(methods))
		for n := range methods {
			names = append(names, n)
		}
		sort.Strings(names)
		return fmt.Errorf("unknown setup method %q; the methods are: %s", name, strings.Join(names, ", "))
	}
	spec["setupMethods"] = map[string]interface{}{name: method}
	return nil
}
