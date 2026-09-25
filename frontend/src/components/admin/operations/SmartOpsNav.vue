<template>
  <nav :aria-label="t('accountOps.smartTitle')" class="mb-5 flex w-fit flex-wrap gap-1 rounded-xl border border-gray-200/70 bg-white/80 p-1 shadow-sm dark:border-dark-700 dark:bg-dark-900">
    <RouterLink v-for="item in items" :key="item.path" :to="item.path" v-slot="{ isActive }" class="rounded-lg px-4 py-2 text-sm transition-colors" :class="route.path === item.path ? 'bg-gray-900 font-medium text-white dark:bg-gray-100 dark:text-gray-900' : 'text-gray-500 hover:bg-gray-100 dark:hover:bg-dark-800'"><span :aria-current="isActive ? 'page' : undefined">{{ t(item.label) }}</span></RouterLink>
  </nav>
</template>
<script setup lang="ts">
import { RouterLink, useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
const { t } = useI18n(), route = useRoute()
const items = [{ path: '/admin/account-quality', label: 'qualityOps.title' }, { path: '/admin/account-ops', label: 'accountOps.title' }]
</script>
