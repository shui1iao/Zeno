import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { AdminOperationalWorkspace } from './AdminOperationalWorkspace'
import { AdminNodeWorkspace } from './AdminNodeWorkspace'
import { AdminNotificationsWorkspace } from './AdminNotificationsWorkspace'
import { AdminTargetWorkspace } from './AdminTargetWorkspace'

const commonProps = {
  nodes: [],
  targets: [],
  notificationChannels: [],
  alertRules: [],
  onNodeCreate: async () => undefined,
  onNodeUpdate: () => {},
  onNodeReorder: () => {},
  onNodeDelete: () => {},
  onInstallCommand: async () => ({ nodeId: 'node-1', command: 'install', commands: { linux: 'install' } }),
  onProbeTargetCreate: () => {},
  onProbeTargetUpdate: () => {},
  onProbeTargetReorder: () => {},
  onProbeTargetDelete: () => {},
  onNotificationChannelCreate: () => {},
  onNotificationChannelUpdate: () => {},
  onNotificationChannelDelete: () => {},
  onNotificationChannelTest: () => {},
  onAlertRuleUpdate: () => {},
}

const synchronousSections = {
  nodes: AdminNodeWorkspace,
  targets: AdminTargetWorkspace,
  notifications: AdminNotificationsWorkspace,
}

describe('AdminOperationalWorkspace', () => {
  it('keeps operational admin sections behind one cohesive router', () => {
    const html = renderToStaticMarkup(
      <AdminOperationalWorkspace {...commonProps} activeSection="nodes" sectionComponents={synchronousSections} />,
    )

    expect(html).toContain('aria-label="admin node list"')
    expect(html).toContain('服务器列表')
    expect(html).toContain('还没有节点。')
    expect(html).not.toContain('通知渠道')
  })

  it('routes targets and notifications to separate feature components', () => {
    const targetsHTML = renderToStaticMarkup(<AdminOperationalWorkspace {...commonProps} activeSection="targets" sectionComponents={synchronousSections} />)
    const notificationsHTML = renderToStaticMarkup(<AdminOperationalWorkspace {...commonProps} activeSection="notifications" sectionComponents={synchronousSections} />)

    expect(targetsHTML).toContain('aria-label="admin probe target list"')
    expect(targetsHTML).not.toContain('延迟监控排序')
    expect(targetsHTML).toContain('添加目标')
    expect(targetsHTML).not.toContain('通知渠道')
    expect(notificationsHTML).toContain('通知渠道')
    expect(notificationsHTML).toContain('通知类型')
    expect(notificationsHTML).not.toContain('服务器列表')
  })

  it('renders the default server workspace without an intermediate loading state', () => {
    const html = renderToStaticMarkup(<AdminOperationalWorkspace {...commonProps} activeSection="nodes" />)
    expect(html).toContain('服务器列表')
    expect(html).not.toContain('加载中…')
  })
})
