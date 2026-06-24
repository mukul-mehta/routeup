package agentctl

import "time"

// Timeouts and budgets for agent IPC, named in one place rather than scattered
// as literals across the package.
//
// Control calls (status, register, list, unregister, shutdown) are bounded by
// each call's context — the HTTP client sets no timeout of its own, so the
// caller's deadline governs. The values below cover the two cases a context
// can't: a generous default for the slow expose handshake when the caller
// passed no deadline, and the fixed budgets for the spawn/stop polling loops.
const (
	// tunnelHandshakeTimeout bounds POST /v1/expose when the caller's context
	// has no deadline. Establishing a tunnel (dial + claim round trip) is
	// slower than an ordinary control call, so it gets its own default.
	tunnelHandshakeTimeout = 60 * time.Second

	// agentReadyBudget is how long spawnAndWait polls a freshly spawned agent
	// for its first /v1/status response.
	agentReadyBudget = 5 * time.Second

	// agentStopBudget is how long Stop waits for a shutting-down agent to
	// stop answering (and a PID-signalled agent to exit).
	agentStopBudget = 5 * time.Second

	// readinessProbeTimeout bounds a single /v1/status probe inside the
	// ready/stop polling loops; readinessProbeInterval is the gap between them.
	readinessProbeTimeout  = 200 * time.Millisecond
	readinessProbeInterval = 100 * time.Millisecond
)
