package state

// Path and environment constants for routeup's per-user runtime state.
const (
	StateDirName    = ".routeup"
	XDGSubdir       = "routeup"
	AgentSocketName = "agent.sock"
	AgentLogName    = "agent.log"
	AgentPIDName    = "agent.pid"
)

const (
	AgentSocketEnv = "ROUTEUP_AGENT_SOCKET"
	XDGRuntimeEnv  = "XDG_RUNTIME_DIR"
)
