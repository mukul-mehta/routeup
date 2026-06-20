// Package certs manages the per-machine local CA and the per-SNI TLS
// leaves it signs.
package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // SHA-1 is the standard for SubjectKeyId per RFC 5280 §4.2.1.2
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/mukul-mehta/routeup/internal/state"
)

const (
	caValidity     = 10 * 365 * 24 * time.Hour
	caCommonName   = "routeup local CA"
	caOrganization = "routeup"
)

// CA is the per-machine local root certificate authority.
type CA struct {
	Cert    *x509.Certificate
	Key     *ecdsa.PrivateKey
	CertPEM []byte
}

// Load reads the CA written by Create. Returns wrapped os.ErrNotExist if missing.
func Load(certPath, keyPath string) (*CA, error) {
	certRaw, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("read cert at %s: %w", certPath, err)
	}

	keyRaw, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read key at %s: %w", keyPath, err)
	}

	certBlock, _ := pem.Decode(certRaw)
	if certBlock == nil || certBlock.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("decode cert pem at %s: not a CERTIFICATE block", certPath)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse cert at %s: %w", certPath, err)
	}

	keyBlock, _ := pem.Decode(keyRaw)
	if keyBlock == nil || keyBlock.Type != "EC PRIVATE KEY" {
		return nil, fmt.Errorf("decode key pem at %s: not an EC PRIVATE KEY block", keyPath)
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse key at %s: %w", keyPath, err)
	}

	return &CA{
		Cert:    cert,
		Key:     key,
		CertPEM: certRaw,
	}, nil
}

// Create writes a fresh CA: cert PEM 0644, key PEM 0600.
func Create(certPath, keyPath string) (*CA, error) {
	if err := state.EnsureParentDir(certPath); err != nil {
		return nil, err
	}
	if err := state.EnsureParentDir(keyPath); err != nil {
		return nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ca key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate ca serial: %w", err)
	}

	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}

	// macOS Security framework rejects CA certs without SubjectKeyId.
	spkiDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshal ca spki: %w", err)
	}
	skid := sha1.Sum(spkiDER)

	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:   serial,
		SubjectKeyId:   skid[:],
		AuthorityKeyId: skid[:],
		Subject: pkix.Name{
			CommonName:         "routeup local CA",
			Organization:       []string{caOrganization},
			OrganizationalUnit: []string{host},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("self-sign ca: %w", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse signed ca: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal ca key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", certPath, err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", keyPath, err)
	}

	return &CA{
		Cert:    cert,
		Key:     key,
		CertPEM: certPEM,
	}, nil
}

// EnsureCA returns the CA when usable; otherwise an error suggesting
// `routeup setup`. Callers add their own framing prefix.
func EnsureCA(certPath, keyPath string) (*CA, error) {
	state, ca, inspectErr := Inspect(certPath, keyPath)
	switch state {
	case CAPresent:
		return ca, nil
	case CAAbsent:
		return nil, fmt.Errorf("no local CA at %s — run `routeup setup` to create one", certPath)
	case CAPartial:
		return nil, fmt.Errorf("partial CA state at %s, %s — delete both files and run `routeup setup` to regenerate", certPath, keyPath)
	case CABroken:
		return nil, fmt.Errorf("local CA unusable: %w — run `routeup setup` to regenerate", inspectErr)
	}
	return nil, fmt.Errorf("unexpected CA state %v", state)
}

// CAState describes the on-disk state of the CA.
type CAState int

const (
	CAAbsent CAState = iota
	CAPartial
	CAPresent
	CABroken
)

func (s CAState) String() string {
	switch s {
	case CAAbsent:
		return "absent"
	case CAPartial:
		return "partial"
	case CAPresent:
		return "present"
	case CABroken:
		return "broken"
	}
	return "unknown"
}

// ErrCAExpired is wrapped by Inspect when the cert is past NotAfter.
var ErrCAExpired = errors.New("CA has expired")

// Inspect classifies the on-disk CA state. ca is non-nil for CAPresent and
// for expiry-caused CABroken; err is non-nil only for CABroken.
func Inspect(certPath, keyPath string) (CAState, *CA, error) {
	certExists := fileExists(certPath)
	keyExists := fileExists(keyPath)

	switch {
	case !certExists && !keyExists:
		return CAAbsent, nil, nil
	case certExists != keyExists:
		return CAPartial, nil, nil
	}

	ca, err := Load(certPath, keyPath)
	if err != nil {
		return CABroken, nil, err
	}
	if time.Now().After(ca.Cert.NotAfter) {
		return CABroken, ca, fmt.Errorf("CA at %s expired on %s: %w",
			certPath, ca.Cert.NotAfter.Format(time.RFC3339), ErrCAExpired)
	}
	return CAPresent, ca, nil
}

// fileExists treats any non-ErrNotExist stat error as "exists" so the
// caller surfaces it via Load instead of silently treating it as missing.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	return !errors.Is(err, os.ErrNotExist)
}
