//go:build darwin

package privbind

import (
	"bytes"
	"os"
	"testing"

	"github.com/mukul-mehta/routeup/internal/ipc"
)

func TestValidateForwarderPlist(t *testing.T) {
	plist := renderPlist(helperPath, 443, ipc.DefaultTLSPort)
	if err := validateForwarderPlist(plist, 443); err != nil {
		t.Fatalf("current plist rejected: %v", err)
	}

	ipv4Only := bytes.Replace(plist, []byte("        <string>[::1]:443</string>\n"), nil, 1)
	if err := validateForwarderPlist(ipv4Only, 443); err == nil {
		t.Fatal("IPv4-only plist accepted")
	}

	wrongBinary := renderPlist("/another/routeup", 443, ipc.DefaultTLSPort)
	if err := validateForwarderPlist(wrongBinary, 443); err == nil {
		t.Fatal("plist pointing at another binary accepted")
	}
}

func TestValidateHelperFileRejectsUserOwnedBinary(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("test requires a non-root user")
	}
	path := t.TempDir() + "/helper"
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRootOwnedFile(info); err == nil {
		t.Fatal("user-owned helper accepted")
	}
}

func TestSameFileContents(t *testing.T) {
	dir := t.TempDir()
	first, second := dir+"/first", dir+"/second"
	if err := os.WriteFile(first, []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("same"), 0o600); err != nil {
		t.Fatal(err)
	}
	match, err := sameFileContents(first, second)
	if err != nil || !match {
		t.Fatalf("sameFileContents = %t, %v", match, err)
	}
	if err := os.WriteFile(second, []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	match, err = sameFileContents(first, second)
	if err != nil || match {
		t.Fatalf("different files = %t, %v", match, err)
	}
}
