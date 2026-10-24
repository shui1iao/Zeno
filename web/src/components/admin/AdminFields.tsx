import { type CSSProperties, type ReactNode, useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useAnchoredPopover } from '../../hooks/useAnchoredPopover'
import { useAnchoredPopoverPosition as usePopoverPosition } from '../../hooks/useAnchoredPopoverPosition'
import { OverlaySurface } from '../OverlaySurface'
import { focusInPopover } from '../../lib/overlayFocus'

export { calculateAnchoredPopoverStyle, measureAnchoredPopoverHeight } from '../../lib/anchoredPopover'

export function adminPopoverExpanded(open: boolean, disabled: boolean): boolean {
  return open && !disabled
}

type AnchoredPopoverVariant = 'calendar' | 'select'

function useAnchoredPopoverPosition({ open, disabled, triggerRef, popoverRef, variant, optionCount = 0, refreshKey = '' }: {
  open: boolean
  disabled: boolean
  triggerRef: React.RefObject<HTMLButtonElement | null>
  popoverRef: React.RefObject<HTMLDivElement | null>
  variant: AnchoredPopoverVariant
  optionCount?: number
  refreshKey?: string
}): CSSProperties {
  const margin = 12
  const width = useCallback((rect: { width: number }, viewportWidth: number) => {
    const touchCalendar = variant === 'calendar' && window.matchMedia('(pointer: coarse), (max-width: 767px)').matches
    const availableWidth = Math.max(0, viewportWidth - margin * 2)
    return variant === 'calendar'
      ? Math.min(touchCalendar ? 312 : 340, availableWidth, Math.max(328, rect.width))
      : Math.min(Math.max(rect.width, 160), availableWidth)
  }, [variant])
  const position = usePopoverPosition({ open: open && !disabled, triggerRef, popoverRef, width, fallbackHeight: variant === 'calendar' ? 354 : Math.min(260, optionCount * 40 + 12), margin, refreshKey })
  return position.style
}

export function AdminDateField({ name, label, defaultValue = '', defaultPermanent = false, disabled = false, permanentLabel, className = '' }: { name: string; label: string; defaultValue?: string | null; defaultPermanent?: boolean; disabled?: boolean; permanentLabel?: string; className?: string }) {
  const [value, setValue] = useState(defaultValue ?? '')
  const [permanent, setPermanent] = useState(defaultPermanent)
  const [month, setMonth] = useState(() => adminDateMonthStart(defaultValue))
  const [open, setOpen] = useState(false)
  const [openPanel, setOpenPanel] = useState<'year' | 'month' | null>(null)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const popoverRef = useRef<HTMLDivElement | null>(null)
  const dismissPopover = useCallback(() => {
    setOpen(false)
    setOpenPanel(null)
  }, [])
  const layer = useAnchoredPopover(open && !disabled, triggerRef, popoverRef, dismissPopover, 'calendar')
  const selectedDate = parseAdminDateValue(value)
  const today = new Date()
  const visibleYear = month.getFullYear()
  const visibleMonth = month.getMonth()
  const yearOptions = adminDateYearOptions(visibleYear)
  const daysInMonth = new Date(month.getFullYear(), month.getMonth() + 1, 0).getDate()
  const leadingBlankDays = (new Date(month.getFullYear(), month.getMonth(), 1).getDay() + 6) % 7
  const calendarCells = [
    ...Array.from({ length: leadingBlankDays }, (_, index) => ({ key: `blank-${index}`, day: null })),
    ...Array.from({ length: daysInMonth }, (_, index) => ({ key: `day-${index + 1}`, day: index + 1 })),
  ]
  const popoverStyle = useAnchoredPopoverPosition({
    open: layer.active,
    disabled,
    triggerRef,
    popoverRef,
    variant: 'calendar',
    refreshKey: `${visibleYear}-${visibleMonth}-${openPanel ?? 'days'}`,
  })
  const pickDate = (date: Date) => {
    setValue(formatAdminDateValue(date))
    setPermanent(false)
    setMonth(new Date(date.getFullYear(), date.getMonth(), 1))
    setOpen(false)
    setOpenPanel(null)
  }
  const shiftMonth = (delta: number) => {
    setMonth((current) => new Date(current.getFullYear(), current.getMonth() + delta, 1))
    setOpenPanel(null)
  }
  const selectYear = (year: number) => {
    setMonth((current) => new Date(year, current.getMonth(), 1))
    setOpenPanel(null)
  }
  const selectMonth = (nextMonth: number) => {
    setMonth((current) => new Date(current.getFullYear(), nextMonth, 1))
    setOpenPanel(null)
  }
  const clearDate = () => {
    setValue('')
    setPermanent(false)
    setOpen(false)
    setOpenPanel(null)
  }
  const togglePermanent = () => {
    setValue('')
    setPermanent((current) => !current)
    setOpen(false)
    setOpenPanel(null)
  }

  useEffect(() => {
    setValue(defaultValue ?? '')
    setPermanent(defaultPermanent)
    setMonth(adminDateMonthStart(defaultValue))
    setOpen(false)
    setOpenPanel(null)
  }, [defaultValue, defaultPermanent])

  useEffect(() => {
    if (!disabled) return
    setOpen(false)
    setOpenPanel(null)
  }, [disabled])

  const moveDayFocus = (event: React.KeyboardEvent<HTMLButtonElement>, date: Date) => {
    const delta = event.key === 'ArrowLeft' ? -1 : event.key === 'ArrowRight' ? 1 : event.key === 'ArrowUp' ? -7 : event.key === 'ArrowDown' ? 7 : event.key === 'Home' ? -((date.getDay() + 6) % 7) : event.key === 'End' ? 6 - ((date.getDay() + 6) % 7) : null
    if (delta === null && event.key !== 'PageUp' && event.key !== 'PageDown') return
    event.preventDefault()
    const next = new Date(date)
    if (delta !== null) next.setDate(next.getDate() + delta)
    else {
      const day = next.getDate()
      next.setDate(1)
      next.setMonth(next.getMonth() + (event.key === 'PageUp' ? -1 : 1))
      next.setDate(Math.min(day, new Date(next.getFullYear(), next.getMonth() + 1, 0).getDate()))
    }
    setMonth(new Date(next.getFullYear(), next.getMonth(), 1))
    requestAnimationFrame(() => { const root = popoverRef.current; if (root) focusInPopover(root, root.querySelector<HTMLElement>(`[data-date="${formatAdminDateValue(next)}"]`)) })
  }
  const calendar = layer.present ? (
    <OverlaySurface ref={popoverRef} className="admin-date-popover" role="dialog" aria-label={`${label}日历`} style={popoverStyle} tabIndex={-1} data-anchored-overlay="" data-overlay-state={layer.state} onAnimationEnd={event => { if (event.target === event.currentTarget && layer.state === 'closed') layer.finishExit() }} inert={!layer.active} aria-hidden={!layer.active || undefined}>
      <div className="admin-date-calendar-header">
        <button type="button" aria-label="上个月" onClick={() => shiftMonth(-1)}>‹</button>
        <div className="admin-date-current" aria-label="选择年月">
          <button className="admin-date-current-button" type="button" aria-expanded={openPanel === 'year'} onClick={() => setOpenPanel((current) => (current === 'year' ? null : 'year'))}>{visibleYear} 年</button>
          <button className="admin-date-current-button" type="button" aria-expanded={openPanel === 'month'} onClick={() => setOpenPanel((current) => (current === 'month' ? null : 'month'))}>{visibleMonth + 1} 月</button>
        </div>
        <button type="button" aria-label="下个月" onClick={() => shiftMonth(1)}>›</button>
      </div>
      {openPanel === 'year' && (
        <div className="admin-date-option-panel admin-date-year-panel" aria-label="年份选项">
          {yearOptions.map((year) => (
            <button className={year === visibleYear ? 'is-selected' : ''} type="button" key={year} onClick={() => selectYear(year)}>{year}</button>
          ))}
        </div>
      )}
      {openPanel === 'month' && (
        <div className="admin-date-option-panel admin-date-month-panel" aria-label="月份选项">
          {adminDateMonthOptions.map((option) => (
            <button className={option.value === visibleMonth ? 'is-selected' : ''} type="button" key={option.value} onClick={() => selectMonth(option.value)}>{option.label}</button>
          ))}
        </div>
      )}
      {openPanel === null && (
        <>
          <div className="admin-date-weekdays" aria-hidden="true">
            {adminDateWeekdays.map((weekday) => <span key={weekday}>{weekday}</span>)}
          </div>
          <div className="admin-date-grid">
            {calendarCells.map((cell) => {
              if (cell.day === null) return <span className="admin-date-empty" key={cell.key} />
              const date = new Date(month.getFullYear(), month.getMonth(), cell.day)
              const dateValue = formatAdminDateValue(date)
              const isSelected = selectedDate ? dateValue === formatAdminDateValue(selectedDate) : false
              const isToday = dateValue === formatAdminDateValue(today)
              return (
                <button className={`${isSelected ? 'is-selected' : ''}${isToday ? ' is-today' : ''}`} type="button" key={cell.key} data-date={dateValue} aria-label={dateValue} onKeyDown={event => moveDayFocus(event, date)} onClick={() => pickDate(date)}>
                  {cell.day}
                </button>
              )
            })}
          </div>
          <div className="admin-date-actions">
            <button type="button" onClick={clearDate}>清空</button>
            <button type="button" onClick={() => pickDate(today)}>今天</button>
          </div>
        </>
      )}
    </OverlaySurface>
  ) : null

  return (
    <div className={['admin-form-control admin-date-field', className].filter(Boolean).join(' ')}>
      <span>{label}</span>
      <input type="hidden" name={name} value={value} disabled={disabled} />
      {permanentLabel && <input type="hidden" name={`${name.replace(/-date$/, '')}-permanent`} value={permanent ? '1' : '0'} disabled={disabled} />}
      <div className="admin-date-picker">
        <button ref={triggerRef} className="admin-date-trigger" type="button" aria-expanded={adminPopoverExpanded(open, disabled)} disabled={disabled} onClick={() => setOpen((current) => {
          if (current) setOpenPanel(null)
          return !current
        })}>
          <span className={value ? '' : 'is-placeholder'}>{value || 'YYYY-MM-DD'}</span>
          <CalendarIcon />
        </button>
        {permanentLabel && <button className={`admin-date-permanent${permanent ? ' is-active' : ''}`} type="button" aria-pressed={permanent} disabled={disabled} onClick={togglePermanent}>{permanentLabel}</button>}
        {calendar && (typeof document === 'undefined' ? calendar : createPortal(calendar, document.body))}
      </div>
    </div>
  )
}

export function AdminSegmentedField({ name, label, options, value, defaultValue, disabled = false, onChange, className = '' }: { name: string; label: string; options: Array<{ value: string; label: string }>; value?: string; defaultValue?: string; disabled?: boolean; onChange?: (value: string) => void; className?: string }) {
  const [internalValue, setInternalValue] = useState(defaultValue ?? options[0]?.value ?? '')
  const [open, setOpen] = useState(false)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const popoverRef = useRef<HTMLDivElement | null>(null)
  const dismissPopover = useCallback(() => setOpen(false), [])
  const layer = useAnchoredPopover(open && !disabled, triggerRef, popoverRef, dismissPopover, 'listbox')
  const selectedValue = value ?? internalValue
  const selectedOption = options.find((option) => option.value === selectedValue) ?? options[0]
  const popoverStyle = useAnchoredPopoverPosition({ open: layer.active, disabled, triggerRef, popoverRef, variant: 'select', optionCount: options.length })
  const setSelectedValue = (nextValue: string) => {
    if (disabled) return
    if (value === undefined) setInternalValue(nextValue)
    onChange?.(nextValue)
    setOpen(false)
  }

  useEffect(() => {
    if (disabled) setOpen(false)
  }, [disabled])

  const classes = ['admin-form-control', 'admin-segmented-field admin-select-menu-field', className, disabled ? 'is-disabled' : ''].filter(Boolean).join(' ')
  const popover = layer.present ? (
    <OverlaySurface ref={popoverRef} className="admin-select-popover" data-field={name} role="listbox" aria-label={`${label}选项`} style={popoverStyle} tabIndex={-1} data-anchored-overlay="" data-overlay-state={layer.state} onAnimationEnd={event => { if (event.target === event.currentTarget && layer.state === 'closed') layer.finishExit() }} inert={!layer.active} aria-hidden={!layer.active || undefined}>
      {options.map((option) => (
        <button key={option.value} type="button" role="option" tabIndex={-1} aria-selected={selectedValue === option.value} data-active={selectedValue === option.value} onClick={() => setSelectedValue(option.value)}>
          <span>{option.label}</span>
        </button>
      ))}
    </OverlaySurface>
  ) : null
  return (
    <div className={classes}>
      <span>{label}</span>
      <input type="hidden" name={name} value={selectedValue} disabled={disabled} />
      <button ref={triggerRef} className="admin-select-trigger" type="button" aria-haspopup="listbox" aria-expanded={adminPopoverExpanded(open, disabled)} disabled={disabled} onClick={() => setOpen((current) => !current)}>
        <span>{selectedOption?.label ?? selectedValue}</span>
        <ChevronDownIcon expanded={adminPopoverExpanded(open, disabled)} />
      </button>
      {popover && (typeof document === 'undefined' ? popover : createPortal(popover, document.body))}
    </div>
  )
}

type AdminExpandedCheckListOption = { value: string; label: string }

interface AdminExpandedCheckListProps {
  options: AdminExpandedCheckListOption[]
  value: string[]
  onChange: (value: string[]) => void
  title?: string
  panelLabel?: string
  emptyText?: string
  allowEmpty?: boolean
  renderRight?: (option: AdminExpandedCheckListOption) => ReactNode
}

export function AdminExpandedCheckList({ options, value, onChange, title = '已选', panelLabel = '选择项目', emptyText = '暂无可选项', allowEmpty = true, renderRight }: AdminExpandedCheckListProps) {
  const [expanded, setExpanded] = useState(false)
  const optionValues = new Set(options.map((option) => option.value))
  const normalizedValue = Array.from(new Set((Array.isArray(value) ? value : []).filter((item) => optionValues.has(item))))
  const selected = new Set(normalizedValue)
  const selectedLabels = options.filter((option) => selected.has(option.value)).map((option) => option.label)
  const selectionSummary = selectedLabels.length > 0 ? selectedLabels.join('、') : '未选择'
  const allSelected = options.length > 0 && normalizedValue.length === options.length
  const toggleValue = (optionValue: string, checked: boolean) => {
    if (checked) {
      onChange(Array.from(new Set([...normalizedValue, optionValue])))
      return
    }
    if (!allowEmpty && normalizedValue.length <= 1) return
    onChange(normalizedValue.filter((item) => item !== optionValue))
  }

  return (
    <section className="admin-assignment-field" data-open={expanded}>
      <button className="admin-assignment-field__header" type="button" aria-expanded={expanded} onClick={() => setExpanded((current) => !current)}>
        <span className="admin-assignment-field__heading">
          <strong>{title}</strong>
          <span title={selectionSummary}>{selectionSummary}</span>
        </span>
        <span className="admin-assignment-field__meta">
          <span>{normalizedValue.length} / {options.length}</span>
          <ChevronDownIcon expanded={expanded} />
        </span>
      </button>
      {expanded && (
        <div className="admin-assignment-field__body">
          <div className="admin-assignment-field__toolbar">
            <span>{panelLabel}</span>
            {options.length > 0 && (!allSelected || allowEmpty) && (
              <button className="admin-assignment-field__bulk" type="button" onClick={() => onChange(allSelected ? [] : options.map((option) => option.value))}>
                {allSelected ? '清空' : '全选'}
              </button>
            )}
          </div>
          <div className="admin-assignment-field__list" role="list">
            {options.length === 0 && <div className="admin-assignment-field__empty">{emptyText}</div>}
            {options.map((option) => {
              const checked = selected.has(option.value)
              return (
                <div className="admin-assignment-field__row" role="listitem" data-checked={checked} key={option.value}>
                  <button className="admin-assignment-field__option" type="button" title={option.label} aria-pressed={checked} disabled={!allowEmpty && checked && normalizedValue.length <= 1} onClick={() => toggleValue(option.value, !checked)}>
                    <span className="admin-assignment-field__check" aria-hidden="true">{checked && <SelectionCheckIcon />}</span>
                    <span className="admin-assignment-field__label">{option.label}</span>
                  </button>
                  {checked && renderRight && <div className="admin-assignment-field__side">{renderRight(option)}</div>}
                </div>
              )
            })}
          </div>
        </div>
      )}
    </section>
  )
}

function SelectionCheckIcon() {
  return <svg viewBox="0 0 14 14"><path d="m3 7.2 2.3 2.2L11 4" /></svg>
}

const adminDateWeekdays = ['一', '二', '三', '四', '五', '六', '日']
const adminDateMonthOptions = Array.from({ length: 12 }, (_, index) => ({ value: index, label: `${index + 1} 月` }))

function adminDateYearOptions(visibleYear: number): number[] {
  const currentYear = new Date().getFullYear()
  const start = Math.min(currentYear - 2, visibleYear - 4)
  const end = Math.max(currentYear + 10, visibleYear + 6)
  return Array.from({ length: end - start + 1 }, (_, index) => start + index)
}

function parseAdminDateValue(value?: string | null): Date | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec((value ?? '').trim())
  if (!match) return null
  const year = Number(match[1])
  const month = Number(match[2]) - 1
  const day = Number(match[3])
  const date = new Date(year, month, day)
  if (date.getFullYear() !== year || date.getMonth() !== month || date.getDate() !== day) return null
  return date
}

function formatAdminDateValue(date: Date): string {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function adminDateMonthStart(value?: string | null): Date {
  const parsed = parseAdminDateValue(value)
  const source = parsed ?? new Date()
  return new Date(source.getFullYear(), source.getMonth(), 1)
}

function ChevronDownIcon({ expanded }: { expanded: boolean }) {
  return (
    <svg className={expanded ? 'is-expanded' : ''} viewBox="0 0 24 24" aria-hidden="true">
      <path d="m6 9 6 6 6-6" />
    </svg>
  )
}

function CalendarIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M7 2.75a.75.75 0 0 1 .75.75V5h8.5V3.5a.75.75 0 0 1 1.5 0V5H19a3 3 0 0 1 3 3v10.25A3.75 3.75 0 0 1 18.25 22H5.75A3.75 3.75 0 0 1 2 18.25V8a3 3 0 0 1 3-3h1.25V3.5A.75.75 0 0 1 7 2.75ZM3.5 10v8.25a2.25 2.25 0 0 0 2.25 2.25h12.5a2.25 2.25 0 0 0 2.25-2.25V10h-17ZM5 6.5A1.5 1.5 0 0 0 3.5 8v.5h17V8A1.5 1.5 0 0 0 19 6.5H5Z" />
    </svg>
  )
}
