# 普通 HTTP 流式首字优化

## 2026-09-30：CPA 统计口径

新增 `openai_ttft_mode=cpa`，在 9092 单独启用。普通 OpenAI HTTP 流式请求的 `first_token_ms` 从上游 `RoundTrip` 开始，到响应体第一次 `Read` 返回 `n > 0`；包含响应头等待，但响应头到达本身不结束计时。前置事件、心跳、未完成的一行数据均算首字节，不等同于用户收到正文。数值按实际毫秒记录，不人为压低。

每个 HTTP 响应持有独立的原子计时状态；失败尝试不会污染后续响应。普通 HTTP 在共享连接池之上包裹传输层，TLS 指纹路径同样支持；插件等自定义传输使用调用边界回退。Responses（含透传）、Chat Completions、Messages 及 OpenAI 协议转换路径只在组装流式结果时读取该计时，原有首个有效事件、输出暂存、超时、重试、usage 提取逻辑不依赖它。非流式结果和 WS 不改口径。历史 `semantic`/`visible` 选项及默认值保留。

统计变更只影响启用后的新请求，不重写历史用量。调度和统计读取 `first_token_ms` 时也会使用新口径，切换前后样本不宜直接比较；总耗时及用量计费不变。下文关于“原有 TTFT 不变”的描述对应此前转发优化阶段。

本轮验证包括分片首读、零字节/EOF、首读同时 EOF、响应头等待、重试隔离、并发快照、五条流式处理路径的结果及正文保留，以及 CPA 首字节已记录但只有前置事件时仍触发原生 Responses 超时。全包编译、前端类型检查/i18n/构建及既有定向首字回归通过。

9092 已部署 `sub2api-piao-9092:cpa-ttft-20260930` 并启用 `cpa`。两次同模型 `gpt-5.6-sol` 普通 Responses 流式请求记录 `first_token_ms=1959/773`，客户端正文分别约 `7514/7939 ms`，请求均完整结束并记录 usage。首字节与正文时间的差值不能归因于网关排队：CPA 允许前置事件计入，模型正文仍可能较晚到达。健康、首页、管理员登录、设置读取均通过；其余 13 个容器 ID 和启动时间不变，9092 调试覆盖已撤下，恢复 `LOG_LEVEL=info`。旧数据未修改。

本次处理 HTTP/SSE 的首个有效内容发送延迟，不涉及 WebSocket。已实现两项转发路径修复和可选诊断。没有调整模型、计费、账户选择策略或上游超时值，没有部署或测量线上效果。

## 已实现

### 首个有效内容及时刷新

Responses 流中，搜索等语义进度事件可能先填写现有 `firstTokenMs`。此前首个有效内容的强制刷新依赖该指标为空，导致正文到达时可能继续等待队列中的其他事件。

现在使用独立状态记录首个有效内容是否已经刷新。完整首事件到达即强制刷新，后续内容保留原有批量发送逻辑。仍在完整 SSE 事件边界刷新，保留输出前错误切换、客户端断开后读取 usage 等行为，现有 TTFT 计量语义不变。

### 保留不同配置的热连接池

HTTP 客户端缓存键增加上游 profile，避免同一个账户或代理的不同响应头超时策略互相替换缓存。该问题在 TLS 指纹客户端中可复现，因为不同 profile 原本共用协议缓存键。

保留账户/代理隔离、协议降级、闲置清理和缓存容量限制。不同 profile 可各保留一个池，因此混用 profile 时可能保留更多连接；没有提高任何连接上限。普通 HTTP 连接复用另有真实本地服务器回归测试。

### 可选耗时诊断

通过现有 `log.level: debug` 启用，结束流式请求时输出 `gateway.http_stream_latency` 日志。默认 info 级别不安装 SSE 观察器。支持普通 POST `/responses`、`/chat/completions`、`/messages` 及已挂载的别名路径；GET、WS 和 compact 不启用。

观察器不改写协议、不延迟实际数据发送；它识别完整事件中的正文、推理、工具参数等有效内容，忽略心跳、空 delta、角色声明和纯 usage。诊断日志不记录提示词或输出正文。单事件观察限制为 256 KiB，超限跳过该事件并标记 `event_observation_limited`，随后恢复观察。

## 指标口径

时间单位为毫秒，可含小数。缺少观测的指标不输出，不填零。

| 字段 | 含义 |
| --- | --- |
| `first_output_ms` | 从诊断中间件进入到首个完整有效事件调用下游 Flush 完成 |
| `first_text_ms` | 同一起点到首个正文事件 Flush 完成，与推理/工具输出区分 |
| `first_output_kind` | 首个有效事件类别，如 text、reasoning、tool |
| `pre_upstream_ms` | 从中间件进入到第一次调用上游 HTTP 客户端，包括此前鉴权、选账户、排队等 |
| `account_selection_ms` | 已接入的 OpenAI 默认调度器选账户耗时，多次调用累计 |
| `user_queue_ms` / `account_queue_ms` | 实际进入对应等待槽位函数的耗时，含失败等待，跨尝试累计 |
| `upstream_attempts` | 本次请求进入上游 HTTP 客户端的尝试数 |
| `connection_acquire_ms` / `connection_reused` | 最后一次尝试取得连接耗时及是否复用连接 |
| `dns_ms` / `tcp_ms` / `tls_ms` | 最后一次尝试中观测到的连接阶段耗时；复用连接或自定义 transport 可能缺项 |
| `request_written_ms` | 从最后一次上游尝试开始到请求写出 |
| `response_header_first_byte_ms` | 从最后一次上游尝试开始到收到首个响应头字节 |
| `response_ready_ms` | 从最后一次上游尝试开始到响应交回业务层，包含客户端获取及解压初始化 |
| `upstream_body_first_byte_ms` | 从最后一次上游尝试开始到业务层首次读到 SSE 响应体字节 |
| `upstream_first_output_ms` | 从最后一次上游尝试开始到观察器识别首个完整有效事件 |
| `relay_first_output_ms` | 从上游有效事件被读取到下游有效事件 Flush 完成 |
| `upstream_protocol` | 上游响应协议 |

阶段耗时存在包含关系，不能简单相加。入口计时不含此前的其他中间件，也不含客户端到网关的网络时间。Flush 只代表服务器执行了刷新，不证明浏览器或客户端已收到数据。事件观察是有限识别，超大事件或未覆盖的供应商格式可能导致缺项或偏晚。

原有 usage、计费和调度器 `first_token_ms` 没有改口径；不能直接拿这些字段与新指标混合比较。CPA 的首个 body read 也不能直接等同于用户看到首字。

## Review 修复

复查发现并修复了诊断层的三个问题：

- 同一 Responses 完成事件或 Chat 多 choice 事件包含工具/推理及正文时，原实现只识别首个类别，可能漏记 `first_text_ms`。现在独立识别同事件正文。
- Anthropic `content_block_start` 携带非空正文、Responses done 事件携带完整工具参数/推理等情况下，原实现漏记有效内容。已补齐识别，并将 reasoning item 的内容归为推理，避免误计为正文。
- SSE 纯 CR 换行及开头 UTF-8 BOM 原本无法正确识别。现在支持 CR、LF、CRLF 及跨分片换行，保持超大事件跳过和恢复逻辑。

上述问题均有修复前失败的回归用例；另外补测了旧尝试延迟回调不污染新尝试计时、下游写入失败不记录首字成功，以及不同换行/分片下的超大事件恢复。首字强制刷新和连接池实现未发现新的需修复问题。

第二轮 review 聚焦正常请求兼容性，未发现需要修改业务代码的新问题。新增真实 HTTP 客户端测试覆盖 Responses、Chat Completions、Messages 三种流格式，分别运行于 info/debug 日志级别：服务器先发送首事件、等待客户端读取后才允许发送后续内容，验证诊断中间件不把首事件缓冲到请求结束；并逐字节比对心跳、正文、300 KiB 后续内容及终止事件，检查响应头。另验证 debug 下非流式 200、401、429 的状态、响应头和 JSON 正文保持原样，不输出流式诊断日志。

本轮 Responses/透传/Chat 流式、取消/断流计费/切换、槽位等待释放、路由白名单的定向回归通过；streamlatency 与 middleware 全包测试通过。repository 全包仍有原已确认的 1 个计费设置 mock 失败和 3 个缺少 `sh` 的 pg_dump 测试失败，没有新增失败。上述为本地测试结果，未进行生产环境真实模型调用，race 验证限制仍同下文。

## 验证记录

验证环境：Windows，Go 1.27.0。

- Responses 刷新测试通过，覆盖完整事件边界、突发事件、错误、断流及 usage。新增“语义进度先于正文”测试在旧代码失败、修复后通过。
- 不同 profile 的缓存和超时测试通过；TLS 指纹池复用测试在旧代码失败、修复后通过。普通 HTTP 本地网络复用测试通过。
- SSE 观察器、httptrace 组合、重试计时、debug 开关、中间件协议保持测试通过。
- Chat Completions 转发、取消、断流计费、错误切换，以及槽位等待/释放和路由白名单定向回归通过。
- 所有包编译检查通过。服务端构建产物位于 `backend/.codex-tmp/sub2api-first-byte.exe`。

全量测试未全绿：8 个已有 Cookie/WS/OAuth service 用例及账户计费设置 mock 用例，在通过 Go overlay 移除本次集成改动后仍失败。另有 Windows 缺少 `sh` 引起的 pg_dump 测试失败；handler/routes 全量测试遇到已有 Cookie 后台采集 mock 的空指针异常。因此不能将定向回归通过解释为全仓库测试通过。当前环境没有可用 C 编译器且 CGO 未启用，未完成 race 验证。

## 上线验收方式

使用同一模型、账户池、请求输入、输出长度和并发，分别收集冷连接、热连接以及需要排队的样本。短期开启 debug 诊断，比较 `first_output_ms`、`first_text_ms`、`relay_first_output_ms` 的 P50/P95/P99 和连接复用比例，同时检查错误率、重试率、usage、取消请求及工具调用完整性。客户端测试必须解析到完整正文事件，不能只以 HTTP 响应头或心跳作为首字。

网关内额外转发延迟可向毫秒级优化；端到端首字还包含上游模型生成、排队及网络耗时。当前尚无线上数据，不能承诺端到端已经达到毫秒级。若上游很快而客户端仍慢，应继续定位反向代理缓冲、压缩和客户端渲染；若耗时集中于上游或排队，则据实测另行调整，避免无依据放大并发或削弱重试与计费保障。

## 2026-10-10：前置事件在首个输出项时下发（接入方首字对齐）

现象：9091（本项目）对接到 8080（另一套 Sub2API，`openai_ttft_mode=semantic`）。最近 2 小时同一批约 3.2 万条流式请求，9091 自己记录的 `first_token_ms`（cpa 口径）p50 1.6 秒、p90 5.3 秒；8080 对 9091 渠道记录的 p50 8.0 秒、p90 17.8 秒；两边总耗时 p50 都约 19 秒。同一时段 8080 的 pro 渠道（open.codex521.cc）跑同模型同 effort（gpt-6.1-sol high）总耗时 19.7 秒与 9091 渠道的 20.3 秒接近、输出量相当，但 8080 记到的首字是 0.7 秒。

原因：Responses 流式在首输出前把 `response.created`、`response.in_progress`、空 `response.output_item.added`（reasoning，summary 为空）全部暂存在 attempt 本地 stage，直到首个可见/语义内容才一起下发，用于"输出前错误换号"。接入方按语义事件统计首字时，收到的第一个非元数据事件就是推理完成后才放出的那批，因此 8080 的首字等于模型思考时长，而 pro 渠道是立刻转发前置事件。

改动：新增网关转发设置 `openai_stream_structural_commit`（默认开启，设置页「Responses 流式前置事件立即下发」）。开启时首个 `response.output_item.added` 即提交首输出：stage 中的前置事件立刻刷给接入方，后续事件沿用原有批量发送与首段正文独立刷新。关闭时恢复原行为。三条路径一致：原生 Responses 转发、透传、WS→HTTP 桥接的首轮暂存。

边界：提交后流内再出现可重试错误（如上游 429/过载）不再由本网关换号重放，`response.failed` 原样下发，由接入方自己的 failover 处理。上游有一种已知模式是先发 `response.created`、空 reasoning `output_item.added`、空 summary part 和若干 keepalive，再推过载 error（回归 `TestOpenAIStreamMetadataAndKeepaliveBeforeOverloadFailOver` 即此场景）；开启后这类失败会到达接入方。同理，`openai_first_output_timeout_seconds` 只守到首次提交：开启后首个输出项即提交，之后上游停滞不再触发首输出超时换号（9091 当前未启用该超时）。最近 2 小时 9091 上游阶段记录 2439 条 overloaded、3379 条 429，占成功请求约 18%，其中多少发生在首个输出项之后未单独统计；接入方（8080）对 `response.failed` 同样走自己的换号，终端用户仍能得到结果，但 9091 渠道在接入方侧的错误计数会上升。`first_token_ms` 的三种口径不变；若希望两边数字直接可比，9091 应与 8080 一样使用 `semantic`（当前 9091 为 `cpa`）。本改动只让接入方更早收到前置事件，模型产出首段正文的时间不变。

验证：单测覆盖提交判定、默认值与缓存回退、开启时前置事件在无可见内容时即下发、关闭时继续暂存且顺序不变、提交后可重试失败改为原样下发而关闭时仍换号。`go build`、`go vet` 通过；FirstOutput/Flush/TTFT/Setting 相关回归通过，仅剩分支既有的 `TestOpenAIGatewayService_APIKeyPassthrough_RebuildsUpstreamErrors` 失败。前端 `vue-tsc`、SettingsView 与 i18n 完整性测试通过。未部署、未改线上设置。

### 同一时段的另一项发现：选号阶段排队后拒绝

9091 `ops_error_logs` 最近 2 小时有 30155 条 `routing` 阶段 503「Service temporarily unavailable」，全部来自分组 5（降智分组）；同期该组成功请求 9716 条，即约四分之三的降智请求在等待后被拒。被拒请求的 `routing_latency_ms` p50 34.8 秒、p90 89 秒（对应 `gateway.scheduling.fallback_wait_timeout` 默认 30 秒与 `sticky_session_wait_timeout` 默认 120 秒的等待）。错误按 10 分钟分桶呈阵发（11:20–11:40、12:00–12:20 各数千条）。分组 2（正常账号）同期只有 2 条。

活跃池运行态（分组 2）：当前需求 85、窗口峰值 93、目标容量 168、就绪容量 180，成员 active 9 / exhausted 71 / waiting 180 / standby 9；分组 2 共 40 个可调度账号、并发合计 800。降智分组只能使用 Host 全部冷却（exhausted）的账号，容量取决于有多少账号处于该状态，与分组 2 的容量策略无关。

这部分不是转发代码问题，是容量与等待策略：

- 接入方视角，等 35 秒再收到 503 比立刻收到 503 更糟——8080 自己有多渠道 failover，越早拿到 503 越早切走。可考虑把 9091 的 `GATEWAY_SCHEDULING_FALLBACK_WAIT_TIMEOUT` 从 30s 降到数秒、`GATEWAY_SCHEDULING_STICKY_SESSION_WAIT_TIMEOUT` 从 120s 降到十几秒（环境变量名由 viper `.`→`_` 映射得到，需在 compose 中设置并重启）。
- 降智分组要么增加可降级账号 / 放宽降级条件，要么在接入方降低该渠道权重。
- 这些是部署与策略调整，本次未改线上配置。
