package serve

import (
	"strconv"
	"strings"

	"github.com/wzslr321/torio/internal/brain"
	"github.com/wzslr321/torio/internal/lima"
)

// renderUnit produces the exact bytes of the custom user systemd unit for the
// Hermes backend. It is deterministic and derived entirely from the pinned
// constants, so the loopback bind, the HERMES_HOME profile pin, and Restart
// policy are enforced by code (and locked by a golden test), never by hand.
//
// Environment=HERMES_HOME pins the existing /home/hermes/.hermes profile
// (hermes_constants.get_hermes_home reads $HERMES_HOME), and --skip-build is
// what lets the backend start without an npm build step in a non-interactive
// service.
func renderUnit() []byte {
	execStart := hermesShim + " serve --skip-build --host " + BindHost + " --port " + strconv.Itoa(BindPort)
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=Torio loopback backend (torio serve)\n")
	b.WriteString("\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString("WorkingDirectory=" + workingDir + "\n")
	b.WriteString("Environment=HERMES_HOME=" + lima.HermesProfilePath + "\n")
	b.WriteString("Environment=\"HERMES_ENVIRONMENT_HINT=" + brain.EnvironmentHint + "\"\n")
	b.WriteString("ExecStart=" + execStart + "\n")
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=2\n")
	b.WriteString("\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return []byte(b.String())
}
