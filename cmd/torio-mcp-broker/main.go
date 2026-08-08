// Command torio-mcp-broker owns upstream MCP OAuth sessions and exposes the
// exact root-owned tool grant over uid-attributed Unix sockets (ADR-0012).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

const (
	defaultPolicyDir = "/etc/torio-mcp/policy.d"
	defaultSocketDir = "/run/torio-mcp"
	defaultStoreDir  = "/home/torio-mcp/oauth"
	defaultAuditPath = "/home/torio-mcp/audit.jsonl"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "login" {
		runLoginCommand(os.Args[2:])
		return
	}
	runDaemonCommand(os.Args[1:])
}

func runDaemonCommand(args []string) {
	flags := flag.NewFlagSet("torio-mcp-broker", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	policyDir := flags.String("policy-dir", defaultPolicyDir, "root-owned MCP policy directory")
	socketDir := flags.String("socket-dir", defaultSocketDir, "broker runtime directory")
	storeDir := flags.String("store-dir", defaultStoreDir, "private OAuth session directory")
	auditPath := flags.String("audit-file", defaultAuditPath, "private audit JSONL file")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		if err == nil {
			_, _ = fmt.Fprintln(os.Stderr, "torio-mcp-broker: unexpected positional argument")
		}
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runDaemon(ctx, daemonConfig{
		policyDir: *policyDir,
		socketDir: *socketDir,
		storeDir:  *storeDir,
		auditPath: *auditPath,
	}); err != nil {
		slog.Error("MCP broker stopped", "error", err)
		os.Exit(1)
	}
}

func runLoginCommand(args []string) {
	flags := flag.NewFlagSet("torio-mcp-broker login", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	policyDir := flags.String("policy-dir", defaultPolicyDir, "root-owned MCP policy directory")
	storeDir := flags.String("store-dir", defaultStoreDir, "private OAuth session directory")
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		if err == nil {
			_, _ = fmt.Fprintln(os.Stderr, "usage: torio-mcp-broker login [flags] <service>")
		}
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runLogin(ctx, loginConfig{
		policyDir: *policyDir,
		storeDir:  *storeDir,
		service:   flags.Arg(0),
		output:    os.Stdout,
	}); err != nil {
		slog.Error("MCP login failed", "error", err)
		os.Exit(1)
	}
}
