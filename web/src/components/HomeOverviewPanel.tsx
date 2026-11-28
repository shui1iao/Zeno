import { memo, useCallback, useId, useRef, useState, type CSSProperties } from 'react'
import { createPortal } from 'react-dom'
import { DashboardHeader } from './DashboardHeader'
import { ServerFlag } from './ServerFlag'
import HomeStatusMatrix from './HomeStatusMatrix'
import { availableCurrencyOptions, formatCurrencyAmount, normalizeCurrencyCode, normalizeCurrencyRates, type CurrencyCode, type CurrencyRates } from '../lib/currency'
import { defaultSettings } from '../lib/appearance'
import { byteUnits, scaleBytes } from '../lib/format'
import type { AdminSettings, AdminTheme, HomeCardNode } from '../types'
import { OverlaySurface } from './OverlaySurface'
import { useAnchoredPopover } from '../hooks/useAnchoredPopover'
import { useAnchoredPopoverPosition } from '../hooks/useAnchoredPopoverPosition'

// Rates trim trailing zeros (1.5 MB/s); cumulative totals always show two
// decimals so that the two counters line up.
function byteParts(value: number, trimZeros: boolean): { value: string; unit: string } {
  if (!Number.isFinite(value) || value <= 0) return { value: '0', unit: 'B' }
  const { size, power } = scaleBytes(value)
  const fixed = size.toFixed(power === 0 ? 0 : 2)
  return { value: trimZeros && power > 0 ? fixed.replace(/\.00$/, '').replace(/(\.\d)0$/, '$1') : fixed, unit: byteUnits[power] }
}

function compactRateParts(value: number): { value: string; unit: string } {
  const parts = byteParts(value, true)
  return { value: parts.value, unit: `${parts.unit}/s` }
}

function totalBytesParts(value: number): { value: string; unit: string } {
  return byteParts(value, false)
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
  /** Ordered home nodes for the status matrix and the attention summary. */
  nodes?: ReadonlyArray<HomeCardNode>
  onOpenNode?: (nodeId: string) => void
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

export type HomeView = 'card' | 'list'

export const homeViewStorageKey = 'zeno_home_view'

export function storedHomeView(): HomeView {
  // No window (SSR/tests) or blocked storage both land in the catch.
  try {
    return localStorage.getItem(homeViewStorageKey) === 'list' ? 'list' : 'card'
  } catch {
    return 'card'
  }
}

export function rememberHomeView(view: HomeView): void {
  try { localStorage.setItem(homeViewStorageKey, view) } catch { /* storage unavailable */ }
}

const homeViewOptions = [
  { value: 'card' as const, label: '卡片视图', icon: <ViewGridIcon /> },
  { value: 'list' as const, label: '列表视图', icon: <ViewListIcon /> },
]

/**
 * Two fixed columns, so the shared sliding-selector look is driven purely by its
 * CSS variables; the measuring SlidingSelector stays out of the initial bundle.
 */
function HomeViewToggle({ view, onChange }: { view: HomeView; onChange: (view: HomeView) => void }) {
  const style = { '--slider-columns': 2, '--slider-grid-columns': 2, '--slider-row': 0, '--slider-width': 'calc(100% / 2)', '--slider-shift': view === 'list' ? '50%' : '0%' } as CSSProperties
  return (
    <div className="sliding-selector sliding-selector--compact home-view-toggle" role="group" aria-label="首页视图" style={style}>
      {homeViewOptions.map((option) => (
        <button key={option.value} type="button" data-active={option.value === view} data-value={option.value} aria-pressed={option.value === view} aria-label={option.label} title={option.label} onClick={() => onChange(option.value)}>
          {option.icon}
        </button>
      ))}
    </div>
  )
}

interface HomeRegionFilterProps {
  regions: string[]
  activeRegion: string
  onChange: (region: string) => void
  /** Server count per region code plus `ALL`. */
  counts?: Readonly<Record<string, number>>
  view?: HomeView
  onViewChange?: (view: HomeView) => void
}

export const HomeRegionFilter = memo(function HomeRegionFilter({ regions, activeRegion, onChange, counts, view, onViewChange }: HomeRegionFilterProps) {
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
            {counts?.[region] !== undefined && <span className="region-count">{counts[region]}</span>}
          </button>
        ))}
      </div>
      {view && onViewChange && (
        <HomeViewToggle view={view} onChange={onViewChange} />
      )}
    </nav>
  )
})

function ViewGridIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <rect x="4" y="4" width="7" height="7" rx="1.6" />
      <rect x="13" y="4" width="7" height="7" rx="1.6" />
      <rect x="4" y="13" width="7" height="7" rx="1.6" />
      <rect x="13" y="13" width="7" height="7" rx="1.6" />
    </svg>
  )
}

function ViewListIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M9 6h11M9 12h11M9 18h11M4.5 6h.01M4.5 12h.01M4.5 18h.01" />
    </svg>
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

const currencyPopoverWidth = () => 248
const currencyPopoverAlign = (width: number) => width <= 767 ? 'center' as const : 'end' as const

function HomeCurrencyMenu({ value, options, onChange }: HomeCurrencyMenuProps) {
  const [open, setOpen] = useState(false)
  const menuId = useId()
  const rootRef = useRef<HTMLDivElement | null>(null)
  const triggerRef = useRef<HTMLButtonElement | null>(null)
  const popoverRef = useRef<HTMLDivElement | null>(null)
  const selectedIndex = Math.max(0, options.findIndex((option) => option.value === value))
  const selectedOption = options[selectedIndex]

  const dismiss = useCallback(() => setOpen(false), [])
  const layer = useAnchoredPopover(open, triggerRef, popoverRef, dismiss, 'listbox')
  const position = useAnchoredPopoverPosition({ open: layer.active, triggerRef, popoverRef, width: currencyPopoverWidth, fallbackHeight: Math.ceil(options.length / 2) * 38 + 14, margin: 8, gap: 7, align: currencyPopoverAlign })
  const selectCurrency = (currency: CurrencyCode) => {
    onChange?.(currency)
    setOpen(false)
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
        onClick={() => setOpen(current => !current)}
        onKeyDown={(event) => {
          if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
          event.preventDefault()
          setOpen(true)
        }}
      >
        <span className="home-currency-select__value">{selectedOption?.shortLabel ?? value}</span>
      </button>
      {layer.present && typeof document !== 'undefined' && createPortal(
        <OverlaySurface ref={popoverRef} id={menuId} className="home-currency-popover" role="listbox" aria-label="金额单位" style={position.style} tabIndex={-1} data-anchored-overlay="" data-side={position.side} data-overlay-state={layer.state} onAnimationEnd={event => { if (event.target === event.currentTarget && layer.state === 'closed') layer.finishExit() }} inert={!layer.active} aria-hidden={!layer.active || undefined}>
          {options.map((option) => (
            <button
              key={option.value}
              type="button"
              role="option"
              tabIndex={-1}
              aria-selected={option.value === value}
              data-active={option.value === value}
              onClick={() => selectCurrency(option.value)}
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

export const HomeOverviewPanel = memo(function HomeOverviewPanel({ totalCount, onlineCount, monthlyCost, displayCurrency = 'CNY', totalUp, totalDown, upSpeed, downSpeed, nodes, onOpenNode }: HomeOverviewPanelProps) {
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
          {nodes && <HomeStatusMatrix nodes={nodes} onOpenNode={onOpenNode} />}
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
})
