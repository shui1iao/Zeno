import { lazy, Suspense, useCallback, useEffect, useRef, useState, type ComponentType } from 'react'
import { ServerCard } from './components/ServerCard'
import { applyCustomCode } from './lib/customCode'
import { availableCurrencyOptions, billingCycleMonths, convertCurrencyAmount, normalizeCurrencyRates, rememberHomeCurrency, storedHomeCurrency, type CurrencyCode, type CurrencyRates } from './lib/currency'
import type { AdminSettings, AdminTheme, HomeCardNode } from './types'
import { DashboardHeader } from './components/DashboardHeader'
import { AdminDashboardLoadError, AdminModuleErrorBoundary } from './components/admin/AdminDashboardBoundary'
import { applyDocumentBranding, settingsForChrome, shellStyleForSettings, storedBackgroundEnabled, storedThemeOverride, useDocumentTheme } from './lib/appearance'
import { useAdminAccess } from './hooks/useAdminAccess'
import { usePublicSettings } from './hooks/usePublicSettings'
import { useDashboardRouter } from './hooks/useDashboardRouter'
import { homeRealtimeSnapshotForNodes, useSummaryController } from './hooks/useSummaryController'
import { HomeRegionFilter, HomeTopPanel } from './components/HomeOverviewPanel'
import type { AdminDashboardContainerProps } from './components/admin/AdminDashboard'
import type { NodeDetailRouteProps } from './components/NodeDetailRoute'
import type { DashboardRoute } from './lib/route'
import { prefetchNodeLatency } from './api/publicClient'

export { applyCustomCode, extractSafeCustomCSS } from './lib/customCode'
export { availableHistoryRanges, coerceHistoryRange, rangeRequiresAdmin } from './lib/historyRange'
export { loadStoredSummary, rememberSummary } from './lib/summaryCache'
export { adminTokenMaxAgeMs } from './lib/adminToken'
export { applyDocumentBranding, documentBrandingForSettings, shellStyleForSettings } from './lib/appearance'
export { isAdminUnauthorizedError } from './lib/adminSettings'
export { shouldRefreshHomeRealtimeSnapshot } from './hooks/useSummaryController'
export { HomeOverviewPanel, HomeRegionFilter, HomeTopPanel } from './components/HomeOverviewPanel'

const loadAdminDashboardModule = () => import('./components/admin/AdminDashboard')
const loadAdminDashboard = () => loadAdminDashboardModule().then((module) => ({ default: module.AdminDashboardContainer }))
const loadNodeDetailRouteModule = () => import('./components/NodeDetailRoute')
const LazyAdminDashboard = lazy(loadAdminDashboard)
const LazyNodeDetailRoute = lazy(() => loadNodeDetailRouteModule().then((module) => ({ default: module.NodeDetailRoute })))
const LazyServiceDetailRoute = lazy(() => import('./components/ServiceDetailRoute').then((module) => ({ default: module.ServiceDetailRoute })))
let preloadedAdminDashboard: ComponentType<AdminDashboardContainerProps> | null = null
let preloadedNodeDetailRoute: ComponentType<NodeDetailRouteProps> | null = null
let adminRoutePreload: Promise<void> | null = null
let nodeDetailRoutePreload: Promise<void> | null = null

export async function preloadAdminRoute(): Promise<void> {
  if (adminRoutePreload === null) {
    adminRoutePreload = loadAdminDashboardModule()
      .then((adminModule) => { preloadedAdminDashboard = adminModule.AdminDashboardContainer })
      .catch((error: unknown) => {
        adminRoutePreload = null
        throw error
      })
  }
  return adminRoutePreload
}

export async function preloadNodeDetailRoute(): Promise<void> {
  if (nodeDetailRoutePreload === null) {
    nodeDetailRoutePreload = loadNodeDetailRouteModule()
      .then((detailModule) => { preloadedNodeDetailRoute = detailModule.NodeDetailRoute })
      .catch((error: unknown) => {
        nodeDetailRoutePreload = null
        throw error
      })
  }
  return nodeDetailRoutePreload
}

export function DashboardRouteState({
  settings,
  message,
  isError = false,
  isAdmin = false,
  onHome,
  onAdmin,
  onThemeChange,
  onBackgroundToggle,
  backgroundEnabled,
}: {
  settings: AdminSettings
  message: string
  isError?: boolean
  isAdmin?: boolean
  onHome: () => void
  onAdmin: () => void
  onThemeChange: (theme: AdminTheme) => void
  onBackgroundToggle?: () => void
  backgroundEnabled: boolean
}) {
  return (
    <div className="kulin-container route-state-container">
      <section className={`home-top-card route-state-panel${isAdmin ? ' is-admin' : ''}`}>
        <DashboardHeader
          settings={settings}
          onHome={onHome}
          onAdmin={isAdmin ? onHome : onAdmin}
          adminLabel={isAdmin ? '前台' : undefined}
          onThemeChange={onThemeChange}
          onBackgroundToggle={onBackgroundToggle}
          backgroundEnabled={backgroundEnabled}
        />
        {message !== '' && <div className={`route-state-card${isError ? ' is-error' : ''}`}>{message}</div>}
      </section>
    </div>
  )
}

function sum(values: Array<number | null | undefined>): number {
  return values.reduce<number>((total, value) => total + (value ?? 0), 0)
}

export function homeTrafficTotalsForNodes(nodes: HomeCardNode[]): { totalUp: number; totalDown: number } {
  return {
    totalUp: sum(nodes.map((node) => node.netOutLifetimeBytes ?? node.netOutTotalBytes)),
    totalDown: sum(nodes.map((node) => node.netInLifetimeBytes ?? node.netInTotalBytes)),
  }
}

export function homeMonthlyCostForNodes(nodes: HomeCardNode[], displayCurrency: CurrencyCode, inputExchangeRates: CurrencyRates): number {
  const exchangeRates = normalizeCurrencyRates(inputExchangeRates)
  return sum(nodes.map((node) => {
    if (node.monthlyCostCny === null || node.monthlyCostCny === undefined) return 0
    const cycleMonths = billingCycleMonths(node.billingCycle)
    const convertedRenewal = cycleMonths > 0
      ? convertCurrencyAmount(node.renewalAmount, node.renewalCurrency, displayCurrency, exchangeRates)
      : null
    if (convertedRenewal !== null) return convertedRenewal / cycleMonths
    return convertCurrencyAmount(node.monthlyCostCny, 'CNY', displayCurrency, exchangeRates) ?? 0
  }))
}

export function orderHomeNodes(nodes: HomeCardNode[]): HomeCardNode[] {
  return nodes.map((node, index) => ({ node, index }))
    .sort((left, right) => {
      const leftOffline = left.node.status === 'online' ? 0 : 1
      const rightOffline = right.node.status === 'online' ? 0 : 1
      if (leftOffline !== rightOffline) return leftOffline - rightOffline
      return left.index - right.index
    })
    .map((entry) => entry.node)
}

function normalizeHomeRegion(countryCode: string | undefined): string {
  const code = (countryCode ?? '').trim().toUpperCase()
  if (code === 'TW') return 'CN'
  return /^[A-Z]{2}$/.test(code) ? code : ''
}

export function homeRegionOptions(nodes: HomeCardNode[]): string[] {
  const seen = new Set<string>()
  const regions: string[] = []
  nodes.forEach((node) => {
    const region = normalizeHomeRegion(node.countryCode)
    if (region === '' || seen.has(region)) return
    seen.add(region)
    regions.push(region)
  })
  return regions
}

export function filterHomeNodesByRegion(nodes: HomeCardNode[], region: string): HomeCardNode[] {
  if (region === 'ALL') return nodes
  return nodes.filter((node) => normalizeHomeRegion(node.countryCode) === region)
}

export function shouldPreloadAdminRoute(routeKind: DashboardRoute['kind'], summaryReady: boolean, adminToken: string): boolean {
  return routeKind === 'home' && summaryReady && adminToken !== ''
}

export function shouldPreloadNodeDetailRoute(routeKind: DashboardRoute['kind'], _summaryReady: boolean): boolean {
  return routeKind === 'home'
}

export function App() {
  const { state, summaryRef, homeRealtimeSnapshot } = useSummaryController()
  const [homeRegion, setHomeRegion] = useState('ALL')
  const [homeCurrency, setHomeCurrency] = useState<CurrencyCode>(storedHomeCurrency)
  const { route, navigateHome, navigateAdmin, navigateNode } = useDashboardRouter()
  const { settings, settingsReady, setSettings } = usePublicSettings()
  const { adminToken, setAdminToken, expireAdminSession } = useAdminAccess()
  const [AdminDashboardRoute, setAdminDashboardRoute] = useState<ComponentType<AdminDashboardContainerProps>>(() => preloadedAdminDashboard ?? LazyAdminDashboard)
  const [NodeDetailRouteComponent, setNodeDetailRouteComponent] = useState<ComponentType<NodeDetailRouteProps>>(() => preloadedNodeDetailRoute ?? LazyNodeDetailRoute)
  const [adminSurfaceMounted, setAdminSurfaceMounted] = useState(route.kind === 'admin')
  const [adminSurfaceReady, setAdminSurfaceReady] = useState(false)
  const adminSurfaceReadyRef = useRef(false)
  const adminNavigationPendingRef = useRef(false)
  const navigateAdminRef = useRef(navigateAdmin)
  navigateAdminRef.current = navigateAdmin
  const [backgroundAssetsReady, setBackgroundAssetsReady] = useState(false)
  const [themeOverride, setThemeOverride] = useState<AdminTheme | null>(() => storedThemeOverride())
  const [backgroundEnabled, setBackgroundEnabled] = useState(() => storedBackgroundEnabled())
  const backgroundEnabledRef = useRef(backgroundEnabled)
  const effectiveSettings = settingsForChrome(settings, themeOverride, backgroundEnabled)
  const nodeRouteId = route.kind === 'node' ? route.nodeId : null
  useDocumentTheme(effectiveSettings)

  const adoptPreloadedNodeDetailRoute = useCallback(() => {
    if (preloadedNodeDetailRoute) setNodeDetailRouteComponent(() => preloadedNodeDetailRoute as ComponentType<NodeDetailRouteProps>)
  }, [])

  useEffect(() => {
    if (nodeRouteId === null) return
    // Start both the detail surface and its initial chart request as soon as the
    // route is known. The controller reuses this request without changing data.
    void preloadNodeDetailRoute().catch(() => {})
    void prefetchNodeLatency(nodeRouteId, '1d').catch(() => {})
  }, [nodeRouteId])

  useEffect(() => {
    if (!shouldPreloadNodeDetailRoute(route.kind, state.kind === 'ready')) return undefined
    let active = true
    let timeoutId: number | null = null
    let idleId: number | null = null
    const preload = () => {
      void preloadNodeDetailRoute()
        .then(() => { if (active) adoptPreloadedNodeDetailRoute() })
        .catch(() => {})
    }
    if (typeof window.requestIdleCallback === 'function') idleId = window.requestIdleCallback(preload, { timeout: 1_000 })
    else timeoutId = window.setTimeout(preload, 0)
    return () => {
      active = false
      if (idleId !== null) window.cancelIdleCallback(idleId)
      if (timeoutId !== null) window.clearTimeout(timeoutId)
    }
  }, [route.kind, state.kind, adoptPreloadedNodeDetailRoute])

  useEffect(() => {
    applyDocumentBranding(settings)
  }, [settings.siteTitle, settings.logoUrl])

  useEffect(() => {
    applyCustomCode(settings)
  }, [settings.customCode])

  useEffect(() => {
    if (!shouldPreloadAdminRoute(route.kind, state.kind === 'ready', adminToken)) return undefined
    let active = true
    void preloadAdminRoute()
      .then(() => {
        if (!active || !preloadedAdminDashboard) return
        setAdminDashboardRoute(() => preloadedAdminDashboard as ComponentType<AdminDashboardContainerProps>)
        setAdminSurfaceMounted(true)
      })
      .catch(() => {})
    return () => { active = false }
  }, [route.kind, state.kind, adminToken])

  useEffect(() => {
    if (!settingsReady || typeof Image === 'undefined') return undefined
    const urls = [...new Set([settings.desktopBackgroundUrl || settings.backgroundUrl, settings.mobileBackgroundUrl].map((value) => value.trim()).filter(Boolean))]
    if (urls.length === 0) {
      setBackgroundAssetsReady(true)
      return undefined
    }
    let active = true
    let remaining = urls.length
    const timers: number[] = []
    setBackgroundAssetsReady(false)
    const images = urls.map((url) => {
      const image = new Image()
      image.decoding = 'async'
      let settled = false
      const finish = () => {
        if (settled) return
        settled = true
        window.clearTimeout(timeoutID)
        remaining -= 1
        if (active && remaining === 0) setBackgroundAssetsReady(true)
      }
      const timeoutID = window.setTimeout(finish, 8000)
      timers.push(timeoutID)
      image.onload = finish
      image.onerror = finish
      image.src = url
      if (image.complete) queueMicrotask(finish)
      return image
    })
    return () => {
      active = false
      timers.forEach((timerID) => window.clearTimeout(timerID))
      images.forEach((image) => {
        image.onload = null
        image.onerror = null
      })
    }
  }, [settingsReady, settings.backgroundUrl, settings.desktopBackgroundUrl, settings.mobileBackgroundUrl])

  const setThemeMode = (nextTheme: AdminTheme) => {
    window.localStorage.setItem('zeno_theme_override', nextTheme)
    setThemeOverride(nextTheme)
  }

  const toggleBackground = () => {
    const nextValue = !backgroundEnabledRef.current
    backgroundEnabledRef.current = nextValue
    window.localStorage.setItem('zeno_background_enabled', String(nextValue))
    setBackgroundEnabled(nextValue)
  }

  const backgroundConfigured = (settings.desktopBackgroundUrl || settings.backgroundUrl || settings.mobileBackgroundUrl).trim() !== ''
  const backgroundToggle = settingsReady && backgroundConfigured && (!backgroundEnabled || backgroundAssetsReady) ? toggleBackground : undefined
  const nodes = state.kind === 'ready' ? state.data.nodes : []
  const homeRealtimeNodes = homeRealtimeSnapshot?.nodes ?? nodes
  const homeNodes = orderHomeNodes(homeRealtimeNodes)
  const homeRegions = homeRegionOptions(homeNodes)
  const activeHomeRegion = homeRegion === 'ALL' || homeRegions.includes(homeRegion) ? homeRegion : 'ALL'
  const visibleHomeNodes = filterHomeNodesByRegion(homeNodes, activeHomeRegion)
  const services = state.kind === 'ready' ? state.data.services : []
  const exchangeRates = normalizeCurrencyRates(state.kind === 'ready' ? state.data.exchangeRates : null)
  const homeCurrencyOptions = availableCurrencyOptions(exchangeRates)
  const activeHomeCurrency = homeCurrencyOptions.some((option) => option.value === homeCurrency) ? homeCurrency : 'CNY'
  const selectedNode = route.kind === 'node' ? nodes.find((node) => node.id === route.nodeId) : undefined
  const selectedService = route.kind === 'service' ? services.find((service) => service.id === route.targetId) : undefined
  const totalCount = homeRealtimeNodes.length
  const onlineCount = homeRealtimeNodes.filter((node) => node.status === 'online').length
  const offlineCount = homeRealtimeNodes.filter((node) => node.status === 'offline').length
  const { totalUp, totalDown } = homeTrafficTotalsForNodes(homeRealtimeNodes)
  const monthlyCost = homeMonthlyCostForNodes(homeRealtimeNodes, activeHomeCurrency, exchangeRates)
  const currentRealtimeSnapshot = homeRealtimeSnapshot ?? homeRealtimeSnapshotForNodes(homeRealtimeNodes)
  const upSpeed = currentRealtimeSnapshot.upSpeed
  const downSpeed = currentRealtimeSnapshot.downSpeed
  const hasBackgroundImage = (effectiveSettings.desktopBackgroundUrl || effectiveSettings.backgroundUrl || effectiveSettings.mobileBackgroundUrl).trim() !== ''
  const changeHomeCurrency = (currency: CurrencyCode) => {
    rememberHomeCurrency(currency)
    setHomeCurrency(currency)
  }
  const preloadAdmin = () => {
    return preloadAdminRoute()
      .then(() => {
        if (!preloadedAdminDashboard) return
        setAdminDashboardRoute(() => preloadedAdminDashboard as ComponentType<AdminDashboardContainerProps>)
        setAdminSurfaceMounted(true)
      })
  }
  const navigateAdminSmoothly = () => {
    adminNavigationPendingRef.current = true
    void preloadAdmin()
      .then(() => {
        if (!adminSurfaceReadyRef.current) return
        adminNavigationPendingRef.current = false
        navigateAdminRef.current()
      })
      .catch(() => {
        adminNavigationPendingRef.current = false
        setAdminSurfaceMounted(true)
        navigateAdminRef.current()
      })
  }
  const preloadAdminIntent = () => { void preloadAdmin().catch(() => {}) }
  const preloadNodeIntent = useCallback((nodeId: string) => {
    // Prime the module cache without replacing an already mounted lazy route.
    void preloadNodeDetailRoute().catch(() => {})
    void prefetchNodeLatency(nodeId, '1d').catch(() => {})
  }, [])
  const handleAdminReadyStateChange = useCallback((ready: boolean) => {
    adminSurfaceReadyRef.current = ready
    setAdminSurfaceReady(ready)
    if (ready) setAdminSurfaceMounted(true)
    if (!ready || !adminNavigationPendingRef.current) return
    adminNavigationPendingRef.current = false
    navigateAdminRef.current()
  }, [])
  const routeStateProps = {
    settings: effectiveSettings,
    onHome: navigateHome,
    onAdmin: navigateAdminSmoothly,
    onThemeChange: setThemeMode,
    onBackgroundToggle: backgroundToggle,
    backgroundEnabled: hasBackgroundImage,
  }

  return (
    <main className="kulin-shell" data-theme={effectiveSettings.theme} data-appearance={effectiveSettings.appearancePreset} data-background={hasBackgroundImage ? 'on' : 'off'} style={shellStyleForSettings(effectiveSettings)}>
      {(adminSurfaceMounted || route.kind === 'admin') && (
        <div hidden={route.kind !== 'admin'} aria-hidden={route.kind !== 'admin'} data-admin-ready={adminSurfaceReady}>
          <AdminModuleErrorBoundary fallback={<AdminDashboardLoadError />}>
            <Suspense fallback={route.kind === 'admin' ? <DashboardRouteState {...routeStateProps} isAdmin message="" /> : null}>
              <AdminDashboardRoute
                onHome={navigateHome}
                settings={settings}
                chromeSettings={effectiveSettings}
                onAdminTokenChange={setAdminToken}
                onSettingsChange={setSettings}
                onReadyStateChange={handleAdminReadyStateChange}
                onThemeChange={setThemeMode}
                onBackgroundToggle={backgroundToggle}
                backgroundEnabled={hasBackgroundImage}
              />
            </Suspense>
          </AdminModuleErrorBoundary>
        </div>
      )}

      {route.kind !== 'admin' && state.kind === 'loading' && <DashboardRouteState {...routeStateProps} message="正在读取 Controller API…" />}
      {route.kind !== 'admin' && state.kind === 'error' && <DashboardRouteState {...routeStateProps} isError message={`API 读取失败：${state.message}`} />}

      {state.kind === 'ready' && route.kind === 'node' && selectedNode && (
        <Suspense fallback={<DashboardRouteState {...routeStateProps} message="加载中…" />}>
          <NodeDetailRouteComponent
            node={selectedNode}
            summary={summaryRef.current}
            adminToken={adminToken}
            expireAdminSession={expireAdminSession}
            onBack={navigateHome}
            topHeader={<DashboardHeader settings={effectiveSettings} onHome={navigateHome} onAdmin={navigateAdminSmoothly} onAdminIntent={preloadAdminIntent} onThemeChange={setThemeMode} onBackgroundToggle={backgroundToggle} backgroundEnabled={hasBackgroundImage} />}
          />
        </Suspense>
      )}

      {state.kind === 'ready' && route.kind === 'node' && !selectedNode && (
        <DashboardRouteState {...routeStateProps} isError message={`没有找到这台服务器：${route.nodeId}`} />
      )}

      {state.kind === 'ready' && route.kind === 'service' && (
        <Suspense fallback={<DashboardRouteState {...routeStateProps} message="加载中…" />}>
          <LazyServiceDetailRoute
            targetId={route.targetId}
            target={selectedService}
            adminToken={adminToken}
            expireAdminSession={expireAdminSession}
            onBack={navigateHome}
            topHeader={<DashboardHeader settings={effectiveSettings} onHome={navigateHome} onAdmin={navigateAdminSmoothly} onAdminIntent={preloadAdminIntent} onThemeChange={setThemeMode} onBackgroundToggle={backgroundToggle} backgroundEnabled={hasBackgroundImage} />}
            loadingFallback={<DashboardRouteState {...routeStateProps} message="加载中…" />}
            notFoundFallback={<DashboardRouteState {...routeStateProps} isError message={`没有找到这个监控服务：${route.targetId}`} />}
          />
        </Suspense>
      )}

      {state.kind === 'ready' && route.kind === 'home' && (
        <div className="kulin-container">
          <HomeTopPanel
            settings={effectiveSettings}
            totalCount={totalCount}
            onlineCount={onlineCount}
            offlineCount={offlineCount}
            monthlyCost={monthlyCost}
            displayCurrency={activeHomeCurrency}
            exchangeRates={exchangeRates}
            currencyOptions={homeCurrencyOptions}
            onCurrencyChange={changeHomeCurrency}
            totalUp={totalUp}
            totalDown={totalDown}
            upSpeed={upSpeed}
            downSpeed={downSpeed}
            onHome={navigateHome}
            onAdmin={navigateAdminSmoothly}
            onAdminIntent={preloadAdminIntent}
            onThemeChange={setThemeMode}
            onBackgroundToggle={backgroundToggle}
            backgroundEnabled={hasBackgroundImage}
          />

          <HomeRegionFilter regions={homeRegions} activeRegion={activeHomeRegion} onChange={setHomeRegion} />

          <section className="server-card-list" aria-label="server cards">
            {visibleHomeNodes.map((node) => <ServerCard key={node.id} node={node} serverCardTheme={effectiveSettings.serverCardTheme} displayCurrency={activeHomeCurrency} exchangeRates={exchangeRates} onOpen={navigateNode} onIntent={preloadNodeIntent} />)}
          </section>
        </div>
      )}
    </main>
  )
}
