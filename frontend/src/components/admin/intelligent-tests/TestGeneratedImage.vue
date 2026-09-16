<template>
  <div class="flex h-full min-h-40 w-full items-center rounded-xl bg-white bg-[linear-gradient(45deg,#f3f4f6_25%,transparent_25%),linear-gradient(-45deg,#f3f4f6_25%,transparent_25%)] p-2 dark:bg-dark-900" :class="fullSize ? 'justify-start' : 'justify-center'" data-testid="generated-test-image">
    <img v-if="url && !failed" :src="url" alt="模型生成的鹈鹕测试图像" :style="fullSize ? dimensions : undefined" :class="fullSize ? 'max-w-none shrink-0' : 'max-h-[65vh] max-w-full object-contain'" @error="failed = true" />
    <p v-else class="p-5 text-center text-xs text-gray-500">{{ loading ? '正在加载生成图像…' : '暂无可安全显示的图像，可查看原始答复或重新评估。' }}</p>
  </div>
</template>
<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { intelligentTestsAPI } from '@/api/intelligentTests'
import { testImageURL } from './display'
const props = defineProps<{ source?: string; recordId?: number; publicView?: boolean; fullSize?: boolean }>()
const inline = computed(() => testImageURL(props.source))
const fetched = ref(''), loading = ref(false), failed = ref(false)
const fetchedSVG = ref('')
const dimensions = computed(() => {
  const raw = props.source || fetchedSVG.value
  if (!raw || raw.length > 2_000_000) return undefined
  const svg = new DOMParser().parseFromString(raw, 'image/svg+xml').documentElement
  const box = (svg.getAttribute('viewBox') || '').trim().split(/[\s,]+/).map(Number)
  const width = Number(svg.getAttribute('width')) || box[2]
  const height = Number(svg.getAttribute('height')) || box[3]
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) return undefined
  const scale = Math.min(1, 4096 / Math.max(width, height))
  return { width: `${width * scale}px`, height: `${height * scale}px` }
})
let controller: AbortController | undefined, generation = 0
const url = computed(() => inline.value || fetched.value)
function release() { if (fetched.value) URL.revokeObjectURL(fetched.value); fetched.value = ''; fetchedSVG.value = '' }
watch(() => [props.source, props.recordId, props.publicView], async () => {
  const current = ++generation
  controller?.abort(); release(); failed.value = false; loading.value = false
  if (inline.value || !props.recordId) return
  controller = new AbortController(); loading.value = true
  try {
    const blob = await intelligentTestsAPI.image(props.recordId, props.publicView, controller.signal)
    if (current !== generation) return
    if (!blob.type.includes('image/svg+xml') || blob.size > 128 * 1024) { failed.value = true; return }
    const svg = await blob.text()
    if (current !== generation) return
    fetchedSVG.value = svg
    fetched.value = URL.createObjectURL(blob)
  } catch { if (current === generation) failed.value = true }
  finally { if (current === generation) loading.value = false }
}, { immediate: true })
onUnmounted(() => { generation++; controller?.abort(); release() })
</script>
