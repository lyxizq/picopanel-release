[简体中文](./DEPLOY.md) · English

# PicoPanel · Deployment Guide

For deployment and operations: steps for the server (lz-server) and collector (lz-agent), a field-by-field config reference, start/stop script usage, and the node_exporter collection mode. Architecture and feature overview in [README_EN.md](README_EN.md), daily operations in [OPERATE.md](OPERATE.md, Chinese), feature list in [FEATURES.md](FEATURES.md, Chinese).

## 1. Overview

```
                        ┌─ Agent pull: GET /metrics (optional X-Server-Token) ──► lz-agent (:19100, read-only metrics port)
Browser ──► lz-server ───┼─ Agent push: lz-agent POSTs actively (X-Agent-Token, agent-initiated, no inbound port needed)
(:8080 Web/API)         ├─ node_exporter pull: GET /metrics ──► node_exporter (:9100, standard Prometheus text)
                        └─ SSH direct (retired, existing machines compatible): the server logs into targets and runs read-only commands
```

- **Server**: single binary (frontend packed in), single-file SQLite storage, zero dependencies. Recommended directory `/home/lz-monitor/server/`
- **Agent**: zero-dependency static build running on monitored machines, zero disk writes by default. Recommended directory `/home/lz-monitor/agent/`
- Server and agents are deployed in **separate directories on separate machines** — never mix them

## 2. Release artifacts

Run `make release` on the dev machine; it produces three deploy directories under `release/deploy/` (**no tarballs**):

| Directory | Contents | Upload to |
|---|---|---|
| `server/` | `lz-server.gz` + `server.yaml` + `lz-server.sh` | server machine `/home/lz-monitor/server/` |
| `agent-amd64/` | `lz-agent.gz` + `agent.yaml` + `lz-agent.sh` | x86_64 monitored machine `/home/lz-monitor/agent/` |
| `agent-arm64/` | same (arm64 build) | arm64 monitored machine `/home/lz-monitor/agent/` |

Binaries ship as gzip archives (~40% of the plain size in transfer); the start/stop script extracts automatically on `start` (first run) and `upgrade` — no manual gunzip.

## 3. Server deployment

### 3.1 First deployment

```bash
# Dev machine: after make release, sync the whole directory
rsync -a release/deploy/server/ user@server:/home/lz-monitor/server/

# Server machine:
cd /home/lz-monitor/server
./lz-server.sh start        # background start; extracts lz-server.gz and loads the server.yaml next to it
```

- The first start auto-creates the admin **`admin / admin123`** (logged); change it **immediately** after signing in (top-right "Profile")
- The database `lz-monitor.db` and log `lz-server.log` land in the deploy directory regardless of the calling location
- **Encryption key**: the first start generates `/etc/lm.idc` (credential encryption key, 0600). The process needs write access to that path (usually run as root). **It must be backed up together with the database** — once lost, encrypted SSH passwords/IPs are unrecoverable (re-entering credentials restores operation)
- Troubleshooting startup failures: read `/home/lz-monitor/server/lz-server.log` (startup errors land there too); if the config file is wrong, run `./lz-server -config server.yaml` in the foreground to see the error directly

### 3.2 Start/stop script (lz-server.sh)

```bash
/home/lz-monitor/server/lz-server.sh start      # start (no-ops with a hint if already running)
/home/lz-monitor/server/lz-server.sh stop       # stop (SIGTERM graceful exit, SIGKILL after 10s)
/home/lz-monitor/server/lz-server.sh restart    # restart
/home/lz-monitor/server/lz-server.sh status     # run status
/home/lz-monitor/server/lz-server.sh upgrade    # upgrade: extract lz-server.gz next to it → replace → restart
/home/lz-monitor/server/lz-server.sh restart -resetuser   # forgot password: reset to admin/admin123 then start normally
/home/lz-monitor/server/lz-server.sh restart -resettotp   # lost 2FA: clear TOTP then sign in with the password alone
```

- The script lives next to the binary and can be called from anywhere (paths resolve relative to the script directory); the PID is recorded in `lz-server.pid`
- Upgrades keep the old build as `lz-server.old`; rollback: `mv lz-server.old lz-server && ./lz-server.sh restart`
- Extra arguments after `start`/`restart` pass through to the binary (`-resetuser`, `-resettotp` use this; they apply only to that start)
- Autostart on boot: add the start command to `/etc/rc.local` or call the script from a systemd unit
- **Start via the script, never the binary directly**: in some virtualized environments (OpenVZ/LXC etc.) the Go runtime derives a CPU quota of 0 and crashes immediately; the script falls back to `GOMAXPROCS=<logical cores>` automatically

### 3.3 Config file server.yaml

All fields are optional; defaults are the values in the table. Placed in the deploy directory it loads automatically (or pass `-config`). Full comments in `deploy/server.example.yaml`.

| Field | Default | Description |
|---|---|---|
| `listen` | `:8080` | Web/API listen address |
| `db_path` | `lz-monitor.db` | SQLite file path (relative to the deploy directory) |
| `alert_keep_days` | `90` | Days to keep recovered alerts |
| `offline_after` | `2m` | A server is offline after this long without a successful scrape |
| `scrape_interval` | `10s` | Scrape cycle (shared by agent pull / node_exporter / SSH direct) |
| `scrape_timeout` | `10s` | Per-scrape / SSH execution timeout |
| `log_file` | `lz-server.log` | Rolling log file (foreground runs also print to the terminal) |
| `log_max_mb` | `300` | Total log size cap (current file + `.1`/`.2` backups share it; rotates automatically) |
| `snapshot_token` | empty | Token for the read-only snapshot/instant-query endpoints (for Homepage-style panels); empty = disabled. Generate: `openssl rand -hex 32` |
| `web_allow` | empty | Web access whitelist (empty = unrestricted): IP / CIDR / domain entries; only matches may reach pages and APIs; the agent report endpoint is not restricted |
| `real_ip_header` | empty | Real client IP header (empty = TCP peer): when behind an edge proxy/CDN, read the injected header (e.g. Aliyun ESA `Ali-Real-Client-Ip`, Tencent EdgeOne's custom origin header) |
| `trusted_proxies` | empty | Trusted proxy networks (IP/CIDR, pairs with the above): only headers injected by these peers are trusted. **Empty = the header is never trusted** (a direct connection can forge it to bypass login rate limits/IP whitelists — fail-closed): edge deployments must add their origin-facing networks here; if you can't, don't enable `real_ip_header` and restrict direct access to 8080 with a firewall instead |
| `cookie_secure` | `false` | Mark the session Cookie Secure (HTTPS-only transport): set `true` for HTTPS / Nginx+TLS / CDN deployments; keep `false` on plain-HTTP intranets (browsers drop the cookie and login breaks) |
| `update.sources` | ghfast mirror → gh-proxy mirror → `github:lyxizq/picopanel-release` | Online update source list, tried in order: `github:<user>/picopanel-release` (GitHub Releases) or a static directory URL (containing `manifest.json`+`.sig`); mirrors simply prefix the GitHub address. **Empty = feature off** (default off before v1.0.16) |
| `update.channel` | `stable` | `stable` = releases (the GitHub source uses releases/latest, prereleases excluded); `beta` = prereleases |
| `update.check_interval` | `30m` | Auto-check period (e.g. `24h`); `0` = manual check only on the "System" page |
| `update.agents` | `true` | After a new version is found, auto-upgrade your agents (signature-verified, dry-run, auto-rollback on failure; opt out per machine with `upgrade: false` in agent.yaml) |

Online updates are enabled by default (fresh installs ship with the three sources in `server.yaml`, since 2026-10-08): the "Online update" card on the web "System" page checks/applies updates in one click — automatic database backup (1 copy kept) → signature verification → dry-run of the new build (`-check-config`) → atomic replace with in-process restart; any failure keeps the current version, and a failed start after upgrade auto-rolls back to the old one (`.old` + an `upgrade.json` marker in the deploy directory). Existing deployments keep their `server.yaml` on upgrade; add `update.sources` to enable or change sources.

Public deployments: the server speaks plain HTTP — put Nginx/Caddy with TLS in front (**the web terminal uses WebSocket; the proxy must pass upgrade headers**, full config in chapter 6); change the agent's `server_url` to https accordingly.

## 4. Agent deployment

### 4.1 First deployment

```bash
# Dev machine: pick the directory by target arch (uname -m: x86_64 → amd64, aarch64 → arm64)
rsync -a release/deploy/agent-amd64/ user@target:/home/lz-monitor/agent/

# Monitored machine:
cd /home/lz-monitor/agent
vim agent.yaml             # edit per collect mode (below)
chmod 600 agent.yaml       # protect the token
./lz-agent.sh start
```

### 4.2 Two data egress modes (agent.yaml `mode`)

**pull (default)** — the agent opens a read-only metrics port and waits for the server to scrape; it never needs to know the server address and sends nothing out:

```yaml
mode: "pull"
listen: ":19100"     # metrics port for server scraping (full interface only for cross-machine scraping; use 127.0.0.1:19100 for same-machine/tunnel)
token: ""            # optional scrape token (sent by the server as X-Server-Token; shareable across machines); metrics include intranet info — set a token when listening on all interfaces
```

On the server web "Servers" → New server → pick **Agent pull**: address `<machine IP>:19100` (default port appended automatically), token matching agent.yaml (both may stay empty).

**push** — the agent POSTs to the server actively; for agents behind NAT/intranet the server cannot reach; opens no port:

```yaml
mode: "push"
server_url: "http://<server IP>:8080"   # public addresses must be https (http is only allowed for private/loopback targets, validated at agent start)
token: "<push token>"   # required, one-to-one with a server entry
```

On the server create the server entry with **Agent push** first, generate the push token (shown exactly once at creation; regenerate anytime in edit), and put it into agent.yaml's token.

### 4.3 Config file agent.yaml

Full comments in `deploy/agent.example.yaml`.

| Field | Default | Description |
|---|---|---|
| `mode` | `pull` | `pull` opens a read-only metrics port and waits; `push` reports to the server actively |
| `listen` | `127.0.0.1:19100` | pull-mode metrics port (the fileless default binds loopback only; for cross-machine scraping set `:19100` explicitly and set a token) |
| `server_url` | empty | required for push: server address (with port; behind a proxy write the https domain). **Public addresses must be https** — report responses carry task payloads, and plaintext over the public internet can be injected by a man-in-the-middle; http is only allowed for private/loopback targets |
| `interval` | `10s` | internal collect cycle (matches the server's 10s scrape rhythm; normally untouched) |
| `token` | empty | pull = scrape token (X-Server-Token; shareable or empty); push = report identity (X-Agent-Token, required, one-to-one) |
| `collect_listens` | `true` | collect listening ports. `false` stops reading and reporting them — the server's port list and "port listening" alerts pause (set back to true to resume) |
| `upgrade` | `true` | accept online upgrades (since v1.0.2): when the server pushes/distributes a new version, verify, replace and restart automatically (auto-rollback on failure). **pull-mode machines must configure `token`** — machines without a key refuse remote upgrades (prevents the metrics port from being abused for code execution); set `false` to opt this machine out |
| `log_file` | `off` | **zero logging by default**: writes no files, zero disk writes. For troubleshooting set `agent.log` (rolling, in the deploy directory) |
| `log_max_mb` | `300` | Total log size cap (same policy as the server) |

### 4.4 Start/stop script (lz-agent.sh)

```bash
/home/lz-monitor/agent/lz-agent.sh start      # start (extracts lz-agent.gz on first run)
/home/lz-monitor/agent/lz-agent.sh stop       # stop
/home/lz-monitor/agent/lz-agent.sh restart    # restart
/home/lz-monitor/agent/lz-agent.sh status     # run status
/home/lz-monitor/agent/lz-agent.sh upgrade    # upgrade: extract lz-agent.gz next to it → replace → restart (old build kept as lz-agent.old)
```

With zero logging, a failed start leaves nothing to inspect: run `/home/lz-monitor/agent/lz-agent` in the foreground temporarily to see the error, or set `log_file` to `agent.log` and restart.

## 5. node_exporter collection

Machines already running a Prometheus node_exporter **need no agent of this system** — the server scrapes their `/metrics` directly (standard Prometheus text format, whitelisted parsing mapped onto this system's metrics).

### 5.1 Install and run node_exporter on the target

Using the official binary as an example (distro packages like `prometheus-node-exporter` work the same way):

```bash
# Download & extract (pick the latest version from the website, linux-amd64/arm64 by arch)
tar xzf node_exporter-*.linux-amd64.tar.gz
cd node_exporter-*.linux-amd64
./node_exporter --web.listen-address=:9100     # 9100 is the default; add the flag for a custom port
```

A systemd service is recommended (`/etc/systemd/system/node_exporter.service`):

```ini
[Unit]
Description=node_exporter
After=network.target

[Service]
ExecStart=/opt/node_exporter/node_exporter --web.listen-address=:9100
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload && systemctl enable --now node_exporter
curl http://127.0.0.1:9100/metrics     # self-check: output means it works
```

### 5.2 Add the server on the web

Web "Servers" → New server → collect mode **node_exporter**:

- **Address**: `<target IP>:9100` (a standard port written in full is fine; an IP alone auto-appends the conventional default port `:10200` — non-standard ports must include the port)
- **Token (optional)**: node_exporter **itself validates nothing**; this field is sent with scrapes as the `X-Server-Token` header, **meant for a reverse proxy in front of the target** (see 5.3). Leave empty for a direct, unauthenticated exporter

### 5.3 Security notes (node_exporter /metrics is readable by anyone who can connect)

Pick one per environment:

1. **Firewall the source**: only the server IP may reach port 9100 on the target (simplest; recommended on intranets)
2. **Loopback only + authenticated proxy**: set node_exporter to `--web.listen-address=127.0.0.1:9100`, and put Nginx in front checking the `X-Server-Token` header:

   ```nginx
   server {
       listen 10200;
       location /metrics {
           if ($http_x_server_token != "your-secret") { return 403; }
           proxy_pass http://127.0.0.1:9100;
       }
   }
   ```

   Put the same "your-secret" in the server's token field
3. **Bind to the intranet interface**: `--web.listen-address=<intranet IP>:9100` — unreachable from the public internet entirely

### 5.4 Metric semantics (differences from this system's agent)

- The metric set matches the agent **exactly** (CPU/memory/disk capacity/disk IO/load/network/TCP connections/uptime)
- **No listening-port metrics** (node_exporter has no equivalent): the port list stays empty and "port listening" alerts skip automatically (equivalent to the agent's `collect_listens: false`)
- CPU/NIC/disk IO are cumulative counters in node_exporter; the server derives rates from two scrapes: **the first cycle after a server restart shows 0 for these; normal from the next cycle on**
- Upgrading node_exporter itself is unrelated to this system (replace the official binary and restart); the server needs no change

## 6. Public access (Nginx TLS + CDN)

Public deployments must terminate TLS on a fronting Nginx/Caddy (change agent push `server_url` to https accordingly). **The web terminal uses WebSocket** — besides the usual `proxy_pass` you must pass the upgrade headers, or the "Terminal" page cannot connect (HTTP APIs and pages are unaffected):

```nginx
server {
    listen 443 ssl;
    server_name monitor.example.com;
    ssl_certificate     /etc/nginx/cert.pem;
    ssl_certificate_key /etc/nginx/cert.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        # WebSocket passthrough (required by /api/v1/term/ws)
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        # long-lived terminal sessions: relax the read timeout (the server itself heartbeats every 90s and idles out at 30 minutes)
        proxy_read_timeout 10m;
        # SFTP transfers use large bodies — disable buffering to pass through
        proxy_buffering off;
        # file management upload/download of large files: relax read/write timeouts and allow large bodies
        # (the server already drops the 30s timeout per request for upload/download; Nginx must relax too)
        location /api/v1/term/files/ {
            proxy_pass http://127.0.0.1:8080;
            proxy_set_header Host $host;
            proxy_http_version 1.1;
            proxy_request_buffering off; # streaming upload passthrough, no spooling to disk on Nginx
            proxy_buffering off;
            proxy_read_timeout 1h;
            proxy_send_timeout 1h;
            client_max_body_size 0; # no upload size limit (tighten as needed)
        }
    }
}
```

Server-side SFTP upload/download already relaxes the default 30s timeout per request (slow large-file transfers are not cut off); with the Nginx settings above, large files over the public internet work too — if you still hit timeouts, prefer an scp/sftp client straight to the target.

### CDN / edge acceleration (Tencent EdgeOne etc.)

When traffic returns to origin through a CDN/edge accelerator, two more points apply on top of the Nginx notes above (EdgeOne as the example; other CDNs are similar):

1. **Enable WebSocket**: EdgeOne does not forward WebSocket upgrades by default and the terminal page keeps showing "disconnected, reconnecting..." (pages, HTTP APIs and file management stay fine — use that to tell it apart). In the console "Rule engine" create a rule (match the accelerated domain, or narrow it to the URL Path `/api/v1/term/ws`) with the action "WebSocket" enabled; set a generous timeout (the server heartbeats every 25s and the frontend every 30s, so defaults usually suffice)
2. **Real login IP**: a CDN only appends its node IP to the `X-Forwarded-For` chain, which the server does not trust (anti-spoofing, see 3.3 `real_ip_header`). Enable the CDN's "carry client IP header to origin" (EdgeOne's feature name) to inject the single client IP into a fixed header (e.g. `X-Real-IP`), set `real_ip_header: "X-Real-IP"` (matching the header name configured in the CDN), and add the CDN's officially published origin-facing networks into `trusted_proxies` (EdgeOne documents its origin IP ranges; entries or CIDRs both work), then restart. **Configuring `real_ip_header` without `trusted_proxies` means the header is not trusted** (fail-closed since 2026-10-06: anyone who knows the origin address could forge the header to impersonate IPs and bypass login lockouts/whitelists — better off than exposing it); if the CDN's origin networks truly cannot be pinned down, disable `real_ip_header` and restrict direct access to origin port 8080 with a firewall/security group (allowing only CDN networks) — login logs will then show CDN node IPs, but rate limiting and whitelists stay trustworthy

## 7. Ports & firewall list

| Machine | Port | Protocol/direction | Purpose | Allow from |
|---|---|---|---|---|
| Server | 8080/tcp | inbound | Web pages / API / agent push intake | admins + all push-mode agents |
| Monitored machine (agent pull) | 19100/tcp | inbound | read-only metrics port | server IP only |
| Monitored machine (agent push) | — | outbound → server 8080 | active reporting | no inbound |
| Monitored machine (node_exporter) | 9100/tcp (or custom) | inbound | /metrics for server scraping | server IP only (see 5.3) |
| Monitored machine (web terminal / SFTP; legacy SSH-direct collection uses the same port) | 22/tcp (or custom) | inbound | server SSH login: terminal / file management (SSH-direct collection retired; existing machines still collect through it) | server IP only |

## 8. Upgrades & rollback (summary)

**Method 1: online update (since v1.0.2, recommended)** — fresh installs ship with update sources configured in `server.yaml` (since 2026-10-08, see 3.3); machines deployed before v1.0.16 just add `update.sources` to server.yaml:

```bash
# Server: web "System" page → Online update card → "Check for updates" / "Apply update & restart"
#   (auto DB backup → signature check → dry-run → atomic replace → in-process restart; failures keep the old build, failed starts auto-rollback)
# Agents: nothing to do — new versions upgrade automatically (update.agents: true, default on);
#   or on the "Servers" page, upgrade per version column / "Upgrade All Agents"
```

- **Bootstrap**: versions older than v1.0.2 lack online updates — upgrade manually once via method 2 to v1.0.2, then stay online forever
- pull-mode machines must have `token` in agent.yaml for online upgrades (machines without a key are always refused — security policy)
- Old binaries are kept as `.old`; manual rollback is the same as method 2

**Method 2: manual upload (offline / fallback)**:

```bash
# Server: after rsync-ing the new lz-server.gz from the dev machine
/home/lz-monitor/server/lz-server.sh upgrade
# Rollback:
mv /home/lz-monitor/server/lz-server.old /home/lz-monitor/server/lz-server && /home/lz-monitor/server/lz-server.sh restart

# Agents likewise:
/home/lz-monitor/agent/lz-agent.sh upgrade     # rollback: mv lz-agent.old lz-agent then restart
```

- scp/rsync-overwriting a **running binary** fails with `Text file busy` — always go through `upgrade` (extract + verify first, then atomic replace; a corrupt package never interrupts service)
- Old databases migrate automatically on first start with a new version (idempotent; any old version can jump straight to the latest; large databases interrupt service for 1–3 minutes with progress logged; the online updater backs up the db first as `lz-monitor.db.pre-upgrade.1` — 1 copy kept)
- The frontend entry page is no-cache and static assets are content-hash long-cached, so a normal browser refresh after upgrade suffices (no hard refresh needed)
- Binary self-check flags: `./lz-server -version`, `./lz-server -check-config` (same for the agent) — the online updater's dry-run uses the latter
