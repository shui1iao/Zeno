import { type KeyboardEvent as ReactKeyboardEvent, useEffect, useId, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { DashboardHeader } from './DashboardHeader'
import { ServerFlag } from './ServerFlag'
import { availableCurrencyOptions, formatCurrencyAmount, normalizeCurrencyCode, normalizeCurrencyRates, type CurrencyCode, type CurrencyRates } from '../lib/currency'
import { defaultSettings } from '../lib/appearance'
import type { AdminSettings, AdminTheme } from '../types'
import { OverlaySurface } from './OverlaySurface'

function compactBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit += 1 }
  const amount = unit === 0 ? size.toFixed(0) : size.toFixed(2).replace(/\.00$/, '').replace(/(\.\d)0$/, '$1')
  return `${amount} ${units[unit]}`
}

function compactRateParts(value: number): { value: string; unit: string } {
  const [amount, unit = 'B'] = compactBytes(value).split(' ')
  return { value: amount, unit: `${unit}/s` }
}

// Cumulative totals always show two decimals so that the two counters line up.
function totalBytesParts(value: number): { value: string; unit: string } {
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  if (!Number.isFinite(value) || value <= 0) return { value: '0', unit: 'B' }
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit += 1 }
  return { value: unit === 0 ? size.toFixed(0) : size.toFixed(2), unit: units[unit] }
}

interface HomeOverviewPanelProps {
  settings?: AdminSettings
  totalCount: number
  onlineCount: number
  offlineCount: number
  monthlyCost: number
  displayCurrency?: CurrencyCode
  exchangeRates?: CurrencyRates
  currencyOptions?: ReadonlyArray<{ value: CurrencyCode; label: string; shortLabel: string; flagCode: string }>
  onCurrencyChange?: (currency: CurrencyCode) => void
  totalUp: number
  totalDown: number
  upSpeed: number
  downSpeed: number
}

interface HomeTopPanelProps extends HomeOverviewPanelProps {
  onHome: () => void
  onAdmin: () => void
  onAdminIntent?: () => void
  onThemeChange?: (theme: AdminTheme) => void
  onBackgroundToggle?: () => void
  backgroundEnabled?: boolean
}

export function HomeTopPanel({ settings = defaultSettings, onHome, onAdmin, onAdminIntent, onThemeChange, onBackgroundToggle, backgroundEnabled = false, ...overview }: HomeTopPanelProps) {
  const headerCurrency = normalizeCurrencyCode(overview.displayCurrency ?? 'CNY')
  const headerExchangeRates = normalizeCurrencyRates(overview.exchangeRates ?? { CNY: 1 })
  const headerCurrencyOptions = overview.currencyOptions ?? availableCurrencyOptions(headerExchangeRates)
  return (
    <section className="home-top-card home-overview-card" aria-label="homepage control panel">
      <DashboardHeader
        settings={settings}
        onHome={onHome}
        onAdmin={onAdmin}
        onAdminIntent={onAdminIntent}
        leadingAction={<HomeCurrencyMenu value={headerCurrency} options={headerCurrencyOptions} onChange={overview.onCurrencyChange} />}
        onThemeChange={onThemeChange}
        onBackgroundToggle={onBackgroundToggle}
        backgroundEnabled={backgroundEnabled}
      />
      <HomeOverviewPanel settings={settings} {...overview} />
    </section>
  )
}

export function HomeRegionFilter({ regions, activeRegion, onChange }: { regions: string[]; activeRegion: string; onChange: (region: string) => void }) {
  const options = ['ALL', ...regions]
  return (
    <nav className="region-filter-bar" aria-label="服务器地区筛选">
      <div className="region-filter-buttons" aria-label="服务器地区">
        {options.map((region) => (
          <button
            key={region}
            type="button"
            className={region === 'ALL' ? 'region-filter-all' : undefined}
            data-value={region}
            aria-label={region === 'ALL' ? '全部' : `筛选 ${region} 地区`}
            title={region === 'ALL' ? undefined : region}
            aria-pressed={region === activeRegion}
            onClick={() => onChange(region)}
          >
            {region === 'ALL' ? <span className="region-all-text">全部</span> : <ServerFlag countryCode={region} />}
          </button>
        ))}
      </div>
    </nav>
  )
}

function HomeTrafficDirectionIcon({ direction }: { direction: 'upload' | 'download' }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d={direction === 'upload' ? 'M12 16V4' : 'M12 4v12'} />
      <path d={direction === 'upload' ? 'm7 9 5-5 5 5' : 'm7 11 5 5 5-5'} />
      <path d="M5 20h14" />
    </svg>
  )
}

function HomeMonthlyCostIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <rect x="3" y="6" width="18" height="12" rx="2" />
      <circle cx="12" cy="12" r="2.5" />
      <path d="M7 9h.01M17 15h.01" />
    </svg>
  )
}

function HomeTrafficTotalIcon({ direction }: { direction: 'upload' | 'download' }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M4 14v4a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-4" />
      <path d="M7 17h.01M10 17h.01" />
      <path d={direction === 'upload' ? 'M12 12V3' : 'M12 3v9'} />
      <path d={direction === 'upload' ? 'm8.5 6.5 3.5-3.5 3.5 3.5' : 'm8.5 8.5 3.5 3.5 3.5-3.5'} />
    </svg>
  )
}

interface HomeTrafficSummaryProps {
  direction: 'upload' | 'download'
  kind: 'rate' | 'total'
  rate: ReturnType<typeof compactRateParts>
  total: ReturnType<typeof totalBytesParts>
}

function HomeTrafficSummary({ direction, kind, rate, total }: HomeTrafficSummaryProps) {
  const isUpload = direction === 'upload'
  if (kind === 'rate') {
    return (
      <div className={`home-summary__metric home-summary__metric--rate home-summary__metric--${direction}`} aria-label={isUpload ? 'upload rate' : 'download rate'}>
        <div className="home-summary__metric-label">
          <span className="home-summary__metric-icon"><HomeTrafficDirectionIcon direction={direction} /></span>
          <span>{isUpload ? '上传' : '下载'}</span>
        </div>
        <div className="home-summary__metric-value home-summary__metric-value--rate">
          <strong>{rate.value}</strong>
          <span>{rate.unit}</span>
        </div>
      </div>
    )
  }
  return (
    <div className={`home-summary__metric home-summary__metric--total home-summary__metric--${direction}`} aria-label={isUpload ? 'total sent' : 'total received'}>
      <div className="home-summary__metric-label">
        <span className="home-summary__metric-icon home-summary__metric-icon--total"><HomeTrafficTotalIcon direction={direction} /></span>
        <span>{isUpload ? '累计发送' : '累计接收'}</span>
      </div>
      <div className="home-summary__metric-value home-summary__metric-value--total">
        <strong>{total.value}</strong>
        <span>{total.unit}</span>
      </div>
    </div>
  )
}

interface HomeCurrencyMenuProps {
  value: CurrencyCode
  options: ReadonlyArray<{ value: CurrencyCode; label: string; shortLabel: string; flagCode: string }>
  onChange?: (currency: CurrencyCode) => void
}

interface HomeCurrencyMenuPosition {
  top: number
  left: number
}

const homeCurrencyMenuWidth = 248
const homeCurrencyMenuMargin = 8

function resolveHomeCurrencyMenuPosition(button: HTMLButtonElement, viewportWidth: number): HomeCurrencyMenuPosition {
  const rect = button.getBoundingClientRect()
  const width = Math.min(homeCurrencyMenuWidth, viewportWidth - homeCurrencyMenuMargin * 2)
  const preferredLeft = viewportWidth <= 767
    ? rect.left + rect.width / 2 - width / 2
    : rect.right - width
  return {
    top: rect.bottom + 7,
    left: Math.max(homeCurrencyMenuMargin, Math.min(viewportWidth - width - homeCurrencyMenuMargin, preferredLeft)),
  }
}

function HomeCurrencyMenu({ value, options, onChange }: HomeCurrencyMenuProps) {
  const [open, setOpen] = useState(false)
  const [menuPosition, setMenuPosition] = useState<HomeCurrencyMenuPosition | null>(null)
  const menuId = useId()
  const rootRef = useRef<HTMLDivElement | null>(null)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const popoverRef = useRef<HTMLDivElement | null>(null)
  const optionRefs = useRef<Array<HTMLButtonElement | null>>([])
  const selectedIndex = Math.max(0, options.findIndex((option) => option.value === value))
  const selectedOption = options[selectedIndex]

  useEffect(() => {
    if (!open) return undefined
    const frame = window.requestAnimationFrame(() => optionRefs.current[selectedIndex]?.focus())
    const handlePointerDown = (event: PointerEvent) => {
      if (rootRef.current?.contains(event.target as Node)) return
      if (popoverRef.current?.contains(event.target as Node)) return
      setOpen(false)
      setMenuPosition(null)
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      setOpen(false)
      setMenuPosition(null)
      triggerRef.current?.focus()
    }
    const updatePosition = () => {
      const button = triggerRef.current
      if (button) setMenuPosition(resolveHomeCurrencyMenuPosition(button, window.innerWidth))
    }
    updatePosition()
    document.addEventListener('pointerdown', handlePointerDown)
    document.addEventListener('keydown', handleKeyDown)
    window.addEventListener('resize', updatePosition)
    window.addEventListener('scroll', updatePosition, true)
    return () => {
      window.cancelAnimationFrame(frame)
      document.removeEventListener('pointerdown', handlePointerDown)
      document.removeEventListener('keydown', handleKeyDown)
      window.removeEventListener('resize', updatePosition)
      window.removeEventListener('scroll', updatePosition, true)
    }
  }, [open, selectedIndex])

  const selectCurrency = (currency: CurrencyCode) => {
    onChange?.(currency)
    setOpen(false)
    setMenuPosition(null)
    window.requestAnimationFrame(() => triggerRef.current?.focus())
  }

  const focusOption = (index: number) => {
    const normalizedIndex = (index + options.length) % options.length
    optionRefs.current[normalizedIndex]?.focus()
  }

  const handleOptionKeyDown = (event: ReactKeyboardEvent<HTMLButtonElement>, index: number) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowRight') {
      event.preventDefault()
      focusOption(index + 1)
    } else if (event.key === 'ArrowUp' || event.key === 'ArrowLeft') {
      event.preventDefault()
      focusOption(index - 1)
    } else if (event.key === 'Home') {
      event.preventDefault()
      focusOption(0)
    } else if (event.key === 'End') {
      event.preventDefault()
      focusOption(options.length - 1)
    } else if (event.key === 'Tab') {
      setOpen(false)
      setMenuPosition(null)
    }
  }

  const toggleMenu = () => {
    if (open) {
      setOpen(false)
      setMenuPosition(null)
      return
    }
    const button = triggerRef.current
    if (button) setMenuPosition(resolveHomeCurrencyMenuPosition(button, window.innerWidth))
    setOpen(true)
  }

  return (
    <div className="home-currency-menu" ref={rootRef}>
      <button
        ref={triggerRef}
        className="home-currency-select"
        type="button"
        title="切换首页金额单位"
        aria-label={`金额单位：${selectedOption?.label ?? value}`}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={menuId}
        onClick={toggleMenu}
        onKeyDown={(event) => {
          if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
          event.preventDefault()
          const button = triggerRef.current
          if (button) setMenuPosition(resolveHomeCurrencyMenuPosition(button, window.innerWidth))
          setOpen(true)
        }}
      >
        <span className="home-currency-select__value">{selectedOption?.shortLabel ?? value}</span>
      </button>
      {open && menuPosition && typeof document !== 'undefined' && createPortal(
        <OverlaySurface ref={popoverRef} id={menuId} className="home-currency-popover" role="listbox" aria-label="金额单位" style={menuPosition}>
          {options.map((option, index) => (
            <button
              key={option.value}
              ref={(element) => { optionRefs.current[index] = element }}
              type="button"
              role="option"
              aria-selected={option.value === value}
              data-active={option.value === value}
              onClick={() => selectCurrency(option.value)}
              onKeyDown={(event) => handleOptionKeyDown(event, index)}
            >
              <ServerFlag countryCode={option.flagCode} className="home-currency-option__flag" />
              <span className="home-currency-option__name">{option.label.replace(` ${option.value}`, '')}</span>
              <span className="home-currency-option__code">{option.shortLabel}</span>
            </button>
          ))}
        </OverlaySurface>,
        document.body,
      )}
    </div>
  )
}

export function HomeOverviewPanel({ totalCount, onlineCount, monthlyCost, displayCurrency = 'CNY', totalUp, totalDown, upSpeed, downSpeed }: HomeOverviewPanelProps) {
  const uploadRate = compactRateParts(upSpeed)
  const downloadRate = compactRateParts(downSpeed)
  const activeCurrency = normalizeCurrencyCode(displayCurrency)
  return (
    <section className="home-summary" aria-label="server overview">
      <div className="home-summary__metric home-summary__metric--status" aria-label="服务器在线摘要">
        <div className="home-summary__metric-label">
          <span className="home-summary__status-dot" aria-hidden="true" />
          <span>在线节点</span>
        </div>
        <div className="home-summary__metric-value home-summary__metric-value--status">
          <strong>{onlineCount}</strong>
          <span>/ {totalCount}</span>
        </div>
      </div>
      <div className="home-summary__metric home-summary__metric--cost" aria-label="月均消费">
        <div className="home-summary__metric-label">
          <span className="home-summary__metric-icon"><HomeMonthlyCostIcon /></span>
          <span>月均消费</span>
        </div>
        <div className="home-summary__metric-value home-summary__metric-value--cost">
          <strong>{formatCurrencyAmount(monthlyCost, activeCurrency, { fixed: true, spaced: true })}</strong>
        </div>
      </div>
      <HomeTrafficSummary direction="upload" kind="total" rate={uploadRate} total={totalBytesParts(totalUp)} />
      <HomeTrafficSummary direction="download" kind="total" rate={downloadRate} total={totalBytesParts(totalDown)} />
      <HomeTrafficSummary direction="upload" kind="rate" rate={uploadRate} total={totalBytesParts(totalUp)} />
      <HomeTrafficSummary direction="download" kind="rate" rate={downloadRate} total={totalBytesParts(totalDown)} />
    </section>
  )
}
