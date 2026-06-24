package cli

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/server"
)

// newServerCmd builds the hidden `routeup server` operator command.
func newServerCmd() *cobra.Command {
	var (
		configPath  string
		domain      string
		listen      string
		namespace   string
		dbPath      string
		reserved    []string
		tlsMode     string
		acmeEmail   string
		acmeCA      string
		acmeStorage string
		tlsCert     string
		tlsKey      string
	)

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run the routeup public server (operator)",
		Long: "Run the routeup public server: token-authorized public route claims\n" +
			"backed by SQLite, with a WebSocket + yamux request tunnel. Configuration\n" +
			"is layered as flags over an optional routeup-server.json over built-in\n" +
			"defaults.\n\n" +
			"The server always serves HTTPS. By default it obtains wildcard\n" +
			"certificates automatically via Let's Encrypt using the Cloudflare DNS-01\n" +
			"challenge (set CLOUDFLARE_API_TOKEN): the root and public-namespace\n" +
			"wildcards at startup, and a per-namespace wildcard on first claim. Use\n" +
			"--tls-mode cert to serve an operator-provided certificate instead.",
		Example: "  CLOUDFLARE_API_TOKEN=… \\\n" +
			"    routeup server --domain routeup.dev --public-namespace try --acme-email you@example.com\n\n" +
			"  # operator-provided certificate (e.g. a Cloudflare origin cert, or local dev)\n" +
			"  routeup server --domain routeup.dev --tls-mode cert --tls-cert c.pem --tls-key k.pem --listen 127.0.0.1:8443",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := resolveServerConfig(configPath, server.ServerConfig{
				Domain:          domain,
				Listen:          listen,
				PublicNamespace: namespace,
				DBPath:          dbPath,
				Reserved:        reserved,
				TLSMode:         tlsMode,
				ACMEEmail:       acmeEmail,
				ACMECA:          acmeCA,
				ACMEStorage:     acmeStorage,
				TLSCert:         tlsCert,
				TLSKey:          tlsKey,
			})
			if err != nil {
				return err
			}

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
			srv, err := server.New(cfg, logger)
			if err != nil {
				return err
			}

			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return srv.Run(ctx)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "path to the server config file (default routeup-server.json)")
	cmd.Flags().StringVar(&domain, "domain", "", "public suffix served, e.g. routeup.dev (required)")
	cmd.Flags().StringVar(&listen, "listen", "", "ingress bind address (default :443)")
	cmd.Flags().StringVar(&namespace, "public-namespace", "", "token-less namespace label, e.g. try (empty disables)")
	cmd.Flags().StringVar(&dbPath, "db", "", "SQLite database path (default routeup-server.db)")
	cmd.Flags().StringArrayVar(&reserved, "reserved", nil, "extra reserved subdomain label (repeatable)")
	cmd.Flags().StringVar(&tlsMode, "tls-mode", "", "acme (default) or cert")
	cmd.Flags().StringVar(&acmeEmail, "acme-email", "", "ACME account email (recommended in acme mode)")
	cmd.Flags().StringVar(&acmeCA, "acme-ca", "", "ACME directory: production (default) or staging")
	cmd.Flags().StringVar(&acmeStorage, "acme-storage", "", "directory to cache issued certificates")
	cmd.Flags().StringVar(&tlsCert, "tls-cert", "", "PEM certificate (cert mode)")
	cmd.Flags().StringVar(&tlsKey, "tls-key", "", "PEM private key (cert mode)")
	return cmd
}
