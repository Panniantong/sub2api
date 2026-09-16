<template>
  <div v-if="hasHomeContent" class="min-h-screen">
    <iframe v-if="isHomeContentUrl" :src="homeContent.trim()" title="站点首页" class="h-screen w-full border-0" allowfullscreen />
    <!-- Administrator-provided home content remains an explicit customization. -->
    <div v-else v-html="homeContent" />
  </div>
  <div v-else class="relay-public" :data-testid="compactHomeEnabled ? 'compact-home' : 'relay-home'">
    <a href="#main-content" class="relay-skip">跳到主要内容</a>
    <header class="relay-public-header">
      <nav class="relay-container flex items-center justify-between gap-4" aria-label="首页导航">
        <RelayBrand :name="siteName" :logo="siteLogo" />
        <div class="flex shrink-0 items-center gap-2 sm:gap-3">
          <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="relay-button relay-button-small">
            {{ isAuthenticated ? '进入工作台' : '登录控制台' }}<Icon name="arrowRight" size="sm" />
          </router-link>
          <router-link v-if="showModelPlazaEntry" to="/model-plaza" class="relay-nav-link hidden sm:inline-flex">模型与价格</router-link>
          <a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer" class="relay-nav-link hidden md:inline-flex">文档</a>
          <button type="button" class="relay-icon-button" :aria-label="isDark ? '切换浅色主题' : '切换深色主题'" @click="toggleTheme">
            <Icon :name="isDark ? 'sun' : 'moon'" size="sm" />
          </button>
          <div class="hidden sm:block"><LocaleSwitcher /></div>
        </div>
      </nav>
    </header>

    <main id="main-content" class="relay-container">
      <div v-if="settingsFailed" class="relay-settings-warning" role="status">
        站点配置暂未加载，部分入口可能尚未显示。
        <button type="button" @click="loadSettings">重新加载</button>
      </div>

      <section v-if="compactHomeEnabled" class="relay-compact">
        <img :src="siteLogo || '/relay-mark.svg'" alt="" class="mx-auto mb-6 h-20 w-20 rounded-2xl" />
        <span class="relay-eyebrow">AI RELAY, MADE CLEAR</span>
        <h1>{{ siteName }}</h1>
        <p>{{ siteSubtitle }}</p>
        <router-link :to="isAuthenticated ? dashboardPath : '/login'" class="relay-button mt-8">{{ isAuthenticated ? '打开工作台' : '开始接入' }}<Icon name="arrowRight" size="sm" /></router-link>
        <router-link v-if="showModelPlazaEntry" to="/model-plaza" class="relay-text-link mt-5">浏览模型与价格 →</router-link>
      </section>

      <template v-else>
        <section class="relay-hero">
          <div class="relay-hero-copy">
            <div class="relay-kicker"><span class="relay-kicker-line" /> YOUR AI, CONNECTED.</div>
            <h1>少一点接入折腾，<br /><span>多一点创造可能。</span></h1>
            <p class="relay-hero-subtitle">{{ siteSubtitle }}</p>
            <p class="relay-hero-description">把模型接入、API 密钥、调用记录与费用放在同一个工作台。让你的下一次灵感，从一次清晰的调用开始。</p>
            <div class="mt-8 flex flex-wrap items-center gap-3">
              <router-link :to="isAuthenticated ? dashboardPath : registrationEnabled ? '/register' : '/login'" class="relay-button">{{ isAuthenticated ? '打开我的工作台' : registrationEnabled ? '创建账户，开始接入' : '登录并开始接入' }}<Icon name="arrowRight" size="sm" /></router-link>
              <a href="#quickstart" class="relay-button relay-button-outline">查看接入示例</a>
            </div>
            <div class="relay-hero-notes"><span><Icon name="key" size="sm" />独立密钥管理</span><span><Icon name="chart" size="sm" />实际用量可查</span><span><Icon name="swap" size="sm" />兼容常用 SDK</span></div>
          </div>
          <div class="relay-flow" aria-label="接入流程示意">
            <div class="relay-flow-caption"><span>ONE CONNECTION. MORE POSSIBILITIES.</span><Icon name="externalLink" size="sm" /></div>
            <div class="relay-flow-client"><Icon name="terminal" size="lg" /><div><strong>你的应用</strong><span>代码助手 · Agent · 创意工具</span></div></div>
            <div class="relay-flow-line"><span>YOUR API KEY</span></div>
            <div class="relay-flow-hub"><img :src="siteLogo || '/relay-mark.svg'" alt="" /><div><strong>{{ siteName }}</strong><span>统一入口 / 密钥 / 用量</span></div><span class="relay-hub-tag">RELAY</span></div>
            <div class="relay-flow-branches" aria-hidden="true"><i /><i /><i /></div>
            <div class="relay-flow-models"><div>OpenAI<span>兼容协议</span></div><div>Anthropic<span>Messages API</span></div><div>Gemini<span>原生协议</span></div></div>
            <p class="relay-flow-footnote">协议接入示意。可用模型、价格与权限以账户分组为准。</p>
          </div>
        </section>

        <section class="relay-benefits" aria-label="工作台能力">
          <article v-for="item in benefits" :key="item.title"><Icon :name="item.icon" size="lg" /><div><h2>{{ item.title }}</h2><p>{{ item.description }}</p></div></article>
        </section>

        <section id="quickstart" class="relay-start-section">
          <div>
            <span class="relay-eyebrow">BUILT FOR YOUR WORKFLOW</span>
            <h2 class="relay-section-title">保留熟悉的工具，<br />换一个更清楚的入口。</h2>
            <p class="relay-section-description">在支持自定义 Base URL 的客户端中配置地址和密钥，或直接用 API 发起请求。</p>
            <ol class="relay-steps">
              <li><span>01</span><div><h3>创建一把专属密钥</h3><p>选择账户可用的分组，设置额度、有效期和访问限制。</p><router-link to="/keys">打开密钥管理 →</router-link></div></li>
              <li><span>02</span><div><h3>选择协议和可用模型</h3><p>复制示例中的地址；模型名称与分组必须匹配。</p><router-link v-if="showModelPlazaEntry" to="/model-plaza">浏览模型与价格 →</router-link></div></li>
              <li><span>03</span><div><h3>发起调用，核对用量</h3><p>从一条短请求开始，在工作台查看模型、Token 与实际扣费。</p><router-link to="/usage">查看调用记录 →</router-link></div></li>
            </ol>
          </div>
          <div class="terminal-container min-w-0"><RelayQuickStart /></div>
        </section>

        <section class="relay-faq-section">
          <div><span class="relay-eyebrow">CLEAR FROM THE START</span><h2 class="relay-section-title">接入前，你可能想知道</h2><p class="relay-section-description">把费用、权限和异常说清楚。</p></div>
          <div class="relay-faq">
            <details v-for="item in faqs" :key="item.question"><summary>{{ item.question }}<Icon name="plus" size="sm" /></summary><p>{{ item.answer }}</p></details>
          </div>
        </section>
        <section class="relay-bottom-cta"><div><span class="relay-eyebrow">READY WHEN YOU ARE</span><h2>下一次调用，从这里出发。</h2></div><router-link :to="isAuthenticated ? dashboardPath : '/login'" class="relay-button">进入控制台<Icon name="arrowRight" size="sm" /></router-link></section>
      </template>
    </main>
    <footer class="relay-public-footer"><div class="relay-container flex flex-wrap items-center justify-between gap-4"><span>© {{ currentYear }} {{ siteName }} <span class="mx-2 opacity-40">/</span> AI Relay</span><div class="flex gap-5"><router-link v-if="showModelPlazaEntry" to="/model-plaza">模型与价格</router-link><a v-if="docUrl" :href="docUrl" target="_blank" rel="noopener noreferrer">使用文档</a><a href="https://github.com/Wei-Shaw/sub2api" target="_blank" rel="noopener noreferrer">基于 Sub2API</a></div></div></footer>
  </div>
</template>

<script setup lang="ts">
import '@/styles/relay.css'
import { computed, onMounted, ref } from 'vue'
import { useAuthStore, useAppStore } from '@/stores'
import LocaleSwitcher from '@/components/common/LocaleSwitcher.vue'
import Icon from '@/components/icons/Icon.vue'
import RelayBrand from '@/components/relay/RelayBrand.vue'
import RelayQuickStart from '@/components/relay/RelayQuickStart.vue'
import { useRelayBrand } from '@/composables/useRelayBrand'
import { FeatureFlags, isFeatureFlagEnabled } from '@/utils/featureFlags'

const authStore = useAuthStore()
const appStore = useAppStore()
const { siteName, siteLogo, siteSubtitle, docUrl } = useRelayBrand()
const homeContent = computed(() => appStore.cachedPublicSettings?.home_content || '')
const hasHomeContent = computed(() => homeContent.value.trim().length > 0)
const isHomeContentUrl = computed(() => /^https?:\/\//i.test(homeContent.value.trim()))
const compactHomeEnabled = computed(() => appStore.cachedPublicSettings?.compact_home_enabled === true)
const registrationEnabled = computed(() => appStore.cachedPublicSettings?.registration_enabled === true)
const isAuthenticated = computed(() => authStore.isAuthenticated)
const dashboardPath = computed(() => authStore.isAdmin ? '/admin/dashboard' : '/dashboard')
const showModelPlazaEntry = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza) && (isAuthenticated.value || !appStore.cachedPublicSettings?.model_plaza_require_auth))
const currentYear = new Date().getFullYear()
const isDark = ref(document.documentElement.classList.contains('dark'))
const settingsFailed = ref(false)
const benefits = [
  { icon: 'key' as const, title: '权限，掌握在自己手里', description: '独立管理 API 密钥，按项目划分额度，随时停用或撤销。' },
  { icon: 'chart' as const, title: '每一笔用量，有据可查', description: '请求、模型、Token 与费用统一记录，方便核对实际消耗。' },
  { icon: 'terminal' as const, title: '从代码到工具，顺畅接入', description: '支持常用模型协议，在已有 SDK 和客户端中配置接入。' },
]
const faqs = [
  { question: '这里能使用哪些模型？', answer: '模型由运营者配置，并受账户分组、密钥权限和上游可用性影响。请以模型广场、密钥使用说明或对应协议的模型列表为准。协议支持不代表所有模型均已上线。' },
  { question: '费用如何计算？', answer: '费用取决于模型单价、实际 Token 用量、分组倍率和订阅规则。工作台会分别展示标准费用与实际扣除金额，最终以调用记录和账单为准。' },
  { question: '已有的客户端需要重新配置什么？', answer: '一般需要修改 Base URL、API Key 和模型名称。OpenAI SDK 的基础地址通常以 /v1 结尾；Anthropic SDK 使用不带 /v1 的地址。请在密钥管理中查看对应分组的接入说明。' },
  { question: '调用报错时应该先检查什么？', answer: '401 / 403：检查密钥、有效期和分组权限。400：检查模型与协议参数。429：查看限流或额度提示，等待后重试。5xx：保留时间和请求 ID，通过工单反馈。不要在工单中提交完整密钥。' },
]
function toggleTheme() {
  isDark.value = !isDark.value
  document.documentElement.classList.toggle('dark', isDark.value)
  localStorage.setItem('theme', isDark.value ? 'dark' : 'light')
}
async function loadSettings() {
  settingsFailed.value = false
  await appStore.fetchPublicSettings()
  settingsFailed.value = !appStore.publicSettingsLoaded
}
onMounted(() => {
  const saved = localStorage.getItem('theme')
  isDark.value = saved === 'dark' || (!saved && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.classList.toggle('dark', isDark.value)
  authStore.checkAuth()
  if (!appStore.publicSettingsLoaded) void loadSettings()
})
</script>

