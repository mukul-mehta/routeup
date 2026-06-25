# Deploying the routeup server on Fly.io

The complete one-time setup. Run every `fly` command **from the repository root**
(the Docker build needs `go.mod` and the source).

## Before you start

You need:

- A domain whose DNS is on **Cloudflare** (this guide uses `routeup.dev`).
- The **flyctl** CLI installed and authenticated: `fly auth login`.
- The `routeup` CLI on your own machine (to expose routes once the server is up).

## 1. Create a scoped Cloudflare API token

Cloudflare dashboard → **My Profile → API Tokens → Create Token → "Edit zone DNS"**
template → Zone Resources → Include → Specific zone → `routeup.dev` → Create.
Copy the token (shown once). This is what lets the server answer ACME DNS-01.

## 2. Edit the config

In `deploy/routeup-server.json`, set `domain` and `acme_email`. Leave
`acme_ca: "staging"` for the first deploy. If you pick an app name other than
`routeup-server`, set it in `deploy/fly.toml` (`app = "…"`) too.

Pick a region near you (`fly platform regions`) and set `primary_region` in
`deploy/fly.toml`. This guide uses `sin` (Singapore) — the closest region to
India that's available on the free Legacy Hobby plan (Mumbai `bom` is paid-only).
Asia egress is pricier than US/EU ($0.04 vs $0.02/GB, 30 vs 100 GB free) — fine
at low traffic.

## 3. Create the app, IPs, volume, secret

```bash
fly apps create routeup-server                          # don't use `fly launch` (it rewrites fly.toml)

fly ips allocate-v4 -a routeup-server                   # DEDICATED IPv4 — $2/mo, required for TLS passthrough
fly ips allocate-v6 -a routeup-server                   # free
fly ips list -a routeup-server                          # note the v4 and v6 — you need them for DNS

fly volumes create routeup_data --size 1 --region sin -a routeup-server   # cert cache + SQLite (free: <3GB)

fly secrets set CLOUDFLARE_API_TOKEN=<your-token> -a routeup-server
```

## 4. DNS on Cloudflare — set ALL of these to **DNS only (grey cloud)**

DNS wildcards match one label, same as TLS wildcards, so you need a record per
namespace level. An **orange-cloud (proxied)** record would terminate TLS and
break the tunnel — keep them grey.

```
A     *.routeup.dev       ->  <dedicated v4>
AAAA  *.routeup.dev       ->  <v6>
A     *.try.routeup.dev   ->  <dedicated v4>
AAAA  *.try.routeup.dev   ->  <v6>
```

- The apex `routeup.dev` stays pointed at your marketing site (a separate deploy).
- The control host `edge.routeup.dev` resolves via the `*.routeup.dev` wildcard — no separate record.
- For each namespace you mint a token for later, add `*.<ns>.routeup.dev` → the same IPs (see step 8).

## 5. Deploy (staging certs first)

```bash
fly deploy -c deploy/fly.toml
fly scale count 1 -a routeup-server        # ensure exactly one machine (it's single-instance)
fly logs -a routeup-server
```

In the logs, watch for `obtaining startup wildcard certificates` followed by no
error. If DNS-01 fails, it's almost always the token scope or the wrong zone.

## 6. Verify staging

```bash
curl -k https://edge.routeup.dev/_routeup/v1/health
# {"status":"ok","domain":"routeup.dev"}
```

`-k` because Let's Encrypt **staging** certs are intentionally untrusted. If this
returns ok, the whole path works: DNS, the Cloudflare token, issuance, and serving.

## 7. Cut over to production

Edit `deploy/routeup-server.json` → `"acme_ca": "production"`, then:

```bash
fly deploy -c deploy/fly.toml
curl https://edge.routeup.dev/_routeup/v1/health     # no -k now; the cert is publicly trusted
```

If a stale staging cert lingers: `fly ssh console -a routeup-server -C "rm -rf /data/acme"`
and redeploy.

## 8. Mint a token and add its DNS

```bash
fly ssh console -a routeup-server \
  -C "/usr/local/bin/routeup token create mukul --allow '*.mukul.routeup.dev' --db /data/server.db"
```

Copy the `sk_routeup_…` secret (shown once). Then add the namespace's DNS on
Cloudflare (grey cloud):

```
A     *.mukul.routeup.dev  ->  <dedicated v4>
AAAA  *.mukul.routeup.dev  ->  <v6>
```

The first claim into a brand-new namespace warms its wildcard cert for ~a minute
(one DNS-01 issuance), then it's cached on the volume.

## 9. Expose a route from your machine

```bash
routeup setup                                          # once: local CA + agent (expose runs through the agent)
ROUTEUP_TOKEN=sk_routeup_… \
  routeup expose myapp --port 8080 --server https://edge.routeup.dev
#  -> https://myapp.mukul.routeup.dev
```

No token (public namespace):

```bash
routeup expose cool --port 8080 --server https://edge.routeup.dev
#  -> https://cool.try.routeup.dev  (ephemeral)
```

## Continuous deployment

After this one-time bring-up, you don't run `fly deploy` by hand: every push to
`main` deploys the server automatically (the `deploy` job in
`.github/workflows/ci.yml`), once `test` and `lint` pass. The CLI is separate —
it releases from `v*` tags via goreleaser, on its own cadence.

Enable it once by giving Actions a scoped deploy token:

```bash
fly tokens create deploy -a routeup-server     # prints a "FlyV1 ..." token
gh secret set FLY_API_TOKEN                     # paste it when prompted
# or: GitHub → Settings → Secrets and variables → Actions → new secret FLY_API_TOKEN
```

The job runs `flyctl deploy --remote-only -c deploy/fly.toml` (builds on Fly's
remote builders, no Docker on the runner) and serializes deploys
(`concurrency: fly-deploy`) because the server is a single stateful instance.

## Operations

```bash
fly logs -a routeup-server                  # tail logs
fly scale memory 512 -a routeup-server      # more RAM (also bump GOMEMLIMIT in fly.toml to ~440MiB, then redeploy)
fly scale vm shared-cpu-2x -a routeup-server# more CPU
fly deploy -c deploy/fly.toml               # redeploy after editing the config or code
fly ssh console -a routeup-server           # shell on the box (token admin, /data inspection)
```

## What it costs

On the **Legacy Hobby** plan, sized at 256MB: compute, the 1GB volume, and
< 100GB/mo egress are all inside the free allowance. The only charge is the
**dedicated IPv4 (~$2/mo)**, which TLS passthrough requires. Watch Dashboard →
Cost Explorer the first week to confirm. Don't switch off Legacy Hobby — it's
irreversible and Pay-As-You-Go has no free allowances.
