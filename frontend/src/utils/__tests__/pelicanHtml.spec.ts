import { describe, expect, it } from 'vitest'
import { extractPelicanHtml } from '../pelicanHtml'

function parse(html: string) {
  return new DOMParser().parseFromString(html, 'text/html')
}

describe('extractPelicanHtml', () => {
  it('puts the CSP ahead of everything the model wrote', () => {
    const out = extractPelicanHtml(
      '<!DOCTYPE html><html lang="zh"><!-- <head> --><script>fetch("https://evil.test")</script><head><title>Pelican</title></head><body><svg></svg></body></html>'
    )
    expect(out.startsWith('<!doctype html><meta http-equiv="Content-Security-Policy"')).toBe(true)
    expect(out.match(/<!doctype/gi)).toHaveLength(1)

    const doc = parse(out)
    expect(doc.head.firstElementChild?.getAttribute('http-equiv')).toBe('Content-Security-Policy')
    expect(doc.head.firstElementChild?.getAttribute('content')).toContain("connect-src 'none'")
    expect(doc.documentElement.getAttribute('lang')).toBe('zh')
    expect(doc.title).toBe('Pelican')
    expect(doc.body.querySelector('svg')).not.toBeNull()
  })

  it('wraps bare SVG and unwraps fenced output', () => {
    const svg = parse(extractPelicanHtml('Here you go:\n```html\n<svg viewBox="0 0 10 10"></svg>\n```'))
    expect(svg.head.firstElementChild?.getAttribute('http-equiv')).toBe('Content-Security-Policy')
    expect(svg.body.querySelector('svg')?.getAttribute('viewBox')).toBe('0 0 10 10')
  })

  it('rejects output without HTML or SVG', () => {
    expect(extractPelicanHtml('21')).toBe('')
    expect(extractPelicanHtml('<div>no document</div>')).toBe('')
  })
})
