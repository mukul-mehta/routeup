// Package route models route names and their hostname mappings.
//
// A route name is a dotted, DNS-label-style identifier (e.g. "api.myapp") that
// the rest of routeup uses as the core domain object. Parse normalizes and
// validates input; LocalHost produces the local hostname. Public hosts are
// granted by a configured routeup server rather than derived in this package.
package route

// LocalSuffix is the hostname suffix appended to route names for local routing.
// "localhost" is reserved by RFC 6761; resolvers short-circuit it to 127.0.0.1
// without any DNS plumbing.
const LocalSuffix = "localhost"

// PublicSuffix is the hosted suffix rejected when users provide a route name;
// public hosts themselves may use this suffix or a self-hosted server's domain.
const PublicSuffix = "routeup.dev"
