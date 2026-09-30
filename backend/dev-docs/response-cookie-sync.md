# 响应 Cookie 同步与目标 Host 刷新

配置入口：Cookie 库 → 获取配置 → 来源三：上游响应 Cookie 同步。
`response_cookie_sync_enabled` 默认关闭，独立于本地采集、远程同步及 WS。

- 正常 HTTP 上游响应只复制最多 32 KiB / 64 条 Set-Cookie；128 条队列非阻塞提交，单个后台 worker 空闲后退出。
- 后台独立读取配置、检查 HTTP 成功状态、有效 __oailb host/exp 和 Host 白名单，再更新 Cookie 库；请求取消不取消已接收的任务。
- 每个规范化 Host 一条记录；优先比较双方 JWT iat，其次 exp，缺少可比较时间时回退采集时间。相同 token / 相同时间不覆盖。比较和更新使用同一互斥锁。
- 本地采集及远程同步使用相同的新旧判断，避免回退。管理员整库替换仍为显式替换操作。
- 获取日志 task/kind=response_sync，stage 区分 updated、ignored_older、ignored_status、ignored_invalid、ignored_host、failed、queue_full。数据库失败时保留原值；日志存储自身失败写服务日志。
- 高峰期队列满时跳过同步并汇总记录，不阻塞转发。队列不落盘，进程退出时未处理任务可能丢失。
- 原有上游使用记录、下游 Cookie 回传不受入库结果影响。

动态采集中的“即将过期刷新”直接读取目标 Host 的当前 Cookie，用已配置的采集账号凭据和采集代理发起 HTTP 请求。只接受同 Host 且更新的有效响应 Cookie；不把带 Cookie 的定向请求作为代理路由学习样本，不修改采集账号绑定、票据、会话或触发绑定验证。目标已过期/缺失时跳过，交由补齐任务处理。库中的 Host 即使无代理历史也可成为刷新目标。

验证覆盖：并发 Host 去重、新旧顺序、配置读写、白名单、错误/删除/过期响应、队列满与阻塞存储、请求取消、流式/非流式及上下游 Cookie 隔离、目标刷新请求凭据与 Host 一致性。

Review 修复：后台配置快照不占用转发配置锁、不覆盖管理员更新的缓存；本地/Relay 采集拒绝旧 Cookie 后使用库中当前 Cookie 验证；刷新发送前复查窗口，目标已续期则跳过；连续无更新的刷新进入目标退避。响应同名 Cookie 后续删除会清除前值，正数 Max-Age 优先于 Expires。跳过日志统一显示为提示色。
