//go:build linux

package certs

import (
	"slices"
	"testing"
)

func TestSelectLinuxTrustStore(t *testing.T) {
	tests := []struct {
		name    string
		present []string // anchor dirs that "exist"
		wantDir string   // "" means an error is expected
		wantCmd string
		wantArg []string
	}{
		{
			name:    "rhel family",
			present: []string{"/etc/pki/ca-trust/source/anchors"},
			wantDir: "/etc/pki/ca-trust/source/anchors",
			wantCmd: "update-ca-trust",
			wantArg: []string{"extract"},
		},
		{
			name:    "debian family",
			present: []string{"/usr/local/share/ca-certificates"},
			wantDir: "/usr/local/share/ca-certificates",
			wantCmd: "update-ca-certificates",
		},
		{
			name:    "arch family",
			present: []string{"/etc/ca-certificates/trust-source/anchors"},
			wantDir: "/etc/ca-certificates/trust-source/anchors",
			wantCmd: "trust",
			wantArg: []string{"extract-compat"},
		},
		{
			name: "rhel wins when several anchor dirs coexist",
			present: []string{
				"/etc/ca-certificates/trust-source/anchors",
				"/etc/pki/ca-trust/source/anchors",
			},
			wantDir: "/etc/pki/ca-trust/source/anchors",
			wantCmd: "update-ca-trust",
			wantArg: []string{"extract"},
		},
		{
			name:    "no known anchor dir",
			present: nil,
			wantDir: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists := func(p string) bool { return slices.Contains(tt.present, p) }

			got, err := selectLinuxTrustStore(linuxTrustStores, exists)
			if tt.wantDir == "" {
				if err == nil {
					t.Fatalf("expected error for no anchor dir, got plan %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.anchorDir != tt.wantDir {
				t.Errorf("anchorDir = %q, want %q", got.anchorDir, tt.wantDir)
			}
			if got.refreshCmd != tt.wantCmd {
				t.Errorf("refreshCmd = %q, want %q", got.refreshCmd, tt.wantCmd)
			}
			if !slices.Equal(got.refreshArgs, tt.wantArg) {
				t.Errorf("refreshArgs = %v, want %v", got.refreshArgs, tt.wantArg)
			}
		})
	}
}
