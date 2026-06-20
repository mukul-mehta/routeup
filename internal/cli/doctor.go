package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/mukul-mehta/routeup/internal/agentctl"
	"github.com/mukul-mehta/routeup/internal/certs"
	"github.com/mukul-mehta/routeup/internal/privbind"
	"github.com/mukul-mehta/routeup/internal/state"
)

type checkLevel int

const (
	checkOK checkLevel = iota
	checkWarn
	checkFail
)

func (l checkLevel) label() string {
	switch l {
	case checkOK:
		return "[ok]"
	case checkWarn:
		return "[warn]"
	case checkFail:
		return "[fail]"
	}
	return "[unknown]"
}

type checkResult struct {
	level checkLevel
	msg   string
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check that routeup is set up correctly",
		Long: "Read-only checks against the local routeup state:\n" +
			"  - is the local CA present and not near expiry?\n" +
			"  - is the CA trusted by the OS?\n" +
			"  - is port 443 (or your chosen port) set up?\n" +
			"  - is the agent reachable?\n\n" +
			"Exits 0 if every check is ok or warn, non-zero if any check fails.",
		Args: cobra.NoArgs,
		RunE: runDoctor,
	}
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	caRes, ca, certPath := checkCA()
	trustRes := checkTrust(ca, certPath)
	bindRes := checkBind()
	agentRes := checkAgent(cmd)

	checks := []checkResult{caRes, trustRes, bindRes, agentRes}

	anyFail := false
	for _, c := range checks {
		_, _ = fmt.Fprintf(out, "  %-6s %s\n", c.level.label(), c.msg)
		if c.level == checkFail {
			anyFail = true
		}
	}
	if anyFail {
		return errors.New("one or more checks failed")
	}
	return nil
}

// checkCA returns (result, ca, certPath). ca is non-nil for OK/Warn.
func checkCA() (checkResult, *certs.CA, string) {
	certPath, err := state.CACertPath()
	if err != nil {
		return checkResult{checkFail, fmt.Sprintf("resolve CA path: %v", err)}, nil, ""
	}
	keyPath, err := state.CAKeyPath()
	if err != nil {
		return checkResult{checkFail, fmt.Sprintf("resolve CA key path: %v", err)}, nil, certPath
	}

	caState, ca, inspectErr := certs.Inspect(certPath, keyPath)
	switch caState {
	case certs.CAAbsent:
		return checkResult{checkFail, fmt.Sprintf("no local CA at %s — run `routeup setup`", certPath)}, nil, certPath

	case certs.CAPartial:
		return checkResult{checkFail, fmt.Sprintf("partial CA state (cert or key missing) at %s, %s — delete both and run `routeup setup`",
			certPath, keyPath)}, nil, certPath

	case certs.CABroken:
		if errors.Is(inspectErr, certs.ErrCAExpired) && ca != nil {
			return checkResult{checkFail, fmt.Sprintf("local CA expired on %s — delete %s and %s, then run `routeup setup`",
				ca.Cert.NotAfter.Format("2006-01-02"), certPath, keyPath)}, nil, certPath
		}
		return checkResult{checkFail, fmt.Sprintf("local CA at %s is unreadable: %v — run `routeup setup` to regenerate",
			certPath, inspectErr)}, nil, certPath
	}

	// CAPresent. 30-day expiry warning is a soft status.
	until := time.Until(ca.Cert.NotAfter)
	days := int(until / (24 * time.Hour))
	if days < 30 {
		return checkResult{checkWarn, fmt.Sprintf("local CA at %s expires in %d days (%s) — consider re-running `routeup setup`",
			certPath, days, ca.Cert.NotAfter.Format("2006-01-02"))}, ca, certPath
	}
	return checkResult{checkOK, fmt.Sprintf("local CA at %s (expires %s)",
		certPath, ca.Cert.NotAfter.Format("2006-01-02"))}, ca, certPath
}

// checkTrust reports OS trust status. Untrusted = warn (--cacert still works).
func checkTrust(ca *certs.CA, certPath string) checkResult {
	if ca == nil {
		return checkResult{checkWarn, "trust: skipped (no usable CA)"}
	}
	trusted, err := certs.VerifyTrust(certPath)
	if err != nil {
		return checkResult{checkWarn, fmt.Sprintf("trust probe failed: %v", err)}
	}
	if !trusted {
		return checkResult{checkWarn, "local CA not in OS trust store — run `routeup setup` to install"}
	}
	return checkResult{checkOK, "local CA trusted in OS trust store"}
}

// checkBind reports whether the port-binding machinery is healthy. Reads the
// configured port and binary from the setup marker.
func checkBind() checkResult {
	port := state.TLSPortOrDefault()
	binPath := ""
	if m, err := state.ReadSetupMarker(); err == nil && m != nil {
		binPath = m.BinPath
	}
	h, msg := privbind.Check(port, binPath)
	switch h {
	case privbind.HealthOK:
		return checkResult{checkOK, msg}
	case privbind.HealthWarn:
		return checkResult{checkWarn, msg}
	default:
		return checkResult{checkFail, msg}
	}
}

// checkAgent reports reachability. Offline = warn (agent starts on demand).
func checkAgent(cmd *cobra.Command) checkResult {
	sockPath, err := state.AgentSocketPath()
	if err != nil {
		return checkResult{checkFail, fmt.Sprintf("resolve agent socket path: %v", err)}
	}
	client := agentctl.NewClient(sockPath, "", cmd.Root().Version)

	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()

	status, err := client.Status(ctx)
	if err != nil {
		return checkResult{checkWarn, "agent not running (it starts on demand via `routeup serve`)"}
	}
	return checkResult{checkOK, fmt.Sprintf("agent running (version %s, uptime %ds)",
		status.Version, status.UptimeSeconds)}
}
