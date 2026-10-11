<script setup lang="ts">
import { computed, ref } from 'vue'
import { getAll } from '@/api/admin/groups'

type Rule = { group_id: number; mode: string; pre_output_seconds?: number; max_switches?: number }
const model = defineModel<string>({ default: '' })
const groups = ref<{ id: number; name: string }[]>([])
const loadError = ref(false)
let loaded = false
const rules = computed<Rule[]>(() => {
  try { return JSON.parse(model.value || '[]') ?? [] } catch { return [] }
})
async function loadGroups() {
  if (loaded) return
  try { groups.value = await getAll(); loaded = true; loadError.value = false } catch { loadError.value = true }
}
function save(rows: Rule[]) { model.value = rows.length ? JSON.stringify(rows) : '' }
function update(index: number, field: keyof Rule, event: Event) {
  const value = (event.target as HTMLInputElement).value
  const rows = rules.value.map(row => ({ ...row }))
  const row = rows[index]!
  if (field === 'mode') {
    row.mode = value
    if (value === 'legacy') { delete row.pre_output_seconds; delete row.max_switches }
  } else if (field === 'group_id') row.group_id = Number(value)
  else if (value === '') delete row[field]
  else row[field] = Number(value)
  save(rows)
}
async function add() {
  await loadGroups()
  const available = groups.value.find(g => !rules.value.some(r => r.group_id === g.id))
  if (available) save([...rules.value, { group_id: available.id, mode: 'inherit' }])
}
</script>

<template>
  <div class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600">
    <div class="flex items-center justify-between">
      <span class="font-medium">分组覆盖规则</span>
      <button type="button" class="btn btn-secondary" @click="add">添加分组规则</button>
    </div>
    <p class="text-xs text-gray-500">未配置的分组继承全局。按请求 API Key 所属分组生效，Cookie 降级账号同样遵循该规则。保存后对新请求生效。</p>
    <p v-if="loadError" class="text-sm text-red-500">分组列表加载失败，请重新点击添加或分组下拉框。</p>
    <div v-for="(row, index) in rules" :key="index" class="grid items-end gap-3 rounded-lg bg-gray-50 p-3 dark:bg-dark-700 sm:grid-cols-5" data-testid="group-scheduling-row">
      <label class="text-sm">请求分组
        <select :value="row.group_id" class="input mt-1" @focus="loadGroups" @change="update(index, 'group_id', $event)">
          <option v-if="!groups.some(g => g.id === row.group_id)" :value="row.group_id">分组 #{{ row.group_id }}</option>
          <option v-for="group in groups" :key="group.id" :value="group.id" :disabled="rules.some((r, i) => i !== index && r.group_id === group.id)">{{ group.name }} (#{{ group.id }})</option>
        </select>
      </label>
      <label class="text-sm">调度模式
        <select :value="row.mode" class="input mt-1" @change="update(index, 'mode', $event)">
          <option value="inherit">继承全局</option><option value="legacy">旧调度</option><option value="balanced">高级：综合评分</option><option value="low_load">高级：低负载优先</option>
        </select>
      </label>
      <template v-if="row.mode !== 'legacy'">
        <label class="text-sm">首字预算（秒，0 关闭）<input :value="row.pre_output_seconds" type="number" min="0" max="3600" step="1" placeholder="继承全局" class="input mt-1" @input="update(index, 'pre_output_seconds', $event)" /></label>
        <label class="text-sm">最多换号次数<input :value="row.max_switches" type="number" min="0" max="20" step="1" placeholder="继承全局" class="input mt-1" @input="update(index, 'max_switches', $event)" /></label>
      </template>
      <p v-else class="text-xs text-gray-500 sm:col-span-2">不启用新增首字计时；重试、换号及排队使用原有逻辑和服务端配置。</p>
      <button type="button" class="btn btn-secondary" @click="save(rules.filter((_, i) => i !== index))">删除规则</button>
    </div>
  </div>
</template>
