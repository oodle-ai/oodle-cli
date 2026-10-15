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

// Client is a minimal NerdGraph client.
type Client struct {
	URL    string
	APIKey string
	HTTP   *http.Client
}

func NewClient(rawURL, apiKey string) (*Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("invalid New Relic URL %q (expected e.g. %s)", rawURL, DefaultURL)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("New Relic API key is empty")
	}
	return &Client{URL: rawURL, APIKey: apiKey, HTTP: &http.Client{Timeout: 60 * time.Second}}, nil
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
	Dashboards  int
	Policies    int
	Conditions  int
	Accounts    int
	AccountErrs []string
}

// Export writes dashboards and alerts as JSON files under dir.
func (c *Client) Export(ctx context.Context, dir string, log io.Writer) (*Report, error) {
	r := &Report{}
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
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
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
		p, err := c.exportAlertKind(ctx, policiesQuery, "policiesSearch", "policies", a.ID, filepath.Join(acctDir, "policies"))
		r.Policies += p
		if err == nil {
			var n int
			n, err = c.exportAlertKind(ctx, conditionsQuery, "nrqlConditionsSearch", "nrqlConditions", a.ID, filepath.Join(acctDir, "conditions"))
			r.Conditions += n
		}
		if err != nil {
			// Keys often lack alert access on some accounts; keep exporting the rest.
			msg := fmt.Sprintf("account %d (%s): %v", a.ID, a.Name, err)
			r.AccountErrs = append(r.AccountErrs, msg)
			fmt.Fprintf(log, "  warning: %s\n", msg)
			continue
		}
		fmt.Fprintf(log, "  alerts: account %d (%s) done\n", a.ID, a.Name)
	}
	return nil
}

// exportAlertKind pages through account.alerts.<search>.<list>, writing each item as <id>.json.
func (c *Client) exportAlertKind(ctx context.Context, q, search, list string, accountID int, dir string) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	count := 0
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
			return count, err
		}
		page := d.Actor.Account.Alerts[search]
		var items []json.RawMessage
		if err := json.Unmarshal(page[list], &items); err != nil && page[list] != nil {
			return count, err
		}
		for _, it := range items {
			var id struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(it, &id) != nil || id.ID == "" {
				continue
			}
			if err := writeJSON(filepath.Join(dir, id.ID+".json"), it); err != nil {
				return count, err
			}
			count++
		}
		var next *string
		_ = json.Unmarshal(page["nextCursor"], &next)
		if next == nil || *next == "" {
			return count, nil
		}
		cursor = *next
	}
}

func writeJSON(path string, raw json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
