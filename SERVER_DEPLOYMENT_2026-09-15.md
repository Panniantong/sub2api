# 2026-09-15 服务器部署结果

已按用户授权将本地修复部署到 https://juhe.pub。新版本为 `optional-controls-20260915`，服务器 `107.149.55.60`，部署连接核对既有 SSH 主机指纹。登录密码仅在连接时输入，没有保存在项目、源码归档或本报告中。

## 当前版本

- 镜像：`chengchuan-relay:optional-controls-20260915`
- 镜像 ID：`sha256:bc0a061f84b8be362b443c3ff2e7fdff255b5ea4a80cb907961ea742192c8b19`
- 构建时间：2026-09-15 02:33:07 UTC。
- 当前 Compose 目录：`/opt/chengchuan-relay/releases/optional-controls-20260915/source/deploy`。后续操作应使用此目录，旧说明中的 `source/deploy`、`feature-source-next/deploy` 是历史部署目录。
- 发布源码包：项目外 `交付包/relay-optional-controls-20260915-source.tar.gz`，4,001 个文件逐项 SHA-256 验证；压缩包 SHA-256 为 `c67137e3567f9b59d7069af74bd52d489edbf7e85625f5c4f1df1a2955ed444f`。

## 生效内容和默认状态

本次同时上线本地报告中的账号保护修复、B 方案 v3 评分、鹈鹕 SVG 展示、测试队列与轮询修复、可选 RPM / 自适应并发、独立完整性控制和真实分组统计。

线上全部账号的并发数、代理和 extra 配置在切换前后摘要一致，没有批量切换策略或覆盖管理员设置。严格 RPM 开启账号数为 0，自适应并发开启账号数为 0；管理员可在账号编辑的“可选流量控制”启用，建议参数可继续自定义。分组管理勾选“完整统计”后显示入口。

第 248 号迁移 `248_intelligent_assessment_v2.sql` 已执行，新增队列调度字段并扩展测试状态约束；既有迁移文件未改动。历史测试结论没有自动批量重评；需要时从详情手动复核。

## 验证结果

1. 服务器构建完整前后端镜像成功，含前端类型检查和 i18n 检查。
2. 将部署前数据库备份恢复到隔离容器，验证新迁移与重复执行；用户、账号、密钥、用量行以及历史测试原始字段保持一致。隔离网络禁止测试副本访问真实上游。
3. 新镜像在隔离环境的 20 项 HTTP/API 检查通过。
4. 线上切换后的 20 项 HTTPS 检查通过，覆盖健康、前端入口与资源、未认证拒绝、现有管理员登录、流量配置读取、分组统计与日期校验、账号测试中心。
5. 线上规则试判确认 `**ANSWER: 12颗**` 显示 `completed / correct / non_compliant`，评分版本 3，答案正确性与格式分别判断。该请求只运行规则，不调用模型、不改写历史测试。
6. 本机外部访问 `https://juhe.pub/health` 返回 200 和 `{"status":"ok"}`，服务器 HTTPS 登录页返回 200。

切换及首次完整检查耗时约 8.3 秒，不等同于精确停机时长。仅重建应用容器，PostgreSQL 和 Redis 的容器 ID 保持不变。未执行真实上游模型、支付、发信或图片生成测试；最新页面的浏览器目视验收仍受此前浏览器工具认证错误限制。

## 备份与回滚

服务器私有备份目录：`/opt/chengchuan-relay/backups/before-optional-controls-20260915`。

保留部署前源码、应用数据、私有环境设置、部署前数据库 dump，以及停止旧应用后制作的 `database-at-cutover.dump`。数据库备份目录索引已用 pg_restore 检查。旧镜像另加标签 `chengchuan-relay:rollback-optional-controls-20260915`，原镜像也保留。

普通应用回滚可从新部署目录执行：

```sh
RELAY_VERSION=rollback-optional-controls-20260915 docker compose --env-file .env -f compose.relay.yml up -d --no-deps --no-build --wait app
```

第 248 号迁移主要为兼容扩展，旧镜像回滚不需要自动还原数据库；新状态在旧测试界面显示可能有限制。不得使用删除卷命令更新，也不应在产生新业务数据后直接覆盖数据库备份。备份包含私有业务数据，应保留原目录权限。

本次临时应用、数据库副本、Redis 和隔离网络已清理；原有测试容器、线上容器及业务卷未删除。被清理的是可从保留备份重建的验证副本。

服务器证据目录：`/opt/chengchuan-relay/evidence/optional-controls-20260915`。本地可复核副本见 `verification/deployment-20260915/`。
