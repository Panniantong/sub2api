import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it, vi } from 'vitest'
import { useRelayBrand } from '@/composables/useRelayBrand'

const store = vi.hoisted(() => ({ cachedPublicSettings: { site_logo: '' }, siteLogo: '' }))
vi.mock('@/stores/app', () => ({ useAppStore: () => store }))

const dir = dirname(fileURLToPath(import.meta.url))
const sidebarSource = readFileSync(resolve(dir, '../AppSidebar.vue'), 'utf8')
const keyUsageViewSource = readFileSync(resolve(dir, '../../../views/KeyUsageView.vue'), 'utf8')

describe('site_logo sanitization', () => {
  it('AppSidebar imports sanitizeUrl and applies it to siteLogo', () => {
    expect(sidebarSource).toContain("import { sanitizeUrl } from '@/utils/url'")
    expect(sidebarSource).toContain('sanitizeUrl(appStore.siteLogo')
  })

  it('home branding rejects executable logo URLs', () => {
    store.cachedPublicSettings.site_logo = 'javascript:alert(1)'
    expect(useRelayBrand().siteLogo.value).toBe('')
  })

  it('KeyUsageView applies sanitizeUrl to siteLogo', () => {
    expect(keyUsageViewSource).toContain('sanitizeUrl(appStore.cachedPublicSettings?.site_logo || appStore.siteLogo')
  })

  it('layout consumers allow relative and image data URLs', () => {
    for (const src of [sidebarSource, keyUsageViewSource]) {
      expect(src).toContain('allowRelative: true')
      expect(src).toContain('allowDataUrl: true')
    }
    for (const url of ['/logo.svg', 'data:image/png;base64,aGVsbG8=']) {
      store.cachedPublicSettings.site_logo = url
      expect(useRelayBrand().siteLogo.value).toBe(url)
    }
  })
})
