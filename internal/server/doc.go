// Package server implements the routeup public server: token storage and
// verification, route-claim authorization against token allow patterns,
// reserved-subdomain enforcement, the public-namespace path, the persisted
// claim/grace state machine, and the tunnel ingress that routes public
// requests to connected agents.
//
// The allow, reserved, and host files hold the pure, I/O-free authorization
// primitives the rest of the server builds on.
package server
