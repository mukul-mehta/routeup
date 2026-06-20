package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestUninstall_CancelsOnNo exercises the confirmation guard without
// touching the system: answering "n" must bail before any teardown.
func TestUninstall_CancelsOnNo(t *testing.T) {
	cmd := newUninstallCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader("n\n"))
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("uninstall: %v\noutput: %s", err, out.String())
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("expected 'cancelled', got:\n%s", out.String())
	}
}
