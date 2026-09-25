import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import BaseDialog from '../BaseDialog.vue'

vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

describe('BaseDialog', () => {
  afterEach(() => {
    document.body.innerHTML = ''
    document.body.classList.remove('modal-open')
  })

  it('resets body scroll position when reopened', async () => {
    const wrapper = mount(BaseDialog, {
      attachTo: document.body,
      props: { show: false, title: 'Details' },
      slots: { default: '<div style="height: 2000px">content</div>' },
      global: { stubs: { Icon: true } }
    })

    await wrapper.setProps({ show: true })
    await nextTick()
    const body = document.body.querySelector<HTMLElement>('.modal-body')
    expect(body).not.toBeNull()
    body!.scrollTop = 480

    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await nextTick()

    expect(document.body.querySelector<HTMLElement>('.modal-body')?.scrollTop).toBe(0)
    wrapper.unmount()
  })

  describe('click outside', () => {
    const mountOpen = (props: Record<string, unknown> = {}) =>
      mount(BaseDialog, {
        attachTo: document.body,
        props: { show: true, title: 'Details', ...props },
        slots: { default: '<p class="selectable">request-id-to-copy</p>' },
        global: { stubs: { Icon: true } }
      })

    // 按浏览器的派发顺序模拟一次鼠标操作：按下和松开的目标不同时，click 落在两者的共同祖先上
    const pointer = (down: Element, up: Element, click: Element) => {
      down.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
      up.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }))
      click.dispatchEvent(new MouseEvent('click', { bubbles: true }))
    }

    const overlay = () => document.body.querySelector('.modal-overlay')!
    const panelText = () => document.body.querySelector('.selectable')!

    it('closes when the overlay is pressed and released', async () => {
      const wrapper = mountOpen({ closeOnClickOutside: true, placement: 'right' })
      await nextTick()

      pointer(overlay(), overlay(), overlay())

      expect(wrapper.emitted('close')).toHaveLength(1)
      wrapper.unmount()
    })

    it('stays open when a press and release do not both land on the overlay', async () => {
      const wrapper = mountOpen({ closeOnClickOutside: true, placement: 'right' })
      await nextTick()

      // 在面板里拖选文字，松手落在遮罩上
      pointer(panelText(), overlay(), overlay())
      // 在遮罩上按下，拖进面板里松手
      pointer(overlay(), panelText(), overlay())
      expect(wrapper.emitted('close')).toBeUndefined()

      pointer(overlay(), overlay(), overlay())
      expect(wrapper.emitted('close')).toHaveLength(1)
      wrapper.unmount()
    })

    it('ignores overlay clicks unless closeOnClickOutside is enabled', async () => {
      const wrapper = mountOpen()
      await nextTick()

      pointer(overlay(), overlay(), overlay())

      expect(wrapper.emitted('close')).toBeUndefined()
      wrapper.unmount()
    })
  })
})
