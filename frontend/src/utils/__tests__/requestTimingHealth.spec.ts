import { describe, expect, it } from 'vitest'
import { stageScale, timingHealth } from '../requestTimingHealth'
describe('request timing health interpretation', () => {
  it('uses separate first-output, total and local-stage reference bands', () => {
    expect(timingHealth(15000, 'first')).toBe('warn')
    expect(timingHealth(15000, 'total')).toBe('good')
    expect(timingHealth(15000, 'stage')).toBe('critical')
    expect([199, 200, 999, 1000, 4999, 5000].map(n => timingHealth(n, 'stage'))).toEqual(['good', 'warn', 'warn', 'slow', 'slow', 'critical'])
  })
  it('does not rate unknown values or parent intervals as local bottlenecks', () => {
    for (const value of [null, undefined, -1, NaN, Infinity]) expect(timingHealth(value, 'stage')).toBe('neutral')
    expect(timingHealth(60000, stageScale('forward_attempt'))).toBe('neutral')
    expect(timingHealth(60000, stageScale('handler'))).toBe('neutral')
    expect(timingHealth(60000, stageScale('user_queue'))).toBe('critical')
  })
})
