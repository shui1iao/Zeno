import { afterEach, describe, expect, it, vi } from 'vitest'
import { createAdminNode, deleteAdminNode, fetchAdminNodes, reorderAdminNodes, requestAdminNodeInstallCommand, updateAdminNode } from './client'

describe('fetchAdminNodes', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('sends the admin token in X-Admin-Token and never in the URL', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({ nodes: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await fetchAdminNodes('admin-pass')

    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes', {
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })
})
describe('updateAdminNode', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('patches editable node fields with the admin token in X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      node: {
        id: 'example-node-a',
        display_name: 'Example Node A Edited',
        status: 'disabled',
        country_code: 'HK',
        region: 'Hong Kong',
        home_probe_target_id: 'cloudflare',
        disabled: true,
        billing_mode: 'max',
        monthly_reset_day: 20,
        expiry_date: '2026-08-01',
        billing_cycle: '月付',
        display_order: 10,
        public_ipv4: '198.51.100.8',
        public_ipv6: '2001:db8::8',
        monthly_quota_bytes: 123456789,
        created_at: '2026-07-02T00:00:00Z',
        updated_at: '2026-07-03T00:00:00Z',
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const controller = new AbortController()
    const node = await updateAdminNode('admin-pass', 'example-node-a', {
      displayName: 'Example Node A Edited',
      countryCode: 'HK',
      region: 'Hong Kong',
      homeProbeTargetId: 'cloudflare',
      expiryDate: '2026-08-01',
      billingCycle: '月付',
      renewalAmount: 20,
      renewalCurrency: 'USD',
      billingMode: 'max',
      monthlyResetDay: 20,
      displayOrder: 10,
      publicIPv4: '198.51.100.8',
      publicIPv6: '2001:db8::8',
      monthlyQuotaBytes: 123456789,
      disabled: true,
      probeTargetIds: ['cloudflare', 'google'],
    }, controller.signal)

    expect(node.displayName).toBe('Example Node A Edited')
    expect(node.disabled).toBe(true)
    expect(node.monthlyQuotaBytes).toBe(123456789)
    expect(node.homeProbeTargetId).toBe('cloudflare')
    expect(node.expiryDate).toBe('2026-08-01')
    expect(node.billingCycle).toBe('月付')
    expect(node.billingMode).toBe('max')
    expect(node.monthlyResetDay).toBe(20)
    expect(node.displayOrder).toBe(10)
    expect(node.publicIPv4).toBe('198.51.100.8')
    expect(node.publicIPv6).toBe('2001:db8::8')
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes/example-node-a', {
      method: 'PATCH',
      signal: controller.signal,
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        display_name: 'Example Node A Edited',
        country_code: 'HK',
        region: 'Hong Kong',
        home_probe_target_id: 'cloudflare',
        expiry_date: '2026-08-01',
        billing_cycle: '月付',
        renewal_amount: 20,
        renewal_currency: 'USD',
        billing_mode: 'max',
        monthly_reset_day: 20,
        display_order: 10,
        public_ipv4: '198.51.100.8',
        public_ipv6: '2001:db8::8',
        monthly_quota_bytes: 123456789,
        disabled: true,
        probe_target_ids: ['cloudflare', 'google'],
      }),
    })
  })
})
describe('reorderAdminNodes', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('sends the complete target order in one PATCH request', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await reorderAdminNodes('admin-pass', ['node-c', 'node-a', 'node-b'])

    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes/reorder', {
      method: 'PATCH',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({ node_ids: ['node-c', 'node-a', 'node-b'] }),
    })
  })
})
describe('deleteAdminNode', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('deletes a node with the admin token in X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    await deleteAdminNode('admin-pass', 'example-node-a')

    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes/example-node-a', {
      method: 'DELETE',
      headers: {
        Accept: 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
    })
  })
})
describe('createAdminNode', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('creates a backend-first node with editable fields and the admin token in X-Admin-Token only', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      node: {
        id: 'new-server-a1b2c3d4',
        display_name: 'New Server',
        status: 'no_data',
        country_code: 'US',
        region: 'Los Angeles',
        disabled: false,
        billing_mode: 'out',
        monthly_reset_day: 10,
        expiry_date: '2026-09-01',
        billing_cycle: '月付',
        display_order: 20,
        public_ipv4: '203.0.113.20',
        public_ipv6: '2001:db8::20',
        monthly_quota_bytes: 1099511627776,
        created_at: '2026-07-03T00:00:00Z',
        updated_at: '2026-07-03T00:00:00Z',
      },
    }), { status: 201, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const node = await createAdminNode('admin-pass', {
      displayName: 'New Server',
      countryCode: 'US',
      region: 'Los Angeles',
      expiryDate: '2026-09-01',
      billingCycle: '月付',
      renewalAmount: 88,
      renewalCurrency: 'CNY',
      billingMode: 'out',
      monthlyResetDay: 10,
      displayOrder: 20,
      publicIPv4: '203.0.113.20',
      publicIPv6: '2001:db8::20',
      monthlyQuotaBytes: 1099511627776,
    })

    expect(node.id).toBe('new-server-a1b2c3d4')
    expect(node.status).toBe('no_data')
    expect(node.displayOrder).toBe(20)
    expect(node.publicIPv4).toBe('203.0.113.20')
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes', {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({
        display_name: 'New Server',
        country_code: 'US',
        region: 'Los Angeles',
        expiry_date: '2026-09-01',
        billing_cycle: '月付',
        renewal_amount: 88,
        renewal_currency: 'CNY',
        billing_mode: 'out',
        monthly_reset_day: 10,
        display_order: 20,
        public_ipv4: '203.0.113.20',
        public_ipv6: '2001:db8::20',
        monthly_quota_bytes: 1099511627776,
      }),
    })
  })
})
describe('requestAdminNodeInstallCommand', () => {
  const originalFetch = globalThis.fetch

  afterEach(() => {
    globalThis.fetch = originalFetch
    vi.restoreAllMocks()
  })

  it('requests an install command from the node edit context without putting the admin token in the URL', async () => {
    const fetchMock = vi.fn(async () => new Response(JSON.stringify({
      node_id: 'example-node-a',
      command: "ZENO_INSTALL_URL='https://zeno.siaoz.si/agent/install.sh' ZENO_CONTROLLER_URL='https://probe.example.com' ZENO_NODE_ID='example-node-a' bash -o pipefail -c 'curl -fsSL \"$ZENO_INSTALL_URL\" | sudo env ZENO_CONTROLLER_URL=\"$ZENO_CONTROLLER_URL\" ZENO_NODE_ID=\"$ZENO_NODE_ID\" bash'",
      commands: {
        linux: "ZENO_INSTALL_URL='https://zeno.siaoz.si/agent/install.sh' ZENO_CONTROLLER_URL='https://probe.example.com' ZENO_NODE_ID='example-node-a' bash -o pipefail -c 'curl -fsSL \"$ZENO_INSTALL_URL\" | sudo env ZENO_CONTROLLER_URL=\"$ZENO_CONTROLLER_URL\" ZENO_NODE_ID=\"$ZENO_NODE_ID\" bash'",
        macos: "ZENO_INSTALL_URL='https://zeno.siaoz.si/agent/install.sh' ZENO_CONTROLLER_URL='https://probe.example.com' ZENO_NODE_ID='example-node-a' bash -o pipefail -c 'curl -fsSL \"$ZENO_INSTALL_URL\" | sudo env ZENO_CONTROLLER_URL=\"$ZENO_CONTROLLER_URL\" ZENO_NODE_ID=\"$ZENO_NODE_ID\" bash'",
        windows: "powershell -NoProfile -ExecutionPolicy Bypass -Command \"$env:ZENO_CONTROLLER_URL='https://probe.example.com'; irm 'https://zeno.siaoz.si/agent/install.ps1' | iex\"",
      },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    globalThis.fetch = fetchMock as unknown as typeof fetch

    const result = await requestAdminNodeInstallCommand('admin-pass', 'example-node-a', 'https://probe.example.com')

    expect(result.nodeId).toBe('example-node-a')
    expect(result.command).toContain('zeno.siaoz.si/agent/install.sh')
    expect(result.command).toContain('bash -o pipefail')
    expect(result.commands.windows).toContain('zeno.siaoz.si/agent/install.ps1')
    expect(fetchMock).toHaveBeenCalledWith('/api/admin/v1/nodes/example-node-a/install-command', {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        'X-Admin-Token': 'admin-pass',
      },
      body: JSON.stringify({ controller_url: 'https://probe.example.com' }),
    })
  })

  it('explains how to fix a missing Agent controller URL without exposing a raw 409', async () => {
    globalThis.fetch = vi.fn(async () => new Response(JSON.stringify({
      error: 'configure agent controller url before generating install commands',
    }), { status: 409, headers: { 'Content-Type': 'application/json' } })) as unknown as typeof fetch

    await expect(requestAdminNodeInstallCommand('admin-pass', 'mechrevo')).rejects.toThrow(
      '当前后台访问地址无法用于 Agent 接入，请在系统设置中填写 Agent 可访问的接入地址。',
    )
  })
})
