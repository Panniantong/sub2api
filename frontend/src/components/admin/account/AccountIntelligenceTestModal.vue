<template>
  <BaseDialog :show="show" title="智力测试" width="extra-wide" @close="handleClose">
    <div class="space-y-4">
      <div class="rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-sm dark:border-dark-600 dark:bg-dark-800">
        <span class="font-medium">账号：</span>{{ account?.name || '-' }}
        <span class="ml-4 font-medium">当前 Cookie Host：</span>
        <code class="break-all">{{ account?.cookie_binding?.host || '未绑定' }}</code>
      </div>
      <label class="block text-sm text-gray-600 dark:text-gray-300">测试模型
        <input v-model="modelId" class="mt-1 w-full rounded border px-3 py-2 dark:border-dark-600 dark:bg-dark-700" placeholder="例如 gpt-5.3-codex" />
      </label>
      <section v-if="htmlTest" class="rounded-lg border-2 border-primary-200 bg-primary-50/40 p-4 dark:border-primary-800 dark:bg-primary-950/20">
        <div class="flex items-center justify-between gap-3">
          <label class="flex min-w-0 items-start gap-2">
            <input v-if="htmlTest" v-model="htmlTest.selected" type="checkbox" class="mt-1 h-4 w-4 rounded border-gray-300 text-primary-600" :disabled="htmlTest.status === 'running'" />
            <div>
            <h3 class="font-semibold text-gray-900 dark:text-gray-100">{{ htmlTest.title }}</h3>
            <div class="text-xs text-gray-500">{{ statusLabel(htmlTest.status) }} · 结果预览</div>
            </div>
          </label>
          <Icon v-if="htmlTest.status === 'running'" name="refresh" size="sm" class="animate-spin text-primary-500" />
          <Icon v-else-if="htmlTest.status === 'success'" name="checkCircle" size="sm" class="text-emerald-500" />
          <Icon v-else-if="htmlTest.status === 'error'" name="xCircle" size="sm" class="text-red-500" />
        </div>
        <iframe v-if="htmlTest.html" :srcdoc="htmlTest.html" sandbox="allow-scripts" class="mt-3 h-[min(58vh,560px)] w-full rounded border border-gray-300 bg-white" title="HTML 结果预览"></iframe>
        <div v-else class="mt-3 flex h-24 items-center justify-center rounded border border-dashed border-gray-300 bg-white/70 text-sm text-gray-500 dark:border-dark-600 dark:bg-dark-800/50">等待醍醐 HTML 结果…</div>
        <details class="mt-3 rounded border border-gray-200 bg-white/70 p-2 text-xs dark:border-dark-600 dark:bg-dark-800/50">
          <summary class="cursor-pointer text-primary-600">查看醍醐原始输出</summary>
          <pre class="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words text-gray-700 dark:text-gray-200">{{ htmlTest.output || '暂无输出' }}</pre>
        </details>
        <button class="mt-3 text-xs text-primary-600 hover:underline" @click="htmlTest.expanded = !htmlTest.expanded">
          {{ htmlTest.expanded ? '收起交互日志' : `查看交互日志（${htmlTest.logs.length}）` }}
        </button>
        <div v-if="htmlTest.expanded" class="mt-2 max-h-40 overflow-auto rounded border border-gray-200 bg-white p-2 text-xs dark:border-dark-600 dark:bg-dark-900">
          <div v-for="(log, index) in htmlTest.logs" :key="index" class="border-b border-gray-100 py-1 last:border-0 dark:border-dark-700">
            <div class="text-gray-400">{{ log.time }} · {{ log.type }} · Host: {{ log.cookieHost || '未绑定' }}</div>
            <div class="break-words text-gray-700 dark:text-gray-200">{{ log.text || log.error || '' }}</div>
          </div>
        </div>
      </section>
      <div class="grid gap-4 lg:grid-cols-2">
        <section v-for="item in tests.filter(entry => entry.case !== 'html')" :key="item.case" class="flex min-h-[360px] flex-col rounded-lg border border-gray-200 dark:border-dark-600">
          <div class="flex items-center justify-between border-b border-gray-200 px-3 py-2 dark:border-dark-600">
            <label class="flex min-w-0 items-center gap-2">
              <input v-model="item.selected" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-primary-600" :disabled="item.status === 'running'" />
              <span class="min-w-0">
              <div class="font-medium text-gray-900 dark:text-gray-100">{{ item.title }}</div>
              <div class="text-xs text-gray-500">{{ statusLabel(item.status) }}</div>
              </span>
            </label>
            <Icon v-if="item.status === 'running'" name="refresh" size="sm" class="animate-spin text-primary-500" />
            <Icon v-else-if="item.status === 'success'" name="checkCircle" size="sm" class="text-emerald-500" />
            <Icon v-else-if="item.status === 'error'" name="xCircle" size="sm" class="text-red-500" />
          </div>
          <div class="flex-1 space-y-2 overflow-auto p-3">
            <label class="block text-xs text-gray-500">提示词
              <textarea v-model="item.prompt" rows="5" class="mt-1 w-full rounded border border-gray-300 px-2 py-1.5 text-xs leading-5 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-100" :disabled="item.status === 'running'" />
            </label>
            <div class="rounded bg-gray-900 p-3 font-mono text-xs leading-5 text-gray-100 whitespace-pre-wrap break-words">{{ item.output || '等待响应…' }}</div>
            <button class="text-xs text-primary-600 hover:underline" @click="item.expanded = !item.expanded">
              {{ item.expanded ? '收起交互日志' : `查看交互日志（${item.logs.length}）` }}
            </button>
            <div v-if="item.expanded" class="max-h-40 overflow-auto rounded border border-gray-200 p-2 text-xs dark:border-dark-600">
              <div v-for="(log, index) in item.logs" :key="index" class="border-b border-gray-100 py-1 last:border-0 dark:border-dark-700">
                <div class="text-gray-400">{{ log.time }} · {{ log.type }} · Host: {{ log.cookieHost || '未绑定' }}</div>
                <div class="break-words text-gray-700 dark:text-gray-200">{{ log.text || log.error || '' }}</div>
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>
    <template #footer>
      <div class="flex justify-end gap-2">
        <button class="btn btn-secondary" @click="handleClose">关闭</button>
        <button class="btn btn-primary" :disabled="running" @click="startTests">
          <Icon v-if="running" name="refresh" size="sm" class="mr-1 animate-spin" />
          {{ running ? '测试中…' : '执行已选择题目' }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch, onUnmounted } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import intelligenceTestAPI, { type IntelligenceTestResult } from '@/api/admin/intelligenceTest'
import type { Account } from '@/types'

const prompts = {
  html: '创建个新的html，用svg画一个鹈鹕骑车的动图，不要进行任何测试。',
  candy: '在一个黑色的袋子里放 有三种口味的糖果，每种糖果有两 种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已 知不同口味 的糖果和 不同形状 的数量统计如下表。参赛者需要在活动 前决定摸出 的糖果数目，那 么，最少取出多少个糖果 才能保 证手中同时拥有不同形 状的苹果味和桃子味 的糖果？（同时手中有圆形苹果味匹配五角星桃子 味糖果，或者有 圆形桃 子味匹配五角星 苹果味糖果都满 足 要求） 苹果味 桃子味 西 瓜 味 圆形 7 9 8 五角星形 7 6 4 帮我做一下 不调 用任何工具 也不调用联网',
  host_validation: "don't search the internet, do you know Thibault Sottiaux on X. answer yes or no"
} as const
type TestCase = keyof typeof prompts
type Status = 'idle' | 'running' | 'success' | 'error'
interface LogItem { time: string; type: string; text?: string; error?: string; cookieHost?: string }
interface TestItem { case: TestCase; title: string; prompt: string; selected: boolean; status: Status; output: string; html: string; logs: LogItem[]; expanded: boolean; resultID?: string }

const props = defineProps<{ show: boolean; account: Account | null }>()
const emit = defineEmits<{ (e: 'close'): void }>()
const tests = ref<TestItem[]>([])
const modelId = ref('gpt-6-astra')
const pollTimer = ref<ReturnType<typeof setInterval> | null>(null)
const running = computed(() => tests.value.some(item => item.status === 'running'))
const htmlTest = computed(() => tests.value.find(item => item.case === 'html'))
const makeTests = (): TestItem[] => [
  { case: 'html', title: '醍醐：SVG 动图', prompt: prompts.html, selected: true, status: 'idle', output: '', html: '', logs: [], expanded: false },
  { case: 'candy', title: '数糖果', prompt: prompts.candy, selected: true, status: 'idle', output: '', html: '', logs: [], expanded: false },
  { case: 'host_validation', title: 'Host 降智验证', prompt: prompts.host_validation, selected: true, status: 'idle', output: '', html: '', logs: [], expanded: false }
]
const statusLabel = (status: Status) => ({ idle: '未开始', running: '运行中', success: '完成', error: '失败' })[status]
const now = () => new Date().toLocaleString('zh-CN', { hour12: false })

watch(() => props.show, visible => {
  if (visible) {
    tests.value = makeTests()
    void loadResults()
  } else {
    stopPolling()
  }
})

const startTests = async () => {
  if (!props.account || running.value) return
  const selected = tests.value.filter(item => item.selected)
  if (!selected.length) return
  for (const item of selected) {
    item.status = 'idle'
    item.output = ''
    item.html = ''
    item.logs = []
  }
  await Promise.all(selected.map(item => startTest(item)))
  startPolling()
}

const applyResult = (item: TestItem, result: IntelligenceTestResult) => {
  item.resultID = result.id
  item.status = result.status
  item.output = result.output || result.error || ''
  item.html = result.html || ''
  item.logs = (result.events || []).map(event => ({ time: event.timestamp ? new Date(event.timestamp).toLocaleString('zh-CN', { hour12: false }) : now(), type: event.type, text: event.text, error: event.error, cookieHost: event.cookie_host }))
}
const loadResults = async () => {
  if (!props.account) return
  try {
    const results = await intelligenceTestAPI.list(props.account.id)
    const latestByCase = new Map<string, IntelligenceTestResult>()
    for (const result of results) {
      const previous = latestByCase.get(result.case)
      const resultTime = Date.parse(result.started_at || result.finished_at || '') || 0
      const previousTime = previous ? Date.parse(previous.started_at || previous.finished_at || '') || 0 : -1
      if (!previous || resultTime >= previousTime) latestByCase.set(result.case, result)
    }
    for (const item of tests.value) {
      const result = latestByCase.get(item.case)
      if (result) applyResult(item, result)
    }
    const latest = latestByCase.get('html') || latestByCase.get('candy') || latestByCase.get('host_validation')
    if (latest?.model_id) modelId.value = latest.model_id
    if (results.some(result => result.status === 'running')) startPolling()
  } catch (error) {
    console.error('Failed to load intelligence test results:', error)
  }
}
const startTest = async (item: TestItem) => {
  if (!props.account) return
  item.status = 'running'
  item.output = ''
  item.html = ''
  item.logs = []
  try {
    const result = await intelligenceTestAPI.start(props.account.id, { case: item.case, prompt: item.prompt, model_id: modelId.value.trim() || 'gpt-6-astra' })
    applyResult(item, result)
  } catch (error) {
    item.status = 'error'
    item.output = error instanceof Error ? error.message : '请求失败'
  }
}
const startPolling = () => {
  if (pollTimer.value) return
  pollTimer.value = setInterval(() => { void loadResults() }, 1000)
}
const stopPolling = () => {
  if (pollTimer.value) clearInterval(pollTimer.value)
  pollTimer.value = null
}
const handleClose = () => emit('close')
onUnmounted(stopPolling)
</script>
