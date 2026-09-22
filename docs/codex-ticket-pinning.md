# Codex 门票固定出口与会话身份

移植来源：用户提供的 mracry/sub2api 源码快照（ZIP SHA-256 `2151721700fe475982e594cf47fb94ffdbe18d26e9aebdd3216d7584665b73f6`）及《Codex门票钉住原理.md》。原作者的 30 分钟实测属于其报告，本分支尚未复现。

本版将票据绑定到采票时的出口节点和会话身份。打票成功后，HTTP/SSE 与 WebSocket 业务请求复用相同出口；请求体会移除采票时不存在的 `client_metadata`、`prompt_cache_key` 和设备身份字段，并沿用上游返回的 `x-codex-turn-state`。

同一账号在业务请求期间暂停采票，避免后台探针轮换或消耗当前会话。票据过期、被 312/响应形态拒绝或出口节点不可用时，当前会话失效，后续请求需要新开会话并重新采票。

节点选择使用定向 Mihomo Selector，并通过节点指纹确认运行时节点没有被替换。节点命中、失败、延迟和冷却记录写入 `codex_harvest_nodes`；采票流程事件写入 `codex_harvest_flow_events`。启用此功能需要执行迁移 239 和 240。

292/312 只是 `x-codex-turn-state` 的形态，不能单独证明模型质量；发布验收仍需查看实际 `response.model`。
