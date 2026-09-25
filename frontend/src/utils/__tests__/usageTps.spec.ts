import { describe, expect, it } from 'vitest'

import { formatUsageOutputTps, usageOutputTps } from '../usageTps'

describe('usageTps', () => {
  it('excludes first-token latency when first_token_ms is recorded', () => {
    // 872 tokens over (31.26s - 2.91s) = 28.35s of generation
    expect(usageOutputTps({ output_tokens: 872, duration_ms: 31_260, first_token_ms: 2_910 })).toBeCloseTo(30.758, 3)
    expect(formatUsageOutputTps({ output_tokens: 872, duration_ms: 31_260, first_token_ms: 2_910 })).toBe('30.8 t/s')
  })

  it('falls back to the total duration when first_token_ms is missing', () => {
    expect(usageOutputTps({ output_tokens: 500, duration_ms: 10_000, first_token_ms: null })).toBe(50)
    expect(formatUsageOutputTps({ output_tokens: 500, duration_ms: 10_000 })).toBe('50.0 t/s')
  })

  it('rounds to an integer from 100 t/s upwards', () => {
    expect(formatUsageOutputTps({ output_tokens: 3_000, duration_ms: 11_000, first_token_ms: 1_000 })).toBe('300 t/s')
    expect(formatUsageOutputTps({ output_tokens: 999, duration_ms: 10_000, first_token_ms: 0 })).toBe('99.9 t/s')
  })

  it('returns null when there is nothing meaningful to measure', () => {
    expect(usageOutputTps(null)).toBeNull()
    expect(usageOutputTps({ output_tokens: 0, duration_ms: 5_000, first_token_ms: 1_000 })).toBeNull()
    expect(usageOutputTps({ output_tokens: 100, duration_ms: null, first_token_ms: null })).toBeNull()
    expect(usageOutputTps({ output_tokens: 100, duration_ms: 0, first_token_ms: null })).toBeNull()
    expect(formatUsageOutputTps({ output_tokens: 0, duration_ms: 5_000 })).toBeNull()
  })

  it('returns null when the generation window is empty or negative', () => {
    expect(usageOutputTps({ output_tokens: 10, duration_ms: 2_000, first_token_ms: 2_000 })).toBeNull()
    expect(usageOutputTps({ output_tokens: 10, duration_ms: 2_000, first_token_ms: 2_500 })).toBeNull()
  })

  it('skips image and video requests', () => {
    expect(usageOutputTps({ output_tokens: 4_160, duration_ms: 40_000, image_count: 1 })).toBeNull()
    expect(usageOutputTps({ output_tokens: 4_160, duration_ms: 40_000, image_output_tokens: 4_160 })).toBeNull()
    expect(usageOutputTps({ output_tokens: 100, duration_ms: 40_000, billing_mode: 'video' })).toBeNull()
  })
})
