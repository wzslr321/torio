package lima

import (
	_ "embed"
	"strings"
)

const (
	TorioMCPBrokerUnitName = "torio-mcp-broker.service"
	TorioMCPBrokerPath     = "/usr/local/bin/torio-mcp-broker"
	TorioMCPBrokerUnitPath = "/etc/systemd/system/" + TorioMCPBrokerUnitName
	mcpBrokerStagingPath   = "/etc/systemd/system/torio-mcp-broker-staging.service"
)

//go:embed templates/torio-mcp-broker.service
var embeddedMCPBrokerUnit []byte

// mcpBrokerUnit returns a copy so callers cannot mutate the embedded unit used
// by later installation steps in the same process.
func mcpBrokerUnit() []byte {
	return append([]byte(nil), embeddedMCPBrokerUnit...)
}

func mcpBrokerEffectiveUnitShowArgs() []string {
	return []string{
		"sudo", "-n", "systemctl", "show",
		"--property=FragmentPath",
		"--property=DropInPaths",
		"--property=NeedDaemonReload",
		"--property=User",
		"--property=Group",
		"--property=SupplementaryGroups",
		"--property=DynamicUser",
		"--property=Type",
		"--property=NotifyAccess",
		"--property=RuntimeDirectory",
		"--property=RuntimeDirectoryMode",
		"--property=UMask",
		"--property=NoNewPrivileges",
		"--property=PrivateTmp",
		"--property=ProtectSystem",
		"--property=ReadWritePaths",
		"--property=AmbientCapabilities",
		"--property=Restart",
		"--property=RestartUSec",
		TorioMCPBrokerUnitName,
	}
}

func effectiveMCPBrokerUnitExact(output string) bool {
	want := map[string]string{
		"FragmentPath":         TorioMCPBrokerUnitPath,
		"DropInPaths":          "",
		"NeedDaemonReload":     "no",
		"User":                 TorioMCPUser,
		"Group":                TorioMCPClientsGroup,
		"SupplementaryGroups":  "",
		"DynamicUser":          "no",
		"Type":                 "notify",
		"NotifyAccess":         "main",
		"RuntimeDirectory":     "torio-mcp",
		"RuntimeDirectoryMode": "0750",
		"UMask":                "0077",
		"NoNewPrivileges":      "yes",
		"PrivateTmp":           "yes",
		"ProtectSystem":        "strict",
		"ReadWritePaths":       TorioMCPHome,
		"AmbientCapabilities":  "",
		"Restart":              "on-failure",
		"RestartUSec":          "2s",
	}
	seen := make(map[string]struct{}, len(want))
	for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return false
		}
		expected, known := want[key]
		if !known || value != expected {
			return false
		}
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return len(seen) == len(want)
}
