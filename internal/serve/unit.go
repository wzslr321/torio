package serve

import (
	"strconv"
	"strings"

	"hermes-box.local/hb/internal/lima"
)

// renderUnit produces the exact bytes of the custom user systemd unit for the
// Hermes backend. It is deterministic and derived entirely from the pinned
// constants, so the loopback bind, the HERMES_HOME profile pin, and Restart
// policy are enforced by code (and locked by a golden test), never by hand.
//
// Design notes proven by live discovery:
//   - Environment=HERMES_HOME pins the existing /home/hermes/.hermes profile
//     (hermes_constants.get_hermes_home reads $HERMES_HOME).
//   - ExecStart binds 127.0.0.1:9119 explicitly and passes --skip-build so the
//     backend starts without an npm build step in a non-interactive service.
//   - Restart=always keeps the backend persistent; loopback-only means no
//     network-ordering dependency is needed.
//   - WantedBy=default.target + user linger makes it start at boot.
func renderUnit() []byte {
	execStart := hermesShim + " serve --skip-build --host " + BindHost + " --port " + strconv.Itoa(BindPort)
	var b strings.Builder
	b.WriteString("[Unit]\n")
	b.WriteString("Description=Hermes Box loopback backend (hb serve)\n")
	b.WriteString("\n")
	b.WriteString("[Service]\n")
	b.WriteString("Type=simple\n")
	b.WriteString("WorkingDirectory=" + workingDir + "\n")
	b.WriteString("Environment=HERMES_HOME=" + lima.HermesKBPath + "\n")
	b.WriteString("ExecStart=" + execStart + "\n")
	b.WriteString("Restart=always\n")
	b.WriteString("RestartSec=2\n")
	b.WriteString("\n")
	b.WriteString("[Install]\n")
	b.WriteString("WantedBy=default.target\n")
	return []byte(b.String())
}
