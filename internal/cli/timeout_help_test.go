package cli

import (
	"regexp"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/wzslr321/torio/internal/config"
)

// recommendedTimeout matches a concrete "--timeout <duration>" recommendation in
// help text. A bare "--timeout" with no value is a reference to the flag, not a
// recommendation, and is deliberately not matched.
var recommendedTimeout = regexp.MustCompile(`--timeout[= ]([0-9]+(?:\.[0-9]+)?(?:ns|us|µs|ms|s|m|h)+)`)

// A timeout the CLI tells the operator to pass must be one the CLI accepts.
// `vm bootstrap` is the longest operation Torio drives — apt plus a Hermes source
// install — so a recommendation that policy rejects sends the operator into a
// dead end with exit 2 instead of the long-running command they were told to run.
func TestHelpNeverRecommendsATimeoutPolicyRejects(t *testing.T) {
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		for _, text := range []string{cmd.Short, cmd.Long, cmd.Example} {
			for _, match := range recommendedTimeout.FindAllStringSubmatch(text, -1) {
				got, err := time.ParseDuration(match[1])
				if err != nil {
					t.Errorf("%s: help recommends unparsable timeout %q: %v", cmd.CommandPath(), match[1], err)
					continue
				}
				if err := (config.Settings{Timeout: got}).Validate(); err != nil {
					t.Errorf("%s: help recommends --timeout %s, but policy rejects it: %v",
						cmd.CommandPath(), match[1], err)
				}
			}
		}
		for _, sub := range cmd.Commands() {
			walk(sub)
		}
	}
	walk(newRootCmd(&app{}))
}
