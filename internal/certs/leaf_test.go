package certs

import (
	"crypto/tls"
	"crypto/x509"
	"sync"
	"testing"
	"time"
)

func newTestIssuer(t *testing.T) (*CA, *Issuer) {
	t.Helper()
	certPath, keyPath := caPaths(t)
	ca, err := Create(certPath, keyPath)
	if err != nil {
		t.Fatalf("Create CA: %v", err)
	}
	return ca, NewIssuer(ca, ".localhost")
}

func TestIssue_ValidLocalhost(t *testing.T) {
	ca, issuer := newTestIssuer(t)

	cert, err := issuer.GetCertificate(&tls.ClientHelloInfo{ServerName: "api.myapp.localhost"})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if cert == nil || cert.Leaf == nil {
		t.Fatal("returned cert or leaf is nil")
	}
	if cert.PrivateKey == nil {
		t.Fatal("returned cert has no PrivateKey")
	}
	if len(cert.Certificate) < 2 {
		t.Fatalf("chain length = %d, want at least 2 (leaf + CA)", len(cert.Certificate))
	}

	pool := x509.NewCertPool()
	pool.AddCert(ca.Cert)
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{
		Roots:   pool,
		DNSName: "api.myapp.localhost",
	}); err != nil {
		t.Errorf("leaf.Verify: %v", err)
	}
}

func TestIssue_RejectsBadSNI(t *testing.T) {
	_, issuer := newTestIssuer(t)

	cases := []struct {
		name string
		sni  string
	}{
		{"empty", ""},
		{"external", "example.com"},
		{"bare-suffix", ".localhost"},
		{"wrong-suffix", "foo.example.com"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := issuer.GetCertificate(&tls.ClientHelloInfo{ServerName: tc.sni})
			if err == nil {
				t.Errorf("expected error for SNI %q, got nil", tc.sni)
			}
		})
	}
}

func TestIssue_Cached(t *testing.T) {
	_, issuer := newTestIssuer(t)
	hello := &tls.ClientHelloInfo{ServerName: "myapp.localhost"}

	first, err := issuer.GetCertificate(hello)
	if err != nil {
		t.Fatalf("first GetCertificate: %v", err)
	}
	second, err := issuer.GetCertificate(hello)
	if err != nil {
		t.Fatalf("second GetCertificate: %v", err)
	}
	if first != second {
		t.Error("second call returned different *tls.Certificate pointer; cache not used")
	}
}

func TestIssue_Concurrent(t *testing.T) {
	_, issuer := newTestIssuer(t)

	const N = 100
	var wg sync.WaitGroup
	results := make([]*tls.Certificate, N)
	errs := make([]error, N)

	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cert, err := issuer.GetCertificate(&tls.ClientHelloInfo{ServerName: "concurrent.localhost"})
			results[idx] = cert
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	first := results[0]
	if first == nil {
		t.Fatalf("first result nil: %v", errs[0])
	}
	for i, got := range results[1:] {
		idx := i + 1
		if errs[idx] != nil {
			t.Errorf("goroutine %d: %v", idx, errs[idx])
		}
		if got != first {
			t.Errorf("goroutine %d returned different cert pointer; not deduped", idx)
		}
	}
}

func TestIssue_LeafShape(t *testing.T) {
	_, issuer := newTestIssuer(t)

	cert, err := issuer.GetCertificate(&tls.ClientHelloInfo{ServerName: "shape.localhost"})
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	leaf := cert.Leaf

	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "shape.localhost" {
		t.Errorf("DNSNames = %v, want [shape.localhost]", leaf.DNSNames)
	}
	if leaf.IsCA {
		t.Error("leaf IsCA = true, want false")
	}
	hasServerAuth := false
	for _, eku := range leaf.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
			break
		}
	}
	if !hasServerAuth {
		t.Error("ExtKeyUsage missing ServerAuth")
	}
	if leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Error("KeyUsage missing DigitalSignature")
	}

	expected := time.Now().Add(leafValidity)
	if delta := leaf.NotAfter.Sub(expected); delta > time.Hour || delta < -time.Hour {
		t.Errorf("NotAfter = %v, expected ~%v (delta %v)", leaf.NotAfter, expected, delta)
	}
}
