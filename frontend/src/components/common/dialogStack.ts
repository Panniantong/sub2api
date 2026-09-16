import { shallowReactive } from 'vue'

const dialogs = shallowReactive<{ id: string; zIndex: number }[]>([])
let sequence = 0
export const nextDialogId = () => `modal-title-${++sequence}`
export function registerDialog(id: string, zIndex: number) {
  const current = dialogs.find(item => item.id === id)
  if (current) current.zIndex = zIndex
  else dialogs.push({ id, zIndex })
  document.body.classList.add('modal-open')
}
export function unregisterDialog(id: string) {
  const index = dialogs.findIndex(item => item.id === id)
  if (index >= 0) dialogs.splice(index, 1)
  document.body.classList.toggle('modal-open', dialogs.length > 0)
}
export const isTopDialog = (id: string) => dialogs.at(-1)?.id === id
export function dialogLayer(id: string, fallback: number) {
  let layer = 40
  for (const item of dialogs) {
    layer = Math.max(layer + 10, item.zIndex)
    if (item.id === id) return layer
  }
  return fallback
}
