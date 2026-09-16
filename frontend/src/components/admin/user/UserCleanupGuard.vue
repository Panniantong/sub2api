<template>
  <div v-if="role === 'user'" class="space-y-3 rounded-xl border border-gray-200 p-4 dark:border-dark-700">
    <p class="text-sm font-medium text-gray-800 dark:text-gray-100">清理保护</p>
    <p class="text-xs text-gray-500">受保护或系统账号不会进入无余额用户清理范围。此项单独保存，立即生效。</p>
    <p v-if="loading" class="text-xs text-gray-500">正在读取保护状态…</p>
    <template v-else-if="guard">
      <label class="flex items-center gap-2 text-sm"><input :checked="guard.is_protected" type="checkbox" :disabled="saving" @click.prevent="requestChange(!guard.is_protected, guard.is_system)" />保留此用户，不参与批量清理</label>
      <label v-if="canMarkSystem" class="flex items-center gap-2 text-sm"><input :checked="guard.is_system" type="checkbox" :disabled="saving" @click.prevent="requestChange(guard.is_protected, !guard.is_system)" />标记为系统账号</label>
      <p v-else-if="guard.is_system" class="text-xs text-emerald-600">系统账号已自动排除，仅超级管理员可取消此标记。</p>
    </template>
    <p v-if="error" class="text-xs text-red-600" role="alert">{{ error }} <button type="button" class="underline" @click="load">重新读取</button></p>
    <p v-if="saved" class="text-xs text-emerald-600" role="status">保护设置已保存</p>
    <div v-if="pending" class="space-y-2 rounded-lg bg-amber-50 p-3 text-xs text-amber-900 dark:bg-amber-950/30 dark:text-amber-200">
      <p>取消保护后，此用户可能在满足条件时进入批量清理范围。确认取消保护？</p>
      <div class="flex gap-3"><button type="button" :disabled="saving" @click="pending = null">取消</button><button type="button" :disabled="saving" class="font-medium underline" @click="save(pending.protected, pending.system)">确认取消保护</button></div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { userCleanupAPI, type UserCleanupGuard } from '@/api/admin/userCleanup'

const props = defineProps<{ userId: number; role: string; canMarkSystem?: boolean }>()
const guard = ref<UserCleanupGuard | null>(null)
const loading = ref(false)
const saving = ref(false)
const saved = ref(false)
const error = ref('')
const pending = ref<{ protected: boolean; system: boolean } | null>(null)
let requestVersion = 0

async function load() {
  const version = ++requestVersion
  guard.value = null; error.value = ''; saved.value = false; pending.value = null
  if (props.role !== 'user') return
  loading.value = true
  try { const value = await userCleanupAPI.getGuard(props.userId); if (version === requestVersion) guard.value = value }
  catch (e) { if (version === requestVersion) error.value = e instanceof Error ? e.message : '读取保护状态失败' }
  finally { if (version === requestVersion) loading.value = false }
}

function requestChange(protectedValue: boolean, system: boolean) {
  if (saving.value || !guard.value) return
  if ((guard.value.is_protected && !protectedValue) || (guard.value.is_system && !system)) pending.value = { protected: protectedValue, system }
  else void save(protectedValue, system)
}

async function save(protectedValue: boolean, system: boolean) {
  if (saving.value) return
  const version = requestVersion
  saving.value = true; error.value = ''; saved.value = false
  try { const value = await userCleanupAPI.updateGuard(props.userId, protectedValue, props.canMarkSystem ? system : undefined); if (version === requestVersion) { guard.value = value; saved.value = true; pending.value = null } }
  catch (e) { if (version === requestVersion) error.value = e instanceof Error ? e.message : '保存保护状态失败' }
  finally { saving.value = false }
}

watch(() => [props.userId, props.role], load, { immediate: true })
</script>
