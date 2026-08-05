import { type ComponentType, type FormEvent, useEffect, useState, useTransition } from 'react'
import type { AdminAlertRuleUpdateInput, AdminNodeCreateInput, AdminNodeUpdateInput, AdminNotificationChannelCreateInput, AdminNotificationChannelUpdateInput, AdminProbeTargetInput, AdminProbeTargetUpdateInput, AdminSettingsUpdateInput } from '../../api/adminClient'
import { DashboardHeader } from '../DashboardHeader'
import AdminAccountSection from './AdminAccountSection'
import AdminOperationalWorkspace, { type AdminOperationalWorkspaceProps } from './AdminOperationalWorkspace'
import { AdminModuleErrorBoundary, AdminOperationalWorkspaceLoadError } from './AdminDashboardBoundary'
import AdminSettingsSection from './AdminSettingsSection'
import { defaultSettings } from '../../lib/appearance'
import { slidingSelectorStyle } from '../../lib/slidingSelector'
import type { AdminNode, AdminNodeInstallCommand, AdminSettings, AdminTheme } from '../../types'
import type { AdminAuthState, AdminLoadState } from '../../lib/adminModel'
import { useAdminController } from '../../hooks/useAdminController'
import '../../styles/admin.css'

export type AdminSection = 'nodes' | 'targets' | 'notifications' | 'account' | 'settings'

const adminSections: ReadonlyArray<{ id: AdminSection; label: string }> = [
  { id: 'nodes', label: '服务器' },
  { id: 'targets', label: '延迟监控' },
  { id: 'notifications', label: '通知' },
  { id: 'account', label: '账户' },
  { id: 'settings', label: '设置' },
]
type MaybePromise<T = void> = T | Promise<T>

export interface AdminDashboardProps {
  onHome: () => void
  settings?: AdminSettings
  chromeSettings?: AdminSettings
  hasAdminToken?: boolean
  adminSessionReady?: boolean
  authState?: AdminAuthState
  adminState?: AdminLoadState
  initialSection?: AdminSection
  onAdminLogin?: (username: string, password: string) => void
  onAdminTokenClear?: () => void
  onAdminAccountUpdate?: (username: string, currentPassword: string, newPassword: string) => Promise<void>
  onAdminNodeCreate?: (input: AdminNodeCreateInput) => Promise<AdminNode | void>
  onAdminNodeUpdate?: (nodeId: string, input: AdminNodeUpdateInput) => MaybePromise
  onAdminNodeDelete?: (nodeId: string) => MaybePromise
  onAdminInstallCommand?: (nodeId: string) => Promise<AdminNodeInstallCommand>
  onAdminProbeTargetCreate?: (input: AdminProbeTargetInput) => MaybePromise
  onAdminProbeTargetUpdate?: (targetId: string, input: AdminProbeTargetUpdateInput) => MaybePromise
  onAdminProbeTargetDelete?: (targetId: string) => MaybePromise
  onAdminNotificationChannelCreate?: (input: AdminNotificationChannelCreateInput) => MaybePromise
  onAdminNotificationChannelUpdate?: (channelId: string, input: AdminNotificationChannelUpdateInput) => MaybePromise
  onAdminNotificationChannelDelete?: (channelId: string) => MaybePromise
  onAdminNotificationChannelTest?: (channelId: string) => void
  onAdminAlertRuleUpdate?: (ruleId: string, input: AdminAlertRuleUpdateInput) => MaybePromise
  onAdminSettingsUpdate?: (input: AdminSettingsUpdateInput) => MaybePromise
  onThemeChange?: (theme: AdminTheme) => void
  onBackgroundToggle?: () => void
  backgroundEnabled?: boolean
  operationalWorkspace?: ComponentType<AdminOperationalWorkspaceProps>
}

export interface AdminDashboardContainerProps {
  onHome: () => void
  settings?: AdminSettings
  chromeSettings?: AdminSettings
  onAdminTokenChange?: (token: string) => void
  onSettingsChange?: (settings: AdminSettings) => void
  onReadyStateChange?: (ready: boolean) => void
  onThemeChange?: (theme: AdminTheme) => void
  onBackgroundToggle?: () => void
  backgroundEnabled?: boolean
}

export function AdminDashboardContainer({
  onHome,
  settings = defaultSettings,
  chromeSettings = settings,
  onAdminTokenChange,
  onSettingsChange,
  onReadyStateChange,
  onThemeChange,
  onBackgroundToggle,
  backgroundEnabled = true,
}: AdminDashboardContainerProps) {
  const controller = useAdminController(true, {
    initialSettings: settings,
    onTokenChange: onAdminTokenChange,
    onSettingsChange,
  })
  const ready = adminSurfaceIsReady(controller.adminToken !== '', controller.adminSessionReady, controller.adminState)
  useEffect(() => onReadyStateChange?.(ready), [onReadyStateChange, ready])
  return (
    <AdminDashboard
      onHome={onHome}
      settings={controller.settings}
      chromeSettings={chromeSettings}
      hasAdminToken={controller.adminToken !== ''}
      adminSessionReady={controller.adminSessionReady}
      authState={controller.adminAuthState}
      adminState={controller.adminState}
      onAdminLogin={controller.submitAdminLogin}
      onAdminTokenClear={controller.clearAdminToken}
      onAdminAccountUpdate={controller.updateAdminAccountDetails}
      onAdminNodeCreate={controller.createAdminNodeDetails}
      onAdminNodeUpdate={controller.updateAdminNodeDetails}
      onAdminNodeDelete={controller.deleteAdminNodeDetails}
      onAdminInstallCommand={controller.requestAdminInstallCommand}
      onAdminProbeTargetCreate={controller.createAdminProbeTargetDetails}
      onAdminProbeTargetUpdate={controller.updateAdminProbeTargetDetails}
      onAdminProbeTargetDelete={controller.deleteAdminProbeTargetDetails}
      onAdminNotificationChannelCreate={controller.createAdminNotificationChannelDetails}
      onAdminNotificationChannelUpdate={controller.updateAdminNotificationChannelDetails}
      onAdminNotificationChannelDelete={controller.deleteAdminNotificationChannelDetails}
      onAdminNotificationChannelTest={controller.testAdminNotificationChannelDetails}
      onAdminAlertRuleUpdate={controller.updateAdminAlertRuleDetails}
      onAdminSettingsUpdate={controller.updateAdminSettingsDetails}
      onThemeChange={onThemeChange}
      onBackgroundToggle={onBackgroundToggle}
      backgroundEnabled={backgroundEnabled}
    />
  )
}

export function AdminDashboard({
  onHome,
  settings = defaultSettings,
  chromeSettings = settings,
  hasAdminToken = false,
  adminSessionReady = true,
  authState = { kind: 'idle' },
  adminState = { kind: 'idle' },
  initialSection = 'nodes',
  onAdminLogin = () => {},
  onAdminTokenClear = () => {},
  onAdminAccountUpdate = () => Promise.reject(new Error('account update unavailable')),
  onAdminNodeCreate = () => Promise.resolve(),
  onAdminNodeUpdate = () => {},
  onAdminNodeDelete = () => {},
  onAdminInstallCommand = () => Promise.reject(new Error('install command unavailable')),
  onAdminProbeTargetCreate = () => {},
  onAdminProbeTargetUpdate = () => {},
  onAdminProbeTargetDelete = () => {},
  onAdminNotificationChannelCreate = () => {},
  onAdminNotificationChannelUpdate = () => {},
  onAdminNotificationChannelDelete = () => {},
  onAdminNotificationChannelTest = () => {},
  onAdminAlertRuleUpdate = () => {},
  onAdminSettingsUpdate = () => {},
  onThemeChange,
  onBackgroundToggle,
  backgroundEnabled = true,
  operationalWorkspace,
}: AdminDashboardProps) {
  const [activeSection, setActiveSection] = useState<AdminSection>(initialSection)
  const [, startSectionTransition] = useTransition()
  const OperationalWorkspace = operationalWorkspace ?? AdminOperationalWorkspace
  const isAuthenticated = adminSessionReady && hasAdminToken
  const handleTokenSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const form = event.currentTarget
    const formData = new FormData(form)
    const username = String(formData.get('admin-username') ?? '').trim()
    const password = String(formData.get('admin-password') ?? '').trim()
    if (username === '' || password === '') return
    onAdminLogin(username, password)
  }
  const dashboardHeader = (
    <DashboardHeader
      settings={chromeSettings}
      onHome={onHome}
      onAdmin={onHome}
      adminLabel="前台"
      onThemeChange={onThemeChange}
      onBackgroundToggle={onBackgroundToggle}
      backgroundEnabled={backgroundEnabled}
    />
  )

  return (
    <div className="kulin-container admin-container">
      <section className={`admin-panel${isAuthenticated ? ' admin-panel--authenticated' : ' home-top-card'}${adminSessionReady && !hasAdminToken ? ' admin-panel--login' : ''}`} aria-label="admin dashboard">
        {isAuthenticated ? (
          <div className="home-top-card admin-chrome-card">
            {dashboardHeader}
            <div className="admin-toolbar">
              <AdminSectionNav
                activeSection={activeSection}
                onSectionChange={(section) => startSectionTransition(() => setActiveSection(section))}
              />
            </div>
          </div>
        ) : dashboardHeader}

        {adminSessionReady && !hasAdminToken && (
          <form className="admin-login-card" aria-label="admin login form" onSubmit={handleTokenSubmit}>
              <div className="admin-login-title">
                <strong>后台登录</strong>
              </div>
              <label>
                <span>账号</span>
                <input name="admin-username" autoComplete="username" placeholder="admin" aria-label="后台账号" />
              </label>
              <label>
                <span>密码</span>
                <input name="admin-password" type="password" autoComplete="current-password" placeholder="admin" aria-label="后台密码" />
              </label>
              <button type="submit" disabled={authState.kind === 'loading'}>{authState.kind === 'loading' ? '登录中…' : '登录后台'}</button>
              {authState.kind === 'error' && <p className="admin-login-error">{authState.message}</p>}
          </form>
        )}

        {adminSessionReady && hasAdminToken && (
          <div className="home-top-card admin-content-card">
            {authState.kind === 'error' && <div className="admin-state-card is-error">{authState.message}</div>}
            {adminState.kind === 'error' && <div className="admin-state-card is-error">Admin API 读取失败：{adminState.message}</div>}

            {adminState.kind === 'ready' && (activeSection === 'nodes' || activeSection === 'targets' || activeSection === 'notifications') && (
              <AdminModuleErrorBoundary key={activeSection} fallback={<AdminOperationalWorkspaceLoadError />}>
                <OperationalWorkspace
                  activeSection={activeSection}
                  nodes={adminState.nodes}
                  targets={adminState.targets}
                  notificationChannels={adminState.notificationChannels}
                  alertRules={adminState.alertRules}
                  onNodeCreate={onAdminNodeCreate}
                  onNodeUpdate={onAdminNodeUpdate}
                  onNodeDelete={onAdminNodeDelete}
                  onInstallCommand={onAdminInstallCommand}
                  onProbeTargetCreate={onAdminProbeTargetCreate}
                  onProbeTargetUpdate={onAdminProbeTargetUpdate}
                  onProbeTargetDelete={onAdminProbeTargetDelete}
                  onNotificationChannelCreate={onAdminNotificationChannelCreate}
                  onNotificationChannelUpdate={onAdminNotificationChannelUpdate}
                  onNotificationChannelDelete={onAdminNotificationChannelDelete}
                  onNotificationChannelTest={onAdminNotificationChannelTest}
                  onAlertRuleUpdate={onAdminAlertRuleUpdate}
                />
              </AdminModuleErrorBoundary>
            )}

            {adminState.kind === 'ready' && activeSection === 'account' && (
              <AdminAccountSection account={adminState.account} onUpdate={onAdminAccountUpdate} onLogout={onAdminTokenClear} />
            )}

            {adminState.kind === 'ready' && activeSection === 'settings' && (
              <AdminSettingsSection settings={settings} onUpdate={onAdminSettingsUpdate} />
            )}
          </div>
        )}
      </section>
    </div>
  )
}

function AdminSectionNav({ activeSection, onSectionChange }: { activeSection: AdminSection; onSectionChange: (section: AdminSection) => void }) {
  const activeIndex = Math.max(0, adminSections.findIndex((section) => section.id === activeSection))
  return (
    <nav className="sliding-selector admin-section-nav" aria-label="后台导航" style={slidingSelectorStyle(adminSections.length, activeIndex)}>
      {adminSections.map((section) => (
        <button
          key={section.id}
          type="button"
          data-active={activeSection === section.id}
          aria-current={activeSection === section.id ? 'page' : undefined}
          onClick={() => onSectionChange(section.id)}
        >
          <span>{section.label}</span>
        </button>
      ))}
    </nav>
  )
}

export function adminSurfaceIsReady(hasAdminToken: boolean, adminSessionReady: boolean, adminState: AdminLoadState): boolean {
  if (!adminSessionReady) return false
  if (!hasAdminToken) return true
  return adminState.kind === 'ready' || adminState.kind === 'error'
}

export default AdminDashboard
