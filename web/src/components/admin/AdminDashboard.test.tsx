import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { applyCustomCode } from '../../lib/customCode'
import { remoteInsecureAgentControllerURL, validateAdminSettingsInput } from '../../lib/adminSettings'
import { AdminDashboard, adminSurfaceIsReady } from './AdminDashboard'
import AdminAccountSection from './AdminAccountSection'
import { AdminDashboardLoadError, AdminModuleErrorBoundary, AdminOperationalWorkspaceLoadError } from './AdminDashboardBoundary'
import AdminSettingsSection from './AdminSettingsSection'
import { AdminOperationalWorkspace, formatTargetAssignmentSummary, renewalCurrencyOptions, type AdminOperationalWorkspaceProps } from './AdminOperationalWorkspace'
import { AdminNodeWorkspace } from './AdminNodeWorkspace'
import { AdminNotificationsWorkspace } from './AdminNotificationsWorkspace'
import { AdminTargetWorkspace } from './AdminTargetWorkspace'
import { AdminCredentialField, AdminDeleteConfirmModal } from './AdminPrimitives'
import { alertRules, backupNode, exampleNodeANode, exampleNodeATarget, httpTarget, pingTarget, settings, telegramChannel } from '../../../test/fixtures/adminTestFixtures'

describe('remoteInsecureAgentControllerURL', () => {
  it('only marks non-loopback HTTP origins as plaintext remote transport', () => {
    expect(remoteInsecureAgentControllerURL('http://203.0.113.10:18980')).toBe(true)
    expect(remoteInsecureAgentControllerURL('http://[2001:db8::10]:18980')).toBe(true)
    expect(remoteInsecureAgentControllerURL('http://localhost:18980')).toBe(false)
    expect(remoteInsecureAgentControllerURL('http://127.0.0.2:18980')).toBe(false)
    expect(remoteInsecureAgentControllerURL('http://[::1]:18980')).toBe(false)
    expect(remoteInsecureAgentControllerURL('https://zeno.example.com')).toBe(false)
  })
})

describe('renewalCurrencyOptions', () => {
  it('keeps the server editor currency menu short enough to fit without SGD and KRW', () => {
    expect(renewalCurrencyOptions.map((option) => option.value)).toEqual(['CNY', 'USD', 'HKD', 'EUR', 'GBP', 'JPY', 'AUD', 'CAD'])
  })
})

describe('AdminDeleteConfirmModal', () => {
  it('keeps the delete confirmation short and names the subject', () => {
    const html = renderToStaticMarkup(
      <AdminDeleteConfirmModal
        title="删除延迟监控"
        subjectName="Zeno Health"
        confirmLabel="删除延迟监控"
        onConfirm={() => {}}
        onClose={() => {}}
      />,
    )

    expect(html).toContain('class="admin-modal admin-delete-modal"')
    expect(html).toContain('role="dialog"')
    expect(html).toContain('aria-describedby=')
    expect(html).toContain('aria-busy="false"')
    expect(html).toContain('确认删除')
    expect(html).toContain('Zeno Health')
    expect(html).toContain('删除后无法恢复。')
    expect(html).not.toContain('影响范围')
    expect(html).not.toContain('确认后将立即执行删除。')
    expect(html).toContain('class="is-danger" type="submit"')
  })
})

function SynchronousAdminOperationalWorkspace(props: AdminOperationalWorkspaceProps) {
  return <AdminOperationalWorkspace {...props} sectionComponents={{ nodes: AdminNodeWorkspace, targets: AdminTargetWorkspace, notifications: AdminNotificationsWorkspace }} />
}

function renderAdmin(section: 'nodes' | 'targets' | 'notifications' | 'account' | 'settings' = 'nodes', authState: { kind: 'idle' } | { kind: 'loading' } | { kind: 'error'; message: string } = { kind: 'idle' }) {
  return renderToStaticMarkup(
    <AdminDashboard
      onHome={() => {}}
      settings={settings}
      hasAdminToken
      authState={authState}
      initialSection={section}
      adminState={{
        kind: 'ready',
        account: { username: 'admin' },
        nodes: [exampleNodeANode, backupNode],
        targets: [exampleNodeATarget, pingTarget, httpTarget],
        notificationChannels: [telegramChannel],
        alertRules,
      }}
      operationalWorkspace={SynchronousAdminOperationalWorkspace}
      onAdminLogin={() => {}}
      onAdminTokenClear={() => {}}
      onAdminAccountUpdate={async () => {}}
      onAdminNodeCreate={async () => undefined}
      onAdminNodeUpdate={() => {}}
      onAdminNodeDelete={() => {}}
      onAdminInstallCommand={async () => ({ nodeId: 'example-node-a', command: 'install command', commands: { linux: 'install command' } })}
      onAdminProbeTargetCreate={() => {}}
      onAdminProbeTargetUpdate={() => {}}
      onAdminProbeTargetDelete={() => {}}
      onAdminNotificationChannelCreate={() => {}}
      onAdminNotificationChannelUpdate={() => {}}
      onAdminNotificationChannelDelete={() => {}}
      onAdminNotificationChannelTest={() => {}}
      onAdminAlertRuleUpdate={() => {}}
      onAdminSettingsUpdate={() => {}}
    />,
  )
}

describe('AdminDashboard', () => {
  it('renders recoverable fallbacks when either lazy admin chunk fails', () => {
    expect(AdminModuleErrorBoundary.getDerivedStateFromError()).toEqual({ failed: true })

    const dashboardHTML = renderToStaticMarkup(<AdminDashboardLoadError onRetry={() => {}} />)
    expect(dashboardHTML).toContain('role="alert"')
    expect(dashboardHTML).toContain('后台加载失败，请刷新后重试。')

    const workspaceHTML = renderToStaticMarkup(<AdminOperationalWorkspaceLoadError onRetry={() => {}} />)
    expect(workspaceHTML).toContain('role="alert"')
    expect(workspaceHTML).toContain('运营后台加载失败，请刷新后重试。')
    expect(workspaceHTML).toContain('<button type="button">刷新重试</button>')
  })

  it('forwards operational data and callbacks to a synchronous test workspace inside the real dashboard DOM', () => {
    let forwarded: AdminOperationalWorkspaceProps | undefined
    const nodes = [exampleNodeANode, backupNode]
    const targets = [exampleNodeATarget, pingTarget, httpTarget]
    const notificationChannels = [telegramChannel]
    const onNodeCreate: AdminOperationalWorkspaceProps['onNodeCreate'] = async () => undefined
    const onNodeUpdate: AdminOperationalWorkspaceProps['onNodeUpdate'] = () => {}
    const onNodeDelete: AdminOperationalWorkspaceProps['onNodeDelete'] = () => {}
    const onInstallCommand: AdminOperationalWorkspaceProps['onInstallCommand'] = async (nodeId) => ({ nodeId, command: 'install', commands: { linux: 'install' } })
    const onProbeTargetCreate: AdminOperationalWorkspaceProps['onProbeTargetCreate'] = () => {}
    const onProbeTargetUpdate: AdminOperationalWorkspaceProps['onProbeTargetUpdate'] = () => {}
    const onProbeTargetReorder: AdminOperationalWorkspaceProps['onProbeTargetReorder'] = () => {}
    const onProbeTargetDelete: AdminOperationalWorkspaceProps['onProbeTargetDelete'] = () => {}
    const onNotificationChannelCreate: AdminOperationalWorkspaceProps['onNotificationChannelCreate'] = () => {}
    const onNotificationChannelUpdate: AdminOperationalWorkspaceProps['onNotificationChannelUpdate'] = () => {}
    const onNotificationChannelDelete: AdminOperationalWorkspaceProps['onNotificationChannelDelete'] = () => {}
    const onNotificationChannelTest: AdminOperationalWorkspaceProps['onNotificationChannelTest'] = () => {}
    const onAlertRuleUpdate: AdminOperationalWorkspaceProps['onAlertRuleUpdate'] = () => {}
    const TestOperationalWorkspace = (props: AdminOperationalWorkspaceProps) => {
      forwarded = props
      return <div data-testid="operational-workspace-probe">{props.activeSection}:{props.nodes[0]?.displayName}</div>
    }

    const html = renderToStaticMarkup(
      <AdminDashboard
        onHome={() => {}}
        hasAdminToken
        initialSection="targets"
        adminState={{ kind: 'ready', account: { username: 'admin' }, nodes, targets, notificationChannels, alertRules }}
        operationalWorkspace={TestOperationalWorkspace}
        onAdminNodeCreate={onNodeCreate}
        onAdminNodeUpdate={onNodeUpdate}
        onAdminNodeDelete={onNodeDelete}
        onAdminInstallCommand={onInstallCommand}
        onAdminProbeTargetCreate={onProbeTargetCreate}
        onAdminProbeTargetUpdate={onProbeTargetUpdate}
        onAdminProbeTargetReorder={onProbeTargetReorder}
        onAdminProbeTargetDelete={onProbeTargetDelete}
        onAdminNotificationChannelCreate={onNotificationChannelCreate}
        onAdminNotificationChannelUpdate={onNotificationChannelUpdate}
        onAdminNotificationChannelDelete={onNotificationChannelDelete}
        onAdminNotificationChannelTest={onNotificationChannelTest}
        onAdminAlertRuleUpdate={onAlertRuleUpdate}
      />,
    )

    expect(forwarded).toMatchObject({
      activeSection: 'targets',
      nodes,
      targets,
      notificationChannels,
      alertRules,
      onNodeCreate,
      onNodeUpdate,
      onNodeDelete,
      onInstallCommand,
      onProbeTargetCreate,
      onProbeTargetUpdate,
      onProbeTargetReorder,
      onProbeTargetDelete,
      onNotificationChannelCreate,
      onNotificationChannelUpdate,
      onNotificationChannelDelete,
      onNotificationChannelTest,
      onAlertRuleUpdate,
    })
    expect(html.match(/class="kulin-container admin-container"/g)).toHaveLength(1)
    expect(html).toMatch(/<section class="admin-panel admin-panel--authenticated"[\s\S]*<div class="home-top-card admin-chrome-card">[\s\S]*<div class="admin-content-card"><div data-testid="operational-workspace-probe">targets:Example Node A<\/div><\/div><\/div><\/section><\/div>$/)
  })

  it('keeps Telegram credentials masked with an accessible visibility toggle and no reflected value', () => {
    const html = renderToStaticMarkup(
      <AdminCredentialField name="channel-credential" placeholder="留空则保留已保存 Token" />,
    )

    expect(html).toContain('name="channel-credential"')
    expect(html).toContain('type="password"')
    expect(html).toContain('autoComplete="new-password"')
    expect(html).toContain('class="admin-secret-toggle"')
    expect(html).toContain('aria-label="显示 Telegram Bot Token"')
    expect(html).toContain('aria-pressed="false"')
    expect(html).not.toContain('value=')
    expect(html).not.toContain('telegram-bot-secret')
  })

  it('keeps the login form hidden until the HttpOnly cookie session probe finishes', () => {
    const html = renderToStaticMarkup(<AdminDashboard onHome={() => {}} adminSessionReady={false} />)

    expect(html).toContain('admin-panel')
    expect(html).not.toContain('加载中…')
    expect(html).not.toContain('admin-login-card')
    expect(html).not.toContain('后台登录')
  })

  it('unifies authenticated chrome with its content and opens backend directly on the server list', () => {
    const html = renderAdmin()

    expect(html.match(/home-top-card/g)).toHaveLength(1)
    expect(html).toContain('admin-panel admin-panel--authenticated')
    expect(html).toContain('admin-chrome-card')
    expect(html).toContain('admin-content-card')
    expect(html).not.toContain(['Zeno', '后台'].join(' '))
    expect(html).not.toContain('控' + '制台')
    expect(html).not.toContain('列表只保留' + '关键字段')
    expect(html).toContain('admin-section-nav')
    expect(html).toContain('后台导航')
    expect(html).toContain('服务器')
    expect(html).toContain('延迟监控')
    expect(html).not.toContain('10 台')
    expect(html).not.toContain('13 个目标')
    expect(html).not.toContain('2 类型')
    expect(html).not.toContain('1 异常 / 2 类型')
    expect(html).toContain('账户')
    expect(html).toContain('设置')
    expect(html).toContain('通知')
    expect(html).not.toContain('nav-logout-button')
    expect(html).not.toContain('退出登录')
    expect(html).not.toContain(['刷', '新'].join(''))
    expect(html).not.toContain('修改密码</button>')
    expect(html).toContain('服务器列表')
    expect(html).toContain('Example Node A')
    expect(html).not.toContain('admin-overview-panel')
  })

  it('keeps the logged-in dashboard visible and shows logout failures without restoring the old header action', () => {
    const html = renderAdmin('nodes', { kind: 'error', message: '退出失败：admin logout failed: 500' })

    expect(html).not.toContain('nav-logout-button')
    expect(html).toContain('退出失败：admin logout failed: 500')
    expect(html).toContain('admin-section-nav')
    expect(html).toContain('服务器列表')
  })

  it('renders account settings as a dedicated account page', () => {
    const dashboard = renderAdmin('account')
    const html = renderToStaticMarkup(<AdminAccountSection account={{ username: 'admin' }} onUpdate={() => Promise.resolve()} onLogout={() => {}} />)

    expect(dashboard).toContain('账户')
    expect(dashboard).toContain('修改账号和密码')
    expect(dashboard).not.toContain('加载中…')
    expect(dashboard).not.toContain('服务器列表')
    expect(html).toContain('修改账号和密码')
    expect(html).toContain('登录信息')
    expect(html).not.toContain('密码与会话')
    expect(html).toContain('name="account-username"')
    expect(html).toContain('value="admin"')
    expect(html).toContain('name="current-password"')
    expect(html).toContain('name="new-password"')
    expect(html).toContain('class="admin-account-logout-button"')
    expect(html).toContain('退出登录')
    expect(html).toContain('保存账户')
  })

  it('renders settings as a lightweight appearance configuration page', () => {
    const dashboard = renderAdmin('settings')
    const html = renderToStaticMarkup(<AdminSettingsSection settings={settings} onUpdate={() => {}} />)

    expect(dashboard).toContain('设置')
    expect(dashboard).toContain('站点设置')
    expect(dashboard).not.toContain('加载中…')
    expect(dashboard).not.toContain('服务器列表')
    expect(html).toContain('站点设置')
    expect(html).toContain('外观配置')
    expect(html).toContain('界面外观')
    expect(html).not.toContain('站点信息')
    expect(html).not.toContain('主题与背景')
    expect(html).toContain('admin-settings-form')
    expect(html).toContain('name="site-title"')
    expect(html).toContain('水饺监控')
    expect(html).not.toContain('name="site-subtitle"')
    expect(html).not.toContain('站点副标题')
    expect(html).toContain('name="logo-url"')
    expect(html).toContain('placeholder="可留空"')
    expect(html).not.toContain('留空显示字母 Z')
    expect(html).toContain('/assets/logo/custom.png')
    expect(html).toContain('头像 / Logo URL')
    expect(html).not.toContain('/assets/avatar/custom.webp')
    expect(html).not.toContain('name="avatar-url"')
    expect(html).toContain('name="theme"')
    expect(html).toContain('深色')
    expect(html).toContain('Agent 接入 URL')
    expect(html).toContain('name="agent-controller-url"')
    expect(html).toContain('https://zeno.example.com')
    expect(html).not.toContain(['图片字段', '只填 https:// 链接或 /assets/... 站内路径'].join(''))
    expect(html).not.toContain(['最近', '更新：'].join(''))
    expect(html).toContain('name="desktop-background-url"')
    expect(html).toContain('https://example.com/desktop-bg.webp')
    expect(html).toContain('name="mobile-background-url"')
    expect(html).toContain('https://example.com/mobile-bg.webp')
    expect(html).not.toContain('外观样式')
    expect(html).toContain('admin-appearance-top')
    expect(html).toContain('name="appearance-preset"')
    expect(html).toContain('高斯模糊主题')
    expect(html).toContain('name="server-card-theme"')
    expect(html).toContain('服务器卡片')
    expect(html).toContain('经典卡片')
    expect(html).toContain('节点舱')
    expect(html.match(/class="sliding-selector admin-appearance-preset-slider sliding-selector--large"/g)).toHaveLength(2)
    expect(html).not.toContain('admin-card-theme-slider sliding-selector--medium')
    expect(html).toContain('name="card-opacity"')
    expect(html).toContain('卡片透明度')
    expect(html).toContain('name="card-blur"')
    expect(html).toContain('卡片模糊度')
    expect(html).toContain('name="card-radius"')
    expect(html).toContain('卡片圆角')
    expect(html).toContain('name="border-strength"')
    expect(html).toContain('边框强度')
    expect(html).toContain('name="shadow-strength"')
    expect(html).toContain('阴影强度')
    expect(html).toContain('name="background-overlay"')
    expect(html).toContain('背景遮罩')
    expect(html).toContain('name="theme-color"')
    expect(html).toContain('自定义 CSS')
    expect(html).toContain('name="custom-code"')
    expect(html).toContain('&lt;style&gt;.home-top-card { border-color: #2563eb; }&lt;/style&gt;')
    expect(html).toContain('&lt;script&gt;window.ZenoCustomLoaded = true;&lt;/script&gt;')
    expect(html).not.toContain('token')
    expect(html).not.toContain('secret')
    expect(html).not.toContain('credential')
    expect(html).not.toContain('hash')
  })

  it('validates settings URL fields before saving', () => {
    const baseInput = {
      expectedRevision: 4,
      siteTitle: 'Zeno',
      logoUrl: '/assets/logo/id.png',
      theme: 'system' as const,
      agentControllerUrl: '',
      backgroundUrl: 'https://example.com/desktop.webp',
      desktopBackgroundUrl: 'https://example.com/desktop.webp',
      mobileBackgroundUrl: '',
      appearancePreset: 'default' as const,
      serverCardTheme: 'classic' as const,
      cardOpacity: 0.7,
      cardBlur: 0,
      cardRadius: 20,
      borderStrength: 0.3,
      shadowStrength: 0.2,
      backgroundOverlay: 0,
      themeColor: '#2563eb',
      customCode: '',
    }

    expect(validateAdminSettingsInput(baseInput)).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, expectedRevision: -1 })).toContain('设置版本')
    expect(validateAdminSettingsInput({ ...baseInput, logoUrl: '' })).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, logoUrl: 'http://example.com/logo.png' })).toContain('头像 / Logo URL')
    expect(validateAdminSettingsInput({ ...baseInput, desktopBackgroundUrl: 'javascript:alert(1)' })).toContain('电脑端背景图 URL')
    expect(validateAdminSettingsInput({ ...baseInput, mobileBackgroundUrl: '//example.com/bg.png' })).toContain('手机端背景图 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'https://user:pass@example.com' })).toContain('Agent 接入 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'https://zeno.example.com/?token=1' })).toContain('Agent 接入 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'http://203.0.113.10:18980' })).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'http://[2001:db8::10]:18980' })).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'http://203.0.113.10' })).toContain('Agent 接入 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'http://zeno.example.com:18980' })).toContain('Agent 接入 URL')
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'http://127.0.0.1:18980' })).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, agentControllerUrl: 'https://zeno.example.com/' })).toBeNull()
    expect(validateAdminSettingsInput({ ...baseInput, appearancePreset: 'other' as never })).toContain('外观模板')
    expect(validateAdminSettingsInput({ ...baseInput, serverCardTheme: 'rack' as never })).toContain('服务器卡片主题')
    expect(validateAdminSettingsInput({ ...baseInput, cardOpacity: 0.1 })).toContain('卡片透明度')
    expect(validateAdminSettingsInput({ ...baseInput, cardBlur: 41 })).toContain('卡片模糊度')
    expect(validateAdminSettingsInput({ ...baseInput, cardRadius: 7 })).toContain('卡片圆角')
    expect(validateAdminSettingsInput({ ...baseInput, borderStrength: 1.1 })).toContain('边框强度')
    expect(validateAdminSettingsInput({ ...baseInput, shadowStrength: 1.1 })).toContain('阴影强度')
    expect(validateAdminSettingsInput({ ...baseInput, backgroundOverlay: 0.9 })).toContain('背景遮罩')
    expect(validateAdminSettingsInput({ ...baseInput, themeColor: 'blue' })).toContain('主题色')
    expect(validateAdminSettingsInput({ ...baseInput, customCode: 'a'.repeat(60001) })).toContain('自定义代码')
  })

  it('reveals a warmed backend only after login or protected data is ready', () => {
    expect(adminSurfaceIsReady(false, false, { kind: 'idle' })).toBe(false)
    expect(adminSurfaceIsReady(false, true, { kind: 'idle' })).toBe(true)
    expect(adminSurfaceIsReady(true, true, { kind: 'loading' })).toBe(false)
    expect(adminSurfaceIsReady(true, true, { kind: 'ready', account: { username: 'admin' }, nodes: [], targets: [], notificationChannels: [], alertRules: [] })).toBe(true)
    expect(adminSurfaceIsReady(true, true, { kind: 'error', message: 'failed' })).toBe(true)
  })

  it('applies custom code through managed document nodes', () => {
    type TestElement = {
      id: string
      type: string
      hidden: boolean
      nodeName: string
      textContent: string | null
      attributes: Array<{ name: string; value: string }>
      childNodes: TestElement[]
      appendChild: (child: TestElement) => TestElement
      setAttribute: (name: string, value: string) => void
      remove: () => void
    }
    const documentStub = {
      customNodes: [] as TestElement[],
      head: {
        children: [] as TestElement[],
        appendChild(element: TestElement) {
          this.children.push(element)
          return element
        },
      },
      body: {
        children: [] as TestElement[],
        appendChild(element: TestElement) {
          this.children.push(element)
          return element
        },
      },
      createElement(tag: string): TestElement | { nodeName: string; content: { childNodes: TestElement[] }; innerHTML: string } {
        const makeElement = (nodeName: string): TestElement => {
          const element: TestElement = {
            id: '',
            type: '',
            hidden: false,
            nodeName,
            textContent: null,
            attributes: [],
            childNodes: [],
            appendChild(child: TestElement) {
              this.childNodes.push(child)
              return child
            },
            setAttribute(name: string, value: string) {
              this.attributes.push({ name, value })
              if (name === 'data-zeno-custom-code') documentStub.customNodes.push(element)
            },
            remove() {
              documentStub.head.children = documentStub.head.children.filter((child) => child !== element)
              documentStub.body.children = documentStub.body.children.filter((child) => child !== element)
              documentStub.customNodes = documentStub.customNodes.filter((child) => child !== element)
            },
          }
          return element
        }
        if (tag === 'template') {
          const style = makeElement('STYLE')
          style.textContent = '.home-top-card { border-color: #2563eb; }'
          const script = makeElement('SCRIPT')
          script.textContent = 'window.ZenoCustomLoaded = true;'
          return { nodeName: 'TEMPLATE', content: { childNodes: [style, script] }, innerHTML: '' }
        }
        return makeElement(tag.toUpperCase())
      },
      querySelectorAll(selector: string) {
        return selector === '[data-zeno-custom-code]' ? this.customNodes : []
      },
    }
    const previousDocument = globalThis.document
    try {
      Object.defineProperty(globalThis, 'document', { value: documentStub, configurable: true })
      applyCustomCode(settings)
      expect(documentStub.body.children.some((child) => child.textContent === 'window.ZenoCustomLoaded = true;')).toBe(false)
      expect(documentStub.head.children.some((child) => child.nodeName === 'STYLE' && child.textContent === '.home-top-card { border-color: #2563eb; }')).toBe(true)
      applyCustomCode({ ...settings, customCode: '' })
      expect(documentStub.querySelectorAll('[data-zeno-custom-code]')).toHaveLength(0)
    } finally {
      Object.defineProperty(globalThis, 'document', { value: previousDocument, configurable: true })
    }
  })

  it('renders real notification channels and types instead of a placeholder', () => {
    const html = renderAdmin('notifications')

    expect(html).toContain('通知渠道')
    expect(html).toContain('通知类型')
    expect(html).toContain('Zeno Telegram')
    expect(html).not.toContain('接收人')
    expect(html).not.toContain('Bot Token</span>')
    expect(html).not.toContain('凭据已设置')
    expect(html).toContain('添加通知类型')
    expect(html).toContain('<button class="admin-primary-action" type="button">添加通知类型</button>')
    expect(html).toContain('CPU 使用率')
    expect(html).toContain('启用中')
    expect(html).toContain('添加通知渠道')
    expect(html).toContain('aria-label="编辑通知渠道 Zeno Telegram"')
    expect(html).toContain('aria-label="删除通知渠道 Zeno Telegram"')
    expect(html).toContain('title="编辑渠道"')
    expect(html).toContain('title="删除渠道"')
    expect(html).toContain('<span class="sr-only">编辑渠道</span>')
    expect(html).toContain('<span class="sr-only">删除渠道</span>')
    expect(html).not.toContain('停用渠道')
    expect(html).not.toContain('启用渠道')
    expect(html).not.toContain('<button class="admin-row-action" type="button">测试发送</button>')
    expect(html).not.toContain('zeno-telegram')
    expect(html).not.toContain('7579942307')
    expect(html).not.toContain('telegram-bot-secret')
    expect(html).not.toContain('告警')
  })

  it('renders notification type triggers in the notifications section', () => {
    const html = renderAdmin('notifications')

    expect(html).toContain('通知类型')
    expect(html).toContain('CPU 使用率')
    expect(html).not.toContain('data-label="范围"')
    expect(html).not.toContain('全部服务器')
    expect(html).toContain('离线通知')
    expect(html).not.toContain('node_offline')
    expect(html).toContain('添加通知类型')
    expect(html).toContain('aria-label="编辑通知类型 CPU 使用率"')
    expect(html).toContain('aria-label="删除通知类型 CPU 使用率"')
    expect(html).not.toContain('移除')
    expect(html).not.toContain('cpu_high · 资源')
    expect(html).not.toContain('触发条件</h4>')
    expect(html).not.toContain('告警')
    expect(html).not.toContain('telegram-bot-secret')
  })


  it('renders a unified username and password login screen when unauthenticated', () => {
    const html = renderToStaticMarkup(<AdminDashboard onHome={() => {}} />)

    expect(html).toContain('admin-login-card')
    expect(html).toContain('name="admin-username"')
    expect(html).toContain('name="admin-password"')
    expect(html).toContain('placeholder="admin"')
    expect(html).toContain('后台登录')
    expect(html).not.toContain('默认账号：' + 'admin / admin')
    expect(html).not.toContain('列表 / 弹窗编辑')
    expect(html).not.toContain('控' + '制台')
    expect(html).not.toContain('Admin Token')
  })

  it('renders authenticated server management as a compact list, not detailed cards', () => {
    const html = renderAdmin('nodes')

    expect(html).toContain('服务器列表')
    expect(html).toContain('admin-list')
    expect(html).toContain('Example Node A')
    expect(html).not.toContain('<span>状态</span>')
    expect(html).not.toContain('data-label="状态"')
    expect(html).toContain('Agent 版本')
    expect(html).toContain('agent-test')
    expect(html).toContain('198.51.100.8')
    expect(html).toContain('2001:db8::8')
    expect(html).not.toContain('v4 198.51.100.8')
    expect(html).not.toContain('v6 2001:db8::8')
    expect(html).toContain('admin-ip-stack')
    expect(html).not.toContain('debian 13')
    expect(html).not.toContain('2026-08-01')
    expect(html).not.toContain('月付')
    expect(html).not.toContain('example-harbor · 🇭🇰 HK · 顺序 10')
    expect(html).not.toContain('顺序 10')
    expect(html).toContain('服务器排序')
    expect(html).not.toContain('name="node-sort"')
    expect(html).not.toContain('按状态排序')
    expect(html).not.toContain('按 Agent 排序')
    expect(html).not.toContain('按公网 IP 排序')
    expect(html).not.toContain('整理顺序')
    expect(html).not.toContain('上移')
    expect(html).not.toContain('下移')
    expect(html).toContain('aria-label="编辑服务器 Example Node A"')
    expect(html).toContain('aria-label="删除服务器 Example Node A"')
    expect(html).toContain('admin-row-action is-icon')
    expect(html).toContain('admin-row-action is-icon is-danger')
    expect(html).not.toContain('admin-node-card')
    expect(html).not.toContain('name="display-name"')
    expect(html).not.toContain('保存服务器')
    expect(html).not.toContain('admin-pass')
  })

  it('keeps latency monitor management on its own list page', () => {
    const html = renderAdmin('targets')

    expect(html).toContain('延迟监控')
    expect(html).toContain('admin-target-list')
    expect(html).not.toContain('name="target-sort"')
    expect(html).not.toContain('按手动顺序')
    expect(html).not.toContain('按名称排序')
    expect(html).not.toContain('按启用状态排序')
    expect(html).not.toContain('整理顺序')
    expect(html).not.toContain('<span>状态</span>')
    expect(html).not.toContain('data-label="状态"')
    expect(html).not.toContain('>启用中<')
    expect(html).not.toContain('example-node-a-local')
    expect(html).not.toContain('顺序 20')
    expect(html).toContain('127.0.0.1:18980')
    expect(html).not.toContain('3 次 / 1200ms / 60s')
    expect(html).toContain('1 / 2 服务器启用')
    expect(html).toContain('aria-label="编辑目标 Example Node A"')
    expect(html).toContain('aria-label="删除目标 Example Node A"')
    expect(html).toContain('admin-row-action is-icon')
    expect(html).toContain('admin-row-action is-icon is-danger')
    expect(html).not.toContain('停用目标')
    expect(html).not.toContain('全节点启用')
    expect(html).not.toContain('全节点停用')
    expect(html).not.toContain('上移')
    expect(html).not.toContain('下移')
    expect(html.indexOf('Example ICMP')).toBeLessThan(html.indexOf('Example Node A'))
    expect(html).not.toContain('admin-target-card')
    expect(html).not.toContain('name="target-name"')
    expect(html).not.toContain('保存目标')
    expect(html).not.toContain('admin-pass')
  })

  it('reports monitor availability only through per-server assignments', () => {
    expect(formatTargetAssignmentSummary(exampleNodeATarget)).toBe('1 / 2 服务器启用')
    expect(formatTargetAssignmentSummary({ ...exampleNodeATarget, assignments: [] })).toBe('未分配服务器')
  })

  it('renders ping monitor targets without requiring a port', () => {
    const html = renderAdmin('targets')

    expect(html).toContain('Example ICMP')
    expect(html).not.toContain('ICMP Ping')
    expect(html).toContain('8.8.8.8')
    expect(html).not.toContain('4 次 / 900ms / 45s')
    expect(html).toContain('1 / 1 服务器启用')
    expect(html).not.toContain('8.8.8.8:')
  })

  it('renders HTTP GET monitor targets without requiring a port', () => {
    const html = renderAdmin('targets')

    expect(html).toContain('Zeno Health HTTP')
    expect(html).not.toContain('HTTP GET')
    expect(html).toContain('https://example.com/health')
    expect(html).not.toContain('2 次 / 1500ms / 60s')
    expect(html).toContain('1 / 1 服务器启用')
    expect(html).not.toContain('https://example.com/health:')
  })

  it('does not render every admin workspace on one page', () => {
    const nodeHtml = renderAdmin('nodes')
    const targetHtml = renderAdmin('targets')

    expect(nodeHtml).toContain('服务器列表')
    expect(nodeHtml).not.toContain('延迟监控目标列表')
    expect(targetHtml).toContain('延迟监控目标列表')
    expect(targetHtml).not.toContain('服务器列表')
  })
})
