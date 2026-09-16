import { computed } from 'vue'
import { useAppStore } from '@/stores/app'

// The configured address may already end with /v1. Keep a deployment path prefix.
export function normalizeRelayEndpoint(configured: string, origin: string): string {
  try {
    const url = new URL(configured.trim() || origin)
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) return ''
    url.search = ''
    url.hash = ''
    url.pathname = url.pathname.replace(/\/+$/, '').replace(/\/v1$/, '') + '/v1'
    return url.toString().replace(/\/$/, '')
  } catch {
    return ''
  }
}

export function useRelayEndpoint() {
  const app = useAppStore()
  const endpoint = computed(() => normalizeRelayEndpoint(app.cachedPublicSettings?.api_base_url || app.apiBaseUrl || '', window.location.origin))
  return { endpoint }
}
