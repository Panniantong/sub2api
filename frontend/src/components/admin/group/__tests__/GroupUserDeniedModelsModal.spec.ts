import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { AdminGroup } from '@/types'
import GroupUserDeniedModelsModal from '../GroupUserDeniedModelsModal.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  getGroupUserDeniedModels: vi.fn(),
  batchSetGroupUserDeniedModels: vi.fn(),
  clearGroupUserDeniedModels: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    users: { list: mocks.list },
    groups: {
      getGroupUserDeniedModels: mocks.getGroupUserDeniedModels,
      batchSetGroupUserDeniedModels: mocks.batchSetGroupUserDeniedModels,
      clearGroupUserDeniedModels: mocks.clearGroupUserDeniedModels,
      getModelAllowlistCandidates: mocks.getModelAllowlistCandidates
    }
  }
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)
afterEach(() => vi.useRealTimers())

const savedEntry = {
  user_id: 3, user_name: 'alice', user_email: 'alice@example.com', user_notes: '', user_status: 'active',
  denied_models: ['gpt-6-luna']
}

beforeEach(() => {
  vi.clearAllMocks()
  vi.useFakeTimers()
  mocks.getGroupUserDeniedModels.mockResolvedValue([savedEntry])
  mocks.getModelAllowlistCandidates.mockResolvedValue(['gpt-6-luna', 'gpt-6-sol'])
  mocks.list.mockResolvedValue({ items: [{ id: 7, email: 'bob@example.com', status: 'active' }] })
  mocks.batchSetGroupUserDeniedModels.mockResolvedValue(undefined)
  mocks.clearGroupUserDeniedModels.mockResolvedValue(undefined)
})

async function openModal() {
  const wrapper = mount(GroupUserDeniedModelsModal, {
    props: { show: false, group: { id: 1, name: 'Group A', platform: 'openai' } as AdminGroup },
    global: { stubs: {
      BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
      Icon: true, PlatformIcon: true, Pagination: true
    } }
  })
  await wrapper.setProps({ show: true })
  await flushPromises()
  return wrapper
}

const buttonByText = (wrapper: Awaited<ReturnType<typeof openModal>>, text: string) =>
  wrapper.findAll('button').find(b => b.text() === text)

describe('GroupUserDeniedModelsModal', () => {
  it('lists saved limits with their models and offers group models as suggestions', async () => {
    const wrapper = await openModal()
    const row = wrapper.get('[data-testid="denied-models-row-3"]')
    expect(row.text()).toContain('alice@example.com')
    expect(row.findAll('[data-testid="model-tag"]').map(tag => tag.text())).toEqual(['gpt-6-luna'])
    expect(mocks.getModelAllowlistCandidates).toHaveBeenCalledWith(1)
    expect(buttonByText(wrapper, 'common.save')).toBeUndefined()
  })

  it('adds a user with denied models and saves the whole group', async () => {
    const wrapper = await openModal()
    await wrapper.get('[data-testid="denied-models-user-search"]').setValue('bob')
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    await wrapper.get('[data-testid="denied-models-user-option"]').trigger('click')

    const addButton = wrapper.get('[data-testid="denied-models-add"]')
    expect(addButton.attributes('disabled')).toBeDefined()
    const draft = wrapper.findAll('[data-testid="model-tag-draft"]')[0]
    await draft.setValue('gpt-6-*')
    await draft.trigger('keydown', { key: 'Enter' })
    await addButton.trigger('click')

    await wrapper.get('[data-testid="denied-models-save"]').trigger('click')
    await flushPromises()
    expect(mocks.batchSetGroupUserDeniedModels).toHaveBeenCalledWith(1, [
      { user_id: 3, denied_models: ['gpt-6-luna'] },
      { user_id: 7, denied_models: ['gpt-6-*'] }
    ])
    expect(wrapper.emitted('close')).toBeTruthy()
  })

  it('drops users whose list became empty or who were removed', async () => {
    const wrapper = await openModal()
    await wrapper.get('[data-testid="denied-models-remove-3"]').trigger('click')
    await wrapper.get('[data-testid="denied-models-save"]').trigger('click')
    await flushPromises()
    expect(mocks.batchSetGroupUserDeniedModels).toHaveBeenCalledWith(1, [])
  })

  it('shows the server reason when saving fails', async () => {
    mocks.batchSetGroupUserDeniedModels.mockRejectedValueOnce({ message: 'wildcard "*" is only allowed at the end of a model name' })
    const wrapper = await openModal()
    await wrapper.get('[data-testid="denied-models-remove-3"]').trigger('click')
    await wrapper.get('[data-testid="denied-models-save"]').trigger('click')
    await flushPromises()
    expect(mocks.showError).toHaveBeenCalledWith('wildcard "*" is only allowed at the end of a model name')
    expect(wrapper.emitted('close')).toBeFalsy()
  })

  it('loads saved limits even when mounted already open, so saving keeps them', async () => {
    const wrapper = mount(GroupUserDeniedModelsModal, {
      props: { show: true, group: { id: 1, name: 'Group A', platform: 'openai' } as AdminGroup },
      global: { stubs: {
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
        Icon: true, PlatformIcon: true, Pagination: true
      } }
    })
    await flushPromises()
    expect(wrapper.find('[data-testid="denied-models-row-3"]').exists()).toBe(true)
  })

  it('clears every limit of the group at once', async () => {
    const wrapper = await openModal()
    await wrapper.get('[data-testid="denied-models-clear-all"]').trigger('click')
    await flushPromises()
    expect(mocks.clearGroupUserDeniedModels).toHaveBeenCalledWith(1)
    expect(wrapper.find('[data-testid="denied-models-row-3"]').exists()).toBe(false)
  })
})
