import { describe, expect, it } from 'vitest'

import { durationSeverity, firstTokenSeverity, tpsSeverity } from '../latencyHealth'

describe('latencyHealth', () => {
  it('classifies first-token latency at 10s/30s/60s boundaries', () => {
    expect(firstTokenSeverity(0)).toBe('good')
    expect(firstTokenSeverity(9_999)).toBe('good')
    expect(firstTokenSeverity(10_000)).toBe('warn')
    expect(firstTokenSeverity(29_999)).toBe('warn')
    expect(firstTokenSeverity(30_000)).toBe('slow')
    expect(firstTokenSeverity(59_999)).toBe('slow')
    expect(firstTokenSeverity(60_000)).toBe('critical')
  })

  it('classifies total duration at 1min/3min/5min boundaries', () => {
    expect(durationSeverity(0)).toBe('good')
    expect(durationSeverity(59_999)).toBe('good')
    expect(durationSeverity(60_000)).toBe('warn')
    expect(durationSeverity(179_999)).toBe('warn')
    expect(durationSeverity(180_000)).toBe('slow')
    expect(durationSeverity(299_999)).toBe('slow')
    expect(durationSeverity(300_000)).toBe('critical')
  })

  it('classifies output speed at 10/20 t/s boundaries (higher is better)', () => {
    expect(tpsSeverity(0)).toBe('critical')
    expect(tpsSeverity(9.99)).toBe('critical')
    expect(tpsSeverity(10)).toBe('warn')
    expect(tpsSeverity(19.99)).toBe('warn')
    expect(tpsSeverity(20)).toBe('good')
    expect(tpsSeverity(500)).toBe('good')
  })
})
