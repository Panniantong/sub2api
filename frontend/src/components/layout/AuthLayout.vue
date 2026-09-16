<template>
  <div class="relay-public min-h-screen">
    <div class="mx-auto grid min-h-screen max-w-[1440px] lg:grid-cols-[.95fr_1.05fr]">
      <aside class="relative hidden flex-col justify-between overflow-hidden bg-[#173a2c] p-12 text-[#e4eddd] lg:flex xl:p-16">
        <router-link to="/home" aria-label="返回首页"><RelayBrand :name="siteName" :logo="siteLogo" /></router-link>
        <div class="relative z-10 py-14">
          <span class="text-[10px] font-semibold tracking-[.22em] text-[#a3c298]">A CLEARER WAY TO CONNECT.</span>
          <h2 class="mt-6 text-4xl font-semibold leading-relaxed tracking-tight">你的灵感，<br />值得顺畅抵达。</h2>
          <p class="mt-6 max-w-sm text-sm leading-7 text-[#b4c9a9]">统一的模型接入入口，清晰的用量与费用。把精力留给代码、产品，和下一个好想法。</p>
          <div class="mt-12 space-y-4 text-sm text-[#c2d5b5]">
            <p class="flex items-center gap-3"><Icon name="key" size="sm" />按项目管理 API 密钥</p>
            <p class="flex items-center gap-3"><Icon name="chart" size="sm" />查看真实调用与实际扣费</p>
            <p class="flex items-center gap-3"><Icon name="terminal" size="sm" />连接熟悉的 SDK 与客户端</p>
          </div>
        </div>
        <div class="relative z-10 border-t border-white/15 pt-6 text-xs text-[#95b089]">{{ siteSubtitle }}</div>
        <svg class="pointer-events-none absolute -bottom-10 -right-24 h-96 w-96 text-[#92bd73]/10" viewBox="0 0 300 300" fill="none" aria-hidden="true"><circle cx="150" cy="150" r="65" stroke="currentColor" stroke-width="30" /><circle cx="150" cy="150" r="125" stroke="currentColor" stroke-width="30" /></svg>
      </aside>
      <main class="flex min-w-0 flex-col justify-center px-5 py-9 sm:px-10">
        <div class="mx-auto w-full max-w-md">
          <router-link to="/home" class="mb-8 block lg:hidden" aria-label="返回首页"><RelayBrand :name="siteName" :logo="siteLogo" /></router-link>
          <router-link to="/home" class="mb-9 hidden items-center gap-2 text-xs text-gray-500 hover:text-primary-600 dark:text-dark-400 lg:inline-flex"><Icon name="arrowLeft" size="sm" />返回首页</router-link>
          <div class="rounded-2xl border border-[#dfe7d9] bg-white p-6 shadow-sm dark:border-[#304632] dark:bg-[#19271e] sm:p-8"><slot /></div>
          <div class="mt-6 text-center text-sm"><slot name="footer" /></div>
          <p class="mt-8 text-center text-xs text-gray-400 dark:text-dark-400">© {{ currentYear }} {{ siteName }} · AI Relay</p>
        </div>
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import '@/styles/relay.css'
import { onMounted } from 'vue'
import { useAppStore } from '@/stores'
import { useRelayBrand } from '@/composables/useRelayBrand'
import RelayBrand from '@/components/relay/RelayBrand.vue'
import Icon from '@/components/icons/Icon.vue'
const appStore = useAppStore()
const { siteName, siteLogo, siteSubtitle } = useRelayBrand()
const currentYear = new Date().getFullYear()
onMounted(() => { void appStore.fetchPublicSettings() })
</script>

