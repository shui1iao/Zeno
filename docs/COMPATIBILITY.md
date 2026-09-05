# Controller ↔ Agent compatibility / 兼容性

[中文](#中文) · [English](#english)

## 中文

### 当前验证组合

| Controller | Agent | 状态 | 说明 |
| --- | --- | --- | --- |
| v1.0.16 | v0.6.6 | 支持 | 当前稳定组合 |
| v1.0.15 | v0.6.6 | 支持 | 上一稳定组合 |
| v1.0.14 | v0.6.6 | 支持 | 更早稳定组合 |
| v1.0.12 | v0.6.6 | 支持 | 更早稳定组合 |
| v1.0.10 | v0.6.6 | 支持 | 更早稳定组合 |
| v1.0.9 | v0.6.6 | 支持 | 更早稳定组合 |
| v1.0.9 | v0.6.5 | 支持 | 现有 v0.6.5 Agent 可继续上报并独立升级 |
| v1.0.8 | v0.6.6 | 支持 | 更早稳定组合 |

只有表中明确列出的组合属于已验证支持范围。较旧 Agent 可能仍可上报，但未列出的组合仅为 best effort；排障前请先升级 Agent。Controller 与 Agent 独立发布，版本号不要求相同。

### 平台与架构

- Controller 官方镜像：Linux `amd64`、`arm64`、`arm/v6`；Docker Engine + Docker Compose v2。
- Agent：Linux `amd64` / `arm64` / `armv6` / `armv7`（systemd）、macOS `amd64` / Apple Silicon（LaunchDaemon）、Windows `amd64` / `arm64`（Windows service）。
- Linux Agent 安装器要求 systemd，并创建专用非登录用户。其他 init 系统可手动运行二进制，但不在官方安装器支持范围。
- Windows on ARM 需要可运行原生 arm64 服务的 Windows 版本；macOS 安装需要 sudo 权限。

### 支持与弃用策略

- 新 Controller 会尽量保持对已发布 Agent 协议字段的向后兼容；未知 JSON 字段必须被忽略。
- 安全修复可能要求同步升级。若存在最低 Agent 版本，Release Notes 和本表会明确写出；未注明时仍建议 Controller 与 Agent 都使用最新稳定版。
- 计划移除协议、环境变量或安装路径时，至少提前一个稳定 Controller Release 在 Release Notes 和本文档中标记 deprecated。安全风险要求立即移除时除外。
- enrollment token 是一次性的；安装后使用每节点独立 runtime token。重新生成安装命令可能使旧凭据失效。
- 升级前备份 Controller 的 `.env`、`docker-compose.yml`、`data/`、`secrets/`。Controller 回滚可能同时回滚数据库 schema；按 [UPGRADE.md](UPGRADE.md) 使用同一份完整备份，不要只替换镜像。

报告兼容性问题时，请提供脱敏后的 Controller/Agent 版本、操作系统/架构和相关时间段；不要提供 token、完整安装命令、Authorization header 或通知凭据。安全问题请按 [SECURITY.md](SECURITY.md) 私密报告。

## English

### Currently verified combination

| Controller | Agent | Status | Notes |
| --- | --- | --- | --- |
| v1.0.16 | v0.6.6 | Supported | Current stable combination |
| v1.0.15 | v0.6.6 | Supported | Previous stable combination |
| v1.0.14 | v0.6.6 | Supported | Earlier stable combination |
| v1.0.12 | v0.6.6 | Supported | Earlier stable combination |
| v1.0.10 | v0.6.6 | Supported | Earlier stable combination |
| v1.0.9 | v0.6.6 | Supported | Earlier stable combination |
| v1.0.9 | v0.6.5 | Supported | Existing v0.6.5 Agents remain supported and can upgrade independently |
| v1.0.8 | v0.6.6 | Supported | Earlier stable combination |

Only combinations explicitly listed above are verified and supported. Older Agents may continue to report, but unlisted combinations are best effort; upgrade the Agent before troubleshooting. Controller and Agent are released independently and their version numbers do not need to match.

### Platforms and architectures

- Official Controller image: Linux `amd64`, `arm64`, and `arm/v6`; Docker Engine with Docker Compose v2.
- Agent: Linux `amd64` / `arm64` / `armv6` / `armv7` (systemd), macOS `amd64` / Apple Silicon (LaunchDaemon), and Windows `amd64` / `arm64` (Windows service).
- The Linux installer requires systemd and creates a dedicated non-login user. Manually running the binary under another init system is outside installer support.
- Windows on ARM requires a Windows version capable of running native arm64 services; macOS installation requires sudo.

### Support and deprecation policy

- New Controllers aim to remain backward-compatible with already released Agent protocol fields; unknown JSON fields must be ignored.
- Security fixes may require coordinated upgrades. A minimum Agent version will be stated in Release Notes and this table when one applies; otherwise, running both latest stable releases is still recommended.
- Planned removal of a protocol, environment variable, or install path will be marked deprecated in Release Notes and this document for at least one stable Controller release, except when an immediate removal is required for security.
- Enrollment tokens are one-time credentials. Installation exchanges one for a per-node runtime token; regenerating an install command may invalidate previous credentials.
- Before upgrading, back up Controller `.env`, `docker-compose.yml`, `data/`, and `secrets/`. A Controller rollback may also need a database-schema rollback; follow [UPGRADE.md](UPGRADE.md) and restore one complete backup rather than changing only the image.

For compatibility reports, include redacted Controller/Agent versions, OS/architecture, and the relevant time window. Never include tokens, complete install commands, Authorization headers, or notification credentials. Report security issues privately as described in [SECURITY.md](SECURITY.md).
