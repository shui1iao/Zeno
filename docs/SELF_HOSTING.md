# Self-hosting / 自部署指南

Zeno Controller 官方自部署方式是 Docker Compose 一键安装器。Controller 包含 Go API、SQLite 数据库和静态 Web UI；Agent 由独立的 Zeno-Agent 仓库发布。

## 1. 安装 Controller

准备一台支持 `amd64`、`arm64` 或 `arm/v6`、已安装 Docker Engine 24+ 和 Docker Compose 2.20+ 的 Linux 服务器。安装器必须以 root 运行（直接 root 或 `sudo`）。建议至少 1 vCPU、512 MiB 可用内存、1 GiB 可用磁盘；节点数、历史保留和 SQLite 备份增长时应相应增加。执行：

```bash
(
  set -Eeuo pipefail
  installer=$(mktemp)
  trap 'rm -f "$installer"' EXIT
  curl -fsSL https://zeno.siaoz.si/install.sh -o "$installer"
  sudo bash "$installer"
)
```

默认目录：

```text
/opt/zeno/.zeno-installation
/opt/zeno/.env
/opt/zeno/docker-compose.yml
/opt/zeno/data/zeno.db
/opt/zeno/secrets/
/opt/zeno/backups/
```

`.zeno-installation` 是安装器的目录身份标记。不要删除该文件，也不要把它复制到无关目录；安装器会拒绝接管没有有效标记、且无法识别为旧版 Zeno 的非空目录。

默认监听 `127.0.0.1:18980`，Controller 容器以固定非 root 用户 `10001:10001` 运行。不要把该端口直接暴露到公网；请通过 Caddy、Nginx 或其他 HTTPS 反向代理提供访问。

仓库根目录的 Compose 是安装器模板，不是“clone 后直接 up”的完整安装包：所需 `.env`、权限和 notification keyring/authority secrets 由安装器生成并维护。不要在空 clone 中直接 `docker compose up`，也不要手工编辑安装目录中的 Compose 后期待升级器保留未知修改。

指定安装目录、端口或明确镜像版本时，把变量传给安装器：

```bash
(
  set -Eeuo pipefail
  installer=$(mktemp)
  trap 'rm -f "$installer"' EXIT
  curl -fsSL https://zeno.siaoz.si/install.sh -o "$installer"
  sudo env \
    ZENO_INSTALL_DIR=/opt/zeno \
    ZENO_HOST_PORT=18980 \
    ZENO_IMAGE=ghcr.io/shui1iao/zeno:vX.Y.Z \
    ZENO_DB_CHECK_TIMEOUT=10m \
    bash "$installer"
)
```

重复运行同一安装器会执行镜像 provenance 校验、停服前预检、一致性离线备份、SQLite `quick_check`、原子配置替换、readiness 检查和失败自动恢复。升级与恢复细节见 [`UPGRADE.md`](UPGRADE.md)。

数据库检查使用独立的 `ZENO_DB_CHECK_TIMEOUT`，默认 `10m`，支持整数 `s`、`m`、`h` 且最大 `24h`。大型数据库可能需要数分钟；安装器会持久化该值。超时或检查失败会自动回滚，不影响 Controller 的普通运行时超时设置。

## 2. 配置 HTTPS 入口

Caddy 示例：

```caddyfile
zeno.example.com {
    reverse_proxy 127.0.0.1:18980
}
```

Nginx 示例（WebSocket upgrade 必须转发）：

```nginx
map $http_upgrade $connection_upgrade { default upgrade; '' close; }
server {
    listen 443 ssl http2;
    server_name zeno.example.com;
    # ssl_certificate / ssl_certificate_key 按你的 ACME 客户端配置
    location / {
        proxy_pass http://127.0.0.1:18980;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
    }
}
```

同机反代通常使用安装器默认的 Docker gateway trusted proxy。反代在另一台机器时，重新运行安装器并设置其**实际来源 IP/CIDR**，不要信任整个互联网或宽泛私网：

```bash
(
  set -Eeuo pipefail
  installer=$(mktemp)
  trap 'rm -f "$installer"' EXIT
  curl -fsSL https://zeno.siaoz.si/install.sh -o "$installer"
  sudo env ZENO_TRUSTED_PROXIES=192.0.2.10/32 bash "$installer"
)
```

验证 Controller：

```bash
curl -fsS http://127.0.0.1:18980/health
curl -fsS http://127.0.0.1:18980/ready
curl -fsS http://127.0.0.1:18980/api/public/v1/summary
```

## 3. 登录 Admin

首次安装的 bootstrap Admin 凭据保存在：

```bash
sudo cat /opt/zeno/secrets/zeno_admin_token
```

打开 `/dashboard`，使用账号 `admin` 和 bootstrap 凭据登录。登录后建议在后台“账户”页修改账号和密码。

Admin API 只接受请求头：

```http
X-Admin-Token: <session-token>
```

不要把 Admin/session token 放进 URL query string、日志或 issue。

## 4. 配置 Agent 接入 URL

在后台“设置”中填写 Agent 能访问的 HTTPS 地址：

```text
https://zeno.example.com
```

该地址会写入后台生成的 Agent 安装命令。远程 Agent 默认必须使用 HTTPS；为兼容没有反向代理的受控网络，`http://<直接 IP>:<显式端口>` 也可使用。后台复制命令前会再次确认风险，生成命令显式传入 `ZENO_ALLOW_INSECURE_HTTP=1`，安装器才会在服务配置中持久化 runtime opt-in；enrollment/runtime bearer token 仍会明文传输。主机名 HTTP、没有显式端口的远程 HTTP 仍会被拒绝；loopback HTTP 可正常使用。URL 不得包含用户名、密码、query 或 fragment。

Zeno 不自动修改 DNS、反向代理或防火墙。

## 5. 添加服务器并安装 Agent

1. 在后台“服务器”中添加服务器。
2. 填写名称、地区、账单周期、流量口径、月重置日和配额等展示字段。
3. 打开服务器编辑弹窗，点击“复制安装命令”。
4. 选择 Linux、macOS 或 Windows，并在目标机器执行命令。

Agent 安装器和多平台 release 来自独立的 [Zeno-Agent](https://github.com/shui1iao/Zeno-Agent) 仓库。Linux Agent 使用其自己的 `zeno-agent.service`；macOS 使用 LaunchDaemon；Windows 使用系统服务。

## 6. 验证 Agent

Linux Agent 示例：

```bash
systemctl is-active zeno-agent.service
journalctl -u zeno-agent.service --since '5 minutes ago' --no-pager
```

Public summary 中新服务器应从 `no_data` 变为 `online` 或 `warning`。

## 7. 数据、权限与备份

最小备份范围：

```text
/opt/zeno/.env
/opt/zeno/docker-compose.yml
/opt/zeno/data/
/opt/zeno/secrets/
```

安装器默认保留最近 5 份完整安装备份。`data/` 由运行 UID/GID `10001:10001` 持有；`secrets/` 目录应为 `root:10001`、模式 `0750`，secret 文件应为 `root:10001`、模式 `0640`。不要把 secrets 改为运行用户所有。

可执行的离线备份示例（会短暂停服以获得 SQLite 与 keyring 一致快照）：

```bash
sudo sh -eu -c '
  cd /opt/zeno
  docker compose stop zeno
  trap "docker compose start zeno" EXIT
  dst="/var/backups/zeno/$(date -u +%Y%m%dT%H%M%SZ)"
  install -d -m 0700 "$dst"
  cp -a -- .env docker-compose.yml data secrets "$dst/"
  (cd "$dst" && find . -type f -print0 | sort -z | xargs -0 sha256sum > MANIFEST.sha256)
'
```

恢复前先校验 manifest，停止 Controller，把当前目录另存，再将同一备份中的 `.env`、Compose、`data/`、`secrets/` 一起恢复；详细权限与失败回滚步骤见 [UPGRADE.md](UPGRADE.md)。不要只恢复数据库或只恢复 keyring。

## 8. 忘记管理员密码

官方镜像提供离线恢复模式。它将用户名重置为 `admin`、把密码重置为安装时 bootstrap secret，并撤销所有 Admin session；不会更改 Agent token。先做完整备份，再执行：

```bash
cd /opt/zeno
sudo docker compose stop zeno
sudo docker compose run --rm --no-deps zeno \
  -db /data/zeno.db \
  -reset-admin-password-file /run/secrets/zeno_admin_token
sudo docker compose up -d zeno
curl -fsS http://127.0.0.1:18980/ready
```

登录后立即设置新密码。不要把 bootstrap secret 输出到 shell history、Issue 或日志。

## 9. 卸载

先创建并验证最终备份。以下命令会删除容器/网络，但保留 `/opt/zeno` 数据：

```bash
cd /opt/zeno
sudo docker compose down
```

确认不再需要恢复后，才由管理员手工删除 `/opt/zeno` 和对应备份。Zeno 安装器不会修改 DNS、Caddy/Nginx 或防火墙，因此这些入口也必须由管理员单独移除。不要使用 `down -v` 或通配路径删除未知数据。

## 10. 数据收集与公开边界

当前版本不提供远程终端、文件管理、远程命令执行或脚本任务，也不提供 Nezha、Komari、Kulin 的兼容 API。

Agent 采集并上报：主机名、OS/kernel/架构/虚拟化、CPU/内存/swap/磁盘、进程与 TCP 连接计数、网络接口累计量与速率、uptime、best-effort 公网 IPv4/IPv6 和 GeoIP 国家码，以及管理员配置的 ICMP/TCP/HTTP 探测结果。Controller 保存节点管理字段、流量历史、探测样本和通知状态。Public page/API 会展示管理员配置的名称/地区、在线状态、资源与流量摘要、到期/配额信息和探测统计；不会公开 runtime/enrollment token、通知凭据、完整私有配置或数据库。公网 IP 是否作为节点展示字段使用应由管理员在发布前检查；不要把 Admin 权限授予不可信用户。

故障排查顺序：`/ready` → `docker compose ps` → `docker compose logs --tail=200 zeno` → 反代 TLS/WebSocket → Agent 服务状态/日志 → [SUPPORT.md](../SUPPORT.md)。

## English operational summary

- Run the installer as root/sudo on Linux `amd64`, `arm64`, or `arm/v6`. Current baseline: Docker Engine 24+, Compose 2.20+, 1 vCPU, 512 MiB free memory, 1 GiB free disk.
- The repository Compose file is an **installer-managed template**, not a complete clone-and-up deployment. The installer creates `.env`, keyrings, permissions, backups, and the loopback binding.
- Keep `18980` on loopback. Terminate TLS in Caddy/Nginx, forward WebSocket upgrade headers, and list only the proxy's real source in `ZENO_TRUSTED_PROXIES`.
- Back up `.env`, Compose, `data/`, and `secrets/` together while stopped; verify the manifest and restore the same snapshot together. The offline `-reset-admin-password-file` command above resets the username to `admin`, revokes sessions, and does not rotate Agent credentials.
- `docker compose down` removes the isolated stack but intentionally preserves `/opt/zeno`; remove data/backups only after a verified final backup. Zeno does not manage DNS, reverse proxies, or firewall rules.
- Agent collection/public-page boundaries and the troubleshooting order are specified above. See [COMPATIBILITY.md](COMPATIBILITY.md) for supported Controller/Agent combinations.
