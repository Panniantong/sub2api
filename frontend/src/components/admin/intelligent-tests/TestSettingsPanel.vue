<template>
  <div class="space-y-5">
    <div class="rounded-xl border border-primary-200 bg-primary-50/50 p-4 text-sm leading-relaxed text-gray-600 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-300">测试功能与用户可见性独立控制。关闭用户可见后，普通用户的入口和结果接口都会隐藏；检测不会自动修改账号策略。</div>
    <form v-for="setting in drafts" :key="setting.test_type" class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800" @submit.prevent="save(setting)">
      <fieldset :disabled="saving === setting.test_type" @input="markDirty(setting.test_type)" @change="markDirty(setting.test_type)">
      <div class="flex flex-wrap items-center justify-between gap-4 border-b border-gray-100 pb-4 dark:border-dark-700">
        <div><h2 class="font-semibold">{{ setting.name || testName(setting.test_type) }}</h2><p class="mt-1 text-xs text-gray-500">独立的题目、模型和评分规则</p></div>
        <div class="flex flex-wrap gap-5 text-sm">
          <label class="flex items-center gap-2"><input v-model="setting.enabled" type="checkbox" class="rounded text-primary-600" /> 管理员可用</label>
          <label class="flex items-center gap-2"><input v-model="setting.user_visible" type="checkbox" class="rounded text-primary-600" /> 用户可查看</label>
        </div>
      </div>
      <div class="mt-4 grid gap-4 sm:grid-cols-2">
        <label class="block text-sm">模型<input v-model.trim="setting.config.model" class="input mt-2" :list="`intelligent-models-${setting.test_type}`" placeholder="留空使用账号默认模型" maxlength="200" />
          <datalist :id="`intelligent-models-${setting.test_type}`"><option v-for="model in modelSuggestions" :key="model" :value="model" /></datalist>
        </label>
        <label class="block text-sm">超时（秒）<input v-model.number="setting.config.timeout_seconds" type="number" class="input mt-2" min="30" max="600" required /></label>
        <label class="block text-sm sm:col-span-2">测试题目<textarea v-model="setting.config.prompt" class="input mt-2 min-h-32" maxlength="16000" required /></label>
        <label class="block text-sm">评分规则<select v-model="setting.config.evaluator" class="input mt-2"><option value="svg_structure">SVG 结构校验</option><option value="exact_answer">标准答案校验</option></select></label>
        <template v-if="setting.config.evaluator === 'exact_answer'">
          <label class="block text-sm">标准答案<input v-model="setting.config.expected_answer" class="input mt-2" maxlength="200" required /></label>
          <label class="block text-sm">答案类型<select v-model="setting.config.answer_type" class="input mt-2"><option value="auto">自动识别数值或文本</option><option value="number">数值等价比较</option><option value="text">文本比较</option></select></label>
          <label class="block text-sm">单位规则<select v-model="setting.config.answer_unit_mode" class="input mt-2"><option value="none">不接受单位</option><option value="configured">只接受指定单位</option><option value="legacy">沿用旧题兼容规则</option></select></label>
          <label v-if="setting.config.answer_unit_mode !== 'none'" class="block text-sm">允许的数值单位<input v-model.trim="setting.config.answer_unit" class="input mt-2" maxlength="32" :required="setting.config.answer_unit_mode === 'configured'" placeholder="例如：颗（也接受颗糖）" /><span v-if="setting.config.answer_unit_mode === 'legacy'" class="mt-1 block text-xs text-gray-500">旧糖果题留空时兼容“颗”；选择“不接受单位”可明确关闭。</span></label>
          <label class="block text-sm">独立格式要求<select v-model="setting.config.answer_format" class="input mt-2"><option value="answer_line">唯一末行 ANSWER: 答案</option><option value="free_text">不要求固定格式</option></select></label>
        </template>
      </div>
      <p class="mt-3 text-xs leading-relaxed text-gray-500">{{ setting.config.evaluator === 'svg_structure' ? '安全展示生成的 SVG，检查基本结构，不为图像内容或模型能力打分。' : 'B 方案分别判断答案和格式。12、12.0、全角数字及允许单位可数值等价；Markdown 和格式差异不扣答案分。多个冲突答案显示无法判定。仅检查最终答案，不验证完整推导。修改题目时请同步标准答案与单位。' }}</p>
      <details class="mt-4 rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
        <summary class="cursor-pointer text-sm">粘贴答复试判（使用当前草稿，不调用模型）</summary>
        <textarea v-model="trialOutputs[setting.test_type]" class="input mt-3 min-h-24" maxlength="131072" placeholder="粘贴模型答复或 SVG" />
        <button type="button" class="btn btn-secondary mt-2" :disabled="!!previewing || !trialOutputs[setting.test_type]" @click="preview(setting)">{{ previewing === setting.test_type ? '正在试判…' : '试判当前答复' }}</button>
        <div v-if="previews[setting.test_type]" class="mt-3 space-y-3">
          <TestAssessment :assessment="previews[setting.test_type]?.evaluation" :status="previews[setting.test_type]?.status" />
          <TestGeneratedImage v-if="previews[setting.test_type]?.result_image" :source="previews[setting.test_type]?.result_image" />
        </div>
      </details>
      <p v-if="errors[setting.test_type]" class="mt-3 text-sm text-red-600" role="alert">{{ errors[setting.test_type] }}</p>
      <div class="mt-4 flex items-center justify-end gap-3"><span v-if="saved === setting.test_type" class="text-sm text-emerald-600">已保存</span><button class="btn btn-primary" :disabled="!!saving">{{ saving === setting.test_type ? '保存中…' : '保存设置' }}</button></div>
      </fieldset>
    </form>
  </div>
</template>
<script setup lang="ts">
import { ref, watch } from 'vue'
import { intelligentTestsAPI, type TestSetting, type TestRecord } from '@/api/intelligentTests'
import { extractApiErrorMessage } from '@/utils/apiError'
import { testName } from './display'
import TestAssessment from './TestAssessment.vue'
import TestGeneratedImage from './TestGeneratedImage.vue'
const props = defineProps<{ settings: TestSetting[] }>()
const emit = defineEmits<{ saved: [setting: TestSetting] }>()
const drafts = ref<TestSetting[]>([]), saving = ref(''), saved = ref(''), errors = ref<Record<string, string>>({})
const dirty = new Set<string>()
const trialOutputs = ref<Record<string, string>>({}), previews = ref<Record<string, TestRecord | undefined>>({}), previewing = ref('')
function markDirty(type: string) { dirty.add(type); saved.value = ''; previews.value[type] = undefined }
// Suggestions remain editable; explicit choices are preserved by the runner.
const modelSuggestions = ['gpt-5.3-codex', 'gpt-5.4', 'gpt-5.4-mini', 'gpt-5.5', 'claude-sonnet-4-5-20250929', 'gemini-2.0-flash']
watch(() => props.settings, value => {
  drafts.value = value.map(item => {
    const existing = drafts.value.find(draft => draft.test_type === item.test_type)
    if (existing && dirty.has(item.test_type)) return existing
    return { ...item, config: { answer_type: 'auto', answer_format: 'answer_line', answer_unit_mode: item.config.answer_unit ? 'configured' : 'legacy', ...item.config } }
  })
}, { immediate: true, deep: true })
async function preview(setting: TestSetting) {
  if (previewing.value) return
  const type = setting.test_type, output = trialOutputs.value[type] || '', config = { ...setting.config }
  previewing.value = type; errors.value[type] = ''
  try {
    const result = await intelligentTestsAPI.previewEvaluation(output, config)
    if (output === trialOutputs.value[type] && JSON.stringify(config) === JSON.stringify(setting.config)) previews.value[type] = result
  } catch (err) { errors.value[type] = extractApiErrorMessage(err, '试判失败') }
  finally { previewing.value = '' }
}
async function save(setting: TestSetting) {
  if (saving.value) return
  const snapshot: TestSetting = { ...setting, config: { ...setting.config } }
  saving.value = setting.test_type; saved.value = ''; errors.value[setting.test_type] = ''
  try { const result = await intelligentTestsAPI.saveSetting(snapshot); dirty.delete(snapshot.test_type); saved.value = snapshot.test_type; emit('saved', result) }
  catch (error) { errors.value[setting.test_type] = extractApiErrorMessage(error, '设置保存失败') }
  finally { saving.value = '' }
}
</script>
