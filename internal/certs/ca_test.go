package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// caPaths returns fresh cert+key paths under a per-test temp dir.
func caPaths(t *testing.T) (certPath, keyPath string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
}

// writeExpiredCA writes a CA whose NotAfter is in the past. Used by the
// expired-CA branch of Inspect. Lives in the test file because production
// code never produces expired CAs intentionally.
func writeExpiredCA(t *testing.T, certPath, keyPath string) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("serial: %v", err)
	}
	past := time.Now().Add(-365 * 24 * time.Hour)
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "expired-test-ca"},
		NotBefore:             past.Add(-time.Hour),
		NotAfter:              past,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}

func TestCreate_WritesFilesWithCorrectPerms(t *testing.T) {
	certPath, keyPath := caPaths(t)

	if _, err := Create(certPath, keyPath); err != nil {
		t.Fatalf("Create: %v", err)
	}

	certInfo, err := os.Stat(certPath)
	if err != nil {
		t.Fatalf("stat cert: %v", err)
	}
	if got, want := certInfo.Mode().Perm(), os.FileMode(0o644); got != want {
		t.Errorf("cert perms = %v, want %v", got, want)
	}

	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if got, want := keyInfo.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("key perms = %v, want %v", got, want)
	}
}

func TestLoad_Roundtrip(t *testing.T) {
	certPath, keyPath := caPaths(t)

	created, err := Create(certPath, keyPath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	loaded, err := Load(certPath, keyPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if created.Cert.SerialNumber.Cmp(loaded.Cert.SerialNumber) != 0 {
		t.Errorf("serial mismatch: created=%s loaded=%s",
			created.Cert.SerialNumber, loaded.Cert.SerialNumber)
	}
	if !created.Cert.NotAfter.Equal(loaded.Cert.NotAfter) {
		t.Errorf("NotAfter mismatch: created=%v loaded=%v",
			created.Cert.NotAfter, loaded.Cert.NotAfter)
	}
	if !created.Key.Equal(loaded.Key) {
		t.Errorf("private key mismatch across Create/Load")
	}
}

func TestLoad_MissingReturnsErrNotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(filepath.Join(dir, "missing.crt"), filepath.Join(dir, "missing.key"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Load on missing files: err = %v, want wrapping os.ErrNotExist", err)
	}
}

func TestCA_Shape(t *testing.T) {
	certPath, keyPath := caPaths(t)
	ca, err := Create(certPath, keyPath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !ca.Cert.IsCA {
		t.Error("IsCA = false, want true")
	}
	if ca.Cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("KeyUsageCertSign not set")
	}
	if !ca.Cert.BasicConstraintsValid {
		t.Error("BasicConstraintsValid = false, want true")
	}
	if !strings.HasPrefix(ca.Cert.Subject.CommonName, caCommonName) {
		t.Errorf("CN = %q, want prefix %q", ca.Cert.Subject.CommonName, caCommonName)
	}
	if len(ca.Cert.Subject.Organization) == 0 || ca.Cert.Subject.Organization[0] != caOrganization {
		t.Errorf("O = %v, want [%q]", ca.Cert.Subject.Organization, caOrganization)
	}

	expected := time.Now().Add(caValidity)
	if delta := ca.Cert.NotAfter.Sub(expected); delta > time.Hour || delta < -time.Hour {
		t.Errorf("NotAfter = %v, expected ~%v (delta %v)", ca.Cert.NotAfter, expected, delta)
	}
}

func TestCreate_HasNonZeroSerial(t *testing.T) {
	certPath, keyPath := caPaths(t)
	ca, err := Create(certPath, keyPath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ca.Cert.SerialNumber.Sign() == 0 {
		t.Error("serial number = 0; want random 128-bit value")
	}
}

func TestVerifyTrust_FreshCAIsUntrusted(t *testing.T) {
	certPath, keyPath := caPaths(t)
	if _, err := Create(certPath, keyPath); err != nil {
		t.Fatalf("Create: %v", err)
	}
	trusted, err := VerifyTrust(certPath)
	if err != nil {
		t.Fatalf("VerifyTrust: %v", err)
	}
	if trusted {
		t.Error("freshly-created CA reported trusted; impossible without prior install")
	}
}

func TestInspect_Absent(t *testing.T) {
	certPath, keyPath := caPaths(t)
	state, ca, err := Inspect(certPath, keyPath)
	if state != CAAbsent {
		t.Errorf("state = %v, want CAAbsent", state)
	}
	if ca != nil {
		t.Errorf("ca = %v, want nil", ca)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestInspect_PartialCertOnly(t *testing.T) {
	certPath, keyPath := caPaths(t)
	if err := os.WriteFile(certPath, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	state, ca, err := Inspect(certPath, keyPath)
	if state != CAPartial {
		t.Errorf("state = %v, want CAPartial", state)
	}
	if ca != nil {
		t.Errorf("ca = %v, want nil", ca)
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestInspect_PartialKeyOnly(t *testing.T) {
	certPath, keyPath := caPaths(t)
	if err := os.WriteFile(keyPath, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	state, _, _ := Inspect(certPath, keyPath)
	if state != CAPartial {
		t.Errorf("state = %v, want CAPartial", state)
	}
}

func TestInspect_Present(t *testing.T) {
	certPath, keyPath := caPaths(t)
	if _, err := Create(certPath, keyPath); err != nil {
		t.Fatalf("Create: %v", err)
	}

	state, ca, err := Inspect(certPath, keyPath)
	if state != CAPresent {
		t.Errorf("state = %v, want CAPresent", state)
	}
	if ca == nil {
		t.Error("ca = nil, want loaded CA")
	}
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}

func TestInspect_BrokenParse(t *testing.T) {
	certPath, keyPath := caPaths(t)
	garbageCert := []byte("-----BEGIN CERTIFICATE-----\nbm90LWEtY2VydA==\n-----END CERTIFICATE-----\n")
	garbageKey := []byte("-----BEGIN EC PRIVATE KEY-----\nbm90LWEta2V5\n-----END EC PRIVATE KEY-----\n")
	if err := os.WriteFile(certPath, garbageCert, 0o644); err != nil {
		t.Fatalf("write garbage cert: %v", err)
	}
	if err := os.WriteFile(keyPath, garbageKey, 0o600); err != nil {
		t.Fatalf("write garbage key: %v", err)
	}

	state, ca, err := Inspect(certPath, keyPath)
	if state != CABroken {
		t.Errorf("state = %v, want CABroken", state)
	}
	if ca != nil {
		t.Errorf("ca = %v, want nil for parse-failure CABroken", ca)
	}
	if err == nil {
		t.Fatal("err = nil, want load error")
	}
	if errors.Is(err, ErrCAExpired) {
		t.Errorf("err unexpectedly wraps ErrCAExpired: %v", err)
	}
}

func TestInspect_BrokenExpired(t *testing.T) {
	certPath, keyPath := caPaths(t)
	writeExpiredCA(t, certPath, keyPath)

	state, ca, err := Inspect(certPath, keyPath)
	if state != CABroken {
		t.Errorf("state = %v, want CABroken", state)
	}
	if ca == nil {
		t.Error("ca = nil, want parsed CA returned even when expired")
	}
	if err == nil {
		t.Fatal("err = nil, want ErrCAExpired wrap")
	}
	if !errors.Is(err, ErrCAExpired) {
		t.Errorf("err does not wrap ErrCAExpired: %v", err)
	}
}
