import type { AdminSettingsUpdateInput } from '../api/adminClient'

const maxSettingsCustomCodeLength = 60000

export function isAdminUnauthorizedError(error: unknown): boolean {
  return error instanceof Error && /^admin .+: 401$/.test(error.message)
}


export function validateAdminSettingsInput(input: AdminSettingsUpdateInput): string | null {
  if (!Number.isSafeInteger(input.expectedRevision) || input.expectedRevision < 0) return '设置版本无效，请载入最新设置。'
  if (!validSettingsImageURL(input.logoUrl ?? '')) return '头像 / Logo URL 只能是 https:// 链接或 /assets/... 站内路径。'
  if (!validSettingsImageURL(input.desktopBackgroundUrl ?? input.backgroundUrl ?? '')) return '电脑端背景图 URL 只能是 https:// 链接或 /assets/... 站内路径。'
  if (!validSettingsImageURL(input.mobileBackgroundUrl ?? '')) return '手机端背景图 URL 只能是 https:// 链接或 /assets/... 站内路径。'
  if (!validAgentControllerURL(input.agentControllerUrl ?? '')) return 'Agent 接入 URL 必须使用 https://；loopback 或“直接 IP + 显式端口”可使用 http://，且不能包含用户名密码、query 或 fragment。'
  if (input.appearancePreset !== undefined && input.appearancePreset !== 'default' && input.appearancePreset !== 'gaussian_blur') return '外观模板无效。'
  if (input.serverCardTheme !== undefined && input.serverCardTheme !== 'classic' && input.serverCardTheme !== 'capsule') return '服务器卡片主题无效。'
  if (!validSettingsNumber(input.cardOpacity, 0.2, 1)) return '卡片透明度无效。'
  if (!validSettingsNumber(input.cardBlur, 0, 40)) return '卡片模糊度无效。'
  if (!validSettingsNumber(input.cardRadius, 8, 36)) return '卡片圆角无效。'
  if (!validSettingsNumber(input.borderStrength, 0, 1)) return '边框强度无效。'
  if (!validSettingsNumber(input.shadowStrength, 0, 1)) return '阴影强度无效。'
  if (!validSettingsNumber(input.backgroundOverlay, 0, 0.8)) return '背景遮罩无效。'
  if (input.themeColor !== undefined && !/^#[0-9a-fA-F]{6}$/.test(input.themeColor)) return '主题色无效。'
  if (customCodeLength(input.customCode ?? '') > maxSettingsCustomCodeLength) return '自定义代码不能超过 60000 字。'
  return null
}

function validSettingsNumber(value: number | undefined, min: number, max: number): boolean {
  return value === undefined || (Number.isFinite(value) && value >= min && value <= max)
}

function customCodeLength(value: string): number {
  return Array.from(value.trim()).length
}

function validSettingsImageURL(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed === '') return true
  if (trimmed.startsWith('/') && !trimmed.startsWith('//')) return true
  try {
    const parsed = new URL(trimmed)
    return parsed.protocol === 'https:' && parsed.hostname !== '' && parsed.username === '' && parsed.password === ''
  } catch {
    return false
  }
}

function validAgentControllerURL(value: string): boolean {
  const trimmed = value.trim().replace(/\/+$/, '')
  if (trimmed === '') return true
  try {
    const parsed = new URL(trimmed)
    const host = parsed.hostname.toLowerCase().replace(/^\[|\]$/g, '')
    const ipv4 = host.split('.')
    const validIPv4 = ipv4.length === 4 && ipv4.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255)
    const loopbackIPv4 = validIPv4 && ipv4[0] === '127'
    const loopback = host === 'localhost' || host === '::1' || loopbackIPv4
    const authority = trimmed.match(/^http:\/\/([^/?#]+)/i)?.[1] ?? ''
    const explicitPortMatch = authority.match(/^\[[^\]]+\]:(\d+)$/) ?? authority.match(/^[^:]+:(\d+)$/)
    const explicitPort = explicitPortMatch ? Number(explicitPortMatch[1]) : 0
    const directIPWithPort = (validIPv4 || host.includes(':')) && explicitPort >= 1 && explicitPort <= 65535
    return (parsed.protocol === 'https:' || (parsed.protocol === 'http:' && (loopback || directIPWithPort))) && parsed.hostname !== '' && parsed.username === '' && parsed.password === '' && parsed.search === '' && parsed.hash === ''
  } catch {
    return false
  }
}

export function remoteInsecureAgentControllerURL(value: string): boolean {
  try {
    const parsed = new URL(value.trim())
    if (parsed.protocol !== 'http:') return false
    const host = parsed.hostname.toLowerCase().replace(/^\[|\]$/g, '').replace(/\.$/, '')
    if (host === 'localhost' || host === '::1') return false
    const ipv4 = host.split('.')
    return !(ipv4.length === 4 && ipv4.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255) && ipv4[0] === '127')
  } catch {
    return false
  }
}
