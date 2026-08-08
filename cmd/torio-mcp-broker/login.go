package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/mcpbroker"
)

type codeReceiver struct {
	output io.Writer
	result chan *auth.AuthorizationResult
}

func newCodeReceiver(output io.Writer) *codeReceiver {
	return &codeReceiver{output: output, result: make(chan *auth.AuthorizationResult, 1)}
}

func (r *codeReceiver) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		code := request.URL.Query().Get("code")
		state := request.URL.Query().Get("state")
		if code == "" || state == "" {
			http.Error(writer, "incomplete OAuth callback", http.StatusBadRequest)
			return
		}
		result := &auth.AuthorizationResult{Code: code, State: state, Iss: request.URL.Query().Get("iss")}
		select {
		case r.result <- result:
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(writer, "Torio MCP authorization complete. You can close this window.\n")
		default:
			http.Error(writer, "OAuth callback already received", http.StatusConflict)
		}
	})
	return mux
}

func (r *codeReceiver) Fetch(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
	if args == nil || args.URL == "" {
		return nil, errors.New("authorization server returned no operator URL")
	}
	if _, err := fmt.Fprintf(r.output, "Open this URL in your browser to authorize MCP:\n%s\n", args.URL); err != nil {
		return nil, errors.New("show OAuth authorization URL")
	}
	select {
	case result := <-r.result:
		return result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type loginConfig struct {
	policyDir string
	storeDir  string
	service   string
	output    io.Writer
}

func runLogin(ctx context.Context, cfg loginConfig) error {
	policy, err := loadPolicyDir(cfg.policyDir)
	if err != nil {
		return err
	}
	grant, ok := findServiceGrant(policy, cfg.service)
	if !ok {
		return fmt.Errorf("policy grants no service %q", cfg.service)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", oauthCallbackPort))
	if err != nil {
		return errors.New("listen for OAuth callback")
	}
	receiver := newCodeReceiver(cfg.output)
	server := &http.Server{
		Handler:           receiver.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       15 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		<-serveErr
	}()

	handler, err := mcpbroker.NewOAuthHandler(ctx, mcpbroker.OAuthHandlerConfig{
		Service:     grant.Name,
		RedirectURL: oauthCallbackURL,
		Store:       mcpbroker.NewSessionStore(cfg.storeDir),
		Fetcher:     receiver.Fetch,
	})
	if err != nil {
		return err
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "torio-mcp-login", Version: "1"},
		&mcp.ClientOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
	)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:     grant.UpstreamEndpoint,
		HTTPClient:   &http.Client{Timeout: upstreamTimeout},
		OAuthHandler: handler,
	}, nil)
	if err != nil {
		return errors.New("OAuth login did not establish an MCP session")
	}
	defer session.Close()
	checkCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if _, err := session.ListTools(checkCtx, nil); err != nil {
		return errors.New("authorized MCP session could not enumerate tools")
	}
	_, err = fmt.Fprintf(cfg.output, "MCP login complete for %s.\n", grant.Name)
	return err
}

func findServiceGrant(policy lima.Set, service string) (lima.ServiceGrant, bool) {
	for _, grant := range policy.Grants().Services {
		if grant.Name == service {
			return grant, true
		}
	}
	return lima.ServiceGrant{}, false
}
