package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
)

// certManager supplies the server's TLS certificate and lets the claim path
// ensure a wildcard certificate exists for a namespace base.
type certManager interface {
	// TLSConfig returns the *tls.Config for the listener.
	TLSConfig() *tls.Config
	// EnsureNamespace makes sure a wildcard for "*.<base>" is being managed.
	// It is a no-op for static certificates and is safe to call repeatedly.
	EnsureNamespace(ctx context.Context, base string)
}

// buildCertManager assembles the cert manager for the configured TLS mode. The
// server always serves HTTPS; the modes differ only in where the certificate
// comes from.
func (s *Server) buildCertManager(ctx context.Context) (certManager, error) {
	switch s.cfg.TLSMode {
	case "", TLSModeACME:
		return s.newACMECertManager(ctx)
	case TLSModeCert:
		return s.newStaticCertManager()
	default:
		return nil, fmt.Errorf("unknown tls mode %q", s.cfg.TLSMode)
	}
}

// staticCertManager serves an operator-provided certificate. It manages no
// namespaces — the supplied certificate must already cover what the server
// serves (e.g. a SAN cert for the apex plus the namespaces in use).
type staticCertManager struct{ cfg *tls.Config }

func (m *staticCertManager) TLSConfig() *tls.Config                  { return m.cfg }
func (m *staticCertManager) EnsureNamespace(context.Context, string) {}

func (s *Server) newStaticCertManager() (certManager, error) {
	pair, err := tls.LoadX509KeyPair(s.cfg.TLSCert, s.cfg.TLSKey)
	if err != nil {
		return nil, fmt.Errorf("load tls keypair: %w", err)
	}
	return &staticCertManager{cfg: &tls.Config{
		Certificates: []tls.Certificate{pair},
		MinVersion:   tls.VersionTLS12,
	}}, nil
}

// acmeCertManager obtains and renews wildcard certificates via Let's Encrypt
// using the Cloudflare DNS-01 challenge. It manages the root wildcard (and the
// public namespace) at startup, and a per-namespace wildcard lazily on first
// claim into that namespace.
type acmeCertManager struct {
	magic  *certmagic.Config
	logger *slog.Logger
}

func (m *acmeCertManager) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: m.magic.GetCertificate,
		NextProtos:     []string{"h2", "http/1.1"},
		MinVersion:     tls.VersionTLS12,
	}
}

func (m *acmeCertManager) EnsureNamespace(ctx context.Context, base string) {
	if base == "" {
		return
	}
	domain := "*." + base
	// ManageAsync is idempotent; certmagic skips a domain it already manages.
	if err := m.magic.ManageAsync(ctx, []string{domain}); err != nil {
		m.logger.Warn("manage namespace certificate", "domain", domain, "err", err)
	}
}

func (s *Server) newACMECertManager(ctx context.Context) (certManager, error) {
	token := os.Getenv(CloudflareTokenEnv)
	if token == "" {
		return nil, fmt.Errorf("tls mode acme needs a scoped Cloudflare API token in %s", CloudflareTokenEnv)
	}

	ca := certmagic.LetsEncryptProductionCA
	if s.cfg.ACMECA == "staging" {
		ca = certmagic.LetsEncryptStagingCA
	}

	magic := certmagic.NewDefault()
	if s.cfg.ACMEStorage != "" {
		magic.Storage = &certmagic.FileStorage{Path: s.cfg.ACMEStorage}
	}
	magic.Issuers = []certmagic.Issuer{
		certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{
			CA:     ca,
			Email:  s.cfg.ACMEEmail,
			Agreed: true,
			DNS01Solver: &certmagic.DNS01Solver{
				DNSManager: certmagic.DNSManager{
					DNSProvider:        &cloudflare.Provider{APIToken: token},
					PropagationTimeout: 2 * time.Minute,
				},
			},
		}),
	}

	// Startup wildcards: the root wildcard covers the control host and flat
	// root claims; the public namespace wildcard covers try, if enabled.
	startup := []string{"*." + s.cfg.Domain}
	if s.cfg.PublicNamespace != "" {
		startup = append(startup, "*."+s.cfg.PublicNamespace+"."+s.cfg.Domain)
	}
	// ManageAsync (not Sync) so the listener comes up immediately and the certs
	// are obtained in the background. Blocking on issuance would leave the port
	// closed for 30s-2min on a fresh CA, which fails platform smoke checks
	// (e.g. Fly). certmagic's GetCertificate serves from cache once ready, and
	// logs any issuance failure.
	s.logger.Info("managing startup wildcard certificates", "domains", startup, "ca", ca)
	if err := magic.ManageAsync(ctx, startup); err != nil {
		return nil, fmt.Errorf("manage startup certificates %v: %w", startup, err)
	}

	return &acmeCertManager{magic: magic, logger: s.logger}, nil
}
