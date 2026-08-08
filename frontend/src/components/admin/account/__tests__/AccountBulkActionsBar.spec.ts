import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

import AccountBulkActionsBar from '../AccountBulkActionsBar.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key
  })
}))

describe('AccountBulkActionsBar', () => {
  it('allows selecting all results before any row is selected', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    const button = wrapper.findAll('button').find(item =>
      item.text().includes('admin.accounts.bulkActions.selectAllResults')
    )

    expect(button).toBeDefined()
    await button!.trigger('click')
    expect(wrapper.emitted('select-all-results')).toHaveLength(1)
  })

  it('preserves the upstream billing probe action from v0.1.166', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [1],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false
      }
    })

    const button = wrapper.findAll('button').find(item =>
      item.text().includes('admin.accounts.bulkActions.probeUpstreamBilling')
    )

    expect(button).toBeDefined()
    await button!.trigger('click')
    expect(wrapper.emitted('probe-upstream-billing')).toHaveLength(1)
  })

  it('starts a batch test and renders live progress while it is running', async () => {
    const wrapper = mount(AccountBulkActionsBar, {
      props: {
        selectedIds: [1, 2, 3],
        totalResults: 45,
        selectingAll: false,
        allResultsSelected: false,
        batchTesting: false,
        batchTestProcessed: 0,
        batchTestTotal: 0
      }
    })

    const button = wrapper.get('[data-test="batch-test-accounts"]')
    await button.trigger('click')
    expect(wrapper.emitted('batch-test')).toHaveLength(1)

    await wrapper.setProps({
      batchTesting: true,
      batchTestProcessed: 50,
      batchTestTotal: 700
    })
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.text()).toContain('50')
    expect(button.text()).toContain('700')
    expect(wrapper.get('[data-test="batch-test-progress"]').attributes('style')).toContain('7.14')
  })
})
