import type { UsageLog } from '@/types'
import { BILLING_MODE_VIDEO } from './billingMode'

type UsageTpsRow = Partial<
  Pick<UsageLog, 'output_tokens' | 'duration_ms' | 'first_token_ms' | 'image_count' | 'image_output_tokens' | 'billing_mode'>
>

/**
 * 输出速度 TPS（tokens/s），用于用量明细"延迟"列。
 *
 * 有首字数据（流式）时只算首字之后的生成窗口：output_tokens ÷ (duration_ms − first_token_ms)，
 * 即常说的 output speed，不受排队和首字等待影响；
 * 无首字数据（非流式）时退化为 output_tokens ÷ duration_ms，包含首字等待，数值会偏低。
 * 图片/视频请求的 token 反映不了文本生成速度，不计算；无输出或耗时无效时返回 null。
 *
 * 注意 output_tokens 含推理 token，而首字之前可能已有推理耗时（OpenAI 首 token 口径为
 * 「真实可见输出」openai_ttft_mode=visible 时尤其明显），这类请求的 TPS 会偏高。
 */
export const usageOutputTps = (row: UsageTpsRow | null | undefined): number | null => {
  const outputTokens = row?.output_tokens ?? 0
  const durationMs = row?.duration_ms ?? 0
  if (outputTokens <= 0 || durationMs <= 0) return null
  if ((row?.image_count ?? 0) > 0 || (row?.image_output_tokens ?? 0) > 0 || row?.billing_mode === BILLING_MODE_VIDEO) {
    return null
  }
  const generationMs = row?.first_token_ms != null ? durationMs - row.first_token_ms : durationMs
  if (generationMs <= 0) return null
  return outputTokens / (generationMs / 1000)
}

/** "30.8 t/s"；100 t/s 及以上取整。不可计算时返回 null，由调用方显示占位符。 */
export const formatUsageOutputTps = (row: UsageTpsRow | null | undefined): string | null => {
  const tps = usageOutputTps(row)
  if (tps == null) return null
  return `${tps >= 100 ? Math.round(tps) : tps.toFixed(1)} t/s`
}
