# Server recovery

The Fly deployment keeps SQLite and the certmagic ACME cache together on the
encrypted `routeup_data` volume mounted at `/data`. Fly scheduled snapshots are
the v1 backup mechanism. Restoring the complete volume keeps route holds, token
hashes, and cached certificates consistent.

## Inspect backups

Find the volume ID and list its snapshots:

```bash
fly volumes list -a routeup-server
fly volumes snapshots list <volume-id> -a routeup-server
```

Fly volumes created by the deployment guide have scheduled snapshots enabled.
The default retention is five days. Create an extra snapshot immediately before
a risky database or deployment operation:

```bash
fly volumes snapshots create <volume-id> -a routeup-server
```

## Restore a snapshot

Do not overwrite or delete the damaged volume first. Stop the server, create a
new volume from the chosen snapshot in the same region, and keep the old volume
until verification succeeds:

```bash
fly scale count 0 -a routeup-server

fly volumes create routeup_data_restored \
  --app routeup-server \
  --region sin \
  --size 1 \
  --snapshot-id <snapshot-id>
```

Temporarily change `deploy/fly.toml` so `[mounts].source` is
`routeup_data_restored`, validate, and deploy:

```bash
fly config validate -c deploy/fly.toml
fly deploy -c deploy/fly.toml
```

Verify health, token listing, tunnel claims, metrics, and public forwarding:

```bash
curl https://edge.routeup.dev/_routeup/v1/health
fly ssh console -a routeup-server \
  -C "/usr/local/bin/routeup token list --db /data/server.db"
fly logs -a routeup-server
```

On startup, anonymous holds from the snapshot are deleted and token holds left
active by the old process enter the normal 30-second grace window. Existing
clients using the same token can reconnect immediately; a different token may
claim the route after grace expires.

After verification, either keep the restored volume name in deployment config
or schedule a maintenance window to move back to a newly restored
`routeup_data`. Delete the damaged volume only after the replacement and a fresh
snapshot are confirmed.

## Tokens

The database stores token hashes, not recoverable token secrets. A restored
database accepts the same secrets clients already hold, but it cannot reveal a
lost secret. Create a replacement token, update clients, verify it, and revoke
the old token ID.

## ACME certificates

Do not delete `/data/acme` during database recovery. Restoring the full volume
also restores certmagic's certificate cache and avoids unnecessary ACME
issuance. If the cache alone is lost, redeploy and allow DNS-01 issuance to
rebuild it; avoid repeated retries that could hit CA rate limits.

## Complete rebuild

If no usable snapshot exists, create a new encrypted `routeup_data` volume,
deploy the server, verify wildcard issuance, recreate tokens, and retain the
existing DNS records. Active tunnels must reconnect with newly issued token
secrets. Metrics counters restart from zero because they are process-local.
