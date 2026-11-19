import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { nodePath, parseDashboardRoute, type DashboardRoute } from '../lib/route'

function blurActiveElement() {
  if (typeof document === 'undefined') return
  const activeElement = document.activeElement
  if (activeElement instanceof HTMLElement || activeElement instanceof SVGElement) activeElement.blur()
}

export function useDashboardRouter(onNavigateNode?: () => void) {
  const [route, setRoute] = useState<DashboardRoute>(() => parseDashboardRoute(window.location.pathname))
  const onNavigateNodeRef = useRef(onNavigateNode)
  onNavigateNodeRef.current = onNavigateNode

  useEffect(() => {
    const normalizeUnknownHomePath = () => {
      const pathname = window.location.pathname
      if (parseDashboardRoute(pathname).kind === 'home' && pathname !== '/' && pathname !== '/index.html') {
        window.history.replaceState(null, '', '/')
      }
    }
    normalizeUnknownHomePath()
    const handlePopState = () => {
      blurActiveElement()
      normalizeUnknownHomePath()
      setRoute(parseDashboardRoute(window.location.pathname))
    }
    window.addEventListener('popstate', handlePopState)
    return () => window.removeEventListener('popstate', handlePopState)
  }, [])

  useLayoutEffect(() => {
    document.documentElement.scrollTop = 0
    document.body.scrollTop = 0
    window.scrollTo({ left: 0, top: 0, behavior: 'auto' })
  }, [route.kind, route.kind === 'node' ? route.nodeId : ''])

  const navigateHome = useCallback(() => {
    blurActiveElement()
    window.history.pushState(null, '', '/')
    setRoute({ kind: 'home' })
  }, [])
  const navigateAdmin = useCallback(() => {
    blurActiveElement()
    window.history.pushState(null, '', '/dashboard')
    setRoute({ kind: 'admin' })
  }, [])
  const navigateNode = useCallback((nodeId: string) => {
    blurActiveElement()
    window.history.pushState(null, '', nodePath(nodeId))
    onNavigateNodeRef.current?.()
    setRoute({ kind: 'node', nodeId })
  }, [])

  return { route, navigateHome, navigateAdmin, navigateNode }
}
