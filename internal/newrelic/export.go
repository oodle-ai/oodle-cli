// Package newrelic exports New Relic dashboards and alerts via NerdGraph.
package newrelic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultURL = "https://api.newrelic.com/graphql"
	EUURL      = "https://api.eu.newrelic.com/graphql"
)

// KeyHelp explains which key to create when validation fails.
const KeyHelp = `New Relic needs a USER key (starts with "NRAK-"); license, ingest and browser keys will not work.

To create one:
  1. Open the API keys page:
       US accounts: https://one.newrelic.com/api-keys
       EU accounts: https://one.eu.newrelic.com/api-keys
  2. Click "Create a key", choose Key type "User", pick the account, and give it a name.
  3. The key inherits your user's permissions, so your user needs read access to
     Dashboards and Alerts (e.g. a "Read only" or "All product admin" role) on every
     account you want exported.
  4. EU-region accounts must also pass --url ` + EUURL + `

Docs: https://docs.newrelic.com/docs/apis/intro-apis/new-relic-api-keys/#user-key`

// Client talks to NerdGraph plus the REST v2 and Infrastructure APIs, which
// hold the alert condition types NerdGraph does not expose.
type Client struct {
	URL      string // NerdGraph
	RESTURL  string // e.g. https://api.newrelic.com/v2
	InfraURL string // e.g. https://infra-api.newrelic.com/v2
	APIKey   string
	HTTP     *http.Client
}

func NewClient(rawURL, apiKey string) (*Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("invalid New Relic URL %q (expected e.g. %s)", rawURL, DefaultURL)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("New Relic API key is empty")
	}
	return &Client{
		URL:      rawURL,
		RESTURL:  u.Scheme + "://" + u.Host + "/v2",
		InfraURL: u.Scheme + "://infra-" + u.Host + "/v2",
		APIKey:   apiKey,
		HTTP:     &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// query runs a GraphQL query and decodes its "data" into out.
func (c *Client) query(ctx context.Context, q string, vars map[string]any, out any) error {
	body, _ := json.Marshal(map[string]any{"query": q, "variables": vars})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("API-Key", c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("NerdGraph returned HTTP %d: %s", resp.StatusCode, truncate(raw))
	}
	var env struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decoding NerdGraph response: %w (body: %s)", err, truncate(raw))
	}
	if len(env.Errors) > 0 {
		msgs := make([]string, len(env.Errors))
		for i, e := range env.Errors {
			msgs[i] = e.Message
		}
		return fmt.Errorf("NerdGraph error: %s", strings.Join(msgs, "; "))
	}
	return json.Unmarshal(env.Data, out)
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "..."
	}
	return string(b)
}

// Validate checks the URL and key with a cheap identity query and returns the user's email.
func (c *Client) Validate(ctx context.Context) (string, error) {
	var d struct {
		Actor struct {
			User struct {
				Email string `json:"email"`
			} `json:"user"`
		} `json:"actor"`
	}
	if err := c.query(ctx, `{ actor { user { email } } }`, nil, &d); err != nil {
		return "", err
	}
	if d.Actor.User.Email == "" {
		return "", fmt.Errorf("key did not resolve to a user")
	}
	return d.Actor.User.Email, nil
}

// Report counts what was written.
type Report struct {
	Dashboards int
	Policies   int
	Conditions map[string]int // keyed by ConditionKinds Dir
	Accounts   int
	Warnings   []string
}

// Export writes dashboards and alerts as JSON files under dir.
func (c *Client) Export(ctx context.Context, dir string, log io.Writer) (*Report, error) {
	r := &Report{Conditions: map[string]int{}}
	if err := c.exportDashboards(ctx, filepath.Join(dir, "dashboards"), r, log); err != nil {
		return nil, fmt.Errorf("exporting dashboards: %w", err)
	}
	if err := c.exportAlerts(ctx, filepath.Join(dir, "alerts"), r, log); err != nil {
		return nil, fmt.Errorf("exporting alerts: %w", err)
	}
	return r, nil
}

const dashboardSearchQuery = `query($cursor: String) { actor { entitySearch(query: "type = 'DASHBOARD'") { results(cursor: $cursor) {
  nextCursor
  entities { guid ... on DashboardEntityOutline { dashboardParentGuid } }
} } } }`

// Fields match the dashboardCreate input so the JSON can be re-imported.
const dashboardFetchQuery = `query($guids: [EntityGuid]!) { actor { entities(guids: $guids) { ... on DashboardEntity {
  guid name accountId description permissions
  pages { guid name description widgets {
    id title rawConfiguration
    layout { column row width height }
    visualization { id }
    linkedEntities { guid }
  } }
  variables {
    name title type isMultiSelection replacementStrategy
    defaultValues { value { string } }
    items { title value }
    nrqlQuery { accountIds query }
  }
} } } }`

func (c *Client) exportDashboards(ctx context.Context, dir string, r *Report, log io.Writer) error {
	var guids []string
	var cursor any
	for {
		var d struct {
			Actor struct {
				EntitySearch struct {
					Results struct {
						NextCursor *string `json:"nextCursor"`
						Entities   []struct {
							GUID                string  `json:"guid"`
							DashboardParentGUID *string `json:"dashboardParentGuid"`
						} `json:"entities"`
					} `json:"results"`
				} `json:"entitySearch"`
			} `json:"actor"`
		}
		if err := c.query(ctx, dashboardSearchQuery, map[string]any{"cursor": cursor}, &d); err != nil {
			return err
		}
		res := d.Actor.EntitySearch.Results
		for _, e := range res.Entities {
			// Pages of multi-page dashboards are entities too; their parent carries them.
			if e.DashboardParentGUID == nil {
				guids = append(guids, e.GUID)
			}
		}
		if res.NextCursor == nil || *res.NextCursor == "" {
			break
		}
		cursor = *res.NextCursor
	}
	fmt.Fprintf(log, "  found %d dashboards\n", len(guids))

	// entities(guids:) accepts at most 25 GUIDs per call.
	for i := 0; i < len(guids); i += 25 {
		batch := guids[i:min(i+25, len(guids))]
		var d struct {
			Actor struct {
				Entities []json.RawMessage `json:"entities"`
			} `json:"actor"`
		}
		if err := c.query(ctx, dashboardFetchQuery, map[string]any{"guids": batch}, &d); err != nil {
			return err
		}
		for _, e := range d.Actor.Entities {
			var id struct {
				GUID string `json:"guid"`
			}
			if json.Unmarshal(e, &id) != nil || id.GUID == "" {
				continue
			}
			if err := writeJSON(filepath.Join(dir, id.GUID+".json"), e); err != nil {
				return err
			}
			r.Dashboards++
		}
		fmt.Fprintf(log, "  dashboards: %d/%d\n", r.Dashboards, len(guids))
	}
	return nil
}

const accountsQuery = `{ actor { accounts { id name } } }`

const policiesQuery = `query($id: Int!, $cursor: String) { actor { account(id: $id) { alerts { policiesSearch(cursor: $cursor) {
  nextCursor
  policies { id name accountId incidentPreference }
} } } } }`

const conditionsQuery = `query($id: Int!, $cursor: String) { actor { account(id: $id) { alerts { nrqlConditionsSearch(cursor: $cursor) {
  nextCursor
  nrqlConditions {
    id name type enabled description policyId runbookUrl violationTimeLimitSeconds
    nrql { query }
    terms { operator priority threshold thresholdDuration thresholdOccurrences }
    signal { aggregationDelay aggregationMethod aggregationTimer aggregationWindow fillOption fillValue slideBy }
    expiration { closeViolationsOnExpiration expirationDuration openViolationOnExpiration }
    ... on AlertsNrqlBaselineCondition { baselineDirection }
  }
} } } } }`

// ConditionKinds lists every condition type exported, in report order; Dir is
// its folder under alerts/<account>/.
var ConditionKinds = []struct{ Dir, Label string }{
	{"nrql_conditions", "NRQL"},
	{"apm_conditions", "APM, browser and mobile"},
	{"external_service_conditions", "External service"},
	{"synthetics_conditions", "Synthetics"},
	{"location_failure_conditions", "Synthetics multi-location"},
	{"infra_conditions", "Infrastructure"},
}

// restKinds are the per-policy REST v2 condition endpoints; NerdGraph does not expose these types.
var restKinds = []struct{ dir, path, key string }{
	{"apm_conditions", "alerts_conditions.json?policy_id=%s", "conditions"},
	{"external_service_conditions", "alerts_external_service_conditions.json?policy_id=%s", "external_service_conditions"},
	{"synthetics_conditions", "alerts_synthetics_conditions.json?policy_id=%s", "synthetics_conditions"},
	{"location_failure_conditions", "alerts_location_failure_conditions/policies/%s.json?policy_id=%s", "location_failure_conditions"},
}

func (c *Client) exportAlerts(ctx context.Context, dir string, r *Report, log io.Writer) error {
	var d struct {
		Actor struct {
			Accounts []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"accounts"`
		} `json:"actor"`
	}
	if err := c.query(ctx, accountsQuery, nil, &d); err != nil {
		return err
	}
	r.Accounts = len(d.Actor.Accounts)
	for _, a := range d.Actor.Accounts {
		acctDir := filepath.Join(dir, fmt.Sprint(a.ID))
		// Keys often lack alert access on some accounts; warn and keep exporting the rest.
		warn := func(what string, err error) {
			msg := fmt.Sprintf("account %d (%s) %s: %v", a.ID, a.Name, what, err)
			r.Warnings = append(r.Warnings, msg)
			fmt.Fprintf(log, "  warning: %s\n", msg)
		}

		policyIDs, err := c.exportGraphQLList(ctx, policiesQuery, "policiesSearch", "policies", a.ID, filepath.Join(acctDir, "policies"))
		r.Policies += len(policyIDs)
		if err != nil {
			warn("policies", err)
			continue
		}
		ids, err := c.exportGraphQLList(ctx, conditionsQuery, "nrqlConditionsSearch", "nrqlConditions", a.ID, filepath.Join(acctDir, "nrql_conditions"))
		r.Conditions["nrql_conditions"] += len(ids)
		if err != nil {
			warn("NRQL conditions", err)
		}

		for _, k := range restKinds {
			for _, p := range policyIDs {
				n, err := c.exportREST(ctx, c.RESTURL+"/"+strings.ReplaceAll(k.path, "%s", p), k.key, filepath.Join(acctDir, k.dir))
				r.Conditions[k.dir] += n
				if err != nil {
					warn(k.dir, err)
					break
				}
			}
		}
		for _, p := range policyIDs {
			n, err := c.exportInfra(ctx, p, filepath.Join(acctDir, "infra_conditions"))
			r.Conditions["infra_conditions"] += n
			if err != nil {
				warn("infra_conditions", err)
				break
			}
		}
		fmt.Fprintf(log, "  alerts: account %d (%s): %d policies\n", a.ID, a.Name, len(policyIDs))
	}
	return nil
}

// exportGraphQLList pages through account.alerts.<search>.<list>, writing each
// item as <id>.json, and returns the IDs written.
func (c *Client) exportGraphQLList(ctx context.Context, q, search, list string, accountID int, dir string) ([]string, error) {
	var ids []string
	var cursor any
	for {
		var d struct {
			Actor struct {
				Account struct {
					Alerts map[string]map[string]json.RawMessage `json:"alerts"`
				} `json:"account"`
			} `json:"actor"`
		}
		if err := c.query(ctx, q, map[string]any{"id": accountID, "cursor": cursor}, &d); err != nil {
			return ids, err
		}
		page := d.Actor.Account.Alerts[search]
		written, err := writeItems(dir, page[list])
		ids = append(ids, written...)
		if err != nil {
			return ids, err
		}
		var next *string
		_ = json.Unmarshal(page["nextCursor"], &next)
		if next == nil || *next == "" {
			return ids, nil
		}
		cursor = *next
	}
}

// exportREST pages a REST v2 list endpoint (?page=N, Link rel="next").
func (c *Client) exportREST(ctx context.Context, endpoint, key, dir string) (int, error) {
	n := 0
	for page := 1; ; page++ {
		body, hdr, err := c.get(ctx, fmt.Sprintf("%s&page=%d", endpoint, page))
		if err != nil {
			return n, err
		}
		var env map[string]json.RawMessage
		if err := json.Unmarshal(body, &env); err != nil {
			return n, fmt.Errorf("decoding %s: %w", key, err)
		}
		written, err := writeItems(dir, env[key])
		n += len(written)
		if err != nil || len(written) == 0 || !strings.Contains(hdr.Get("Link"), `rel="next"`) {
			return n, err
		}
	}
}

// exportInfra pages the Infrastructure alerts API (offset/limit with meta.total).
func (c *Client) exportInfra(ctx context.Context, policyID, dir string) (int, error) {
	n := 0
	for offset := 0; ; {
		body, _, err := c.get(ctx, fmt.Sprintf("%s/alerts/conditions?policy_id=%s&offset=%d&limit=50", c.InfraURL, policyID, offset))
		if err != nil {
			return n, err
		}
		var env struct {
			Data json.RawMessage `json:"data"`
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return n, fmt.Errorf("decoding infra conditions: %w", err)
		}
		written, err := writeItems(dir, env.Data)
		n += len(written)
		offset += len(written)
		if err != nil || len(written) == 0 || offset >= env.Meta.Total {
			return n, err
		}
	}
}

func (c *Client) get(ctx context.Context, u string) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Api-Key", c.APIKey)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(body))
	}
	return body, resp.Header, nil
}

// writeItems writes each element of a JSON array as dir/<id>.json and returns the IDs.
func writeItems(dir string, list json.RawMessage) ([]string, error) {
	if len(list) == 0 || string(list) == "null" {
		return nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(list, &items); err != nil {
		return nil, err
	}
	var ids []string
	for _, it := range items {
		var v struct {
			ID json.RawMessage `json:"id"`
		}
		// IDs are strings in NerdGraph and numbers in the REST APIs.
		id := ""
		if json.Unmarshal(it, &v) == nil {
			id = strings.Trim(string(v.ID), `"`)
		}
		if id == "" || id == "null" {
			continue
		}
		if err := writeJSON(filepath.Join(dir, id+".json"), it); err != nil {
			return ids, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// writeJSON creates the parent directory on demand so empty kinds leave no folder.
func writeJSON(path string, raw json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
