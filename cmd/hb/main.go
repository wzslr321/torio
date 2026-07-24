// Command hb is the Hermes Box control-plane CLI. This binary wires process
// I/O, a signal-aware context, and build metadata into the cli package, which
// owns flag parsing, dispatch, the JSON envelope, and exit-code mapping.
package main

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"

	"hermes-box.local/hb/internal/cli"
)

// Build metadata. These are overridable at link time, e.g.
//
//	go build -ldflags "-X main.version=1.0.0 -X main.commit=$(git rev-parse --short HEAD) -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
//
// When not set, they fall back to the embedded build info.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	// Cancel the operation context on interrupt/termination so in-flight
	// external commands (via internal/execx) are stopped promptly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, resolveBuildInfo())
	os.Exit(code)
}

// resolveBuildInfo prefers link-time values and falls back to the Go build
// info embedded in the binary for the commit and build date.
func resolveBuildInfo() cli.BuildInfo {
	c, d := commit, date
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if c == "unknown" && s.Value != "" {
					c = s.Value
				}
			case "vcs.time":
				if d == "unknown" && s.Value != "" {
					d = s.Value
				}
			}
		}
	}
	return cli.BuildInfo{
		Version:   version,
		Commit:    c,
		BuildDate: d,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}
