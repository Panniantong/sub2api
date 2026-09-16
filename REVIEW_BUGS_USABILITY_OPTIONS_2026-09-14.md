# 完整源码审查：bug、易用性、操作流程与账号测试

日期：2026-09-14。本轮仅审查并提出方案，未修改业务代码、配置或服务器，也未调用真实上游账号。新增的诊断用例放在本机临时目录，通过 Go overlay 和提取原前端函数运行，不进入项目构建。

结论：当前最突出的问题是测试结论的含义不准确，以及用户选择、后台实际执行、页面显示之间仍有不一致。前一轮解决了传输身份和配置保存等问题，但没有解决本轮发现的判分语义问题；测试全绿不能证明产品判定合理。

## 一、为什么答案正确仍显示“疑似降智”

**已确认的根因：标准答案评估器把格式不合规、无法抽取答案和答案不匹配都保存为 `suspected_degradation`，分数均为 0。**

实际判断条件不是只看数学结果。它要求全文只能有一行符合 `ANSWER: 答案` 的文本，这行必须是最后一个非空行，抽取后的字符串还必须与配置的标准答案完全一致。代码支持英文大小写和中文冒号，但不进行单位、Markdown、全角数字或数值等价归一化。

位置：[评分器](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_evaluator.go:40)、[状态写回](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_service.go:242)、[显示文案](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/admin/intelligent-tests/display.ts:4)。

使用配置标准答案 `12`，直接运行当前评估器得到：

| 模型输出 | 当前结果 | 当前原因 |
| --- | --- | --- |
| `ANSWER: 12` | 成功，100 分 | 满足格式及字符串比较 |
| `12` | 疑似降智，0 分 | 没有规定前缀 |
| `最后盒子里有12颗糖。` | 疑似降智，0 分 | 没有规定前缀 |
| `ANSWER: 12颗` | 疑似降智，0 分 | 单位使字符串不相等 |
| `**ANSWER: 12**` | 疑似降智，0 分 | Markdown 加粗导致匹配失败 |
| 代码块中的 `ANSWER: 12` | 疑似降智，0 分 | 最后一行是代码块结束标记 |
| `ANSWER: 12` 后再补一句说明 | 疑似降智，0 分 | 最后一行不符合协议 |
| 连续两行 `ANSWER: 12` | 疑似降智，0 分 | 规定只能出现一次 |
| `ANSWER: 12.0` 或 `ANSWER: １２` | 疑似降智，0 分 | 未做数值/字符归一化 |
| 错误推导后写 `ANSWER: 12` | 成功，100 分 | 完全不检查推导过程 |

这解释了“题目结果正确，但显示疑似降智”的确定性触发方式。格式遵循可以单独评价，但当前界面把这些不同性质的问题合并成能力风险，结论过强。

现有测试还把直接输出 `12` 应得到 `suspected_degradation` 写成了预期，因此之前测试通过并不能发现这个产品逻辑问题。参见 [现有评估测试](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_runner_test.go:155)。

**对具体那条记录的判断边界**：本次没有读取你看到的那条服务器记录。如果输出确实只有一个末行 `ANSWER: 12`、快照里的标准答案也是 `12`，当前评估器会判成功；此时应核对该记录的 `evaluation.reason`、`actual_answer`、`expected_answer`、配置快照和记录 ID，确认是否看到了旧记录、另一测试类型的汇总风险或尚未刷新的页面。不能仅凭截图上的最终数字确定具体分支。

## 二、其他已发现的问题

等级说明：P1 优先处理，可能改变真实运行行为或导致错误结论；P2 影响可靠性、操作判断或效率。“复现”使用合成数据；“源码确认”表示调用链可确认，但没有操作真实数据库或服务器。

### B01 · P1：受保护账号选择随机代理，可能清空固定代理

**服务层已复现。** 编辑页勾选随机代理会同时提交 `extra.proxy_mode=random` 和 `proxy_id=0`。后端受管字段保护保留原固定模式，但后续仍处理 `proxy_id=0`，清空固定代理。

合成账号原为初代兼容、固定代理 ID 9；请求后返回：`random=false`、`proxy_id=null`、保护模式仍为 `legacy`。因此账号可能回到直连或默认出口，未得到用户所选的随机代理。

位置：[前端关联字段提交](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/account/EditAccountModal.vue:6067)、[受管字段合并](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/admin_account.go:698)、[清空代理](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/admin_account.go:754)。数据库保存路径也会按空 ProxyID 清除关联，但本次没有对数据库或真实出口做修改验证。

可选处理：直接拒绝与保护策略冲突的整次操作，并保留原代理；或提供独立的“切换代理与保护策略”预览流程，一次确认全部关联变化。不能仅忽略其中一个字段。

### B02 · P1：慢接口会被自动刷新反复取消

**已用原前端 `load()` 函数复现。** 静默刷新不设置 `loading=true`，每 5 秒定时器仍会发起下一次请求；`load()` 开始时又取消前一次请求。接口耗时持续超过 5 秒时，列表可能一直停留在旧结果。

诊断中连续触发三次静默刷新，产生三次请求、取消两次、没有结果落到页面；停止刷新后最后一次请求才可显示结果。这会放大“后台已经完成，界面却仍显示旧状态”的问题。

位置：[load 函数](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/views/admin/IntelligentTestsView.vue:95)、[定时刷新](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/views/admin/IntelligentTestsView.vue:155)。

可选处理：请求完成后再安排下一次刷新；或增加独立的请求进行中标记，静默请求也参与去重。用户改筛选时仍可主动取消旧请求。

### B03 · P1：用户明确选择的测试模型可能被静默替换

**已复现。** OpenAI OAuth 测试中，明确传入 `gpt-5.4` 或 `gpt-5.4-mini` 会变为 `gpt-5.3-codex`。代码没有区分“留空使用默认值”与“管理员主动选择了这个模型”，并会覆盖配置快照中的模型。

影响：测试结果不能直接用于评价用户选择的模型，单靠最终记录也难以恢复原请求意图。本条只说明本地改写行为，不判断真实上游当前支持哪些模型。

位置：[模型替换](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_runner.go:32)、[快照覆盖](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_runner.go:121)。

可选处理：仅为空值选择默认模型；显式模型不可用时返回清晰错误。确需兼容映射时，同时保存并展示“请求模型、实际模型、映射原因”。

### B04 · P2：同账号同测试类型的任务复用忽略模型与题目差异

**源码确认。** 新任务的唯一性只按账号、测试类型和进行中状态判断；即使新请求指定了另一模型，后台也可能返回旧任务 ID。虽然接口有 `reused`，两个提交入口都仍提示“已加入队列/已提交 N 个任务”。

影响：管理员以为新模型/新配置已开始测试，实际拿到旧模型/旧配置的任务和结果。

位置：[进行中任务复用](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/repository/intelligent_test_queue.go:110)、[提交提示](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/views/admin/IntelligentTestsView.vue:136)。

可选处理：参数相同时明确提示复用；参数不同则拒绝并说明冲突，或排在现有任务之后。不要以新的提交成功文案掩盖旧任务复用。

### B05 · P2：换模型后重试仍复用旧幂等键

**前端签名已复现，后端冲突分支已确认。** 快速测试签名只包含账号和测试类型，不包含所选模型；后端请求指纹却包含模型。若首次请求已提交但响应丢失，管理员换模型后重试会复用旧键，触发 409 冲突。

位置：[快速测试签名](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/admin/intelligent-tests/QuickTestDialog.vue:54)、[模型覆盖参数](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/admin/intelligent-tests/QuickTestDialog.vue:58)、[后端指纹](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/repository/intelligent_test_queue.go:25)。

可选处理：签名覆盖完整、规范化后的提交参数；仅原样重试复用旧键，任何参数变化生成新键，并区分“重试提交”与“重新运行”。

### B06 · P2：SVG 结构通过被显示成满分成功，不能代表画对了

**已复现。** `<svg><rect/></svg>`、零宽高矩形及一个简单圆都能得到 100 分。评估器只检查结构、安全词汇及是否存在图形节点，不检查鹈鹕、自行车、部件关系或可见画面。

位置：[SVG 评分](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_evaluator.go:29)、[形状计数检查](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_evaluator.go:166)。

结构检查本身有价值；问题在于“满分成功”容易被理解为图像或模型能力满分。方案是单独显示“SVG 结构通过，内容未评估”，或另设内容审核。任何方案都应保留现有 SVG 安全过滤。

### B07 · P2：参数错误会被误分类成账号认证异常

**已复现。** HTTP 400 的 `max_output_tokens must be positive`、`max_tokens exceeds request limit` 都被归类为 `account_error`，因为分类器只要发现 `token` 字样就按账号问题处理。

位置：[错误分类](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_runner.go:296)。

影响：运营可能去刷新授权、停用或更换本来正常的账号。可选处理是优先使用 HTTP 状态及结构化错误码，再区分请求参数、认证、模型、网络、限流和评估异常，最后才使用受限关键词兜底。

### B08 · P2：正确文本只出现在终态事件时，会被当作无输出

**已复现，属于兼容性条件触发。** 给现有流解析器输入带 `output_text=ANSWER: 12` 的 `response.completed`、不提供 delta，解析结果是“完成=true，抽取文本为空”。后续智能测试因空结果判失败。

位置：[Responses 流解析](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/account_test_service.go:2880)、[空输出拒绝](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_runner.go:183)。

可选处理：在没有累计文本时从终态输出提取文本，避免重复拼接。若仅支持严格 delta 流，也应显示“响应格式不兼容”，不能把它解释为账号能力问题。本条通常产生“测试失败”，并非“疑似降智”的直接分支。

### B09 · P2：测试任务没有共享正常业务的账号并发准入

**源码确认，尚未做真实负载实验。** 智能队列限制自身的进行中任务，但 AccountTestService 没有使用正常网关的账号并发槽。传给 HTTPUpstream 的并发参数是连接池大小，不等同于 HTTP/2、WSS 和不同传输池之间共享的请求准入。

因此正常业务已占满账号额度时，测试仍可能额外发送请求，尤其在低并发策略或混合 HTTP/WSS 场景下影响正常用户。

位置：[测试执行](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_runner.go:104)、[HTTP 参数含义](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/repository/http_upstream.go:192)、[队列自身限制](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/repository/intelligent_test_queue.go:156)。

可选处理：测试共享账号准入并降低优先级；或使用明确预留的测试容量/专用测试账号。界面展示等待原因和容量占用。

## 三、易用性、操作性和逻辑性问题

### U01：开始重测就可能让“异常账号”统计下降

账号卡片和概览均取最新任务，包含 queued/running。对原异常记录启动重测后，最新状态变为 queued，旧异常就可能不再计入概览，但这时尚无成功结果。风险统计又按账号/测试类型合并不同模型、题目及保护策略的历史，缺少可比较条件。

建议分别呈现“当前任务状态”和“最近完成的评估结果”，能力趋势按模型、题目版本、评估器版本及策略分组。位置：[概览](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/repository/intelligent_test_repo.go:238)、[历史异常聚合](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/repository/intelligent_test_repo.go:286)。

### U02：设置/筛选元数据加载失败后，刷新可能无法恢复

测试类型与分组只在组件挂载时加载。测试页的“刷新”主要更新账号列表；静默刷新成功还会清除共用错误。若首次读取测试设置失败，错误提示可能消失，但运行按钮仍缺失或不可用。其他窗口更改测试开关后，本页也可能长期保留旧按钮状态。

建议给元数据单独的错误与重试状态，手动刷新同步更新设置和列表。位置：[初始化元数据](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/views/admin/IntelligentTestsView.vue:148)、[共用错误清除](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/views/admin/IntelligentTestsView.vue:109)。

### U03：判定原因不够可见，复制的信息也不完整

卡片展示答复预览和“疑似降智”，但不直接展示评估原因、抽取答案与标准答案。详情中的原因又藏在后续区域；公开结果不带这些评估字段。复制测试信息还省略了记录 ID、评估原因、配置快照和保护策略，影响定位你反馈的这类问题。

另外，任何空分数都显示“待评估”，包括已经终止且永远不会继续评分的认证/网络失败。建议改为“未评估：原因”，并在卡片就显示判定依据；提供脱敏诊断导出。位置：[详情及复制](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/admin/intelligent-tests/TestDetailDialog.vue:10)、[卡片](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/admin/intelligent-tests/TestResultCard.vue:23)。

### U04：设置页没有完整展示实际评分协议，长度限制也不一致

“标准答案校验”说明未完整提示唯一 `ANSWER` 行、末行要求及严格字符串比较，也没有“粘贴答复试判”入口。修改题目但沿用旧标准答案会导致新答复继续失败，用户只能花上游用量后排查。

前端题目允许 16000 个字符，后端限制 16000 字节；例如 6000 个常见汉字可在前端输入，却超过后端字节限制。标准答案前端 maxlength=500，后端上限为 200 字节。

建议统一限制单位、实时计数，显示具体协议及例子，并提供不调用模型的本地规则试判。位置：[设置表单](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/admin/intelligent-tests/TestSettingsPanel.vue:18)、[后端限制](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_service.go:91)。

### U05：任务操作缺少取消、清晰复用反馈和明确的模型适用范围

当前测试管理路由没有针对单个排队任务的取消入口。批量误选题目/模型后，操作人员只能等待，或通过关闭整个测试类型影响更多任务。

快速测试又直接显示模型目录，没有按题目输出类型过滤。两个现有评估器都消费文本/SVG；选择原生图像等模型可能实际产生上游用量，但其 image 事件不进入文本评分器，导致无效测试。

建议至少支持取消尚未发送的排队任务，显示新建/复用/冲突的任务明细，并根据测试所需输出类型过滤模型；批量执行前展示实际模型、题目版本和数量。位置：[管理路由](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/server/routes/relay_management.go:18)、[快速模型选择](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/admin/intelligent-tests/QuickTestDialog.vue:6)、[文本事件解析](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/intelligent_test_runner.go:284)。

### U06：账号编辑中可操作的字段，保存后可能被静默忽略

保护开启时，指纹、TLS 和并发输入仍可编辑，但后端保留受管字段并重新限制并发。用户能选择并保存某值，却未必得到该值；操作成功提示没有说明哪些修改被保留或拒绝。

建议把受管字段设为只读并显示控制它的策略，或在保存前列出“将应用/不会应用”的变更；涉及代理的字段需按 B01 整体处理。位置：[并发](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/account/EditAccountModal.vue:1771)、[指纹选择](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/account/EditAccountModal.vue:2375)、[受管字段](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/service/account_protection.go:119)。

### U07：保护开关文案容易被理解为模型质量保证

关闭确认提示可能发生“模型能力下降、错误路由”，而具体策略主要控制身份、TLS、并发及部分语义转换。通用并发保护也使用“防降智已开启”的主标签，具体范围仅在提示中解释。

建议按实际能力显示“并发保护/身份策略/语义检查”，将基线测试中的关闭状态呈现为中性选择；只有真实路由或认证异常才提示对应风险。位置：[关闭提示](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/account/ProtectionToggle.vue:12)、[异常风险文案](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/repository/intelligent_test_repo.go:317)。

### U08：用户清理会丢失后端错误原因，预览过期后难以操作

API 客户端拒绝的是带 `message/code` 的普通对象；清理组件只识别 `instanceof Error`，因此预览过期、权限不足、锁等待等明确错误可能都变成泛化重试提示。预览有 expires_at，但界面未显示到期状态或倒计时。

建议统一错误提取，针对预览过期提供“重新预览”，针对繁忙提示稍后重试；避免用户一直重复提交已经失效的预览。位置：[错误对象](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/api/client.ts:247)、[清理错误处理](C:/Users/30214/Desktop/sub2本地/sub2新站/frontend/src/components/admin/user/UserCleanupControl.vue:78)。

### U09：列表读取过重，测试历史缺少保留策略

账号卡片和历史列表先从数据库读取包含 raw_response、完整配置、输入及图片的全部记录，再在 Go 中清空部分字段。页面每 5 秒刷新，原始响应较大时会产生不必要的数据库传输和内存压力。当前代码也未发现 account_tests 与幂等请求表的自动保留期清理；2000 的限制仅限制进行中的队列。

建议列表查询只选摘要字段，图片和详情按需加载；原始响应与结果历史分别配置保留期和大小预算。此项是源码可确认的扩展性风险，本次没有进行生产负载测量。位置：[完整列定义](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/repository/intelligent_test_repo.go:22)、[事后裁剪](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/internal/repository/intelligent_test_repo.go:57)、[建表及索引](C:/Users/30214/Desktop/sub2本地/sub2新站/backend/migrations/247_intelligent_tests.sql:14)。

### U10：交付版本和运维文档仍需纳入可追溯流程

当前 Git 跟踪文件数为 0；运行手册、策略矩阵等文档又被 `.gitignore` 的 `docs/*` 规则排除。直接使用 `git add .` 建立版本时，这些手册不会自动进入提交，影响后续检出、部署和回滚核对。

可选保留源码包及校验清单，或建立经过筛选的 Git 基线并修正文档忽略规则。本轮没有执行提交或修改忽略规则。Live 暂停开放、在线原地更新/回滚关闭仍是明确的功能限制，不当成已恢复的能力。

## 四、可选方案：如何处理“正确却被判疑似降智”

代理关联字段、慢刷新取消和幂等键等确定性 bug，应进入独立修复清单。判分标准可以选择以下方案。

| 方案 | 具体做法 | 优点 | 代价及限制 |
| --- | --- | --- | --- |
| A：最小调整 | 保留严格评分器，将展示改为“规则未通过/需要复核”；立即显示格式原因、标准答案和抽取值；不把 0/100 当能力分 | 改动较小、兼容现有记录与接口 | 仍会拒绝 `12颗`、`12.0` 等等价输出；旧状态码仍可能被外部客户端误读 |
| **B：可解释的多维评估（推荐）** | 分开记录执行状态、答案正确性、格式合规和能力信号；按答案类型做受控归一化；歧义答案标“无法自动判定”；设置页提供试判 | 直接解决当前体验问题，结果可解释，算术等题不必增加上游调用 | 需要明确数值、单位、文本等比较规则，以及接口/历史版本兼容 |
| C：完整能力评测 | 在 B 基础上加入多题、多轮、同模型同配置基线及趋势；复杂图像/语义结果引入人工复核或可选裁判 | 更适合评价账号长期变化 | 开发与运行成本更高；裁判本身也可能误判，不能用少量题直接证明上游降智 |

B 的预期展示示例：**执行完成；数值答案正确；格式不符合；能力下降证据不足。**

不建议只搜索输出中有没有 `12`，这会把题目复述、被否定的答案和错误推导一起放行。数值等价规则也不能直接套在所有文本或单位题上。

## 五、其他方面的可选方案

| 方面 | 较轻方案 | 更完整方案 |
| --- | --- | --- |
| SVG 测试 | 仅显示“结构与安全检查通过”，内容标未评估，移除能力满分暗示 | 增加图像内容人工复核/视觉评价，与结构检查分开保存 |
| 历史误判 | 原结果保留，明确显示旧规则版本，新规则只处理新测试 | 使用已保存答复和配置快照重新评估，保留原判定、新判定及原因；完整输出缺失时标待人工复核，不再次消耗上游用量 |
| 测试操作 | 修复轮询、完整参数幂等键、复用提示，支持取消未执行任务 | 统一任务中心，共享业务准入、优先级和容量预算，展示每批执行计划 |
| 账号保护编辑 | 锁定受管字段；冲突的代理切换整次拒绝并保留原配置 | 提供策略/代理联动预览与单次确认，明确每个变化的生效范围 |
| 结果公开 | 只公开经过明确选择的结果与中性结论 | 增加每次发布/撤回和复核记录，避免题目变化后整类历史自动暴露 |

我的建议是采用 **B + 历史保留式重评**，SVG 先采用结构检查的中性表述；同时优先处理 B01、B02、B03、B04、B05，避免测试实际行为与用户选择继续不一致。上述均为可选方案，本轮未应用。

## 六、本轮证据与范围

- 深入检查了题目配置→任务提交/复用→模型选择→出站请求→流解析→评估→持久化→列表、详情及公开展示链路，以及账号编辑、清理操作、错误处理、刷新和版本交付。
- 临时 Go 用例直接调用当前评分器、流解析器、错误分类器、模型选择函数和实际 AdminService；均只使用合成输入与内存仓库，无真实上游请求、无真实账号写入。
- 前端诊断提取当前 load 函数和签名表达式运行，复现慢请求被取消及模型变化签名不变。
- 本轮没有重跑前后端全量测试，也没有声称逐行穷尽整个大型仓库。重点是发现已有测试没有表达的业务和交互问题。
- 未获取具体服务器误判记录，未做浏览器像素级验收或生产负载实验。它们不会被写成已证实的线上事故。
- 项目内本轮只新增本审查报告；临时诊断位于 `C:/Users/30214/AppData/Local/Temp/sub2-assessment-review-20260914`。
