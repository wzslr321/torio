package mcpbroker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/oauth2"
)

func TestSessionStoreRoundTripsPrivateOAuthState(t *testing.T) {
	base := filepath.Join(t.TempDir(), "oauth")
	store := NewSessionStore(base)
	cfg := &oauth2.Config{
		ClientID:     "dynamic-client",
		ClientSecret: "client-secret-value",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://auth.example.test/authorize",
			TokenURL: "https://auth.example.test/token",
		},
		RedirectURL: "http://localhost:43119/callback",
		Scopes:      []string{"tickets.read", "tickets.write"},
	}
	token := &oauth2.Token{
		AccessToken:  "access-secret-value",
		TokenType:    "Bearer",
		RefreshToken: "refresh-secret-value",
		Expiry:       time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC),
	}

	if err := store.Save("tickets", cfg, token); err != nil {
		t.Fatalf("Save: %v", err)
	}
	gotCfg, gotToken, err := store.Load("tickets")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if gotCfg.ClientID != cfg.ClientID || gotCfg.ClientSecret != cfg.ClientSecret ||
		gotCfg.Endpoint != cfg.Endpoint || gotCfg.RedirectURL != cfg.RedirectURL ||
		strings.Join(gotCfg.Scopes, " ") != strings.Join(cfg.Scopes, " ") {
		t.Fatalf("loaded config = %#v, want %#v", gotCfg, cfg)
	}
	if gotToken.AccessToken != token.AccessToken || gotToken.RefreshToken != token.RefreshToken ||
		gotToken.TokenType != token.TokenType || !gotToken.Expiry.Equal(token.Expiry) {
		t.Fatalf("loaded token = %#v, want equivalent token", gotToken)
	}

	dirInfo, err := os.Stat(base)
	if err != nil {
		t.Fatalf("stat store dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("store dir mode = %o, want 700", got)
	}
	fileInfo, err := os.Stat(filepath.Join(base, "tickets.json"))
	if err != nil {
		t.Fatalf("stat session: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("session mode = %o, want 600", got)
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("read store dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "tickets.json" {
		t.Fatalf("store entries = %v, want only final session file", entries)
	}
}

func TestSessionStoreRefusesLooseOrLinkedState(t *testing.T) {
	t.Run("loose mode", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "oauth")
		store := NewSessionStore(base)
		if err := store.Save("tickets", testOAuthConfig(), testOAuthToken("one")); err != nil {
			t.Fatalf("Save: %v", err)
		}
		path := filepath.Join(base, "tickets.json")
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		if _, _, err := store.Load("tickets"); err == nil || !strings.Contains(err.Error(), "0600") {
			t.Fatalf("Load error = %v, want private-mode refusal", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		base := filepath.Join(root, "oauth")
		if err := os.Mkdir(base, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		target := filepath.Join(root, "outside.json")
		if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
			t.Fatalf("write target: %v", err)
		}
		if err := os.Symlink(target, filepath.Join(base, "tickets.json")); err != nil {
			t.Fatalf("symlink: %v", err)
		}
		if _, _, err := NewSessionStore(base).Load("tickets"); err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("Load error = %v, want symlink refusal", err)
		}
	})
}

type rotatingTokenSource struct {
	token *oauth2.Token
	err   error
}

func (s rotatingTokenSource) Token() (*oauth2.Token, error) { return s.token, s.err }

func TestSavingTokenSourcePersistsRefreshBeforeReturningIt(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "oauth"))
	cfg := testOAuthConfig()
	initial := testOAuthToken("old")
	if err := store.Save("tickets", cfg, initial); err != nil {
		t.Fatalf("initial Save: %v", err)
	}
	refreshed := testOAuthToken("new")
	source := NewSavingTokenSource(rotatingTokenSource{token: refreshed}, cfg, initial, store, "tickets")

	got, err := source.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got.AccessToken != "access-new" {
		t.Fatalf("access token = %q, want refreshed token", got.AccessToken)
	}
	_, persisted, err := store.Load("tickets")
	if err != nil {
		t.Fatalf("Load refreshed session: %v", err)
	}
	if persisted.AccessToken != got.AccessToken || persisted.RefreshToken != got.RefreshToken {
		t.Fatalf("persisted token = %#v, returned %#v", persisted, got)
	}
}

func TestSavingTokenSourceFailsClosedWhenRefreshCannotPersist(t *testing.T) {
	cfg := testOAuthConfig()
	initial := testOAuthToken("old")
	refreshed := testOAuthToken("new")
	source := NewSavingTokenSource(rotatingTokenSource{token: refreshed}, cfg, initial, failingSessionStore{err: errors.New("disk full")}, "tickets")

	if token, err := source.Token(); err == nil || token != nil || !strings.Contains(err.Error(), "persist") {
		t.Fatalf("Token = %#v, err = %v, want persistence failure", token, err)
	}
}

func TestOAuthHandlerRestoresSessionWithoutStartingAuthorization(t *testing.T) {
	store := NewSessionStore(filepath.Join(t.TempDir(), "oauth"))
	cfg := testOAuthConfig()
	token := testOAuthToken("saved")
	if err := store.Save("tickets", cfg, token); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fetches := 0
	handler, err := NewOAuthHandler(context.Background(), OAuthHandlerConfig{
		Service:        "tickets",
		RedirectURL:    cfg.RedirectURL,
		Store:          store,
		RequireSession: true,
		Fetcher: func(context.Context, *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			fetches++
			return nil, errors.New("must not authorize")
		},
	})
	if err != nil {
		t.Fatalf("NewOAuthHandler: %v", err)
	}
	source, err := handler.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	got, err := source.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got.AccessToken != token.AccessToken || fetches != 0 {
		t.Fatalf("restored token = %#v, authorization fetches = %d", got, fetches)
	}
}

func TestOAuthHandlerRequiresLoginForDaemon(t *testing.T) {
	_, err := NewOAuthHandler(context.Background(), OAuthHandlerConfig{
		Service:        "tickets",
		RedirectURL:    "http://localhost:43119/callback",
		Store:          NewSessionStore(filepath.Join(t.TempDir(), "oauth")),
		RequireSession: true,
		Fetcher: func(context.Context, *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			return nil, errors.New("noninteractive")
		},
	})
	if !errors.Is(err, ErrOAuthSessionNotFound) {
		t.Fatalf("NewOAuthHandler error = %v, want ErrOAuthSessionNotFound", err)
	}
}

func TestOAuthHandlerInteractiveModeConfiguresDynamicRegistration(t *testing.T) {
	var authorizationURL string
	handler, err := NewOAuthHandler(context.Background(), OAuthHandlerConfig{
		Service:     "tickets",
		RedirectURL: "http://localhost:43119/callback",
		Store:       NewSessionStore(filepath.Join(t.TempDir(), "oauth")),
		Fetcher: func(_ context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			authorizationURL = args.URL
			parsed, parseErr := url.Parse(args.URL)
			if parseErr != nil {
				return nil, parseErr
			}
			return &auth.AuthorizationResult{Code: "code", State: parsed.Query().Get("state")}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewOAuthHandler: %v", err)
	}
	if handler == nil || authorizationURL != "" {
		t.Fatalf("handler = %v, authorization started during construction: %q", handler, authorizationURL)
	}
}

func TestOAuthHandlerCompletesDiscoveryDCRPKCEAndPersistsSession(t *testing.T) {
	var server *httptest.Server
	var sawDCR, sawPKCE, sawVerifier bool
	mux := http.NewServeMux()
	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)
	resourceURL := server.URL + "/mcp"
	redirectURL := "http://localhost:43119/callback"

	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"resource":%q,"authorization_servers":[%q]}`, resourceURL, server.URL)
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"registration_endpoint":%q,"response_types_supported":["code"],"grant_types_supported":["authorization_code","refresh_token"],"token_endpoint_auth_methods_supported":["none"],"code_challenge_methods_supported":["S256"],"authorization_response_iss_parameter_supported":true}`,
			server.URL, server.URL+"/authorize", server.URL+"/token", server.URL+"/register")
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		sawDCR = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"client_id":"dynamic-client","redirect_uris":[%q],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`, redirectURL)
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		sawPKCE = query.Get("code_challenge") != "" && query.Get("code_challenge_method") == "S256"
		location := query.Get("redirect_uri") + "?code=authorization-code&state=" + url.QueryEscape(query.Get("state")) + "&iss=" + url.QueryEscape(server.URL)
		http.Redirect(w, r, location, http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "form", http.StatusBadRequest)
			return
		}
		sawVerifier = r.Form.Get("code_verifier") != "" && r.Form.Get("code") == "authorization-code"
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"access-token","refresh_token":"refresh-token","token_type":"Bearer","expires_in":3600}`)
	})

	store := NewSessionStore(filepath.Join(t.TempDir(), "oauth"))
	handler, err := NewOAuthHandler(context.Background(), OAuthHandlerConfig{
		Service:     "tickets",
		RedirectURL: redirectURL,
		Store:       store,
		HTTPClient:  server.Client(),
		Fetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			client := *server.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			response, err := client.Get(args.URL)
			if err != nil {
				return nil, err
			}
			defer response.Body.Close()
			location, err := response.Location()
			if err != nil {
				return nil, err
			}
			return &auth.AuthorizationResult{
				Code:  location.Query().Get("code"),
				State: location.Query().Get("state"),
				Iss:   location.Query().Get("iss"),
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewOAuthHandler: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, resourceURL, nil)
	response := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    request,
	}
	response.Header.Set("WWW-Authenticate", `Bearer resource_metadata="`+server.URL+`/.well-known/oauth-protected-resource"`)
	if err := handler.Authorize(context.Background(), request, response); err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	source, err := handler.TokenSource(context.Background())
	if err != nil {
		t.Fatalf("TokenSource: %v", err)
	}
	token, err := source.Token()
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if token.AccessToken != "access-token" || !sawDCR || !sawPKCE || !sawVerifier {
		t.Fatalf("OAuth result token=%q DCR=%t PKCE=%t verifier=%t", token.AccessToken, sawDCR, sawPKCE, sawVerifier)
	}
	config, persisted, err := store.Load("tickets")
	if err != nil {
		t.Fatalf("Load persisted OAuth: %v", err)
	}
	if config.ClientID != "dynamic-client" || persisted.RefreshToken != "refresh-token" {
		t.Fatalf("persisted config/token = %#v / %#v", config, persisted)
	}
}

type failingSessionStore struct{ err error }

func (s failingSessionStore) Save(string, *oauth2.Config, *oauth2.Token) error { return s.err }
func (s failingSessionStore) Load(string) (*oauth2.Config, *oauth2.Token, error) {
	return nil, nil, s.err
}

func testOAuthConfig() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "client",
		ClientSecret: "secret",
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://auth.example.test/authorize",
			TokenURL: "https://auth.example.test/token",
		},
		RedirectURL: "http://localhost:43119/callback",
	}
}

func testOAuthToken(suffix string) *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  "access-" + suffix,
		TokenType:    "Bearer",
		RefreshToken: "refresh-" + suffix,
		Expiry:       time.Now().UTC().Add(time.Hour),
	}
}
