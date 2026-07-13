package grafana

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

)

// Client is a Grafana API client
type Client struct {
	httpClient *http.Client
	baseURL    string
	token      string
}

// NewClient creates a new Grafana API client
func NewClient(baseURL, token string) *Client {
	return &Client{
		httpClient: createHTTPClient(),
		baseURL:    baseURL,
		token:      token,
	}
}

func createHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			IdleConnTimeout: 90 * time.Second,
		},
	}
}

// DoRequest performs an HTTP request to the Grafana API
func (c *Client) DoRequest(
	ctx context.Context,
	method string,
	path string,
) ([]byte, error) {
	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return nil, wrapErr(err, "failed to create request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, wrapErr(err, "request failed")
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, wrapErr(err, "failed to read response body")
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf(
			"request failed with status %d: %s",
			resp.StatusCode,
			string(body),
		)
	}

	return body, nil
}

// CheckHealth checks the Grafana API health
func (c *Client) CheckHealth(ctx context.Context) error {
	_, err := c.DoRequest(ctx, http.MethodGet, "/api/health")
	if err != nil {
		return wrapErr(err, "health check failed")
	}
	return nil
}

// GetDashboards retrieves all dashboards from Grafana
func (c *Client) GetDashboards(ctx context.Context) ([]Dashboard, error) {
	respBody, err := c.DoRequest(ctx, http.MethodGet, "/api/search?type=dash-db")
	if err != nil {
		return nil, wrapErr(err, "failed to get dashboards")
	}

	var dashboards []Dashboard
	if err := json.Unmarshal(respBody, &dashboards); err != nil {
		return nil, wrapErr(err, "failed to parse dashboards")
	}

	return dashboards, nil
}

// GetDashboardByUID retrieves a dashboard by its UID
func (c *Client) GetDashboardByUID(
	ctx context.Context,
	uid string,
) ([]byte, error) {
	path := fmt.Sprintf("/api/dashboards/uid/%s", uid)
	respBody, err := c.DoRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, wrapErrf(err, "failed to get dashboard %s", uid)
	}
	return respBody, nil
}

// GetFolders retrieves folders from Grafana, optionally filtered by parent UID
// If nested folders is not enabled parentUid is not used and the immediate
// subfolders under the root are returned.
// Reg: https://grafana.com/docs/grafana/latest/developer-resources/api-reference/http-api/folder/
func (c *Client) GetFolders(
	ctx context.Context,
	parentUID string,
) ([]Folder, error) {
	path := "/api/folders"
	if parentUID != "" {
		path = fmt.Sprintf("%s?parentUid=%s", path, parentUID)
	}

	respBody, err := c.DoRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, wrapErr(err, "failed to get folders")
	}

	var folders []Folder
	if err := json.Unmarshal(respBody, &folders); err != nil {
		return nil, wrapErr(err, "failed to parse folders")
	}

	return folders, nil
}

// GetFolderByUID retrieves a folder by its UID
func (c *Client) GetFolderByUID(ctx context.Context, uid string) ([]byte, error) {
	path := fmt.Sprintf("/api/folders/%s", uid)
	respBody, err := c.DoRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, wrapErrf(err, "failed to get folder %s", uid)
	}
	return respBody, nil
}

// GetDatasources retrieves all datasources from Grafana
func (c *Client) GetDatasources(ctx context.Context) ([]Datasource, error) {
	respBody, err := c.DoRequest(ctx, http.MethodGet, "/api/datasources")
	if err != nil {
		return nil, wrapErr(err, "failed to get datasources")
	}

	var datasources []Datasource
	if err := json.Unmarshal(respBody, &datasources); err != nil {
		return nil, wrapErr(err, "failed to parse datasources")
	}

	return datasources, nil
}

// GetDatasourceByUID retrieves a datasource by its UID
func (c *Client) GetDatasourceByUID(
	ctx context.Context,
	uid string,
) ([]byte, error) {
	path := fmt.Sprintf("/api/datasources/uid/%s", uid)
	respBody, err := c.DoRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, wrapErrf(err, "failed to get datasource %s", uid)
	}
	return respBody, nil
}

// GetAlertRules retrieves all alert rules from Grafana
func (c *Client) GetAlertRules(ctx context.Context) ([]AlertRule, error) {
	respBody, err := c.DoRequest(
		ctx,
		http.MethodGet,
		"/api/v1/provisioning/alert-rules",
	)
	if err != nil {
		return nil, wrapErr(err, "failed to get alert rules")
	}

	var alertRules []AlertRule
	if err := json.Unmarshal(respBody, &alertRules); err != nil {
		return nil, wrapErr(err, "failed to parse alert rules")
	}

	return alertRules, nil
}

// GetAlertRuleByUID retrieves an alert rule by its UID
func (c *Client) GetAlertRuleByUID(
	ctx context.Context,
	uid string,
) ([]byte, error) {
	path := fmt.Sprintf("/api/v1/provisioning/alert-rules/%s", uid)
	respBody, err := c.DoRequest(ctx, http.MethodGet, path)
	if err != nil {
		return nil, wrapErrf(err, "failed to get alert rule %s", uid)
	}
	return respBody, nil
}

// GetContactPoints retrieves all contact points from Grafana
func (c *Client) GetContactPoints(ctx context.Context) ([]ContactPoint, error) {
	respBody, err := c.DoRequest(
		ctx,
		http.MethodGet,
		"/api/v1/provisioning/contact-points",
	)
	if err != nil {
		return nil, wrapErr(err, "failed to get contact points")
	}

	var contactPoints []ContactPoint
	if err := json.Unmarshal(respBody, &contactPoints); err != nil {
		return nil, wrapErr(err, "failed to parse contact points")
	}

	return contactPoints, nil
}

// GetNotificationPolicies retrieves the notification policies from Grafana
func (c *Client) GetNotificationPolicies(ctx context.Context) ([]byte, error) {
	respBody, err := c.DoRequest(
		ctx,
		http.MethodGet,
		"/api/v1/provisioning/policies",
	)
	if err != nil {
		return nil, wrapErr(err, "failed to get notification policies")
	}
	return respBody, nil
}

// GetMuteTimings retrieves all mute timings from Grafana
func (c *Client) GetMuteTimings(ctx context.Context) ([]MuteTiming, error) {
	respBody, err := c.DoRequest(
		ctx,
		http.MethodGet,
		"/api/v1/provisioning/mute-timings",
	)
	if err != nil {
		return nil, wrapErr(err, "failed to get mute timings")
	}

	var muteTimings []MuteTiming
	if err := json.Unmarshal(respBody, &muteTimings); err != nil {
		return nil, wrapErr(err, "failed to parse mute timings")
	}

	return muteTimings, nil
}

// GetNotificationTemplates retrieves all notification templates from Grafana
func (c *Client) GetNotificationTemplates(
	ctx context.Context,
) ([]NotificationTemplate, error) {
	respBody, err := c.DoRequest(
		ctx,
		http.MethodGet,
		"/api/v1/provisioning/templates",
	)
	if err != nil {
		return nil, wrapErr(err, "failed to get notification templates")
	}

	var templates []NotificationTemplate
	if err := json.Unmarshal(respBody, &templates); err != nil {
		return nil, wrapErr(err, "failed to parse notification templates")
	}

	return templates, nil
}

// HasMatchingTags checks if any of the dashboard tags match the include tags
func HasMatchingTags(dashboardTags []string, includeTags []string) bool {
	// If no include tags specified, include all dashboards
	if len(includeTags) == 0 {
		return true
	}

	// Check if any dashboard tag matches any include tag
	for _, dashTag := range dashboardTags {
		for _, includeTag := range includeTags {
			if dashTag == includeTag {
				return true
			}
		}
	}
	return false
}
