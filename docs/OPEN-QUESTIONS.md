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
OQ-009  Agent autostart approach for Phase 4
OQ-010  Tunnel reconnect tuning surface
OQ-011  mDNS for same-LAN device testing
OQ-012  Server observability and metrics
OQ-013  Public server rate limiting
OQ-016  Dual-stack loopback for the agent listener
OQ-017  Release artifact signing / provenance
```

Resolved and removed: OQ-014 (DNS provider → Cloudflare) and OQ-015 (ACME
library → certmagic). The decision is recorded in `PLAN.md` under Public Server
→ TLS; the implementation is `internal/server/tls.go`.

---

## OQ-002: macOS port 443 strategy

Status: open
Linked milestone: Phase 4

How does the agent bind port 443 on macOS without a bad privileged-helper UX?

Options:

- `pfctl` rdr rule from 443 to the high port. No sudo per run, sudo once at setup.
- LaunchDaemon running as root listening on 443, forwarding to the per-user agent. Heavier setup, two processes.
- `authopen`-style helper invoked from setup. Complex.

Decision criteria: which option survives reboot, system updates, and uninstall cleanly.

## OQ-003: Linux port 443 strategy

Status: leaning `cap_net_bind_service`
Linked milestone: Phase 4

How does the agent bind 443 on Linux without sudo per run?

Options:

- `setcap 'cap_net_bind_service=+ep' /path/to/routeup`. Sudo once at setup. Must be reapplied on upgrade.
- iptables/nftables redirect from 443 to a high port. Sudo once at setup.
- systemd socket activation with the unit running as user, socket owned by root. Cleanest if user uses systemd.

Decision criteria: whether `setcap` survives package upgrades on common distros.

## OQ-009: Agent autostart approach for Phase 4

Status: leaning on-demand fork for v1, user-level launch unit added in Phase 4
Linked milestone: Phase 4

v1 forks the agent on first CLI call. Phase 4 adds:

- macOS: `LaunchAgent` plist in `~/Library/LaunchAgents/`. No sudo.
- Linux: systemd user unit at `~/.config/systemd/user/routeup.service` enabled via `systemctl --user enable routeup`. No sudo.

Sudo is only required for port 443 binding and CA trust install, not for the agent itself.

## OQ-010: Tunnel reconnect tuning surface

Status: implemented with hard-coded defaults; revisit a config surface only on complaints
Linked milestone: Phase 6

The implementation hard-codes tunnel parameters and offers no CLI flag or config
knob. Now in use: the tunnel client backs off 500ms..30s (x2) in
`internal/tunnel/client.go`; yamux uses a 1MiB per-stream flow-control window
and a 30s connection write timeout in `internal/tunnel/utils.go`; and the server
holds a released token claim for a 30s grace window in
`internal/server/holds.go`. yamux keepalive covers heartbeat. Surface as config
only if real complaints justify it.

## OQ-011: mDNS for same-LAN device testing

Status: deferred-to-post-v1
Linked milestone: post-v1

Mobile testing on the same LAN currently routes through the public server. mDNS (`charpai.local`) could short-circuit that for iOS/macOS clients. Android needs a third-party resolver. Revisit only if real latency complaints appear.

Library options when revisited: `hashicorp/mdns` or `grandcat/zeroconf`.

## OQ-012: Server observability and metrics

Status: open
Linked milestone: Phase 5+

What does the public server expose for operational visibility?

Options:

- `/metrics` Prometheus endpoint with route counts, tunnel counts, request totals.
- Structured `slog` logs with request ids only.
- Nothing for v1; operator tails logs.

## OQ-013: Public server rate limiting

Status: open
Linked milestone: Phase 5

Rate-limit per token, per route, per source IP?

Defaults to consider:

- Per token: claim creation rate.
- Per route: request rate, per-second cap.
- Per source IP: connection rate.
- Public-namespace claims: per-IP rate limit on claim creation specifically. With no token to bind to, source IP is the only handle for abuse prevention. Important when the operator enables the public namespace on a hosted server.

A self-hosted operator must be able to disable rate limiting entirely.

## OQ-016: Dual-stack loopback for the agent listener

Status: deferred-to-phase-4
Linked milestone: Phase 4

The agent's proxy listener binds `127.0.0.1` (IPv4 only). The upstream dial was changed to `localhost` so it reaches dev servers on either loopback family, but the listen side was left as IPv4. A client that resolves `*.localhost` to `::1` only, and won't fall back to IPv4, can't reach the agent.

This is low risk today: browsers and macOS `getaddrinfo` return both `127.0.0.1` and `::1` for `*.localhost` and try both, so the IPv4 listener is reachable. No real client has been observed failing.

Binding both families properly needs two listeners (`127.0.0.1:7070` and `[::1]:7070`), since `localhost:7070` binds only one family and `:7070` would expose the agent on every interface. Phase 4 reworks the listener for TLS and port 443, so handle it there if it is still worth doing.

## OQ-017: Release artifact signing / provenance

Status: leaning do-nothing for v1 (sha256 + HTTPS only)
Linked milestone: post-v1

Should release artifacts be cryptographically signed or carry build provenance, beyond the current sha256 + HTTPS?

Current state: `checksums.txt` lists a sha256 for each archive; `routeup update`, `install.sh`, and the Homebrew cask all fetch over HTTPS from GitHub and verify that sha256. The trust root is "GitHub + TLS." A sha256 sitting next to the artifact is integrity-against-corruption, not authenticity: anyone who can tamper with a release can rewrite the checksum too.

Options:

- A) Do nothing. Keep sha256 + HTTPS. Same trust model as rustup's installer and most `curl | sh` tools. Zero key management. (current choice)
- B) minisign signature over `checksums.txt`, verified in-process by `routeup update` via the `aead.dev/minisign` library (embedded public key, not a CLI). The only option that hardens a real user flow with zero user-facing change: the unattended self-update verifies invisibly and aborts on a tampered release. Limitation: the signing key would live in a CI secret, the same trust domain as the releases it protects; generating the key offline recovers most of the benefit (OpenBSD `signify` model). Covers only the direct-download update path (brew relies on its own cask sha256).
- C) GitHub Artifact Attestations (keyless Sigstore / SLSA provenance). Zero user-facing change and nothing to store or rotate, but verification is opt-in (`gh attestation verify`), so it protects only auditors and distro packagers, not normal users, and cannot be checked in-process by the self-updater without a heavy embedded verifier.

Why A for v1: every verify-at-the-terminal scheme (`gh`, `minisign` CLI, `cosign`) is opt-in — it imposes nothing on users but also protects no one who does not run it. The only friction-free protection is B, and its value is capped because the key would sit in CI alongside what it signs, while the first install is already HTTPS-trusted. The key-management burden is not justified at v1.

Revisit when there is a concrete trigger: distro packagers or a security review want signed/provenanced releases, or the auto-update path becomes a higher-value target. At that point prefer B (minisign in `update`, offline-generated key) for friction-free user protection, optionally plus C for auditable provenance.
