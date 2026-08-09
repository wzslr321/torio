package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wzslr321/torio/internal/lima"
	"github.com/wzslr321/torio/internal/mcpbroker"
)

const (
	oauthCallbackPort = 43119
	oauthCallbackURL  = "http://localhost:43119/callback"
	connectTimeout    = 30 * time.Second
	upstreamTimeout   = 65 * time.Second
)

type daemonConfig struct {
	policyDir string
	socketDir string
	storeDir  string
	auditPath string
}

type serviceRuntime struct {
	grant    lima.ServiceGrant
	upstream *mcp.ClientSession
	listener *net.UnixListener
}

func runDaemon(ctx context.Context, cfg daemonConfig) error {
	policy, err := loadPolicyDir(cfg.policyDir)
	if err != nil {
		return err
	}
	grants := policy.Grants().Services
	if len(grants) == 0 {
		return errors.New("MCP policy grants no services")
	}
	auditFile, err := openAuditFile(cfg.auditPath)
	if err != nil {
		return err
	}
	defer auditFile.Close()
	recorder := mcpbroker.NewJSONRecorder(auditFile)

	runtimes := make([]*serviceRuntime, 0, len(grants))
	defer func() {
		for _, runtime := range runtimes {
			if runtime.listener != nil {
				_ = runtime.listener.Close()
			}
			if runtime.upstream != nil {
				_ = runtime.upstream.Close()
			}
		}
	}()

	store := mcpbroker.NewSessionStore(cfg.storeDir)
	for _, grant := range grants {
		upstream, err := connectUpstream(ctx, grant, store)
		if err != nil {
			return err
		}
		runtime := &serviceRuntime{grant: grant, upstream: upstream}
		runtimes = append(runtimes, runtime)

		validationCtx, cancel := context.WithTimeout(ctx, connectTimeout)
		_, err = mcpbroker.NewServiceServer(validationCtx, mcpbroker.ServiceConfig{
			Service:  grant.Name,
			Policy:   policy,
			Upstream: upstream,
			Audit:    recorder,
		})
		cancel()
		if err != nil {
			return fmt.Errorf("service %s failed policy/upstream validation: %w", grant.Name, err)
		}
	}

	if err := validateRuntimeDir(cfg.socketDir); err != nil {
		return err
	}
	for _, runtime := range runtimes {
		listener, err := listenService(cfg.socketDir, runtime.grant.Name)
		if err != nil {
			return err
		}
		runtime.listener = listener
	}
	if err := writeRuntimeFile(cfg.socketDir, ".policy.sha256", []byte(policy.Digest()+"\n"), 0o600); err != nil {
		return err
	}
	if err := notifyReady(); err != nil {
		return err
	}

	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errs := make(chan error, len(runtimes))
	var wg sync.WaitGroup
	for _, runtime := range runtimes {
		wg.Add(1)
		go func(runtime *serviceRuntime) {
			defer wg.Done()
			errs <- serveService(serveCtx, policy, recorder, runtime)
		}(runtime)
	}
	select {
	case <-ctx.Done():
		for _, runtime := range runtimes {
			_ = runtime.listener.Close()
		}
		wg.Wait()
		return nil
	case err := <-errs:
		cancel()
		for _, runtime := range runtimes {
			_ = runtime.listener.Close()
		}
		wg.Wait()
		return err
	}
}

func connectUpstream(ctx context.Context, grant lima.ServiceGrant, store mcpbroker.SessionStorage) (*mcp.ClientSession, error) {
	handler, err := mcpbroker.NewOAuthHandler(ctx, mcpbroker.OAuthHandlerConfig{
		Service:        grant.Name,
		RedirectURL:    oauthCallbackURL,
		Store:          store,
		RequireSession: true,
		Fetcher: func(context.Context, *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			return nil, errors.New("daemon cannot start OAuth authorization; run torio mcp login")
		},
	})
	if err != nil {
		return nil, err
	}
	client := mcp.NewClient(
		&mcp.Implementation{Name: "torio-mcp-broker", Version: "1"},
		&mcp.ClientOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
	)
	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	session, err := client.Connect(connectCtx, &mcp.StreamableClientTransport{
		Endpoint:     grant.UpstreamEndpoint,
		HTTPClient:   &http.Client{Timeout: upstreamTimeout},
		OAuthHandler: handler,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("service %s could not connect to its authorized upstream", grant.Name)
	}
	return session, nil
}

func serveService(ctx context.Context, policy lima.Set, audit mcpbroker.Recorder, runtime *serviceRuntime) error {
	for {
		conn, err := runtime.listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("service %s accept failed", runtime.grant.Name)
		}
		go serveConnection(ctx, policy, audit, runtime, conn)
	}
}

func serveConnection(ctx context.Context, policy lima.Set, audit mcpbroker.Recorder, runtime *serviceRuntime, conn *net.UnixConn) {
	defer conn.Close()
	uid, err := peerUID(conn)
	if err != nil {
		slog.Warn("MCP connection refused: peer uid unavailable", "service", runtime.grant.Name)
		return
	}
	buildCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	server, err := mcpbroker.NewServiceServer(buildCtx, mcpbroker.ServiceConfig{
		Service:  runtime.grant.Name,
		Policy:   policy,
		Upstream: runtime.upstream,
		Audit:    audit,
		PeerUID:  uid,
	})
	cancel()
	if err != nil {
		slog.Warn("MCP connection refused: service validation failed", "service", runtime.grant.Name)
		return
	}
	_ = server.Run(ctx, &mcp.IOTransport{Reader: conn, Writer: conn})
}

func validateRuntimeDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("broker runtime directory is unavailable")
	}
	return nil
}

func listenService(dir, service string) (*net.UnixListener, error) {
	if err := lima.ValidateServiceName(service); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, service+".sock")
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("broker socket path is occupied by a non-socket")
		}
		if err := os.Remove(path); err != nil {
			return nil, errors.New("remove stale broker socket")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("inspect broker socket path")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen for service %s", service)
	}
	listener.SetUnlinkOnClose(true)
	if err := os.Chmod(path, 0o660); err != nil {
		_ = listener.Close()
		return nil, errors.New("set broker socket mode")
	}
	return listener, nil
}

func openAuditFile(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return nil, errors.New("audit file must be a private regular file with mode 0600")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("inspect audit file")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, errors.New("open audit file")
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		_ = file.Close()
		return nil, errors.New("audit file postcondition failed")
	}
	return file, nil
}

func writeRuntimeFile(dir, name string, body []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(dir, ".runtime-*")
	if err != nil {
		return errors.New("create broker runtime file")
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(mode); err != nil {
		return errors.New("set broker runtime file mode")
	}
	if _, err := temp.Write(body); err != nil {
		return errors.New("write broker runtime file")
	}
	if err := temp.Sync(); err != nil {
		return errors.New("sync broker runtime file")
	}
	if err := temp.Close(); err != nil {
		return errors.New("close broker runtime file")
	}
	path := filepath.Join(dir, name)
	if err := os.Rename(tempPath, path); err != nil {
		return errors.New("commit broker runtime file")
	}
	committed = true
	directory, err := os.Open(dir)
	if err != nil {
		return errors.New("open broker runtime directory")
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return errors.New("sync broker runtime directory")
	}
	return nil
}

func notifyReady() error {
	address := os.Getenv("NOTIFY_SOCKET")
	if address == "" {
		return nil
	}
	if strings.HasPrefix(address, "@") {
		address = "\x00" + strings.TrimPrefix(address, "@")
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: address, Net: "unixgram"})
	if err != nil {
		return errors.New("connect to systemd notify socket")
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("READY=1")); err != nil {
		return errors.New("notify systemd readiness")
	}
	return nil
}
