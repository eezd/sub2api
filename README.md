<div align="center">

<img src="assets/logo.svg" alt="Sub2API Logo" width="128" />

# Sub2API — eezd fork

[![Release](https://img.shields.io/github/v/release/eezd/sub2api?include_prereleases&label=release)](https://github.com/eezd/sub2api/releases)
[![Go](https://img.shields.io/badge/Go-1.27.0-00ADD8.svg)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3.4+-4FC08D.svg)](https://vuejs.org/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-336791.svg)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D.svg)](https://redis.io/)

**An AI API gateway for subscription quota distribution, multi-provider routing, and operator-managed deployments.**

[English](README.md) · [中文](README_CN.md) · [日本語](README_JA.md)

</div>

> This repository is the `eezd/sub2api` fork of [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api). It tracks upstream while adding the fork features listed below. The fork is not an official upstream release.

## What this fork adds

The table below is the important difference from upstream. Core Sub2API features remain available; this section calls out functionality maintained specifically in this fork.

| Fork addition | What it does | Documentation |
|---|---|---|
| **Infinite Canvas workspace** | Embeds the Infinite Canvas application at `/workspace/canvas`, with an optional external deployment through `INFINITE_CANVAS_URL`. It can connect to the local Codex Agent without putting an API key in a URL. | [`docs/infinite-canvas.md`](docs/infinite-canvas.md) |
| **Account recent-request timeline** | Adds a Recent Requests column to the admin account list. It displays the latest five requests from the last 15 minutes, including success/error state, latency, first-token time, tokens, cost, multiplier, and request details. Data is fetched in one batch per refresh rather than one request per account. | [`POST /api/v1/admin/ops/requests/recent-by-account`](backend/internal/server/routes/admin.go) |
| **ModelTrace model-degradation check** | Adds an admin-only account test for OpenAI and Anthropic text-model accounts. An administrator explicitly selects the model; the server collects three valid semantic-free number sequences in at most six calls and performs closed-set statistical attribution against the bundled GPT/Claude reference bank. Answers cut off by an output cap, refusal, or content filter are discarded. Successful, failed, and cancelled runs are retained per account. Results are diagnostic, not conclusive identity proof. | [`POST /api/v1/admin/accounts/:id/model-trace`](backend/internal/server/routes/admin.go)<br>[History API](backend/internal/server/routes/admin.go) |
| **SVG animation degradation check** | Adds a separate one-call visual test beside ModelTrace. The administrator selects a text model, sends the fixed pelican-on-a-bicycle HTML/SVG prompt, and views the returned animation in a script-free sandbox whose content security policy is placed before any model output, so the preview cannot load external resources. Truncated or filtered answers are recorded as failures. Successful, failed, and cancelled runs are retained; prior animations can be replayed from account history. | [`POST /api/v1/admin/accounts/:id/svg-animation-test`](backend/internal/server/routes/admin.go)<br>[History API](backend/internal/server/routes/admin.go) |
| **Persistent bulk degradation checks** | Select accounts across pages and use **Degradation Check** or **SVG Animation Degradation Check** in the bulk-actions bar (up to 1,000 accounts). Explicitly choose each account's model; platform fills apply only where that model is available, and unsupported/unselected rows are skipped. Server-side tasks survive dialog close, navigation, refresh, and sign-out; reopen **Bulk check tasks** without selecting accounts. Explicit cancellation stops queued/running items but retains completed results and account history. Shared-database instances allow at most two concurrent checks and serialize the same account. Interrupted items are never automatically replayed; pending items continue. Checks consume upstream quota. | [`/api/v1/admin/accounts/degradation-check-batches`](backend/internal/server/routes/admin.go) |
| **Codex turn-state ticketing** | Optionally harvests and injects the `x-codex-turn-state` ticket for supported OpenAI OAuth models. Harvesting, proxy selection, TTL, model scope, and fail-closed behavior are administrator-controlled. | `gateway.openai_codex_ticket` in [`deploy/config.example.yaml`](deploy/config.example.yaml) |
| **Signed OpenAI Transport plugin** | Ships an official `.s2plugin` for OpenAI OAuth outbound HTTP/TLS. It supports Node.js 24-style ClientHello profiles, account proxy inheritance, connection limits, timeouts, UI configuration, signature verification, and percentage rollout. | [`docs/PLUGIN_INSTALLATION.md`](docs/PLUGIN_INSTALLATION.md) |
| **TLS fingerprint profiles** | Adds managed TLS fingerprint profiles for compatible outbound paths, with bounded ClientHello fields, step-up authentication for profile mutations, and direct/HTTP CONNECT/SOCKS5H proxy support. | [`docs/PLUGIN_DEVELOPMENT.md`](docs/PLUGIN_DEVELOPMENT.md) and `backend/pkg/pluginapi/docs/` |
| **Stronger outbound URL security** | New installations use HTTPS-only upstream validation and reject private, loopback, link-local, and unspecified destinations by default. Redirects and response-provided URLs are validated separately. | [`deploy/EDGE_SECURITY.md`](deploy/EDGE_SECURITY.md) |
| **Fork release line** | Publishes versioned releases as `vX.Y.Z-custom.N`, signed plugin packages, and full-version GHCR images. The current release is [`v0.2.9-custom.1`](https://github.com/eezd/sub2api/releases/tag/v0.2.9-custom.1). | [Release workflow](.github/workflows/release.yml) |

### Compatibility rule for the official plugin

The official plugin is released together with the fork version it was tested against. For example:

```text
Sub2API: 0.2.9-custom.1
Plugin:  0.2.9-custom.1
Requires: >=0.2.9 <0.3.0
```

Upload the matching `.s2plugin` from the same GitHub Release. Keep `plugins.allow_unsigned: false` in production; the official package is verified by the host's built-in publisher key.

## Upstream capabilities retained

This fork includes the upstream platform capabilities, including:

- Multi-account management for OAuth and API-key accounts.
- API-key distribution, groups, sticky sessions, account scheduling, failover, concurrency limits, and rate limiting.
- Token-level usage tracking, model pricing, billing, subscriptions, and built-in payment providers.
- OpenAI-compatible, Anthropic-compatible, Gemini, Grok/xAI, Antigravity, and other provider integrations available in the current release.
- Composite groups for model-based routing across providers ([operator guide](docs/COMPOSITE_GROUPS.md)).
- Synchronous and asynchronous image tasks, batch image processing, and OpenAI Responses WebSocket ingress controls.
- Admin monitoring, usage reports, backups, prompt audit, security settings, and external dashboard integrations.
- Simple Mode for personal or internal deployments.

See the [upstream project](https://github.com/Wei-Shaw/sub2api) for the upstream history and baseline architecture. Fork-specific behavior is documented above and in the linked files.

## Important notice

- Review the terms of service of every upstream provider before using OAuth accounts, subscription quotas, or API relay features.
- Operate the system only in compliance with applicable law, provider contracts, privacy requirements, payment rules, and network-access restrictions.
- This software is provided for technical learning and research. The deployer is solely responsible for accounts, data, users, billing, content, compliance, and operational security.
- A deployment, hosted service, paid plan, relay service, or commercial activity based on this repository is not authorized or endorsed by the project maintainers. Read [`docs/legal/admin-compliance.en.md`](docs/legal/admin-compliance.en.md) before operating a public or commercial instance.

## Quick start

### Docker Compose

Docker Compose is the easiest development and self-hosting path. The checked-in Compose files currently reference the upstream image name; for this fork, replace the application image with the versioned GHCR image from the fork release:

```bash
git clone https://github.com/eezd/sub2api.git
cd sub2api/deploy
cp .env.example .env
chmod 600 .env

# Set a strong POSTGRES_PASSWORD, JWT_SECRET, and TOTP_ENCRYPTION_KEY in .env.
# Use the fork image instead of the upstream default:
sed -i 's#weishaw/sub2api:latest#ghcr.io/eezd/sub2api:0.2.9-custom.1#' docker-compose.local.yml

mkdir -p data postgres_data redis_data
docker compose -f docker-compose.local.yml up -d
docker compose -f docker-compose.local.yml logs -f sub2api
```

Open `http://YOUR_SERVER_IP:8080`. With `AUTO_SETUP=true`, the container applies migrations and creates the initial admin account. If `ADMIN_PASSWORD` is not set, read the generated password from the application logs.

For production, pin an exact fork release instead of using `latest`. The local-directory Compose variant is recommended because `data/`, `postgres_data/`, and `redis_data/` can be backed up and migrated together.

See [`deploy/README.md`](deploy/README.md) for Compose variants, recovery behavior, Apple `container`, migration, and operational commands.

### Pre-built binaries

Download the platform archive from the [fork Releases page](https://github.com/eezd/sub2api/releases), then configure PostgreSQL 15+ and Redis 7+ before starting the server. The setup wizard creates the first administrator when no configuration exists.

Available release targets currently include:

- Linux amd64 and arm64
- macOS amd64 and arm64
- Windows amd64

### Build from source

```bash
git clone https://github.com/eezd/sub2api.git
cd sub2api

# Frontend
corepack enable
cd frontend
pnpm install
pnpm run build

# Backend with embedded frontend
cd ../backend
VERSION="$(./scripts/resolve-version.sh)"
go build -tags embed -ldflags="-X main.Version=${VERSION}" -o sub2api ./cmd/server
./sub2api
```

The `embed` build tag embeds the frontend into the binary. For local development, run the backend and frontend separately; see [`DEV_GUIDE.md`](DEV_GUIDE.md).

## Configuration essentials

### Secure outbound defaults

New installations should keep the outbound URL boundary enabled:

```yaml
security:
  url_allowlist:
    enabled: true
    upstream_hosts:
      - api.openai.com
      - api.anthropic.com
    allow_private_hosts: false
    allow_insecure_http: false
```

Add provider hostnames to `upstream_hosts`; do not disable the boundary just to add a provider. Cross-origin redirects are rejected before credentials are forwarded. Response-provided URLs are restricted to public destinations and validate every redirect hop.

Only use the following weaker settings in an isolated development environment:

```yaml
security:
  url_allowlist:
    enabled: false
    allow_private_hosts: true
    allow_insecure_http: true
```

Disabling these checks can expose credentials and internal services. Enforce equivalent egress controls at the network boundary if compatibility requires it.

### OpenAI WebSocket fallback

If a proxy repeatedly reconnects OpenAI Responses WebSockets, force upstream HTTP/SSE without changing the client-facing protocol:

```yaml
gateway:
  openai_ws:
    force_http: true
```

For Compose, use `GATEWAY_OPENAI_WS_FORCE_HTTP=true`. See the full WebSocket settings in [`deploy/config.example.yaml`](deploy/config.example.yaml).

### Codex ticket harvesting

The feature is disabled by default. Enable and configure it only for accounts and models where it is required:

```yaml
gateway:
  openai_codex_ticket:
    enabled: true
    harvest_proxy_url: socks5h://user:password@proxy.example:1080
    models:
      - gpt-6-astra
      - gpt-5.6-sol
    fail_closed: true
```

The ticket is system-managed. Do not put ticket values into account-edit payloads or user-provided account metadata. Treat harvest proxy credentials as secrets.

## Installing the OpenAI Transport plugin

1. Download `sub2api-openai-transport-<version>.s2plugin` from the matching fork Release.
2. In the admin dashboard, open **Plugins**, upload the package, and verify its signature and compatibility.
3. Configure and test it before enabling it.
4. Start with a low rollout percentage and observe plugin health, request success rate, latency, and logs.
5. Disable the binding to return OpenAI OAuth accounts to the core transport path.

The plugin does not replace account selection, OAuth refresh, SSE parsing, retries, or billing. Do not unpack or modify a signed package. Full installation, upgrade, rollback, configuration, and source-build instructions are in [`docs/PLUGIN_INSTALLATION.md`](docs/PLUGIN_INSTALLATION.md).

## Infinite Canvas

The fork embeds the Canvas application at:

```text
/workspace/canvas
```

The default same-origin deployment serves the embedded app at `/canvas-app/`. To use an external Canvas deployment, set `INFINITE_CANVAS_URL` and ensure the external app implements the documented bridge. API keys are passed through the authenticated bridge, not placed in a public URL.

To connect the Codex Agent from the Canvas workspace:

```bash
npx -y @basketikun/canvas-agent
```

See [`docs/infinite-canvas.md`](docs/infinite-canvas.md) for deployment, bridge security, external hosting, and troubleshooting.

## Admin account request timeline

The admin account list includes **Recent Requests** when the column is visible. It refreshes the current page in a single batched request, shows up to five requests per account from the previous 15 minutes, and opens a detail panel with request ID, model, status, timing, token usage, cost, and account multiplier.

This path intentionally avoids the old per-account polling pattern. The endpoint accepts the visible account IDs and returns grouped results:

```http
POST /api/v1/admin/ops/requests/recent-by-account
Content-Type: application/json

{"account_ids":[101,102,103]}
```

Request timing details are diagnostic-only and readable for 30 days (including the cutoff). Expired details stay hidden even before physical cleanup; hourly cleanup deletes batches of at most 10,000 rows under one five-second deadline. A downstream disconnect is recorded separately from upstream completion: drained usage and the disconnect state are retained even if the upstream later fails, without issuing another model request. Cancellation or a closed pipe caused by closing a successfully completed stream is not reported as an upstream transport failure; errors returned by Read before Close begins remain visible. OpenAI passthrough retains the requested service tier and reasoning effort for the existing billing rules.

Upstream balance probes accept responses up to 256 KiB; billing declarations retain their separate 64 KiB limit. Oversized balance responses fail without replacing the last successful balance.

Mihomo fixed node ports are persistent, monotonically allocated, and never reused. Hot reload preflights only newly added ports and verifies SOCKS readiness on all configured node ports before saving, including existing listeners rebuilt by node disable/recover operations. On reload/readiness failure, it restores and verifies the old configuration using an independent bounded context; if restoration cannot be verified, it stops the managed kernel instead of claiming the old configuration is active. Node listeners are TCP-only (HTTP/SOCKS5 CONNECT, `udp: false`), so an occupied UDP port can no longer leave a half-bound listener behind.

## Nginx and reverse proxies

When Nginx proxies Codex or other clients that use underscore-containing headers, add this inside the `http` block:

```nginx
underscores_in_headers on;
```

Also configure `server.trusted_proxies` with only the proxy CIDRs that directly connect to Sub2API. See [`deploy/EDGE_SECURITY.md`](deploy/EDGE_SECURITY.md) before trusting forwarded client-IP headers.

## Documentation map

| Topic | Guide |
|---|---|
| Deployment and upgrades | [`deploy/README.md`](deploy/README.md) |
| Payment configuration | [`docs/PAYMENT.md`](docs/PAYMENT.md) |
| Composite groups | [`docs/COMPOSITE_GROUPS.md`](docs/COMPOSITE_GROUPS.md) |
| Asynchronous image tasks | [`docs/ASYNC_IMAGE_TASKS.md`](docs/ASYNC_IMAGE_TASKS.md) |
| Batch image processing | [`docs/BATCH_IMAGE_MVP.md`](docs/BATCH_IMAGE_MVP.md) |
| OpenAI Transport installation | [`docs/PLUGIN_INSTALLATION.md`](docs/PLUGIN_INSTALLATION.md) |
| Plugin development and package format | [`docs/PLUGIN_DEVELOPMENT.md`](docs/PLUGIN_DEVELOPMENT.md), [`backend/pkg/pluginapi/docs/`](backend/pkg/pluginapi/docs/) |
| Infinite Canvas | [`docs/infinite-canvas.md`](docs/infinite-canvas.md) |
| Edge and proxy security | [`deploy/EDGE_SECURITY.md`](deploy/EDGE_SECURITY.md) |
| Development | [`DEV_GUIDE.md`](DEV_GUIDE.md) |

## Project structure

```text
sub2api/
├── backend/                  # Go gateway, services, repositories, migrations
├── frontend/                 # Vue admin and user interface
├── plugins/openai-transport/ # Official OpenAI Transport plugin UI/runtime source
├── integrations/infinite-canvas/ # Canvas submodule
├── deploy/                   # Compose, binary, Apple container, and config files
├── docs/                     # Operator, security, payment, and plugin guides
└── scripts/                  # Build and integration helpers
```

## License

This project is licensed under the [GNU Lesser General Public License v3.0 or later](LICENSE).

The fork maintains the upstream license and attribution. Fork-specific code and documentation are distributed under the applicable repository license; see [`LICENSE`](LICENSE) and [`CLA.md`](CLA.md).

<div align="center">

**If this fork is useful, please star the repository and report reproducible issues.**

</div>
