<template>
  <AppLayout>
    <div class="mx-auto max-w-3xl space-y-6">
      <div class="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <h1 class="text-2xl font-semibold text-gray-900 dark:text-white">{{ t('tickets.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('tickets.description') }}</p>
        </div>
        <button type="button" class="btn btn-primary" @click="showCreate = true">
          {{ t('tickets.newTicket') }}
        </button>
      </div>

      <div v-if="loading" class="flex items-center justify-center py-16">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
      </div>

      <div v-else-if="items.length === 0" class="rounded-lg border border-dashed border-gray-300 bg-white px-6 py-12 text-center dark:border-dark-600 dark:bg-dark-800">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('tickets.noTickets') }}</p>
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
          <p class="mt-1 text-xs text-gray-400">#{{ item.id }} · {{ formatTime(item.updated_at) }}</p>
        </div>
      </div>
    </div>

    <BaseDialog :show="showCreate" :title="t('tickets.newTicket')" width="normal" @close="showCreate = false">
      <div class="space-y-4">
        <div>
          <label class="input-label">{{ t('tickets.subject') }}</label>
          <input v-model="createForm.subject" type="text" class="input" :placeholder="t('tickets.subjectPlaceholder')" />
        </div>
        <div>
          <label class="input-label">{{ t('tickets.message') }}</label>
          <textarea v-model="createForm.body" rows="5" class="input" :placeholder="t('tickets.messagePlaceholder')"></textarea>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button type="button" class="btn btn-secondary" @click="showCreate = false">{{ t('common.cancel') }}</button>
          <button type="button" class="btn btn-primary" :disabled="saving" @click="doCreate">
            {{ saving ? t('common.saving') : t('tickets.send') }}
          </button>
        </div>
      </template>
    </BaseDialog>

    <BaseDialog :show="showDetail" :title="detail?.subject || ''" width="normal" @close="showDetail = false">
      <div v-if="detail" class="space-y-3">
        <div v-for="r in detail.replies || []" :key="r.id" class="rounded-lg p-3" :class="r.author === 'admin' ? 'bg-primary-50 dark:bg-primary-900/20' : 'bg-gray-50 dark:bg-dark-700'">
          <p class="mb-1 text-xs text-gray-400">{{ r.author === 'admin' ? t('tickets.support') : t('tickets.you') }} · {{ formatTime(r.created_at) }}</p>
          <p class="whitespace-pre-wrap text-sm text-gray-800 dark:text-gray-200">{{ r.body }}</p>
        </div>
        <div v-if="detail.status !== 'closed'" class="flex gap-2">
          <input v-model="replyText" type="text" class="input flex-1" :placeholder="t('tickets.replyPlaceholder')" @keyup.enter="doReply" />
          <button type="button" class="btn btn-primary" :disabled="saving" @click="doReply">{{ t('tickets.send') }}</button>
        </div>
        <div v-if="detail.status !== 'closed'" class="flex justify-end">
          <button type="button" class="btn btn-sm btn-secondary" @click="doClose">{{ t('tickets.closeTicket') }}</button>
        </div>
      </div>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ticketsAPI from '@/api/tickets'
import type { SupportTicket } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime as formatDateTimeValue } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()

const items = ref<SupportTicket[]>([])
const loading = ref(false)
const saving = ref(false)
const showCreate = ref(false)
const showDetail = ref(false)
const detail = ref<SupportTicket | null>(null)
const replyText = ref('')
const createForm = reactive({ subject: '', body: '' })

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
  if (s === 'closed') return t('tickets.closed')
  if (s === 'answered') return t('tickets.answered')
  return t('tickets.open')
}

const load = async () => {
  loading.value = true
  try {
    const res = await ticketsAPI.list()
    items.value = res.items || []
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('tickets.loadFailed')))
  } finally {
    loading.value = false
  }
}

const doCreate = async () => {
  if (!createForm.subject.trim() || !createForm.body.trim()) return
  saving.value = true
  try {
    await ticketsAPI.create({ subject: createForm.subject.trim(), body: createForm.body.trim() })
    appStore.showSuccess(t('tickets.createSuccess'))
    createForm.subject = ''
    createForm.body = ''
    showCreate.value = false
    await load()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('tickets.saveFailed')))
  } finally {
    saving.value = false
  }
}

const openDetail = async (id: number) => {
  try {
    detail.value = await ticketsAPI.get(id)
    replyText.value = ''
    showDetail.value = true
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('tickets.loadFailed')))
  }
}

const doReply = async () => {
  if (!detail.value || !replyText.value.trim()) return
  saving.value = true
  try {
    await ticketsAPI.reply(detail.value.id, replyText.value.trim())
    appStore.showSuccess(t('tickets.replySuccess'))
    replyText.value = ''
    detail.value = await ticketsAPI.get(detail.value.id)
    await load()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('tickets.saveFailed')))
  } finally {
    saving.value = false
  }
}

const doClose = async () => {
  if (!detail.value) return
  try {
    await ticketsAPI.close(detail.value.id)
    appStore.showSuccess(t('tickets.closeSuccess'))
    showDetail.value = false
    await load()
  } catch (error: any) {
    appStore.showError(extractApiErrorMessage(error, t('tickets.saveFailed')))
  }
}

onMounted(() => {
  load()
})
</script>
