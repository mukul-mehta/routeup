# Open Questions

This file tracks unresolved product, architecture, and engineering questions for `routeup`. Decided questions move to `PLAN.md` or `docs/ARCHITECTURE.md` and leave this file.

## How To Use This File

- Each question has a stable id (`OQ-NNN`) that can be referenced from code, commits, and other docs.
- `Status` is one of `open`, `leaning <option>`, or `deferred-to-phase-N`.
- Re-read this file at the start of each phase. Items not actionable for the next phase become `deferred-to-phase-N`.
- When an item is decided, the answer moves to `PLAN.md` or `docs/ARCHITECTURE.md` and the entry is removed here.
- IDs are stable. Resolved entries are deleted, not reused. Holes in the numbering are expected.

## Index

```txt
OQ-002  macOS port 443 strategy
OQ-003  Linux port 443 strategy
OQ-009  Agent autostart approach for Phase 5
OQ-010  Tunnel reconnect tuning surface
OQ-011  mDNS for same-LAN device testing
OQ-012  Server observability and metrics
OQ-013  Public server rate limiting
OQ-014  DNS provider for wildcard ACME
OQ-015  ACME library choice
OQ-016  Dual-stack loopback for the agent listener
```

---

## OQ-002: macOS port 443 strategy

Status: open
Linked milestone: Phase 5

How does the agent bind port 443 on macOS without a bad privileged-helper UX?

Options:

- `pfctl` rdr rule from 443 to the high port. No sudo per run, sudo once at setup.
- LaunchDaemon running as root listening on 443, forwarding to the per-user agent. Heavier setup, two processes.
- `authopen`-style helper invoked from setup. Complex.

Decision criteria: which option survives reboot, system updates, and uninstall cleanly.

## OQ-003: Linux port 443 strategy

Status: leaning `cap_net_bind_service`
Linked milestone: Phase 5

How does the agent bind 443 on Linux without sudo per run?

Options:

- `setcap 'cap_net_bind_service=+ep' /path/to/routeup`. Sudo once at setup. Must be reapplied on upgrade.
- iptables/nftables redirect from 443 to a high port. Sudo once at setup.
- systemd socket activation with the unit running as user, socket owned by root. Cleanest if user uses systemd.

Decision criteria: whether `setcap` survives package upgrades on common distros.

## OQ-009: Agent autostart approach for Phase 5

Status: leaning on-demand fork for v1, user-level launch unit added in Phase 5
Linked milestone: Phase 5

v1 forks the agent on first CLI call. Phase 5 adds:

- macOS: `LaunchAgent` plist in `~/Library/LaunchAgents/`. No sudo.
- Linux: systemd user unit at `~/.config/systemd/user/routeup.service` enabled via `systemctl --user enable routeup`. No sudo.

Sudo is only required for port 443 binding and CA trust install, not for the agent itself.

## OQ-010: Tunnel reconnect tuning surface

Status: deferred-to-phase-8
Linked milestone: Phase 8

Initial implementation hard-codes reconnect parameters and offers no CLI flag or config knob. Surface as config only if real complaints justify it.

Hard-coded defaults to start with:

```txt
backoff: 500ms..30s, multiplier 2, jitter +/-20%
heartbeat: every 15s, drop after 3 missed
server claim grace: 30s after disconnect
```

## OQ-011: mDNS for same-LAN device testing

Status: deferred-to-post-v1
Linked milestone: post-v1

Mobile testing on the same LAN currently routes through the public server. mDNS (`charpai.local`) could short-circuit that for iOS/macOS clients. Android needs a third-party resolver. Revisit only if real latency complaints appear.

Library options when revisited: `hashicorp/mdns` or `grandcat/zeroconf`.

## OQ-012: Server observability and metrics

Status: open
Linked milestone: Phase 6+

What does the public server expose for operational visibility?

Options:

- `/metrics` Prometheus endpoint with route counts, tunnel counts, request totals.
- Structured `slog` logs with request ids only.
- Nothing for v1; operator tails logs.

## OQ-013: Public server rate limiting

Status: open
Linked milestone: Phase 7

Rate-limit per token, per route, per source IP?

Defaults to consider:

- Per token: claim creation rate.
- Per route: request rate, per-second cap.
- Per source IP: connection rate.
- Public-namespace claims: per-IP rate limit on claim creation specifically. With no token to bind to, source IP is the only handle for abuse prevention. Important when the operator enables the public namespace on a hosted server.

A self-hosted operator must be able to disable rate limiting entirely.

## OQ-014: DNS provider for wildcard ACME

Status: open
Linked milestone: Phase 6+

The public server needs wildcard certificates for `*.<public-suffix>`. Wildcard certs require ACME DNS-01, which requires DNS API access.

Options:

- Cloudflare. Free, fast API, widely used. Probably default.
- Route 53. Solid, IAM-heavy.
- Self-hosted PowerDNS or similar. For operators avoiding third-party DNS.

Operator configures one provider; cert manager handles issuance and renewal.

## OQ-015: ACME library choice

Status: open
Linked milestone: Phase 6+

Options:

- `go-acme/lego`. Direct ACME client, many DNS provider plugins, lower-level.
- `caddyserver/certmagic`. Higher-level, batteries-included, opinionated.

certmagic is faster to integrate. lego gives more control. Pick when the server cert flow becomes the active milestone.

## OQ-016: Dual-stack loopback for the agent listener

Status: deferred-to-phase-5
Linked milestone: Phase 5

The agent's proxy listener binds `127.0.0.1` (IPv4 only). The upstream dial was changed to `localhost` so it reaches dev servers on either loopback family, but the listen side was left as IPv4. A client that resolves `*.localhost` to `::1` only, and won't fall back to IPv4, can't reach the agent.

This is low risk today: browsers and macOS `getaddrinfo` return both `127.0.0.1` and `::1` for `*.localhost` and try both, so the IPv4 listener is reachable. No real client has been observed failing.

Binding both families properly needs two listeners (`127.0.0.1:7070` and `[::1]:7070`), since `localhost:7070` binds only one family and `:7070` would expose the agent on every interface. Phase 5 reworks the listener for TLS and port 443, so handle it there if it is still worth doing.
