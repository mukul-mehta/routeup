package state

// Path and environment constants for routeup's per-user runtime state.
const (
	StateDirName     = ".routeup"
	XDGSubdir        = "routeup"
	AgentSocketName  = "agent.sock"
	AgentLogName     = "agent.log"
	AgentPIDName     = "agent.pid"
	CACertName       = "ca.crt"
	CAKeyName        = "ca.key"
	ClientConfigName = "client.json"
)

const (
	AgentSocketEnv = "ROUTEUP_AGENT_SOCKET"
	XDGRuntimeEnv  = "XDG_RUNTIME_DIR"
)
