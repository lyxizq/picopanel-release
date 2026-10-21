# PicoPanel 部署文档

> 本文档基于 **v1.0.36**（2026-10-20 发布）编写，适用于该版本及之后经在线更新获得的版本。
> 面向从 [Releases](../../releases) 下载部署的用户：全流程不需要源码与编译环境，产物零依赖。

PicoPanel 是 Linux 服务器监控系统：服务端（Go + SQLite，单二进制内嵌 Web 前端）周期采集各服务器指标、评估告警并落库，浏览器登录查看实时状态与历史曲线，另带 Web SSH 终端、文件管理（SFTP）、待办、文档等模块。

- **服务端 `lz-server`**：单二进制（前端已打包），SQLite 单文件存储，监听 `:8080`，零外部依赖
- **采集端 `lz-agent`**：零依赖静态编译，对磁盘默认零写入；`pull` 模式开只读指标端口 `:19100`，`push` 模式不开任何端口
- 已有 Prometheus node_exporter 的机器可免装 agent，服务端直接拉取其 `/metrics`

三种采集方式按服务器独立选择、可混用；SSH 直连采集已下线（存量机器兼容），新机器请用 agent 或 node_exporter。

## 1. 环境要求

| 项目 | 要求 |
|---|---|
| 系统 | Linux x86_64（amd64）或 arm64（aarch64） |
| 依赖 | 无（静态编译；不需要数据库、运行时、容器） |
| 权限 | 服务端建议 root 运行（需写 `/etc/lm.idc` 凭据加密密钥，见 3.4）；agent 普通用户即可 |
| 浏览器 | 现代浏览器（Chrome/Edge/Firefox/Safari），支持手机浏览 |

## 2. 下载与校验

### 2.1 下载

每个 Release 含 5 个资产：`lz-server.gz`、`lz-agent-amd64.gz`、`lz-agent-arm64.gz`、`manifest.json`（各文件 sha256 与尺寸）、`manifest.json.sig`（ed25519 签名）。二进制以 gzip 分发，传输体积约为明文 40%。

```bash
# 最新版直链（github.com/lyxizq/picopanel-release）
curl -LO https://github.com/lyxizq/picopanel-release/releases/latest/download/lz-server.gz
curl -LO https://github.com/lyxizq/picopanel-release/releases/latest/download/lz-agent-amd64.gz   # x86_64 被监控机
curl -LO https://github.com/lyxizq/picopanel-release/releases/latest/download/lz-agent-arm64.gz   # aarch64 被监控机
curl -LO https://github.com/lyxizq/picopanel-release/releases/latest/download/manifest.json

# 国内网络可用镜像加速：在完整 GitHub 地址前拼镜像前缀，例如
curl -LO https://ghfast.top/https://github.com/lyxizq/picopanel-release/releases/latest/download/lz-server.gz
```

架构按目标机 `uname -m` 选：`x86_64` → amd64，`aarch64` → arm64。

### 2.2 完整性校验（推荐）

```bash
sha256sum lz-server.gz
# 输出与 manifest.json 中 assets.server.sha256 一致即完整（agent 两文件同理）
```

### 2.3 签名验签（可选）

在线更新链路内置同款 ed25519 验签（公钥烙在程序里），手工校验供离线/审计场景。发布公钥（hex，64 字符）：

```
e058e593c29fc784fa4dfce200a8c377fbd14f85b4405568c6a0f362b9288a0d
```

校验方法（需 OpenSSL 1.1.1+ 与 xxd，以下命令已实测）：

```bash
# 下载签名文件后，把上面公钥串存为 picopanel.pub，然后：
printf '302a300506032b6570032100%s' "$(tr -d ' \n' < picopanel.pub)" | xxd -r -p > pub.der
openssl pkey -pubin -inform DER -in pub.der -out pub.pem
xxd -r -p manifest.json.sig > sig.bin
openssl pkeyutl -verify -pubin -inkey pub.pem -rawin -in manifest.json -sigfile sig.bin
# 输出 "Signature Verified Successfully" 即通过
```

## 3. 服务端部署

### 3.1 安装

```bash
mkdir -p /opt/picopanel/server && cd /opt/picopanel/server
# 把下载的 lz-server.gz 放到本目录，然后：
gunzip lz-server.gz && chmod +x lz-server
./lz-server -version        # 自检：打印版本号即正常
```

### 3.2 配置文件 server.yaml（全部字段可选，不配置也能跑）

新建 `/opt/picopanel/server/server.yaml`（与二进制同目录自动加载）。推荐至少保留 `update` 段——**配了它才有在线更新**（服务端自升级 + agent 自动分发）：

```yaml
listen: ":8080"            # Web/API 监听地址
db_path: lz-monitor.db     # SQLite 文件路径（相对本目录）

# 在线更新源（按序回退）。GitHub 源取 releases/latest；前两条为国内镜像加速
update:
  sources:
    - https://ghfast.top/https://github.com/lyxizq/picopanel-release/releases/latest/download
    - https://gh-proxy.com/https://github.com/lyxizq/picopanel-release/releases/latest/download
    - github:lyxizq/picopanel-release
  channel: stable          # stable=正式版；beta=预发布版
  check_interval: 30m      # 自动检查周期；不配=仅后台「系统信息」页手动检查
  agents: true             # 检出新版后自动升级名下 agent
```

<details>
<summary>全部字段一览（点击展开）</summary>

| 字段 | 默认值 | 说明 |
|---|---|---|
| `listen` | `:8080` | Web/API 监听地址 |
| `db_path` | `lz-monitor.db` | SQLite 文件路径（相对部署目录） |
| `alert_keep_days` | `90` | 已恢复告警保留天数 |
| `offline_after` | `2m` | 超过该时长无成功抓取判定离线 |
| `scrape_interval` | `10s` | 抓取周期（Agent 拉取 / node_exporter 共用） |
| `scrape_timeout` | `10s` | 单次抓取超时 |
| `log_file` | `lz-server.log` | 滚动日志文件 |
| `log_max_mb` | `50` | 日志总占用上限（当前文件 + 2 个备份均摊，超限自动轮转） |
| `snapshot_token` | 空 | 只读快照/即时查询接口令牌（Homepage 等面板轮询用）；空 = 不开通。生成：`openssl rand -hex 32` |
| `web_allow` | 空 | Web 访问白名单（空 = 不限制）：IP / CIDR / 域名条目，仅命中者可访问；agent 上报接口不受限 |
| `real_ip_header` | 空 | 真实客户端 IP 请求头（空 = 取 TCP 对端）：部署在边缘代理/CDN 后面时读代理注入的头 |
| `trusted_proxies` | 空 | 可信代理网段（IP/CIDR）：仅这些对端注入的头被采信。**留空 = 该头一律不采信**（防伪造，fail-closed）——边缘代理部署必须把回源网段配进来，配不了就别开 `real_ip_header` |
| `cookie_secure` | `false` | 会话 Cookie 加 Secure 属性：HTTPS / 反代 TLS / CDN 部署改 `true`，纯 HTTP 内网保持 `false` |
| `update.sources` | 空（关闭） | 在线更新源列表，见上方示例：`github:用户名/picopanel-release` 或静态目录 URL；镜像加速把前缀拼在 GitHub 地址前 |
| `update.channel` | `stable` | `stable` = 正式版；`beta` = 预发布版 |
| `update.check_interval` | `30m` | 自动检查周期（如 `24h`）；`0` = 仅后台手动检查 |
| `update.agents` | `true` | 检出新版后自动升级名下各台 agent（失败自动回滚；单机可在 agent.yaml `upgrade: false` 退出） |

</details>

### 3.3 systemd 服务（推荐开机自启）

新建 `/etc/systemd/system/picopanel-server.service`：

```ini
[Unit]
Description=PicoPanel server
After=network.target

[Service]
WorkingDirectory=/opt/picopanel/server
ExecStart=/opt/picopanel/server/lz-server
Restart=on-failure
RestartSec=3
# 部分虚拟化环境（OpenVZ/LXC 等）Go 运行时会把 CPU 配额推导为 0 启动即崩，
# 遇到时取消下行注释（改成实际逻辑核数）
#Environment=GOMAXPROCS=2
# 专用户运行需预建可写的 /etc/lm.idc（见 3.4），否则保持 root
#User=picopanel

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload && systemctl enable --now picopanel-server
systemctl status picopanel-server        # active (running) 即成功
curl -s http://127.0.0.1:8080 -o /dev/null -w '%{http_code}\n'   # 200/30x 即正常
```

不想用 systemd 也可直接前台运行 `./lz-server`（读同目录 `server.yaml`）或自行 nohup。

### 3.4 首次启动

- 自动创建管理员 **`admin / admin123`**（日志有提示），浏览器打开 `http://<服务端IP>:8080` 登录后**立即在右上角「个人信息」改掉**
- 首次启动自动生成 **`/etc/lm.idc`**（凭据加密密钥，权限 0600，一般需 root 运行）。**该文件必须随数据库一起备份**——丢失后已加密的 SSH 密码/IP 无法恢复（重新填写凭据可恢复使用）
- 数据库 `lz-monitor.db` 与日志 `lz-server.log` 都在部署目录（`WorkingDirectory`），备份这两个文件加 `/etc/lm.idc` 即可整体迁移
- 防火墙放行 8080（清单见第 5 节）；公网部署务必加 TLS 反代（见第 6 节）

## 4. 添加被监控机

服务端 Web「服务器管理」→ 新建服务器，三选一；再到被监控机装对应采集端。

### 4.1 方式一：Agent 拉取（默认，推荐）

agent 在被监控机开只读指标端口 `:19100` 等服务端来抓，不外发任何数据、不需要知道服务端地址。

1. 服务端新建服务器 → 选 **Agent 拉取**：地址填 `<被监控机IP>:19100`（只写 IP 自动补 `:19100`），可选抓取密钥（多台可共用）
2. 被监控机安装（架构按 `uname -m` 选包）：

```bash
mkdir -p /opt/picopanel/agent && cd /opt/picopanel/agent
# 上传 lz-agent-amd64.gz（或 arm64）后：
gunzip lz-agent-amd64.gz && mv lz-agent-amd64 lz-agent && chmod +x lz-agent

cat > agent.yaml <<'EOF'
mode: "pull"
listen: ":19100"
interval: 10s
token: ""            # 与服务端该机器配置的抓取密钥一致；都留空也行
upgrade: true        # 接受服务端下发的自动升级；pull 模式要远程升级必须配 token
log_file: "off"      # 默认零日志零写盘；排查问题时改 agent.log
EOF
chmod 600 agent.yaml
```

3. systemd 服务 `/etc/systemd/system/picopanel-agent.service`：

```ini
[Unit]
Description=PicoPanel agent
After=network.target

[Service]
WorkingDirectory=/opt/picopanel/agent
ExecStart=/opt/picopanel/agent/lz-agent
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
```

```bash
systemctl daemon-reload && systemctl enable --now picopanel-agent
```

4. 防火墙放行：**仅服务端 IP 可访问 19100**。agent 指标端口与 node_exporter 同模型——能连上就能读指标，请用防火墙限制来源，或配置 `token`（配了之后读取需携带密钥，401 拦截）
5. （可选）Web 终端 / 文件管理：该机器会自动出现在「终端(SSH)」「文件(SFTP)」两页（采集地址 host 拼 `:22`），首次连接补输 SSH 用户名密码即可；监控走 agent、终端走 SSH 互不影响

### 4.2 方式二：Agent 上报（agent 在内网/NAT 后、服务端无法主动访问时）

agent 主动 POST 到服务端，被监控机不开任何入站端口：

1. 服务端新建服务器 → 选 **Agent 上报**：填名称，点「生成」上报密钥（**仅创建时展示一次**，可随时在编辑里重新生成）
2. 被监控机 agent.yaml 改为：

```yaml
mode: "push"
server_url: "http://<服务端IP>:8080"   # 经反代 HTTPS 时写 https 域名
interval: 10s
token: "<服务端生成的上报密钥>"          # 与服务器一对一，必填
```

其余安装步骤同 4.1（免第 4 步防火墙，纯出站）。上报模式机器不出现在 Web 终端/文件页（服务端无法主动 SSH 进内网）。

### 4.3 方式三：node_exporter（已有 Prometheus exporter 的机器，免装 agent）

服务端新建服务器 → 选 **node_exporter**：地址填 `<目标机IP>:9100`（exporter 跑在标准 `:9100` 时地址写全即可；**只写 IP 会自动补缺省端口 `:10200`**，非标准端口务必带端口）。指标与 agent 同口径（无监听端口指标，端口类展示与告警自动跳过）。

安全提示（node_exporter 的 /metrics 对任何能连上的人都可读，三选一）：

1. 防火墙只放行服务端 IP 访问 9100（最简单，内网推荐）
2. exporter 只监听 `127.0.0.1`，前置 Nginx 校验 `X-Server-Token` 头（服务端「密钥」栏填同一值）
3. bind 到内网接口，公网不可达

### 4.4 agent.yaml 全字段

| 字段 | 默认值 | 说明 |
|---|---|---|
| `mode` | `pull` | `pull` 只读指标端口等抓取；`push` 主动上报服务端 |
| `listen` | `:19100` | pull 模式指标端口 |
| `server_url` | 空 | push 模式必填：服务端地址（含端口） |
| `interval` | `10s` | 内部采集周期，与服务端抓取节奏一致，一般不动 |
| `token` | 空 | pull = 抓取密钥（可共用可留空）；push = 上报身份（一对一必填）。**pull 模式要在线升级必须配置**——未配密钥的机器一律拒绝远程升级 |
| `collect_listens` | `true` | 是否采集监听端口；`false` 后端口列表与「端口监听」类告警停更 |
| `upgrade` | `true` | 是否接受在线升级（自动验签、替换并重启，失败自动回滚）；`false` 本机退出 |
| `log_file` | `off` | **默认零日志**、磁盘零写入；排查问题时改 `agent.log` |
| `log_max_mb` | `300` | 日志总占用上限 |

agent 资源占用：CPU <0.1% 单核、常驻内存 10~15MB；仅暴露 `GET /metrics` 一个只读路由，无命令执行、无文件读取、不接收写操作。

## 5. 端口与防火墙清单

| 机器 | 端口 | 方向 | 用途 | 放行范围 |
|---|---|---|---|---|
| 服务端 | 8080/tcp | 入站 | Web 页面 / API / agent 上报接收 | 管理者 + 全部 push 模式 agent |
| 被监控机（agent pull） | 19100/tcp | 入站 | 只读指标端口 | 仅服务端 IP |
| 被监控机（agent push） | — | 出站 → 服务端 8080 | 主动上报 | 无入站 |
| 被监控机（node_exporter） | 9100/tcp（或自定义） | 入站 | /metrics 供服务端抓取 | 仅服务端 IP |
| 被监控机（Web 终端 / SFTP） | 22/tcp（或自定义） | 入站 | 服务端 SSH 登录：终端 / 文件管理 | 仅服务端 IP |

## 6. 公网接入（反代 TLS）

服务端自身为明文 HTTP，公网部署必须在前置 Nginx/Caddy 配 TLS，agent push 模式的 `server_url` 同步改 https，`cookie_secure` 改 `true`。**Web 终端用 WebSocket**，除常规反代外必须透传升级头，否则「终端」页连不上（页面与 HTTP API 不受影响，据此可区分）：

```nginx
server {
    listen 443 ssl;
    server_name monitor.example.com;
    ssl_certificate     /etc/nginx/cert.pem;
    ssl_certificate_key /etc/nginx/cert.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        # WebSocket 透传（Web 终端需要）
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        # 终端长连接：放宽读超时（服务端自身有心跳与 30 分钟空闲断开）
        proxy_read_timeout 10m;
        proxy_buffering off;
    }

    # 文件管理上传/下载大文件：流式直通并放宽超时
    location /api/v1/term/files/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_http_version 1.1;
        proxy_request_buffering off;
        proxy_buffering off;
        proxy_read_timeout 1h;
        proxy_send_timeout 1h;
        client_max_body_size 0;
    }
}
```

经 CDN/边缘加速（腾讯 EdgeOne、阿里 ESA 等）时另有两处：

1. **开启 WebSocket 转发**（EdgeOne 默认不开），否则终端页一直重连
2. **真实登录 IP**：在 CDN 开启「携带客户端 IP 回源」（注入固定头如 `X-Real-IP`），再配 `real_ip_header: "X-Real-IP"`（头名与 CDN 一致）并把 **CDN 官方回源网段加进 `trusted_proxies`** 后重启。只配 `real_ip_header` 不配 `trusted_proxies` 时该头不会被采信（防伪造，fail-closed）；回源网段拿不到固定列表时，关闭该头并改用防火墙/安全组只放行 CDN 网段访问源站 8080

## 7. 升级与回滚

**方式一：在线更新（推荐）**——`server.yaml` 配好 `update.sources` 后：

```bash
# 服务端：后台「系统信息」页 → 在线更新卡 →「检查更新」/「应用更新并自动重启」
#   （自动备份数据库 → 校验签名 → 试运行 → 原子替换 → 同进程重启；失败保留旧版，启动失败自动回滚）
# agent：无需操作——检出新版后由服务端自动升级（update.agents: true，缺省开）
```

**方式二：手动替换（离线/兜底）**：

```bash
# 下载新 .gz 解压后，务必用 mv 原子替换（cp 直接覆盖运行中的二进制会报 Text file busy）：
gunzip -c lz-server.gz > /opt/picopanel/server/lz-server.new
chmod +x /opt/picopanel/server/lz-server.new
mv /opt/picopanel/server/lz-server.new /opt/picopanel/server/lz-server
systemctl restart picopanel-server
# 回滚：到 Releases 页下载旧版本 .gz，同法替换
# agent 同理（lz-agent → systemctl restart picopanel-agent）
```

其他说明：

- 旧数据库首次用新版启动会自动迁移（幂等，任意旧版本可直跳最新；大库迁移期间服务中断 1~3 分钟，日志可见进度；在线更新升级前自动备库一份）
- 自检参数：`./lz-server -version`、`./lz-server -check-config`（agent 同）
- 忘记密码：`systemctl stop picopanel-server` 后前台跑一次 `./lz-server -resetuser`（重置为 admin/admin123 并正常启动，Ctrl-C 停掉），再 `systemctl start` 恢复；两步验证丢失同理用 `-resettotp`（清除 TOTP 后密码直接登录）

## 8. 常见问题

- **服务启动即崩（OpenVZ/LXC 等容器化 VPS）**：Go 运行时把 CPU 配额推导为 0 所致，systemd unit 里取消 `Environment=GOMAXPROCS=<核数>` 注释并 `daemon-reload` 重启
- **页面打不开**：`systemctl status picopanel-server` 看进程；看部署目录 `lz-server.log`；确认防火墙放行 8080；配置文件写错时可前台 `./lz-server -config server.yaml` 直接看报错
- **agent 加不上/显示离线**：pull 模式检查服务端能否访问 `被监控机IP:19100`（`curl http://<ip>:19100/metrics`），防火墙是否只对服务端放行；push 模式检查 `server_url` 与 `token` 是否与服务端一致，`offline_after`（默认 2 分钟）内无成功上报即判离线
- **agent 无法远程升级**：pull 模式未配 `token` 会被拒绝（安全口径），在 agent.yaml 配置密钥并与服务端一致即可
- **node_exporter 机器 CPU/网卡/IO 显示 0**：这些是累计计数器，服务端按两次抓取差值算速率，**重启服务端后第一个周期为 0，下一周期起正常**；LVM 逻辑卷（`/dev/mapper/…`）无对应设备名，IO 显示 0
- **数据保留**：默认 90 天，在 Web「系统设置」页运行期修改（不在配置文件）；近 7 天存 10 秒明细、更早自动降采样为 5 分钟均值
- **日志太大**：服务端默认上限 50MB、agent 300MB，超限自动轮转；agent 默认零日志不写盘
