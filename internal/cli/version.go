package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newVersionCmd builds `torio version`. It takes no positional arguments and
// honors the bounded operation context and --json.
func newVersionCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version and build information",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := a.opContext(cmd)
			defer cancel()
			if err := runVersion(ctx, a.stdout, a.jsonOut, a.build); err != nil {
				return internalError(err.Error())
			}
			return nil
		},
	}
}

// BuildInfo carries build/runtime metadata for `torio version`. cmd/torio populates
// Version/Commit/BuildDate via -ldflags and the runtime fields from the Go
// runtime; tests inject deterministic values.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
	GoVersion string
	OS        string
	Arch      string
}

// versionData is the `data` object of the version envelope.
type versionData struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

func (b BuildInfo) data() versionData {
	return versionData{
		Version:   b.Version,
		Commit:    b.Commit,
		BuildDate: b.BuildDate,
		GoVersion: b.GoVersion,
		OS:        b.OS,
		Arch:      b.Arch,
	}
}

// runVersion writes version information. In JSON mode it emits exactly one
// envelope to stdout; otherwise it writes a human-readable summary to stdout.
// It honors the operation context: a cancelled/expired context aborts before
// any output is written.
func runVersion(ctx context.Context, stdout io.Writer, jsonOut bool, build BuildInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if jsonOut {
		return writeJSON(stdout, successEnvelope("version", build.data()))
	}
	if _, err := fmt.Fprintf(stdout, "torio %s (commit %s, built %s)\n", build.Version, build.Commit, build.BuildDate); err != nil {
		return err
	}
	_, err := fmt.Fprintf(stdout, "%s %s/%s\n", build.GoVersion, build.OS, build.Arch)
	return err
}
