<template>
  <AppLayout>
    <div class="space-y-6">
      <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">
            {{ t('admin.tickets.title') }}
            <span v-if="openCount > 0" class="ml-2 rounded-full bg-red-100 px-2 py-0.5 text-sm font-medium text-red-700 dark:bg-red-900/30 dark:text-red-300">
              {{ t('admin.tickets.openCount', { count: openCount }) }}
            </span>
          </h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.tickets.description') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <div class="w-36">
            <Select v-model="statusFilter" :options="statusOptions" @change="loadList" />
          </div>
          <input
            v-model="searchQuery"
            type="text"
            class="input w-52"
            :placeholder="t('admin.tickets.user')"
            @keyup.enter="loadList"
          />
          <button type="button" class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="loadList">
            <Icon name="refresh" size="sm" :class="loading ? 'animate-spin' : ''" />
            {{ t('common.refresh') }}
          </button>
        </div>
      </div>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else-if="items.length === 0" class="rounded-lg border border-dashed border-gray-300 bg-white px-6 py-12 text-center dark:border-dark-600 dark:bg-dark-800">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.tickets.noTickets') }}</p>
      </div>

      <div v-else class="space-y-3">
        <div
          v-for="item in items"
          :key="item.id"
          class="cursor-pointer rounded-lg border border-gray-100 bg-white p-4 shadow-sm hover:border-primary-300 dark:border-dark-700 dark:bg-dark-800"
          @click="openDetail(item.id)"
        >
          <div class="flex items-center justify-between gap-3">
            <p class="truncate font-medium text-gray-900 dark:text-white">{{ item.subject }}</p>
            <span class="inline-flex flex-shrink-0 rounded-full px-2 py-0.5 text-xs font-medium" :class="statusClass(item.status)">
              {{ statusLabel(item.status) }}
            </span>
          </div>
          <p class="mt-1 text-xs text-gray-400">#{{ item.id }} · {{ item.user_email || ('user ' + item.user_id) }} · {{ formatTime(item.updated_at) }}</p>
        </div>
      </div>

      <div v-if="hasMore" class="flex justify-center">
        <button type="button" class="btn btn-secondary" :disabled="loadingMore" @click="loadMore">
          {{ loadingMore ? t('common.loading') : t('common.loadMore') }}
        </button>
      </div>
    </div>

    <BaseDialog :show="showDetail" :title="detail?.subject || ''" width="normal" @close="showDetail = false">
      <div v-if="detail" class="space-y-3">
        <p class="text-xs text-gray-400">{{ detail.user_email || ('user ' + detail.user_id) }} · #{{ detail.id }}</p>
        <div v-for="r in detail.replies || []" :key="r.id" class="rounded-lg p-3" :class="r.author === 'admin' ? 'bg-primary-50 dark:bg-primary-900/20' : 'bg-gray-50 dark:bg-dark-700'">
          <p class="mb-1 text-xs text-gray-400">{{ r.author === 'admin' ? t('admin.tickets.support') : (detail?.user_email || t('admin.tickets.user')) }} · {{ formatTime(r.created_at) }}</p>
          <p class="whitespace-pre-wrap text-sm text-gray-800 dark:text-gray-200">{{ r.body }}</p>
        </div>
        <div v-if="detail.status !== 'closed'" class="flex gap-2">
          <input v-model="replyText" type="text" class="input flex-1" :placeholder="t('admin.tickets.replyPlaceholder')" @keyup.enter="doReply" />
          <button type="button" class="btn btn-primary" :disabled="saving" @click="doReply">{{ t('admin.tickets.sendReply') }}</button>
        </div>
        <div v-if="detail.status !== 'closed'" class="flex justify-end">
          <button type="button" class="btn btn-sm btn-secondary" @click="doClose">{{ t('admin.tickets.closeTicket') }}</button>
        </div>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import Select from '@/components/common/Select.vue'
import { adminAPI } from '@/api/admin'
import type { SelectOption, SupportTicket } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime as formatDateTimeValue } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()

const items = ref<SupportTicket[]>([])
const loading = ref(false)
const saving = ref(false)
const showDetail = ref(false)
const detail = ref<SupportTicket | null>(null)
const replyText = ref('')
const statusFilter = ref<string>('')
const searchQuery = ref('')
const openCount = ref(0)
const pageSize = 50
const hasMore = ref(false)
const loadingMore = ref(false)

const statusOptions = computed<SelectOption[]>(() => [
  { label: t('admin.tickets.all'), value: '' },
  { label: t('admin.tickets.open'), value: 'open' },
  { label: t('admin.tickets.answered'), value: 'answered' },
  { label: t('admin.tickets.closed'), value: 'closed' }
])

const formatTime = (v: string): string => {
  try {
    return formatDateTimeValue(v)
  } catch {
    return v
  }
}
const statusClass = (s: string): string => {
  if (s === 'closed') return 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400'
  if (s === 'answered') return 'bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300'
  return 'bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
}
const statusLabel = (s: string): string => {
  if (s === 'closed') return t('admin.tickets.closed')
  if (s === 'answered') return t('admin.tickets.answered')
  return t('admin.tickets.open')
}

const loadList = async () => {
  loading.value = true
  try {
    const [res, stats] = await Promise.all([
      adminAPI.tickets.list({ status: statusFilter.value || undefined, search: searchQuery.value.trim() || undefined, limit: pageSize }),
      adminAPI.tickets.stats()
    ])
    items.value = res.items || []
    hasMore.value = (res.items || []).length >= pageSize
    openCount.value = stats.open || 0
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.tickets.loadFailed')))
  } finally {
    loading.value = false
  }
}

const loadMore = async () => {
  if (loadingMore.value || !hasMore.value) return
  loadingMore.value = true
  try {
    const res = await adminAPI.tickets.list({
      status: statusFilter.value || undefined,
      search: searchQuery.value.trim() || undefined,
      limit: pageSize,
      offset: items.value.length
    })
    items.value = items.value.concat(res.items || [])
    hasMore.value = (res.items || []).length >= pageSize
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.tickets.loadFailed')))
  } finally {
    loadingMore.value = false
  }
}

const openDetail = async (id: number) => {
  try {
    detail.value = await adminAPI.tickets.get(id)
    replyText.value = ''
    showDetail.value = true
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.tickets.loadFailed')))
  }
}

const doReply = async () => {
  if (!detail.value || !replyText.value.trim()) return
  saving.value = true
  try {
    await adminAPI.tickets.reply(detail.value.id, replyText.value.trim())
    appStore.showSuccess(t('admin.tickets.replySuccess'))
    replyText.value = ''
    detail.value = await adminAPI.tickets.get(detail.value.id)
    await loadList()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.tickets.saveFailed')))
  } finally {
    saving.value = false
  }
}

const doClose = async () => {
  if (!detail.value) return
  try {
    await adminAPI.tickets.close(detail.value.id)
    appStore.showSuccess(t('admin.tickets.closeSuccess'))
    showDetail.value = false
    await loadList()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('admin.tickets.saveFailed')))
  }
}

onMounted(() => {
  loadList()
})
</script>
