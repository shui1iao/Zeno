import { useEffect, useLayoutEffect, useState } from 'react'
import { nodePath, parseDashboardRoute, type DashboardRoute } from '../lib/route'

function blurActiveElement() {
  if (typeof document === 'undefined') return
  const activeElement = document.activeElement
  if (activeElement instanceof HTMLElement || activeElement instanceof SVGElement) activeElement.blur()
}

export function useDashboardRouter(onNavigateNode?: () => void) {
  const [route, setRoute] = useState<DashboardRoute>(() => parseDashboardRoute(window.location.pathname))

  useEffect(() => {
    const handlePopState = () => {
      blurActiveElement()
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

  const navigateHome = () => {
    blurActiveElement()
    window.history.pushState(null, '', '/')
    setRoute({ kind: 'home' })
  }
  const navigateAdmin = () => {
    blurActiveElement()
    window.history.pushState(null, '', '/dashboard')
    setRoute({ kind: 'admin' })
  }
  const navigateNode = (nodeId: string) => {
    blurActiveElement()
    window.history.pushState(null, '', nodePath(nodeId))
    onNavigateNode?.()
    setRoute({ kind: 'node', nodeId })
  }

  return { route, navigateHome, navigateAdmin, navigateNode }
}
