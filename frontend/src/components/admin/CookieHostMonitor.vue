<template>
  <section class="space-y-4">
    <div class="card space-y-3 p-5">
      <h2 class="text-lg font-semibold">Host 拉黑恢复监控</h2>
      <p class="text-sm text-gray-500">固定“邮箱 + Host”复测 yes/no，观察降智现象何时恢复。响应不能证明服务端确实拉黑，展示的是实测恢复区间，不是上游公布的 TTL。重新导入同邮箱账号会继续关联历史。</p>
      <form class="space-y-4" @submit.prevent="save">
        <div class="grid gap-4 md:grid-cols-3">
          <label class="space-y-1"><span class="block text-sm">绑定监控账号</span><select v-model.number="form.account_id" required class="input w-full" :disabled="data?.config.enabled" @change="selectAccount"><option :value="0" disabled>请选择账号</option><option v-for="account in accounts" :key="account.id" :value="account.id">#{{ account.id }} · {{ account.name }}</option></select><span class="text-xs text-gray-500">{{ form.email || '选择后按邮箱保留历史，重新导入可继续关联' }}</span></label>
          <label class="space-y-1"><span class="block text-sm">固定 Host</span><input v-model="form.host" required class="input w-full" :disabled="data?.config.enabled" list="monitor-hosts" placeholder="chat.gateway.unified-136.api.openai.com" /><datalist id="monitor-hosts"><option v-for="host in hosts" :key="host" :value="host" /></datalist></label>
          <label class="space-y-1"><span class="block text-sm">验证模型</span><input v-model="form.model" required maxlength="128" class="input w-full" /></label>
          <label class="space-y-1"><span class="block text-sm">首次等待（秒）</span><input v-model.number="form.initial_wait_seconds" required type="number" min="0" max="604800" class="input w-full" /><span class="text-xs text-gray-500">默认 21600 秒（6 小时），从启用时开始计时。</span></label>
          <label class="space-y-1"><span class="block text-sm">后续复测间隔（秒）</span><input v-model.number="form.interval_seconds" required type="number" min="30" max="86400" class="input w-full" /></label>
          <label class="flex items-center gap-2"><input v-model="form.enabled" type="checkbox" />启用固定 Host 监控</label>
        </div>
        <p class="text-xs text-amber-600">启用并保存后直接绑定账号到指定 Host，暂停普通调度、Cookie 采集/轮换和后台打票，后端禁止更换或清除 Cookie Host。需要换账号或 Host 时，请先关闭监控并保存。监控不会因普通绑定时间到期而切换节点；只使用此 Host 的有效 Cookie，缺失时等待补齐。关闭页面后仍按规则自动检测；启用前已在执行的请求仍可能影响测量。</p>
        <p class="text-xs text-gray-500">建议启用后先点“立即验证”建立 no 基线，再等待 6 小时复测。仅第一次出现 yes 且此前记录过 no，才标为观察到恢复。错误自动重试最多 2 次，不计入 no；Cookie 有效期与账号限制恢复时间分开展示。关闭再启用或更换邮箱、Host、模型会开始新实验，旧历史保留最近 2000 条。</p>
        <div class="flex flex-wrap gap-3"><button class="btn btn-primary" :disabled="busy">保存配置</button><button type="button" class="btn btn-secondary" :disabled="busy || !data?.config.enabled || data?.running" @click="run">{{ data?.running ? '后台验证中…' : '立即验证（已保存配置）' }}</button><button type="button" class="btn btn-secondary" @click="load(false)">刷新结果</button></div>
      </form>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p><p v-if="notice" class="text-sm text-emerald-600">{{ notice }}</p>
    </div>
    <div class="grid gap-3 md:grid-cols-3">
      <div class="card p-4"><div class="text-xs text-gray-500">运行状态</div><div class="mt-2" :class="data?.running ? 'animate-pulse text-amber-600' : ''">{{ data?.running ? '正在请求指定 Host' : data?.config.enabled ? '等待下次验证' : '未启用' }}</div><div v-if="data?.config.enabled" class="mt-1 text-xs text-gray-500">计划时间 {{ date(data.next_at) }}</div></div>
      <div class="card p-4"><div class="text-xs text-gray-500">本轮首次 no / 最近 no</div><div class="mt-2 text-sm">{{ date(data?.first_no) }}<br />{{ date(data?.last_no) }}</div></div>
      <div class="card p-4"><div class="text-xs text-gray-500">从首次 no 起，实测恢复区间</div><div class="mt-2 text-sm">{{ recovery }}</div><div class="mt-1 text-xs text-gray-500">{{ data?.recovered_at ? `首次恢复：${date(data.recovered_at)}` : '未观察到 no → yes 时，不推断 TTL' }}</div></div>
    </div>
    <div class="card overflow-x-auto">
      <table class="w-full text-left text-sm"><thead><tr class="border-b"><th class="p-3">时间 / 邮箱</th><th class="p-3">Host / 模型</th><th class="p-3">结果</th><th class="p-3">Cookie 过期时间</th><th class="p-3">响应</th></tr></thead><tbody>
        <tr v-for="row in data?.items || []" :key="row.id" class="border-b align-top"><td class="p-3 whitespace-nowrap">{{ date(row.finished_at) }}<div class="text-xs text-gray-500">{{ row.email }} · #{{ row.account_id }}</div></td><td class="max-w-xs p-3"><div class="truncate" :title="row.host">{{ row.host }}</div><div class="text-xs text-gray-500">{{ row.model }}</div></td><td class="p-3 whitespace-nowrap" :class="row.result === 'yes' ? 'text-emerald-600' : row.result === 'no' ? 'text-amber-600' : 'text-red-600'">{{ labels[row.result] || row.result }}<div class="text-xs">HTTP {{ row.http_status || '—' }}</div></td><td class="p-3 whitespace-nowrap">{{ date(row.cookie_expires_at) }}</td><td class="p-3"><details><summary class="cursor-pointer">查看响应</summary><pre class="mt-2 max-h-60 max-w-lg overflow-auto whitespace-pre-wrap break-all text-xs">{{ row.response }}</pre></details></td></tr>
        <tr v-if="!data?.items.length"><td colspan="5" class="p-6 text-center text-gray-500">暂无验证记录</td></tr>
      </tbody></table>
    </div>
    <div class="flex items-center justify-between"><span class="text-sm text-gray-500">历史共 {{ data?.total || 0 }} 条 · 第 {{ page }} 页（时间倒序）</span><div class="flex gap-2"><button class="btn btn-secondary" :disabled="page <= 1" @click="changePage(-1)">上一页</button><button class="btn btn-secondary" :disabled="page * 20 >= (data?.total || 0)" @click="changePage(1)">下一页</button></div></div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { apiClient } from '@/api/client'
defineProps<{ hosts: string[] }>()
interface Config { enabled: boolean; account_id: number; email: string; host: string; model: string; initial_wait_seconds: number; interval_seconds: number }
interface Sample { id: string; email: string; host: string; model: string; account_id: number; finished_at: string; cookie_expires_at?: string; result: string; http_status: number; response: string }
interface Result { config: Config; items: Sample[]; total: number; running: boolean; next_at: string; first_no?: string; last_no?: string; recovered_at?: string }
const form = reactive<Config>({ enabled: false, account_id: 0, email: '', host: '', model: 'gpt-6-astra', initial_wait_seconds: 21600, interval_seconds: 300 })
interface MonitorAccount { id: number; name: string; type: string; extra?: Record<string, unknown> }
const accounts = ref<MonitorAccount[]>([])
async function loadAccounts() {
  try {
    let page = 1
    const items: MonitorAccount[] = []
    while (true) {
      const { data } = await apiClient.get<{ items: MonitorAccount[]; total: number }>('/admin/accounts', { params: { platform: 'openai', status: 'active', page, page_size: 100 } })
      items.push(...data.items)
      if (!data.items.length || items.length >= data.total) break
      page++
    }
    accounts.value = items.filter(a => a.type === 'oauth' || a.type === 'setup-token')
  } catch (e) { error.value = message(e) }
}
function selectAccount() {
  const account = accounts.value.find(a => a.id === form.account_id)
  form.email = ''
  if (account?.extra?.codex_cookie_host) form.host = String(account.extra.codex_cookie_host)
}
const data = ref<Result>(); const page = ref(1); const error = ref(''); const notice = ref(''); const busy = ref(false)
const endpoint = '/admin/settings/cookie/host-monitor'
const labels: Record<string, string> = { yes: 'yes · 验证正常', no: 'no · 疑似降智', error: '请求异常（不计拉黑）', cookie_unavailable: 'Cookie 不可用' }
const date = (v?: string) => v && !v.startsWith('0001') ? new Date(v).toLocaleString() : '—'
const duration = (ms: number) => `${(Math.max(0, ms) / 3600000).toFixed(2)} 小时`
const recovery = computed(() => {
  const r = data.value
  if (!r?.first_no) return '尚无 no 基线，无法估算'
  if (!r.recovered_at) return '尚未观察到恢复'
  return `${duration(Date.parse(r.last_no!) - Date.parse(r.first_no))} ～ ${duration(Date.parse(r.recovered_at) - Date.parse(r.first_no))}`
})
let loading = false; let stopped = false; let timer: ReturnType<typeof setInterval>
const message = (e: unknown) => e instanceof Error ? e.message : '操作失败'
async function load(config = false) {
  if (loading) return
  loading = true
  try { const result = (await apiClient.get<Result>(endpoint, { params: { page: page.value, page_size: 20 } })).data; if (!stopped) { data.value = result; if (config) Object.assign(form, result.config) } } catch (e) { error.value = message(e) } finally { loading = false }
}
async function save() { busy.value = true; error.value = ''; notice.value = ''; try { await apiClient.put(endpoint, form); notice.value = '已保存，监控由后端持续运行'; await load(true) } catch (e) { error.value = message(e) } finally { busy.value = false } }
async function run() { busy.value = true; error.value = ''; try { await apiClient.post(`${endpoint}/run`); notice.value = '已提交后台验证，关闭页面不影响执行'; await load(false) } catch (e) { error.value = message(e) } finally { busy.value = false } }
function changePage(delta: number) { page.value += delta; void load(false) }
onMounted(() => { void load(true); void loadAccounts(); timer = setInterval(() => { void load(false) }, 5000) })
onUnmounted(() => { stopped = true; clearInterval(timer) })
</script>
