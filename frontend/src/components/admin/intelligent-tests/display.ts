import DOMPurify from 'dompurify'
import type { TestStatus, TestSubmission } from '@/api/intelligentTests'

export const statusLabels: Record<TestStatus, string> = {
  waiting: '等待测试', queued: '排队中', running: '执行中', completed: '执行完成', cancelled: '已取消', success: '执行完成（旧记录）',
  failed: '测试失败', rate_limited: '临时限流', account_error: '账号异常',
  model_error: '模型不可用', request_error: '请求参数异常', network_error: '连接或上游异常', suspected_degradation: '旧规则未通过 · 待复核'
}
export const answerLabels = { correct: '最终答案正确', incorrect: '最终答案不一致', undetermined: '答案需复核', not_evaluated: '内容未自动评估' }
export const formatLabels = { compliant: '格式符合', non_compliant: '格式有差异', not_required: '无需固定格式', not_evaluated: '格式未评估' }
export function testSubmissionMessage(data: TestSubmission) {
  const reused = data.reused_count ?? (data.reused ? data.records.length : 0)
  const created = data.created_count ?? (data.records.length - reused)
  return reused ? `新建 ${created} 个任务，复用 ${reused} 个已有任务；可查看任务状态` : `已提交 ${created} 个测试任务`
}
export const isTextTestModel = (model: string) => !/^(?:gpt-image|dall-e|grok-imagine|tts-|whisper|sora)/i.test(model)
export const testNames: Record<string, string> = { pelican: '鹈鹕测试', candy: '糖果测试' }
export const testName = (type: string) => testNames[type] ?? type
export const isPending = (status?: string) => status === 'queued' || status === 'running'
export const currentEvaluatorVersion = 3
export const isModernAssessment = (version?: number) => version !== undefined && version >= 2 && version <= currentEvaluatorVersion
export function testTime(value?: string | null): string {
  if (!value) return '尚未测试'
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString('zh-CN', { hour12: false }) : '—'
}
export function testImageURL(value?: string): string {
  if (!value || value.length > 2_000_000) return ''
  // Model output is untrusted. Never render SVG as live DOM or open the raw document.
  if (!value.trimStart().startsWith('<svg')) return ''
  const clean = DOMPurify.sanitize(value, {
    USE_PROFILES: { svg: true },
    FORBID_TAGS: ['script', 'style', 'foreignObject', 'image', 'use', 'a', 'animate', 'animateMotion', 'animateTransform', 'set'],
    FORBID_ATTR: ['href', 'xlink:href', 'style']
  })
  if (!clean.includes('<svg')) return ''
  return `data:image/svg+xml;charset=utf-8,${encodeURIComponent(clean)}`
}
