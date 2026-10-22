export type DashboardRoute =
  | { kind: 'home' }
  | { kind: 'admin' }
  | { kind: 'node'; nodeId: string }

function decodeRouteSegment(value: string): string | null {
  try {
    return decodeURIComponent(value)
  } catch {
    return null
  }
}

export function parseDashboardRoute(pathname: string): DashboardRoute {
  const normalized = pathname || '/'
  if (normalized === '/' || normalized === '/index.html') {
    return { kind: 'home' }
  }

  if (normalized === '/dashboard' || normalized === '/dashboard/') {
    return { kind: 'admin' }
  }

  const match = normalized.match(/^\/server\/([^/]+)\/?$/)
  if (match) {
    const nodeId = decodeRouteSegment(match[1])
    return nodeId === null ? { kind: 'home' } : { kind: 'node', nodeId }
  }

  return { kind: 'home' }
}

export function nodePath(nodeId: string): string {
  return `/server/${encodeURIComponent(nodeId)}`
}
