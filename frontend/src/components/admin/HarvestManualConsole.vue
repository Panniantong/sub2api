<template>
  <section class="card overflow-visible p-5" aria-labelledby="harvest-console-title">
    <div class="mb-4 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 id="harvest-console-title" class="text-sm font-semibold text-gray-900 dark:text-white">{{ t(`${prefix}.title`) }}</h2>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t(`${prefix}.description`) }}</p>
      </div>
    </div>
    <div class="grid grid-cols-1 gap-4 lg:grid-cols-12">
      <div class="space-y-3 lg:col-span-5">
        <div ref="accountRoot">
          <label class="text-xs font-medium text-gray-700 dark:text-gray-300">{{ t(`${prefix}.account`) }}</label>
          <input
            v-model="keyword"
            data-testid="manual-account-input"
            type="text"
            :placeholder="t(`${prefix}.accountPlaceholder`)"
            class="input mt-1 w-full text-xs"
            autocomplete="off"
            :disabled="harvesting"
            @focus="open = true"
            @input="open = true"
          />
          <div v-if="open" class="relative z-20">
            <div class="absolute mt-1 w-full overflow-hidden rounded-xl border border-gray-200 bg-white shadow-xl dark:border-dark-700 dark:bg-dark-800">
              <div class="flex items-center justify-between gap-2 border-b border-gray-100 bg-gray-50 px-3 py-1.5 text-[11px] text-gray-500 dark:border-dark-700 dark:bg-dark-700/50">
                <span>{{ t(`${prefix}.accountCount`, { n: ordered.length }) }}</span>
                <span>{{ searching ? t(`${prefix}.sortSimilarity`) : t(`${prefix}.sortAvailability`) }}</span>
              </div>
              <div class="max-h-80 overflow-y-auto">
                <button
                  v-for="account in ordered"
                  :key="account.id"
                  :data-testid="`manual-account-${account.id}`"
                  type="button"
                  class="flex w-full items-center gap-2 border-b border-gray-50 px-3 py-2 text-left last:border-b-0 hover:bg-gray-50 dark:border-dark-700 dark:hover:bg-dark-700"
                  @click="selectAccount(account)"
                >
                  <span class="shrink-0 rounded border border-emerald-200 bg-emerald-50 px-1.5 py-0.5 font-mono text-[11px] font-semibold text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300">#{{ account.id }}</span>
                  <span class="min-w-0 flex-1">
                    <span class="block truncate text-xs text-gray-900 dark:text-white">{{ account.name || t(`${prefix}.unnamed`) }}</span>
                    <span class="block truncate text-[10px] text-gray-400">{{ accountMeta(account) }}</span>
                  </span>
                  <span class="flex shrink-0 flex-col items-end gap-0.5">
                    <span class="whitespace-nowrap rounded-full border px-2 py-0.5 text-[10px] font-semibold" :class="harvestAvailabilityClass(resolveHarvestAvailability(account))">
                      {{ t(`admin.harvestFlow.availability.${resolveHarvestAvailability(account)}`) }}
                    </span>
                    <span v-if="recoverText(account)" class="font-mono text-[10px] text-gray-400">{{ recoverText(account) }}</span>
                  </span>
                </button>
                <div v-if="!ordered.length" class="px-3 py-6 text-center text-xs text-gray-400">{{ t(`${prefix}.noAccounts`) }}</div>
              </div>
            </div>
          </div>
        </div>

        <div>
          <label class="text-xs font-medium text-gray-700 dark:text-gray-300">{{ t(`${prefix}.models`) }}</label>
          <div class="mt-1 flex flex-wrap gap-2">
            <label
              v-for="model in models"
              :key="model"
              class="flex cursor-pointer items-center gap-1.5 rounded-xl border px-2 py-1.5 text-xs"
              :class="selectedModels.includes(model) ? 'border-emerald-300 bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40' : 'border-gray-200 text-gray-600 dark:border-dark-700 dark:text-gray-400'"
            >
              <input v-model="selectedModels" type="checkbox" :value="model" :disabled="harvesting" class="rounded text-primary-600" />
              {{ model }}
            </label>
          </div>
        </div>

        <div class="grid grid-cols-2 gap-2">
          <label class="text-xs text-gray-600 dark:text-gray-300">
            {{ t(`${prefix}.probeInterval`) }}
            <input v-model.number="form.probe_interval_seconds" data-testid="manual-probe-interval" type="number" min="1" max="300" :disabled="harvesting" class="input mt-1 w-full font-mono text-xs" />
            <span class="mt-1 block text-[10px] text-gray-400">{{ t(`${prefix}.probeIntervalHint`) }}</span>
          </label>
          <label class="text-xs text-gray-600 dark:text-gray-300">
            {{ t(`${prefix}.rateLimitCooldown`) }}
            <input v-model.number="form.rate_limit_cooldown_seconds" type="number" min="1" max="60" :disabled="harvesting" class="input mt-1 w-full font-mono text-xs" />
            <span class="mt-1 block text-[10px] text-gray-400">{{ t(`${prefix}.rateLimitCooldownHint`) }}</span>
          </label>
          <label class="text-xs text-gray-600 dark:text-gray-300">
            {{ t(`${prefix}.maxAttempts`) }}
            <input v-model.number="form.max_attempts" type="number" min="1" max="100" :disabled="harvesting" class="input mt-1 w-full font-mono text-xs" />
          </label>
          <label class="text-xs text-gray-600 dark:text-gray-300">
            {{ t(`${prefix}.nodeSwitch`) }}
            <select v-model="form.node_switch_rule" data-testid="manual-node-switch" :disabled="harvesting" class="input mt-1 w-full text-xs">
              <option v-for="rule in nodeSwitchRules" :key="rule" :value="rule">{{ t(`${prefix}.nodeSwitchRules.${rule}`) }}</option>
            </select>
          </label>
        </div>

        <label class="block text-xs text-gray-600 dark:text-gray-300">
          {{ t(`${prefix}.collectLanes`) }}
          <input v-model.number="collectLanes" data-testid="manual-collect-lanes" type="number" min="2" max="32" :disabled="harvesting" class="input mt-1 w-full font-mono text-xs" />
          <span class="mt-1 block text-[10px] text-gray-400">{{ t(`${prefix}.parallelHint`) }}</span>
        </label>

        <label class="flex items-center gap-2 text-xs text-gray-600 dark:text-gray-300">
          <input v-model="form.stop_on_success" data-testid="manual-stop-on-success" type="checkbox" :disabled="harvesting" class="rounded text-primary-600" />
          {{ t(`${prefix}.stopOnSuccess`) }}
        </label>

        <div class="flex gap-2 border-t border-dashed border-gray-200 pt-3 dark:border-dark-700">
          <button
            v-if="!harvesting"
            data-testid="manual-start"
            type="button"
            class="btn btn-primary btn-sm flex-1"
            :disabled="!selected || !selectedModels.length"
            @click="start(false)"
          >
            {{ t(`${prefix}.start`) }}
          </button>
          <button v-if="!harvesting" data-testid="manual-parallel-start" type="button" class="btn btn-secondary btn-sm flex-1" :disabled="!selected || !selectedModels.length" @click="start(true)">{{ t(`${prefix}.parallelStart`) }}</button>
          <button v-else data-testid="manual-stop" type="button" class="btn btn-secondary btn-sm flex-1" @click="stop">{{ t(`${prefix}.stop`) }}</button>
          <button data-testid="manual-clear" type="button" class="btn btn-secondary btn-sm" :disabled="harvesting" @click="logs = []">{{ t(`${prefix}.clear`) }}</button>
        </div>
      </div>

      <div class="flex min-h-[22rem] flex-col rounded-2xl border border-gray-100 bg-gray-50/40 dark:border-dark-700 dark:bg-dark-800/40 lg:col-span-7">
        <div class="flex flex-wrap justify-between gap-2 border-b border-gray-100 px-4 py-2 text-xs dark:border-dark-700">
          <div>{{ t(`${prefix}.status`) }} <strong :class="statusColor">{{ statusText }}</strong></div>
          <div>{{ t(`${prefix}.progress`) }} <span class="font-mono font-semibold">{{ progressText }}</span></div>
          <div>{{ t(`${prefix}.currentNode`) }} <span class="font-mono">{{ currentNode || '—' }}</span></div>
          <div>{{ t(`${prefix}.stored`) }} <strong class="font-mono text-emerald-600">{{ t(`${prefix}.storedCount`, { n: ticketsStored }) }}</strong></div>
        </div>
        <ol data-testid="manual-logs" class="flex max-h-[420px] flex-1 flex-col gap-1.5 overflow-y-auto p-3 font-mono text-xs text-gray-700 dark:text-gray-300">
          <li v-for="(log, index) in logs" :key="`${log.time}-${index}`" class="flex items-start gap-2">
            <span class="shrink-0 pt-0.5 text-[10px] text-gray-400">{{ log.time }}</span>
            <span class="shrink-0 rounded px-1 py-0.5 text-[10px] font-semibold uppercase" :class="logClass(log.level)">{{ log.level }}</span>
            <div class="min-w-0 flex-1">
              <div class="break-all leading-snug">{{ log.message }}</div>
              <div v-if="log.detail" class="mt-0.5 break-all text-[10px] text-gray-400">{{ log.detail }}</div>
            </div>
          </li>
          <li v-if="!logs.length" class="py-8 text-center italic text-gray-400">{{ t(`${prefix}.empty`) }}</li>
        </ol>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { streamManualCodexHarvest, type CodexHarvestFlowAccount, type ManualHarvestProgress } from '@/api/admin/accounts'
import {
  harvestAccountLabel,
  harvestAvailabilityClass,
  harvestAvailabilityRecoverClock,
  orderHarvestAccounts,
  resolveHarvestAvailability
} from '@/utils/harvestAvailability'

const props = defineProps<{ accounts: CodexHarvestFlowAccount[]; models: string[] }>()
const emit = defineEmits<{ running: [boolean]; finished: [] }>()
const { t } = useI18n()
const prefix = 'admin.harvestFlow.console'
const nodeSwitchRules = ['312_or_2fail', 'every_request', '312_only', 'never'] as const
const accountRoot = ref<HTMLElement | null>(null)
const keyword = ref('')
const open = ref(false)
const selected = ref<CodexHarvestFlowAccount | null>(null)
const selectedModels = ref<string[]>([])
const harvesting = ref(false)
const collectLanes = ref(10)
const statusText = ref(t(`${prefix}.idle`))
const statusColor = ref('text-gray-400')
const progressText = ref('0 / 20')
const currentNode = ref('')
const ticketsStored = ref(0)
const logs = ref<Array<{ time: string; level: string; message: string; detail?: string }>>([])
const form = ref({
  probe_interval_seconds: 10,
  rate_limit_cooldown_seconds: 30,
  max_attempts: 20,
  node_switch_rule: '312_or_2fail',
  stop_on_success: true
})
let abort: AbortController | null = null
let disposed = false

const ordered = computed(() => orderHarvestAccounts(props.accounts || [], keyword.value, selected.value))
const searching = computed(() => {
  const raw = keyword.value.trim()
  return !!raw && !(selected.value && raw === harvestAccountLabel(selected.value))
})
const models = computed(() => props.models?.length ? props.models : selectedModels.value)

watch(() => props.models, (next) => {
  if (!next?.length) return
  if (!selectedModels.value.length) selectedModels.value = [...next]
}, { immediate: true })

watch(() => props.accounts, (list) => {
  if (!selected.value) return
  const next = (list || []).find(item => item.id === selected.value?.id) || null
  selected.value = next
  if (next) keyword.value = harvestAccountLabel(next)
})

function accountMeta(account: CodexHarvestFlowAccount) {
  const tickets = t(`${prefix}.tickets`, { ready: account.ready_count || 0, total: account.tickets?.length ?? 0 })
  const kind = resolveHarvestAvailability(account)
  const reasonKey = `admin.harvestFlow.availability.reasons.${kind}`
  const reason = t(reasonKey)
  return reason === reasonKey || kind === 'available' ? tickets : `${tickets} · ${reason}`
}

function recoverText(account: CodexHarvestFlowAccount) {
  const recovered = harvestAvailabilityRecoverClock(account.recover_at)
  if (!recovered) return ''
  return recovered.date
    ? t('admin.harvestFlow.availability.recoverLater', { date: recovered.date, time: recovered.clock })
    : t('admin.harvestFlow.availability.recoverToday', { time: recovered.clock })
}

function selectAccount(account: CodexHarvestFlowAccount) {
  selected.value = account
  keyword.value = harvestAccountLabel(account)
  open.value = false
  addLog('SELECT', t(`${prefix}.selectLog`, { id: account.id, name: account.name || '' }))
}

function addLog(level: string, message: string, detail?: string) {
  logs.value.unshift({ time: new Date().toTimeString().split(' ')[0] || '', level: level.toUpperCase(), message, detail })
  if (logs.value.length > 200) logs.value.pop()
}

function logClass(level: string) {
  switch (level) {
    case 'OK': return 'bg-emerald-100 text-emerald-800 dark:bg-emerald-950/60 dark:text-emerald-300'
    case 'WARN': return 'bg-amber-100 text-amber-800 dark:bg-amber-950/60 dark:text-amber-300'
    case 'ERROR': return 'bg-rose-100 text-rose-800 dark:bg-rose-950/60 dark:text-rose-300'
    case 'SELECT': return 'bg-violet-100 text-violet-800 dark:bg-violet-950/60 dark:text-violet-300'
    case 'START': return 'bg-sky-100 text-sky-800 dark:bg-sky-950/60 dark:text-sky-300'
    case 'STOP': return 'bg-gray-200 text-gray-700 dark:bg-dark-600 dark:text-gray-300'
    default: return 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300'
  }
}

function levelFor(result?: string) {
  if (result === 'hit') return 'OK'
  if (result === 'rate_limited' || result === 'miss_degraded' || result === 'node_switch') return 'WARN'
  if (result === 'error') return 'ERROR'
  return 'INFO'
}

function applyProgress(progress: ManualHarvestProgress) {
  if (progress.attempt || progress.max_attempts) {
    progressText.value = `${progress.attempt || 0} / ${progress.max_attempts || form.value.max_attempts}`
  }
  if (progress.node) currentNode.value = progress.node
  if (typeof progress.tickets_stored === 'number') ticketsStored.value = progress.tickets_stored
  if (progress.message) addLog(progress.level || levelFor(progress.result), progress.message, progress.detail)
  if (progress.done) finish(progress.tickets_stored && progress.tickets_stored > 0 ? 'success' : 'finished')
}

function setRunning(value: boolean) {
  harvesting.value = value
  emit('running', value)
}

function finish(kind: 'success' | 'finished' | 'stopped' | 'auth') {
  setRunning(false)
  if (kind === 'success') {
    statusText.value = t(`${prefix}.success`)
    statusColor.value = 'text-emerald-500'
  } else if (kind === 'auth') {
    statusText.value = t(`${prefix}.authFailed`)
    statusColor.value = 'text-rose-500'
  } else if (kind === 'stopped') {
    statusText.value = t(`${prefix}.stopped`)
    statusColor.value = 'text-gray-400'
  } else {
    statusText.value = t(`${prefix}.finished`)
    statusColor.value = 'text-gray-400'
  }
  if (kind === 'success' || kind === 'finished') emit('finished')
}

async function start(parallel = false) {
  if (!selected.value || harvesting.value || !selectedModels.value.length) return
  abort?.abort()
  abort = new AbortController()
  setRunning(true)
  statusText.value = t(`${prefix}.running`)
  statusColor.value = 'text-amber-500'
  progressText.value = `0 / ${form.value.max_attempts}`
  ticketsStored.value = 0
  currentNode.value = ''
  addLog('START', t(`${prefix}.startLog`, { id: selected.value.id }))
  try {
    await streamManualCodexHarvest(selected.value.id, {
      collect_lanes: parallel ? collectLanes.value : 1,
      models: [...selectedModels.value],
      probe_interval_seconds: form.value.probe_interval_seconds,
      rate_limit_cooldown_seconds: form.value.rate_limit_cooldown_seconds,
      max_attempts: form.value.max_attempts,
      node_switch_rule: form.value.node_switch_rule,
      stop_on_success: form.value.stop_on_success
    }, applyProgress, abort.signal)
    if (harvesting.value && !disposed) finish('finished')
  } catch (error) {
    if (disposed) return
    const err = error as { name?: string; status?: number; message?: string }
    if (err?.name === 'AbortError' || abort?.signal.aborted) {
      addLog('STOP', t(`${prefix}.stopLog`))
      finish('stopped')
      return
    }
    if (err?.status === 401 || /HTTP 401/.test(err?.message || '')) {
      addLog('ERROR', t(`${prefix}.authLog`))
      finish('auth')
      return
    }
    addLog('ERROR', t(`${prefix}.abortLog`, { error: err?.message || String(error) }))
    finish('finished')
  }
}

function stop() {
  abort?.abort()
}

function onDocClick(event: MouseEvent) {
  if (!open.value || !accountRoot.value) return
  if (!accountRoot.value.contains(event.target as Node)) open.value = false
}

onMounted(() => {
  statusText.value = t(`${prefix}.idle`)
  document.addEventListener('mousedown', onDocClick)
})
onBeforeUnmount(() => {
  disposed = true
  abort?.abort()
  document.removeEventListener('mousedown', onDocClick)
})
</script>