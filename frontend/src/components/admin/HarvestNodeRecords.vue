<template>
  <section aria-labelledby="harvest-nodes-title">
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
      <div>
        <h3 id="harvest-nodes-title" class="sr-only">{{ t(`${prefix}.title`) }}</h3>
        <p class="text-[11px] text-gray-500 dark:text-gray-400">{{ t(`${prefix}.resetHint`) }} · {{ page.total }}</p>
      </div>
      <button type="button" data-testid="reset-all-nodes" class="btn btn-secondary btn-sm" :disabled="busy || page.total === 0" @click="reset(0)">{{ t(`${prefix}.resetAll`) }}</button>
    </div>
    <p v-if="error" role="alert" class="mb-3 text-sm text-red-600">{{ error }}</p>
    <div class="max-h-[560px] space-y-2 overflow-auto pr-1">
      <article
        v-for="node in page.items"
        :key="node.id"
        class="rounded-xl border border-gray-100 p-3 dark:border-dark-700"
      >
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0">
            <p class="truncate text-sm font-medium text-gray-900 dark:text-white">{{ node.node_name }}</p>
            <p class="mt-1 truncate text-[11px] text-gray-400">
              {{ node.provider }} · #{{ node.account_id }} {{ accountNames?.[node.account_id] || '—' }} · {{ node.model }} · {{ node.blocks }}
            </p>
          </div>
          <button
            type="button"
            data-testid="reset-one-node"
            class="shrink-0 text-xs text-primary-600 hover:underline disabled:opacity-50"
            :disabled="busy"
            @click="reset(node.id)"
          >
            {{ t(`${prefix}.resetOne`) }}
          </button>
        </div>
        <div class="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-gray-500 dark:text-gray-400">
          <span><span class="text-emerald-600">{{ node.successes }}</span> / {{ node.misses }} / {{ node.network_errors }} / {{ node.account_errors }} · {{ resultLabel(node.last_result) }}</span>
          <span>{{ t(`${prefix}.lastSuccess`) }} {{ clock(node.last_success) }}</span>
          <span>{{ node.latency_ms }} ms</span>
          <span v-if="cooling(node.cooldown_until) !== '—'">{{ t(`${prefix}.cooldown`) }} {{ cooling(node.cooldown_until) }}</span>
        </div>
      </article>
      <p v-if="page.items.length === 0" class="py-8 text-center text-sm text-gray-500">{{ loading ? t(`${prefix}.loading`) : t(`${prefix}.empty`) }}</p>
    </div>
    <div class="mt-3 flex flex-wrap items-center justify-between gap-2 text-[11px] text-gray-500">
      <p>{{ t(`${prefix}.legend`) }}</p>
      <div class="flex items-center gap-2">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || offset === 0" @click="move(-1)">{{ t(`${prefix}.previous`) }}</button>
        <span>{{ Math.floor(offset / limit) + 1 }} / {{ Math.max(1, Math.ceil(page.total / limit)) }}</span>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || offset + limit >= page.total" @click="move(1)">{{ t(`${prefix}.next`) }}</button>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getCodexHarvestNodes, resetCodexHarvestNodes, type CodexHarvestNodePage } from '@/api/admin/codexHarvest'

const props = defineProps<{ refreshKey?: string; accountNames?: Record<number, string> }>()
const { t, te } = useI18n()
const prefix = 'admin.harvestFlow.nodes'
const page = ref<CodexHarvestNodePage>({ items: [], total: 0 })
const offset = ref(0)
const limit = 8
const loading = ref(false)
const resetting = ref(false)
const busy = computed(() => loading.value || resetting.value)
const error = ref('')
let requestId = 0
let disposed = false
const clock = (value: string | null) => value ? new Date(value).toLocaleString() : '—'
const cooling = (value: string | null) => value && new Date(value).getTime() > Date.now() ? clock(value) : '—'
const resultLabel = (result: string) => te(`admin.harvestFlow.results.${result}`) ? t(`admin.harvestFlow.results.${result}`) : result

async function refresh() {
  if (disposed || resetting.value) return
  const id = ++requestId
  loading.value = true
  try {
    const data = await getCodexHarvestNodes(offset.value, limit)
    if (disposed || id !== requestId) return
    page.value = data
    error.value = ''
    if (offset.value > 0 && offset.value >= data.total) { offset.value = 0; void refresh() }
  } catch {
    if (!disposed && id === requestId) error.value = t(`${prefix}.loadFailed`)
  } finally {
    if (!disposed && id === requestId) loading.value = false
  }
}

async function reset(id: number) {
  if (resetting.value) return
  ++requestId
  resetting.value = true
  loading.value = false
  error.value = ''
  try {
    await resetCodexHarvestNodes(id)
    if (disposed) return
    offset.value = 0
    page.value = { items: [], total: 0 }
  } catch {
    if (!disposed) error.value = t(`${prefix}.resetFailed`)
    return
  } finally {
    if (!disposed) resetting.value = false
  }
  if (!disposed) await refresh()
}
function move(direction: number) { offset.value = Math.max(0, offset.value + direction * limit); void refresh() }
watch(() => props.refreshKey, () => { if (!busy.value) void refresh() })
onMounted(() => { void refresh() })
onBeforeUnmount(() => { disposed = true; requestId++ })
</script>