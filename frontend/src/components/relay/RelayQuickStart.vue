<template>
  <section class="relay-connect" aria-label="API 接入示例">
    <div class="relay-connect-top"><div><span class="relay-eyebrow">QUICK START</span><h2>从第一次调用开始</h2></div><Icon name="terminal" size="lg" /></div>
    <div class="relay-protocols" role="group" aria-label="选择接口协议">
      <button v-for="item in protocols" :key="item.id" type="button" :aria-pressed="protocol === item.id" :class="{ selected: protocol === item.id }" @click="protocol = item.id">{{ item.label }}</button>
    </div>
    <div class="relay-endpoint-row"><div class="min-w-0"><span class="relay-code-label">BASE URL · {{ protocol === 'openai' ? 'OpenAI SDK' : 'Anthropic SDK' }}</span><code>{{ sdkEndpoint || '请管理员配置有效的 API 地址' }}</code></div><button type="button" :disabled="!sdkEndpoint" aria-label="复制基础地址" title="复制基础地址" @click="copy(sdkEndpoint, 'address')"><Icon :name="copiedItem === 'address' ? 'check' : 'copy'" size="sm" /></button></div>
    <div class="relay-code-top"><span>Bash · 请求示例</span><button type="button" :disabled="!endpoint" @click="copy(snippet, 'code')"><Icon :name="copiedItem === 'code' ? 'check' : 'copy'" size="sm" />{{ copiedItem === 'code' ? '已复制' : '复制代码' }}</button></div>
    <pre class="relay-code"><code>{{ snippet }}</code></pre>
    <div class="relay-connect-note"><Icon name="infoCircle" size="sm" /><p>把 <code>YOUR_API_KEY</code> 和 <code>MODEL_ID</code> 换成你的密钥及该分组可用的模型。示例未发起请求。</p></div>
    <p v-if="copyMessage" class="relay-copy-message" role="status">{{ copyMessage }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import Icon from '@/components/icons/Icon.vue'
import { useRelayEndpoint } from '@/composables/useRelayEndpoint'
import { useClipboard } from '@/composables/useClipboard'

const { endpoint } = useRelayEndpoint()
const { copyToClipboard } = useClipboard()
const protocol = ref<'openai' | 'anthropic'>('openai')
const protocols = [{ id: 'openai' as const, label: 'OpenAI 兼容' }, { id: 'anthropic' as const, label: 'Anthropic' }]
const sdkEndpoint = computed(() => protocol.value === 'openai' ? endpoint.value : endpoint.value.replace(/\/v1$/, ''))
const shellQuote = (value: string) => "'" + value.replace(/'/g, "'\"'\"'") + "'"
const snippet = computed(() => {
  const headers = protocol.value === 'openai'
    ? '  -H "Authorization: Bearer YOUR_API_KEY"'
    : '  -H "x-api-key: YOUR_API_KEY" \\\n  -H "anthropic-version: 2023-06-01"'
  const resource = protocol.value === 'openai' ? '/chat/completions' : '/messages'
  return `curl ${shellQuote(endpoint.value + resource)} \\\n${headers} \\\n  -H "Content-Type: application/json" \\\n  -d '{\n    "model": "MODEL_ID",\n    "max_tokens": 128,\n    "messages": [{"role": "user", "content": "你好"}]\n  }'`
})
const copiedItem = ref('')
const copyMessage = ref('')
let resetTimer: ReturnType<typeof setTimeout> | undefined
async function copy(value: string, item: string) {
  if (!value) return
  try {
    const ok = await copyToClipboard(value, item === 'address' ? '接入地址已复制' : '请求示例已复制')
    copiedItem.value = ok ? item : ''
    copyMessage.value = ok ? (item === 'address' ? '接入地址已复制。' : '代码已复制，请替换密钥与模型名称后运行。') : '复制失败，请选中内容手动复制。'
  } catch {
    copyMessage.value = '浏览器未允许复制，请选中内容手动复制。'
  }
  clearTimeout(resetTimer)
  resetTimer = setTimeout(() => { copiedItem.value = ''; copyMessage.value = '' }, 4000)
}
onBeforeUnmount(() => clearTimeout(resetTimer))
</script>
