import { slidingSelectorStyle } from '../lib/slidingSelector'

interface HistoryRangeOption {
  value: string
  label: string
}

interface HistoryRangeSelectorProps {
  ariaLabel: string
  options: ReadonlyArray<HistoryRangeOption>
  value: string
  onChange: (value: string) => void
  className?: string
}

export function HistoryRangeSelector({ ariaLabel, options, value, onChange, className = '' }: HistoryRangeSelectorProps) {
  const activeIndex = Math.max(0, options.findIndex((option) => option.value === value))
  const activeValue = options[activeIndex]?.value
  const style = slidingSelectorStyle(options.length, activeIndex)

  return (
    <div className={`sliding-selector detail-range-row${className ? ` ${className}` : ''}`} role="group" aria-label={ariaLabel} style={style}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          className={activeValue === option.value ? 'is-active' : ''}
          aria-pressed={activeValue === option.value}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  )
}
