<template>
  <nav class="mb-5 flex gap-1 overflow-x-auto border-b border-gray-200 dark:border-dark-700" aria-label="账号管理">
    <RouterLink v-for="item in tabs" :key="item.path" :to="item.path"
      class="whitespace-nowrap border-b-2 px-4 py-3 text-sm transition-colors"
      :class="active === item.key ? 'border-primary-500 font-semibold text-primary-700 dark:text-primary-300' : 'border-transparent text-gray-500 hover:text-primary-600 dark:text-gray-400'"
      :aria-current="active === item.key ? 'page' : undefined">{{ item.label }}</RouterLink>
  </nav>
</template>
<script setup lang="ts">
defineProps<{ active: 'accounts' | 'tests' | 'history' | 'settings' }>()
const tabs = [
  { key: 'accounts', label: '账号列表', path: '/admin/accounts' },
  { key: 'tests', label: '智能测试', path: '/admin/accounts/tests' },
  { key: 'history', label: '测试记录', path: '/admin/accounts/test-history' },
  { key: 'settings', label: '测试设置', path: '/admin/accounts/test-settings' }
]
</script>
