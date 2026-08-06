// Command torio-mcp-broker is the guest service that stands between the agent
// and the MCP servers it uses (ADR-0004).
//
// It runs as torio-mcp, an identity the agent cannot become, and publishes one
// unix socket per service under /run/torio-mcp. Membership in torio-mcp-clients
// is the entire privilege the agent identity holds: it may open the socket. What
// it may then do is decided here, against a policy document in
// /etc/torio-mcp/policy.d that the agent can read and cannot write.
//
// # What it enforces
//
// Two points, and both are the same rule seen from different sides. tools/list
// is filtered to the granted tools, so the surface an agent can see equals the
// surface it may use. tools/call is refused unless the policy lists the tool by
// name. Every decision is recorded, allowed and denied alike.
//
// # What it does not do
//
// It does not defend against a confused deputy. An injected instruction can use
// any granted tool against any permitted target, and to this process that is a
// correct call. The value is elsewhere: what is granted is written down,
// enumerable and verified, so "we granted write access to Jira" is a sentence
// somebody signed rather than a side effect of an installation.
//
// # Where output goes
//
// stdout carries audit records, one JSON object per line, and nothing else.
// stderr carries diagnostics. Neither ever carries tool call arguments or
// upstream reply content: those are Jira and Confluence material, and ADR-0004 §5
// keeps them out of every Torio surface.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/wzslr321/torio/internal/mcpbroker"
)

// policyDir holds one JSON policy document per service, root-owned and
// world-readable (ADR-0004 §4). It is fixed in the binary, like the socket
// directory: internal/lima.TorioMCPPolicyDir provisions the same path from the
// host side and stays the source of truth for it, but a guest binary does not
// import the host adapter.
const policyDir = "/etc/torio-mcp/policy.d"

// clientGroup is the group the broker hands its sockets to. The name is repeated
// from internal/lima.TorioMCPClientsGroup for the same reason, and the relay
// names it too — one string, three places that must not disagree, none of which
// may pull in the others.
const clientGroup = "torio-mcp-clients"

// policyDigestFile is published beside the sockets before readiness. It lets
// the control plane distinguish "these service names are listening" from "the
// running process loaded this exact effective grant".
const policyDigestFile = ".policy.sha256"

// Exit codes follow the table in docs/contracts/cli.md, so an operator debugging
// a guest reads one mapping rather than two. Only the classes this binary can
// reach are named.
const (
	// exitOK is a clean shutdown after a stop signal.
	exitOK = 0
	// exitInternal is a failure none of the classified ones below describe.
	exitInternal = 1
	// exitUsage is a bad invocation or a policy document that does not validate.
	exitUsage = 2
	// exitPrecondition is something that has to exist and does not: the policy
	// directory, a policy document in it, the client group.
	exitPrecondition = 3
	// exitConflict is an address already held by something else.
	exitConflict = 5
	// exitVerificationFailed is a boundary that did not come out the way it was
	// asked for, or that could not be established as safe to take over.
	exitVerificationFailed = 6
	// exitPermissionDenied is a privilege the broker's identity does not have.
	exitPermissionDenied = 7
)

// daemonConfig is the daemon's whole environment. The paths and the group are
// fields rather than constants inside run so that tests can exercise the wiring
// under a temp directory; production passes the fixed guest layout, and nothing
// on the command line can change it.
type daemonConfig struct {
	policyDir   string
	socketDir   string
	clientGroup string
	// peerUID is the source of the caller's identity. It is injectable because
	// SO_PEERCRED exists only on Linux and the wiring still has to be exercised on
	// a maintainer's machine. Left nil, it is the platform's own, which off Linux
	// refuses every connection rather than inventing a uid.
	peerUID func(*net.UnixConn) (uint32, error)
	// stdout receives audit records, one per line.
	stdout io.Writer
	// stderr receives diagnostics.
	stderr io.Writer
}

func main() {
	// SIGINT/SIGTERM cancel the daemon's context, which closes the listeners and
	// the connections parked on them. systemd stops this unit with SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], daemonConfig{
		policyDir:   policyDir,
		socketDir:   mcpbroker.SocketDir,
		clientGroup: clientGroup,
		stdout:      os.Stdout,
		stderr:      os.Stderr,
	}))
}

// run is the whole daemon: load the policy, publish one socket per service,
// serve until stopped.
//
// Every failure before the first socket appears is fatal. A broker that came up
// having loaded nothing would deny every call while looking entirely healthy —
// active unit, socket present, right mode — and the operator would be debugging
// an agent rather than reading an error.
func run(ctx context.Context, args []string, cfg daemonConfig) int {
	log := slog.New(slog.NewTextHandler(cfg.stderr, nil))

	if len(args) != 0 {
		fmt.Fprintf(cfg.stderr, "torio-mcp-broker: takes no arguments; what it serves comes from %s (got %d)\n",
			cfg.policyDir, len(args))
		fmt.Fprintf(cfg.stderr, "usage: torio-mcp-broker\n")
		return exitUsage
	}

	policy, err := mcpbroker.Load(cfg.policyDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Error("the policy directory is missing; the broker has nothing to enforce",
				"dir", cfg.policyDir, "remedy", "run `torio mcp install` on the host", "error", err)
			return exitPrecondition
		}
		// A document that does not validate is not a broker that grants less. It is
		// a grant nobody can read, so nothing is served until it is fixed.
		log.Error("a policy document could not be loaded; the broker serves nothing",
			"dir", cfg.policyDir, "error", err)
		return exitUsage
	}

	grant := policy.Grants()
	if len(grant.Services) == 0 {
		log.Error("the policy directory holds no service documents; the broker has nothing to serve",
			"dir", cfg.policyDir,
			"remedy", "write /etc/torio-mcp/policy.d/<service>.json as root, then start the broker")
		return exitPrecondition
	}

	gid, err := groupID(cfg.clientGroup)
	if err != nil {
		log.Error("the broker's client group does not exist; there is nobody to hand a socket to",
			"group", cfg.clientGroup, "remedy", "run `torio mcp install` on the host", "error", err)
		return exitPrecondition
	}

	servers, code := publish(cfg, policy, grant, gid, log)
	if code != exitOK {
		return code
	}
	digestPath := filepath.Join(cfg.socketDir, policyDigestFile)
	if err := publishPolicyDigest(digestPath, policy.Digest()); err != nil {
		log.Error("published sockets but could not publish the loaded policy generation", "error", err)
		closeAll(servers)
		return exitInternal
	}
	defer os.Remove(digestPath)
	if err := notifySystemdReady(); err != nil {
		log.Error("published sockets but could not notify the service manager", "error", err)
		closeAll(servers)
		return exitInternal
	}

	return serveAll(ctx, servers, log)
}

func publishPolicyDigest(path, digest string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".policy.sha256.tmp-")
	if err != nil {
		return fmt.Errorf("create policy digest: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod policy digest: %w", err)
	}
	if _, err := io.WriteString(tmp, digest+"\n"); err != nil {
		tmp.Close()
		return fmt.Errorf("write policy digest: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync policy digest: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close policy digest: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("publish policy digest: %w", err)
	}
	return nil
}

func notifySystemdReady() error {
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return nil
	}
	if socket[0] == '@' {
		socket = "\x00" + socket[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("connect NOTIFY_SOCKET: %w", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("READY=1")); err != nil {
		return fmt.Errorf("write NOTIFY_SOCKET: %w", err)
	}
	return nil
}

// publishedService is one service the daemon serves: its socket and the server
// that answers on it.
type publishedService struct {
	name   string
	server *server
	ln     *net.UnixListener
}

// publish binds every service's socket and states the grant it will enforce.
//
// The grant is logged at startup because ADR-0004 makes legibility the point:
// what is granted, where it goes, and how many of those tools write, without
// anybody having to read a file. If any service cannot be published the ones
// already up are closed again — a partly-published broker is one whose missing
// service fails at connect, where nothing explains why.
func publish(cfg daemonConfig, policy mcpbroker.Set, grant mcpbroker.Grant, gid int, log *slog.Logger) ([]publishedService, int) {
	audit := newAuditSink(cfg.stdout)
	var published []publishedService

	for _, sg := range grant.Services {
		srv, err := newServer(serverConfig{
			service:    sg.Name,
			policy:     policy,
			policyPath: policyDocumentPath(cfg.policyDir, sg.Name),
			upstream:   pendingUpstream{endpoint: sg.UpstreamEndpoint},
			audit:      audit,
			peerUID:    cfg.peerUID,
			log:        log,
		})
		if err != nil {
			log.Error("a service could not be brought up", "service", sg.Name, "error", err)
			closeAll(published)
			return nil, exitInternal
		}

		ln, err := listenService(cfg.socketDir, sg.Name, gid)
		if err != nil {
			log.Error("a service's socket could not be published", "service", sg.Name, "error", err)
			closeAll(published)
			return nil, listenExit(err)
		}

		log.Info("serving",
			"service", sg.Name,
			"socket", ln.Addr().String(),
			"upstream", sg.UpstreamEndpoint,
			"tools", len(sg.Tools),
			"write_tools", sg.WriteTools)
		published = append(published, publishedService{name: sg.Name, server: srv, ln: ln})
	}
	return published, exitOK
}

// policyDocumentPath is where a service's grant is written down. A denial names
// it, so it is derived from the same directory the policy was loaded from rather
// than from a constant that could drift out of step with it.
func policyDocumentPath(dir, service string) string {
	return filepath.Join(dir, service+policyFileExt)
}

// policyFileExt is the extension internal/mcpbroker loads policy documents from.
// It is repeated rather than imported for the same reason the group name is: a
// denial has to name a real file, and this binary does not reach into the loader
// for a constant it can state.
const policyFileExt = ".json"

func closeAll(services []publishedService) {
	for _, s := range services {
		s.ln.Close()
	}
}

// serveAll runs every service until the context is cancelled or one of them
// fails.
//
// A failing accept loop takes the whole daemon down. The alternative — carrying
// on with the remaining services — is a broker that is up and silently deaf on
// one socket, which is exactly the state ADR-0004 §6 says verification must be
// able to catch.
func serveAll(ctx context.Context, services []publishedService, log *slog.Logger) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	failures := make(chan error, len(services))
	var wg sync.WaitGroup
	for _, svc := range services {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := svc.server.serve(ctx, svc.ln); err != nil {
				log.Error("a service stopped serving", "service", svc.name, "error", err)
				failures <- err
				cancel()
			}
		}()
	}
	wg.Wait()
	closeAll(services)

	select {
	case <-failures:
		return exitInternal
	default:
		return exitOK
	}
}

// groupID resolves a group name to its gid.
func groupID(name string) (int, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, fmt.Errorf("group %s has a gid that is not a number: %w", name, err)
	}
	return gid, nil
}

// listenExit maps a socket failure to the exit class an operator acts on. The
// classes have nothing in common as remedies — stop the other broker, fix the
// unit's directory, fix the group — and a single code would flatten all of them
// into "it did not start".
func listenExit(err error) int {
	var le *listenError
	if errors.As(err, &le) {
		return le.exit
	}
	return exitInternal
}
