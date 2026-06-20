package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

// 825 days matches mkcert; safely under browser non-public-CA limits.
const leafValidity = 825 * 24 * time.Hour

// Issuer signs per-SNI TLS leaves on demand. SNI must end in allowedSuffix.
type Issuer struct {
	ca            *CA
	allowedSuffix string
	mu            sync.Mutex
	cache         map[string]*tls.Certificate
}

func NewIssuer(ca *CA, allowedSuffix string) *Issuer {
	return &Issuer{
		ca:            ca,
		allowedSuffix: allowedSuffix,
		cache:         make(map[string]*tls.Certificate),
	}
}

// GetCertificate plugs into tls.Config.GetCertificate.
func (i *Issuer) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if hello == nil {
		return nil, errors.New("nil ClientHelloInfo")
	}
	name := strings.ToLower(hello.ServerName)
	if name == "" {
		return nil, errors.New("missing SNI server name")
	}
	if !strings.HasSuffix(name, i.allowedSuffix) {
		return nil, fmt.Errorf("sni %q not in suffix %q", name, i.allowedSuffix)
	}
	if strings.TrimSuffix(name, i.allowedSuffix) == "" {
		return nil, fmt.Errorf("sni %q is bare suffix", name)
	}

	i.mu.Lock()
	defer i.mu.Unlock()
	if cert, ok := i.cache[name]; ok {
		return cert, nil
	}

	cert, err := i.issueLeafCert(name)
	if err != nil {
		return nil, fmt.Errorf("issue leaf for %q: %w", name, err)
	}
	i.cache[name] = cert
	return cert, nil
}

// issueLeafCert mints a leaf for sni. Caller holds a mutwx
func (i *Issuer) issueLeafCert(sni string) (*tls.Certificate, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate leaf key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("generate leaf serial: %w", err)
	}

	now := time.Now()
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: sni},
		DNSNames:              []string{sni},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(leafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}

	leafDER, err := x509.CreateCertificate(rand.Reader, tpl, i.ca.Cert, &leafKey.PublicKey, i.ca.Key)
	if err != nil {
		return nil, fmt.Errorf("sign leaf: %w", err)
	}

	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		return nil, fmt.Errorf("parse signed leaf: %w", err)
	}

	return &tls.Certificate{
		Certificate: [][]byte{leafDER, i.ca.Cert.Raw},
		PrivateKey:  leafKey,
		Leaf:        leaf,
	}, nil
}
