import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import CookieRotationPools from '@/components/admin/CookieRotationPools.vue'
import { defaultCookieRotationPool } from '../cookieRotationPools'

describe('Cookie low Host rotation controls', () => {
  it('enables old policies with defaults and preserves exactly three configuration fields', async () => {
    const policy = defaultCookieRotationPool(2)
    delete policy.low_host_rotation_enabled
    delete policy.low_host_threshold
    delete policy.low_host_account_slots
    const wrapper = mount(CookieRotationPools, {
      props: { modelValue: [policy], selectedGroups: [2], groups: [], accounts: [] },
      global: { stubs: { BaseDialog: true } },
    })
    const module = wrapper.get('[data-testid="cookie-low-host-rotation"]')
    expect(module.findAll('input')).toHaveLength(3)
    const numbers = module.findAll('input[type="number"]')
    expect(numbers[0]!.attributes('disabled')).toBeDefined()
    await module.get('input[type="checkbox"]').setValue(true)
    expect(policy.low_host_rotation_enabled).toBe(true)
    expect(policy.low_host_threshold).toBe(5)
    expect(policy.low_host_account_slots).toBe(10)
    await numbers[0]!.setValue(3)
    await numbers[1]!.setValue(5)
    expect(JSON.parse(JSON.stringify(policy))).toMatchObject({
      low_host_rotation_enabled: true, low_host_threshold: 3, low_host_account_slots: 5,
    })
    await module.get('input[type="checkbox"]').setValue(false)
    expect(policy.low_host_rotation_enabled).toBe(false)
    expect(policy.low_host_threshold).toBe(3)
    expect(policy.low_host_account_slots).toBe(5)
  })
})
