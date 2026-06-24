package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	TLSModeACME = "acme"
	TLSModeCert = "cert"
)

const CloudflareTokenEnv = "CLOUDFLARE_API_TOKEN"

type ServerConfig struct {
	Domain          string   `json:"domain"`
	Listen          string   `json:"listen"`
	PublicNamespace string   `json:"public_namespace"`
	DBPath          string   `json:"db"`
	Reserved        []string `json:"reserved"`
	TLSMode         string   `json:"tls_mode"`
	ACMEEmail       string   `json:"acme_email"`
	ACMECA          string   `json:"acme_ca"`
	ACMEStorage     string   `json:"acme_storage"`
	TLSCert         string   `json:"tls_cert"`
	TLSKey          string   `json:"tls_key"`
}

func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Listen:  ":443",
		DBPath:  "routeup-server.db",
		TLSMode: TLSModeACME,
		ACMECA:  "production",
	}
}

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

// Overlay layers over onto base: non-empty strings and non-nil slices win.
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

func (c ServerConfig) EffectiveReserved() ReservedSet {
	extra := make([]string, 0, len(c.Reserved)+1)
	extra = append(extra, c.Reserved...)
	if c.PublicNamespace != "" {
		extra = append(extra, c.PublicNamespace)
	}
	return NewReservedSet(extra...)
}
