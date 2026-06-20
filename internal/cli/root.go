// Package cli wires the routeup command tree.
package cli

import (
	"github.com/spf13/cobra"
)

// Execute runs the routeup root command and returns any error from the command tree.
func Execute() error {
	return newRootCmd().Execute()
}

// version is the routeup build version. Overridden at release time via
// -ldflags -X (must be a var, not a const). The agent reports this string,
// and the CLI compares it against a running agent to decide whether to
// restart a stale build.
var version = "0.0.0-dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "routeup",
		Short: "Stable HTTPS routes for local services",
		Long: "routeup gives local services stable HTTPS names like\n" +
			"https://myapp.localhost, and can expose those same routes publicly\n" +
			"when you need to.\n\n" +
			"Run `routeup setup` once to create and trust a local CA and bind\n" +
			"port 443, then `routeup serve <name> --port <p>` to put a local app\n" +
			"on a trusted HTTPS route.",
		Example: "  # one-time machine setup: local CA, OS trust, port 443\n" +
			"  routeup setup\n\n" +
			"  # serve a local app on https://myapp.localhost\n" +
			"  routeup serve myapp --port 3000\n\n" +
			"  # list what's currently served\n" +
			"  routeup routes",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	root.AddCommand(
		newDoctorCmd(),
		newRoutesCmd(),
		newLogsCmd(),
		newServeCmd(),
		newAgentCmd(),
		newSetupCmd(),
		newForwardCmd(),
		newUninstallCmd(),
		newUpdateCmd(),
	)
	return root
}
