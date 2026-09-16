import { computed } from 'vue'
import { useAppStore } from '@/stores/app'
import { sanitizeUrl } from '@/utils/url'

export const RELAY_NAME = '澄川 AI'
export const RELAY_SUBTITLE = '让好模型，顺畅抵达。'

export function useRelayBrand() {
  const app = useAppStore()
  const siteName = computed(() => {
    const configured = (app.cachedPublicSettings?.site_name || app.siteName || '').trim()
    return !configured || /^sub2api$/i.test(configured) ? RELAY_NAME : configured
  })
  const siteLogo = computed(() => sanitizeUrl(app.cachedPublicSettings?.site_logo || app.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
  const siteSubtitle = computed(() => {
    const configured = app.cachedPublicSettings?.site_subtitle?.trim()
    return !configured || /^(AI API Gateway Platform|Subscription to API Conversion Platform)$/i.test(configured) ? RELAY_SUBTITLE : configured
  })
  const docUrl = computed(() => sanitizeUrl(app.cachedPublicSettings?.doc_url || app.docUrl || ''))
  return { siteName, siteLogo, siteSubtitle, docUrl }
}
