package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
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
	t.Setenv(state.StateDirEnv, "")
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

	if !strings.Contains(out, "certificate authority") || !strings.Contains(out, "created") {
		t.Errorf("output missing certificate authority creation:\n%s", out)
	}
	marker, err := state.ReadSetupMarker()
	if err != nil {
		t.Fatalf("ReadSetupMarker: %v", err)
	}
	if marker.Version != state.CurrentSetupVersion {
		t.Errorf("setup marker version = %d, want %d", marker.Version, state.CurrentSetupVersion)
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
	if !strings.Contains(out, "certificate authority") || !strings.Contains(out, "created") {
		t.Errorf("output missing certificate authority creation:\n%s", out)
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
	if !strings.Contains(buf.String(), "token") || !strings.Contains(buf.String(), "saved") {
		t.Errorf("output missing token saved confirmation:\n%s", buf.String())
	}
}

func TestSetup_RejectsNoBindForPrivilegedPort(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	tests := []struct {
		name string
		args []string
	}{
		{name: "default port", args: []string{"--no-bind"}},
		{name: "explicit privileged port", args: []string{"--no-bind", "--port", "1023"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newSetupCmd()
			cmd.SetArgs(tt.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "--port 1024 or higher") {
				t.Fatalf("error = %v, want --port 1024 or higher", err)
			}

			certPath, pathErr := state.CACertPath()
			if pathErr != nil {
				t.Fatalf("CACertPath: %v", pathErr)
			}
			if _, statErr := os.Stat(certPath); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("CA created before validation: %v", statErr)
			}
		})
	}
}

func TestSetup_TrustFailureDoesNotWriteMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := state.WriteSetupMarker(&state.SetupMarker{Version: 1, TLSPort: 8443}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := newSetupCmd()
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--no-start", "--no-bind", "--port", "47443", "--server=", "--token="})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "trusting certificate") {
		t.Fatalf("error = %v, want trust failure", err)
	}
	assertNoSetupMarker(t)
}

func TestSetup_PrivBindFailureDoesNotWriteMarker(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := newSetupCmd()
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--no-start", "--no-trust", "--port", "443", "--server=", "--token="})

	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "setting up port 443") {
		t.Fatalf("error = %v, want port setup failure", err)
	}
	assertNoSetupMarker(t)
}

func assertNoSetupMarker(t *testing.T) {
	t.Helper()
	marker, err := state.ReadSetupMarker()
	if marker != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("setup marker = %#v, error = %v; want no marker", marker, err)
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
		wantClear        bool
		wantClearToken   bool
		wantSecretCalled bool
		wantSavedMask    bool
		wantErr          string
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
			wantClear:        true,
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
			cc:               state.ClientConfig{Server: "https://edge.routeup.dev", Token: "sk_saved"},
			wantServer:       "https://edge.routeup.dev",
			wantToken:        "sk_saved",
			wantSecretCalled: true,
			wantSavedMask:    true,
		},
		{
			name:             "none clears saved token but keeps server",
			input:            "https://edge.routeup.dev\n",
			secret:           "none",
			cc:               state.ClientConfig{Server: "https://edge.routeup.dev", Token: "sk_saved"},
			wantServer:       "https://edge.routeup.dev",
			wantClearToken:   true,
			wantSecretCalled: true,
			wantSavedMask:    true,
		},
		{
			name:             "blank token does not reuse token from another server",
			input:            "https://new.example\n",
			secret:           "",
			cc:               state.ClientConfig{Server: "https://old.example", Token: "sk_old"},
			wantServer:       "https://new.example",
			wantToken:        "",
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
		{
			name:          "token flag conflicts with interactive opt-out",
			input:         "none\n",
			tokenFromFlag: true,
			startToken:    "sk_flag",
			wantToken:     "sk_flag",
			wantErr:       "--token cannot be combined",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			out := &bytes.Buffer{}
			p := serverCredPrompter{
				in:  bufio.NewReader(strings.NewReader(tt.input)),
				out: out,
				readSecret: func() (string, error) {
					called = true
					return tt.secret, nil
				},
			}
			opts := &runSetupOpts{server: tt.startServer, token: tt.startToken}

			err := p.collect(tt.cc, tt.serverFromFlag, tt.tokenFromFlag, opts)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}

			if opts.server != tt.wantServer {
				t.Errorf("server = %q, want %q", opts.server, tt.wantServer)
			}
			if opts.token != tt.wantToken {
				t.Errorf("token = %q, want %q", opts.token, tt.wantToken)
			}
			if called != tt.wantSecretCalled {
				t.Errorf("secret reader called = %v, want %v", called, tt.wantSecretCalled)
			}
			if opts.clearClient != tt.wantClear {
				t.Errorf("clear client = %v, want %v", opts.clearClient, tt.wantClear)
			}
			if opts.clearToken != tt.wantClearToken {
				t.Errorf("clear token = %v, want %v", opts.clearToken, tt.wantClearToken)
			}
			if got := strings.Contains(out.String(), "["+savedTokenMask+"]"); got != tt.wantSavedMask {
				t.Errorf("saved token mask present = %v, want %v\noutput: %s", got, tt.wantSavedMask, out)
			}
			if tt.wantSavedMask && !strings.Contains(out.String(), "Press Enter to keep the saved token, or type 'none' to clear it") {
				t.Errorf("saved token prompt missing keep instruction: %s", out)
			}
			if tt.cc.Token != "" && strings.Contains(out.String(), tt.cc.Token) {
				t.Errorf("saved token leaked to output: %s", out)
			}
		})
	}
}

func TestReadMaskedInput(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSecret string
		wantOutput string
		wantErr    error
	}{
		{
			name:       "masks typed token",
			input:      "sk_routeup\r",
			wantSecret: "sk_routeup",
			wantOutput: "**********",
		},
		{
			name:       "backspace updates token and mask",
			input:      "abc\x7fd\n",
			wantSecret: "abd",
			wantOutput: "***\b \b*",
		},
		{
			name:       "control-u clears token and mask",
			input:      "abc\x15d\n",
			wantSecret: "d",
			wantOutput: "***\b \b\b \b\b \b*",
		},
		{
			name:       "control-c interrupts",
			input:      "abc\x03",
			wantOutput: "***",
			wantErr:    errTokenPromptInterrupted,
		},
		{
			name:       "control-d accepts typed token",
			input:      "abc\x04",
			wantSecret: "abc",
			wantOutput: "***",
		},
		{
			name:    "empty control-d returns EOF",
			input:   "\x04",
			wantErr: io.EOF,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			secret, err := readMaskedInput(strings.NewReader(tt.input), out)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if secret != tt.wantSecret {
				t.Errorf("secret = %q, want %q", secret, tt.wantSecret)
			}
			if out.String() != tt.wantOutput {
				t.Errorf("output = %q, want %q", out.String(), tt.wantOutput)
			}
			if tt.wantSecret != "" && strings.Contains(out.String(), tt.wantSecret) {
				t.Errorf("secret leaked to output: %q", out.String())
			}
		})
	}
}

func TestSaveClientCredsDoesNotCarryTokenAcrossServers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := state.WriteClientConfig(state.ClientConfig{Server: "https://old.example", Token: "sk_old"}); err != nil {
		t.Fatal(err)
	}
	if err := saveClientCreds(&bytes.Buffer{}, "https://new.example", "", false, false); err != nil {
		t.Fatal(err)
	}
	config, err := state.ReadClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Server != "https://new.example" || config.Token != "" {
		t.Fatalf("client config = %#v, want new server without old token", config)
	}

	if err := saveClientCreds(&bytes.Buffer{}, "", "", true, false); err != nil {
		t.Fatal(err)
	}
	config, err = state.ReadClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Server != "" || config.Token != "" {
		t.Fatalf("client config after clear = %#v", config)
	}
}

func TestSaveClientCredsClearsTokenWithoutServerIdentity(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := state.WriteClientConfig(state.ClientConfig{Token: "orphaned-token"}); err != nil {
		t.Fatal(err)
	}
	if err := saveClientCreds(&bytes.Buffer{}, "https://new.example", "", false, false); err != nil {
		t.Fatal(err)
	}
	config, err := state.ReadClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Server != "https://new.example" || config.Token != "" {
		t.Fatalf("client config = %#v, want new server without orphaned token", config)
	}
}

func TestSaveClientCredsClearsOnlyToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := state.WriteClientConfig(state.ClientConfig{Server: "https://saved.example", Token: "sk_saved"}); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	if err := saveClientCreds(out, "", "", false, true); err != nil {
		t.Fatal(err)
	}
	config, err := state.ReadClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Server != "https://saved.example" || config.Token != "" {
		t.Fatalf("client config = %#v, want saved server without token", config)
	}
	if !strings.Contains(out.String(), "token") || !strings.Contains(out.String(), "cleared") {
		t.Fatalf("output missing token cleared confirmation: %s", out)
	}
}

func TestSetupTokenNoneClearsOnlyToken(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := state.WriteClientConfig(state.ClientConfig{Server: "https://saved.example", Token: "sk_saved"}); err != nil {
		t.Fatal(err)
	}
	cmd := newSetupCmd()
	cmd.SetArgs([]string{"--no-start", "--no-trust", "--no-bind", "--port", "47443", "--token", "none"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	config, err := state.ReadClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.Server != "https://saved.example" || config.Token != "" {
		t.Fatalf("client config = %#v, want saved server without token", config)
	}
}

func TestSaveClientCredsRejectsTokenWithoutServer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := saveClientCreds(&bytes.Buffer{}, "", "orphaned-token", false, false)
	if err == nil || !strings.Contains(err.Error(), "requires a public server") {
		t.Fatalf("error = %v, want missing server error", err)
	}
}

func TestSetupRejectsTokenWhenClearingServer(t *testing.T) {
	cmd := newSetupCmd()
	cmd.SetArgs([]string{"--server", "none", "--token", "secret"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("error = %v, want conflicting clear/token error", err)
	}
}
