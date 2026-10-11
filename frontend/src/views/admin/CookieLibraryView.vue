<template>
  <AppLayout>
    <div class="space-y-5">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div><h1 class="text-2xl font-semibold">Cookie 库</h1><p class="mt-1 text-sm text-gray-500">独立获取 Cookie，按 Host 去重保存。与打票的开关、代理及刷新周期分开管理。</p></div>
        <button class="btn btn-secondary" :disabled="loading" @click="refresh">刷新</button>
      </div>
      <p v-if="error" role="alert" class="rounded bg-red-50 p-3 text-sm text-red-700">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm text-emerald-600">{{ notice }}</p>
      <div class="flex flex-wrap gap-2 border-b pb-3">
        <button v-for="item in tabs" :key="item.key" class="btn" :class="tab === item.key ? 'btn-primary' : 'btn-secondary'" @click="tab = item.key">{{ item.label }}</button>
      </div>
      <section v-if="tab === 'pool-cockpit'" class="space-y-3 rounded-xl border border-gray-200 bg-gray-50/70 p-4 dark:border-dark-700 dark:bg-dark-800/40" aria-label="Cookie 运行驾驶舱">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="flex items-center gap-2"><span class="h-2 w-2 rounded-full" :class="dashboardError ? 'bg-amber-500' : dashboard ? 'bg-emerald-500' : 'bg-gray-400'"></span><h2 class="font-semibold">Cookie 运行驾驶舱</h2><span class="text-xs text-gray-500">每 5 秒更新</span></div>
          <span class="text-xs text-gray-500">{{ dashboard ? '更新于 ' + formatTime(dashboard.updated_at) : '正在加载运行数据…' }}</span>
        </div>
        <p v-if="dashboardError" role="status" class="text-xs text-amber-600">{{ dashboardError }}{{ dashboard ? '，保留上次数据。' : '，等待重试。' }}</p>
        <div class="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
          <button type="button" class="card min-w-0 border-t-2 border-sky-500 px-4 py-4 text-left hover:bg-sky-50 dark:hover:bg-dark-700" @click="tab = 'logs'">
            <div class="text-sm text-gray-500">正在打 Cookie 的账号</div><div class="my-2 text-3xl font-semibold tabular-nums text-sky-600">{{ dashboard?.harvest.total ?? '—' }}<span v-if="dashboard" class="ml-2 text-sm font-normal text-gray-400">/ {{ dashboard.harvest_concurrency }} 并发</span></div>
            <p class="text-xs text-gray-500">{{ dashboard ? dashboard.harvest_enabled ? '采集已启用' : '采集已关闭' : '等待数据' }} · 查看获取日志 →</p>
          </button>
          <div class="card min-w-0 border-t-2 border-indigo-500 px-4 py-4"><div class="text-sm text-gray-500">Cookie Host 轮换账号</div><div class="my-2 text-3xl font-semibold tabular-nums text-indigo-600">{{ dashboard?.rotation_accounts ?? '—' }}</div><p class="text-xs text-gray-500">配置范围内去重账号 · {{ dashboard ? dashboard.rotation_enabled ? '自动轮换已启用' : '自动轮换已关闭' : '等待数据' }}</p></div>
          <div class="card min-w-0 border-t-2 border-emerald-500 px-4 py-4"><div class="text-sm text-gray-500">绑定正常账号</div><div class="my-2 text-3xl font-semibold tabular-nums text-emerald-600">{{ dashboard?.healthy_bound_accounts ?? '—' }}</div><p class="text-xs text-gray-500">账号可调度，绑定与 Cookie 有效，无冷却或监控隔离</p></div>
          <button type="button" class="card min-w-0 border-t-2 border-amber-500 px-4 py-4 text-left hover:bg-amber-50 dark:hover:bg-dark-700" @click="tab = 'rotation-logs'"><div class="text-sm text-gray-500">正在轮换 Cookie Host</div><div class="my-2 text-3xl font-semibold tabular-nums text-amber-600">{{ dashboard?.rotation_running ?? '—' }}</div><p class="text-xs text-gray-500">实际执行中，不含等待重试 · 查看日志 →</p></button>
          <div class="card min-w-0 border-t-2 border-orange-500 px-4 py-4"><div class="text-sm text-gray-500">Cookie Host 降级账号</div><div class="my-2 text-3xl font-semibold tabular-nums text-orange-600">{{ dashboard?.degraded_accounts ?? '—' }}</div><p class="text-xs text-gray-500">符合 Host 耗尽与降级条件，流量单独统计</p></div>
        </div>
        <div v-if="dashboard" class="flex flex-wrap gap-x-5 gap-y-1 text-xs text-gray-500"><span>当前采集任务</span><span>探索 {{ dashboard.harvest.explore || 0 }}</span><span>补齐 {{ dashboard.harvest.fill || 0 }}</span><span>刷新 {{ dashboard.harvest.refresh || 0 }}</span><span>轮询 {{ dashboard.harvest.round_robin || 0 }}</span></div>
      </section>


      <CookiePoolCockpit v-if="tab === 'pool-cockpit'" :dashboard="dashboard" @settings="tab = 'settings'" @detail-open="dashboardDetailOpen = $event" />
      <CookieHostMonitor v-if="tab === 'host-monitor'" :hosts="library.map(item => item.host)" />
      <section v-if="tab === 'library'" class="space-y-3">
        <input v-model="search" class="input w-full" placeholder="搜索 Host" aria-label="搜索 Host" />
        <div class="grid gap-3 sm:grid-cols-3">
          <div class="card px-4 py-3"><div class="text-xs text-gray-500">历史打到 Host</div><div class="mt-1 text-xl font-semibold">{{ allHostRows.length }}</div></div>
          <div class="card px-4 py-3"><div class="text-xs text-gray-500">当前有效 Cookie Host</div><div class="mt-1 text-xl font-semibold text-emerald-600">{{ validLibraryCount }}</div></div>
          <div class="card px-4 py-3"><div class="text-xs text-gray-500">当前缺少 Host</div><div class="mt-1 text-xl font-semibold" :class="missingHostCount > 0 ? 'text-amber-600' : 'text-emerald-600'">{{ missingHostCount }}</div></div>
        </div>
        <p class="text-sm text-gray-500">当前有效 Cookie Host 优先展示；其余为代理历史上打到过、但当前 Cookie 库中缺失或已过期的 Host。每 10 秒自动刷新。</p>
        <p v-if="!filteredHostRows.length" class="card p-6 text-gray-500">{{ loading ? '加载中…' : '暂无历史 Host，请在获取配置中启用采集。' }}</p>
        <section v-if="false" class="card space-y-3 p-5">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div><h2 class="font-semibold">代理 Host 记忆</h2><p class="text-xs text-gray-500">每个代理最多记忆前 100 次成功命中的 Host；达到上限后保持稳定，清空后重新学习。</p></div>
            <button class="btn btn-secondary btn-sm" :disabled="proxyMemoriesLoading" @click="loadProxyMemories">刷新</button>
          </div>
          <div v-if="!proxyMemories.length" class="text-sm text-gray-500">暂无代理 Host 记忆</div>
          <div v-for="memory in proxyMemories" :key="memory.proxy" class="rounded border border-gray-200 p-4 dark:border-dark-700">
            <div class="flex flex-wrap items-center justify-between gap-2"><span class="break-all font-mono text-sm">{{ memory.proxy }}</span><div class="flex items-center gap-3 text-xs"><span :class="memory.completed ? 'text-emerald-600' : 'text-gray-500'">{{ memory.requests }}/{{ memory.limit }} 次{{ memory.completed ? '（已完成）' : '' }}</span><button class="btn btn-secondary btn-sm" :disabled="proxyMemoryResetting === memory.proxy" @click="resetProxyMemory(memory)">清空重新学习</button></div></div>
            <div class="mt-3 flex flex-wrap gap-2"><span v-for="host in displayMemoryHosts(memory)" :key="host.host" class="rounded bg-gray-100 px-2 py-1 font-mono text-xs dark:bg-dark-800">{{ host.host }} ×{{ host.count }}</span><span v-if="!displayMemoryHosts(memory).length" class="text-xs text-gray-500">尚未记忆 Host</span></div>
          </div>
        </section>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <label class="flex items-center gap-2 text-sm"><input type="checkbox" :checked="refreshableHostRows.length > 0 && refreshableHostRows.every(row => selectedHosts.has(row.host))" @change="toggleAllHosts" />选择当前 Host</label>
          <button type="button" class="btn btn-primary btn-sm" :disabled="!selectedHosts.size || manualRefreshLoading" @click="() => refreshSelectedHosts()">{{ manualRefreshLoading ? '刷新中…' : `刷新 Cookie（${selectedHosts.size}）` }}</button>
        </div>
        <div class="card divide-y dark:divide-dark-700">
          <div v-for="row in filteredHostRows" :key="row.host" class="flex min-h-14 flex-wrap items-center gap-3 px-4 py-3 text-sm">
            <input v-if="row.entry" v-model="selectedHostsArray" type="checkbox" :value="row.host" class="h-4 w-4" aria-label="选择 Host" />
            <span class="min-w-0 flex-1 truncate font-mono" :title="row.host">{{ row.host }}</span>
            <span v-if="row.entry" class="shrink-0 text-emerald-600">已获取 · 剩余 {{ remaining(row.entry.expires_at) }}s</span>
            <span v-else class="shrink-0 text-amber-600">当前缺少</span>
            <span class="shrink-0 text-xs text-gray-500">命中 {{ row.hits }} 次</span>
            <button v-if="row.entry" type="button" class="btn btn-secondary btn-sm" @click="showDetail('Cookie · ' + row.host, row.entry.cookie)">查看 Cookie</button>
            <button v-if="row.entry" type="button" class="btn btn-secondary btn-sm" @click="copyCookie(row.entry.cookie)">复制</button>
            <button v-if="row.entry" type="button" class="btn btn-primary btn-sm" :disabled="manualRefreshLoading" @click="() => refreshSelectedHosts([row.host])">刷新 Cookie</button>
          </div>
        </div>
      </section>

      <section v-if="tab === 'proxy-memory'" class="space-y-3">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div><h2 class="text-lg font-semibold">代理 Host 记忆</h2><p class="text-sm text-gray-500">展示代理历史上命中的全部 Host；按配置的成功样本目标与尝试上限完成初始学习，之后持续探索。新增统计从本次升级开始累计。</p></div>
          <div class="flex gap-2"><button class="btn btn-secondary btn-sm" :disabled="proxyMemoriesLoading" @click="loadProxyMemories">刷新</button><button class="btn btn-danger btn-sm" :disabled="proxyMemoriesLoading || !proxyMemories.length" @click="resetAllProxyMemories">清空全部重学</button></div>
        </div>
        <div class="grid gap-3 sm:grid-cols-4">
          <div class="card px-4 py-3"><div class="text-xs text-gray-500">已记忆代理</div><div class="mt-1 text-xl font-semibold">{{ proxyMemorySummary.proxies }}</div></div>
          <div class="card px-4 py-3"><div class="text-xs text-gray-500">已发现 Host</div><div class="mt-1 text-xl font-semibold text-emerald-600">{{ proxyMemorySummary.hosts }}</div></div>
          <div class="card px-4 py-3"><div class="text-xs text-gray-500">累计命中次数</div><div class="mt-1 text-xl font-semibold">{{ proxyMemorySummary.hits }}</div></div>
          <div class="card px-4 py-3"><div class="text-xs text-gray-500">已完成路由学习</div><div class="mt-1 text-xl font-semibold">{{ proxyMemorySummary.completed }}</div></div>
        </div>
        <div v-if="!proxyMemories.length" class="card p-5 text-sm text-gray-500">暂无代理 Host 记忆</div>
        <article v-for="memory in proxyMemories" :key="memory.proxy + ':' + (memory.proxy_username || '')" class="card space-y-3 p-5">
          <div class="flex flex-wrap items-start justify-between gap-3"><div class="min-w-0"><div class="flex flex-wrap items-center gap-2"><div class="break-all font-mono text-sm">{{ memory.proxy }}</div><span v-if="managedProxyForMemory(memory)" class="rounded bg-emerald-50 px-2 py-0.5 text-xs text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300">IP 管理 · {{ managedProxyLocation(managedProxyForMemory(memory)) }}</span></div><div v-if="memory.proxy_username" class="mt-1 text-xs text-gray-500">用户名：{{ memory.proxy_username }}</div></div><div class="flex items-center gap-3 text-xs"><span :class="memory.completed ? 'text-emerald-600' : 'text-gray-500'">路由学习 {{ memory.requests }}/{{ memory.limit }}{{ memory.completed ? '（已完成）' : '' }}</span><button class="btn btn-secondary btn-sm" :disabled="proxyMemoryResetting === memory.proxy" @click="resetProxyMemory(memory)">清空重学</button></div></div>
          <div class="flex flex-wrap gap-x-4 gap-y-1 text-xs text-gray-500"><span>学习成功样本 {{ memory.successful_samples || 0 }} / {{ memory.sample_target || 20 }}</span><template v-if="memory.stats"><span>采集成功 {{ memory.stats.successes }} / {{ memory.stats.attempts }}</span><span>失败 {{ memory.stats.failures }} · 账号 401 {{ memory.stats.unauthorized }}</span><span>发现全局新 Host {{ memory.stats.new_hosts }}</span><span>补齐/刷新目标命中 {{ memory.stats.target_hits }}</span><span>探索 {{ memory.stats.tasks?.explore || 0 }} · 补齐 {{ memory.stats.tasks?.fill || 0 }} · 刷新 {{ memory.stats.tasks?.refresh || 0 }}</span></template><span v-if="memory.backoff_until && remaining(memory.backoff_until) > 0" class="text-amber-600">代理退避剩余 {{ remaining(memory.backoff_until) }}s</span></div>
          <div class="flex flex-wrap gap-2"><span v-for="host in displayMemoryHosts(memory)" :key="host.host" class="rounded bg-gray-100 px-2 py-1 font-mono text-xs dark:bg-dark-800">{{ host.host }} ×{{ host.count }}</span><span v-if="!displayMemoryHosts(memory).length" class="text-xs text-gray-500">尚未记录 Host</span></div>
        </article>
      </section>

      <form v-if="tab === 'settings'" class="card space-y-5 p-6" @submit.prevent="save">
        <p v-if="!settingsLoaded" class="text-sm text-gray-500">配置尚未加载，点击刷新重试。</p>
        <fieldset :disabled="!settingsLoaded || saving" class="space-y-5">
          <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
            <h3 class="font-semibold">运行开关</h3>
            <label class="flex items-center gap-2 text-sm"><input v-model="form.enabled" type="checkbox" />启用 Cookie 后台任务（本地采集 / Host 轮换）</label>
            <p class="text-xs text-gray-500">仅使用远程 Cookie 并自动轮换时，保持后台任务开启，关闭下面的本地账号采集。远程同步与上游响应同步独立运行。</p>
          </section>
          <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
            <h3 class="font-semibold">来源一：本地账号采集</h3>
            <label class="flex items-center gap-2 text-sm"><input v-model="form.local_harvest_disabled" type="checkbox" :true-value="false" :false-value="true" />启用本地账号打 Cookie</label>
            <p class="text-xs text-gray-500">仅使用下方明确选择的获取账号或分组；两项都未配置时不采集，不会自动抽取其他账号。关闭后停止派发新采集任务，已发出的任务完成后退出，不影响 Host 轮换和远程同步。</p>
            <p v-if="!form.account_ids.length && !form.group_ids.length" class="text-sm text-amber-600">尚未配置本地采集账号或分组，本地采集不会运行。</p>
          </section>
          <section class="space-y-3 rounded-lg border border-gray-200 p-4 dark:border-dark-700">
            <h3 class="font-semibold">来源二：远程 Cookie 同步</h3>
            <label class="flex items-center gap-2"><input v-model="form.remote_sync_enabled" type="checkbox" />启用远程同步</label>
            <p class="text-xs text-gray-500">按 Host 合并远程 Cookie，优先比较签发时间，其次到期时间，旧格式回退采集时间；相同时间保留本地。同步独立于本地采集开关运行。</p>
            <div v-if="form.remote_sync_enabled" class="grid gap-4 md:grid-cols-2">
            <label class="block text-sm">远程服务器地址<input v-model="form.remote_sync_url" class="input mt-1 w-full" placeholder="https://sub2api.example.com" /></label>
            <label class="block text-sm">远程 Admin Key<input v-model="form.remote_sync_admin_key" type="password" autocomplete="new-password" class="input mt-1 w-full" placeholder="输入管理员 API Key；留空保留已保存的 Key" /></label>
            <label class="block text-sm">同步频率（秒）<input v-model.number="form.remote_sync_interval_seconds" type="number" min="10" max="86400" class="input mt-1 w-full" placeholder="默认 60 秒" /></label>
            </div>
            <p class="text-xs text-gray-500">保存后生效，在“获取日志”查看同步结果；关闭时保留连接配置，失败时保留已有 Cookie。</p>
          </section>
          <section class="space-y-4 rounded-lg border border-sky-200 bg-sky-50/40 p-4 dark:border-sky-900 dark:bg-sky-950/20" aria-labelledby="response-cookie-source-title">
            <div class="flex flex-wrap items-center justify-between gap-2">
              <div><h3 id="response-cookie-source-title" class="font-semibold">来源三：上游响应 Cookie 同步</h3><p class="mt-1 text-xs text-gray-500">从正常 HTTP 请求的上游响应中获取 Cookie，流式与非流式请求均适用。</p></div>
              <span class="rounded-full px-2.5 py-1 text-xs" :class="form.response_cookie_sync_enabled ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-800'">{{ form.response_cookie_sync_enabled ? '已启用' : '未启用' }}</span>
            </div>
            <label class="flex items-center gap-2 text-sm"><input v-model="form.response_cookie_sync_enabled" type="checkbox" />启用上游响应 Cookie 自动入库</label>
            <div class="grid gap-3 text-xs sm:grid-cols-3">
              <div class="rounded-lg bg-white/70 p-3 dark:bg-dark-800/60"><div class="font-medium">按 Host 去重</div><p class="mt-1 text-gray-500">每个 Host 只保留一条 Cookie。仅接受成功响应中的有效 Cookie，并遵循 Host 白名单。</p></div>
              <div class="rounded-lg bg-white/70 p-3 dark:bg-dark-800/60"><div class="font-medium">最新时间优先</div><p class="mt-1 text-gray-500">优先比较签发时间，其次到期时间，旧格式回退采集时间。相同或更旧的 Cookie 不覆盖。</p></div>
              <div class="rounded-lg bg-white/70 p-3 dark:bg-dark-800/60"><div class="font-medium">异步更新</div><p class="mt-1 text-gray-500">请求无需等待入库。更新、跳过及失败结果写入获取日志；队列繁忙时跳过同步并记录。</p></div>
            </div>
            <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-gray-500"><span>保存后生效，独立于本地采集与远程同步开关。下游 Cookie 回传规则保持原样。</span><button type="button" class="text-sky-700 hover:underline dark:text-sky-300" @click="tab = 'logs'">查看获取日志 →</button></div>
          </section>
          <section class="space-y-5 rounded border border-gray-200 p-4 dark:border-dark-700">
            <div>
              <h3 class="font-semibold">运行与模型</h3>
              <p class="mt-1 text-xs text-gray-500">Cookie 获取模型只用于采集 Host Cookie；智力测试模型单独保存，不会影响 Cookie 获取。</p>
            </div>
            <div class="grid gap-4 md:grid-cols-2">
              <label class="flex items-center gap-2 rounded bg-gray-50 px-3 py-2 text-sm dark:bg-dark-800"><input v-model="form.auto_validate_host" type="checkbox" />自动验证 Host 是否降智</label>
              <label class="space-y-2"><span class="block text-sm">Cookie 获取模型</span><input v-model="form.model" required maxlength="128" class="input w-full" placeholder="gpt-6-astra" /><span class="block text-xs text-gray-500">仅用于获取 Cookie 的请求。</span></label>
              <label class="space-y-2"><span class="block text-sm">同账号采集间隔（秒）</span><input v-model.number="form.interval_seconds" required type="number" min="1" max="3600" class="input w-full" /><span class="block text-xs text-gray-500">账号完成一次采集后的最短休息时间；其他空闲账号可继续采集，不再等待整批结束。</span></label>
              <label class="space-y-2"><span class="block text-sm">智力测试模型（独立配置）</span><input v-model="intelligenceModel" required maxlength="128" class="input w-full" placeholder="gpt-6-astra" /><span class="block text-xs text-gray-500">只用于账号列表中的智力测试和智力监控，不会改变 Cookie 获取模型。</span></label>
              <div class="flex items-end text-xs text-gray-500">智力测试题目和监控周期请在“智力监控”页面配置。</div>
            </div>
            <p class="text-xs text-gray-500">启用自动验证后会发送 yes/no 请求；返回 yes 才绑定候选 Host，返回 no 则进入冷静期后继续尝试其他 Host。</p>
          </section>
          <section class="space-y-4 rounded border border-gray-200 p-4 dark:border-dark-700">
            <div>
              <h3 class="font-semibold">Cookie 获取范围</h3>
              <p class="mt-1 text-xs text-gray-500">获取账号负责采集 Cookie；参与业务请求的账号请在下方“Host 轮换策略”配置。</p>
            </div>
            <div class="grid gap-4 md:grid-cols-2">
              <label class="space-y-2"><span class="block text-sm">Cookie 获取分组（可多选）</span><button type="button" class="input flex min-h-11 w-full items-center justify-between text-left" @click="openPicker('groups')"><span v-if="pickerDisplayNames('groups').length" class="flex min-w-0 flex-wrap gap-1"><span v-for="name in pickerDisplayNames('groups')" :key="name" class="max-w-full truncate rounded bg-primary-50 px-2 py-0.5 text-xs text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">{{ name }}</span></span><span v-else class="truncate text-gray-500">未选择分组</span><span class="ml-3 shrink-0 text-gray-400">⌄</span></button><span class="block text-xs text-gray-500">使用所选分组账号采集 Cookie，也包含当前临时降级到所选分组的账号；恢复后自动退出降级组的采集范围。仅已轮到且 Host 全部耗尽的账号才临时降级。分组和账号均未选择时，不执行本地采集。</span></label>
              <label class="space-y-2"><span class="block text-sm">Cookie 获取账号（可多选）</span><button type="button" class="input flex min-h-11 w-full items-center justify-between text-left" @click="openPicker('accounts')"><span v-if="pickerDisplayNames('accounts').length" class="flex min-w-0 flex-wrap gap-1"><span v-for="name in pickerDisplayNames('accounts')" :key="name" class="max-w-full truncate rounded bg-primary-50 px-2 py-0.5 text-xs text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">{{ name }}</span></span><span v-else class="truncate text-gray-500">未指定账号（仅使用所选分组）</span><span class="ml-3 shrink-0 text-gray-400">⌄</span></button><span class="block text-xs text-gray-500">指定账号只负责采集 Cookie，不会因此获得轮换权限。</span></label>

            </div>
          </section>

          <section class="space-y-4 rounded border border-gray-200 p-4 dark:border-dark-700">
            <div><h3 class="font-semibold">Cookie 代理池</h3><p class="mt-1 text-xs text-gray-500">可直接选用 IP 管理中的代理，也可继续使用手工连接 URL；两种来源会合并并去重。</p></div>
            <label class="flex items-center gap-2"><input v-model="form.use_all_managed_proxies" type="checkbox" />使用 IP 管理中的全部启用代理（当前 {{ managedProxies.length }} 个）</label>
            <label class="block space-y-2"><span class="text-sm">指定 IP 管理代理（可多选）</span><button type="button" class="input flex min-h-11 w-full items-center justify-between text-left disabled:cursor-not-allowed disabled:opacity-60" :disabled="form.use_all_managed_proxies" @click="openPicker('managed-proxies')"><span class="truncate">{{ pickerSummary('managed-proxies') }}</span><span class="ml-3 shrink-0 text-gray-400">⌄</span></button><span class="block text-xs text-gray-500">开启“全部”时会自动使用当前及后续新增的全部启用代理；关闭后仅使用这里选中的启用且未过期代理。</span></label>
            <label class="block space-y-2"><span class="text-sm">手工代理池（每行一个）</span><textarea v-model="proxyText" rows="4" class="input w-full font-mono text-sm" placeholder="http://user:password@host:port" /><span class="block text-xs text-gray-500">保留原有填写方式。未启用 IP 管理且这里留空时，使用采集账号自身代理；没有可用代理时跳过并记录失败，不直连。</span></label>
            <div class="grid gap-4 md:grid-cols-2">
            <label class="space-y-2"><span class="block text-sm">Cookie 采集调度方式</span><select v-model="form.cookie_proxy_schedule_mode" class="input w-full"><option value="round_robin">轮询</option><option value="dynamic">动态适配</option></select><span class="block text-xs text-gray-500">轮询：多个账号按代理池顺序依次取代理。动态适配：按下方比例持续探索、补齐缺失及刷新到期 Host；结合代理历史命中率与退避自动选择代理。</span></label>
            <label class="space-y-2"><span class="block text-sm">并发采集账号数</span><input v-model.number="form.cookie_harvest_concurrency" required type="number" min="1" max="64" class="input w-full" /><span class="block text-xs text-gray-500">同时使用多少个采集账号打 Cookie；每个账号会独立选择代理，适合多个账号并行补齐 Host。</span></label>
            <label class="space-y-2"><span class="block text-sm">初始学习尝试上限</span><input v-model.number="form.cookie_proxy_learning_attempts" required type="number" min="1" max="10000" class="input w-full" /><span class="block text-xs text-gray-500">保留原配置，失败也消耗尝试预算；达到成功样本目标或尝试上限即结束初始学习。结束后仍持续探索并记录历史，不会停止发现新 Host。</span></label>
            </div>
            <div v-if="form.harvest_policy" class="space-y-3 rounded bg-gray-50 p-4 dark:bg-dark-800">
              <div><h4 class="font-semibold">动态覆盖策略</h4><p class="mt-1 text-xs text-gray-500">比例仅对动态适配生效，按任务数分配，合计必须为 100%。缺失补齐或到期刷新没有任务时，其名额用于探索。每个账号、代理同时最多执行一个采集任务。</p></div>
              <fieldset :disabled="form.cookie_proxy_schedule_mode !== 'dynamic'" class="grid gap-4 md:grid-cols-3 disabled:opacity-50">
                <label class="space-y-1"><span class="block text-sm">探索新 Host（%）</span><input v-model.number="form.harvest_policy.explore_percent" required type="number" min="1" max="100" class="input w-full" /></label>
                <label class="space-y-1"><span class="block text-sm">缺失 Host 补齐（%）</span><input v-model.number="form.harvest_policy.fill_percent" required type="number" min="0" max="99" class="input w-full" /></label>
                <label class="space-y-1"><span class="block text-sm">即将过期刷新（%）</span><input v-model.number="form.harvest_policy.refresh_percent" required type="number" min="0" max="99" class="input w-full" /><span class="block text-xs text-gray-500">使用获取账号携带目标 Host 当前 Cookie 发起请求，仅将同 Host、时间更新的响应 Cookie 写回库。</span></label>
                <label class="space-y-1"><span class="block text-sm">探索巡检周期（秒）</span><input v-model.number="form.harvest_policy.exploration_revisit_seconds" required type="number" min="30" max="86400" class="input w-full" /><span class="text-xs text-gray-500">超过该时间未被使用的代理优先探索；实际速度受并发、账号和退避限制。</span></label>
                <label class="space-y-1"><span class="block text-sm">同目标连续未命中次数</span><input v-model.number="form.harvest_policy.target_miss_limit" required type="number" min="1" max="100" class="input w-full" /></label>
                <label class="space-y-1"><span class="block text-sm">目标未命中退避（秒）</span><input v-model.number="form.harvest_policy.target_backoff_seconds" required type="number" min="1" max="86400" class="input w-full" /><span class="text-xs text-gray-500">只暂停该代理补这个 Host，换其他代理尝试；获得的其他有效 Host 仍入库。</span></label>
              </fieldset>
              <p class="text-xs" :class="policyTotal === 100 ? 'text-gray-500' : 'text-red-600'">当前比例合计 {{ policyTotal }}%。刷新窗口沿用下方“Cookie 提前刷新”配置；目标 Host 只是调度意图，最终节点由上游分配。</p>
              <div class="grid gap-4 md:grid-cols-3">
                <label class="space-y-1"><span class="block text-sm">初始学习成功样本目标</span><input v-model.number="form.harvest_policy.learning_samples" required type="number" min="1" max="10000" class="input w-full" /><span class="text-xs text-gray-500">有效 Host 响应才算成功样本；旧学习记录保留，无需清空全部重学。</span></label>
                <label class="space-y-1"><span class="block text-sm">连续采集失败阈值</span><input v-model.number="form.harvest_policy.failure_threshold" required type="number" min="1" max="20" class="input w-full" /><span class="text-xs text-gray-500">401 计入账号异常，不处罚代理。</span></label>
                <label class="space-y-1"><span class="block text-sm">失败代理退避（秒）</span><input v-model.number="form.harvest_policy.failure_backoff_seconds" required type="number" min="1" max="3600" class="input w-full" /><span class="text-xs text-gray-500">轮询和动态模式均生效，到期自动重新尝试。</span></label>
              </div>
            </div>
            <label class="block space-y-2"><span class="text-sm">Host 白名单（每行一个，留空允许所有 Host）</span><textarea v-model="whitelistText" rows="3" class="input w-full font-mono text-sm" placeholder="chat.gateway.unified-84.api.openai.com" /></label>
          </section>
          <section class="space-y-4 rounded border border-gray-200 p-4 dark:border-dark-700">
            <div>
              <h3 class="font-semibold">Host 轮换策略</h3>
              <p class="mt-1 text-xs text-gray-500">配置哪些账号参与轮换、需要准备多少容量，以及 Host 耗尽后的处理方式。候选 Host 验证通过后才接收业务请求。</p>
            </div>
            <label class="flex items-center gap-2"><input v-model="form.cookie_rotation_enabled" :disabled="form.ws_enabled" type="checkbox" @change="handleCookieRotationToggle" />启用 Cookie Host 自动轮换</label>
            <p class="text-xs text-gray-500">启用后会先验证候选 Host，只有返回 yes 才替换当前 Host；Cookie Host 自动轮换与 WS 连接互斥。</p>
            <div class="grid gap-4 md:grid-cols-2">
              <label class="space-y-2"><span class="block text-sm">Host 轮换分组（可多选）</span><button type="button" class="input flex min-h-11 w-full items-center justify-between text-left" @click="openPicker('rotation-groups')"><span v-if="pickerDisplayNames('rotation-groups').length" class="flex min-w-0 flex-wrap gap-1"><span v-for="name in pickerDisplayNames('rotation-groups')" :key="name" class="max-w-full truncate rounded bg-primary-50 px-2 py-0.5 text-xs text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">{{ name }}</span></span><span v-else class="truncate text-gray-500">未选择分组</span><span class="ml-3 shrink-0 text-gray-400">⌄</span></button><span class="block text-xs text-gray-500">只有这些分组中的账号会自动轮换 Host。</span></label>
              <label class="space-y-2"><span class="block text-sm">Host 轮换账号（可多选）</span><button type="button" class="input flex min-h-11 w-full items-center justify-between text-left" @click="openPicker('rotation-accounts')"><span v-if="pickerDisplayNames('rotation-accounts').length" class="flex min-w-0 flex-wrap gap-1"><span v-for="name in pickerDisplayNames('rotation-accounts')" :key="name" class="max-w-full truncate rounded bg-primary-50 px-2 py-0.5 text-xs text-primary-700 dark:bg-primary-900/30 dark:text-primary-300">{{ name }}</span></span><span v-else class="truncate text-gray-500">未单独指定轮换账号</span><span class="ml-3 shrink-0 text-gray-400">⌄</span></button><span class="block text-xs text-gray-500">指定账号可单独加入轮换范围；未选择账号和分组时不会自动轮换任何账号。</span></label>
            </div>
            <CookieRotationPools v-model="rotationPools" :selected-groups="form.rotation_group_ids" :groups="cookieGroups" :accounts="cookieAccounts" :runtime="dashboard?.rotation_pools" />
            <div class="border-t border-gray-200 pt-5 dark:border-dark-700"><h4 class="font-semibold">Host 时序与邮箱冷却</h4><p class="mt-1 text-xs text-gray-500">冷却以邮箱 + Host 持久记录。删除账号后以同一邮箱重新授权，会继承冷却与轮转历史；没有邮箱时只能按账号记录。</p></div>
            <div class="grid gap-4 md:grid-cols-3">
              <label class="space-y-2"><span class="block text-sm">Cookie 刷新提前量（秒）</span><input v-model.number="form.cookie_refresh_before_seconds" required type="number" min="0" max="86400" class="input w-full" /><span class="block text-xs text-gray-500">Cookie 剩余多少秒时开始重新获取。</span></label>
              <label class="space-y-2"><span class="block text-sm">Host 与账号绑定时长（秒）</span><input v-model.number="form.cookie_host_binding_seconds" required type="number" min="10" max="86400" class="input w-full" /><span class="block text-xs text-gray-500">默认 240 秒，表示账号绑定当前 Host 的有效时长。</span></label>
              <label class="space-y-2"><span class="block text-sm">提前轮换 Host（秒）</span><input v-model.number="form.cookie_host_rotation_before_seconds" required type="number" min="0" max="86400" class="input w-full" /><span class="block text-xs text-gray-500">默认 10 秒，绑定剩余 10 秒时开始准备下一个 Host。</span></label>
              <label class="space-y-2"><span class="block text-sm">邮箱 Host 冷静期（秒）</span><input v-model.number="form.ws_host_cooldown_seconds" required type="number" min="0" max="604800" class="input w-full" /><span class="block text-xs text-gray-500">明确返回 no、绑定到期或被轮换下来的 Host 再次尝试前等待时间。同一邮箱重新导入不清零。</span></label>
              <label class="space-y-2"><span class="block text-sm">验证异常冷却（秒）</span><input v-model.number="form.cookie_host_validation_failure_cooldown_seconds" required type="number" min="1" max="604800" class="input w-full" /><span class="block text-xs text-gray-500">网络失败、超时、HTTP 错误或响应不是 yes/no 时的冷却时间，默认 120 秒。</span></label>
            </div>
            <div class="space-y-3 border-t border-gray-200 pt-5 dark:border-dark-700">
              <h4 class="font-semibold">Host 全部耗尽后的降级</h4>
              <label class="block text-sm">降级分组<select v-model.number="form.degraded_group_id" class="input mt-2 w-full"><option :value="0">不降级，保持暂不调度</option><option v-for="group in cookieGroups" :key="group.id" :value="group.id">{{ group.name }}</option></select></label>
              <p class="text-xs leading-6 text-gray-500">降级分组也可选择为 Host 轮换来源。轮换资格只按账号管理中实际保存的分组或单独指定的轮换账号判断；临时降级到此组不会加入此组轮换池，仍按原来的轮换来源恢复。</p>
              <p v-if="(form.degraded_group_id ?? 0) > 0 && form.rotation_group_ids.includes(form.degraded_group_id ?? 0)" class="text-xs leading-6 text-amber-600 dark:text-amber-400">当前降级组同时是轮换来源组：实际归属本组且由本组轮换池管理的账号，轮换期间持续调度；旧 Host 有效时继续使用，未绑定、失效或等待新 Host 验证时在本组走普通降级链路，新 Host 就绪后用于后续请求。没有配置轮换池的本组账号同样适用。停用、限流、并发限制和读取异常保护仍生效；临时降级到此组的其他来源账号保持原逻辑。</p>
              <p class="text-xs leading-6 text-gray-500">只有已经轮到的账号，且当前无有效绑定、库内可用 Host 全部处于该邮箱冷却期，才临时进入降级组。未轮到、未启用、准备验证、仅绑定到期、Cookie 库为空或读取失败均不直接降级；仍有候选时继续尝试。手动禁用和监控隔离始终优先。</p>
              <p class="text-xs leading-6 text-gray-500">降级调用使用普通链路，不携带失效 Host Cookie。非上述重叠场景保持原逻辑：原组启用备用账号补足容量，Host 恢复后退出降级并重新排队等待启用。仅改变账号临时可用分组，不将原组请求转发到降级组，不修改账号实际归属。</p>
            </div>
          </section>
          <section class="space-y-4 rounded border border-gray-200 p-4 dark:border-dark-700">
            <div>
              <h3 class="font-semibold">绑定账号 WS 连接</h3>
              <p class="mt-1 text-xs text-gray-500">WS 只作用于已经绑定并验证通过的 Cookie Host。开启 WS 后不能同时开启 Cookie Host 自动轮换。</p>
            </div>
            <label class="flex items-center gap-2"><input v-model="form.ws_enabled" :disabled="form.cookie_rotation_enabled" type="checkbox" @change="handleWSToggle" />启用绑定 Cookie 账号的 WS 连接</label>
            <p class="text-xs text-gray-500">关闭后，绑定 Cookie 的账号新请求使用 HTTP；Cookie 获取和 Host 验证仍然正常运行。</p>
            <div class="grid gap-4 md:grid-cols-3">
              <label class="space-y-2"><span class="block text-sm">每个账号 WS 连接数</span><input v-model.number="form.ws_connections" required type="number" min="1" max="64" class="input w-full" /></label>
              <label class="space-y-2"><span class="block text-sm">WS 有效期（秒）</span><input v-model.number="form.ws_ttl_seconds" required type="number" min="60" max="3600" class="input w-full" /><span class="block text-xs text-gray-500">最长 3600 秒，连接池到期后需重新建立。</span></label>

            </div>
          </section>
          <button class="btn btn-primary" type="submit">{{ saving ? '保存中…' : '保存配置' }}</button>
          <p class="text-xs text-gray-500">连接数在新绑定时生效。连接池建立后仅复用、不自动补建；断线耗尽需先解绑再绑定，过期后保存绑定可重新建立。服务重启时恢复已保存的有效绑定。</p>
        </fieldset>
      </form>

      <section v-if="tab === 'logs'" class="space-y-3">
        <div class="card flex flex-wrap gap-x-6 gap-y-2 px-4 py-3 text-sm"><span>正在采集 {{ harvestRunning.total || 0 }} / {{ form.cookie_harvest_concurrency }}</span><span>探索 {{ harvestRunning.explore || 0 }}</span><span>补齐 {{ harvestRunning.fill || 0 }}</span><span>刷新 {{ harvestRunning.refresh || 0 }}</span><span>轮询 {{ harvestRunning.round_robin || 0 }}</span></div>
        <div class="overflow-hidden rounded border border-gray-800 bg-[#0b1220] text-gray-200 shadow-sm">
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-700 bg-[#111827] px-4 py-2 text-xs">
            <div class="flex items-center gap-2 font-mono"><span class="h-2.5 w-2.5 rounded-full" :class="!acquisitionEnabled ? 'bg-gray-500' : liveAcquisitionPaused ? 'bg-amber-400' : 'bg-emerald-400'"></span><span>cookie acquisition live</span><span class="text-gray-400">· {{ !acquisitionEnabled ? 'Cookie 获取未启用' : liveAcquisitionPaused ? '已暂停' : '实时运行中' }}</span></div>
            <div class="flex items-center gap-2"><span v-if="liveStatusError" class="text-red-300">{{ liveStatusError }}</span><button type="button" class="rounded border border-gray-600 px-2 py-1 hover:bg-gray-700" @click="toggleLive('acquisition')">{{ liveAcquisitionPaused ? '继续' : '暂停' }}</button><button type="button" class="rounded border border-gray-600 px-2 py-1 hover:bg-gray-700" @click="clearLive('acquisition')">清空窗口</button></div>
          </div>
          <div ref="acquisitionConsole" class="h-[420px] overflow-y-auto px-4 py-3 font-mono text-xs leading-6">
            <div v-for="log in liveAcquisitionLogs" :key="`live-acquisition-${log.id}`" class="break-all" :class="liveAcquisitionTone(log)">[{{ formatClock(log.created_at) }}] [获取] {{ liveAcquisitionText(log) }}</div>
            <div v-if="!liveAcquisitionLogs.length" class="text-gray-500">等待 Cookie 获取任务输出...</div>
          </div>
        </div>
        <p class="text-sm text-gray-500">最近 200 次 Cookie 获取记录。日志仅在进入页面、手动刷新或翻页时加载；单条响应最多保留 32 KiB。</p>
        <div class="flex flex-wrap items-center gap-3"><label class="text-sm text-gray-600 dark:text-gray-300">获取来源</label><select v-model="acquisitionSource" class="input max-w-xs"><option value="all">全部</option><option value="explore">探索</option><option value="fill">缺少补齐</option><option value="refresh">即将过期刷新</option><option value="response_sync">请求同步</option></select><span class="text-xs text-gray-500">实时窗口始终展示全部来源</span></div>
        <div class="card overflow-x-auto">
          <table class="w-full text-left text-sm"><thead><tr class="border-b"><th class="p-3">时间 / 账号</th><th class="p-3">结果</th><th class="p-3">Host / 代理</th><th class="p-3">详情</th></tr></thead>
            <tbody><tr v-for="log in filteredLogs" :key="log.id" class="border-b dark:border-dark-700">
              <td class="p-3 whitespace-nowrap">{{ formatTime(log.created_at) }}<br /><span class="text-xs text-gray-500">{{ isSchedulerLog(log) ? '系统调度' : (log.account_name || '—') }}<template v-if="log.account_id > 0"> #{{ log.account_id }}</template> · {{ log.model }}</span></td>
              <td class="p-3"><span v-if="isSchedulerLog(log)" class="text-amber-600">调度提示</span><span v-else :class="log.stage?.startsWith('ignored_') ? 'text-amber-600' : log.success ? 'text-emerald-600' : 'text-red-600'">{{ acquisitionResultLabel(log) }} · {{ log.status_code || '未收到响应' }}</span><p class="mt-1 text-xs">{{ log.message }}</p></td>
              <td class="max-w-sm break-all p-3 font-mono text-xs"><span class="font-sans text-gray-500">{{ harvestTaskLabel(log.task) }}</span> · {{ log.host || '—' }}<p v-if="log.target_host" class="text-amber-600">目标 {{ log.target_host }}</p><p class="mt-1 text-gray-500">{{ log.proxy || '—' }}<span v-if="log.proxy_username" class="ml-2 text-gray-400">user={{ log.proxy_username }}</span></p></td>
              <td class="p-3"><button class="btn btn-secondary btn-sm whitespace-nowrap" @click="showDetail('Cookie 获取详情', log)">查看响应 / Cookie</button></td>
            </tr><tr v-if="!filteredLogs.length"><td colspan="4" class="p-6 text-gray-500">暂无匹配获取日志</td></tr></tbody>
          </table>
        </div>
      </section>

      <section v-if="tab === 'rotation-logs'" class="space-y-3">
        <div class="overflow-hidden rounded border border-gray-800 bg-[#0b1220] text-gray-200 shadow-sm">
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-700 bg-[#111827] px-4 py-2 text-xs">
            <div class="flex items-center gap-2 font-mono"><span class="h-2.5 w-2.5 rounded-full" :class="!form.cookie_rotation_enabled ? 'bg-gray-500' : liveRotationPaused ? 'bg-amber-400' : 'bg-emerald-400'"></span><span>host cookie rotation live</span><span class="text-gray-400">· {{ !form.cookie_rotation_enabled ? '自动轮转未启用' : liveRotationPaused ? '已暂停' : '实时运行中' }}</span></div>
            <div class="flex items-center gap-2"><span v-if="liveStatusError" class="text-red-300">{{ liveStatusError }}</span><button type="button" class="rounded border border-gray-600 px-2 py-1 hover:bg-gray-700" @click="toggleLive('rotation')">{{ liveRotationPaused ? '继续' : '暂停' }}</button><button type="button" class="rounded border border-gray-600 px-2 py-1 hover:bg-gray-700" @click="clearLive('rotation')">清空窗口</button></div>
          </div>
          <div ref="rotationConsole" class="h-[420px] overflow-y-auto px-4 py-3 font-mono text-xs leading-6">
            <div v-for="log in liveRotationLogs" :key="`live-rotation-${log.id}`" class="break-all" :class="liveRotationTone(log)">[{{ formatClock(log.created_at) }}] [轮换] {{ liveRotationText(log) }}</div>
            <div v-if="!liveRotationLogs.length" class="text-gray-500">等待自动 Host Cookie 轮转任务输出...</div>
          </div>
        </div>
        <div class="card space-y-3 p-4">
          <div class="flex flex-wrap items-center justify-between gap-3"><h2 class="font-semibold">自动 Host Cookie 轮转历史（按邮箱继承）</h2><select v-model="validationAccountId" class="input max-w-xs"><option value="">全部账号</option><option v-for="account in validationAccounts" :key="account.id" :value="String(account.id)">{{ account.name }} (#{{ account.id }})</option></select></div>
          <div class="flex flex-wrap items-center gap-2"><input v-model="validationEmail" type="search" class="input min-w-0 flex-1" placeholder="输入完整邮箱，查询删除账号前后的轮转历史" aria-label="按邮箱查询轮转历史" @keydown.enter.prevent="changeValidationPage(1)" /><button type="button" class="btn btn-secondary btn-sm" @click="changeValidationPage(1)">查询邮箱</button></div>
          <div class="overflow-x-auto"><table class="w-full text-left text-sm"><thead><tr class="border-b"><th class="p-2">时间 / 账号</th><th class="p-2">Host / 代理用户</th><th class="p-2">阶段</th><th class="p-2">详情</th><th class="p-2">响应</th></tr></thead><tbody><tr v-for="log in validationLogs" :key="`validation-${log.id}`" class="border-b dark:border-dark-700"><td class="p-2 whitespace-nowrap">{{ formatTime(log.created_at) }}<br /><span class="text-xs text-gray-500">{{ log.account_email || log.account_name || '—' }} #{{ log.account_id }}</span></td><td class="p-2 break-all font-mono text-xs">{{ log.host || '—' }}<span v-if="log.proxy_username" class="mt-1 block font-sans text-gray-500">user={{ log.proxy_username }}</span></td><td class="p-2">{{ validationStageLabel(log.stage) }}</td><td class="p-2 text-xs">{{ log.message }}</td><td class="p-2"><button class="btn btn-secondary btn-sm" @click="showDetail('自动轮转详情', log)">查看</button></td></tr><tr v-if="!validationLogs.length"><td colspan="5" class="p-4 text-gray-500">暂无自动轮转日志</td></tr></tbody></table></div>
          <Pagination v-if="validationTotal > 0" :page="validationPage" :page-size="validationPageSize" :total="validationTotal" :show-page-size-selector="false" @update:page="changeValidationPage" />
        </div>
      </section>
    </div>
    <BaseDialog :show="picker !== null" :title="pickerTitle" width="normal" @close="picker = null">
      <div class="space-y-3">
        <p class="text-sm text-gray-500">可勾选多个选项，确认后保存选择。</p>
        <div v-if="picker === 'groups' || picker === 'rotation-groups'" class="max-h-80 space-y-1 overflow-y-auto">
          <label v-for="group in cookieGroups" :key="group.id" class="flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 hover:bg-gray-50 dark:hover:bg-dark-700"><input v-model="pickerDraft" type="checkbox" :value="group.id" class="h-4 w-4" /><span>{{ group.name }} (#{{ group.id }})</span></label>
          <p v-if="!cookieGroups.length" class="p-3 text-sm text-gray-500">暂无可用分组</p>
        </div>
        <div v-else-if="picker === 'managed-proxies'" class="max-h-80 space-y-1 overflow-y-auto">
          <label v-for="proxy in managedProxies" :key="proxy.id" class="flex cursor-pointer items-start gap-3 rounded px-3 py-2 hover:bg-gray-50 dark:hover:bg-dark-700"><input v-model="pickerDraft" type="checkbox" :value="proxy.id" class="mt-0.5 h-4 w-4" /><span class="min-w-0"><span class="block truncate">{{ proxy.name || `代理 #${proxy.id}` }} · {{ managedProxyLocation(proxy) }}</span><span class="block truncate font-mono text-xs text-gray-500">{{ managedProxyDisplayURL(proxy) }}</span></span></label>
          <p v-if="!managedProxies.length" class="p-3 text-sm text-gray-500">IP 管理中暂无启用代理</p>
        </div>
        <div v-else class="max-h-80 space-y-1 overflow-y-auto">
          <label v-for="account in cookieAccounts" :key="account.id" class="flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 hover:bg-gray-50 dark:hover:bg-dark-700"><input v-model="pickerDraft" type="checkbox" :value="account.id" class="h-4 w-4" /><span>#{{ account.id }} {{ account.name }} · {{ account.type }}</span></label>
          <p v-if="!cookieAccounts.length" class="p-3 text-sm text-gray-500">暂无可用账号</p>
        </div>
      </div>
      <template #footer><div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" @click="picker = null">取消</button><button type="button" class="btn btn-primary" @click="applyPicker">应用（{{ pickerDraft.length }}）</button></div></template>
    </BaseDialog>
    <BaseDialog :show="manualRefreshDialog" title="Cookie 刷新实时日志" width="extra-wide" @close="manualRefreshDialog = false">
      <div class="space-y-3">
        <p class="text-sm text-gray-500">已异步提交 {{ manualRefreshHosts.join('、') }}，请求从服务器直连上游。日志会自动更新。</p>
        <div class="h-[55vh] overflow-y-auto rounded bg-[#0b1220] p-4 font-mono text-xs leading-6 text-gray-200">
          <div v-for="log in liveAcquisitionLogs" :key="`manual-live-${log.id}`" :class="liveAcquisitionTone(log)">[{{ formatClock(log.created_at) }}] {{ liveAcquisitionText(log) }}</div>
          <div v-if="!liveAcquisitionLogs.length" class="text-gray-500">等待异步任务输出...</div>
        </div>
      </div>
    </BaseDialog>
    <BaseDialog :show="detail !== null" :title="detailTitle" width="extra-wide" @close="detail = null"><pre class="max-h-[65vh] overflow-auto whitespace-pre-wrap break-all text-xs">{{ detail }}</pre></BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import CookieRotationPools from '@/components/admin/CookieRotationPools.vue'
import CookiePoolCockpit from '@/components/admin/CookiePoolCockpit.vue'
import CookieHostMonitor from '@/components/admin/CookieHostMonitor.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import * as cookies from '@/api/admin/cookies'
import intelligenceMonitorAPI, { type IntelligenceMonitorConfig } from '@/api/admin/intelligenceMonitor'
import { accountsAPI } from '@/api/admin/accounts'
import { groupsAPI } from '@/api/admin/groups'
import { proxiesAPI } from '@/api/admin/proxies'
import type { OpenAICodexCookieLibraryEntry } from '@/api/admin/settings'
import type { AccountListItem, AdminGroup, Proxy } from '@/types'

const route = useRoute()
const tabs = [{ key: 'library', label: 'Cookie 库' }, { key: 'proxy-memory', label: '代理 Host 记忆' }, { key: 'settings', label: '获取配置 / WS' }, { key: 'logs', label: '获取日志' }, { key: 'rotation-logs', label: '自动轮转日志' }]
const tab = ref('library')
tabs.push({ key: 'host-monitor', label: 'Host 拉黑监控' }, { key: 'pool-cockpit', label: '活跃池驾驶舱' })
const search = ref(typeof route.query.host === 'string' ? route.query.host : '')
const library = ref<OpenAICodexCookieLibraryEntry[]>([])
const logs = ref<cookies.CookieLog[]>([])
const acquisitionSource = ref('all')
const selectedHostsArray = ref<string[]>([])
const selectedHosts = computed(() => new Set(selectedHostsArray.value))
const manualRefreshLoading = ref(false)
const manualRefreshDialog = ref(false)
const manualRefreshHosts = ref<string[]>([])
const validationOnlyLogs = ref<cookies.CookieLog[]>([])
const loading = ref(false)
const saving = ref(false)
const settingsLoaded = ref(false)
const error = ref('')
const notice = ref('')
const now = ref(Date.now())
const detail = ref<string | null>(null)
const detailTitle = ref('')
const form = reactive<cookies.CookieSettings>({ rotation_pools: [], response_cookie_sync_enabled: false, local_harvest_disabled: false, harvest_policy: cookies.defaultHarvestPolicy(), enabled: false, model: 'gpt-6-astra', interval_seconds: 5, account_ids: [], group_ids: [], rotation_account_ids: [], rotation_group_ids: [], cookie_host_scheduling_guard_enabled: false, proxy_urls: [], managed_proxy_ids: [], use_all_managed_proxies: false, cookie_proxy_schedule_mode: 'round_robin', cookie_harvest_concurrency: 1, cookie_proxy_learning_attempts: 100, dynamic_proxy_fill_host_cookie: false, host_whitelist: [], auto_validate_host: false, ws_enabled: false, ws_connections: 10, ws_ttl_seconds: 3600, ws_host_cooldown_seconds: 14400, cookie_host_validation_failure_cooldown_seconds: 120, cookie_refresh_before_seconds: 600, cookie_rotation_enabled: false, cookie_host_binding_seconds: 240, cookie_host_rotation_before_seconds: 10 })
const rotationPools = computed({ get: () => form.rotation_pools || [], set: value => { form.rotation_pools = value } })
const validationEmail = ref('')

const policyTotal = computed(() => { const p = form.harvest_policy; return p ? p.explore_percent + p.fill_percent + p.refresh_percent : 100 })
const harvestRunning = ref<Record<string, number>>({})
const dashboard = ref<cookies.CookieDashboard | null>(null)
const dashboardError = ref('')
const dashboardDetailOpen = ref(false)
let dashboardPolling = false
let dashboardDisposed = false
let dashboardNextAt = 0
let dashboardFailures = 0
async function loadDashboard() {
  if (dashboardPolling || dashboardDisposed || document.hidden || dashboardDetailOpen.value) return
  dashboardPolling = true
  try {
    const value = await cookies.getDashboard()
    if (dashboardDisposed) return
    dashboard.value = value
    harvestRunning.value = value.harvest
    dashboardError.value = ''
    dashboardFailures = 0
  } catch {
    if (!dashboardDisposed) dashboardError.value = '运行数据更新失败'
    dashboardFailures++
  } finally {
    dashboardPolling = false
    dashboardNextAt = Date.now() + Math.min(30000, 5000 * 2 ** dashboardFailures)
  }
}
const harvestTaskLabel = (task?: string) => ({ explore: '探索新 Host', fill: '补齐缺失', refresh: '到期刷新', round_robin: '轮询采集', response_sync: '上游响应同步', remote_sync: '远程同步' }[task || ''] || '采集')
const intelligenceModel = ref('gpt-6-astra')
const intelligenceMonitorConfig = ref<IntelligenceMonitorConfig | null>(null)
const cookieAccounts = ref<AccountListItem[]>([])
const cookieGroups = ref<AdminGroup[]>([])
const proxyText = ref('')
const whitelistText = ref('')
const validationAccountId = ref('')
const validationPage = ref(1)
const validationPageSize = 10
const validationTotal = ref(0)
type LiveLogKind = 'acquisition' | 'rotation'
const liveAcquisitionLogs = ref<cookies.CookieLog[]>([])
const liveRotationLogs = ref<cookies.CookieLog[]>([])
const liveAcquisitionPaused = ref(false)
const liveRotationPaused = ref(false)
const liveStatusError = ref('')
const acquisitionConsole = ref<HTMLElement | null>(null)
const rotationConsole = ref<HTMLElement | null>(null)
const liveAcquisitionSeen = new Set<string>()
const liveRotationSeen = new Set<string>()
let livePolling = false
type PickerKind = 'groups' | 'accounts' | 'rotation-groups' | 'rotation-accounts' | 'managed-proxies'
const picker = ref<PickerKind | null>(null)
const pickerDraft = ref<number[]>([])
const proxyMemories = ref<cookies.CookieProxyHostMemory[]>([])
const managedProxies = ref<Proxy[]>([])
const proxyMemoriesLoading = ref(false)
const proxyMemoryResetting = ref('')
const proxyEndpointKey = (protocol: string, host: string, port: number, username = '') => `${protocol.toLowerCase()}://${host.toLowerCase()}:${port}|${username}`
const memoryEndpointKey = (memory: cookies.CookieProxyHostMemory) => {
  try {
    const parsed = new URL(memory.proxy)
    const port = Number(parsed.port || (parsed.protocol === 'https:' ? 443 : 80))
    return proxyEndpointKey(parsed.protocol.replace(':', ''), parsed.hostname.replace(/^\[|\]$/g, ''), port, memory.proxy_username || parsed.username || '')
  } catch { return '' }
}
const managedProxyMap = computed(() => new Map(managedProxies.value.map(proxy => [proxyEndpointKey(proxy.protocol, proxy.host.replace(/^\[|\]$/g, ''), proxy.port, proxy.username || ''), proxy])))
const managedProxyForMemory = (memory: cookies.CookieProxyHostMemory) => managedProxyMap.value.get(memoryEndpointKey(memory))
const managedProxyLocation = (proxy?: Proxy) => {
  if (!proxy) return '位置未知'
  const parts = [proxy.country, proxy.region, proxy.city].map(value => value?.trim()).filter((value, index, values) => value && values.indexOf(value) === index)
  return parts.length ? parts.join(' / ') : (proxy.country_code || '位置未知')
}
const managedProxyDisplayURL = (proxy: Proxy) => {
  const host = proxy.host.includes(':') && !proxy.host.startsWith('[') ? `[${proxy.host}]` : proxy.host
  return `${proxy.protocol}://${proxy.username ? `${proxy.username}@` : ''}${host}:${proxy.port}`
}
type HostRow = { host: string; hits: number; entry?: OpenAICodexCookieLibraryEntry }
// Proxy memory may contain legacy route keys such as "unified-24". They are
// internal routing identifiers, not Cookie Hosts, so they must not appear in
// the Cookie library or affect its totals.
const isDisplayableHost = (value: string) => {
  const host = value.trim().toLowerCase()
  return host.includes('.') && !/^unified-\d+$/.test(host)
}
const displayMemoryHosts = (memory: cookies.CookieProxyHostMemory) => (memory.hosts || []).filter(item => isDisplayableHost(item.host))
const proxyMemorySummary = computed(() => {
  const hosts = new Set<string>()
  let hits = 0
  let completed = 0
  for (const memory of proxyMemories.value) {
    if (memory.completed) completed++
    for (const item of displayMemoryHosts(memory)) {
      hosts.add(item.host.trim().toLowerCase())
      hits += Number(item.count || 0)
    }
  }
  return { proxies: proxyMemories.value.length, hosts: hosts.size, hits, completed }
})
const validLibraryCount = computed(() => library.value.filter(entry => isDisplayableHost(entry.host)).length)
const allHostRows = computed<HostRow[]>(() => {
  const rows = new Map<string, HostRow>()
  for (const memory of proxyMemories.value) {
    for (const item of memory.hosts || []) {
      const host = item.host.trim().toLowerCase()
      if (!isDisplayableHost(host)) continue
      const current = rows.get(host) || { host, hits: 0 }
      current.hits += item.count || 0
      rows.set(host, current)
    }
  }
  for (const entry of library.value) {
    const host = entry.host.trim().toLowerCase()
    if (!isDisplayableHost(host)) continue
    const current = rows.get(host) || { host, hits: 0 }
    current.entry = entry
    current.hits = Math.max(1, current.hits)
    rows.set(host, current)
  }
  return [...rows.values()].sort((a, b) => {
    if (Boolean(a.entry) !== Boolean(b.entry)) return a.entry ? -1 : 1
    if (a.entry && b.entry) return new Date(b.entry.captured_at).getTime() - new Date(a.entry.captured_at).getTime()
    return b.hits - a.hits || a.host.localeCompare(b.host)
  })
})
const missingHostCount = computed(() => allHostRows.value.filter(row => !row.entry).length)
const filteredHostRows = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  return keyword ? allHostRows.value.filter(row => row.host.includes(keyword)) : allHostRows.value
})
const refreshableHostRows = computed(() => filteredHostRows.value.filter(row => Boolean(row.entry)))
const filteredLogs = computed(() => {
  if (acquisitionSource.value === 'all') return logs.value
  return logs.value.filter(log => log.task === acquisitionSource.value || log.kind === acquisitionSource.value)
})
const validationLogs = computed(() => validationOnlyLogs.value)
const validationAccounts = computed(() => cookieAccounts.value.map(account => ({ id: account.id, name: account.name })))
const validationStageLabels: Record<string, string> = { host_selected: '准备绑定 Host', host_cookie_bound: 'Host Cookie 已绑定', validation_started: '开始验证提示词', validation_succeeded: '验证成功', validation_rejected: '验证未通过', validation_failed: '验证失败', host_bound: 'Host 已绑定', host_bind_failed: 'Host 绑定失败', previous_host_cooldown: '旧 Host 进入冷静期', ws_build_started: '开始构建 WS', ws_build_succeeded: 'WS 构建成功', ws_build_failed: 'WS 构建失败', ws_build_skipped: 'WS 构建已跳过' }
const validationStageLabel = (stage?: string) => validationStageLabels[stage || ''] || '历史记录'
const remaining = (value: string) => Math.max(0, Math.floor((new Date(value).getTime() - now.value) / 1000))
const formatTime = (value: string) => value ? new Date(value).toLocaleString() : '—'
const formatClock = (value: string) => value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '----/--/-- --:--:--'
const acquisitionEnabled = computed(() => form.enabled || form.remote_sync_enabled || form.response_cookie_sync_enabled)
const acquisitionResultLabel = (log: cookies.CookieLog) => log.stage?.startsWith('ignored_') ? '已跳过' : log.success ? '已入库' : '未入库'
const isSchedulerLog = (log: cookies.CookieLog) => log.kind === 'scheduler' || log.kind === 'remote_sync' || (log.kind !== 'response_sync' && log.account_id <= 0 && !log.status_code)
const liveAccountLabel = (log: cookies.CookieLog) => isSchedulerLog(log) ? '系统调度' : `账号 ${log.account_name || '—'} #${log.account_id}`
const liveAcquisitionText = (log: cookies.CookieLog) => {
  if (isSchedulerLog(log)) return `${liveAccountLabel(log)} · ${log.message}`
  const result = acquisitionResultLabel(log)
  const status = log.status_code ? `HTTP ${log.status_code}` : '未收到响应'
  const host = log.host ? ` · Host ${log.host}` : ''
  return `${harvestTaskLabel(log.task)} · ${liveAccountLabel(log)} · ${result} · ${status} · ${log.message}${host}${log.target_host ? ` · 目标 ${log.target_host}` : ''}`
}
const liveRotationText = (log: cookies.CookieLog) => {
  const host = log.host ? ` · Host ${log.host}` : ''
  return `${liveAccountLabel(log)} · ${validationStageLabel(log.stage)} · ${log.message}${host}`
}
const liveAcquisitionTone = (log: cookies.CookieLog) => isSchedulerLog(log) || log.stage?.startsWith('ignored_') ? 'text-amber-300' : log.success ? 'text-emerald-300' : 'text-red-300'
const liveRotationTone = (log: cookies.CookieLog) => {
  const stage = log.stage || ''
  if (stage.includes('failed') || stage === 'validation_rejected') return 'text-red-300'
  if (stage.includes('succeeded') || stage === 'host_bound') return 'text-emerald-300'
  if (stage === 'previous_host_cooldown') return 'text-amber-300'
  return 'text-sky-200'
}
function scrollLiveConsole(kind: LiveLogKind) {
  void nextTick(() => {
    const element = kind === 'acquisition' ? acquisitionConsole.value : rotationConsole.value
    if (element) element.scrollTop = 0
  })
}
function appendLiveLogs(kind: LiveLogKind, items: cookies.CookieLog[]) {
  const seen = kind === 'acquisition' ? liveAcquisitionSeen : liveRotationSeen
  const target = kind === 'acquisition' ? liveAcquisitionLogs : liveRotationLogs
  const fresh = [...items].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()).filter(item => {
    if (!item.id || seen.has(item.id)) return false
    seen.add(item.id)
    return true
  })
  if (!fresh.length) return
  target.value = [...target.value, ...fresh].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()).slice(0, 100)
  scrollLiveConsole(kind)
}
async function pollLiveLogs() {
  if (livePolling) return
  const acquisitionActive = (tab.value === 'logs' || manualRefreshDialog.value) && !liveAcquisitionPaused.value
  const rotationActive = tab.value === 'rotation-logs' && !liveRotationPaused.value
  if (!acquisitionActive && !rotationActive) return
  livePolling = true
  try {
    const tasks: Promise<void>[] = []
    if (acquisitionActive) {
      tasks.push(cookies.getLogs(100).then(value => appendLiveLogs('acquisition', value || [])))
      tasks.push(cookies.getHarvestRuntime().then(value => { harvestRunning.value = value }))
    }
    if (rotationActive) tasks.push(cookies.getValidationLogs({ page: 1, page_size: 100 }).then(value => appendLiveLogs('rotation', value.items || [])))
    await Promise.all(tasks)
    liveStatusError.value = ''
  } catch {
    liveStatusError.value = '实时日志连接异常'
  } finally {
    livePolling = false
  }
}
function toggleLive(kind: LiveLogKind) {
  if (kind === 'acquisition') liveAcquisitionPaused.value = !liveAcquisitionPaused.value
  else liveRotationPaused.value = !liveRotationPaused.value
  void pollLiveLogs()
}
function clearLive(kind: LiveLogKind) {
  if (kind === 'acquisition') { liveAcquisitionLogs.value = []; liveAcquisitionSeen.clear() }
  else { liveRotationLogs.value = []; liveRotationSeen.clear() }
}
const pickerTitle = computed(() => ({ groups: '选择获取分组', accounts: '选择指定账号', 'rotation-groups': '选择轮换分组', 'rotation-accounts': '选择轮换账号', 'managed-proxies': '选择 IP 管理代理' })[picker.value || 'accounts'])
function openPicker(kind: PickerKind) {
  picker.value = kind
  const ids = kind === 'groups' ? form.group_ids : kind === 'accounts' ? form.account_ids : kind === 'rotation-groups' ? form.rotation_group_ids : kind === 'rotation-accounts' ? form.rotation_account_ids : form.managed_proxy_ids
  pickerDraft.value = [...ids].map(Number)
}
function applyPicker() {
  if (picker.value === 'groups') form.group_ids = [...pickerDraft.value]
  if (picker.value === 'accounts') form.account_ids = [...pickerDraft.value]
  if (picker.value === 'rotation-groups') form.rotation_group_ids = [...pickerDraft.value]
  if (picker.value === 'rotation-accounts') form.rotation_account_ids = [...pickerDraft.value]
  if (picker.value === 'managed-proxies') form.managed_proxy_ids = [...pickerDraft.value]
  picker.value = null
}
function handleCookieRotationToggle() {
  if (form.cookie_rotation_enabled) form.ws_enabled = false
}
function handleWSToggle() {
  if (form.ws_enabled) form.cookie_rotation_enabled = false
}
function pickerSummary(kind: PickerKind) {
  const ids = kind === 'groups' ? form.group_ids : kind === 'accounts' ? form.account_ids : kind === 'rotation-groups' ? form.rotation_group_ids : kind === 'rotation-accounts' ? form.rotation_account_ids : form.managed_proxy_ids
  if (kind === 'managed-proxies') return ids.length ? `已选择 ${ids.length} 个代理` : '未选择 IP 管理代理'
  if (!ids.length) return kind === 'groups' || kind === 'rotation-groups' ? '未选择分组' : kind === 'accounts' ? '未指定账号（仅使用所选分组）' : '未单独指定轮换账号'
  return `已选择 ${ids.length} 项`
}
function pickerDisplayNames(kind: PickerKind) {
  const ids = kind === 'groups' ? form.group_ids : kind === 'accounts' ? form.account_ids : kind === 'rotation-groups' ? form.rotation_group_ids : kind === 'rotation-accounts' ? form.rotation_account_ids : form.managed_proxy_ids
  if (kind === 'groups' || kind === 'rotation-groups') return ids.map(id => cookieGroups.value.find(group => Number(group.id) === Number(id))?.name || `分组 #${id}`)
  if (kind === 'accounts' || kind === 'rotation-accounts') return ids.map(id => cookieAccounts.value.find(account => Number(account.id) === Number(id))?.name || `账号 #${id}`)
  return ids.map(id => managedProxies.value.find(proxy => Number(proxy.id) === Number(id))?.name || `代理 #${id}`)
}
function showDetail(title: string, value: unknown) { detailTitle.value = title; detail.value = JSON.stringify(value, null, 2) }
function toggleAllHosts(event: Event) {
  const checked = (event.target as HTMLInputElement).checked
  const current = new Set(selectedHostsArray.value)
  for (const row of refreshableHostRows.value) {
    if (checked) current.add(row.host)
    else current.delete(row.host)
  }
  selectedHostsArray.value = [...current]
}
async function refreshSelectedHosts(hosts = selectedHostsArray.value) {
  const values = [...new Set(hosts.map(host => host.trim().toLowerCase()).filter(Boolean))]
  if (!values.length || manualRefreshLoading.value) return
  manualRefreshHosts.value = values
  manualRefreshDialog.value = true
  manualRefreshLoading.value = true
  liveAcquisitionLogs.value = []
  liveAcquisitionSeen.clear()
  try {
    const result = await cookies.refreshHosts(values)
    notice.value = `已提交 ${result.queued} 个 Cookie 刷新任务`
    void pollLiveLogs()
  } catch (err) {
    error.value = message(err)
  } finally {
    manualRefreshLoading.value = false
  }
}
async function copyCookie(value: string) {
  try {
    await navigator.clipboard.writeText(value)
    notice.value = 'Cookie 已复制'
    window.setTimeout(() => { if (notice.value === 'Cookie 已复制') notice.value = '' }, 1800)
  } catch {
    error.value = '复制失败，请检查浏览器剪贴板权限'
  }
}
function message(err: unknown) { return err instanceof Error ? err.message : '请求失败，请重试' }
async function loadProxyMemories() {
  proxyMemoriesLoading.value = true
  try { proxyMemories.value = (await cookies.getProxyHostMemories()) || [] } catch (err) { error.value = message(err) } finally { proxyMemoriesLoading.value = false }
}
async function resetProxyMemory(memory: cookies.CookieProxyHostMemory) {
  const label = memory.proxy_username ? `${memory.proxy}（${memory.proxy_username}）` : memory.proxy
  if (!window.confirm(`确认清空代理 ${label} 的 Host 记忆并重新学习吗？此操作只影响这个代理。`)) return
  const key = `${memory.proxy}|${memory.proxy_username || ''}`
  proxyMemoryResetting.value = key
  try { await cookies.resetProxyHostMemory(memory.proxy, memory.proxy_username || ''); await loadProxyMemories() } catch (err) { error.value = message(err) } finally { proxyMemoryResetting.value = '' }
}
async function resetAllProxyMemories() {
  if (!window.confirm('确认清空全部代理的 Host 记忆并重新学习吗？此操作不可撤销。')) return
  proxyMemoryResetting.value = '__all__'
  try { await cookies.resetAllProxyHostMemories(); await loadProxyMemories() } catch (err) { error.value = message(err) } finally { proxyMemoryResetting.value = '' }
}
async function loadSettings() {
  const [value, monitorConfig] = await Promise.all([cookies.getSettings(), intelligenceMonitorAPI.getConfig()])
  Object.assign(form, value)
  form.degraded_group_id = value.degraded_group_id || 0
  form.harvest_policy = { ...cookies.defaultHarvestPolicy(), ...value.harvest_policy }
  form.account_ids = (value.account_ids?.length ? value.account_ids : (value.account_id ? [value.account_id] : [])).map(Number)
  form.group_ids = (value.group_ids || []).map(Number)
  form.rotation_account_ids = (value.rotation_account_ids || []).map(Number)
  form.rotation_group_ids = (value.rotation_group_ids || []).map(Number)
  form.rotation_pools = value.rotation_pools || []
  form.managed_proxy_ids = (value.managed_proxy_ids || []).map(Number)
  proxyText.value = (value.proxy_urls || []).join('\n')
  whitelistText.value = (value.host_whitelist || []).join('\n')
  intelligenceMonitorConfig.value = monitorConfig
  intelligenceModel.value = monitorConfig.model_id || 'gpt-6-astra'
  settingsLoaded.value = true
}
async function loadCookieAccounts() {
  const all: AccountListItem[] = []
  for (let page = 1; ; page++) {
    const result = await accountsAPI.list(page, 200, { platform: 'openai', status: 'active', lite: 'true' })
    all.push(...(result.items || []))
    if (!result.items?.length || all.length >= result.total) break
  }
  cookieAccounts.value = all.filter(account => account.type === 'oauth' || account.type === 'setup-token')
}
async function loadCookieGroups() {
  cookieGroups.value = (await groupsAPI.getAll('openai')).filter(group => group.status === 'active')
}
async function loadManagedProxies() {
  managedProxies.value = (await proxiesAPI.getAllWithCount()) || []
}
async function loadValidationLogs() {
  const result = await cookies.getValidationLogs({ account_id: validationEmail.value.trim() ? undefined : validationAccountId.value ? Number(validationAccountId.value) : undefined, email: validationEmail.value.trim() || undefined, page: validationPage.value, page_size: validationPageSize })
  validationOnlyLogs.value = result.items || []
  validationTotal.value = result.total || 0
}
function changeValidationPage(page: number) {
  validationPage.value = page
  void loadValidationLogs().catch(err => { error.value = message(err) })
}
let libraryRequest: Promise<void> | null = null
let libraryNextAt = 0
let libraryFailures = 0
function loadLibrary(): Promise<void> {
  if (libraryRequest) return libraryRequest
  libraryRequest = cookies.getLibrary().then(value => {
    if (!dashboardDisposed) library.value = value || []
    libraryFailures = 0
  }).catch(err => {
    libraryFailures++
    throw err
  }).finally(() => {
    libraryNextAt = Date.now() + Math.min(60000, 10000 * 2 ** libraryFailures)
    libraryRequest = null
  })
  return libraryRequest
}
async function refresh() {
  if (loading.value) return
  loading.value = true
  try {
    const results = await Promise.allSettled([
      loadDashboard(),
      loadLibrary(),
      cookies.getLogs().then(value => { logs.value = value || [] }),
      loadProxyMemories(),
      loadValidationLogs(),
      loadCookieAccounts(),
      loadCookieGroups(),
      loadManagedProxies(),
      settingsLoaded.value ? Promise.resolve() : loadSettings()
    ])
    const failure = results.find(result => result.status === 'rejected')
    error.value = failure?.status === 'rejected' ? message(failure.reason) : ''
  } finally { loading.value = false }
}
async function save() {
  saving.value = true; error.value = ''; notice.value = ''
  try {
    const lines = (text: string) => text.split(/\r?\n/).map(line => line.trim()).filter(Boolean)
    const [value] = await Promise.all([
      cookies.saveSettings({ ...form, account_ids: form.account_ids.map(Number), group_ids: form.group_ids.map(Number), rotation_account_ids: form.rotation_account_ids.map(Number), rotation_group_ids: form.rotation_group_ids.map(Number), managed_proxy_ids: form.managed_proxy_ids.map(Number), dynamic_proxy_fill_host_cookie: form.cookie_proxy_schedule_mode === 'dynamic', proxy_urls: lines(proxyText.value), host_whitelist: lines(whitelistText.value) }),
      saveIntelligenceModel()
    ])
    Object.assign(form, value)
    form.account_ids = (value.account_ids?.length ? value.account_ids : (value.account_id ? [value.account_id] : [])).map(Number)
    form.group_ids = (value.group_ids || []).map(Number)
    form.rotation_account_ids = (value.rotation_account_ids || []).map(Number)
    form.rotation_group_ids = (value.rotation_group_ids || []).map(Number)
  form.rotation_pools = value.rotation_pools || []
    form.managed_proxy_ids = (value.managed_proxy_ids || []).map(Number)
    notice.value = '已保存，Host 轮换范围、分组容量与降级策略已更新。'
    void loadDashboard()
  } catch (err) { error.value = message(err) } finally { saving.value = false }
}
async function saveIntelligenceModel() {
  const current = intelligenceMonitorConfig.value || { enabled: false, interval_seconds: 3600, max_rounds: 20, model_id: 'gpt-6-astra', group_ids: [], prompts: [] }
  const updated = await intelligenceMonitorAPI.updateConfig({ ...current, model_id: intelligenceModel.value.trim() || 'gpt-6-astra' })
  intelligenceMonitorConfig.value = updated
  intelligenceModel.value = updated.model_id
}
let refreshTimer: ReturnType<typeof setInterval> | undefined
let clockTimer: ReturnType<typeof setInterval> | undefined
let liveTimer: ReturnType<typeof setInterval> | undefined
let dashboardTimer: ReturnType<typeof setInterval> | undefined
watch(validationAccountId, () => { validationPage.value = 1; void loadValidationLogs().catch(err => { error.value = message(err) }) })
watch(tab, value => { if (value === 'pool-cockpit' || value === 'settings') void loadDashboard(); if (value === 'logs' || value === 'rotation-logs') void pollLiveLogs() })
onMounted(() => { void refresh(); refreshTimer = setInterval(() => { if (!document.hidden && !dashboardDisposed && (tab.value === 'library' || tab.value === 'host-monitor') && Date.now() >= libraryNextAt) void loadLibrary().catch(err => { error.value = message(err) }) }, 1000); clockTimer = setInterval(() => { now.value = Date.now() }, 1000); liveTimer = setInterval(() => { void pollLiveLogs() }, 1500) })
onMounted(() => { dashboardTimer = setInterval(() => { if ((tab.value === 'pool-cockpit' || tab.value === 'settings') && Date.now() >= dashboardNextAt) void loadDashboard() }, 1000); document.addEventListener('visibilitychange', onDashboardVisibility) })
function onDashboardVisibility() { if (!document.hidden && (tab.value === 'pool-cockpit' || tab.value === 'settings') && Date.now() >= dashboardNextAt) void loadDashboard() }
onUnmounted(() => { dashboardDisposed = true; clearInterval(dashboardTimer); document.removeEventListener('visibilitychange', onDashboardVisibility); clearInterval(refreshTimer); clearInterval(clockTimer); clearInterval(liveTimer) })
</script>
