# Phase 1 Plan / 第一阶段计划

> 这是早期阶段计划文档。Phase 1 的规格锁定已经完成，Zeno 现在已进入可运行预览、后台管理和部署工具固化阶段。

## Phase 1 原始目标

把 Zeno 的硬约束锁死，避免后续实现时滑向旧系统兼容、重型探针或 UI 随手改版。

## 已创建文档

- `README.md`
- `docs/NON_GOALS.md`
- `docs/DISPLAY_LOCK.md`
- `docs/HOME_CARD_SPEC.md`
- `docs/LATENCY_STATS_SPEC.md`
- `docs/LATENCY_CHART_SPEC.md`
- `docs/DATA_MODEL.md`
- `docs/API.md`
- `docs/SECURITY.md`
- `docs/SELF_HOSTING.md`
- `docs/TECHNICAL_DESIGN.md`

## 当前状态

Zeno 当前已经具备完整预览闭环：

- Go Controller + SQLite。
- Go Agent + systemd。
- Public API 和 Admin API。
- React 前台和后台。
- Agent 主机状态、完整资源历史（CPU/内存/磁盘/网络速率/负载/Swap/进程/TCP/网络累计）、月流量（含首页计费周期范围）、延迟/服务探测和服务多节点历史。
- `tcping`、`ping`/ICMP、`http_get` 探针目标。
- Public 服务详情页；首页只保留服务器卡片和优化后的整体概览，不单独展示监控服务列表。
- Admin 单管理员登录、账户页修改账号/密码、退出登录、服务器、服务器列表内排序、Agent 安装命令复制、Agent 接入 URL、探针目标、探针目标列表内排序、节点分配、通知和外观设置；外观 / Agent 接入 URL 保存前会先校验格式。
- Admin 手机端按卡片化列表和双列导航展示；后台各列表都按内容自然撑开并交给页面滚动；服务器列表只保留名称、状态、公网 IP、Agent 和编辑操作，IPv4/IPv6 分行显示且无协议前缀。
- Telegram-only 通知 dispatch 和测试发送。
- 服务器元数据：到期日、账单周期、显示顺序（后台可整理，也可在编辑里调整）、国家码/国旗、公网 IPv4、公网 IPv6，Agent 可自动识别公网 IP / GeoIP。
- 通知页只展示已添加通知类型；添加通知类型通过弹窗选择预置规则，并支持按服务器范围生效。
- Controller Docker Compose 一键安装器、独立 Agent 安装器、GUKO 服务器清单导入脚本和自部署指南。
- Controller 预览部署：`/opt/zeno` Docker Compose（Agent 由 Zeno-Agent 仓库管理）。

## 当前迭代原则

1. 继续按功能包推进：后端、前端、文档、测试、commit、必要时部署 smoke。
2. 保持绿色项目边界：不加旧系统兼容，不引入远控/命令执行。
3. 保持已确认 UI：主页卡片、详情页密度、Admin 分区和列表/弹窗结构不随手重设计；弹窗表单统一按分区组织，避免所有字段挤在一块。
4. Admin / Public API 响应继续使用 explicit DTO，不返回 token、secret、hash、credential、bearer 等敏感字段。
5. 部署切片结束时清理 build/tmp，确认 Example Node A 服务 active、`/health` OK、本地 git clean。

## 下一批建议切片

1. 多节点铺 Agent：在 Controller 公网 HTTPS 入口确认后逐台安装，先少量 smoke，再批量。
2. UI polish：拖拽排序等，放在主线闭环之后。
