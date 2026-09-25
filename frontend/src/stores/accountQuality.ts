import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { useAuthStore } from './auth'
import { listQualityPlans, listQualityOperations, type QualityOperation } from '@/api/admin/accountQuality'
import { getAllIncludingInactive } from '@/api/admin/groups'
import type { AdminGroup, ScheduledTestPlan } from '@/types'

// Session-only summaries survive route navigation. Answer bodies stay in the detail drawer.
export const useAccountQualityStore = defineStore('accountQuality', () => {
  const auth = useAuthStore()
  const plans = ref<ScheduledTestPlan[]>([]), groups = ref<AdminGroup[]>([])
  const operations = ref<QualityOperation[]>([]), cursor = ref(0)
  const rulesLoaded = ref(false), operationsLoaded = ref(false)
  const rulesLoading = ref(false), operationsLoading = ref(false), moreLoading = ref(false)
  const rulesError = ref(''), operationsError = ref(''), groupsError = ref('')
  const updatedAt = ref(0)
  const search = ref(''), selectedPlanId = ref<number | null>(null), operationFilter = ref('all')
  let ruleVersion = 0, operationVersion = 0, groupVersion = 0
  let rulesFlight: Promise<void> | null = null, groupsFlight: Promise<void> | null = null
  const errorText = (e: unknown) => e instanceof Error ? e.message : 'qualityOps.error'

  function reset() {
    ruleVersion++; operationVersion++; groupVersion++
    plans.value = []; groups.value = []; operations.value = []; cursor.value = 0
    rulesLoaded.value = operationsLoaded.value = false
    rulesLoading.value = operationsLoading.value = moreLoading.value = false
    rulesError.value = operationsError.value = groupsError.value = ''
    search.value = ''; selectedPlanId.value = null; operationFilter.value = 'all'; updatedAt.value = 0
    rulesFlight = groupsFlight = null
  }
  watch(() => auth.user ? `${auth.user.id}:${auth.user.role}` : '', reset, { flush: 'sync' })

  function refreshRules(force = false): Promise<void> {
    if (rulesFlight && !force) return rulesFlight
    const version = ++ruleVersion
    rulesLoading.value = true; rulesError.value = ''
    const flight = (async () => {
      try {
        const data = await listQualityPlans()
        if (version !== ruleVersion) return
        plans.value = data; rulesLoaded.value = true
        if (selectedPlanId.value && !data.some(p => p.id === selectedPlanId.value)) selectedPlanId.value = null
        updatedAt.value = Date.now()
      } catch (e) { if (version === ruleVersion) rulesError.value = errorText(e) }
      finally { if (version === ruleVersion) { rulesLoading.value = false; rulesFlight = null } }
    })()
    rulesFlight = flight
    return flight
  }
  function refreshGroups(): Promise<void> {
    if (groupsFlight) return groupsFlight
    const version = ++groupVersion
    groupsError.value = ''
    const flight = (async () => {
      try { const data = await getAllIncludingInactive(); if (version === groupVersion) groups.value = data }
      catch (e) { if (version === groupVersion) groupsError.value = errorText(e) }
      finally { if (version === groupVersion) groupsFlight = null }
    })()
    groupsFlight = flight
    return flight
  }
  async function refreshOperations(append = false) {
    if (append && (moreLoading.value || operationsLoading.value || !cursor.value)) return
    const version = ++operationVersion
    if (append) moreLoading.value = true
    else { operationsLoading.value = true; moreLoading.value = false }
    operationsError.value = ''
    try {
      const page = await listQualityOperations(append ? cursor.value : 0)
      if (version !== operationVersion) return
      const unique = new Map<number, QualityOperation>()
      for (const item of [...(append ? operations.value : []), ...page.items]) unique.set(item.id, item)
      operations.value = [...unique.values()]; cursor.value = page.next_cursor
      operationsLoaded.value = true; updatedAt.value = Date.now()
    } catch (e) { if (version === operationVersion) operationsError.value = errorText(e) }
    finally { if (version === operationVersion) operationsLoading.value = moreLoading.value = false }
  }
  async function refresh() {
    await Promise.all([refreshRules(), refreshGroups(), refreshOperations()])
  }
  return { plans, groups, operations, cursor, rulesLoaded, operationsLoaded, rulesLoading, operationsLoading, moreLoading,
    rulesError, operationsError, groupsError, updatedAt, search, selectedPlanId, operationFilter,
    refresh, refreshRules, refreshGroups, refreshOperations, reset }
})
