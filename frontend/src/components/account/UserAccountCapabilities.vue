<template>
  <section v-if="accounts.length" class="mt-6 space-y-4" aria-label="账号能力检测">
    <div><h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">账号详情 · 能力检测</h2><p class="mt-1 text-sm text-gray-500">查看你可访问账号的已公开检测结果。单题与格式检测仅供参考，不代表模型整体能力。</p></div>
    <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
      <article v-for="account in accounts" :key="account.account_id" class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
        <h3 class="mb-4 text-sm font-semibold">{{ account.platform }} · {{ account.account_type }} #{{ account.account_id }}</h3>
        <div v-for="record in account.tests" :key="record.id" class="mt-3 flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 pt-3 text-sm dark:border-dark-700">
          <div><p>{{ testName(record.test_type) }}</p><p class="mt-1 text-xs text-gray-500">最近检测：{{ testTime(record.created_at) }}</p></div>
          <div class="flex items-center gap-3"><TestStatusBadge :status="record.status" /><button class="text-xs text-primary-600" @click="detailId = record.id">查看结果</button></div>
          <TestAssessment class="w-full" :assessment="record.evaluation" :status="record.status" compact public-view />
          <button v-if="record.test_type === 'pelican'" class="aspect-[4/3] w-full overflow-hidden rounded-xl" aria-label="查看生成的鹈鹕图像" @click="detailId = record.id"><TestGeneratedImage :source="record.result_image" :record-id="record.id" public-view /></button>
        </div>
        <button class="mt-4 text-xs text-gray-500 hover:text-primary-600" @click="openHistory(account.account_id)">查看历史检测</button>
      </article>
    </div>
    <Pagination v-if="total > 12" :page="page" :page-size="12" :total="total" :show-page-size-selector="false" @update:page="changePage" />
    <BaseDialog :show="historyAccount !== null" :title="`账号 #${historyAccount} · 检测历史`" width="wide" @close="historyAccount = null">
      <p v-if="historyError" class="text-sm text-red-600" role="alert">{{ historyError }}</p>
      <div v-for="record in history" :key="record.id" class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 py-3 text-sm dark:border-dark-700"><div><p>{{ testName(record.test_type) }}</p><p class="mt-1 text-xs text-gray-500">{{ testTime(record.created_at) }} · {{ (record.duration_ms / 1000).toFixed(1) }} 秒</p></div><div class="flex items-center gap-3"><TestStatusBadge :status="record.status" /><button class="text-primary-600" @click="historyAccount = null; detailId = record.id">查看结果</button></div></div>
      <p v-if="!history.length && !historyError" class="py-8 text-center text-sm text-gray-500">暂无可见历史</p>
      <Pagination v-if="historyTotal > 12" :page="historyPage" :page-size="12" :total="historyTotal" :show-page-size-selector="false" @update:page="loadHistory" />
    </BaseDialog>
    <TestDetailDialog :record-id="detailId" public-view @close="detailId = null" />
  </section>
</template>
<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { intelligentTestsAPI, type PublicTestAccount, type PublicTestRecord } from '@/api/intelligentTests'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import TestDetailDialog from '@/components/admin/intelligent-tests/TestDetailDialog.vue'
import TestStatusBadge from '@/components/admin/intelligent-tests/TestStatusBadge.vue'
import TestAssessment from '@/components/admin/intelligent-tests/TestAssessment.vue'
import TestGeneratedImage from '@/components/admin/intelligent-tests/TestGeneratedImage.vue'
import { testName, testTime } from '@/components/admin/intelligent-tests/display'
const accounts = ref<PublicTestAccount[]>([]), page = ref(1), total = ref(0), detailId = ref<number | null>(null)
const emit = defineEmits<{ availability: [visible: boolean] }>()
const historyAccount = ref<number | null>(null), history = ref<PublicTestRecord[]>([]), historyPage = ref(1), historyTotal = ref(0), historyError = ref('')
let timer: ReturnType<typeof setInterval>, disposed = false, version = 0, inFlight = false, historyVersion = 0, historyInFlight = false
async function load(quiet = false) {
  if (quiet && inFlight) return
  const current = ++version
  inFlight = true
  try {
    const result = await intelligentTestsAPI.publicAccounts(page.value)
    if (disposed || current !== version) return
    accounts.value = result.items; total.value = result.total
    emit('availability', accounts.value.length > 0)
    // Visibility changes remove stale public modules, including open detail dialogs.
    if (!accounts.value.length) { detailId.value = null; historyAccount.value = null }
  } catch { if (!disposed && current === version) { accounts.value = []; detailId.value = null; historyAccount.value = null; emit('availability', false) } }
  finally { if (current === version) inFlight = false }
}
function changePage(value: number) { page.value = value; void load() }
function openHistory(id: number) { historyAccount.value = id; history.value = []; void loadHistory(1) }
async function loadHistory(value: number, quiet = false) {
  const id = historyAccount.value
  if (id === null || (quiet && historyInFlight)) return
  const current = ++historyVersion
  historyInFlight = true; historyPage.value = value
  historyError.value = ''
  try {
    const data = await intelligentTestsAPI.publicTests(id, value)
    if (id !== historyAccount.value || disposed || current !== historyVersion) return
    history.value = data.items; historyPage.value = value; historyTotal.value = data.total
  } catch { if (id === historyAccount.value && !disposed && current === historyVersion) { history.value = []; historyError.value = '历史不可用或已停止公开。' } }
  finally { if (current === historyVersion) historyInFlight = false }
}
onMounted(() => { void load(); timer = setInterval(() => { if (!document.hidden) { void load(true); if (historyAccount.value !== null) void loadHistory(historyPage.value, true) } }, 5000) })
onUnmounted(() => { disposed = true; version++; historyVersion++; clearInterval(timer) })
</script>
