<template>
  <div
    class="flex min-h-[38px] flex-wrap items-center gap-1 rounded-lg border border-gray-200 bg-white px-2 py-1 focus-within:border-primary-500 dark:border-dark-500 dark:bg-dark-700"
    data-testid="model-tag-input"
  >
    <span
      v-for="model in modelValue"
      :key="model"
      class="inline-flex items-center gap-1 rounded bg-red-50 px-2 py-0.5 text-xs font-medium text-red-700 dark:bg-red-900/20 dark:text-red-300"
      data-testid="model-tag"
    >
      {{ model }}
      <button
        type="button"
        class="rounded hover:text-red-900 dark:hover:text-red-100"
        :aria-label="`remove ${model}`"
        @click="removeModel(model)"
      >
        <Icon name="x" size="xs" />
      </button>
    </span>
    <input
      v-model="draft"
      :list="listId"
      type="text"
      autocomplete="off"
      class="min-w-[140px] flex-1 border-0 bg-transparent px-1 py-0.5 text-sm text-gray-900 focus:outline-none focus:ring-0 dark:text-white"
      :placeholder="placeholder"
      data-testid="model-tag-draft"
      @keydown="handleKeydown"
      @blur="commitDraft"
    />
    <datalist :id="listId">
      <option v-for="candidate in availableCandidates" :key="candidate" :value="candidate" />
    </datalist>
  </div>
</template>

<script lang="ts">
let modelTagInputCounter = 0
</script>

<script setup lang="ts">
import { computed, ref } from 'vue'
import Icon from '@/components/icons/Icon.vue'

const props = defineProps<{
  modelValue: string[]
  candidates?: string[]
  placeholder?: string
}>()

const emit = defineEmits<{
  'update:modelValue': [value: string[]]
}>()

const listId = `model-tag-input-${++modelTagInputCounter}`
const draft = ref('')

const hasModel = (models: string[], model: string) =>
  models.some(existing => existing.toLowerCase() === model.toLowerCase())

const availableCandidates = computed(() =>
  (props.candidates ?? []).filter(candidate => !hasModel(props.modelValue, candidate))
)

// 输入框里可以一次粘贴多个模型，逗号或空白分隔；与已有条目按小写去重。
const commitDraft = () => {
  const parts = draft.value.split(/[\s,，]+/).map(part => part.trim()).filter(Boolean)
  draft.value = ''
  if (parts.length === 0) return
  const next = [...props.modelValue]
  for (const part of parts) {
    if (!hasModel(next, part)) next.push(part)
  }
  if (next.length !== props.modelValue.length) {
    emit('update:modelValue', next)
  }
}

const removeModel = (model: string) => {
  emit('update:modelValue', props.modelValue.filter(existing => existing !== model))
}

const handleKeydown = (event: KeyboardEvent) => {
  if (event.isComposing) return
  if (event.key === 'Enter' || event.key === ',') {
    event.preventDefault()
    commitDraft()
    return
  }
  if (event.key === 'Backspace' && draft.value === '' && props.modelValue.length > 0) {
    removeModel(props.modelValue[props.modelValue.length - 1])
  }
}
</script>
