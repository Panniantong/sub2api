<template>
  <div class="card overflow-hidden">
    <div class="border-b border-gray-100 px-5 py-5 dark:border-dark-700"><p class="text-[10px] font-semibold tracking-[.16em] text-gray-400">START / BUILD / TRACK</p><h2 class="mt-2 text-lg font-semibold text-gray-900 dark:text-white">常用入口</h2><p class="mt-1 text-xs leading-6 text-gray-500 dark:text-dark-400">接入配置、账单核对与问题反馈，一步到位。</p></div>
    <div class="divide-y divide-gray-100 px-5 dark:divide-dark-700">
      <router-link v-for="item in actions" :key="item.to" :to="item.to" class="group flex items-center gap-3 py-4">
        <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-[#eff4ea] text-[#597646] dark:bg-[#273e28] dark:text-[#bfdaa4]"><Icon :name="item.icon" size="md" /></span>
        <div class="min-w-0 flex-1"><h3 class="text-sm font-medium text-gray-900 group-hover:text-primary-600 dark:text-white">{{ item.label }}</h3><p class="mt-1 text-xs leading-5 text-gray-500 dark:text-dark-400">{{ item.description }}</p></div>
        <Icon name="arrowRight" size="sm" class="shrink-0 text-gray-400 transition-transform group-hover:translate-x-1" />
      </router-link>
    </div>
    <div class="border-t border-gray-100 bg-gray-50 px-5 py-3 text-xs leading-6 text-gray-500 dark:border-dark-700 dark:bg-dark-800/50 dark:text-dark-400">提示：每把密钥可以设置独立额度和有效期。请按项目创建密钥，便于管理与追踪。</div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { useAuthStore } from '@/stores/auth'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'

const auth = useAuthStore()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()
const actions = computed(() => [
  { to: '/keys', icon: 'key' as const, label: 'API 密钥与客户端配置', description: '创建密钥、设置额度，查看各客户端接入说明。' },
  ...(isFeatureFlagEnabled(FeatureFlags.modelPlaza) ? [{ to: '/model-plaza?embedded=1', icon: 'grid' as const, label: '模型与价格', description: '浏览当前配置的模型与计费信息。' }] : []),
  ...(auth.isSimpleMode ? [] : [
    { to: '/usage', icon: 'chart' as const, label: '调用记录', description: '核对模型、Token 和每次请求的实际费用。' },
    { to: '/billing', icon: 'creditCard' as const, label: '账单与余额', description: '查看资金明细，追踪每一笔变动。' },
    { to: '/tickets', icon: 'chat' as const, label: '问题反馈', description: '提交请求时间和错误信息，跟进处理进度。' }
  ]),
  ...(canUseBatchImage.value && !auth.isSimpleMode ? [{ to: '/batch-image', icon: 'sparkles' as const, label: '批量图片工作台', description: '使用可用图片模型处理创意任务。' }] : []),
])
onMounted(() => { void refreshBatchImageAccess() })
</script>

