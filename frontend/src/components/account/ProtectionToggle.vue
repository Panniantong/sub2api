<template>
  <button
    type="button" class="inline-flex items-center gap-1 rounded-full px-2 py-1 text-xs font-medium transition-colors disabled:opacity-50"
    :class="enabled ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-amber-50 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300'"
    :title="scopeDescription" :disabled="busy || !authStore.isAdmin" :aria-label="enabled ? '关闭账号保护' : '开启账号保护'" @click.stop="toggle"
  >
    <Icon :name="enabled ? 'shield' : 'exclamationTriangle'" size="sm" />
    {{ account.protection_scope === 'generic_v1' ? '并发保护' : '账号保护' }} {{ enabled ? '已开启' : '已关闭' }}
  </button>
  <ConfirmDialog
    :show="confirming" title="关闭账号保护？"
    message="关闭后将还原该策略管理的身份、传输或并发设置。保护策略不保证模型答题质量，测试不会自动修改此开关。"
    confirm-text="确认关闭" cancel-text="取消" danger @cancel="confirming = false" @confirm="save(false, true)"
  />
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { Account, AccountListItem } from '@/types'
import { setProtection } from '@/api/admin/accounts'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import Icon from '@/components/icons/Icon.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

const props = defineProps<{ account: AccountListItem }>()
const emit = defineEmits<{ updated: [account: Account] }>()
const authStore = useAuthStore()
const appStore = useAppStore()
const busy = ref(false)
const confirming = ref(false)
const enabled = computed(() => props.account.anti_degradation ?? (props.account.extra?.anti_degrade as { enabled?: boolean } | undefined)?.enabled === true)
const scopeDescription = computed(() => {
  if (!enabled.value) return '管理员已关闭保护；测试不会自动改变此设置'
  if (props.account.protection_scope === 'generic_v1') return '通用保护：并发上限。此账号暂不支持 Codex 专属身份与请求完整性检查，不保证上游模型质量。'
  if (props.account.protection_scope === 'codex_v3') return '兼容保护 v3：稳定设备身份、会话隔离、并发上限及请求完整性；沿用标准传输，不保证上游模型质量。'
  return '沿用已保存的保护策略；查看账号配置可了解具体能力范围'
})
function toggle() { if (enabled.value) confirming.value = true; else void save(true) }
async function save(value: boolean, confirmDisable = false) {
  if (busy.value || !authStore.isAdmin) return
  const id = props.account.id
  busy.value = true
  confirming.value = false
  try {
    const updated = await setProtection(id, value, confirmDisable)
    emit('updated', updated)
    appStore.showSuccess(value ? '账号保护已开启' : '账号保护已关闭')
  } catch (error: any) { appStore.showError(error?.message || '更新账号保护设置失败') }
  finally { busy.value = false }
}
</script>
