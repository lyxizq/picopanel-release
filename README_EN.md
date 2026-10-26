[简体中文](./README.md) · English

# PicoPanel

<div align="center">

![PicoPanel icon](web/public/apple-touch-icon.png)

</div>

PicoPanel is a Linux server monitoring system: the server (Go + SQLite, single binary with an embedded web frontend) periodically collects metrics from each server, evaluates alert rules and stores everything in a database. After signing in from a browser you can view real-time status and history charts for multiple servers. It also ships with a web SSH terminal, file management (SFTP) and a docs module; 12 built-in themes (light/dark/violet/dark gold/teal/rose/glacier/ember/moss/graphite/neon/abyss) with mobile-friendly browsing.

Documentation: this file covers the feature overview; **per-module daily operations are in [OPERATE.md](OPERATE.md) (operations manual, Chinese)**; deployment and upgrades are in [DEPLOY_EN.md](DEPLOY_EN.md); the detailed feature list is in [FEATURES.md](FEATURES.md, Chinese).

Three collection modes, selectable per server and mixable:

- **Agent pull** (default): the monitored machine runs lz-agent exposing a read-only metrics port (`:19100`), and the server scrapes it periodically. Best for machines where you can install the agent — it is statically compiled with zero dependencies and opens no write endpoints.
- **Agent push**: the agent actively POSTs metrics to the server, so the machine opens no port. Best for agents behind NAT/intranet where the server cannot reach them.
- **node_exporter pull**: the target machine only runs the Prometheus-standard node_exporter; the server scrapes its `/metrics` and maps it to the same metric set. Best for machines already running node_exporter — no need to install lz-agent.

> SSH-direct collection was retired (2026-10-04): the creation entry has been removed; existing SSH machines remain compatible (collection and web terminal keep working). For machines where an agent is impractical, node_exporter is recommended.

## Features

- **Zero-dependency deployment**: single server binary (frontend packed via go:embed), single-file SQLite storage, no database installation needed
- **Metrics collected**: CPU, memory/Swap (same semantics as Prometheus/node_exporter: MemTotal − MemAvailable, corrected by cgroup limits in containers), load (1/5/15), disks (per-mount usage and IO usage — IO is the iostat %util metric, latest value only with no history), NIC up/down rates, TCP connection count, listening ports, host info and uptime
- **Web SSH terminal + file management**: open a shell on target machines straight from the browser (xterm.js with multiple tabs; full-screen programs like vim/htop work; sessions auto-suspend on network blips/proxy timeouts and reconnect without loss; command history + quick-command completion popup; bottom server info bar; closing a tab asks for confirmation; one-click "reset fingerprint and reconnect" when the host fingerprint changes); a dedicated file management page (breadcrumbs/multi-select/context menu/drag-and-drop upload/resumable transfers/chunked large-file upload/archive extraction/image preview/system directory protection) — see "Web terminal & SFTP usage"
- **Multi-server management**: groups with dual views (module cards / row list), purchase & expiry countdown (color shifts when near due), custom data cards and "my charts" on the monitoring page with inline editing; server spec "N cores / N GB" shown in two places — next to uptime on monitor cards (replacing the old load value) and after the name in the server detail dialog (C = CPU cores, G = memory GB rounded up with decimals; missing data is simply omitted)
- **Threshold alerts**: metric + operator + threshold + duration, global or per-server rules; offline detection; full trigger/recovery history, delivered via WxPusher (WeChat), generic webhooks (custom JSON templates for DingTalk/Feishu/Slack bots) or browser Web Push, and always recorded in the in-site message center; **notification batching** — when multiple devices alert/recover within a short window (60s silence or a full 10-minute window) they merge into one notification (same for in-site and push, listing every server and detail) to avoid flooding your phone when a batch of servers goes offline, at the cost of ~1 minute extra delay for a single isolated alert
- **History charts**: per-server detail page from 5 minutes to 30 days with automatic downsampling; click a chart to zoom and pick a custom window; network traffic renders as mirrored up/down area charts
- **HTTPS certificate monitoring**: hourly TLS probes for the domain list, top-bar reminders 15 days before expiry, optional daily expiry push with in-site reminder messages
- **To-dos**: modeled on Microsoft To Do — left list navigation (My day / Important / Planned / Done + colored group icons and an ungrouped bucket), quick add and a full form, important star, detail sidebar; **subtasks up to 5 levels deep** (independent to-dos; deleting a parent moves the whole tree to trash, restoring restores the chain — descendants and trashed ancestors come back; a fully deleted ancestor chain breaks the link and everything below becomes top-level); repeat rules (daily/weekly/monthly/yearly/custom period with month-end convergence) auto-generate the next occurrence on completion and completions can be reverted; delete to trash with restore / permanent delete / empty; three-stage due reminders (1 hour before / at due time / 1 day overdue, per-item "remind or not" toggle) always land in the in-site message center and fan out to push channels per switches; unfinished items render in red in messages
- **In-site message center**: a dedicated page (reachable from the sidebar menu / top-bar dropdown / home stat cards; unread badge, type filter, read management; "Messages | Alerts" tabs with alert rules and history in the alerts tab) aggregating alert triggers/recoveries, certificate expiry, to-dos, inspection reports, login reminders, membership expiry, mail and TV-show messages; in-site records are unaffected by push channel switches (login reminders carry account/time/source IP, same as external push)
- **Push settings**: the "Notifications" page in settings centralizes three channels, each with a master switch — configuring and pushing require the channel to be on; turning it off blocks configuration and stops pushes; Web Push auto-generates VAPID keys and one-click subscribes the current browser (HTTPS required)
- **Toolbox**: 40+ pure-frontend mini tools (URL / encoding / utilities; all computation is local, nothing touches the server) + TCP/UDP connectivity checks (server-initiated probes with real TCP handshakes and timing, IP-rate-limited against abuse)
- **Service integrations & home weather**: Homepage-widget-style integrations with Home Assistant / qBittorrent / Emby (server-proxied polling + caching to avoid CORS and token exposure; credentials stored encrypted and never echoed back); the home "Services" glass card shows four stats — qB download/upload rates and seeding count, Emby library counts and now-playing, HA entity states (auto-split into cards every 4 items); home weather capsule (Gaode / Open-Meteo dual providers — Gaode is more accurate in China and needs a free key, see the weather section of FEATURES.md; Open-Meteo works keyless; visitor-IP auto-location or manual city, 3-day forecast bubble), all configured in the home manage drawer
- **More console modules**: **service monitor** (server → port two-level TCP probes with per-port name/status/latency/uptime, manual check for one or all); **mail** (multi-account IMAP/SMTP, locally cached inbox opens instantly, send/attachments/unread badge); **database** (read-only MySQL/PostgreSQL/SQLite queries and full backups, statement whitelist prevents writes); **usage query / GitHub release watch** (tabs inside the service monitor page; usage query covers Zhipu Coding Plan window usage / DeepSeek balance / Tencent EdgeOne traffic, bandwidth, requests and protection hits, polled by the server); **PT sites** (26-site catalog + browser cookie import, daily 06:00 scrape of upload/download/ratio/seeding/bonus, M-Team via the official API); **smart home** (dedicated full-screen /ha page: controllable device tiles + temperature/humidity sensor entities + room sections, the landing page for restricted users); **server status** (runtime metrics/logs/connectivity self-check). Feature list in FEATURES.md, per-module operations in [OPERATE.md](OPERATE.md)
- **Docs**: group management (drag docs across groups in the left panel, drag to reorder within a group) + rich-text/Markdown dual format (autosave, 10 history versions, trash), see "Docs module usage"
- **Experience**: 12 themes (light/dark/violet/dark gold/teal/rose/glacier/ember/moss/graphite/neon/abyss, remembered; dark themes apply global glassmorphism, and the home & smart-home pages follow the theme), mobile drawer navigation, multi-tab switching that keeps state (restored from local storage)
- **Web login**: username/password (bcrypt); first start auto-creates `admin/admin123`; forgotten password can be reset with `-resetuser`; TOTP two-factor can be enabled (Authenticator QR scan), and `-resettotp` clears it so the password alone works again

## Screenshots

**Home page** — glassmorphism cards, monitoring module and service integrations, custom link groups

![Home page](docs/screenshots/home.png)

**Console overview** — data monitoring tiles, custom charts and grouped server lists

![Console overview](docs/screenshots/console.png)

**Server detail** — multi-dimensional history charts, disk mounts and listening ports

![Server detail](docs/screenshots/server-detail.png)

**Smart home** — Home Assistant room sections, device tiles and appliances

![Smart home](docs/screenshots/smart-home.png)

## Architecture

```
                          ┌─ Agent pull: periodic GET /metrics (optional X-Server-Token) ──► lz-agent (:19100, read-only metrics port)
lz-server (manually started) ──────┼─ node_exporter pull: periodic GET /metrics text ──► node_exporter (:9100, standard Prometheus format)
(10s scrape cycle)         ├─ SSH direct (retired, existing machines compatible): key login, long-lived connection running read-only commands ──► target sshd
                          └─ Agent push: ◄── lz-agent periodically POSTs /api/v1/agent/report (X-Agent-Token, no inbound port needed)
        ├─ Scrape manager (pull/direct → assemble a unified Report → write DB → alert evaluation; push reports enter the same evaluation path at the API)
        ├─ Alert engine (rule cache / duration evaluation / offline detection / notification batching)
        ├─ REST API (web session auth; the report endpoint uses X-Agent-Token)
        └─ SQLite (WAL) + go:embed frontend
```

- Agent pull: the agent only opens a read-only port, sends nothing out and never needs to know the server address; it keeps sampling on `interval` internally so counter-derived metrics stay fresh
- Agent push: set `mode: "push"` + `server_url` + `token` in `agent.yaml` (the push token is **one-to-one with a server entry** — the server enforces uniqueness; create one entry per machine, each with its own token); offline detection is based on the last report time
- **SSH direct** (retired, existing machines compatible): one long-lived connection reused across cycles (a single session channel per cycle); CPU/NIC counter metrics are derived server-side from cumulative counters; host fingerprint is TOFU (recorded on first connect, rejected on change)
- node_exporter pull: the server parses whitelisted metrics into the same metric set (no listening-port metric — port lists and port alerts skip automatically). CPU/NIC/IO are cumulative counters differentiated server-side, so the first cycle after a server restart shows 0; LVM logical volumes (`/dev/mapper/…`) have no correspondence with kernel device names in /metrics, so IO shows 0 for such mounts. An optional secret is sent as `X-Server-Token` (node_exporter itself ignores it — meant for a reverse proxy in front)
- The server scrapes every 10s by default (pull/direct share `scrape_interval`); timestamps are always the server receive time
- Scalar metrics live in columns, disk/NIC lists in JSON columns, with `UNIQUE(server_id, ts)` idempotent writes; the last 7 days keep 10s details and older data is auto-downsampled into 5-minute average buckets, cleaned hourly
- Retention days are changed at runtime on the web "Settings" page (default 90 days), not in config files

## Quick start

### 1. Build

Requirements: Go 1.26+, Node 18+ (build time only; no Node dependency at runtime).

```bash
make server    # build frontend + server → bin/lz-server
make agent     # local agent (macOS lacks /proc; for debugging only)
make release   # Linux cross-compile → release/deploy/{server,agent-amd64,agent-arm64}/
```

### 2. Start the server

The start/stop script is recommended (see the "Deployment" section below); for quick debugging you can run it in the foreground:

```bash
./bin/lz-server                    # or -config server.yaml (see deploy/server.example.yaml)
```

The first start auto-creates the admin `admin / admin123` (logged) — **change the username and password immediately under the top-right "Profile"**.

### 3. Add monitored servers

1. Open `http://<server>:8080` in a browser and sign in
2. "Servers" → New server, pick one of three (SSH direct is retired and no longer offered; existing SSH machines stay compatible):
   - **Agent pull**: fill in the name and the agent machine's address (e.g. `192.168.1.5:19100`; the default port is appended automatically), plus an optional access token (must match the agent.yaml token; multiple machines may share one token); then deploy the agent on the target machine (next section)
   - **Agent push**: fill in the name and the push token (click "Generate" or leave empty to let the server generate one; one token per server); deploy the agent on the target machine with `mode: "push"`, `server_url: "http://<server IP>:8080"` and `token: <push token>` in agent.yaml
   - ~~**SSH direct**~~ (retired, no new entries; existing ssh machines keep collecting and the web terminal works)
   - **node_exporter**: fill in the name and the target's node_exporter address (e.g. `192.168.1.5:10200`; the default port is appended automatically — if the exporter runs on the standard `:9100`, just write the full address). Metrics match this system's agent (no listening-port metric)
3. (Optional) **Web terminal / file management**: machines other than "Agent push" automatically appear in the "Terminal" and "Files" pages using the configured address (agent/node modes take the scrape address host plus `:22`); you can also add a terminal-specific address and credentials in "Edit server" — monitoring via agent and terminal via SSH are independent; missing credentials trigger an input dialog
4. (Optional) **Configure alerts**: on the "Alerts" page → "Alert rules" tab, create rules — pick a metric (CPU/memory/disk/NIC/load/TCP connections/port listening), a threshold, duration in minutes (0 = immediate) and scope (global or one server). Disk/NIC rules can pin a specific mount/interface; empty means "max across all" ("any disk full" semantics). Port rules are written as `tcp/443`, `udp/53` (a bare number defaults to tcp), with "alert when not listening" (service port closed unexpectedly) or "alert when listening" (a port that should not be open), requiring agent ≥ v0.3 (older agents skip silently, no false alarms). Triggers and recoveries show in the "Alert history" tab; notification channels are configured under "Settings → Notifications" — WxPusher (WeChat), generic webhooks (custom JSON template with {{title}}/{{message}} placeholders) or browser Web Push; all three coexist, each with a master switch (on before configuring/pushing) and independent switches for alert/certificate/to-do reminders. The "Settings" page also offers a one-click inspection summarizing online/firing/expiring certs/top resources, pushed manually or on a daily schedule (via enabled channels)

## Web terminal & SFTP usage

The sidebar has two separate pages, "Terminal" and "Files": the terminal page is a grouped server list + multi-tab shell; the files page is a grouped server list + a BaoTa-style file area (SFTP). The left panel on both pages is a **grouped accordion** (each group has its own accent color; click a group header to collapse/expand; only the first group is expanded by default). **Every server except "Agent push" appears in the list** — addresses are taken from the server config (terminal-specific address first; agent/node modes take the scrape address host plus `:22`; IPv6 must be written as `[::1]:22`), no per-machine setup needed.

### Credentials (same logic for terminal and files)

- **Username + password (or key) configured**: connects directly
- **Only username configured**: a password dialog pops up with an optional "save password" — once saved, future connects are direct and it can be cleared in "Edit server"; if not saved the password lives only in server process memory (re-enter after restart)
- **Neither configured**: a username + password dialog pops up; the username is always saved to the server config so only the password is needed next time
- Wrong password: click "re-enter password" on the terminal tab overlay, or the "Credentials" button on the files page
- **Host fingerprint (TOFU)**: the target's fingerprint is recorded on first successful connection and changes are rejected afterwards (anti-MITM); after reinstalling a machine, reset the fingerprint via the terminal failure overlay's "reset fingerprint and reconnect" one-click action, or tick "reset fingerprint" in "Edit server"
- Changing SSH info in the server config or deleting a server invalidates existing SFTP connections immediately (they rebuild)

### Terminal

- Servers grouped in the left panel/dropdown; click to open a terminal tab, multiple machines at once; **switching tabs never drops the session** (connections stay alive); closing a tab disconnects
- A full interactive shell: `vim` / `htop` / `top` and other full-screen programs work; the window resizes adaptively
- **Completion popup**: while typing, candidates appear under the cursor (per-server local history + built-in common commands); ↑↓ to navigate, →/Tab/click to accept, Esc to close
- **Bottom server info bar**: CPU/memory/disk/Swap mini bars, load, network up/down, disk read/write, uptime (30s polling, independent of the terminal connection)
- **Session suspend/reconnect**: when the WebSocket drops unexpectedly (proxy timeout, backgrounded tab killed, network blip) the SSH session is suspended server-side for 10 minutes — SSH and PTY keep running, output is buffered and replayed, and the frontend reconnects with backoff; only a page refresh or suspension timeout actually reclaims it
- Session security: the WebSocket re-validates the login session every 30 seconds — **signing out or changing the password kicks all terminals**; idle connections close after 30 minutes without traffic

### File management (dedicated page, SFTP)

Pick a server in the left panel to enter the BaoTa-style file area (SFTP connections are pooled server-side; the files page does not need a terminal):

- **Navigation**: breadcrumb clicks jump to any level + "up / refresh" buttons + keyword filter for the current directory
- **List**: large font + file type icons (directories/links/devices/images/videos/audio/archives/scripts/docs each colored); **type** and **owner** columns are sortable; actions are icon buttons
- **Upload**: pick multiple files for serial streaming upload, or **drag files straight from your desktop into the area**; when the target directory already has a file with the same name you are asked whether to overwrite (declining skips that file and continues with the rest). The remote side writes to a `<name>.lzpart` temp file first and atomically renames on completion; the bottom-right upload panel shows **live progress (transferred / total / speed)**
- **Download**: file row button / context menu / **double-click (starts after confirmation)** — via the in-app download panel with live progress below the bar; the file is saved through the browser once complete
- **Resumable upload**: after cancel or failure the remote keeps the `.lzpart` fragment and the task stays in the panel — click "Resume" to continue from the breakpoint; "clear records" also removes the remote fragment
- **Large files & archives**: single-file upload limit 5G (8MB chunks uploaded sequentially, bypassing proxy body limits); double-click an archive to extract (tar/tar.gz/tgz/zip, background task with progress and cancel, same-name conflict pre-check; Chinese filenames in Windows-created zips are auto-corrected); pack the selection or a multi-select into tar.gz for download
- **Online editing & preview**: Monaco editor (auto language detection, formatting, Ctrl+S to save; binaries refuse editing, .txt opens as text); double-click images to preview; content search (regex/recursive/extension filter) and recursive find-by-name
- **New file / new folder** (multi-level) and **rename** (overwrites same-name targets, `mv`-like)
- **Delete**: files delete directly; directories count recursively first then confirm ("contains N files, M subdirectories"); **system directory protection** — `/`, `/etc`, `/usr`, `/var`, `/boot` and similar cannot be deleted themselves, and entries inside system directories require a strong confirmation before deletion (`/home`, `/opt`, `/srv`, `/tmp` are user-managed and not blocked); multi-select batch delete supported; the context menu can download/rename/delete too
- **Right-click selects**: right-clicking an unselected row selects it before opening the menu (rows already in a multi-select keep the whole batch, so batch context actions work)
- **Properties dialog**: directories are not counted by default — click "calculate" to start a background recursive count of entries and size (streamed live, cancellable, not bound by HTTP timeouts; unreadable subdirectories are skipped and marked "not counted"), so huge directories no longer fail
- Uploading large files (hundreds of MB+) through a public reverse proxy requires relaxed timeouts at every layer (see DEPLOY_EN.md "Public access"); otherwise prefer connecting with an scp/sftp client directly to the target

When used behind a public reverse proxy you must configure TLS and pass through WebSockets (see DEPLOY_EN.md "Public access"); everything typed in the terminal (including sudo passwords) travels that path.

### Credential & key storage (masked)

- The `/servers` API **never echoes** SSH passwords or keys — only "configured or not" booleans; in the edit dialog an empty password/key field **means "keep unchanged"**, and deletion requires ticking "clear" (push-mode tokens cannot be cleared, only regenerated via "generate new token")
- When a push server is created, the token is **shown exactly once** (copy it into agent.yaml); no page ever shows it again
- Passwords typed in the terminal/files pages without "save" live only in server process memory and must be re-entered after a server restart

## Deployment

> The full deployment manual (release artifacts, server and agent steps, every server.yaml / agent.yaml field, the five script commands, node_exporter usage, public Nginx/CDN access, port & firewall list, upgrade/rollback summary) is in **[DEPLOY_EN.md](DEPLOY_EN.md)**; this section keeps only the shortest path.

`make release` produces three deploy directories under `release/deploy/` (gzip-compressed binaries + config templates + start/stop scripts, **no tarballs**), deployed separately for the server and agents:

```
/home/lz-monitor/
├── server/    # lz-server.gz + server.yaml + lz-server.sh (server machine)
└── agent/     # lz-agent.gz  + agent.yaml  + lz-agent.sh (monitored machines, pick agent-amd64 / agent-arm64 by arch)
```

```bash
# Dev machine: upload
rsync -a release/deploy/server/ user@server:/home/lz-monitor/server/
rsync -a release/deploy/agent-amd64/ user@target:/home/lz-monitor/agent/

# Server machine:
cd /home/lz-monitor/server
./lz-server.sh start     # background start; lz-monitor.db and lz-server.log both live in this directory

# Monitored machine: edit agent.yaml for your collect mode first (pull/push examples and all fields in DEPLOY_EN.md) and chmod 600
cd /home/lz-monitor/agent && ./lz-agent.sh start
```

- **Always start via the scripts, never the binary directly**: in some virtualized environments (OpenVZ/LXC etc.) the Go runtime derives a CPU quota of 0 and the binary crashes on start; the script falls back to `GOMAXPROCS=<logical cores>` automatically
- Forgot the password: `./lz-server.sh restart -resetuser` (resets to admin/admin123 and continues starting); lost 2FA: `restart -resettotp` (clears TOTP so the password alone works)
- **Upgrades**: a running process holds the binary inode — scp/rsync overwrite fails with `Text file busy`; upload the new `.gz` and run `./lz-server.sh upgrade` (extract → replace → restart, old build kept as `.old`; roll back by `mv`-ing it back and `restart`; same for agents)
- Old databases migrate automatically on first start with a new version (idempotent; large databases interrupt service for ~1–3 minutes with progress logged)

## Security notes

- **Change the default password**: the initial `admin/admin123` must be changed immediately; 5 consecutive login failures lock the source IP for 10 minutes (anti online brute-force)
- **Sensitive fields encrypted at rest**: server IPs (agent_addr/ssh_addr), SSH usernames, SSH passwords and tokens are stored AES-256-GCM encrypted (ciphertext prefixed `enc:v1:`; legacy plaintext migrates automatically on start). The key is a 32-byte random value **generated automatically on first server start** and written to the fixed path **`/etc/lm.idc`** (base64 text, 0600; auto-created if missing; the path is hardcoded, not in config). Note: **this key file must be backed up together with the database** — once lost, encrypted passwords/IPs are unrecoverable (the symptom is servers unreachable; re-entering credentials fixes it). It protects against the database file alone leaking (a copied backup or stolen db); whoever has root on the server can still read the key — the same boundary as every local-encryption scheme
- **Docs module trade-off**: document content (rich-text HTML / Markdown preview) is not HTML-sanitized — this system is single-admin, self-written self-read, with no cross-user injection surface; do not paste untrusted HTML into documents. Images in both editors are stored base64-inline (1MB per image, 20MB per document; link large screenshot sets externally); the Markdown editor's highlighting / Mermaid / formula assets are localized into the single binary and work offline. Multiple windows editing one document get an **optimistic lock** — saves carry a base version; if another window saved first the save is rejected (409) with a dialog to "overwrite remote" or "load latest"
- **Network boundary**: the server speaks plain HTTP; deploy it on an untrusted network only. For public access put Nginx/Caddy with TLS in front (reverse proxy & CDN configs in DEPLOY_EN.md "Public access"; change the agent's `server_url` to https accordingly). An optional `web_allow` whitelist restricts access sources (IP/CIDR/domains)
- **Agent metrics port**: the agent opens a read-only port on the monitored machine (default `:19100`) that anyone who can reach it can read — the same model as node_exporter; restrict sources with a firewall or set a `token` in agent.yaml (enter the same value when creating/editing the server) so scrape requests must present `X-Server-Token`. `chmod 600 agent.yaml` recommended
- **The agent has no intrusive surface**: it exposes exactly one read-only exact route `GET /metrics` (zero parameter parsing, no file reads, no command execution, CGO_ENABLED=0 static build) and accepts no writes; push mode listens on no port at all, purely outbound. Without a token the worst case is readable metrics (hostname, traffic — reconnaissance-grade info); there is structurally no privilege escalation; with a token, reads return 401. Footprint: <0.1% of one CPU core, 10–15MB resident memory — fine to run forever
- **SSH credential privilege trade-off**: SSH credentials equal a full shell on the target — a compromised server reaches every SSH-connected machine, fundamentally different from agent mode (read-only metrics). Although SSH-direct collection is retired, the web terminal/SFTP SSH config in "Edit server" (and existing ssh collection machines) still logs into targets via SSH. Recommendation: create a dedicated low-privilege account per target; prefer key auth (password auth is supported and stored encrypted) with the key file `chmod 600`
- **SSH host fingerprint (TOFU)**: recorded on first successful connection, changes rejected afterwards (anti-MITM); after a target reinstall, reset via the terminal failure overlay's "reset fingerprint and reconnect" or the "Edit server" checkbox
- **Extra notes for web terminal / file management**: terminal sessions use the same credentials as SSH collection (saved passwords are stored encrypted; unsaved ones live in server process memory only); the API never echoes passwords/keys (only "configured or not"); SFTP delete is **recursive** (a content-count confirmation precedes directory deletion; over 200k entries are refused with a hint to batch), and rename is overwrite-semantics — operate carefully
- Built-in protections: login rate limiting, constant-time login comparison (no username enumeration via timing), constant-time agent push token comparison, optimistic locking for multi-window doc editing (409 conflict), optional web access whitelist, fully parameterized SQL, HTTP request timeouts, 1MB scrape response cap, server-side timestamps (historical data cannot be forged), static assets from embed FS (no path traversal), site-wide security headers (nosniff / SAMEORIGIN / Referrer-Policy), doc attachment MIME whitelist inline (others force download). HTTPS deployments can set `cookie_secure`; the edge proxy real-IP `real_ip_header` only takes effect together with `trusted_proxies` networks (unconfigured networks are never trusted, preventing direct-connection spoofing) — see DEPLOY_EN.md 3.3
- Known trade-off: being able to read both the database file **and** the key file (`/etc/lm.idc`) is already a higher level of compromise (e.g. server root taken); for stronger isolation consider encrypting the disk holding the database

## Homepage integration (read-only API)

[Homepage](https://gethomepage.dev) and similar always-on panels cannot hold a web login session (7-day expiry), so two read-only endpoints protected by `snapshot_token` (empty = disabled; set in server.yaml, all fields in DEPLOY_EN.md) are provided:

1. Generate a token and write it to server.yaml (restart to apply): `openssl rand -hex 32` → `snapshot_token: "<token>"`
2. Verify (token in the `X-Snapshot-Token` header, never in the URL — query parameters end up in access/proxy logs):
   `curl -H 'X-Snapshot-Token: <token>' 'http://<server>:8080/api/v1/snapshot?server=<name or ID>'`
   `curl -H 'X-Snapshot-Token: <token>' 'http://<server>:8080/api/v1/query?query=lz_cpu{server="<name or ID>"}'`

**Snapshot endpoint** `/api/v1/snapshot?server=<name or ID>` returns all metrics of one machine at once — good for single-server widgets (Homepage `customapi`). Flat fields returned: `cpu`/`mem`/`swap` (usage %), `mem_used`/`mem_total` (bytes), `mem_h` ("2.0 GB / 4.0 GB"), `load1/5/15`, `disk` (max usage across mounts), `io` (max IO usage), `net_rx`/`net_tx` (combined B/s, filtered by the server's "display NICs" config), `net_rx_h`/`net_tx_h` (preformatted "1.2 Mbps"), `tcp_est`, `uptime`/`uptime_h`, `online`, `firing`, `ts`. Minimal example:

```yaml
- Server monitoring:
    - web-01:
        widget:
            type: customapi
            url: http://10.2.0.1:8080/api/v1/snapshot?server=web-01
            headers:
                X-Snapshot-Token: <token>
            refreshInterval: 30000
            mappings:
                - { field: cpu, label: CPU, format: percent }
                - { field: mem_h, label: Mem }
                - { field: net_rx_h, label: Down }
                - { field: net_tx_h, label: Up }
```

The `server` parameter takes the **name** shown on the servers page (or the numeric ID from the detail page URL); `refreshInterval` is milliseconds — keep it no lower than the server scrape cycle.

**Instant query endpoint** `/api/v1/query` mimics a Prometheus instant query, good for mixing multiple servers in one widget or grabbing occasional values (Homepage's ready-made `prometheusmetric` widget, one query per metric row). Query syntax: `lz_<metric>{server="<name or ID>",token="<token>",mount="<mount>",device="<NIC>"}`. Metric list:

| Query | Meaning | Optional labels |
|---|---|---|
| `lz_cpu` / `lz_mem` / `lz_swap` | CPU / memory / Swap usage % | |
| `lz_mem_used` / `lz_mem_total` | Memory usage bytes | |
| `lz_load1` / `lz_load5` / `lz_load15` | Load | |
| `lz_disk` | Disk usage % | `mount` (default: max across all) |
| `lz_io` | Disk IO usage % | `mount` (default: max across all) |
| `lz_net_rx` / `lz_net_tx` | NIC rate B/s | `device` (default: filtered by the server's NIC config) |
| `lz_tcp_est` | TCP connections | |
| `lz_uptime` | Uptime seconds | |
| `lz_online` / `lz_firing` | Online (1/0) / firing alert count | |

Note: the `prometheusmetric` widget cannot set custom headers, so the token must go into the query string's `token` label — **that token will land in access/proxy logs**, a known trade-off: use a dedicated `snapshot_token` and never reuse other credentials; curl/scripts should prefer the `X-Snapshot-Token` header (not logged). The token can read read-only metrics of every server — its exposure is comparable to a web account; do not commit it to public repositories; the endpoints return only the latest snapshot, never history.

## Development

```bash
make test                        # go vet + all unit tests
cd web && npm run dev            # frontend dev mode (proxies /api → :8080)
./bin/lz-agent -listen :19100   # local integration (macOS shows 0s — /proc is unavailable there by design)
```

Layout: `cmd/` holds the two entrypoints; `internal/proto` the snapshot contract (including /proc line parsing, shared by the agent and legacy SSH collection); `internal/agent` collection and the metrics port (collect/ scrapers with fixture tests); `internal/server` hosts store (two-tier SQLite: 7 days of 10s details + older 5-minute average buckets), scrape (scrape manager), engine (alert engine), api (REST + static assets), ui (embed). The Vue3 + Element Plus + ECharts frontend lives in `web/`.

## License

This project is open source under the [MIT License](LICENSE).
