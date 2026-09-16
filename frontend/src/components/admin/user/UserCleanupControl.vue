<template>
  <div class="flex flex-wrap items-center gap-2">
    <button type="button" class="btn btn-secondary" :disabled="busy" @click="openPreview">
      <Icon name="trash" size="sm" class="mr-1.5" />清理无余额用户
    </button>
    <span v-if="summary" class="text-xs text-gray-500 dark:text-gray-400">可清理 {{ summary.total }} 人</span>
    <button v-else-if="summaryError" type="button" class="text-xs text-amber-600" @click="refreshSummary">统计失败 · 重试</button>
  </div>

  <BaseDialog :show="open" title="清理长期未使用的无余额用户" width="wide" :close-on-escape="!busy" :show-close-button="!busy" @close="close">
    <div class="space-y-4">
      <p v-if="loading" class="py-8 text-center text-sm text-gray-500" role="status">正在计算符合条件的用户…</p>
      <div v-else-if="result" class="rounded-xl bg-emerald-50 p-5 text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-200" role="status">
        <p class="text-lg font-semibold">已清理 {{ result.deleted_count }} 个用户</p>
        <p class="mt-2 text-sm">其中：0 余额 {{ result.zero_balance }} 人，负余额 {{ result.negative_balance }} 人。</p>
        <p v-if="result.skipped_count" class="mt-2 text-sm">{{ result.skipped_count }} 人在预览后发生变化，后端复查后已跳过。</p>
        <p class="mt-2 text-xs">用户和密钥已软删除，历史数据及操作记录保留。</p>
      </div>
      <template v-else-if="preview">
        <p class="text-sm text-gray-700 dark:text-gray-200">检测到 <strong>{{ preview.total }}</strong> 个用户符合清理条件：</p>
        <div class="rounded-xl border border-gray-200 bg-gray-50 p-4 text-sm dark:border-dark-700 dark:bg-dark-900">
          <p>余额 ≤ 0</p><p class="mt-1">最近一次使用时间超过 24 小时</p>
          <p class="mt-2 text-xs text-gray-500">后端截止时间：{{ formatDateTime(preview.cutoff) }}</p>
          <p class="mt-2 text-xs" :class="previewExpired ? 'text-amber-700' : 'text-gray-500'">{{ previewExpired ? '本次预览已过期，请重新预览并确认。' : `预览剩余 ${remainingSeconds} 秒有效` }}</p>
        </div>
        <p class="text-xs leading-6 text-gray-500 dark:text-gray-400">管理员、系统及受保护账号不会清理。注册不足 24 小时、近期登录或活跃、存在冻结余额、有效订阅或未结算任务的用户均会跳过。从未使用或使用记录缺失的账号不自动清理。</p>
        <p v-if="preview.total > preview.candidates.length" class="text-sm text-amber-700 dark:text-amber-300">每次最多清理 {{ preview.batch_limit }} 人。本次仅包含下方预览的 {{ preview.candidates.length }} 人，剩余用户需再次预览确认。</p>
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="showUsers = !showUsers">{{ showUsers ? '收起用户' : '查看用户' }}（{{ preview.candidates.length }}）</button>
        <div v-if="showUsers" class="overflow-hidden rounded-xl border border-gray-200 dark:border-dark-700">
          <div class="overflow-x-auto">
            <table class="w-full min-w-[650px] text-left text-xs">
              <thead class="bg-gray-50 text-gray-500 dark:bg-dark-900"><tr><th class="p-3">用户 ID</th><th class="p-3">用户名 / 邮箱</th><th class="p-3">余额</th><th class="p-3">最近使用</th><th class="p-3">注册时间</th><th class="p-3">保护</th></tr></thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
                <tr v-for="candidate in pageCandidates" :key="candidate.id" class="text-gray-700 dark:text-gray-300">
                  <td class="p-3">#{{ candidate.id }}</td><td class="max-w-56 p-3"><p>{{ candidate.username || '—' }}</p><p class="break-all text-gray-500">{{ candidate.email }}</p></td>
                  <td class="whitespace-nowrap p-3" :class="!candidate.zero_balance ? 'text-amber-600' : ''">{{ candidate.balance }}</td><td class="whitespace-nowrap p-3">{{ formatDateTime(candidate.last_used_at) }}</td><td class="whitespace-nowrap p-3">{{ formatDateTime(candidate.created_at) }}</td>
                  <td class="p-3"><button type="button" class="text-primary-600 hover:underline" :disabled="busy" @click="protect(candidate.id)">保留此用户</button></td>
                </tr>
              </tbody>
            </table>
          </div>
          <Pagination v-if="preview.candidates.length > pageSize" :page="page" :total="preview.candidates.length" :page-size="pageSize" :show-page-size-selector="false" @update:page="page = $event" />
        </div>
        <label v-if="preview.candidates.length" class="flex items-start gap-2 rounded-xl bg-amber-50 p-4 text-sm text-amber-900 dark:bg-amber-950/30 dark:text-amber-200">
          <input v-model="confirmed" type="checkbox" class="mt-1" :disabled="busy" data-testid="cleanup-confirm" />
          <span>我已确认本次 {{ preview.candidates.length }} 个用户，可软删除这些用户及其 API 密钥。执行前后端会重新校验条件。</span>
        </label>
      </template>
      <div v-if="error" class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-950/30 dark:text-red-300" role="alert">{{ error }}</div>
    </div>
    <template #footer>
      <div class="flex flex-wrap justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="busy" @click="close">{{ result ? '完成' : '取消' }}</button>
        <button v-if="!result" type="button" class="btn btn-secondary" :disabled="busy" @click="loadPreview">重新预览</button>
        <button v-if="!result" type="button" class="btn bg-red-600 text-white hover:bg-red-700 disabled:opacity-50" :disabled="busy || previewExpired || !confirmed || !preview?.candidates.length" @click="execute" data-testid="cleanup-execute">{{ submitting ? '正在清理…' : '确认清理' }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import { extractApiErrorMessage } from '@/utils/apiError'
import { userCleanupAPI, type UserCleanupPreview, type UserCleanupResult, type UserCleanupSummary } from '@/api/admin/userCleanup'

const emit = defineEmits<{ completed: [] }>()
const summary = ref<UserCleanupSummary | null>(null)
const summaryError = ref(false)
const open = ref(false)
const loading = ref(false)
const submitting = ref(false)
const confirmed = ref(false)
const showUsers = ref(false)
const error = ref('')
const preview = ref<UserCleanupPreview | null>(null)
const result = ref<UserCleanupResult | null>(null)
const page = ref(1)
const pageSize = 20
const now = ref(Date.now()), receivedAt = ref(Date.now())
const serverExpired = ref(false)
let clock: ReturnType<typeof setInterval>
const remainingSeconds = computed(() => {
  if (!preview.value) return 0
  const ttl = Date.parse(preview.value.expires_at) - Date.parse(preview.value.server_time)
  return Number.isFinite(ttl) ? Math.max(0, Math.ceil((ttl - (now.value - receivedAt.value)) / 1000)) : 0
})
const previewExpired = computed(() => !!preview.value && (serverExpired.value || remainingSeconds.value <= 0))
const busy = computed(() => loading.value || submitting.value)
const pageCandidates = computed(() => preview.value?.candidates.slice((page.value - 1) * pageSize, page.value * pageSize) || [])
const errorText = (value: unknown) => extractApiErrorMessage(value, '操作失败，请重试。同一预览重复提交不会重复清理。')

async function refreshSummary() {
  try { summary.value = await userCleanupAPI.summary(); summaryError.value = false }
  catch { summary.value = null; summaryError.value = true }
}

async function loadPreview() {
  if (busy.value) return
  loading.value = true; confirmed.value = false; error.value = ''; preview.value = null; page.value = 1; serverExpired.value = false
  try { preview.value = await userCleanupAPI.preview(); now.value = receivedAt.value = Date.now(); summary.value = preview.value }
  catch (e) { error.value = errorText(e) }
  finally { loading.value = false }
}

function openPreview() { open.value = true; result.value = null; showUsers.value = false; void loadPreview() }
function close() { if (!busy.value) open.value = false }

async function execute() {
  now.value = Date.now()
  if (busy.value || previewExpired.value || !confirmed.value || !preview.value?.candidates.length) return
  submitting.value = true; error.value = ''
  try { result.value = await userCleanupAPI.execute(preview.value); emit('completed'); void refreshSummary() }
  catch (e) {
    error.value = errorText(e)
    if (e && typeof e === 'object' && 'code' in e && e.code === 'USER_CLEANUP_PREVIEW_EXPIRED') { serverExpired.value = true; confirmed.value = false }
  }
  finally { submitting.value = false }
}

async function protect(userId: number) {
  if (busy.value) return
  submitting.value = true; error.value = ''
  try { await userCleanupAPI.updateGuard(userId, true) }
  catch (e) { error.value = errorText(e); return }
  finally { submitting.value = false }
  await loadPreview()
}

onMounted(() => { void refreshSummary(); clock = setInterval(() => { if (open.value) now.value = Date.now() }, 1000) })
onUnmounted(() => clearInterval(clock))
defineExpose({ refreshSummary })
</script>
