package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// TLS modes. The server always serves TLS — there is no plaintext mode.
const (
	// TLSModeACME obtains and renews wildcard certificates automatically via
	// Let's Encrypt using the Cloudflare DNS-01 challenge. The default.
	TLSModeACME = "acme"
	// TLSModeCert serves an operator-provided certificate and key (e.g. a
	// Cloudflare origin cert, a certbot DNS-01 run, or a self-signed cert for
	// local development used with `expose --insecure`).
	TLSModeCert = "cert"
)

// CloudflareTokenEnv is the environment variable holding the scoped Cloudflare
// API token (Zone.DNS:Edit) used for ACME DNS-01. It is never read from the
// config file.
const CloudflareTokenEnv = "CLOUDFLARE_API_TOKEN"

// ServerConfig is the public server's configuration. It is sourced, in
// increasing precedence, from built-in defaults, a JSON config file, then
// explicit CLI flags. Zero-valued fields mean "unset" so a higher-precedence
// source can overlay a lower one without clobbering it (see Overlay).
type ServerConfig struct {
	// Domain is the public suffix the server serves, e.g. "routeup.dev".
	// Required.
	Domain string `json:"domain"`

	// Listen is the ingress bind address, e.g. ":443".
	Listen string `json:"listen"`

	// PublicNamespace is the token-less namespace label, e.g. "try". Empty
	// disables anonymous claims. It is added to the reserved set automatically.
	PublicNamespace string `json:"public_namespace"`

	// DBPath is the SQLite database path for tokens and claims.
	DBPath string `json:"db"`

	// Reserved lists extra subdomain labels to reserve in addition to the
	// built-in defaults. It extends, never replaces, DefaultReservedLabels.
	Reserved []string `json:"reserved"`

	// TLSMode selects how the server gets its certificate: acme (default) or
	// cert. The server always serves HTTPS.
	TLSMode string `json:"tls_mode"`

	// ACMEEmail is the contact email for the ACME account (recommended, used
	// for expiry notices). Only relevant in acme mode.
	ACMEEmail string `json:"acme_email"`

	// ACMECA selects the ACME directory: "production" (default) or "staging".
	// Use staging while testing to avoid Let's Encrypt rate limits.
	ACMECA string `json:"acme_ca"`

	// ACMEStorage is where issued certificates are cached (so restarts and
	// renewals do not re-issue). Empty uses certmagic's default location.
	ACMEStorage string `json:"acme_storage"`

	// TLSCert and TLSKey are PEM paths used in cert mode. For public use this
	// should be a wildcard certificate for *.<domain> plus the apex.
	TLSCert string `json:"tls_cert"`
	TLSKey  string `json:"tls_key"`
}

// DefaultServerConfig returns the built-in defaults. Every field is overridable
// by a config file or a flag.
func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Listen:  ":443",
		DBPath:  "routeup-server.db",
		TLSMode: TLSModeACME,
		ACMECA:  "production",
	}
}

// LoadServerConfig reads and decodes a server config JSON file. A missing file
// is reported as a wrapped os.ErrNotExist, so callers may treat "no file" as
// "use defaults plus flags".
func LoadServerConfig(path string) (ServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ServerConfig{}, fmt.Errorf("reading server config %s: %w", path, err)
	}
	var c ServerConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return ServerConfig{}, fmt.Errorf("parsing server config %s: %w", path, err)
	}
	return c, nil
}

// Overlay returns base with every field that is set in over replacing the base
// value. "Set" means non-empty for strings and non-nil for Reserved. It layers
// file-over-default and flag-over-file in the precedence chain.
func Overlay(base, over ServerConfig) ServerConfig {
	out := base
	if over.Domain != "" {
		out.Domain = over.Domain
	}
	if over.Listen != "" {
		out.Listen = over.Listen
	}
	if over.PublicNamespace != "" {
		out.PublicNamespace = over.PublicNamespace
	}
	if over.DBPath != "" {
		out.DBPath = over.DBPath
	}
	if over.Reserved != nil {
		out.Reserved = over.Reserved
	}
	if over.TLSMode != "" {
		out.TLSMode = over.TLSMode
	}
	if over.ACMEEmail != "" {
		out.ACMEEmail = over.ACMEEmail
	}
	if over.ACMECA != "" {
		out.ACMECA = over.ACMECA
	}
	if over.ACMEStorage != "" {
		out.ACMEStorage = over.ACMEStorage
	}
	if over.TLSCert != "" {
		out.TLSCert = over.TLSCert
	}
	if over.TLSKey != "" {
		out.TLSKey = over.TLSKey
	}
	return out
}

// Validate checks a fully-resolved config and returns actionable errors.
func (c ServerConfig) Validate() error {
	if c.Domain == "" {
		return errors.New("server domain is required (set \"domain\" in config or pass --domain)")
	}
	if err := validateDNSName(strings.ToLower(c.Domain)); err != nil {
		return fmt.Errorf("invalid domain %q: %w", c.Domain, err)
	}
	if c.PublicNamespace != "" {
		if err := validateLabel(strings.ToLower(c.PublicNamespace)); err != nil {
			return fmt.Errorf("invalid public_namespace %q: %w", c.PublicNamespace, err)
		}
	}
	for _, r := range c.Reserved {
		if err := validateLabel(strings.ToLower(strings.TrimSpace(r))); err != nil {
			return fmt.Errorf("invalid reserved label %q: %w", r, err)
		}
	}
	if c.Listen == "" {
		return errors.New("server listen address is required")
	}
	if c.DBPath == "" {
		return errors.New("server db path is required")
	}
	switch c.TLSMode {
	case "", TLSModeACME:
		// acme is the default (empty == acme); its Cloudflare token is checked
		// at runtime since it comes from the environment, not this config.
	case TLSModeCert:
		if c.TLSCert == "" || c.TLSKey == "" {
			return errors.New("tls mode cert needs both tls_cert and tls_key")
		}
	default:
		return fmt.Errorf("invalid tls_mode %q (want acme or cert)", c.TLSMode)
	}
	if c.ACMECA != "" && c.ACMECA != "production" && c.ACMECA != "staging" {
		return fmt.Errorf("invalid acme_ca %q (want production or staging)", c.ACMECA)
	}
	return nil
}

// EffectiveReserved builds the reserved-label set: the built-in defaults plus
// any configured extras plus the public-namespace label.
func (c ServerConfig) EffectiveReserved() ReservedSet {
	extra := make([]string, 0, len(c.Reserved)+1)
	extra = append(extra, c.Reserved...)
	if c.PublicNamespace != "" {
		extra = append(extra, c.PublicNamespace)
	}
	return NewReservedSet(extra...)
}
