# OpenAI 账号模式一：本地实现与验证

日期：2026-09-11。仅本地实现，没有连接或部署服务器。

## 来源与工作目录

- 冻结源码来源：`C:\Users\ROG\Desktop\sub2备份源码\server-deployed-20260911-173007\source.tar.gz`
- 来源归档 SHA-256：`a10b287a742e2e417705ac27e6232c01dc66b8e5af05da156565ba415248a7ff`
- 开发目录：`C:\Users\ROG\Documents\ChatGPT\sub2升级\work\mode1-local`
- 创建开发副本时，3947 个源文件与原清单逐文件哈希一致。冻结归档和旧 `Desktop\sub2` 未修改。
- 本目录属于外层未提交仓库，没有宣称 Git Commit、合并或线上版本更新。

## 模式一的实际行为

用户选择：整体账号身份独立；TLS 使用固定模板，不要求每账号 JA3/JA4 唯一。

1. 显式应用模式一保存 `extra.anti_degrade`，其中 `mode=mode1`、`policy_version=2`、`max_concurrency` 为保护上限。已有旧方案必须先还原再显式应用，升级不自动更换存量账号身份。
2. 复用系统管理的随机 UUID 种子 `codex_fingerprint_seed`。首次启用缺种子时，真实 AdminService 更新流程创建种子；普通编辑、关闭、重开保留合法种子。新版模式一使用该种子派生设备 ID，两个账号即使具有同一个旧 `openai_device_id` 也不会复用该旧设备 ID。复制账号沿用原有创建新种子的逻辑。API DTO 不展示种子。
3. 指纹模式是 `device`，不使用 `session/full` 合并不同用户或对话。HTTP 与原生 WS 使用相同种子派生设备身份；会话头使用既有账号/API Key 隔离函数。客户端仍需提供可持续的会话标识；不能从任意无状态输入猜出真实对话归属。
4. 模式一固定 `nodejs24` 兼容模板。模板实际通过 uTLS 发出；HTTP、账号测试、WS 池、后台预热和原生 WS 透传接入同一传输策略。不是从模板名称推断生效。
5. 直连、HTTP CONNECT、HTTPS CONNECT、SOCKS5/SOCKS5h 均有 TLS 路径。HTTPS 代理外层证书和上游目标内层证书分别校验。明文、不支持的 ALPN、TLS 失败不静默降级。
6. TLS HTTP 池强制按账号、代理和模板内容摘要隔离；HTTP Transport 自身按目标地址管理连接。WS 复用条件另含目标地址和会话身份。配置变更让新请求使用新池；旧闲置池按 TTL 回收，不强杀在途请求。
7. `Mode1EffectiveConcurrency()` 在调度、WS 连接池和 handler 等待路径持续执行上限；已有更低并发优先。不限或高于16时，应用默认保护上限16。这是可配置工程上限，不是经上游证明的“安全数值”。沿用已有配额、排队、429/退避系统，本次没有新增上游限额预测算法。
8. Responses HTTP 和 WS 比较转换前后关键语义字段：模型、input、instructions、reasoning、tools、tool_choice、text、previous_response_id、输出预算和 session。发现丢失或更改则报 `MODE1_LOSSY_TRANSFORM`。新版模式一禁用部分通过删除推理项继续重试的恢复分支。
9. 普通账号保存保护模式一受管字段；部分更新和批量更新不得移除受管策略。调度缓存保留策略、TLS 和 proxy_mode，避免投影丢字段绕过检查。错误版本或损坏标记在传输层明确失败。
10. 前端区分配置状态与实际握手事实。两个按钮始终可见，模式二明确为旧方案；应用/还原按服务端返回值同步，关闭重开和旧 props 不得伪造成功或覆盖新状态。预览不是线上握手证明。

## 接口与主要文件

- 原接口继续使用：`GET /api/v1/admin/accounts/:id/anti-degrade?mode=mode1`、`POST .../apply?mode=mode1`、`POST .../revert`。
- `backend/internal/service/account_mode1_protection.go`：新策略、状态校验、管理字段保护、有效并发。
- `backend/internal/service/account_anti_degrade.go`：预览、应用、还原接入。
- `backend/internal/service/openai_mode1_integrity.go`：请求语义守卫。
- `backend/internal/service/openai_plugin_transport.go`：真实 HTTP 和账号测试接入。
- `backend/internal/service/openai_ws_client.go`、`openai_ws_pool.go`、`openai_ws_forwarder_ingress.go`、`openai_ws_forwarder_v2.go`、`openai_ws_v2_passthrough_adapter.go`：原生 WS 接入、会话隔离和保真校验。
- `backend/internal/pkg/tlsfingerprint/transport.go`、`profile.go`、`dialer.go`：握手、代理、证书、配置摘要和克隆。
- `backend/internal/repository/http_upstream.go`：真实 HTTP TLS 池。
- `frontend/src/components/account/EditAccountModal.vue`：前端入口及状态同步。

## 本地验证证据

仅使用本机回环测试端点和既有依赖；未向真实 OpenAI 或原服务器发送测试请求。

- 后端联合目标回归：209 个测试/子测试通过、0失败、0跳过；记录 `verification/backend-tests.jsonl`。涉及 service、repository、tlsfingerprint、admin handler 编译与目标测试。
- 随后的种子 DTO 隐藏、重复旧设备身份、Redis 并发槽位验证：三个包目标测试通过；记录 `verification/final-focused-tests.log`。
- 前端关联回归：87项通过（新模式组件/API、旧编辑/Grok、i18n），类型检查、定向 ESLint 通过。对应日志在 `verification` 中留档。
- 本地 Vite 构建通过：`verification/frontend-build.log`。
- 后端 `go build ./cmd/server` 和带 `embed` 标签构建通过；目标包 `go vet` 通过。
- 真实本地 ClientHello 测试覆盖密码套件顺序、曲线、ALPN、可信/不可信证书、代理隧道、取消、不降级、池隔离、模板变更，以及 WSS 101 Upgrade 与多轮消息回显。
- 种子生命周期测试经真实 AdminService + 测试仓库执行，并模拟数据库 JSON 序列化/重新读取。未安装新 PostgreSQL 实例或执行生产数据库迁移；不是全新服务器安装验收。
- `-race` 尝试在本机 runtime/cgo 编译阶段失败，未获得竞态运行结果。

## 明确边界

- 这是固定兼容 TLS 模板，不能宣称已经匹配当前官方 Codex 客户端抓包，也不能宣称已证明防风控或模型质量改善。
- 当前自定义 TLS Transport 使用 HTTP/1.1（含 WSS Upgrade）。显式 h2 返回不支持，不伪装成已协商 h2。
- 旧别名自动替换，例如 `gpt-5.1 → gpt-5.4`，会被严格模式拒绝；不支持的旧请求需明确修正，而不是静默改模型。Messages/Chat Completions等跨协议转换未增加与 Responses 同等范围的逐字段守卫，不宣称全网关语义审计完成。
- 已绑定不支持策略的外部插件会明确拒绝，不能无声绕过插件或TLS。
- 随机代理与模式一固定出口冲突：要求先选固定代理或直连。没有新增专属分组代理链或上游凭据加密。
- 稳定身份和连接保真不能证明上游隐藏策略；模型质量对照评测尚未进行。不得用模板存在、HTTP200、测试名称或配置开关代替实际效果证明。

后续仅在用户授权后考虑部署；本次交付只在本地目录和归档。
