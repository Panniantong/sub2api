<template>
  <AppLayout>
    <div class="w-full max-w-none space-y-6">
      <header class="flex flex-wrap items-center justify-between gap-3">
        <div><h1 class="text-xl font-semibold text-gray-900 dark:text-white">智力监控</h1><p class="mt-1 text-sm text-gray-500">独立执行周期检测并保存每轮结果，不与账号列表中的单次智力测试共享记录。</p></div>
        <div class="flex gap-2"><button class="btn btn-secondary" :disabled="loading" @click="load">刷新</button><button class="btn btn-primary" :disabled="runtime.running || running" @click="runNow"><span v-if="runtime.running || running" class="mr-2 inline-block h-3 w-3 animate-spin rounded-full border-2 border-white/40 border-t-white"></span>{{ runtime.running || running ? '正在执行' : '立即执行一轮' }}</button></div>
      </header>
      <div v-if="loadError" class="rounded border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">{{ loadError }}</div>
      <nav class="flex gap-2 border-b border-gray-200 pb-3 dark:border-dark-700"><button type="button" class="btn" :class="pageTab === 'config' ? 'btn-primary' : 'btn-secondary'" @click="pageTab = 'config'">监控配置</button><button type="button" class="btn" :class="pageTab === 'results' ? 'btn-primary' : 'btn-secondary'" @click="pageTab = 'results'">检测数据<span v-if="runtime.running" class="ml-2 inline-block h-2 w-2 animate-pulse rounded-full bg-white"></span></button></nav>

      <section v-if="runtime.running" class="overflow-hidden rounded-lg border border-primary-200 bg-white shadow-sm dark:border-primary-900 dark:bg-dark-800"><div class="flex flex-wrap items-center justify-between gap-3 px-4 py-3"><div class="flex min-w-0 items-center gap-3"><span class="relative flex h-3 w-3 shrink-0"><span class="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary-400 opacity-60"></span><span class="relative inline-flex h-3 w-3 rounded-full bg-primary-500"></span></span><div class="min-w-0"><div class="font-medium text-gray-900 dark:text-white">正在执行批次 {{ shortRoundID(runtime.round_id) }}</div><div class="truncate text-xs text-gray-500">{{ runtime.account_name || '准备账号' }} · {{ runtime.prompt_title || runtime.message || '准备题目' }}</div></div></div><span class="text-sm font-medium text-primary-600">{{ runtime.completed }}/{{ runtime.total }}</span></div><div class="h-1.5 bg-gray-100 dark:bg-dark-700"><div class="h-full bg-primary-500 transition-all duration-500" :style="{ width: `${runtimeProgress}%` }"></div></div></section>

      <template v-if="pageTab === 'config'">
        <section class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800"><div class="mb-5 flex items-center justify-between gap-3"><div><h2 class="font-medium text-gray-900 dark:text-white">运行参数</h2><p class="mt-1 text-xs text-gray-500">设置监控周期、结果保留数量、测试模型和执行账号范围。</p></div><label class="flex shrink-0 items-center gap-2 text-sm"><input v-model="config.enabled" type="checkbox" class="h-4 w-4" />启用自动监控</label></div><div class="grid gap-4 md:grid-cols-3"><label class="text-sm text-gray-600 dark:text-gray-300">执行间隔（秒）<input v-model.number="config.interval_seconds" type="number" min="30" class="input mt-1 w-full" /></label><label class="text-sm text-gray-600 dark:text-gray-300">保留轮数<input v-model.number="config.max_rounds" type="number" min="1" max="200" class="input mt-1 w-full" /></label><label class="text-sm text-gray-600 dark:text-gray-300">测试模型<input v-model="config.model_id" class="input mt-1 w-full" placeholder="例如 gpt-6-astra" /></label></div><label class="mt-4 block max-w-2xl text-sm text-gray-600 dark:text-gray-300">执行账号组（可多选）<button type="button" class="input mt-1 flex min-h-11 w-full items-center justify-between text-left" @click="openGroupPicker"><span v-if="selectedGroupNames.length" class="flex min-w-0 flex-wrap gap-1"><span v-for="name in selectedGroupNames" :key="name" class="max-w-full truncate rounded bg-primary-50 px-2 py-0.5 text-xs text-primary-700">{{ name }}</span></span><span v-else class="text-gray-500">未选择分组（执行所有启用账号）</span><span class="ml-2 shrink-0 text-gray-400">⌄</span></button><span class="mt-1 block text-xs text-gray-500">未选择时执行全部启用的 OpenAI 账号。</span></label></section>
        <section class="rounded-lg border border-gray-200 bg-white p-5 shadow-sm dark:border-dark-700 dark:bg-dark-800"><div class="mb-4 flex items-center justify-between gap-3"><div><h2 class="font-medium text-gray-900 dark:text-white">检测题目</h2><p class="mt-1 text-xs text-gray-500">每轮仅执行已启用的题目，监控结果单独保存。</p></div><button class="btn btn-secondary px-3 py-1.5 text-sm" @click="addPrompt">新增提示词</button></div><div class="space-y-3"><article v-for="(prompt, index) in config.prompts" :key="prompt.id" class="grid gap-3 rounded border border-gray-200 p-3 md:grid-cols-[190px_1fr_auto] dark:border-dark-600"><div class="flex items-center gap-2"><input v-model="prompt.enabled" type="checkbox" class="h-4 w-4" /><input v-model="prompt.title" class="input min-w-0 flex-1 text-sm" placeholder="题目名称" /></div><textarea v-model="prompt.prompt" rows="3" class="input text-sm" placeholder="输入提示词"></textarea><button class="self-start text-sm text-red-600 hover:underline" @click="removePrompt(index)">删除</button></article><div v-if="config.prompts.length === 0" class="rounded border border-dashed p-6 text-center text-sm text-gray-500">暂无题目，请新增提示词。</div></div><div class="mt-5 flex justify-end"><button class="btn btn-primary" :disabled="saving" @click="saveConfig">{{ saving ? '保存中…' : '保存监控配置' }}</button></div></section>
      </template>

      <section v-else class="space-y-5">
        <div class="flex flex-wrap items-end justify-between gap-3 border-b border-gray-200 pb-4 dark:border-dark-700">
          <div>
            <h2 class="text-lg font-medium text-gray-900 dark:text-white">检测数据</h2>
            <p class="mt-1 text-sm text-gray-500">这里仅展示智力监控产生的批次结果。</p>
          </div>
          <span class="text-sm text-gray-500">共 {{ rounds.length }} 轮</span>
        </div>

        <div v-if="rounds.length === 0" class="rounded-lg border border-dashed p-12 text-center text-sm text-gray-500">暂无监控结果</div>

        <details
          v-for="round in rounds"
          :key="round.id"
          class="group overflow-hidden border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800"
          :open="isRoundOpen(round.id)"
          @toggle="onRoundToggle(round.id, $event)"
        >
          <summary class="grid cursor-pointer list-none gap-4 bg-gray-50 px-5 py-4 marker:hidden sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center dark:bg-dark-900">
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-x-3 gap-y-2">
                <span class="font-medium text-gray-900 dark:text-white">批次 {{ formatTime(round.started_at) }}</span>
                <span
                  class="inline-flex items-center rounded px-2 py-0.5 text-xs font-medium"
                  :class="round.finished_at ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300' : 'bg-primary-50 text-primary-700 dark:bg-primary-950/40 dark:text-primary-300'"
                >
                  {{ round.finished_at ? '已完成' : '执行中' }}
                </span>
              </div>
              <div class="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-xs text-gray-500">
                <span>轮次 {{ shortRoundID(round.id) }}</span>
                <span v-if="round.finished_at">耗时 {{ formatDuration(round.started_at, round.finished_at) }}</span>
                <span v-else>开始于 {{ formatTime(round.started_at) }}</span>
              </div>
            </div>
            <div class="flex items-center gap-5 sm:justify-end">
              <div class="text-right">
                <div class="text-sm font-medium text-gray-800 dark:text-gray-100">{{ round.results.length }} 条结果</div>
                <div class="mt-1 text-xs text-gray-500">
                  <span class="text-emerald-600">成功 {{ roundSuccessCount(round) }}</span>
                  <span class="mx-1.5 text-gray-300">/</span>
                  <span :class="roundFailureCount(round) ? 'text-red-600' : 'text-gray-500'">失败 {{ roundFailureCount(round) }}</span>
                </div>
              </div>
              <span class="text-lg leading-none text-gray-400 transition-transform group-open:rotate-180" aria-hidden="true">⌄</span>
            </div>
          </summary>

          <div v-if="isRoundOpen(round.id)" class="grid gap-6 border-t border-gray-200 p-5 xl:p-6 2xl:grid-cols-2 dark:border-dark-700">
            <article
              v-for="(result, index) in round.results"
              :key="`${round.id}-${index}`"
              class="min-w-0 overflow-hidden rounded-md border border-gray-200 bg-white dark:border-dark-600 dark:bg-dark-800"
            >
              <div class="flex flex-wrap items-start justify-between gap-4 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
                <div class="min-w-0">
                  <div class="text-base font-medium text-gray-900 dark:text-white">{{ result.title || result.case }}</div>
                  <div class="mt-1 truncate text-sm text-gray-500">{{ result.account_name }} · #{{ result.account_id }}</div>
                </div>
                <span
                  class="inline-flex shrink-0 items-center rounded px-2.5 py-1 text-xs font-medium"
                  :class="result.status === 'success' ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300' : 'bg-red-50 text-red-700 dark:bg-red-950/40 dark:text-red-300'"
                >
                  {{ result.status === 'success' ? '成功' : '失败' }}
                </span>
              </div>
              <iframe
                v-if="resultHTML(result)"
                :srcdoc="resultHTML(result)"
                sandbox="allow-scripts"
                class="h-[28rem] w-full bg-white"
                :title="`${result.title} HTML 结果`"
              ></iframe>
              <div v-else class="max-h-[32rem] min-h-64 overflow-auto whitespace-pre-wrap break-words bg-gray-950 p-5 font-mono text-sm leading-7 text-gray-100">{{ result.error || result.output || '未返回内容' }}</div>
              <div class="flex min-h-12 flex-wrap items-center justify-between gap-3 border-t border-gray-100 px-5 py-3 text-xs dark:border-dark-700">
                <code class="min-w-0 flex-1 truncate text-gray-500" :title="result.cookie_host">{{ result.cookie_host || '未绑定 Cookie Host' }}</code>
                <button v-if="resultHTML(result)" class="shrink-0 text-primary-600 hover:underline" @click="openHTML(resultHTML(result), result.title)">放大查看</button>
              </div>
            </article>
          </div>
        </details>
      </section>
    </div>
    <BaseDialog :show="groupPickerOpen" title="选择执行账号组" width="normal" @close="groupPickerOpen = false"><div class="max-h-80 space-y-2 overflow-y-auto"><label v-for="group in groups" :key="group.id" class="flex cursor-pointer items-center gap-3 rounded-lg px-3 py-2 hover:bg-gray-50"><input v-model="groupPickerDraft" type="checkbox" :value="Number(group.id)" class="h-4 w-4" /><span>{{ group.name }} <span class="text-xs text-gray-400">#{{ group.id }}</span></span></label><p v-if="!groups.length" class="p-3 text-sm text-gray-500">暂无可用 OpenAI 分组</p></div><template #footer><div class="flex justify-end gap-2"><button type="button" class="btn btn-secondary" @click="groupPickerOpen = false">取消</button><button type="button" class="btn btn-primary" @click="applyGroupPicker">应用（{{ groupPickerDraft.length }}）</button></div></template></BaseDialog>
    <BaseDialog :show="htmlPreview !== null" :title="htmlPreviewTitle" width="extra-wide" @close="htmlPreview = null"><iframe v-if="htmlPreview" :srcdoc="htmlPreview" sandbox="allow-scripts" class="h-[70vh] w-full bg-white" title="HTML 检测结果"></iframe></BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import { groupsAPI } from '@/api/admin/groups'
import type { IntelligenceMonitorConfig, IntelligenceMonitorRuntime, IntelligenceResult, IntelligenceRound } from '@/api/admin/intelligenceMonitor'
import type { AdminGroup } from '@/types'

const defaultConfig: IntelligenceMonitorConfig = { enabled: false, interval_seconds: 3600, max_rounds: 20, model_id: 'gpt-6-astra', group_ids: [], prompts: [] }
const defaultRuntime: IntelligenceMonitorRuntime = { running: false, total: 0, completed: 0 }
const config = ref<IntelligenceMonitorConfig>({ ...defaultConfig }); const runtime = ref<IntelligenceMonitorRuntime>({ ...defaultRuntime }); const groups = ref<AdminGroup[]>([]); const rounds = ref<IntelligenceRound[]>([]); const openRoundIDs = ref<Set<string>>(new Set())
const pageTab = ref<'config' | 'results'>('config'); const loading = ref(false); const saving = ref(false); const running = ref(false); const loadError = ref(''); const groupPickerOpen = ref(false); const groupPickerDraft = ref<number[]>([]); const htmlPreview = ref<string | null>(null); const htmlPreviewTitle = ref('HTML 检测结果'); let pollTimer: ReturnType<typeof setInterval> | null = null
const selectedGroupNames = computed(() => groups.value.filter(group => config.value.group_ids.includes(Number(group.id))).map(group => group.name)); const runtimeProgress = computed(() => runtime.value.total > 0 ? Math.min(100, Math.round(runtime.value.completed * 100 / runtime.value.total)) : 3)
const openGroupPicker = () => { groupPickerDraft.value = [...config.value.group_ids]; groupPickerOpen.value = true }; const applyGroupPicker = () => { config.value.group_ids = [...groupPickerDraft.value]; groupPickerOpen.value = false }; const shortRoundID = (value?: string) => value ? `#${value.slice(-8)}` : '新任务'; const formatTime = (value: string) => new Date(value).toLocaleString('zh-CN', { hour12: false })
const roundSuccessCount = (round: IntelligenceRound) => round.results.filter(result => result.status === 'success').length
const roundFailureCount = (round: IntelligenceRound) => round.results.length - roundSuccessCount(round)
const isRoundOpen = (roundID: string) => openRoundIDs.value.has(roundID)
const onRoundToggle = (roundID: string, event: Event) => {
  const details = event.target as HTMLDetailsElement
  const next = new Set(openRoundIDs.value)
  if (details.open) next.add(roundID)
  else next.delete(roundID)
  openRoundIDs.value = next
}
const formatDuration = (startedAt: string, finishedAt: string) => {
  const durationSeconds = Math.max(0, Math.round((new Date(finishedAt).getTime() - new Date(startedAt).getTime()) / 1000))
  if (durationSeconds < 60) return `${durationSeconds} 秒`
  const minutes = Math.floor(durationSeconds / 60)
  const seconds = durationSeconds % 60
  return seconds ? `${minutes} 分 ${seconds} 秒` : `${minutes} 分钟`
}
const extractHTML = (raw: string) => { const fenced = raw.match(/```(?:html)?\s*([\s\S]*?)```/i); const value = (fenced?.[1] || raw).trim(); const match = value.match(/(?:<!doctype html|<html|<svg)[\s\S]*/i); return match?.[0]?.trim() || '' }; const resultHTML = (result: IntelligenceResult) => result.html || extractHTML(result.output || ''); const openHTML = (html: string, title?: string) => { htmlPreview.value = html; htmlPreviewTitle.value = title || 'HTML 检测结果' }
const loadRuntime = async () => { const previousRunning = runtime.value.running; runtime.value = { ...defaultRuntime, ...(await adminAPI.intelligenceMonitor.getRuntime()) }; if (previousRunning && !runtime.value.running) await loadResults() }
const loadResults = async () => { const loadedRounds = await adminAPI.intelligenceMonitor.listResults(20); rounds.value = Array.isArray(loadedRounds) ? loadedRounds.map(round => ({ ...round, results: Array.isArray(round.results) ? round.results : [] })) : []; if (rounds.value.length > 0 && openRoundIDs.value.size === 0) openRoundIDs.value = new Set([rounds.value[0].id]) }
const load = async () => { loading.value = true; loadError.value = ''; try { const [loaded] = await Promise.all([adminAPI.intelligenceMonitor.getConfig(), loadRuntime()]); config.value = { ...defaultConfig, ...loaded, group_ids: Array.isArray(loaded?.group_ids) ? loaded.group_ids.map(Number).filter(Number.isFinite) : [], prompts: Array.isArray(loaded?.prompts) ? loaded.prompts : [], model_id: loaded?.model_id || defaultConfig.model_id }; loading.value = false; void loadResults().catch(err => { loadError.value = err instanceof Error ? err.message : '加载检测数据失败' }) } catch (err) { loadError.value = err instanceof Error ? err.message : '加载智力监控失败'; loading.value = false } }
const saveConfig = async () => { saving.value = true; loadError.value = ''; try { config.value = { ...defaultConfig, ...(await adminAPI.intelligenceMonitor.updateConfig({ ...config.value, group_ids: config.value.group_ids.map(Number) })) } } catch (err) { loadError.value = err instanceof Error ? err.message : '保存监控配置失败' } finally { saving.value = false } }
const runNow = async () => { if (runtime.value.running || running.value) return; running.value = true; loadError.value = ''; pageTab.value = 'results'; try { await adminAPI.intelligenceMonitor.runNow(); runtime.value = { running: true, total: 0, completed: 0, message: '正在创建检测批次' }; window.setTimeout(() => void loadRuntime(), 300) } catch (err) { loadError.value = err instanceof Error ? err.message : '启动智力监控失败'; runtime.value.running = false } finally { running.value = false } }
const addPrompt = () => config.value.prompts.push({ id: `custom-${Date.now()}`, title: '自定义题目', prompt: '', enabled: true }); const removePrompt = (index: number) => config.value.prompts.splice(index, 1)
onMounted(async () => { await Promise.all([load(), groupsAPI.getAll('openai').then(value => { groups.value = value }).catch(error => console.error('Failed to load OpenAI groups:', error))]); pollTimer = setInterval(() => { void loadRuntime() }, 1000) }); onUnmounted(() => { if (pollTimer) clearInterval(pollTimer) })
</script>
