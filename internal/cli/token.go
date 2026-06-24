package cli

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/server"
)

// newTokenCmd builds the hidden operator command tree for managing public
// server tokens. These commands open the server's SQLite database directly, so
// the operator runs them on the server host; the server need not be running.
func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Manage public server tokens (operator)",
		Long: "Create, list, and revoke routeup public-server tokens.\n\n" +
			"These are operator commands run on the server host: they read and\n" +
			"write the server's SQLite database directly. Tokens authorize\n" +
			"persistent, scoped public route claims.",
		Hidden: true,
	}
	cmd.AddCommand(newTokenCreateCmd(), newTokenListCmd(), newTokenRevokeCmd())
	return cmd
}

// dbFlags adds the shared --config/--db flags and returns pointers to them.
func dbFlags(cmd *cobra.Command) (configPath, dbPath *string) {
	configPath = cmd.Flags().String("config", "", "path to the server config file (default routeup-server.json)")
	dbPath = cmd.Flags().String("db", "", "path to the server database (overrides config)")
	return configPath, dbPath
}

// openStoreFromFlags resolves the DB path from --config/--db and opens it.
func openStoreFromFlags(cmd *cobra.Command, configPath, dbPath string) (*server.Store, error) {
	cfg, err := resolveServerConfig(configPath, server.ServerConfig{DBPath: dbPath})
	if err != nil {
		return nil, err
	}
	return server.OpenStore(cmd.Context(), cfg.DBPath)
}

func newTokenCreateCmd() *cobra.Command {
	var allow []string
	cmd := &cobra.Command{
		Use:   "create <name> --allow <pattern>",
		Short: "Mint a new token with one or more allow patterns",
		Example: "  routeup token create alice --allow \"*.alice.routeup.dev\"\n" +
			"  routeup token create ci --allow \"*.ci.routeup.dev\" --allow \"*.staging.routeup.dev\"",
		Args: cobra.ExactArgs(1),
	}
	configPath, dbPath := dbFlags(cmd)
	cmd.Flags().StringArrayVar(&allow, "allow", nil, "host allow pattern, e.g. \"*.alice.routeup.dev\" (repeatable)")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if len(allow) == 0 {
			return fmt.Errorf("at least one --allow pattern is required")
		}
		patterns := make([]server.AllowPattern, 0, len(allow))
		for _, raw := range allow {
			p, err := server.ParseAllowPattern(raw)
			if err != nil {
				return err
			}
			patterns = append(patterns, p)
		}

		store, err := openStoreFromFlags(cmd, *configPath, *dbPath)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()

		id, secret, err := store.CreateToken(cmd.Context(), args[0], patterns)
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintln(out, "token created")
		_, _ = fmt.Fprintf(out, "  id:      %s\n", id)
		_, _ = fmt.Fprintf(out, "  name:    %s\n", args[0])
		for _, p := range patterns {
			_, _ = fmt.Fprintf(out, "  allow:   %s\n", p)
		}
		_, _ = fmt.Fprintf(out, "  secret:  %s\n", secret)
		_, _ = fmt.Fprintln(out, "")
		_, _ = fmt.Fprintln(out, "store this secret now — it is shown once and cannot be recovered.")
		return nil
	}
	return cmd
}

func newTokenListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List tokens (secrets are never shown)",
		Args:  cobra.NoArgs,
	}
	configPath, dbPath := dbFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		store, err := openStoreFromFlags(cmd, *configPath, *dbPath)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()

		tokens, err := store.ListTokens(cmd.Context())
		if err != nil {
			return err
		}

		out := cmd.OutOrStdout()
		if len(tokens) == 0 {
			_, _ = fmt.Fprintln(out, "no tokens")
			return nil
		}

		tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "ID\tNAME\tSTATUS\tCREATED\tALLOW")
		for _, tok := range tokens {
			status := "active"
			if tok.Revoked() {
				status = "revoked"
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				tok.ID, tok.Name, status,
				tok.CreatedAt.Format(time.RFC3339),
				patternList(tok.Patterns))
		}
		_ = tw.Flush()
		return nil
	}
	return cmd
}

func newTokenRevokeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke a token by id",
		Args:  cobra.ExactArgs(1),
	}
	configPath, dbPath := dbFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		store, err := openStoreFromFlags(cmd, *configPath, *dbPath)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()

		revoked, err := store.RevokeToken(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if revoked {
			_, _ = fmt.Fprintf(out, "token %s revoked\n", args[0])
		} else {
			_, _ = fmt.Fprintf(out, "no active token with id %q\n", args[0])
		}
		return nil
	}
	return cmd
}

// patternList renders allow patterns as a comma-separated string.
func patternList(patterns []server.AllowPattern) string {
	if len(patterns) == 0 {
		return "-"
	}
	out := patterns[0].String()
	for _, p := range patterns[1:] {
		out += "," + p.String()
	}
	return out
}
