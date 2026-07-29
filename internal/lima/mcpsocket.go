package lima

import (
	"context"
	"fmt"
	"strings"
)

// TorioMCPSocketDir is where the broker publishes one socket per service. It is
// runtime state, not persistent state: the directory is created by the running
// unit and vanishes with it, which is why its absence means "no daemon" rather
// than "something was removed".
const TorioMCPSocketDir = "/run/torio-mcp"

// The socket's own owner, group and mode ARE the access control (ADR-0022 §3):
// only members of the client group may connect at all. Nothing else stands
// between an identity on this guest and the broker, so a widened mode is not a
// cosmetic drift.
var (
	socketOwner    = TorioMCPUser
	socketGroup    = TorioMCPClientsGroup
	socketModes    = map[string]bool{"660": true, "0660": true}
	socketDirModes = map[string]bool{"750": true, "0750": true, "755": true, "0755": true}
)

// socketDirModeList renders the accepted directory modes for a failure message.
// A drift report that names the wrong value without naming the right one leaves
// the operator to go and look it up.
const socketDirModeList = "0750 or 0755"

// verifyBrokerSockets proves that every published socket is both correctly
// owned and actually served.
//
// Ownership alone is not enough, and the gap is not theoretical: a socket file
// left behind by a broker that crashed satisfies every owner, group and mode
// test while refusing every connection. Checking only the file would report that
// the boundary holds on a machine where nothing is running — the most misleading
// answer this command could give, because it is the one an operator would act
// on. Liveness is therefore read from the kernel's list of listening sockets,
// not inferred from the file existing.
func (a *Adapter) verifyBrokerSockets(ctx context.Context, rep *MCPBrokerReport) error {
	const name = "broker_sockets"

	st, kind, err := a.statPath(ctx, rep, name, TorioMCPSocketDir)
	if err != nil {
		return err
	}
	if st == pathUnprovable {
		return a.probeUnusable(rep, name, "the broker socket directory")
	}
	if st == pathAbsent {
		// The daemon is a separate install from the identity boundary. A guest
		// that has the boundary and no daemon is not drifted, and calling it
		// drift would spend the word on a machine that is merely incomplete.
		rep.record(name, true, "absent (no broker daemon installed)")
		return nil
	}
	if kind != "directory" {
		return a.brokerFailed(rep, name, TorioMCPSocketDir+" is not a directory",
			"inspect the guest by hand; this path is runtime state owned by the broker unit")
	}

	dirOG, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "stat", "-c", "%U:%G %a", TorioMCPSocketDir)
	if err != nil {
		return err
	}
	owner, group, mode, ok := parseStatOwnership(dirOG.out)
	if dirOG.exit != 0 || !ok {
		return a.brokerFailed(rep, name, "could not read socket directory ownership/mode", "verify "+TorioMCPSocketDir+" on the guest")
	}
	// The group is compared, not merely reported. At 0750 the directory's group
	// is the only thing that lets the agent identity traverse to the socket, so
	// torio-mcp:torio-mcp 0750 satisfies owner and mode while every connect from
	// hermes fails — and this check would have said the boundary holds on a guest
	// where MCP cannot work at all.
	if owner != socketOwner || group != socketGroup || !socketDirModes[mode] {
		return a.brokerFailed(rep, name,
			fmt.Sprintf("socket directory is %s:%s %s, want %s:%s with a mode of %s",
				owner, group, mode, socketOwner, socketGroup, socketDirModeList),
			"the broker unit must own its runtime directory and hand it to "+socketGroup+
				", which is what lets the agent identity reach the socket at all; reinstall the unit rather than fixing it by hand")
	}

	// One line per socket, carrying everything the check needs, so the number of
	// guest round trips does not grow with the number of services.
	listing, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "find", TorioMCPSocketDir,
		"-mindepth", "1", "-maxdepth", "1", "-type", "s", "-printf", `%f %u %g %m\n`)
	if err != nil {
		return err
	}
	if listing.exit != 0 {
		return a.brokerFailed(rep, name, "could not enumerate broker sockets", "verify "+TorioMCPSocketDir+" on the guest")
	}
	sockets := strings.Fields(strings.TrimSpace(listing.out))
	if len(sockets) == 0 {
		rep.record(name, true, "no sockets published")
		return nil
	}

	listening, err := a.brokerProbe(ctx, rep, name, "sudo", "-n", "ss", "-lxH")
	if err != nil {
		return err
	}
	if listening.exit != 0 {
		return a.brokerFailed(rep, name, "could not read listening sockets", "confirm `ss` is present on the guest")
	}

	served := make([]string, 0, 4)
	for _, line := range strings.Split(strings.TrimSpace(listing.out), "\n") {
		f := strings.Fields(line)
		if len(f) != 4 {
			return a.brokerFailed(rep, name, "unparseable socket listing", "verify "+TorioMCPSocketDir+" on the guest")
		}
		file, sOwner, sGroup, sMode := f[0], f[1], f[2], f[3]
		service := strings.TrimSuffix(file, ".sock")

		if sOwner != socketOwner || sGroup != socketGroup {
			return a.brokerFailed(rep, name,
				fmt.Sprintf("socket %s is %s:%s, want %s:%s", service, sOwner, sGroup, socketOwner, socketGroup),
				"the socket's ownership is the access control; reinstall the broker unit")
		}
		if !socketModes[sMode] {
			return a.brokerFailed(rep, name,
				fmt.Sprintf("socket %s has mode %s, want 0660", service, sMode),
				"a widened mode hands the broker to identities outside "+TorioMCPClientsGroup)
		}
		if !isListening(listening.out, TorioMCPSocketDir+"/"+file) {
			return a.brokerFailed(rep, name,
				fmt.Sprintf("socket %s exists but nothing is listening on it", service),
				"the broker unit is stopped or crashed; restart it — the file alone proves nothing")
		}
		served = append(served, service)
	}

	rep.record(name, true, "serving "+strings.Join(served, ","))
	return nil
}

// isListening reports whether path appears as a LISTEN entry in `ss -lxH`
// output. The path is matched as a whole field rather than a substring: a
// prefix match would accept a different socket whose name merely starts the
// same way, which is exactly the confusion this check exists to prevent.
func isListening(ssOutput, path string) bool {
	for _, line := range strings.Split(ssOutput, "\n") {
		if !strings.Contains(line, "LISTEN") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if field == path {
				return true
			}
		}
	}
	return false
}
