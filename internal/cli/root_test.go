package cli

import (
	"bytes"
	"strings"
	"testing"
)

func runRoot(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	root := newRootCmd()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)

	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestRoot_Version(t *testing.T) {
	stdout, _, err := runRoot(t, "--version")
	if err != nil {
		t.Fatalf("--version returned error: %v", err)
	}

	const want = "0.0.0-dev"
	if !strings.Contains(stdout, want) {
		t.Errorf("--version output missing %q\n--- got ---\n%s", want, stdout)
	}
}

func TestStubSubcommands(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
	}{
		{name: "doctor", cmd: "doctor"},
		{name: "routes", cmd: "routes"},
		{name: "logs", cmd: "logs"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runRoot(t, tc.cmd)
			if err != nil {
				t.Fatalf("%s returned error: %v\nstderr: %s", tc.cmd, err, stderr)
			}

			want := tc.cmd + ": not implemented yet"
			if !strings.Contains(stdout, want) {
				t.Errorf("%s output missing %q\n--- got ---\n%s", tc.cmd, want, stdout)
			}
		})
	}
}
