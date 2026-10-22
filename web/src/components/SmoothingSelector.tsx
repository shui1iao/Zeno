import { SlidingSelector } from './SlidingSelector'

export type SmoothingMode = 'raw' | 'smooth'

const smoothingOptions = [
  { value: 'raw' as const, content: '原始' },
  { value: 'smooth' as const, content: '平滑' },
]

/** “原始 / 平滑” switch for latency charts, built on the shared compact SlidingSelector. */
export function SmoothingSelector({ value, onChange, ariaLabel = '延迟曲线平滑' }: { value: SmoothingMode; onChange: (value: SmoothingMode) => void; ariaLabel?: string }) {
  return (
    <SlidingSelector
      ariaLabel={ariaLabel}
      role="group"
      className="detail-range-row sliding-selector--compact smoothing-selector"
      options={smoothingOptions}
      value={value}
      onChange={onChange}
    />
  )
}
