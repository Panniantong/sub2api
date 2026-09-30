# 下游 Cookie 回传与使用记录

普通 OpenAI HTTP 的 Responses、Chat Completions、Messages 请求，在进入账号覆盖逻辑前保存客户端 Cookie。有客户端 Cookie 时，按客户端原有名称和值生成独立的 `Set-Cookie` 响应头，不复制内部 Cookie 的属性。没有客户端 Cookie 时，回传上游实际响应中的 `Set-Cookie`，保留属性、多条记录和删除 Cookie 指令；上游响应也没有时留空，不使用上游请求中的覆盖值补齐。该规则在响应提交前执行，不依赖默认响应头白名单是否放行 Cookie。

实现使用 Gin 响应写入器，在实际 Write、WriteString、Flush、WriteHeaderNow 前处理，不提前提交响应，因此首输出前的错误切换保持原有行为。多个账号尝试复用同一入站快照。实际发往上游的账号 Cookie、上游 Cookie 采集和原有管理员上游记录保留。WS 握手不在本次修改范围内。

重试时清除上次尚未提交的上游 Cookie，使用当前响应快照；一旦响应提交，记录只保留实际发出的 Cookie。客户端与上游均未提供 Cookie 的旧记录仍为空，不反向填充。

管理员使用记录增加 `downstream_request_cookie`、`downstream_response_cookie`，存于现有 `request_debug` JSON，不需要数据库迁移。入站多值采用 `; ` 拼接，响应按现有响应 Cookie 格式以换行连接各条 `Set-Cookie`。界面沿用 Host、长度、详情弹窗和复制格式；Excel 导出同步增加两列。历史记录缺少快照时显示空值。

验证包括流式/非流式、无 Cookie、多 Cookie、账号覆盖、重试前后快照、提交前不虚报回传、内部 Cookie 不泄漏、保留用量与上游记录，以及真实 HTTP 客户端在自定义白名单放行 Cookie 时仍收到原值。用量写入/读取和管理员 DTO 测试通过，普通用户 DTO 不新增这些字段。启用 unit 标签的流式/Chat/计费定向回归、全包编译、前端 i18n/类型检查/构建通过。

2026-09-30 已单独部署至 9092，镜像 `sub2api-piao-9092:cookie-isolation-20260930`。线上 Responses 流式、非流式各一次测试均返回 200，客户端 Cookie 精确回传，两列落库并可经管理员 API 读取；CPA 设置仍为 `cpa`，非流式首字指标为空。健康、首页、管理员登录通过，其余 13 个容器 ID 和启动时间保持不变。
