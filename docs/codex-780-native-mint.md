# 780 原生采票

在既有后台采票、手动采票、账号缓存与 `/admin/harvest-flow` 上扩展；不需要云函数或另一个公开凭据中继。

## 配置与迁移

新安装默认 `gateway.openai_codex_ticket.target_length: 780`、`ttl_seconds: 240`、`refresh_before_seconds: 60`。已有配置若显式写了 292，应修改该长度配置后重启。保留显式 292 的兼容路径；不自动启用旧系统的采票开关。旧 292 票无法通过 780 的验收，不需要删除数据库历史。

在原有采集设置中选择 SSE 或 WebSocket、目标网关（默认 `unified-95`，可改为参考脚本使用的 `unified-88`），以及可选的公网边缘 IP。边缘 IP 留空时使用 DNS。仍使用原有采票代理、范围、请求预算和取消机制。WebSocket 下游若由 HTTP bridge 转为 SSE，则选择 SSE 采票。

边缘 IP 等价于参考中继的 `X-Edge-IP` 控制：连接指定 IP，HTTP Host、TLS SNI 和证书检查仍为 chatgpt.com。通过 HTTP CONNECT 或 SOCKS 代理也将目标 IP 作为拨号地址。管理员手动采票接口 `POST /api/v1/admin/accounts/:id/manual-harvest` 也接受 `X-Edge-IP`，仅覆盖本次新采票的拨号地址，不修改已保存设置、不外发该头；有效缓存仍按原流程复用。这个设置仅用于采票，普通业务请求不接受用户提供的任意拨号地址。边缘 IP 不是 gateway，gateway 由路由 Cookie 声明。

## 验收和复用

- 检查 Fernet 格式、780 字符长度、签发时间（允许最多 30 秒时钟偏差），有效期从签发时刻计算且最多 240 秒。
- SSE 只读取最多 16 KiB 的完整 `response.created` 事件；要求非空 response ID 和精确匹配的模型声明。WS 单消息最多 16 KiB、累计最多 64 KiB，接受 `codex.response.metadata` 中的票，要求同时有 created 声明。
- 只保留 `__cflb`、`__oailb` 两个路由 Cookie；要求完整 pair、未到 JWT exp、目标网关匹配。JWT 解析不等于签名验证或能力验证。
- 定向请求可复用仍有效的原 pair。服务端只轮换一半或删除 Cookie 时拒收，不拼接不同轮次的 pair。
- 延用账号/模型缓存；更换协议或目标网关后不复用旧策略票。同一账号模型只维护当前策略及备用票，不同时维护多个协议票池。注入阶段再次拒绝协议混用。
- 票龄和 Cookie 的 JWT 有效期分别检查。780 票不使用旧 responses-lite 请求模板。

`response.created` 只证明模型声明，780 也只是格式；不能代替真实任务验证。开启 fail-closed 后，协议不匹配或缺票的请求会被拒绝；关闭时不注入不匹配的票。

## 验证

本地回归覆盖票龄、路由完整性/目标/过期、完整事件解析、代理参数、WS metadata、直拨保留 Host/SNI，以及错误证书拒绝。页面沿用原有保存/草稿逻辑。真实账号测试、上线状态应分别记录，不能从模拟测试推断线上能力恢复。
