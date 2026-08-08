package mcpbroker

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

const oauthHTTPTimeout = 30 * time.Second

// OAuthHandlerConfig describes one operator-authorized OAuth client. The
// broker daemon sets RequireSession; interactive login leaves it false.
type OAuthHandlerConfig struct {
	Service        string
	RedirectURL    string
	Store          SessionStorage
	Fetcher        auth.AuthorizationCodeFetcher
	RequireSession bool
	HTTPClient     *http.Client
}

// NewOAuthHandler wires the official SDK to Torio's private, durable store.
// Construction never starts authorization. Only the SDK calling Fetcher during
// an interactive login can do that.
func NewOAuthHandler(ctx context.Context, cfg OAuthHandlerConfig) (*auth.AuthorizationCodeHandler, error) {
	if cfg.Store == nil {
		return nil, errors.New("OAuth session store is required")
	}
	if cfg.Fetcher == nil {
		return nil, errors.New("OAuth authorization-code fetcher is required")
	}
	if cfg.RedirectURL == "" {
		return nil, errors.New("OAuth redirect URL is required")
	}

	var initial oauth2.TokenSource
	savedConfig, savedToken, err := cfg.Store.Load(cfg.Service)
	switch {
	case err == nil:
		initial = NewSavingTokenSource(
			savedConfig.TokenSource(ctx, savedToken),
			savedConfig,
			savedToken,
			cfg.Store,
			cfg.Service,
		)
	case errors.Is(err, ErrOAuthSessionNotFound) && !cfg.RequireSession:
		// Interactive login is the sole path allowed to begin without state.
	case errors.Is(err, ErrOAuthSessionNotFound):
		return nil, ErrOAuthSessionNotFound
	default:
		return nil, errors.New("load OAuth session")
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: oauthHTTPTimeout}
	}
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		RedirectURL: cfg.RedirectURL,
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				RedirectURIs:            []string{cfg.RedirectURL},
				TokenEndpointAuthMethod: "none",
				GrantTypes:              []string{"authorization_code", "refresh_token"},
				ResponseTypes:           []string{"code"},
				ClientName:              "Torio MCP Broker",
				ApplicationType:         "native",
			},
		},
		AuthorizationCodeFetcher: cfg.Fetcher,
		RequestRefreshToken:      true,
		Client:                   httpClient,
		InitialTokenSource:       initial,
		NewTokenSource: func(tokenCtx context.Context, oauthConfig *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
			if err := cfg.Store.Save(cfg.Service, oauthConfig, token); err != nil {
				return nil, errors.New("persist new OAuth session")
			}
			return NewSavingTokenSource(
				oauthConfig.TokenSource(tokenCtx, token),
				oauthConfig,
				token,
				cfg.Store,
				cfg.Service,
			), nil
		},
	})
	if err != nil {
		return nil, errors.New("configure OAuth authorization handler")
	}
	return handler, nil
}
