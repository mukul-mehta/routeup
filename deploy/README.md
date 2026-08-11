# Deploying the routeup server on Fly.io

[![Deploy](https://github.com/mukul-mehta/routeup/actions/workflows/deploy.yml/badge.svg)](https://github.com/mukul-mehta/routeup/actions/workflows/deploy.yml)

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
The deployment config uses `log_format: "json"`, so lifecycle, tunnel claims,
and completed public requests are emitted as structured JSON to Fly's standard
log stream. `fly logs` displays the same stream.

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

After this one-time bring-up, you don't run `fly deploy` by hand. Every push to
`main` runs `.github/workflows/ci.yml`, including integration coverage. A
successful CI run triggers `.github/workflows/deploy.yml`, which deploys both
the public server and log shipper. CI also validates the example projects, while
the CLI releases from `v*` tags through the separate release workflow.

The combined workflow uses one organization-scoped token to deploy both Fly
apps:

```bash
fly tokens create org \
  --org personal \
  --expiry 2160h \
  --name "Routeup GitHub deployments"

gh secret set FLY_API_TOKEN                    # paste the full FlyV1 value
# or add it under GitHub → Settings → Secrets and variables → Actions
```

The workflow validates both Fly configurations, deploys the server from the
repository source, then deploys the external log-shipper image with the tracked
Loki sink. Deployments are serialized with `concurrency: fly-deploy` because the
server is a single stateful instance.

## Operations

```bash
fly logs -a routeup-server                  # tail logs
fly scale memory 512 -a routeup-server      # more RAM (also bump GOMEMLIMIT in fly.toml to ~440MiB, then redeploy)
fly scale vm shared-cpu-2x -a routeup-server# more CPU
fly deploy -c deploy/fly.toml               # redeploy after editing the config or code
fly ssh console -a routeup-server           # shell on the box (token admin, /data inspection)
```

Public-request info logs include method, status, duration, and response bytes.
Route identity is available only at debug level. Logs always omit paths,
queries, headers, bodies, tokens, cookies, and source addresses. Set
`log_level` to `debug`, `info`, `warn`, or `error` and `log_format` to `text` or
`json` in `deploy/routeup-server.json`.

### Metrics and Grafana

The deployment enables Routeup's Prometheus listener on internal port 9091.
Fly's `[metrics]` configuration scrapes `/metrics` every 15 seconds and stores
the samples in Fly's managed Prometheus-compatible VictoriaMetrics service.
Port 9091 is intentionally not listed under `[[services]]`, so it is not public.

Routeup does not push metrics to Grafana. Grafana queries Fly's metrics store.
To connect an existing Grafana instance, first find the Fly organization slug
and create a read-only token:

```bash
fly orgs list
fly tokens create readonly
```

Add a Prometheus datasource in Grafana with:

```txt
URL: https://api.fly.io/prometheus/<org-slug>/
HTTP header: Authorization
Header value: FlyV1 <read-only-token>
```

The custom metrics are named `routeup_*` and cover active tunnels, tunnel
lifecycle events, claim outcomes, requests by status class, in-flight requests,
request duration, forwarding errors, and reaped holds. They contain no route,
public-host, token, path, source-IP, or user labels. Fly retains metrics for
roughly 15 days; longer retention requires federating or remote-writing into
another Prometheus-compatible store.

An importable dashboard is included at `deploy/grafana-dashboard.json`. In
Grafana, open **Dashboards → New → Import**, upload that file, and select the Fly
Prometheus datasource when prompted. It includes active tunnels, request rate,
5xx percentage, latency percentiles, claim outcomes, tunnel lifecycle events,
forwarding errors, and reaped holds.

### Logs in Grafana

Grafana visualizes logs but does not store them. Grafana Cloud Logs provides the
Loki-compatible store; Fly's log shipper forwards the Fly NATS log stream into
it. The tracked shipper configuration lives at `deploy/log-shipper/fly.toml` and
has no services or public IPs.

Create the Fly app once:

```bash
fly apps create routeup-log-shipper --org personal
```

In the Grafana Cloud portal, open the stack's **Logs → Send Logs** page. Record
the Loki URL and numeric logs instance ID, then create an access-policy token
with `logs:write`. Create a separate organization-scoped read-only Fly token for
the NATS source:

```bash
fly tokens create readonly \
  --org personal \
  --expiry 2160h \
  --name "Routeup log shipper"
```

Set the runtime secrets. `ACCESS_TOKEN` is the full `FlyV1 ...` value from the
previous command; `LOKI_USERNAME` is Grafana Cloud's numeric logs instance ID:

```bash
fly secrets set -a routeup-log-shipper \
  ORG=personal \
  ACCESS_TOKEN='<full FlyV1 token>' \
  LOKI_URL='<Grafana Cloud Loki URL>' \
  LOKI_USERNAME='<Grafana Cloud logs instance ID>' \
  LOKI_PASSWORD='<Grafana Cloud token with logs:write>'
```

The non-secret `SUBJECT='logs.routeup-server.>'` filter is committed in the Fly
config. Validate and perform the first deployment:

```bash
fly config validate -c deploy/log-shipper/fly.toml
fly deploy --no-public-ips \
  -c deploy/log-shipper/fly.toml \
  --file-local /etc/vector/sinks/loki.toml=deploy/log-shipper/loki.toml
fly checks list -a routeup-log-shipper
```

The tracked Loki sink adds `service_name="routeup-server"` from Fly's app-name
metadata. This lets Grafana Cloud identify Routeup instead of grouping its logs
under `unknown_service`. Both the manual command above and GitHub Actions mount
the sink at `/etc/vector/sinks/loki.toml` before Vector starts.

Future changes deploy automatically after CI succeeds on `main`. Runtime Loki
and NATS credentials stay only in Fly secrets; GitHub receives only the
organization-scoped deployment token.

Add Loki as a Grafana datasource with the same endpoint and credentials. Fly's
shipper labels Routeup streams with `fly_app_name="routeup-server"`, so the base
LogQL query is:

```logql
{fly_app_name="routeup-server"}
```

The equivalent Grafana service query is:

```logql
{service_name="routeup-server"}
```

Structured Routeup request logs can be filtered further:

```logql
{fly_app_name="routeup-server"} | json | msg="public request completed"
```

## What it costs

On the **Legacy Hobby** plan, sized at 256MB: compute, the 1GB volume, and
< 100GB/mo egress are all inside the free allowance. The only charge is the
**dedicated IPv4 (~$2/mo)**, which TLS passthrough requires. Watch Dashboard →
Cost Explorer the first week to confirm. Don't switch off Legacy Hobby — it's
irreversible and Pay-As-You-Go has no free allowances.
