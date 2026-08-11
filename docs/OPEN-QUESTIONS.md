# Open Questions

This file tracks unresolved product, architecture, and engineering questions for `routeup`. Decided questions move to `PLAN.md` or `docs/ARCHITECTURE.md` and leave this file.

## How To Use This File

- Each question has a stable id (`OQ-NNN`) that can be referenced from code, commits, and other docs.
- `Status` is one of `open`, `leaning <option>`, `deferred-to-phase-N`, or `deferred-to-post-v1`.
- Re-read this file at the start of each phase. Items not actionable for the next phase become `deferred-to-phase-N`.
- When an item is decided, the answer moves to `PLAN.md` or `docs/ARCHITECTURE.md` and the entry is removed here.
- IDs are stable. Resolved entries are deleted, not reused. Holes in the numbering are expected.

## Index

```txt
OQ-016  Dual-stack loopback for the agent listener
OQ-017  Release artifact signing / provenance
OQ-018  Alerts and notifications
```

Resolved and removed: OQ-002, OQ-003, OQ-009, OQ-010, OQ-011, OQ-012, OQ-013,
OQ-014, and OQ-015. Their decisions are recorded in the architecture,
milestones, and walkthrough docs.

---

## OQ-016: Dual-stack loopback for the agent listener

Status: deferred-to-post-v1
Linked milestone: post-v1

The agent's proxy listener binds `127.0.0.1` (IPv4 only). The upstream dial was changed to `localhost` so it reaches dev servers on either loopback family, but the listen side was left as IPv4. A client that resolves `*.localhost` to `::1` only, and won't fall back to IPv4, can't reach the agent.

This is low risk today: browsers and macOS `getaddrinfo` return both `127.0.0.1` and `::1` for `*.localhost` and try both, so the IPv4 listener is reachable. No real client has been observed failing.

Binding both families properly needs two listeners (`127.0.0.1:7070` and `[::1]:7070`), since `localhost:7070` binds only one family and `:7070` would expose the agent on every interface. Revisit post-v1 if a real client cannot fall back to IPv4.

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

## OQ-018: Alerts and notifications

Status: open
Linked milestone: post-v1/unassigned

Which Grafana alerts and notification channels should be shipped as supported
operator defaults? Decide trigger thresholds, minimum traffic requirements,
no-data behavior, deduplication, secret ownership, and email/Slack/PagerDuty
channels in a separate planning slice. Keep alerts outside the local dashboard;
it observes the developer's agent, not hosted-server operations.
