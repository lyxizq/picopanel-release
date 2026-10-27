[English](./DEPLOY_EN.md) · 简体中文

# PicoPanel · 部署文档

面向部署运维：服务端（lz-server）与采集端（lz-agent）的部署步骤、配置文件逐项说明、启停脚本用法，以及 node_exporter 采集方式的使用。架构与功能全貌见 [README.md](README.md)，日常操作见 [OPERATE.md](OPERATE.md)，功能清单见 [FEATURES.md](FEATURES.md)。

## 1. 总览

```
                        ┌─ Agent 拉取：GET /metrics（可选 X-Server-Token）──► lz-agent (:19100，只读指标端口)
浏览器 ──► lz-server ───┼─ Agent 上报：lz-agent 主动 POST（X-Agent-Token，agent 侧发起，无需入站端口）
(:8080 Web/API)         ├─ node_exporter 拉取：GET /metrics ──► node_exporter (:9100，标准 Prometheus 文本)
                        └─ SSH 直连（已下线，存量机器兼容）：服务端登目标机执行只读命令
```

- **服务端**：单二进制（前端已打包进去），SQLite 单文件存储，零依赖。推荐部署目录 `/home/lz-monitor/server/`
- **Agent**：零依赖静态编译，被监控机上运行，对磁盘默认零写入。推荐部署目录 `/home/lz-monitor/agent/`
- 服务端与 agent **分目录、分机器**部署，互不混杂

## 2. 发布产物

开发机执行 `make release`，产出 `release/deploy/` 下三个部署目录（**不打 tar 包**）：

| 目录 | 内容 | 上传到 |
|---|---|---|
| `server/` | `lz-server.gz` + `server.yaml` + `lz-server.sh` | 服务端机 `/home/lz-monitor/server/` |
| `agent-amd64/` | `lz-agent.gz` + `agent.yaml` + `lz-agent.sh` | x86_64 被监控机 `/home/lz-monitor/agent/` |
| `agent-arm64/` | 同上（arm64 构建） | arm64 被监控机 `/home/lz-monitor/agent/` |

二进制以 gzip 压缩包分发（传输体积约为明文 40%）；启停脚本在 `start`（首次）与 `upgrade` 时自动解压，无需手工 gunzip。

## 3. 服务端部署

### 3.1 首次部署

```bash
# 开发机：make release 后整目录同步
rsync -a release/deploy/server/ user@server:/home/lz-monitor/server/

# 服务端机：
cd /home/lz-monitor/server
./lz-server.sh start        # 后台启动；自动解压 lz-server.gz、加载同目录 server.yaml
```

- 首次启动自动创建管理员 **`admin / admin123`**（日志有提示），登录后**立即改掉**（右上角「个人信息」）
- 数据库 `lz-monitor.db` 与日志 `lz-server.log` 都落在部署目录，与调用位置无关
- **加密密钥**：首次启动自动生成 `/etc/lm.idc`（凭据加密密钥，0600）。需要对该路径有写权限（一般用 root 运行）。**必须随数据库一起备份**，丢失后已加密的 SSH 密码/IP 无法恢复（重填凭据即可恢复使用）
- 启动失败排查：看 `/home/lz-monitor/server/lz-server.log`（启动错误也落此文件）；配置文件写错时前台运行 `./lz-server -config server.yaml` 直接看报错

### 3.2 启停脚本（lz-server.sh）

```bash
/home/lz-monitor/server/lz-server.sh start      # 启动（已在运行则提示，不重复拉起）
/home/lz-monitor/server/lz-server.sh stop       # 停止（SIGTERM 优雅退出，超 10s 强杀）
/home/lz-monitor/server/lz-server.sh restart    # 重启
/home/lz-monitor/server/lz-server.sh status     # 运行状态
/home/lz-monitor/server/lz-server.sh upgrade    # 升级：解压同目录 lz-server.gz → 替换 → 重启
/home/lz-monitor/server/lz-server.sh restart -resetuser   # 忘记密码：重置 admin/admin123 后正常启动
/home/lz-monitor/server/lz-server.sh restart -resettotp   # 两步验证丢失：清除 TOTP 后密码直接登录
```

- 脚本与二进制同目录存放，任意位置调用均可（路径相对脚本目录解析）；PID 记录在 `lz-server.pid`
- 升级旧版保留为 `lz-server.old`；回滚：`mv lz-server.old lz-server && ./lz-server.sh restart`
- `start`/`restart` 后的额外参数原样透传给二进制（`-resetuser`、`-resettotp` 即此机制，只作用于本次启动）
- 开机自启：把 start 命令加入 `/etc/rc.local` 或自建 systemd unit 调用脚本
- **请通过脚本启动，不要直接运行二进制**：部分虚拟化环境（OpenVZ/LXC 等）Go 运行时会把 CPU 配额推导为 0 直接崩，脚本自动回退 `GOMAXPROCS=逻辑核数` 规避

### 3.3 配置文件 server.yaml

全部字段可选，缺省即下表默认值。放部署目录即自动加载（也可 `-config` 指定）。完整注释见 `deploy/server.example.yaml`。

| 字段 | 默认值 | 说明 |
|---|---|---|
| `listen` | `:8080` | Web/API 监听地址 |
| `db_path` | `lz-monitor.db` | SQLite 文件路径（相对部署目录） |
| `alert_keep_days` | `90` | 已恢复告警保留天数 |
| `offline_after` | `2m` | 超过该时长无成功抓取判定离线 |
| `scrape_interval` | `10s` | 抓取周期（Agent 拉取 / node_exporter / SSH 直连共用） |
| `scrape_timeout` | `10s` | 单次抓取/SSH 执行超时 |
| `log_file` | `lz-server.log` | 滚动日志文件（前台运行时终端同步输出） |
| `log_max_mb` | `300` | 日志总占用上限（当前文件 + `.1`/`.2` 两个备份均摊，超限自动轮转） |
| `snapshot_token` | 空 | 只读快照/即时查询接口令牌（Homepage 等面板轮询用）；空 = 不开通。生成：`openssl rand -hex 32` |
| `web_allow` | 空 | Web 访问白名单（空 = 不限制）：IP / CIDR / 域名条目，仅命中者可访问页面与 API；agent 上报接口不受限 |
| `real_ip_header` | 空 | 真实客户端 IP 请求头（空 = 取 TCP 对端）：部署在边缘代理/CDN 后面时读代理注入的头（如阿里 ESA `Ali-Real-Client-Ip`、腾讯 EdgeOne 回源自定义头） |
| `trusted_proxies` | 空 | 可信代理网段（IP/CIDR，配合上一条）：仅这些对端注入的头被采信。**留空 = 该头一律不采信**（直连可伪造该头绕过登录限流/IP 白名单，fail-closed）——边缘代理部署必须把回源网段配进来，配不了就别开 `real_ip_header`，改用防火墙限制 8080 直连 |
| `cookie_secure` | `false` | 会话 Cookie 加 Secure 属性（仅 HTTPS 可传）：HTTPS / Nginx+TLS / CDN 部署改 `true`，纯 HTTP 内网保持 `false`（开了浏览器不带 Cookie 无法登录） |
| `update.sources` | ghfast 镜像 → gh-proxy 镜像 → `github:lyxizq/picopanel-release` | 在线更新源列表，按序回退：`github:用户名/picopanel-release`（GitHub Releases）或静态目录 URL（内含 `manifest.json`+`.sig`）；镜像加速直接把前缀拼在 GitHub 地址前。**改空 = 功能关闭**（v1.0.16 前默认关闭） |
| `update.channel` | `stable` | `stable` = 正式版（GitHub 源取 releases/latest，自动排除预发布）；`beta` = 预发布版 |
| `update.check_interval` | `30m` | 自动检查周期（如 `24h`）；`0` = 仅后台「系统信息」页手动检查 |
| `update.agents` | `true` | 检出新版后自动升级名下各台 agent（agent 升级经验签+试运行，失败自动回滚；单机可在 agent.yaml `upgrade: false` 退出） |

在线更新默认即启用（新装解包 `server.yaml` 自带三源，2026-10-08 起）：后台「系统信息」页在线更新卡可一键检查/应用更新——自动备份数据库（保留 1 份）→ 校验签名 → 试运行新版本（`-check-config`）→ 原子替换并同进程重启；任何一步失败保留当前版本，升级后启动失败自动回滚旧版（`.old` + 部署目录 `upgrade.json` 标记）。已有部署升级时配置文件不被覆盖，沿用现网 `server.yaml`；想启用/换源把 `update.sources` 配上即可。

公网部署：服务端为明文 HTTP，请在前面加 Nginx/Caddy 反代并配置 TLS（**Web 终端用 WebSocket，反代必须透传升级头**，完整配置见第 6 章）；agent 的 `server_url` 同步改 https。

## 4. Agent 部署

### 4.1 首次部署

```bash
# 开发机：按目标机架构选目录（uname -m：x86_64 → amd64，aarch64 → arm64）
rsync -a release/deploy/agent-amd64/ user@target:/home/lz-monitor/agent/

# 被监控机：
cd /home/lz-monitor/agent
vim agent.yaml             # 按采集模式改（见下）
chmod 600 agent.yaml       # 保护 token
./lz-agent.sh start
```

### 4.2 两种数据出口（agent.yaml 的 mode）

**pull 拉取模式（缺省）**——agent 开只读指标端口等服务端来抓，不需要知道服务端地址、不外发任何数据：

```yaml
mode: "pull"
listen: ":19100"     # 服务端抓取的指标端口（跨机抓取才用全接口；同机/隧道用 127.0.0.1:19100）
token: ""            # 可选抓取密钥（服务端以 X-Server-Token 携带；多台可共用）；指标含内网信息，全接口监听时建议配置
```

服务端 Web「服务器管理」→ 新建服务器 → 选 **Agent 拉取**：地址填 `<该机IP>:19100`（缺省端口自动补 `:19100`），token 与 agent.yaml 一致（都留空也行）。

**push 上报模式**——agent 主动 POST 到服务端，适合 agent 在内网/NAT 后、服务端无法主动访问的场景；不开任何端口：

```yaml
mode: "push"
server_url: "http://<服务端IP>:8080"   # 公网地址必须 https（http 仅允许私网/回环目标，agent 启动时校验）
token: "<上报密钥>"   # 必填，与服务器一对一
```

服务端先新建服务器选 **Agent 上报**，生成上报密钥（仅创建时展示一次，可随时在编辑里重新生成），填进 agent.yaml 的 token。

### 4.3 配置文件 agent.yaml

完整注释见 `deploy/agent.example.yaml`。

| 字段 | 默认值 | 说明 |
|---|---|---|
| `mode` | `pull` | `pull` 只读指标端口等抓取；`push` 主动上报服务端 |
| `listen` | `127.0.0.1:19100` | pull 模式指标端口（无配置文件的默认只绑回环；跨机抓取显式配 `:19100` 并建议配 token） |
| `server_url` | 空 | push 模式必填：服务端地址（含端口，反代后写 https 域名）。**公网地址必须 https**——上报响应携带任务载荷，明文公网传输可被中间人注入；http 仅允许私网/回环目标 |
| `interval` | `10s` | 内部采集周期（与服务端 10s 抓取节奏一致，一般不动） |
| `token` | 空 | pull = 抓取密钥（X-Server-Token，可共用可留空）；push = 上报身份（X-Agent-Token，一对一必填） |
| `collect_listens` | `true` | 是否采集监听端口。`false` 后不读端口也不上报——服务端端口列表与「端口监听」类告警停更（改回 true 即恢复） |
| `upgrade` | `true` | 是否接受在线升级（v1.0.2 起）：服务端推送/下发新版本时自动验签、替换并重启（失败自动回滚旧版）。**pull 模式务必配置 `token`**——未配密钥的机器一律拒绝远程升级（防指标端口被利用执行任意代码）；置 `false` 本机退出自动升级 |
| `log_file` | `off` | **默认零日志**：不写任何文件、磁盘零写入。排查问题时改 `agent.log`（滚动写入部署目录） |
| `log_max_mb` | `300` | 日志总占用上限（同服务端口径） |

### 4.4 启停脚本（lz-agent.sh）

```bash
/home/lz-monitor/agent/lz-agent.sh start      # 启动（首次自动解压 lz-agent.gz）
/home/lz-monitor/agent/lz-agent.sh stop       # 停止
/home/lz-monitor/agent/lz-agent.sh restart    # 重启
/home/lz-monitor/agent/lz-agent.sh status     # 运行状态
/home/lz-monitor/agent/lz-agent.sh upgrade    # 升级：解压同目录 lz-agent.gz → 替换 → 重启（旧版留 lz-agent.old）
```

默认零日志时启动失败无文件可查：临时前台运行 `/home/lz-monitor/agent/lz-agent` 看报错，或把 `log_file` 改 `agent.log` 后重启。

## 5. node_exporter 采集方式

已有 Prometheus node_exporter 的机器，**无需安装本系统 agent**，服务端直接拉取其 `/metrics`（标准 Prometheus 文本格式，白名单解析映射为本系统指标）。

### 5.1 目标机安装运行 node_exporter

以官方二进制为例（各发行版仓库的 `prometheus-node-exporter` 包同理）：

```bash
# 下载解压（版本按官网最新，架构按机器选 linux-amd64/arm64）
tar xzf node_exporter-*.linux-amd64.tar.gz
cd node_exporter-*.linux-amd64
./node_exporter --web.listen-address=:9100     # 默认即 9100；自定义端口加该参数
```

建议做成 systemd 服务（`/etc/systemd/system/node_exporter.service`）：

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
curl http://127.0.0.1:9100/metrics     # 自验有输出即可
```

### 5.2 服务端添加服务器

Web「服务器管理」→ 新建服务器 → 采集方式选 **node_exporter**：

- **地址**：`<目标机IP>:9100`（标准端口写全即可；只写 IP 时系统自动补约定的缺省端口 `:10200`——非标准端口务必带端口）
- **密钥（可选）**：node_exporter **本体不校验任何密钥**；此字段以 `X-Server-Token` 请求头随抓取发送，**供目标机前面的反向代理校验用**（见 5.3）。直连无认证的 exporter 留空

### 5.3 安全建议（node_exporter 的 /metrics 对任何能连上的人都可读）

三选一，按环境取：

1. **防火墙限制来源**：目标机只放行服务端 IP 访问 9100 端口（最简单，内网推荐）
2. **只监听本机 + 反代加认证**：node_exporter 改 `--web.listen-address=127.0.0.1:9100`，前面挂 Nginx 按 `X-Server-Token` 头放行：

   ```nginx
   server {
       listen 10200;
       location /metrics {
           if ($http_x_server_token != "你的密钥") { return 403; }
           proxy_pass http://127.0.0.1:9100;
       }
   }
   ```

   服务端该机器的密钥栏填同一个「你的密钥」
3. **bind 到内网接口**：`--web.listen-address=<内网IP>:9100`，公网完全不可达

### 5.4 指标口径说明（与本系统 agent 的差异）

- 指标范围与 agent **同口径**（CPU/内存/磁盘容量/磁盘 IO/负载/网络/TCP 连接/运行时长）
- **无监听端口指标**（node_exporter 无对应项）：端口列表恒空、「端口监听」类告警自动跳过（等同 agent 关 `collect_listens`）
- CPU/网卡/磁盘 IO 在 node_exporter 里是累计计数器，服务端按两次抓取差值计算速率：**服务端重启后的第一个周期这几项显示 0，下一周期起正常**
- node_exporter 自身的升级与本系统无关（官方新版替换二进制重启即可），服务端无需改动

## 6. 公网接入（Nginx 反代 TLS + CDN）

公网部署必须在前置 Nginx/Caddy 配 TLS（agent push 模式的 `server_url` 同步改 https）。**Web 终端用 WebSocket**，除常规 `proxy_pass` 外必须透传升级头，否则「终端」页连不上（HTTP API 与页面不受影响）：

```nginx
server {
    listen 443 ssl;
    server_name monitor.example.com;
    ssl_certificate     /etc/nginx/cert.pem;
    ssl_certificate_key /etc/nginx/cert.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        # WebSocket 透传（/api/v1/term/ws 需要）
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        # 终端会话长连接：放宽读超时（服务端自身有 90s 心跳检测与 30 分钟空闲断开）
        proxy_read_timeout 10m;
        # SFTP 上传下载走大响应体，关闭缓冲直通
        proxy_buffering off;
        # 文件管理上传/下载大文件：放宽读写超时并允许大请求体
        #（服务端对 upload/download 已按请求取消 30s 超时，Nginx 侧同样需要放宽）
        location /api/v1/term/files/ {
            proxy_pass http://127.0.0.1:8080;
            proxy_set_header Host $host;
            proxy_http_version 1.1;
            proxy_request_buffering off; # 上传流式直通，不在 Nginx 落盘缓冲
            proxy_buffering off;
            proxy_read_timeout 1h;
            proxy_send_timeout 1h;
            client_max_body_size 0; # 不限制上传体积（按需收紧）
        }
    }
}
```

SFTP 上传/下载服务端已按请求放宽 30s 默认超时（大文件慢速传输不会被掐断）；Nginx 侧按上面配置放宽后公网传大文件也可用，仍超时的话建议改用 scp/sftp 客户端直连目标机。

### CDN / 边缘加速（腾讯 EdgeOne 等）

经 CDN/边缘加速回源时，除上面的 Nginx 要点外还有两处（以 EdgeOne 为例，其他 CDN 同理）：

1. **开启 WebSocket**：EdgeOne 默认不转发 WebSocket 升级，终端页会一直「连接断开，正在重连...」（页面与 HTTP API、文件管理均正常，据此可区分）。控制台「规则引擎」创建规则（匹配该加速域名，或收敛到 URL Path `/api/v1/term/ws`），执行动作开启「WebSocket」，超时建议设大（服务端每 25s、前端每 30s 各有心跳保活，默认超时一般也够用）
2. **真实登录 IP**：CDN 默认只在 `X-Forwarded-For` 链追加节点 IP，服务端不采信该链（防伪造，见 3.3 `real_ip_header`）。需在 CDN 开启「携带客户端 IP 头部回源」（EdgeOne 功能名），把单个客户端 IP 注入固定头（如 `X-Real-IP`），再配 `real_ip_header: "X-Real-IP"`（头名与 CDN 里填的一致），并把 CDN 官方公布的回源网段加进 `trusted_proxies`（EdgeOne 回源 IP 段有官方文档，IP 条目/网段均可）后重启。**只配 `real_ip_header` 不配 `trusted_proxies` 时该头不会被采信**（2026-10-06 起 fail-closed：任何知道源站地址的人都能伪造这个头冒充他人 IP 绕过登录锁定/白名单，宁可不启用也不能裸奔）；CDN 回源网段确实拿不到固定列表时，关闭 `real_ip_header` 并用防火墙/安全组限制对源站 8080 的直连（只放行 CDN 网段）——登录日志里会是 CDN 节点 IP，但限流与白名单仍然可信

## 7. 端口与防火墙清单

| 机器 | 端口 | 协议/方向 | 用途 | 放行范围 |
|---|---|---|---|---|
| 服务端 | 8080/tcp | 入站 | Web 页面 / API / agent 上报接收 | 管理者 + 全部 push 模式 agent |
| 被监控机（agent pull） | 19100/tcp | 入站 | 只读指标端口 | 仅服务端 IP |
| 被监控机（agent push） | — | 出站→服务端 8080 | 主动上报 | 无入站 |
| 被监控机（node_exporter） | 9100/tcp（或自定义） | 入站 | /metrics 供服务端抓取 | 仅服务端 IP（见 5.3） |
| 被监控机（Web 终端 / SFTP；存量 SSH 直连采集同口） | 22/tcp（或自定义） | 入站 | 服务端 SSH 登录：终端 / 文件管理（SSH 直连采集已下线，存量机器仍走此口采集） | 仅服务端 IP |

## 8. 升级与回滚（汇总）

**方式一：在线更新（v1.0.2 起，推荐）**——新装解包 `server.yaml` 已默认配好更新源（2026-10-08 起，见 3.3 节）；v1.0.16 前部署的存量机器在 server.yaml 配上 `update.sources` 即可：

```bash
# 服务端：后台「系统信息」页 → 在线更新卡 →「检查更新」/「应用更新并自动重启」
#   （自动备库 → 验签 → 试运行 → 原子替换 → 同进程重启；失败保留旧版，启动失败自动回滚）
# agent：无需操作——检出新版后自动升级（update.agents: true，缺省开）；
#   或「服务器管理」页按版本列单台/「升级全部 Agent」
```

- **Bootstrap**：v1.0.2 之前的版本没有在线更新能力，先按方式二手动升一次到 v1.0.2，之后永久在线
- pull 模式机器要在线升级必须在 agent.yaml 配 `token`（未配密钥一律拒绝，安全口径）
- 旧版二进制保留为 `.old`，手动回滚同方式二

**方式二：手动传包（离线/兜底）**：

```bash
# 服务端：开发机 rsync 新的 lz-server.gz 上传后
/home/lz-monitor/server/lz-server.sh upgrade
# 回滚：
mv /home/lz-monitor/server/lz-server.old /home/lz-monitor/server/lz-server && /home/lz-monitor/server/lz-server.sh restart

# agent 同理：
/home/lz-monitor/agent/lz-agent.sh upgrade     # 回滚 mv lz-agent.old lz-agent 后 restart
```

- 直接 scp/rsync 覆盖**正在运行的二进制**会报 `Text file busy`，必须走 `upgrade`（先解压校验再原子替换，包损坏服务不中断）
- 旧数据库首次用新版启动会自动迁移（幂等，任意旧版本可直跳最新；大库期间服务中断 1~3 分钟，日志可见进度；升级前在线更新会自动备库 `lz-monitor.db.pre-upgrade.1`（仅保留 1 份））
- 前端入口页 no-cache、静态资源按内容哈希长缓存，升级后浏览器普通刷新即可（无需强刷）
- 二进制自检参数：`./lz-server -version`、`./lz-server -check-config`（agent 同）——在线更新的试运行即用后者
