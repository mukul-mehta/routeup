// Package ipc holds the wire types the routeup CLI and agent exchange over the
// control socket: the JSON messages, the typed errors, and the shared path and
// address constants.
//
// It has no behavior. Both the agent and the CLI client import it, which keeps
// them from importing each other.
package ipc

import (
	"fmt"
	"time"

	"github.com/mukul-mehta/routeup/internal/route"
)

// DefaultTLSAddr/Port: the agent's internal high-port TLS listener. Chosen
// to dodge common alt-HTTPS ports (7443/8443) and sit below the ephemeral
// range so the OS won't reuse it as an outbound source port.
// DefaultUserPort: the user-facing HTTPS port (what URLs default to).
const (
	DefaultTLSAddr  = "127.0.0.1:47443"
	DefaultTLSPort  = 47443
	DefaultUserPort = 443
)

// Control-plane paths, versioned under /v1/. Shared so the handler routes and
// the client requests can never drift apart.
const (
	PathStatus   = "/v1/status"
	PathRoutes   = "/v1/routes"
	PathShutdown = "/v1/shutdown"
	PathExpose   = "/v1/expose"
	PathUnexpose = "/v1/unexpose"
)

// Claim is one active route registration. The same shape is used for the
// agent's in-memory registry record and the JSON body on /v1/routes.
type Claim struct {
	Name         string         `json:"name"`
	Port         int            `json:"port,omitempty"`
	Targets      []route.Target `json:"targets,omitempty"`
	OwnerPID     int            `json:"owner_pid"`
	OwnerCWD     string         `json:"owner_cwd"`
	RegisteredAt time.Time      `json:"registered_at"`

	// PublicHost is the granted public host when this route is also exposed
	// through a tunnel by the same owner process. Response-only: the agent
	// fills it on GET /v1/routes by joining live tunnels on OwnerPID;
	// registration ignores it.
	PublicHost  string   `json:"public_host,omitempty"`
	PublicPaths []string `json:"public_paths,omitempty"`
}

// Status is the response shape for GET /v1/status.
//
// BootID is a random identifier generated once per agent process at startup.
// A change in BootID tells the CLI the agent restarted (and therefore lost its
// in-memory registry), which is the signal the serve reconcile loop uses to
// re-register.
//
// ExecPath and ExecModTime describe the binary the running agent was launched
// from, captured at startup; the CLI compares them (plus Version) to decide
// whether a running agent is a stale build.
type Status struct {
	Version       string    `json:"version"`
	UptimeSeconds int64     `json:"uptime_seconds"`
	TLSAddr       string    `json:"tls_addr,omitempty"`
	BootID        string    `json:"boot_id"`
	ExecPath      string    `json:"exec_path,omitempty"`
	ExecModTime   time.Time `json:"exec_mod_time"`
}

// ConflictError means a route name is already held by a different, still-alive
// process. The CLI prints it directly, so its fields are what a user sees when
// two invocations collide.
type ConflictError struct {
	Name     string
	Existing Claim
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("route %q is already claimed (pid %d, cwd %s)",
		e.Name, e.Existing.OwnerPID, e.Existing.OwnerCWD)
}

// ErrorBody is the JSON shape returned by the API for 4xx/5xx responses,
// encoded by the agent and decoded by the client.
type ErrorBody struct {
	Error    string `json:"error"`
	OwnerPID int    `json:"owner_pid,omitempty"`
	OwnerCWD string `json:"owner_cwd,omitempty"`
}

// ExposeRequest asks the agent to open a public tunnel for a route. The agent
// dials Server, authenticates with Token, claims the route, and forwards
// inbound public requests to the local Port. OwnerPID lets the agent reap the
// tunnel if the requesting CLI dies. A --random name is resolved by the CLI
// before this request, so Name is always a concrete label here.
type ExposeRequest struct {
	Name     string         `json:"name"`
	Port     int            `json:"port,omitempty"`
	Targets  []route.Target `json:"targets,omitempty"`
	Paths    []string       `json:"paths,omitempty"`
	Server   string         `json:"server"`
	Token    string         `json:"token,omitempty"`
	OwnerPID int            `json:"owner_pid"`
}

// ExposeResponse is the agent's reply: the public host the server granted.
type ExposeResponse struct {
	Host string `json:"host"`
}

// UnexposeRequest tears down a public tunnel by its granted host.
type UnexposeRequest struct {
	Host string `json:"host"`
}
