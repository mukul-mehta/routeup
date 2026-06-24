package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeWildcardCert generates a self-signed cert for routeup.dev + *.routeup.dev
// and writes the cert and key PEMs, returning their paths and the cert pool.
func writeWildcardCert(t *testing.T) (certPath, keyPath string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "routeup.dev"},
		DNSNames:     []string{"routeup.dev", "*.routeup.dev"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)

	dir := t.TempDir()
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, _ := x509.MarshalECPrivateKey(key)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath, pool
}

// TestServer_ServesHTTPS confirms the server loads a cert/key and serves the
// control API over real TLS.
func TestServer_ServesHTTPS(t *testing.T) {
	certPath, keyPath, pool := writeWildcardCert(t)

	store, err := OpenStore(context.Background(), filepath.Join(t.TempDir(), "tls.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	cfg := ServerConfig{
		Domain: "routeup.dev", Listen: "127.0.0.1:0", DBPath: "x",
		TLSMode: TLSModeCert, TLSCert: certPath, TLSKey: keyPath,
	}
	srv, err := NewWithStore(cfg, store, nil)
	if err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	keyPair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadX509KeyPair: %v", err)
	}
	tlsLn := tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{keyPair}, MinVersion: tls.VersionTLS12})
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = hs.Serve(tlsLn) }()
	defer func() { _ = hs.Close() }()

	client := &http.Client{
		Transport: &http.Transport{
			// Connect to the bound address, but present SNI/ServerName routeup.dev
			// so the wildcard cert verifies.
			TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "routeup.dev"},
		},
	}
	url := "https://" + ln.Addr().String() + PathHealth
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Host = "routeup.dev"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("https GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !contains(string(body), "\"status\":\"ok\"") {
		t.Errorf("body = %s", body)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestStaticCertManager confirms cert mode loads a keypair and serves it.
func TestStaticCertManager(t *testing.T) {
	certPath, keyPath, _ := writeWildcardCert(t)
	srv, err := New(ServerConfig{
		Domain: "routeup.dev", Listen: ":0", DBPath: "x",
		TLSMode: TLSModeCert, TLSCert: certPath, TLSKey: keyPath,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cm, err := srv.buildCertManager(context.Background())
	if err != nil {
		t.Fatalf("buildCertManager: %v", err)
	}
	if len(cm.TLSConfig().Certificates) != 1 {
		t.Errorf("got %d certs, want 1", len(cm.TLSConfig().Certificates))
	}
	cm.EnsureNamespace(context.Background(), "mukul.routeup.dev") // no-op, must not panic
}

// TestACMECertManager_RequiresToken confirms acme mode fails clearly without a
// Cloudflare token.
func TestACMECertManager_RequiresToken(t *testing.T) {
	t.Setenv(CloudflareTokenEnv, "")
	srv, err := New(ServerConfig{
		Domain: "routeup.dev", Listen: ":0", DBPath: "x", TLSMode: TLSModeACME,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.buildCertManager(context.Background()); err == nil ||
		!contains(err.Error(), CloudflareTokenEnv) {
		t.Errorf("err = %v, want it to mention %s", err, CloudflareTokenEnv)
	}
}
