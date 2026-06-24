package server

// ControlPrefix namespaces all control-plane endpoints. Requests under this
// path prefix are control (health and the tunnel endpoint); everything else is
// public ingress routed by Host. Using a path prefix (rather than routing by
// Host) lets the server's own control hostname be an ordinary subdomain of the
// public domain without being mistaken for tunnel traffic.
const ControlPrefix = "/_routeup"

// Control-plane paths.
const PathHealth = ControlPrefix + "/v1/health"

// HealthResponse is the body of GET /v1/health.
type HealthResponse struct {
	Status          string `json:"status"`
	Domain          string `json:"domain"`
	PublicNamespace string `json:"public_namespace,omitempty"`
}
