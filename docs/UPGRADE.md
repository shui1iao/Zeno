# Upgrade and rollback / 升级与回滚

Zeno Controller 推荐并支持通过仓库一键安装器管理 Docker Compose 部署。数据库迁移会在 Controller 启动时自动执行；升级必须同时保护 SQLite 数据和文件型 secrets。

## 1. 安装器备份

重复运行 `install.sh` 时，安装器会在修改当前安装前完成停服前路径/磁盘/镜像预检，然后停止旧容器并创建一致性离线备份：

```text
/opt/zeno/backups/install-YYYYmmdd-HHMMSS-PID/
```

备份包含 `.env`、`docker-compose.yml`、`data/` 和 `secrets/`，并带完整性 marker 和 SHA-256 manifest。默认保留最近 **5** 份完整安装备份，可通过 `ZENO_BACKUP_KEEP_COUNT` 调整。安装器会在备份副本和当前数据库上执行 SQLite `quick_check`，启动后等待 `/ready`；失败时使用固定的旧镜像 ID 和完整备份自动恢复。

`quick_check` 的默认上限为 **10 分钟**，而不是 Controller 普通运行时请求超时。大型 SQLite 数据库可能需要数分钟。可在升级命令中设置 `ZENO_DB_CHECK_TIMEOUT=20m`（支持整数 `s`、`m`、`h`，必须大于 0 且不超过 `24h`）；安装器会把该值写入 `.env` 供后续升级复用，并同时传给 Controller 的专用 `-check-db-timeout`。检查失败或超时仍会触发自动恢复，不要通过删除超时或跳过检查来升级。

生产环境仍应把这些目录纳入独立的定期异机备份。

## 2. 使用明确版本升级

先从 Release notes 选择明确的 SemVer tag 或镜像 digest。不要以 mutable tag 作为升级或回滚目标。

```bash
version=vX.Y.Z
curl -fsS "https://zeno.siaoz.si/$version/install.sh" -o install.sh
curl -fsS "https://zeno.siaoz.si/$version/install.sh.sha256" -o install.sh.sha256
sha256sum -c install.sh.sha256
sudo env ZENO_IMAGE="ghcr.io/shui1iao/zeno:$version" ZENO_DB_CHECK_TIMEOUT=10m bash install.sh
rm -f install.sh install.sh.sha256
```

将示例版本替换为目标版本。官方镜像安装默认校验 GitHub build provenance；不要用手工 `docker compose pull/up` 绕过安装器的备份、digest 固定、attestation 和自动恢复流程。

升级后验证：

```bash
docker inspect zeno --format 'image={{.Config.Image}} health={{.State.Health.Status}} version={{index .Config.Labels "org.opencontainers.image.version"}} revision={{index .Config.Labels "org.opencontainers.image.revision"}}'
curl -fsS http://127.0.0.1:18980/health
curl -fsS http://127.0.0.1:18980/ready
curl -fsS http://127.0.0.1:18980/api/public/v1/summary
```

## 3. 回滚

如果刚执行的升级未通过 readiness，安装器会自动恢复旧配置、数据、secrets 和升级前固定的镜像。

需要人工恢复历史备份时：

1. 确认备份目录包含 `.zeno-backup-complete`，并在备份目录运行 `sha256sum -c MANIFEST.sha256`。
2. 停止 Controller，另存当前失败现场。
3. 原样恢复该备份中的 `.env`、`docker-compose.yml`、`data/` 和 `secrets/`。
4. 恢复权限：`data/` 归 `10001:10001`；`secrets/` 归 `root:10001`，目录模式 `0750`、文件模式 `0640`。
5. 以备份 `BACKUP_INFO` 中记录的不可变 rollback image reference 启动，并验证 `/ready`。

权限修复示例（仅在确认恢复内容和路径后执行）：

```bash
cd /opt/zeno
chown -R 10001:10001 data
find data -type d -exec chmod 700 {} +
find data -type f \( -name '*.db' -o -name '*.db-wal' -o -name '*.db-shm' -o -name '*-wal' -o -name '*-shm' \) -exec chmod 600 {} +
chown -R 0:10001 secrets
find secrets -type d -exec chmod 750 {} +
find secrets -type f -exec chmod 640 {} +
```

> 回滚数据库前必须停止 Controller。不要覆盖运行中的 SQLite 文件，也不要把 `secrets/` chown 给容器运行 UID。

## 4. Agent 升级

Controller 升级通常不要求同步升级 Agent。只有 Release notes 明确说明 Agent 协议或功能要求时，才重新运行后台生成的 Zeno-Agent 安装命令。

Agent 安装器和多平台 release 来自独立 Zeno-Agent 仓库；公开安装命令通常不需要设置 `ZENO_AGENT_VERSION`。

## 5. 安装器版本与校验边界

默认 `https://zeno.siaoz.si/install.sh` 是发布方维护的便利入口，适合解析当前推荐稳定版本。可复现升级应使用第一方不可变版本路由：

```text
https://zeno.siaoz.si/vX.Y.Z/install.sh
https://zeno.siaoz.si/vX.Y.Z/install.sh.sha256
```

该路由仅接受严格稳定 SemVer，并为脚本提供独立 SHA-256 元数据。先下载脚本和 checksum、运行 `sha256sum -c`，再以 root 执行；同时用同一版本固定 `ZENO_IMAGE=ghcr.io/shui1iao/zeno:vX.Y.Z`。默认入口继续用于便利安装，但不要把它当作明确回滚目标。仓库 tag 的 raw URL 仍可用于源码审计。

## English summary

Use the installer for upgrades; it creates a stopped, complete backup, checks SQLite, pins the previous image ID, waits for readiness, and restores the complete snapshot on failure. SQLite checks default to 10 minutes; large databases may take several minutes, so set `ZENO_DB_CHECK_TIMEOUT=20m` when needed (integer `s`, `m`, or `h`, maximum `24h`). The value is persisted and passed to the Controller's dedicated `-check-db-timeout`; timeout or check failure still rolls back. Upgrade and rollback to an immutable `vX.Y.Z` or digest—not `latest`—and restore `.env`, Compose, `data/`, and `secrets/` from the same backup. For reproducible upgrades, download `/vX.Y.Z/install.sh` and `/vX.Y.Z/install.sh.sha256`, verify the checksum, and pin `ZENO_IMAGE` to that same version. The unversioned `/install.sh` URL remains a convenience entry point for the currently recommended stable release.
