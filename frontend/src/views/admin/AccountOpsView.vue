<template>
  <AppLayout>
    <div class="account-ops">
      <SmartOpsNav />
      <header class="ops-heading"><div><p class="eyebrow">{{ t('accountOps.smartTitle') }}</p><h2>{{ t('accountOps.workspaceTitle') }}</h2><p class="subtitle">{{ t('accountOps.description') }}</p></div><button class="btn btn-secondary inline-flex items-center gap-2" :disabled="loading" @click="load"><Icon name="refresh" size="sm" :class="{ 'animate-spin': loading }" />{{ t('qualityOps.refresh') }}</button></header>
      <p v-if="error" role="alert" class="error-banner">{{ error }}</p><p v-if="notice" role="status" class="success-banner">{{ notice }}</p>
      <div class="ops-columns">
        <section class="settings-card">
          <div class="section-title"><span class="icon-tile"><Icon name="bell" size="md" /></span><div><h3>{{ t('accountOps.mailAlerts') }}</h3><p>{{ t('accountOps.oneRecipient') }}</p></div></div>
          <div v-if="!draft" class="space-y-4 p-5" role="status"><span v-for="n in 5" :key="n" class="block h-8 animate-pulse rounded-lg bg-gray-100 dark:bg-dark-800" /></div>
          <form v-else class="settings-form" @submit.prevent="save">
            <fieldset :disabled="saving">
              <label class="enable-row"><span><strong>{{ t('accountOps.enabled') }}</strong><small>{{ t('accountOps.enabledHint') }}</small></span><input v-model="draft.enabled" type="checkbox" role="switch" :aria-label="t('accountOps.enabled')" /></label>
              <label class="field-label" for="account-ops-email">{{ t('accountOps.recipient') }}</label><input id="account-ops-email" v-model.trim="draft.recipient" type="email" :required="draft.enabled" maxlength="254" autocomplete="email" class="input w-full" placeholder="ops@example.com" />
              <p class="field-hint">{{ t('accountOps.recipientHint') }}</p>
              <div class="field-label">{{ t('accountOps.alertTypes') }}</div>
              <label class="kind-option"><input v-model="draft.balance_low" type="checkbox" /><span><strong>{{ t('accountOps.balance_low') }}</strong><small>{{ t('accountOps.balanceHint') }}</small></span></label>
              <label class="kind-option"><input v-model="draft.weekly_quota" type="checkbox" /><span><strong>{{ t('accountOps.weekly_quota') }}</strong><small>{{ t('accountOps.weeklyHint') }}</small></span></label>
              <label class="field-label" for="account-ops-cooldown">{{ t('accountOps.cooldown') }}</label><div class="flex items-center gap-3"><input id="account-ops-cooldown" v-model.number="draft.cooldown_minutes" type="number" min="5" max="1440" required class="input w-28" /><span class="text-sm text-gray-500">{{ t('accountOps.minutes') }}</span></div>
              <p class="field-hint">{{ t('accountOps.cooldownHint') }}</p>
              <div class="smtp-state" :class="remote?.smtp_configured ? 'smtp-ready' : 'smtp-missing'"><Icon :name="remote?.smtp_configured ? 'checkCircle' : 'exclamationCircle'" size="sm" /><span>{{ t(remote?.smtp_configured ? 'accountOps.smtpReady' : 'accountOps.smtpMissing') }}</span><a href="/admin/settings">{{ t('accountOps.mailSettings') }}<Icon name="externalLink" size="xs" /></a></div>
              <div class="settings-actions"><span>{{ dirty ? t('accountOps.unsaved') : t('accountOps.savedState') }}</span><button class="btn btn-primary" :disabled="saving || !dirty">{{ t(saving ? 'qualityOps.saving' : 'qualityOps.save') }}</button></div>
            </fieldset>
          </form>
        </section>
        <section class="events-card">
          <header class="events-heading"><div><h3>{{ t('accountOps.events') }}<span>{{ events.length }}</span></h3><p>{{ t('accountOps.eventsHint') }}</p></div><span class="observe-status" :class="remote?.config.enabled ? 'observing' : ''"><span />{{ t(remote?.config.enabled ? 'accountOps.observing' : 'accountOps.disabled') }}</span></header>
          <div class="event-toolbar"><div class="flex items-center gap-2"><Icon name="search" size="sm" class="text-gray-400" /><input v-model="query" class="min-w-0 bg-transparent text-sm outline-none" :aria-label="t('accountOps.search')" :placeholder="t('accountOps.search')" /></div><select v-model="kind" :aria-label="t('accountOps.alertTypes')"><option value="all">{{ t('accountOps.allTypes') }}</option><option value="balance_low">{{ t('accountOps.balance_low') }}</option><option value="weekly_quota">{{ t('accountOps.weekly_quota') }}</option></select></div>
          <div v-if="remote?.dropped_signals || remote?.storage_failures" role="alert" class="error-banner mx-4">{{ t('accountOps.captureIssue') }}</div>
          <div class="events-scroll" data-testid="account-events-scroll" :aria-busy="loading"><table><thead><tr><th>{{ t('qualityOps.accounts') }}</th><th>{{ t('accountOps.failureType') }}</th><th>{{ t('accountOps.lastSeen') }}</th><th>{{ t('accountOps.mailStatus') }}</th></tr></thead><tbody>
            <tr v-for="event in filteredEvents" :key="`${event.account_id}:${event.kind}`"><td><strong :title="event.account_name">{{ event.account_name }}</strong><small>#{{ event.account_id }} · {{ t('accountOps.occurrences', { n: event.occurrences }) }}</small></td><td><span class="failure-badge">{{ t(`accountOps.${event.kind}`) }}</span><small>HTTP {{ event.http_status }} · {{ t(`accountOps.signals.${event.signal}`) }}</small></td><td class="whitespace-nowrap">{{ date(event.last_seen) }}<small>{{ t('accountOps.firstSeen') }} {{ date(event.first_seen) }}</small></td><td><span class="delivery-status" :class="`delivery-${event.state}`">{{ t(`accountOps.states.${event.state}`) }}</span><small>{{ event.last_sent_at ? date(event.last_sent_at) : t('accountOps.notSent') }}</small><small v-if="event.state === 'failed'">{{ t(event.attempts < 3 ? 'accountOps.retryAt' : 'accountOps.retryStopped', { time: date(event.next_send_at) }) }}</small></td></tr>
          </tbody></table><div v-if="!events.length" class="empty-state"><Icon name="bell" size="xl" /><h4>{{ t(loading ? 'qualityOps.loading' : 'accountOps.empty') }}</h4><p>{{ t('accountOps.emptyHint') }}</p></div></div>
          <footer class="events-footer"><p>{{ t('accountOps.noRawErrors') }}</p><button v-if="hasMore" :disabled="loadingMore" @click="more">{{ t(loadingMore ? 'qualityOps.loading' : 'qualityOps.loadMore') }}</button></footer>
        </section>
      </div>
      <aside class="scope-note"><Icon name="infoCircle" size="sm" /><p>{{ t('accountOps.scopeNote') }}</p></aside>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { useAccountOpsStore } from '@/stores/accountOps'
import { useAuthStore } from '@/stores/auth'
import AppLayout from '@/components/layout/AppLayout.vue'
import SmartOpsNav from '@/components/admin/operations/SmartOpsNav.vue'
import Icon from '@/components/icons/Icon.vue'
import { getAccountOpsSettings, saveAccountOpsSettings, getAccountOpsEvents } from '@/api/admin/accountOps'
const { t } = useI18n(), auth = useAuthStore()
const { remote, draft, events, hasMore } = storeToRefs(useAccountOpsStore())
const loading = ref(false), loadingMore = ref(false), saving = ref(false)
const query = ref(''), kind = ref('all'), error = ref(''), notice = ref('')
const dirty = computed(() => !!draft.value && JSON.stringify(draft.value) !== JSON.stringify(remote.value?.config))
const filteredEvents = computed(() => events.value.filter(e => (kind.value === 'all' || e.kind === kind.value) && `${e.account_name} ${e.account_id}`.toLowerCase().includes(query.value.toLowerCase().trim())))
let version = 0, alive = true, timer: ReturnType<typeof setInterval> | undefined
const date = (value: string) => new Date(value).toLocaleString(undefined, { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
const message = (e: unknown) => (e as { message?: string })?.message || t('qualityOps.error')
async function load() {
  if (loading.value || saving.value) return
  const request = ++version; loading.value = true; error.value = ''
  try {
    await Promise.all([
      getAccountOpsSettings().then(settings => {
        if (!alive || request !== version) return
        const preserveDraft = dirty.value
        remote.value = settings; if (!preserveDraft) draft.value = { ...settings.config }
      }).catch(e => { if (alive && request === version) error.value = message(e) }),
      getAccountOpsEvents().then(page => {
        if (!alive || request !== version) return
        events.value = page.items; hasMore.value = page.has_more
      }).catch(e => { if (alive && request === version) error.value = message(e) })
    ])
  } catch (e) { if (alive && request === version) error.value = message(e) }
  finally { if (request === version) loading.value = false }
}
async function save() {
  if (!draft.value || saving.value) return
  const request = ++version; loading.value = false; saving.value = true; error.value = ''; notice.value = ''
  try {
    const config = await saveAccountOpsSettings({ ...draft.value })
    if (!alive || request !== version) return
    remote.value = { ...remote.value!, config }; draft.value = { ...config }; notice.value = t('qualityOps.saved')
  } catch (e) { if (alive && request === version) error.value = message(e) }
  finally { if (request === version) saving.value = false }
}
async function more() {
  if (loadingMore.value || loading.value) return
  const request = version; loadingMore.value = true
  try { const page = await getAccountOpsEvents(events.value.length); if (!alive || request !== version) return; const merged = new Map([...events.value, ...page.items].map(e => [`${e.account_id}:${e.kind}`, e])); events.value = [...merged.values()]; hasMore.value = page.has_more }
  catch (e) { if (alive) error.value = message(e) }
  finally { loadingMore.value = false }
}
watch(() => auth.user ? `${auth.user.id}:${auth.user.role}` : '', () => { version++; loading.value = loadingMore.value = saving.value = false; error.value = notice.value = '' }, { flush: 'sync' })
onMounted(() => { void load(); timer = setInterval(() => { if (document.visibilityState === 'visible' && !loadingMore.value) void load() }, 30_000) })
onBeforeUnmount(() => { alive = false; version++; if (timer) clearInterval(timer) })
</script>
<style scoped>
.account-ops { max-width: 1660px; margin: auto; @apply text-gray-900 dark:text-gray-100; }
.ops-heading { @apply mb-6 flex flex-wrap items-center justify-between gap-4; }
.eyebrow { @apply mb-1 text-[11px] font-semibold tracking-widest text-primary-600; }
.ops-heading h2 { @apply text-2xl font-semibold tracking-tight; }
.subtitle { @apply mt-2 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400; }
.ops-columns { display:grid; grid-template-columns: minmax(300px,.3fr) minmax(0,.7fr); gap:20px; }
.settings-card,.events-card { @apply min-w-0 overflow-hidden rounded-2xl border border-gray-200/80 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900; }
.section-title { @apply flex items-center gap-3 border-b border-gray-100 p-5 dark:border-dark-700; }
.icon-tile { @apply flex h-10 w-10 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-950/30; }
.section-title h3,.events-heading h3 { @apply text-base font-semibold; }
.section-title p,.events-heading p { @apply mt-1 text-xs text-gray-400; }
.settings-form { @apply p-5; }
.enable-row { @apply mb-6 flex items-center justify-between gap-4; }
.enable-row strong,.kind-option strong { @apply block text-sm font-medium; }
.enable-row small,.kind-option small { @apply mt-1 block text-xs leading-relaxed text-gray-400; }
.enable-row input { appearance:none; position:relative; width:40px; height:24px; border-radius:999px; cursor:pointer; flex-shrink:0; @apply bg-gray-200 transition-colors dark:bg-dark-600; }
.enable-row input::before { content:''; position:absolute; width:18px; height:18px; border-radius:50%; top:3px; left:3px; background:white; transition:transform .15s; box-shadow:0 1px 3px #0002; }
.enable-row input:checked { @apply bg-primary-600; }
.enable-row input:checked::before { transform:translateX(16px); }
.kind-option input { accent-color:#0d9488; }
.kind-option:has(input:checked) { @apply border-primary-200 bg-primary-50/30 dark:border-primary-800 dark:bg-primary-950/20; }
.field-label { @apply mb-2 mt-5 block text-xs font-medium text-gray-600 dark:text-gray-300; }
.field-hint { @apply mt-2 text-xs leading-relaxed text-gray-400; }
.kind-option { @apply mb-2 flex items-start gap-3 rounded-xl border border-gray-100 p-3 dark:border-dark-700; }
.kind-option input { @apply mt-1 rounded text-primary-600; }
.smtp-state { @apply mt-5 flex flex-wrap items-center gap-2 rounded-lg p-3 text-xs; }
.smtp-ready { @apply bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300; }
.smtp-missing { @apply bg-amber-50 text-amber-700 dark:bg-amber-950/30 dark:text-amber-300; }
.smtp-state a { @apply ml-auto inline-flex items-center gap-1 underline; }
.settings-actions { @apply mt-5 flex items-center justify-between border-t border-gray-100 pt-4 dark:border-dark-700; }
.settings-actions span { @apply text-xs text-gray-400; }
.events-card { height:clamp(34rem,calc(100dvh - 19rem),58rem); @apply flex flex-col; }
.events-heading { @apply flex flex-wrap items-start justify-between gap-3 p-5; }
.events-heading h3 span { @apply ml-2 rounded-md bg-gray-100 px-2 py-0.5 text-xs font-normal tabular-nums text-gray-500 dark:bg-dark-800; }
.observe-status { @apply inline-flex items-center gap-1.5 rounded-full bg-gray-100 px-2 py-1 text-[11px] text-gray-500 dark:bg-dark-800; }
.observe-status span { @apply h-1.5 w-1.5 rounded-full bg-current; }
.observing { @apply bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300; }
.event-toolbar { @apply flex flex-wrap items-center justify-between gap-3 border-y border-gray-100 bg-gray-50/60 px-5 py-3 dark:border-dark-700 dark:bg-dark-800/50; }
.event-toolbar select { @apply rounded-lg border border-gray-200 bg-white px-2 py-1.5 text-xs dark:border-dark-600 dark:bg-dark-900; }
.events-scroll { @apply min-h-0 flex-1 overflow-auto overscroll-contain; scrollbar-gutter:stable; }
table { @apply w-full text-left text-xs; min-width:650px; }
thead { @apply sticky top-0 z-10 bg-white text-gray-400 dark:bg-dark-900; }
th { @apply whitespace-nowrap px-4 py-3 font-medium; }
td { @apply border-b border-gray-100 px-4 py-4 dark:border-dark-800; }
td strong { @apply block truncate text-[13px] font-medium; max-width:190px; }
td small { @apply mt-1.5 block text-[10px] text-gray-400; }
tbody tr:hover { @apply bg-gray-50/70 dark:bg-dark-800/50; }
.failure-badge { @apply whitespace-nowrap rounded-md bg-amber-50 px-2 py-1 text-[11px] font-medium text-amber-700 dark:bg-amber-950/30 dark:text-amber-300; }
.delivery-status { @apply whitespace-nowrap text-xs text-gray-500; }
.delivery-sent { @apply text-emerald-600; }.delivery-failed { @apply text-red-500; }.delivery-sending,.delivery-pending { @apply text-primary-600; }
.events-footer { @apply flex shrink-0 items-center justify-between gap-3 border-t border-gray-100 px-5 py-4 text-[11px] text-gray-400 dark:border-dark-700; }
.events-footer button { @apply shrink-0 text-primary-600; }
.empty-state { @apply flex min-h-72 flex-col items-center justify-center gap-3 p-6 text-center text-gray-400; }
.empty-state h4 { @apply text-sm font-medium; }.empty-state p { @apply max-w-sm text-xs leading-relaxed; }
.scope-note { @apply mt-5 flex items-start gap-2 text-xs leading-6 text-gray-400; }.scope-note svg { @apply mt-1 shrink-0; }
.error-banner { @apply mb-4 rounded-xl border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900 dark:bg-red-950/30 dark:text-red-300; }
.success-banner { @apply mb-4 rounded-xl bg-emerald-50 p-3 text-sm text-emerald-700 dark:bg-emerald-950/30 dark:text-emerald-300; }
button:disabled { @apply cursor-not-allowed opacity-40; }
@media(max-width:1100px) { .ops-columns {grid-template-columns:1fr;} .events-card {height:36rem;} }
</style>
