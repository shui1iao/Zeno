// @ts-nocheck
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const apiDirectory = dirname(fileURLToPath(import.meta.url))
const sourceDirectory = join(apiDirectory, '..')
const readSource = (path: string) => readFileSync(join(sourceDirectory, path), 'utf8')

describe('API module boundaries', () => {
  it('keeps public application code independent from the admin API client', () => {
    const app = readSource('App.tsx')
    const publicClient = readSource('api/publicClient.ts')
    const publicControllers = [
      readSource('hooks/useSummaryController.ts'),
      readSource('hooks/useNodeDetailController.ts'),
      readSource('hooks/useServiceDetailController.ts'),
    ].join('\n')
    expect(publicControllers).toContain("from '../api/publicClient'")
    expect(publicControllers).not.toContain("from '../api/adminClient'")
    expect(app).not.toContain("from './api/client'")
    expect(app).not.toContain('useAdminController')
    expect(readSource('components/admin/AdminDashboard.tsx')).toContain('useAdminController')
    expect(publicClient).not.toContain('/api/admin/')
    expect(publicClient).not.toContain('adminHeaders')
  })

  it('keeps the compatibility facade free of implementation code', () => {
    const facade = readSource('api/client.ts')
    expect(facade).toMatch(/^export \* from '\.\/publicClient'\nexport \* from '\.\/adminClient'\n$/)
  })

  it('loads admin operations through the admin-only client', () => {
    const controller = readSource('hooks/useAdminController.ts')
    expect(controller).toContain("from '../api/adminClient'")
    expect(controller).not.toContain("from '../api/client'")
  })

  it('loads every backend section with the admin route while keeping it out of the public entry', () => {
    const dashboard = readSource('components/admin/AdminDashboard.tsx')
    const operational = readSource('components/admin/AdminOperationalWorkspace.tsx')
    expect(dashboard).toContain("import AdminAccountSection from './AdminAccountSection'")
    expect(dashboard).toContain("import AdminSettingsSection from './AdminSettingsSection'")
    expect(operational).toContain("import AdminNodeWorkspace from './AdminNodeWorkspace'")
    expect(operational).toContain("import AdminTargetWorkspace from './AdminTargetWorkspace'")
    expect(operational).toContain("import AdminNotificationsWorkspace from './AdminNotificationsWorkspace'")
    expect(readSource('App.tsx')).toContain("import('./components/admin/AdminDashboard')")
    expect(readSource('components/admin/AdminAccountSection.tsx')).toContain('账号只能使用 3-64 位')
    expect(readSource('components/admin/AdminSettingsSection.tsx')).toContain('卡片透明度')
  })

  it('routes every backend save area through the shared in-flow action footer', () => {
    const expectedFooterCounts = new Map([
      ['components/admin/AdminNodeWorkspace.tsx', 2],
      ['components/admin/AdminTargetWorkspace.tsx', 2],
      ['components/admin/AdminNotificationsWorkspace.tsx', 2],
      ['components/admin/AdminAccountSection.tsx', 1],
      ['components/admin/AdminSettingsSection.tsx', 1],
    ])
    for (const [path, expectedCount] of expectedFooterCounts) {
      const source = readSource(path)
      expect(source.match(/<AdminActionFooter\b/g) ?? [], path).toHaveLength(expectedCount)
      expect(source, path).not.toContain('AdminModalActions')
      expect(source, path).not.toContain('admin-modal-actions')
    }
  })
})
