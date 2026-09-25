# Cyber 会话屏蔽的身份边界

`cyber_session_block_enabled` 控制上游实际返回 `cyber_policy` 后的本地会话屏蔽。仅凭请求历史长度或共享来源信息，不判定会话已被封锁。

## 支持的身份

本地封锁按认证后的 API Key ID、身份类型和明确会话 ID 派生独立哈希，按以下优先级读取：

1. 对话/线程请求头：`conversation_id`、`thread_id`、`thread-id`、`X-Conversation-ID`
2. 请求体：`client_metadata.thread_id`
3. 会话请求头：`session_id`、`session-id`、`X-Session-Id`、`X-OpenCode-Session`
4. 请求体：`client_metadata.session_id`

普通 HTTP、流式 HTTP 和 WebSocket `response.create.response` 包装均使用同一套解析规则。若同一类型的请求头和请求体同时存在但值不一致，或者同类型的多个请求头互相冲突，本次请求视为没有可信身份：不写入跨请求封锁键，`usage_logs.session_id` 也留空，避免错误封锁另一个会话。

同一会话在 HTTP / WebSocket 重连时应保持 ID 稳定。共用一个 API Key 的转发网关必须为不同下游用户的不同会话提供互不复用的 ID，例如由可信网关按用户与会话生成不透明标识。客户端声明的会话 ID 不是用户认证凭据；本机制用于抑制重复请求，不替代上游内容审核。

`prompt_cache_key`、`X-Session-Affinity`、IP、User-Agent、历史相似度和条目数均不构成封锁身份。没有明确会话 ID 时，不写入或查询跨请求的会话封锁；上游真实策略拒绝仍正常透传并记录。同一 WebSocket 连接已有的命中后阻断语义保留。

## 身份状态与可观测性

网关把身份解析结果划分为四类：

- `resolved`：得到唯一可信身份；
- `missing`：请求没有提供受支持的身份；
- `conflict`：同类型请求头互相冲突，或请求头与请求体值不一致；
- `invalid`：身份不是字符串、包含控制字符、超过存储长度上限或不是有效 UTF-8。

进程内只累计上述状态、WebSocket 连接继承次数、严格门控拒绝次数和连接内显式换身份次数。每 1024 次观察输出一次聚合结构化日志，不记录原始身份、API Key、用户、IP 或请求体。上游 `cyber_policy` 的既有运维错误记录会附加身份状态、类型、来源以及是否继承，便于判断某次命中为什么能够或不能建立精准会话封锁。

## 可选严格门控

`cyber_session_identity_strict_enabled` 默认关闭，并且只有 `cyber_session_block_enabled=true` 时才生效。开启后，`missing`、`conflict`、`invalid` 请求会在账号选择和上游转发前以客户端参数错误拒绝。后台界面会显示高风险提示；升级不会自动开启。

建议先根据聚合日志确认所有客户端都稳定发送 `thread_id` 或 `session_id`，再考虑开启严格门控。关闭严格门控时保持兼容行为：身份不可信的请求仍会交给上游审核，但不会生成或查询跨请求封锁键。

## WebSocket 连接身份

WebSocket 连接首次出现 `resolved` 身份时会把对应的单向哈希绑定到该客户端连接：

- 后续轮次省略身份时，沿用当前连接已确认的身份做封锁查询和 Cyber 命中写入；不修改客户端请求体，也不伪造请求头；
- 后续轮次再次提供相同身份时正常处理；
- 后续轮次显式切换身份类型或值，或者在已绑定后提供冲突/非法身份时，关闭连接并要求客户端重连，避免把一个会话的风险归到另一个会话；
- 继承身份的轮次触发 `cyber_policy` 后，写入的仍是 API Key ID + 身份类型 + 身份值对应的 v3 键，因此客户端重连后会命中同一封锁；
- 因会话身份门控或本地 Cyber 会话封锁关闭连接时，按客户端/本地准入结果记录，不计为上游账号故障，也不会因此触发账号冷却；
- 没有任何可信身份且严格门控关闭时，仍保持 fail-open，不根据连接来源猜测身份。

## 升级影响

新的会话封锁使用带 `thread` / `session` 类型的 v3 哈希命名空间。读取时临时兼容显式身份旧 v2 键，直到这些键按既有 TTL 自然过期；新命中只写 v3。更早版本中无法区分显式会话 ID 和缓存 key 的旧键不能安全继承，仍留在 Redis 中按原 TTL 自行过期，不迁移或删除。

滚动升级期间，旧实例仍执行旧逻辑；需要所有实例运行修复版本才能消除该误拦。本变更不自动解除下游网关已经记录的用户或会话限制。

问题记录：[上游 issue #6831](https://github.com/Wei-Shaw/sub2api/issues/6831)。
