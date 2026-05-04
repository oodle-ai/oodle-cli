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

const appDevClientID = "HGPO3BrlV70EvFDSWyRjZF3airBmD01T"

type oauthProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	AuthorizationServer  string   `json:"authorization_server"`
	ScopesSupported      []string `json:"scopes_supported"`
}

func (m oauthProtectedResourceMetadata) authServer() string {
	if len(m.AuthorizationServers) > 0 {
		return m.AuthorizationServers[0]
	}
	return m.AuthorizationServer
}

func newAuthCmd(flags *rootFlags) *cobra.Command {
	var domain string
	var instance string

	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authenticate oodle CLI with OAuth",
	}

	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Run OAuth login flow",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthLogin(cmd, flags, domain, instance)
		},
	}
	loginCmd.Flags().StringVar(&domain, "domain", "", "Deployment domain (for example: app-dev.oodle.ai)")
	loginCmd.Flags().StringVar(&instance, "instance", "", "Oodle instance ID to store with OAuth credentials")

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
	cmd.AddCommand(logoutCmd)
	cmd.AddCommand(statusCmd)
	return cmd
}

func runAuthLogin(cmd *cobra.Command, flags *rootFlags, domainFlag, instanceFlag string) error {
	out := cmd.OutOrStdout()
	in := cmd.InOrStdin()

	existing, _ := loadExistingConfig()

	domain := firstNonEmpty(domainFlag, flags.apiURL)
	if domain == "" {
		line, err := promptLine(in, out, "Deployment domain", "app-dev.oodle.ai")
		if err != nil {
			return err
		}
		domain = line
	}
	host, err := normalizeDomain(domain)
	if err != nil {
		return err
	}

	clientID, err := oauthClientIDForDomain(host)
	if err != nil {
		return err
	}
	apiURL := "https://" + host

	instance := instanceFlag
	if instance == "" && existing != nil {
		instance = existing.Instance
	}
	if instance == "" {
		line, err := promptLine(in, out, "Instance ID", "")
		if err != nil {
			return err
		}
		instance = line
	}
	if strings.TrimSpace(instance) == "" {
		return fmt.Errorf("instance is required to configure CLI usage after login")
	}

	meta, err := fetchOAuthProtectedResourceMetadata(cmd.Context(), apiURL)
	if err != nil {
		return err
	}
	authServer := strings.TrimRight(meta.authServer(), "/")
	if authServer == "" {
		return fmt.Errorf("OAuth metadata at %s did not include an authorization server", apiURL)
	}
	resource := strings.TrimSpace(meta.Resource)
	if resource == "" {
		resource = strings.TrimRight(apiURL, "/") + "/v1/api"
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

	cfg := &config.Config{
		APIKey:            apiKey,
		OAuthAccessToken:  token.AccessToken,
		OAuthRefreshToken: token.RefreshToken,
		OAuthClientID:     clientID,
		OAuthAuthServer:   authServer,
		Instance:          instance,
		APIURL:            apiURL,
	}
	if !token.Expiry.IsZero() {
		cfg.OAuthTokenExpiry = token.Expiry.UTC().Format(time.RFC3339)
	}
	if err := cfg.Save(); err != nil {
		return err
	}

	path, _ := config.ConfigPath()
	fmt.Fprintf(out, "OAuth login successful. Configuration saved to %s\n", path)
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

type authStatusRow struct {
	PreferredAuth   string `json:"preferred_auth"`
	OAuthConfigured bool   `json:"oauth_configured"`
	APIKeyConfigured bool  `json:"api_key_configured"`
	RefreshEnabled  bool   `json:"refresh_enabled"`
	OAuthExpired    bool   `json:"oauth_expired"`
	OAuthExpiresAt  string `json:"oauth_expires_at,omitempty"`
	Instance        string `json:"instance,omitempty"`
	APIURL          string `json:"api_url,omitempty"`
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
	case "app-dev.oodle.ai":
		return appDevClientID, nil
	default:
		return "", fmt.Errorf("oauth not supported for deployment domain %q", domain)
	}
}

func normalizeDomain(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", fmt.Errorf("deployment domain is required")
	}
	if strings.Contains(trimmed, "://") {
		u, err := url.Parse(trimmed)
		if err != nil {
			return "", fmt.Errorf("invalid deployment domain %q: %w", input, err)
		}
		if u.Host == "" {
			return "", fmt.Errorf("invalid deployment domain %q", input)
		}
		return strings.ToLower(u.Host), nil
	}
	return strings.ToLower(strings.TrimRight(trimmed, "/")), nil
}

func fetchOAuthProtectedResourceMetadata(ctx context.Context, apiURL string) (*oauthProtectedResourceMetadata, error) {
	wellKnownURL := strings.TrimRight(apiURL, "/") + "/v1/api/.well-known/oauth-protected-resource"
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
