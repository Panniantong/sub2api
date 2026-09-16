import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it, vi } from 'vitest'
import { useRelayBrand } from '@/composables/useRelayBrand'

const store = vi.hoisted(() => ({ cachedPublicSettings: { doc_url: '' }, docUrl: '' }))
vi.mock('@/stores/app', () => ({ useAppStore: () => store }))

const dir = dirname(fileURLToPath(import.meta.url))
const headerSource = readFileSync(resolve(dir, '../AppHeader.vue'), 'utf8')
const keyUsageViewSource = readFileSync(resolve(dir, '../../../views/KeyUsageView.vue'), 'utf8')

describe('doc_url sanitization', () => {
  it('AppHeader imports sanitizeUrl', () => {
    expect(headerSource).toContain("import { sanitizeUrl } from '@/utils/url'")
  })

  it('AppHeader applies sanitizeUrl to docUrl', () => {
    expect(headerSource).toContain('sanitizeUrl(appStore.docUrl)')
  })

  it.each(['javascript:alert(1)', 'data:text/html,<script>alert(1)</script>', 'vbscript:msgbox(1)'])('home branding rejects executable doc URL %s', (url) => {
    store.cachedPublicSettings.doc_url = url
    expect(useRelayBrand().docUrl.value).toBe('')
  })

  it('home branding retains a valid documentation address', () => {
    store.cachedPublicSettings.doc_url = 'https://docs.example.test/guide'
    expect(useRelayBrand().docUrl.value).toBe('https://docs.example.test/guide')
  })

  it('KeyUsageView imports sanitizeUrl', () => {
    expect(keyUsageViewSource).toContain("import { sanitizeUrl } from '@/utils/url'")
  })

  it('KeyUsageView applies sanitizeUrl to docUrl', () => {
    expect(keyUsageViewSource).toContain('sanitizeUrl(appStore.cachedPublicSettings?.doc_url || appStore.docUrl')
  })
})
