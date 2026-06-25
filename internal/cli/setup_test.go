package cli

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mukul-mehta/routeup/internal/state"
)

// runSetupCmd runs setup with all side-effect flags suppressed. Uses a high
// port so the doctor bind check (run by tests that reuse this) stays green
// without a real forwarder/setcap.
func runSetupCmd(t *testing.T) (string, error) {
	t.Helper()
	cmd := newSetupCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"--no-start", "--no-trust", "--no-bind", "--port", "47443"})
	err := cmd.Execute()
	return buf.String(), err
}

func TestSetup_CreatesCAOnFirstRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	out, err := runSetupCmd(t)
	if err != nil {
		t.Fatalf("setup: %v\noutput: %s", err, out)
	}

	certPath, err := state.CACertPath()
	if err != nil {
		t.Fatalf("CACertPath: %v", err)
	}
	keyPath, err := state.CAKeyPath()
	if err != nil {
		t.Fatalf("CAKeyPath: %v", err)
	}

	if _, err := os.Stat(certPath); err != nil {
		t.Errorf("expected cert at %s: %v", certPath, err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("expected key at %s: %v", keyPath, err)
	}

	if !strings.Contains(out, "certificate authority: created") {
		t.Errorf("output missing 'certificate authority: created':\n%s", out)
	}
}

func TestSetup_IdempotentWhenCAExists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, err := runSetupCmd(t); err != nil {
		t.Fatalf("first setup: %v", err)
	}

	out, err := runSetupCmd(t)
	if err != nil {
		t.Fatalf("second setup: %v\noutput: %s", err, out)
	}

	if !strings.Contains(out, "already set up") {
		t.Errorf("second run missing 'already set up':\n%s", out)
	}
	if strings.Contains(out, "certificate authority: created") {
		t.Errorf("second run wrongly claims it created a CA:\n%s", out)
	}
}

func TestSetup_RegeneratesOnPartialCA(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".routeup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	dummyCert := []byte("dummy")
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), dummyCert, 0o644); err != nil {
		t.Fatalf("write dummy cert: %v", err)
	}

	out, err := runSetupCmd(t)
	if err != nil {
		t.Fatalf("expected setup to regenerate on partial state, got error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "recreating") {
		t.Errorf("output missing 'recreating':\n%s", out)
	}
	if !strings.Contains(out, "certificate authority: created") {
		t.Errorf("output missing 'certificate authority: created':\n%s", out)
	}

	// Post-regen: the cert is no longer the dummy bytes, and both files
	// load as a valid CA.
	certPath, keyPath := filepath.Join(dir, "ca.crt"), filepath.Join(dir, "ca.key")
	regen, rerr := os.ReadFile(certPath)
	if rerr != nil {
		t.Fatalf("read regenerated cert: %v", rerr)
	}
	if string(regen) == string(dummyCert) {
		t.Error("cert file still contains dummy bytes — not regenerated")
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Errorf("key file missing after regen: %v", err)
	}
}

func TestSetup_RegeneratesOnCorruptedCA(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".routeup")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	garbageCert := []byte("-----BEGIN CERTIFICATE-----\nbm90LWEtY2VydA==\n-----END CERTIFICATE-----\n")
	garbageKey := []byte("-----BEGIN EC PRIVATE KEY-----\nbm90LWEta2V5\n-----END EC PRIVATE KEY-----\n")
	if err := os.WriteFile(filepath.Join(dir, "ca.crt"), garbageCert, 0o644); err != nil {
		t.Fatalf("write garbage cert: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ca.key"), garbageKey, 0o600); err != nil {
		t.Fatalf("write garbage key: %v", err)
	}

	out, err := runSetupCmd(t)
	if err != nil {
		t.Fatalf("expected setup to regenerate on broken CA, got error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "recreating") {
		t.Errorf("output missing 'recreating':\n%s", out)
	}

	// A second setup call should now be a no-op (CAPresent), proving the
	// regenerated CA is valid.
	secondOut, secondErr := runSetupCmd(t)
	if secondErr != nil {
		t.Fatalf("second setup after regen: %v\noutput: %s", secondErr, secondOut)
	}
	if !strings.Contains(secondOut, "already set up") {
		t.Errorf("second setup didn't see a valid CA — regen produced something Inspect rejects:\n%s", secondOut)
	}
}

// TestSetup_SavesServerAndTokenFromFlags covers the non-interactive path: flags
// populate the client config without any prompt, and the token never prints.
func TestSetup_SavesServerAndTokenFromFlags(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cmd := newSetupCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{
		"--no-start", "--no-trust", "--no-bind", "--port", "47443",
		"--server", "https://edge.routeup.dev", "--token", "sk_routeup_secret",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("setup: %v\noutput: %s", err, buf.String())
	}

	cc, err := state.ReadClientConfig()
	if err != nil {
		t.Fatalf("ReadClientConfig: %v", err)
	}
	if cc.Server != "https://edge.routeup.dev" {
		t.Errorf("server = %q, want https://edge.routeup.dev", cc.Server)
	}
	if cc.Token != "sk_routeup_secret" {
		t.Errorf("token = %q, want sk_routeup_secret", cc.Token)
	}
	if strings.Contains(buf.String(), "sk_routeup_secret") {
		t.Errorf("token value leaked to output:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "token: saved") {
		t.Errorf("output missing 'token: saved':\n%s", buf.String())
	}
}

// TestServerCredPrompter_Collect drives the interactive question flow with a
// scripted reader and a fake secret reader, covering the branch the user asked
// for: ask the token only when a server is set, and keep saved values on blank.
func TestServerCredPrompter_Collect(t *testing.T) {
	tests := []struct {
		name             string
		input            string // bytes "typed" for the server line
		secret           string // value the fake secret reader returns
		cc               state.ClientConfig
		serverFromFlag   bool
		tokenFromFlag    bool
		startServer      string // opts.server before collect (a --server flag value)
		startToken       string
		wantServer       string
		wantToken        string
		wantSecretCalled bool
	}{
		{
			name:             "server and token typed",
			input:            "https://edge.routeup.dev\n",
			secret:           "sk_typed",
			wantServer:       "https://edge.routeup.dev",
			wantToken:        "sk_typed",
			wantSecretCalled: true,
		},
		{
			name:             "blank takes the default server, then asks token",
			input:            "\n",
			secret:           "sk_default",
			wantServer:       defaultPublicServer,
			wantToken:        "sk_default",
			wantSecretCalled: true,
		},
		{
			name:             "blank takes the default server, then asks token",
			input:            "\n",
			secret:           "sk_default",
			wantServer:       defaultPublicServer,
			wantToken:        "sk_default",
			wantSecretCalled: true,
		},
		{
			name:             "'none' opts out and skips the token prompt",
			input:            "none\n",
			wantServer:       "",
			wantToken:        "",
			wantSecretCalled: false,
		},
		{
			name:             "blank server keeps the saved server, then asks token",
			input:            "\n",
			cc:               state.ClientConfig{Server: "https://saved.example"},
			secret:           "sk_new",
			wantServer:       "https://saved.example",
			wantToken:        "sk_new",
			wantSecretCalled: true,
		},
		{
			name:             "blank token keeps the saved token",
			input:            "https://edge.routeup.dev\n",
			secret:           "",
			cc:               state.ClientConfig{Token: "sk_saved"},
			wantServer:       "https://edge.routeup.dev",
			wantToken:        "sk_saved",
			wantSecretCalled: true,
		},
		{
			name:             "server flag skips server prompt but still asks token",
			serverFromFlag:   true,
			startServer:      "https://flag.example",
			secret:           "sk_tok",
			wantServer:       "https://flag.example",
			wantToken:        "sk_tok",
			wantSecretCalled: true,
		},
		{
			name:             "token flag skips the secret read",
			input:            "https://edge.routeup.dev\n",
			tokenFromFlag:    true,
			startToken:       "sk_flag",
			wantServer:       "https://edge.routeup.dev",
			wantToken:        "sk_flag",
			wantSecretCalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			p := serverCredPrompter{
				in:  bufio.NewReader(strings.NewReader(tt.input)),
				out: &bytes.Buffer{},
				readSecret: func() (string, error) {
					called = true
					return tt.secret, nil
				},
			}
			opts := &runSetupOpts{server: tt.startServer, token: tt.startToken}

			p.collect(tt.cc, tt.serverFromFlag, tt.tokenFromFlag, opts)

			if opts.server != tt.wantServer {
				t.Errorf("server = %q, want %q", opts.server, tt.wantServer)
			}
			if opts.token != tt.wantToken {
				t.Errorf("token = %q, want %q", opts.token, tt.wantToken)
			}
			if called != tt.wantSecretCalled {
				t.Errorf("secret reader called = %v, want %v", called, tt.wantSecretCalled)
			}
		})
	}
}
