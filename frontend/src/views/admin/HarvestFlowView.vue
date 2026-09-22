<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-xl font-semibold text-gray-900 dark:text-white">{{ t('admin.harvestFlow.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.harvestFlow.description') }}</p>
        </div>
        <div class="flex items-center gap-3">
          <label class="inline-flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300">
            <input v-model="autoRefresh" type="checkbox" class="rounded border-gray-300 text-primary-600" />
            {{ t('admin.harvestFlow.autoRefresh') }}
          </label>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || saving" @click="fetchFlow">
            <Icon name="refresh" size="sm" class="mr-1" :class="{ 'animate-spin': loading || refreshing }" />
            {{ t('common.refresh') }}
          </button>
        </div>
      </div>

      <div v-if="errorMessage" class="rounded-2xl bg-red-50 p-4 text-sm text-red-600 dark:bg-red-900/20 dark:text-red-400">
        {{ errorMessage }}
      </div>

      <HarvestControlsPanel :refresh-key="snapshot?.generated_at" :runtime="snapshot?.runtime" @saved="fetchFlow" />
      <HarvestManualConsole
        v-if="snapshot"
        :accounts="snapshot.accounts || []"
        :models="snapshot.harvest.models || []"
        @running="harvesting = $event"
        @finished="fetchFlow"
      />

      <div v-if="loading && !snapshot" class="flex items-center justify-center py-16">
        <LoadingSpinner />
      </div>

      <template v-else-if="snapshot">
        <div class="card overflow-hidden p-5">
          <div class="mb-5 flex flex-wrap items-center justify-between gap-3">
            <div class="flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-gray-400">
              <span class="inline-flex items-center gap-1 rounded-full px-2 py-0.5" :class="snapshot.harvest.enabled ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-500 dark:bg-dark-700'">
                {{ snapshot.harvest.enabled ? t('admin.harvestFlow.harvestOn') : t('admin.harvestFlow.harvestOff') }}
              </span>
              <span class="inline-flex items-center gap-1 rounded-full px-2 py-0.5" :class="snapshot.harvest.fail_closed ? 'bg-rose-50 text-rose-700 dark:bg-rose-900/30 dark:text-rose-300' : 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'">
                {{ snapshot.harvest.fail_closed ? t('admin.harvestFlow.failClosed') : t('admin.harvestFlow.failOpen') }}
              </span>
              <span>{{ t('admin.harvestFlow.strategy') }} {{ snapshot.harvest.strategy }}</span>
              <span>{{ t('admin.harvestFlow.scope') }} {{ scopeLabel(snapshot.harvest.scope_mode, snapshot.harvest.account_policy, snapshot.harvest.group_ids) }}</span>
              <span>{{ t('admin.harvestFlow.interval') }} {{ t('admin.harvestFlow.seconds', { n: snapshot.harvest.probe_interval_seconds }) }}</span>
              <span>{{ t('admin.harvestFlow.cooldown') }} {{ t('admin.harvestFlow.seconds', { n: snapshot.harvest.cooldown_seconds }) }}</span>
              <span>{{ t('admin.harvestFlow.targetLength', { n: snapshot.harvest.target_length }) }}</span>
              <span v-if="snapshot.harvest.models?.length">{{ t('admin.harvestFlow.models') }} {{ snapshot.harvest.models.join(' / ') }}</span>
            </div>
            <p v-if="lastUpdated" class="text-xs text-gray-400">{{ t('admin.harvestFlow.lastUpdated', { time: lastUpdated }) }}</p>
          </div>

          <ol class="grid grid-cols-1 gap-3 md:grid-cols-5">
            <li v-for="(stage, index) in snapshot.stages" :key="stage.id" class="relative">
              <div class="rounded-2xl border p-4 transition-colors" :class="stageCardClass(stage.status)">
                <div class="mb-3 flex items-center justify-between">
                  <div class="flex h-9 w-9 items-center justify-center rounded-xl" :class="stageIconClass(stage.status)">
                    <Icon :name="stageIcon(stage.id)" size="sm" />
                  </div>
                  <span class="text-[11px] font-medium uppercase tracking-wide" :class="stageTextClass(stage.status)">
                    {{ statusLabel(stage.status) }}
                  </span>
                </div>
                <p class="text-sm font-semibold text-gray-900 dark:text-white">{{ t(`admin.harvestFlow.stages.${stage.id}`) }}</p>
                <p class="mt-1 break-all font-mono text-xs text-gray-500 dark:text-gray-400">{{ stageDetail(stage) }}</p>
                <p v-if="stage.model || stage.at || stage.http_status" class="mt-2 text-[11px] text-gray-400">
                  <span v-if="stage.model">{{ stage.model }}</span>
                  <span v-if="stage.http_status"> · HTTP {{ stage.http_status }}</span>
                  <span v-if="(stage.model || stage.http_status) && stage.at"> · </span>
                  <span v-if="stage.at">{{ formatClock(stage.at) }}</span>
                </p>
              </div>
              <div v-if="index < snapshot.stages.length - 1" class="pointer-events-none absolute right-[-10px] top-1/2 hidden h-px w-5 bg-gradient-to-r from-gray-300 to-transparent md:block dark:from-dark-600" />
            </li>
          </ol>
        </div>

        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <div class="card p-4">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.harvestFlow.sidecar') }}</p>
            <p v-if="snapshot.sidecar.mode === 'external'" class="mt-1 text-sm font-semibold text-gray-600 dark:text-gray-300">
              {{ t('admin.harvestFlow.externalProxy') }}
            </p>
            <p v-else-if="snapshot.sidecar.mode === 'unconfigured'" class="mt-1 text-sm text-gray-500">{{ t('admin.harvestFlow.proxyUnconfigured') }}</p>
            <p v-else class="mt-1 text-sm font-semibold" :class="snapshot.sidecar.reachable ? 'text-emerald-600 dark:text-emerald-400' : 'text-rose-600 dark:text-rose-400'">
              {{ snapshot.sidecar.reachable ? t('admin.harvestFlow.sidecarReachable') : t('admin.harvestFlow.sidecarOffline') }}
            </p>
            <p v-if="snapshot.sidecar.mode === 'external'" class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.harvestFlow.externalProxyHint') }}</p>
            <template v-else-if="snapshot.sidecar.mode !== 'unconfigured'">
              <p class="mt-2 break-all font-mono text-xs text-gray-500 dark:text-gray-400">
                {{ snapshot.sidecar.now || (snapshot.sidecar.reachable ? t('admin.harvestFlow.poolOnline', { n: snapshot.sidecar.all_count || 0 }) : t('admin.harvestFlow.waitingSidecar')) }}
              </p>
              <p class="mt-1 text-xs text-gray-400">{{ t('admin.harvestFlow.nodePool') }} {{ snapshot.sidecar.all_count || 0 }} · {{ snapshot.sidecar.group || 'CODEX-ROTATE' }}</p>
              <p v-if="snapshot.sidecar.error" class="mt-1 text-xs text-rose-500">{{ snapshot.sidecar.error }}</p>
            </template>
          </div>
          <div class="card p-4">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.harvestFlow.ready') }}</p>
            <p class="mt-1 text-2xl font-bold text-gray-900 dark:text-white">{{ snapshot.counts.tickets_ready }}</p>
            <p class="text-xs text-rose-500">{{ t('admin.harvestFlow.paused') }} {{ snapshot.counts.tickets_blocked }}</p>
          </div>
          <div class="card p-4">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.harvestFlow.probeHit') }} / {{ t('admin.harvestFlow.probeMiss') }}</p>
            <p class="mt-1 text-2xl font-bold text-gray-900 dark:text-white">{{ snapshot.counts.probe_hit }} / {{ snapshot.counts.probe_miss }}</p>
            <p class="text-xs text-gray-400">{{ t('admin.harvestFlow.ticketAccept') }} {{ snapshot.counts.ticket_accept }} · {{ t('admin.harvestFlow.ticketReject') }} {{ snapshot.counts.ticket_reject }}</p>
          </div>
          <div class="card p-4">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.harvestFlow.selectOk') }}</p>
            <p class="mt-1 text-2xl font-bold text-gray-900 dark:text-white">{{ snapshot.counts.select_ok }}</p>
            <p class="text-xs text-gray-400">{{ t('admin.harvestFlow.selectSkip') }} {{ snapshot.counts.select_skip }} · {{ t('admin.harvestFlow.selectFail') }} {{ snapshot.counts.select_fail }}</p>
            <p v-if="snapshot.harvest.harvest_proxy" class="mt-2 truncate font-mono text-[11px] text-gray-400">{{ snapshot.harvest.harvest_proxy }}</p>
          </div>
        </div>

        <div class="grid grid-cols-1 gap-6 xl:grid-cols-5">
          <div class="card p-5 xl:col-span-2">
            <h2 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.harvestFlow.accounts') }}</h2>
            <div v-if="!snapshot.accounts?.length" class="text-sm text-gray-500">{{ t('admin.harvestFlow.noAccounts') }}</div>
            <div v-else class="grid grid-cols-1 gap-3">
              <div
                v-for="account in snapshot.accounts"
                :key="account.id"
                data-test="harvest-account-card"
                class="flex min-h-[13.75rem] flex-col rounded-2xl border border-gray-100 p-4 dark:border-dark-700"
              >
                <div class="flex items-start justify-between gap-2">
                  <div class="min-w-0">
                    <p class="font-medium text-gray-900 dark:text-white">{{ account.name || `#${account.id}` }}</p>
                    <p class="text-xs text-gray-400">
                      {{ account.status }} · {{ account.schedulable ? t('admin.harvestFlow.schedulable') : t('admin.harvestFlow.unschedulable') }}
                      <span v-if="account.skip_harvest"> · {{ t('admin.harvestFlow.skipHarvestBadge') }}</span>
                      <span v-else-if="account.in_scope"> · {{ t('admin.harvestFlow.harvestAccountBadge') }}</span>
                      <span
                        class="ml-1 whitespace-nowrap rounded-full border px-1.5 py-0.5 text-[10px] font-semibold"
                        :class="harvestAvailabilityClass(resolveHarvestAvailability(account))"
                      >{{ t(`admin.harvestFlow.availability.${resolveHarvestAvailability(account)}`) }}</span>
                    </p>
                  </div>
                  <div class="flex shrink-0 items-center gap-2">
                    <button
                      type="button"
                      class="btn btn-secondary btn-sm min-w-[5.5rem]"
                      :disabled="saving"
                      @click="toggleSkipHarvest(account)"
                    >
                      {{ account.skip_harvest ? t('admin.harvestFlow.enableHarvest') : t('admin.harvestFlow.skipHarvest') }}
                    </button>
                    <span class="w-8 text-right text-xs text-gray-400">{{ account.ready_count }}/{{ account.tickets?.length ?? 0 }}</span>
                  </div>
                </div>
                <p data-test="harvest-account-hint" class="mt-1 min-h-[2.5rem] text-[11px] leading-5 text-gray-500 dark:text-gray-400">
                  <span v-if="account.skip_harvest">{{ t('admin.harvestFlow.skipHarvestHint') }}</span>
                  <span v-else-if="recoverText(account)">{{ recoverText(account) }}</span>
                </p>
                <div class="mt-auto grid grid-cols-2 gap-2">
                  <span
                    v-for="ticket in account.tickets"
                    :key="ticket.model"
                    data-test="harvest-ticket-chip"
                    class="flex min-h-[5.75rem] flex-col rounded-2xl px-2.5 py-1.5 text-xs"
                    :class="ticket.blocked ? 'bg-rose-50 text-rose-700 dark:bg-rose-900/30 dark:text-rose-300' : ticket.ready ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'"
                  >
                    <span class="font-medium">{{ ticket.model }} · {{ ticketStatusLine(account, ticket) }}</span>
                    <span class="mt-auto block min-h-[1rem] font-mono text-[11px] opacity-80">{{ ticket.length ? `${ticket.length}B` : '—' }}</span>
                    <span class="block min-h-[1rem] text-[11px] opacity-80">{{ ticketCookieLine(ticket) }}</span>
                    <span class="block min-h-[1rem] text-[11px] opacity-80">{{ ticketProbeLine(ticket) }}</span>
                  </span>
                </div>
              </div>
            </div>
          </div>

          <div class="card min-w-0 p-5 xl:col-span-3">
            <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
              <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.harvestFlow.events') }}</h2>
              <div class="flex flex-wrap gap-1">
                <button
                  v-for="filter in eventFilters"
                  :key="filter"
                  type="button"
                  class="rounded-full px-2.5 py-1 text-[11px]"
                  :class="eventFilter === filter ? 'bg-primary-600 text-white' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'"
                  @click="eventFilter = filter"
                >
                  {{ tabLabel(filter) }}
                </button>
              </div>
            </div>
            <HarvestNodeRecords
              v-if="eventFilter === 'nodes'"
              :refresh-key="snapshot?.generated_at"
              :account-names="accountNames"
            />
            <div v-else-if="!snapshot.events?.length" class="text-sm text-gray-500">{{ t('admin.harvestFlow.noEvents') }}</div>
            <ol v-else class="max-h-[560px] space-y-2 overflow-auto pr-1">
              <li
                v-for="event in filteredEvents"
                :key="event.id"
                class="flex gap-3 rounded-xl border border-gray-100 p-3 dark:border-dark-700"
              >
                <span class="mt-1 h-2.5 w-2.5 flex-shrink-0 rounded-full" :class="eventDotClass(event.kind)" />
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center justify-between gap-2">
                    <p class="text-sm font-medium text-gray-900 dark:text-white">
                      {{ kindLabel(event.kind) }}
                      <span v-if="event.model" class="ml-1 font-mono text-xs text-gray-500">{{ event.model }}</span>
                    </p>
                    <span class="text-[11px] text-gray-400">{{ formatClock(event.at) }}</span>
                  </div>
                  <p class="mt-1 break-all font-mono text-xs text-gray-500 dark:text-gray-400">
                    <span v-if="event.account_name">{{ event.account_name }}</span>
                    <span v-if="event.node"> · {{ event.node }}</span>
                    <span v-if="event.length"> · {{ event.length }}/{{ event.blocks || '-' }}</span>
                    <span v-if="event.expected_length && event.length && event.length !== event.expected_length">
                      · {{ t('admin.harvestFlow.wantShape', { length: event.expected_length, blocks: event.expected_blocks || '-' }) }}
                    </span>
                    <span v-if="event.http_status"> · HTTP {{ event.http_status }}</span>
                    <span v-if="event.standby"> · {{ t('admin.harvestFlow.standby') }}</span>
                  </p>
                  <p v-if="event.reason || event.detail || event.result" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    {{ reasonLabel(event.reason) || resultLabel(event.result) || event.detail || event.result }}
                  </p>
                </div>
              </li>
            </ol>
          </div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import HarvestControlsPanel from '@/components/admin/HarvestControlsPanel.vue'
import HarvestManualConsole from '@/components/admin/HarvestManualConsole.vue'
import HarvestNodeRecords from '@/components/admin/HarvestNodeRecords.vue'
import { getCodexHarvestFlow, updateCodexSkipHarvest, type CodexHarvestFlowAccount, type CodexHarvestFlowEvent, type CodexHarvestFlowSnapshot, type CodexHarvestFlowStage, type CodexHarvestFlowTicket } from '@/api/admin/accounts'
import { harvestAvailabilityClass, harvestAvailabilityRecoverClock, resolveHarvestAvailability } from '@/utils/harvestAvailability'

const { t } = useI18n()
const snapshot = ref<CodexHarvestFlowSnapshot | null>(null)
const accountNames = computed(() => Object.fromEntries((snapshot.value?.accounts || []).map(account => [account.id, account.name])))
const loading = ref(false)
const refreshing = ref(false)
const autoRefresh = ref(true)
const errorMessage = ref('')
const lastUpdated = ref('')
const eventFilter = ref<'all' | 'node' | 'probe' | 'shape' | 'ticket' | 'select' | 'nodes'>('all')
const eventFilters = ['all', 'node', 'probe', 'shape', 'ticket', 'select', 'nodes'] as const
const saving = ref(false)
const harvesting = ref(false)
const nowMs = ref(Date.now())
let timer: number | undefined
let clock: number | undefined
let flowRequestID = 0
let disposed = false

const filteredEvents = computed(() => {
  const events = snapshot.value?.events || []
  if (eventFilter.value === 'all') return events
  if (eventFilter.value === 'shape') {
    return events.filter((event: CodexHarvestFlowEvent) => event.stage === 'probe' || event.kind === 'accept' || event.kind === 'reject')
  }
  return events.filter((event: CodexHarvestFlowEvent) => event.stage === eventFilter.value)
})

function tabLabel(filter: typeof eventFilters[number]) {
  if (filter === 'all') return t('admin.harvestFlow.filterAll')
  if (filter === 'nodes') return t('admin.harvestFlow.nodes.title')
  return t(`admin.harvestFlow.stages.${filter}`)
}

const stageIcons: Record<string, 'globe' | 'bolt' | 'beaker' | 'key' | 'user'> = {
  node: 'globe',
  probe: 'bolt',
  shape: 'beaker',
  ticket: 'key',
  select: 'user'
}

function stageIcon(id: string) {
  return stageIcons[id] || 'bolt'
}

function stageCardClass(status: string) {
  switch (status) {
    case 'ok':
      return 'border-emerald-200 bg-emerald-50/60 dark:border-emerald-900/40 dark:bg-emerald-950/20'
    case 'warn':
      return 'border-amber-200 bg-amber-50/70 dark:border-amber-900/40 dark:bg-amber-950/20'
    case 'fail':
      return 'border-rose-200 bg-rose-50/70 dark:border-rose-900/40 dark:bg-rose-950/20'
    default:
      return 'border-gray-100 bg-gray-50/80 dark:border-dark-700 dark:bg-dark-800/40'
  }
}

function stageIconClass(status: string) {
  switch (status) {
    case 'ok':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
    case 'warn':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
    case 'fail':
      return 'bg-rose-100 text-rose-700 dark:bg-rose-900/40 dark:text-rose-300'
    default:
      return 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-300'
  }
}

function stageTextClass(status: string) {
  switch (status) {
    case 'ok':
      return 'text-emerald-600 dark:text-emerald-400'
    case 'warn':
      return 'text-amber-600 dark:text-amber-400'
    case 'fail':
      return 'text-rose-600 dark:text-rose-400'
    default:
      return 'text-gray-400'
  }
}

function eventDotClass(kind: string) {
  if (kind === 'probe_hit' || kind === 'accept' || kind === 'selected' || kind === 'rotate') {
    return 'bg-emerald-500'
  }
  if (kind === 'skip' || kind === 'probe_miss') {
    return 'bg-amber-500'
  }
  return 'bg-rose-500'
}

function lookupLabel(prefix: string, value?: string) {
  if (!value) return ''
  const key = `${prefix}.${value}`
  const label = t(key)
  return label === key ? value : label
}

function statusLabel(status: string) {
  return lookupLabel('admin.harvestFlow.status', status)
}

function kindLabel(kind: string) {
  return lookupLabel('admin.harvestFlow.kinds', kind)
}

function reasonLabel(reason?: string) {
  return lookupLabel('admin.harvestFlow.reasons', reason)
}

function resultLabel(result?: string) {
  return lookupLabel('admin.harvestFlow.results', result)
}

function scopeLabel(mode?: string, policy?: string, groupIds?: number[]) {
  if (snapshot.value?.harvest.scope_error) return t('admin.harvestFlow.scopeUnavailable')
  const scope = mode === 'selected'
    ? t('admin.harvestFlow.scopeSelected', { n: groupIds?.length || 0 })
    : t('admin.harvestFlow.scopeAll')
  const accountPolicy = policy === 'prioritize_schedulable'
    ? t('admin.harvestFlow.policyPrioritize')
    : t('admin.harvestFlow.policySchedulable')
  return `${scope} · ${accountPolicy}`
}

function stageDetail(stage: CodexHarvestFlowStage) {
  switch (stage.id) {
    case 'node':
      if (snapshot.value?.sidecar.mode === 'external') return t('admin.harvestFlow.externalProxyHint')
      if (snapshot.value?.sidecar.mode === 'unconfigured') return t('admin.harvestFlow.proxyUnconfigured')
      if (snapshot.value?.sidecar.now) return snapshot.value.sidecar.now
      if (snapshot.value?.sidecar.reachable) {
        return t('admin.harvestFlow.poolOnline', { n: snapshot.value.sidecar.all_count || 0 })
      }
      return t('admin.harvestFlow.waitingSidecar')
    case 'probe':
      if (stage.status === 'idle') return t('admin.harvestFlow.idleProbe')
      return resultLabel(stage.detail) || stage.node || stage.detail || t('admin.harvestFlow.idleProbe')
    case 'shape':
      if (stage.status === 'idle') return t('admin.harvestFlow.idleShape')
      if (stage.status === 'ok') {
        return t('admin.harvestFlow.shapeOk', { length: stage.length || 0, blocks: stage.blocks || 0 })
      }
      if (stage.status === 'warn' || stage.detail === 'no ticket body') {
        return t('admin.harvestFlow.shapeNoBody')
      }
      return t('admin.harvestFlow.shapeBad', {
        length: stage.length || 0,
        blocks: stage.blocks || 0,
        expected_length: stage.expected_length || 292,
        expected_blocks: stage.expected_blocks || 10
      })
    case 'ticket':
      if (stage.status === 'idle') return t('admin.harvestFlow.idleTicket')
      if (stage.status === 'fail') {
        return snapshot.value?.counts.tickets_blocked
          ? t('admin.harvestFlow.ticketsPausedCount', { n: snapshot.value.counts.tickets_blocked })
          : t('admin.harvestFlow.ticketRejected')
      }
      if (stage.detail === 'standby stored') return t('admin.harvestFlow.ticketStandby')
      return t('admin.harvestFlow.ticketsReadyCount', { n: snapshot.value?.counts.tickets_ready ?? 0 })
    case 'select':
      if (stage.status === 'idle') return t('admin.harvestFlow.idleSelect')
      return reasonLabel(stage.detail) || resultLabel(stage.detail) || stage.detail || t('admin.harvestFlow.idleSelect')
    default:
      return stage.detail || '—'
  }
}

function formatClock(value?: string) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleTimeString()
}

function recoverText(account: CodexHarvestFlowAccount) {
  const recovered = harvestAvailabilityRecoverClock(account.recover_at)
  if (!recovered) return ''
  return recovered.date
    ? t('admin.harvestFlow.availability.recoverLater', { date: recovered.date, time: recovered.clock })
    : t('admin.harvestFlow.availability.recoverToday', { time: recovered.clock })
}

function formatRemaining(seconds: number) {
  const total = Math.max(0, Math.floor(seconds))
  const hours = Math.floor(total / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  if (hours > 0 && minutes > 0) return t('admin.harvestFlow.durationHoursMinutes', { hours, minutes })
  if (hours > 0) return t('admin.harvestFlow.durationHours', { hours })
  if (minutes > 0) return t('admin.harvestFlow.durationMinutes', { minutes })
  return t('admin.harvestFlow.durationSeconds', { seconds: total })
}

function secondsUntil(value?: string) {
  if (!value) return null
  const exp = Date.parse(value)
  if (Number.isNaN(exp)) return null
  return Math.max(0, Math.floor((exp - nowMs.value) / 1000))
}

function remainingSeconds(ticket: CodexHarvestFlowTicket) {
  return secondsUntil(ticket.expires_at) ?? Math.max(0, ticket.remaining_seconds || 0)
}

function ticketStatusLine(account: CodexHarvestFlowAccount, ticket: CodexHarvestFlowTicket) {
  if (ticket.blocked) {
    return account.in_scope ? t('admin.harvestFlow.blocked') : t('admin.harvestFlow.reasons.harvest_excluded')
  }
  if (!ticket.ready) return t('admin.harvestFlow.missing')
  const time = formatRemaining(remainingSeconds(ticket))
  if (ticket.standby) return t('admin.harvestFlow.remainingStandby', { time })
  const standby = secondsUntil(ticket.standby_expires_at)
  if (standby && standby > 0) {
    return t('admin.harvestFlow.remainingWithStandby', { time, standby: formatRemaining(standby) })
  }
  return t('admin.harvestFlow.remaining', { time })
}

function ticketProbeLine(ticket: CodexHarvestFlowTicket) {
  const probe = ticket.probe
  if (!probe?.result && !probe?.checked_at) return '\u00a0'
  if (!ticket.ready && probe?.checked_at) {
    return t('admin.harvestFlow.lastProbe', { time: formatClock(probe.checked_at) })
  }
  if (!probe?.result) return '\u00a0'
  return probe.http_status ? `${resultLabel(probe.result)} · HTTP ${probe.http_status}` : resultLabel(probe.result)
}

function ticketCookieLine(ticket: CodexHarvestFlowTicket) {
  if (!ticket.cookie_count) return t('admin.harvestFlow.cookies.none')
  const remaining = secondsUntil(ticket.cookie_expires_at)
  if (remaining === null || remaining <= 0) {
    return t('admin.harvestFlow.cookies.expired', { count: ticket.cookie_count })
  }
  return t('admin.harvestFlow.cookies.active', { count: ticket.cookie_count, time: formatRemaining(remaining) })
}

async function fetchFlow() {
  if (disposed || saving.value) return
  const requestID = ++flowRequestID
  if (snapshot.value) {
    refreshing.value = true
  } else {
    loading.value = true
  }
  errorMessage.value = ''
  try {
    const data = await getCodexHarvestFlow()
    if (disposed || requestID !== flowRequestID) return
    snapshot.value = data
    nowMs.value = Date.now()
    lastUpdated.value = new Date().toLocaleTimeString()
  } catch (error) {
    if (disposed || requestID !== flowRequestID) return
    const err = error as { message?: string }
    errorMessage.value = err?.message || String(error)
  } finally {
    if (!disposed && requestID === flowRequestID) {
      loading.value = false
      refreshing.value = false
    }
  }
}

async function toggleSkipHarvest(account: CodexHarvestFlowAccount) {
  if (disposed || saving.value) return
  saving.value = true
  // Reads started before this write must never replace the confirmed state.
  ++flowRequestID
  loading.value = false
  refreshing.value = false
  errorMessage.value = ''
  let saved = false
  try {
    const result = await updateCodexSkipHarvest(account.id, !account.skip_harvest)
    if (disposed) return
    const current = snapshot.value?.accounts?.find(item => item.id === account.id)
    if (current) current.skip_harvest = result.skip_harvest
    saved = true
  } catch (error) {
    if (disposed) return
    const err = error as { message?: string }
    errorMessage.value = err?.message || String(error)
  } finally {
    if (!disposed) saving.value = false
  }
  if (saved && !disposed) await fetchFlow()
}

onMounted(() => {
  void fetchFlow()
  timer = window.setInterval(() => {
    if (autoRefresh.value && !loading.value && !refreshing.value && !saving.value && !harvesting.value) void fetchFlow()
  }, 5000)
  clock = window.setInterval(() => {
    nowMs.value = Date.now()
  }, 1000)
})

onBeforeUnmount(() => {
  disposed = true
  ++flowRequestID
  if (timer !== undefined) window.clearInterval(timer)
  if (clock !== undefined) window.clearInterval(clock)
})
</script>
