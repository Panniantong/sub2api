/** Lazily loaded HTML of one gallery item; `invalid` means the output holds no HTML/SVG. */
export interface PelicanBody {
  status: 'loading' | 'ready' | 'invalid' | 'error'
  html: string
}

type Translate = (key: string, named: Record<string, unknown>) => string

const EFFORTS = new Set(['minimal', 'low', 'medium', 'high', 'xhigh'])

export function pelicanEffortLabel(t: Translate, effort?: string): string {
  const key = (effort || '').trim().toLowerCase()
  if (!EFFORTS.has(key)) return ''
  return t('pelicanShowcase.reasoning', { effort: t(`pelicanShowcase.efforts.${key}`, {}) })
}

export function pelicanDurationLabel(t: Translate, latencyMs?: number): string {
  if (typeof latencyMs !== 'number' || !Number.isFinite(latencyMs) || latencyMs <= 0) return '—'
  return t('pelicanShowcase.duration', { seconds: (latencyMs / 1000).toFixed(1) })
}
