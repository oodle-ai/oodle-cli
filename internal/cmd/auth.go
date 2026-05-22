package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/oauth2"

	"github.com/oodle-ai/oodle-cli/internal/config"
	"github.com/oodle-ai/oodle-cli/internal/output"
)

const (
	us1ClientID              = "HGPO3BrlV70EvFDSWyRjZF3airBmD01T"
	ap1ClientID              = "BtkEridc4BuBIhm8E3IKK0XEDYh82s43"
	us1DeploymentDomain      = "us1.oodle.ai"
	ap1DeploymentDomain      = "ap1.oodle.ai"
	us1OAuthDeploymentDomain = "prod-02-us-west-2.api.oodle.ai"
	ap1OAuthDeploymentDomain = "prod-01-ap-south1.api.oodle.ai"
)

type oauthProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	AuthorizationServer  string   `json:"authorization_server"`
	ScopesSupported      []string `json:"scopes_supported"`
}

type oauthOrgResponse struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Instances []oauthOrgInstance `json:"instances"`
}

type oauthOrgInstance struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (m oauthProtectedResourceMetadata) authServer() string {
	if len(m.AuthorizationServers) > 0 {
		return m.AuthorizationServers[0]
	}
	return m.AuthorizationServer
}

func newAuthCmd(flags *rootFlags) *cobra.Command {
	var loginDeployment string
	var getInstanceDeployment string

	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate oodle CLI with OAuth",
	}

	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Run OAuth login flow",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthLogin(cmd, flags, loginDeployment)
		},
	}
	loginCmd.Flags().StringVarP(&loginDeployment, "deployment", "d", "", "Deployment (us1, ap1, or full deployment URL/host)")

	getInstanceCmd := &cobra.Command{
		Use:   "get-instance",
		Short: "Fetch and store instance from OAuth session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthGetInstance(cmd, flags, getInstanceDeployment)
		},
	}
	getInstanceCmd.Flags().StringVarP(&getInstanceDeployment, "deployment", "d", "", "Deployment (us1, ap1, or full deployment URL/host)")

	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Clear saved OAuth credentials",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthLogout(cmd)
		},
	}
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show configured authentication status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthStatus(cmd, flags)
		},
	}

	cmd.AddCommand(loginCmd)
	cmd.AddCommand(getInstanceCmd)
	cmd.AddCommand(logoutCmd)
	cmd.AddCommand(statusCmd)
	return cmd
}

func runAuthLogin(cmd *cobra.Command, flags *rootFlags, deploymentFlag string) error {
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	in := cmd.InOrStdin()

	existing, _ := loadExistingConfig()

	deployment := firstNonEmpty(deploymentFlag, flags.apiURL)
	if deployment == "" {
		defaultDeployment := deploymentSlugDefaultValue(existing)
		line, err := promptLine(in, out, deploymentSlugLabel(defaultDeployment), defaultDeployment)
		if err != nil {
			return err
		}
		deployment = line
	}
	deployment = strings.TrimSpace(deployment)
	if deployment == "" {
		return fmt.Errorf("deployment is required")
	}

	deploymentDomain, err := normalizeDomain(deployment)
	if err != nil {
		return err
	}
	deploymentDomain = deploymentDomainForDomain(deploymentDomain)
	oauthDeploymentDomain := oauthDeploymentDomainForDomain(deploymentDomain)

	clientID, err := oauthClientIDForDomain(deploymentDomain)
	if err != nil {
		return err
	}
	oauthAPIURL := "https://" + oauthDeploymentDomain
	deploymentAPIURL := "https://" + deploymentDomain

	meta, err := fetchOAuthProtectedResourceMetadata(cmd.Context(), oauthAPIURL)
	if err != nil {
		return err
	}
	authServer := strings.TrimRight(meta.authServer(), "/")
	if authServer == "" {
		return fmt.Errorf("OAuth metadata at %s did not include an authorization server", oauthAPIURL)
	}
	resource := strings.TrimSpace(meta.Resource)
	if resource == "" {
		resource = strings.TrimRight(oauthAPIURL, "/")
	}

	state, err := randomToken(32)
	if err != nil {
		return fmt.Errorf("creating OAuth state: %w", err)
	}
	codeVerifier, err := randomToken(64)
	if err != nil {
		return fmt.Errorf("creating PKCE verifier: %w", err)
	}
	codeChallenge := pkceS256(codeVerifier)

	redirectURL, codeCh, errCh, shutdown, err := startOAuthCallbackServer(out, state)
	if err != nil {
		return fmt.Errorf("starting OAuth callback server: %w", err)
	}
	defer shutdown(context.Background())

	oauthCfg := &oauth2.Config{
		ClientID: clientID,
		Endpoint: oauth2.Endpoint{
			AuthURL:  authServer + "/authorize",
			TokenURL: authServer + "/oauth/token",
		},
		RedirectURL: redirectURL,
		Scopes:      resolveRequestedScopes(meta.ScopesSupported),
	}
	authURL := oauthCfg.AuthCodeURL(
		state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("audience", resource),
	)

	fmt.Fprintln(out, "Opening browser for OAuth login...")
	fmt.Fprintf(out, "If the browser does not open, visit:\n%s\n", authURL)
	_ = openBrowser(authURL)

	waitCtx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
	defer cancel()

	var authCode string
	select {
	case authCode = <-codeCh:
	case callbackErr := <-errCh:
		return callbackErr
	case <-waitCtx.Done():
		return fmt.Errorf("timed out waiting for OAuth login to complete")
	}

	token, err := oauthCfg.Exchange(
		waitCtx,
		authCode,
		oauth2.SetAuthURLParam("code_verifier", codeVerifier),
	)
	if err != nil {
		return fmt.Errorf("exchanging authorization code for token: %w", err)
	}
	if token.AccessToken == "" {
		return fmt.Errorf("OAuth token response did not include an access token")
	}

	apiKey := ""
	if existing != nil {
		apiKey = existing.APIKey
	}
	if apiKey != "" {
		removeAPIKey, err := promptDeleteAPIKey(in, out)
		if err != nil {
			return err
		}
		if removeAPIKey {
			apiKey = ""
		}
	}

	defaultInstance := ""
	if existing != nil {
		defaultInstance = strings.TrimSpace(existing.Instance)
	}
	instance := defaultInstance

	cfg := &config.Config{
		APIKey:            apiKey,
		OAuthAccessToken:  token.AccessToken,
		OAuthRefreshToken: token.RefreshToken,
		OAuthClientID:     clientID,
		OAuthAuthServer:   authServer,
		Instance:          instance,
		APIURL:            deploymentAPIURL,
		Deployment:        deployment,
	}
	if !token.Expiry.IsZero() {
		cfg.OAuthTokenExpiry = token.Expiry.UTC().Format(time.RFC3339)
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	path, _ := config.ConfigPath()
	fmt.Fprintf(out, "OAuth login successful. Configuration saved to %s\n", path)

	org, err := fetchOAuthOrg(cmd.Context(), deploymentAPIURL, token.AccessToken)
	if err != nil {
		fmt.Fprintf(errOut, "Warning: could not fetch instances now: %v\n", err)
		fmt.Fprintln(errOut, "Run 'oodle auth get-instance' to fetch and save the instance later.")
		return nil
	}
	resolvedInstance, err := resolveInstanceForLogin(in, out, org.Instances, defaultInstance)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resolvedInstance) == "" {
		return fmt.Errorf("instance is required to configure CLI usage after login")
	}
	if resolvedInstance != cfg.Instance {
		cfg.Instance = resolvedInstance
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Fprintf(out, "Saved instance %q to %s\n", resolvedInstance, path)
	}
	return nil
}

func promptDeleteAPIKey(in io.Reader, out io.Writer) (bool, error) {
	r := bufio.NewReader(in)
	fmt.Fprint(out, "An API key already exists in config. Do you want to delete it? [y/N]: ")
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func runAuthLogout(cmd *cobra.Command) error {
	out := cmd.OutOrStdout()

	existing, err := loadExistingConfig()
	if err != nil {
		return fmt.Errorf("loading existing config: %w", err)
	}
	if existing == nil {
		fmt.Fprintln(out, "No config file found; already logged out.")
		return nil
	}
	if existing.OAuthAccessToken == "" && existing.OAuthRefreshToken == "" {
		fmt.Fprintln(out, "No OAuth credentials found; already logged out.")
		return nil
	}

	existing.OAuthAccessToken = ""
	existing.OAuthRefreshToken = ""
	existing.OAuthTokenExpiry = ""
	existing.OAuthClientID = ""
	existing.OAuthAuthServer = ""
	if err := existing.Save(); err != nil {
		return err
	}

	path, _ := config.ConfigPath()
	fmt.Fprintf(out, "OAuth credentials removed from %s\n", path)
	return nil
}

func runAuthGetInstance(cmd *cobra.Command, flags *rootFlags, deploymentFlag string) error {
	out := cmd.OutOrStdout()
	in := cmd.InOrStdin()

	existing, err := loadExistingConfig()
	if err != nil {
		return fmt.Errorf("loading existing config: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("OAuth login is required. Run 'oodle auth login' first")
	}

	accessToken := strings.TrimSpace(existing.OAuthAccessToken)
	if accessToken == "" {
		return fmt.Errorf("OAuth login is required. Run 'oodle auth login' first")
	}

	deployment := firstNonEmpty(
		deploymentFlag,
		flags.apiURL,
		os.Getenv("OODLE_DEPLOYMENT"),
		os.Getenv("OODLE_API_URL"),
		os.Getenv("OODLE_URL"),
	)
	if deployment == "" {
		defaultDeployment := deploymentSlugDefaultValue(existing)
		line, err := promptLine(in, out, deploymentSlugLabel(defaultDeployment), defaultDeployment)
		if err != nil {
			return err
		}
		deployment = line
	}
	deployment = strings.TrimSpace(deployment)
	if deployment == "" {
		return fmt.Errorf("deployment is required")
	}

	deploymentDomain, err := normalizeDomain(deployment)
	if err != nil {
		return err
	}
	deploymentDomain = deploymentDomainForDomain(deploymentDomain)
	deploymentAPIURL := "https://" + deploymentDomain

	org, err := fetchOAuthOrg(cmd.Context(), deploymentAPIURL, accessToken)
	if err != nil {
		return fmt.Errorf("fetching organization details from %s/api/org: %w", deploymentAPIURL, err)
	}

	defaultInstance := strings.TrimSpace(firstNonEmpty(os.Getenv("OODLE_INSTANCE")))
	if defaultInstance == "" {
		defaultInstance = strings.TrimSpace(existing.Instance)
	}

	selectedInstance, err := resolveInstanceForLogin(in, out, org.Instances, defaultInstance)
	if err != nil {
		return err
	}

	cfg := &config.Config{}
	*cfg = *existing
	cfg.Instance = selectedInstance
	cfg.APIURL = deploymentAPIURL
	cfg.Deployment = deployment
	if cfg.OAuthAccessToken == "" {
		cfg.OAuthAccessToken = accessToken
	}

	if err := cfg.Save(); err != nil {
		return err
	}
	path, _ := config.ConfigPath()
	fmt.Fprintf(out, "Saved instance %q to %s\n", selectedInstance, path)
	return nil
}

type authStatusRow struct {
	PreferredAuth    string `json:"preferred_auth"`
	OAuthConfigured  bool   `json:"oauth_configured"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	RefreshEnabled   bool   `json:"refresh_enabled"`
	OAuthExpired     bool   `json:"oauth_expired"`
	OAuthExpiresAt   string `json:"oauth_expires_at,omitempty"`
	Instance         string `json:"instance,omitempty"`
	APIURL           string `json:"api_url,omitempty"`
}

func runAuthStatus(cmd *cobra.Command, flags *rootFlags) error {
	existing, err := loadExistingConfig()
	if err != nil {
		return fmt.Errorf("loading existing config: %w", err)
	}
	row := computeAuthStatus(existing, flags)
	cols := []output.Column{
		{Header: "AUTH", Field: "PreferredAuth"},
		{Header: "OAUTH", Field: "OAuthConfigured"},
		{Header: "API_KEY", Field: "APIKeyConfigured"},
		{Header: "REFRESH", Field: "RefreshEnabled"},
		{Header: "EXPIRED", Field: "OAuthExpired"},
		{Header: "EXPIRES_AT", Field: "OAuthExpiresAt"},
		{Header: "INSTANCE", Field: "Instance"},
		{Header: "API_URL", Field: "APIURL"},
	}
	return output.Print(cmd.OutOrStdout(), getOutputFormat(cmd), []authStatusRow{row}, cols)
}

func computeAuthStatus(existing *config.Config, flags *rootFlags) authStatusRow {
	apiKey := firstNonEmpty(flags.apiKey, os.Getenv("OODLE_API_KEY"))
	oauthAccess := os.Getenv("OODLE_OAUTH_ACCESS_TOKEN")
	oauthRefresh := os.Getenv("OODLE_OAUTH_REFRESH_TOKEN")
	instance := firstNonEmpty(flags.instance, os.Getenv("OODLE_INSTANCE"))
	apiURL := firstNonEmpty(flags.apiURL, os.Getenv("OODLE_DEPLOYMENT"), os.Getenv("OODLE_API_URL"), os.Getenv("OODLE_URL"))

	var expiry time.Time
	var hasExpiry bool
	oauthClientID := ""
	oauthAuthServer := ""
	if existing != nil {
		if apiKey == "" {
			apiKey = existing.APIKey
		}
		if oauthAccess == "" {
			oauthAccess = existing.OAuthAccessToken
		}
		if oauthRefresh == "" {
			oauthRefresh = existing.OAuthRefreshToken
		}
		if instance == "" {
			instance = existing.Instance
		}
		if apiURL == "" {
			apiURL = existing.APIURL
		}
		expiry, hasExpiry = existing.OAuthExpiryTime()
		oauthClientID = existing.OAuthClientID
		oauthAuthServer = existing.OAuthAuthServer
	}

	preferred := "none"
	if oauthAccess != "" {
		preferred = "oauth"
	} else if apiKey != "" {
		preferred = "api-key"
	}

	refreshEnabled := oauthAccess != "" && oauthRefresh != "" && oauthClientID != "" && oauthAuthServer != ""
	expiresAt := ""
	expired := false
	if oauthAccess != "" && hasExpiry {
		expiresAt = expiry.UTC().Format(time.RFC3339)
		expired = time.Now().After(expiry)
	}

	return authStatusRow{
		PreferredAuth:    preferred,
		OAuthConfigured:  oauthAccess != "",
		APIKeyConfigured: apiKey != "",
		RefreshEnabled:   refreshEnabled,
		OAuthExpired:     expired,
		OAuthExpiresAt:   expiresAt,
		Instance:         instance,
		APIURL:           apiURL,
	}
}

func oauthClientIDForDomain(domain string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(domain)) {
	case us1DeploymentDomain, us1OAuthDeploymentDomain:
		return us1ClientID, nil
	case ap1DeploymentDomain, ap1OAuthDeploymentDomain:
		return ap1ClientID, nil
	default:
		return "", fmt.Errorf("oauth not supported for deployment %q", domain)
	}
}

func oauthDeploymentDomainForDomain(domain string) string {
	switch strings.ToLower(strings.TrimSpace(domain)) {
	case us1DeploymentDomain:
		return us1OAuthDeploymentDomain
	case ap1DeploymentDomain:
		return ap1OAuthDeploymentDomain
	default:
		return strings.ToLower(strings.TrimSpace(domain))
	}
}

func deploymentDomainForDomain(domain string) string {
	switch strings.ToLower(strings.TrimSpace(domain)) {
	case us1OAuthDeploymentDomain:
		return us1DeploymentDomain
	case ap1OAuthDeploymentDomain:
		return ap1DeploymentDomain
	default:
		return strings.ToLower(strings.TrimSpace(domain))
	}
}

func deploymentSlugDefaultValue(existing *config.Config) string {
	if existing == nil {
		return ""
	}
	if strings.TrimSpace(existing.Deployment) != "" {
		return strings.TrimSpace(existing.Deployment)
	}
	if strings.TrimSpace(existing.APIURL) == "" {
		return ""
	}
	normalized, err := normalizeDomain(existing.APIURL)
	if err != nil {
		return strings.TrimSpace(existing.APIURL)
	}
	return deploymentSlugFromDomain(deploymentDomainForDomain(normalized))
}

func deploymentSlugFromDomain(domain string) string {
	switch strings.ToLower(strings.TrimSpace(domain)) {
	case us1DeploymentDomain:
		return "us1"
	case ap1DeploymentDomain:
		return "ap1"
	default:
		return strings.TrimSpace(domain)
	}
}

func deploymentSlugLabel(defaultValue string) string {
	if strings.TrimSpace(defaultValue) == "" {
		return "Deployment (us1, ap1)"
	}
	return "Deployment (us1, ap1; press Enter to use default)"
}

func normalizeDomain(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", fmt.Errorf("deployment is required")
	}
	var host string
	if strings.Contains(trimmed, "://") {
		u, err := url.Parse(trimmed)
		if err != nil {
			return "", fmt.Errorf("invalid deployment %q: %w", input, err)
		}
		if u.Host == "" {
			return "", fmt.Errorf("invalid deployment %q", input)
		}
		host = u.Host
	} else {
		host = strings.TrimRight(trimmed, "/")
	}
	normalized := strings.ToLower(host)
	switch normalized {
	case "us1":
		return us1DeploymentDomain, nil
	case "ap1":
		return ap1DeploymentDomain, nil
	default:
		return normalized, nil
	}
}

func fetchOAuthProtectedResourceMetadata(ctx context.Context, apiURL string) (*oauthProtectedResourceMetadata, error) {
	wellKnownURLs := []string{
		strings.TrimRight(apiURL, "/") + "/.well-known/oauth-protected-resource",
		strings.TrimRight(apiURL, "/") + "/v1/api/.well-known/oauth-protected-resource",
	}
	var lastErr error
	for _, wellKnownURL := range wellKnownURLs {
		meta, err := fetchOAuthProtectedResourceMetadataFromURL(ctx, wellKnownURL)
		if err == nil {
			return meta, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no OAuth metadata URLs attempted")
	}
	return nil, lastErr
}

func fetchOAuthProtectedResourceMetadataFromURL(ctx context.Context, wellKnownURL string) (*oauthProtectedResourceMetadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, wellKnownURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", wellKnownURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return nil, fmt.Errorf("failed to fetch OAuth metadata from %s: status %d: %s", wellKnownURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var meta oauthProtectedResourceMetadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, fmt.Errorf("decoding OAuth metadata from %s: %w", wellKnownURL, err)
	}
	return &meta, nil
}

func fetchOAuthOrg(ctx context.Context, apiURL, accessToken string) (*oauthOrgResponse, error) {
	orgURL := strings.TrimRight(apiURL, "/") + "/v1/api/org"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, orgURL, nil)
	if err != nil {
		return nil, err
	}
	req.AddCookie(&http.Cookie{Name: "__oodle_session", Value: accessToken})

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting %s: %w", orgURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8*1024))
		return nil, fmt.Errorf("failed to fetch organization from %s: status %d: %s", orgURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var org oauthOrgResponse
	if err := json.NewDecoder(resp.Body).Decode(&org); err != nil {
		return nil, fmt.Errorf("decoding organization from %s: %w", orgURL, err)
	}
	return &org, nil
}

func resolveInstanceForLogin(in io.Reader, out io.Writer, instances []oauthOrgInstance, defaultInstance string) (string, error) {
	if len(instances) == 0 {
		return "", fmt.Errorf("organization response did not include any instances")
	}
	if len(instances) == 1 {
		selected := strings.TrimSpace(instances[0].ID)
		if selected == "" {
			return "", fmt.Errorf("organization response returned one instance without an id")
		}
		return selected, nil
	}

	fmt.Fprintln(out, "Available instances:")
	for _, instance := range instances {
		fmt.Fprintf(out, "- %s (%s) status=%s\n", instance.ID, instance.Name, instance.Status)
	}

	defaultValue := ""
	if instanceExists(instances, defaultInstance) {
		defaultValue = defaultInstance
	}
	line, err := promptLine(in, out, "Instance ID", defaultValue)
	if err != nil {
		return "", err
	}
	selected := strings.TrimSpace(line)
	if selected == "" {
		return "", fmt.Errorf("instance is required")
	}
	if !instanceExists(instances, selected) {
		return "", fmt.Errorf("instance %q not found in organization instances", selected)
	}
	return selected, nil
}

func instanceExists(instances []oauthOrgInstance, id string) bool {
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" {
		return false
	}
	for _, instance := range instances {
		if strings.TrimSpace(instance.ID) == trimmedID {
			return true
		}
	}
	return false
}

func resolveRequestedScopes(supported []string) []string {
	defaults := []string{"openid", "profile", "email", "offline_access"}
	if len(supported) == 0 {
		return defaults
	}
	set := make(map[string]bool, len(supported))
	for _, s := range supported {
		set[s] = true
	}
	out := make([]string, 0, len(defaults))
	for _, s := range defaults {
		if set[s] {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []string{"openid"}
	}
	return out
}

func promptLine(in io.Reader, out io.Writer, label, defaultValue string) (string, error) {
	r := bufio.NewReader(in)
	if defaultValue == "" {
		fmt.Fprintf(out, "%s: ", label)
	} else {
		fmt.Fprintf(out, "%s [%s]: ", label, defaultValue)
	}
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

func startOAuthCallbackServer(out io.Writer, expectedState string) (redirectURL string, codeCh <-chan string, errCh <-chan error, shutdown func(context.Context) error, err error) {
	listener, err := listenOnAllowedOAuthCallbackPort()
	if err != nil {
		return "", nil, nil, nil, err
	}

	codeC := make(chan string, 1)
	errC := make(chan error, 1)
	mux := http.NewServeMux()
	server := &http.Server{Handler: mux}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		state := r.URL.Query().Get("state")
		if state != expectedState {
			http.Error(w, "invalid OAuth state", http.StatusBadRequest)
			select {
			case errC <- fmt.Errorf("invalid OAuth state in callback"):
			default:
			}
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing OAuth code", http.StatusBadRequest)
			select {
			case errC <- fmt.Errorf("missing OAuth code in callback"):
			default:
			}
			return
		}
		fmt.Fprintln(w, "Login complete. You can close this window and return to your terminal.")
		select {
		case codeC <- code:
		default:
		}
	})

	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			select {
			case errC <- serveErr:
			default:
			}
		}
	}()

	redirect := "http://" + listener.Addr().String() + "/callback"
	fmt.Fprintf(out, "Waiting for OAuth callback on %s\n", redirect)

	return redirect, codeC, errC, server.Shutdown, nil
}

func listenOnAllowedOAuthCallbackPort() (net.Listener, error) {
	var lastErr error
	for port := 9400; port <= 9410; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no callback ports available")
	}
	return nil, fmt.Errorf("unable to bind OAuth callback listener on 127.0.0.1:9400-9410: %w", lastErr)
}

func randomToken(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func openBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
