import { describe, expect, it, vi } from 'vitest'
import { apiClient } from '@/api/client'
import { intelligentTestsAPI, type TestSetting } from '@/api/intelligentTests'

vi.mock('@/api/client', () => ({ apiClient: { put: vi.fn() } }))

describe('test settings submission snapshot', () => {
  it('returns the values actually sent when the editable source changes during the request', async () => {
    let complete: () => void = () => undefined
    vi.mocked(apiClient.put).mockImplementation(() => new Promise(resolve => { complete = () => resolve({ data: { updated: true } }) }))
    const setting: TestSetting = { test_type: 'candy', enabled: true, user_visible: false, config: { prompt: 'Original question', model: '', evaluator: 'exact_answer', expected_answer: '12', timeout_seconds: 180 } }
    const pending = intelligentTestsAPI.saveSetting(setting)
    setting.user_visible = true
    setting.config.prompt = 'Not submitted'
    setting.config.expected_answer = '99'
    expect(apiClient.put).toHaveBeenCalledWith('/admin/intelligent-tests/settings/candy', expect.objectContaining({ user_visible: false, config: expect.objectContaining({ prompt: 'Original question', expected_answer: '12' }) }))
    complete()
    const saved = await pending
    expect(saved.user_visible).toBe(false)
    expect(saved.config.prompt).toBe('Original question')
    expect(saved.config.expected_answer).toBe('12')
  })
})
