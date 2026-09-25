import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { useAuthStore } from './auth'
import type { AccountOpsConfig, AccountOpsEvent, AccountOpsSettings } from '@/api/admin/accountOps'
export const useAccountOpsStore = defineStore('accountOps', () => {
  const auth = useAuthStore()
  const remote = ref<AccountOpsSettings | null>(null), draft = ref<AccountOpsConfig | null>(null)
  const events = ref<AccountOpsEvent[]>([]), hasMore = ref(false)
  watch(() => auth.user ? `${auth.user.id}:${auth.user.role}` : '', () => {
    remote.value = draft.value = null; events.value = []; hasMore.value = false
  }, { flush: 'sync' })
  return { remote, draft, events, hasMore }
})
