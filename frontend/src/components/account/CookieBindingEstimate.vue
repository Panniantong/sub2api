<template>
  <span v-if="binding?.estimated_binding_seconds !== undefined" class="block text-xs text-gray-500" :title="explanation">
    预计可用绑定时长 {{ duration }}
    <span class="block text-[11px] text-gray-400">{{ formula }}</span>
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { Account } from '@/types'

const props = defineProps<{ binding: Account['cookie_binding'] }>()
const formula = computed(() => `${props.binding?.available_host_count ?? 0} Host × (${props.binding?.binding_seconds ?? 0} − ${props.binding?.rotation_before_seconds ?? 0}) 秒`)
const explanation = computed(() => `${formula.value}。按当前可绑定 Host 估算，不含当前已绑定、过期或冷却中的 Host；实际时长取决于验证结果及 Cookie 有效期。`)
const duration = computed(() => {
  const seconds = Math.max(0, Math.floor(props.binding?.estimated_binding_seconds ?? 0))
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor(seconds % 3600 / 60)
  const rest = seconds % 60
  return [hours ? `${hours}小时` : '', minutes ? `${minutes}分` : '', rest || !seconds ? `${rest}秒` : ''].filter(Boolean).join(' ')
})
</script>
