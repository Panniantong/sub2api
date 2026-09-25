import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import AnnouncementUserPicker from '../AnnouncementUserPicker.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  getById: vi.fn()
}))
vi.mock('@/api/admin', () => ({
  adminAPI: { users: { list: mocks.list, getById: mocks.getById } }
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

const alice = { id: 7, email: 'alice@example.com', username: 'alice' }
const bob = { id: 9, email: 'bob@example.com', username: '' }

beforeEach(() => {
  vi.clearAllMocks()
  vi.useFakeTimers()
  mocks.list.mockResolvedValue({ items: [alice, bob] })
  mocks.getById.mockImplementation(async (id: number) => {
    const user = [alice, bob].find((u) => u.id === id)
    if (!user) throw new Error('not found')
    return user
  })
})
afterEach(() => vi.useRealTimers())

function mountPicker(modelValue: number[] = []) {
  return mount(AnnouncementUserPicker, {
    props: { modelValue },
    global: { stubs: { Icon: true } }
  })
}

async function search(wrapper: ReturnType<typeof mountPicker>, keyword: string) {
  const input = wrapper.get('[data-testid="announcement-user-search"]')
  await input.setValue(keyword)
  await vi.advanceTimersByTimeAsync(300)
  await flushPromises()
  return input
}

const lastEmitted = (wrapper: ReturnType<typeof mountPicker>) =>
  wrapper.emitted('update:modelValue')?.at(-1)?.[0]

describe('AnnouncementUserPicker', () => {
  it('searches users as the admin types and adds the clicked one', async () => {
    const wrapper = mountPicker()
    const input = await search(wrapper, 'ali')
    expect(mocks.list).toHaveBeenCalledWith(1, 10, { search: 'ali' })

    await wrapper.findAll('[data-testid="announcement-user-option"]')[0].trigger('click')
    expect(lastEmitted(wrapper)).toEqual([7])
    expect((input.element as HTMLInputElement).value).toBe('')
    expect(wrapper.find('[data-testid="announcement-user-option"]').exists()).toBe(false)
  })

  it('picks the first result on Enter without submitting the surrounding form', async () => {
    const wrapper = mountPicker([9])
    const input = await search(wrapper, 'a')

    const event = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    input.element.dispatchEvent(event)
    expect(event.defaultPrevented).toBe(true)
    expect(lastEmitted(wrapper)).toEqual([9, 7])
  })

  it('does not add a user twice', async () => {
    const wrapper = mountPicker([7])
    await search(wrapper, 'alice')
    await wrapper.findAll('[data-testid="announcement-user-option"]')[0].trigger('click')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
  })

  it('looks up saved users by id, including deleted ones, and marks missing ones', async () => {
    const wrapper = mountPicker([7, 404])
    await flushPromises()
    expect(mocks.getById).toHaveBeenCalledWith(7, true)
    const chips = wrapper.findAll('[data-testid="announcement-user-chip"]').map((chip) => chip.text())
    expect(chips[0]).toContain('alice (alice@example.com)')
    expect(chips[1]).toContain('#404')
    expect(chips[1]).toContain('admin.announcements.form.userUnavailable')
  })

  it('removes a user with the chip button', async () => {
    const wrapper = mountPicker([7, 9])
    await wrapper.get('button[aria-label="remove user 7"]').trigger('click')
    expect(lastEmitted(wrapper)).toEqual([9])
  })

  it('says so when nothing matches', async () => {
    mocks.list.mockResolvedValueOnce({ items: [] })
    const wrapper = mountPicker()
    await search(wrapper, 'nobody')
    expect(wrapper.text()).toContain('admin.announcements.form.noUsersFound')
  })
})
