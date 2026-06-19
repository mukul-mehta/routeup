// Package route models route names and their hostname mappings.
//
// A route name is a dotted, DNS-label-style identifier (e.g. "api.myapp") that
// the rest of routeup uses as the core domain object. Parse normalizes and
// validates input; LocalHost and PublicHost produce the local and public
// hostnames by appending the configured suffix constants.
package route

// LocalSuffix is the hostname suffix appended to route names for local routing.
// "localhost" is reserved by RFC 6761; resolvers short-circuit it to 127.0.0.1
// without any DNS plumbing.
const LocalSuffix = "localhost"

// PublicSuffix is the hostname suffix appended to route names for public routing
// via the routeup public server.
const PublicSuffix = "routeup.dev"
