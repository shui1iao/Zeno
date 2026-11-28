import { type CSSProperties, type FormEvent, useEffect, useState } from 'react'
import { AdminSettingsConflictError, type AdminSettingsUpdateInput } from '../../api/adminClient'
import { appearancePresetOptions, appearancePresets, appearanceValuesForSettings, resolvedTheme, shellStyleForSettings, themeOptions, type AppearanceValues } from '../../lib/appearance'
import { validateAdminSettingsInput } from '../../lib/adminSettings'
import { runMaybePromise, type MaybePromise } from '../../lib/maybePromise'
import type { AdminSettings, AppearancePreset, ServerCardTheme } from '../../types'
import { SlidingSelector } from '../SlidingSelector'
import { AdminFormSection, AdminActionFooter, AdminWorkspaceHeading, adminErrorText } from './AdminPrimitives'

export interface AdminSettingsSectionProps {
  settings: AdminSettings
  onUpdate: (input: AdminSettingsUpdateInput) => MaybePromise<AdminSettings | void>
}

type AdminSettingsDraft = {
  siteTitle: string
  logoUrl: string
  agentControllerUrl: string
  desktopBackgroundUrl: string
  mobileBackgroundUrl: string
  customCode: string
  theme: AdminSettings['theme']
  serverCardTheme: ServerCardTheme
  appearance: AppearanceValues
}

const serverCardThemeOptions: Array<{ value: ServerCardTheme; label: string }> = [
  { value: 'classic', label: '经典卡片' },
  { value: 'capsule', label: '节点舱' },
]

function adminSettingsDraft(settings: AdminSettings): AdminSettingsDraft {
  return {
    siteTitle: settings.siteTitle,
    logoUrl: settings.logoUrl,
    agentControllerUrl: settings.agentControllerUrl,
    desktopBackgroundUrl: settings.desktopBackgroundUrl || settings.backgroundUrl,
    mobileBackgroundUrl: settings.mobileBackgroundUrl,
    customCode: settings.customCode,
    theme: settings.theme,
    serverCardTheme: settings.serverCardTheme,
    appearance: appearanceValuesForSettings(settings),
  }
}

function adminSettingsDraftFingerprint(draft: AdminSettingsDraft): string {
  return JSON.stringify(draft)
}

function adminSettingsFingerprint(settings: AdminSettings): string {
  return `${settings.revision}:${adminSettingsDraftFingerprint(adminSettingsDraft(settings))}`
}

export default function AdminSettingsSection({ settings, onUpdate }: AdminSettingsSectionProps) {
  const [settingsError, setSettingsError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [draft, setDraft] = useState<AdminSettingsDraft>(() => adminSettingsDraft(settings))
  const [baseline, setBaseline] = useState<AdminSettingsDraft>(() => adminSettingsDraft(settings))
  const [sourceFingerprint, setSourceFingerprint] = useState(() => adminSettingsFingerprint(settings))
  const [sourceRevision, setSourceRevision] = useState(settings.revision)
  const [serverConflict, setServerConflict] = useState(false)
  const [conflictSettings, setConflictSettings] = useState<AdminSettings | null>(null)
  const serverFingerprint = adminSettingsFingerprint(settings)
  const dirty = adminSettingsDraftFingerprint(draft) !== adminSettingsDraftFingerprint(baseline)
  const appearance = draft.appearance
  const previewTheme = draft.theme
  useEffect(() => {
    if (serverFingerprint === sourceFingerprint) return
    if (dirty) {
      setServerConflict(true)
      setConflictSettings(settings)
      return
    }
    const nextDraft = adminSettingsDraft(settings)
    setDraft(nextDraft)
    setBaseline(nextDraft)
    setSourceFingerprint(serverFingerprint)
    setSourceRevision(settings.revision)
    setServerConflict(false)
    setConflictSettings(null)
  }, [serverFingerprint])
  const updateDraft = (patch: Partial<AdminSettingsDraft>) => setDraft((current) => ({ ...current, ...patch }))
  const updateAppearance = (patch: Partial<AppearanceValues>) => setDraft((current) => ({ ...current, appearance: { ...current.appearance, ...patch } }))
  const updateAppearancePreset = (value: string) => {
    const preset = value === 'gaussian_blur' ? 'gaussian_blur' : 'default'
    updateDraft({ appearance: appearancePresets[preset] })
  }
  const loadLatestSettings = () => {
    const latestSettings = conflictSettings ?? settings
    const nextDraft = adminSettingsDraft(latestSettings)
    setDraft(nextDraft)
    setBaseline(nextDraft)
    setSourceFingerprint(adminSettingsFingerprint(latestSettings))
    setSourceRevision(latestSettings.revision)
    setServerConflict(false)
    setConflictSettings(null)
    setSettingsError(null)
  }
  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    if (serverConflict) {
      setSettingsError('服务端设置已经更新，请先载入最新设置。')
      return
    }
    const input: AdminSettingsUpdateInput = {
      expectedRevision: sourceRevision,
      siteTitle: draft.siteTitle.trim(),
      logoUrl: draft.logoUrl.trim(),
      theme: draft.theme,
      agentControllerUrl: draft.agentControllerUrl.trim(),
      backgroundUrl: draft.desktopBackgroundUrl.trim(),
      desktopBackgroundUrl: draft.desktopBackgroundUrl.trim(),
      mobileBackgroundUrl: draft.mobileBackgroundUrl.trim(),
      appearancePreset: appearance.appearancePreset,
      serverCardTheme: draft.serverCardTheme,
      cardOpacity: appearance.cardOpacity,
      cardBlur: appearance.cardBlur,
      cardRadius: appearance.cardRadius,
      borderStrength: appearance.borderStrength,
      shadowStrength: appearance.shadowStrength,
      backgroundOverlay: appearance.backgroundOverlay,
      themeColor: appearance.themeColor.trim(),
      customCode: draft.customCode.trim(),
    }
    const validationError = validateAdminSettingsInput(input)
    if (validationError) {
      setSettingsError(validationError)
      return
    }
    setSettingsError(null)
    setSubmitting(true)
    const submittedDraft = draft
    runMaybePromise(() => onUpdate(input))
      .then((updatedSettings) => {
        const acceptedDraft = updatedSettings ? adminSettingsDraft(updatedSettings) : submittedDraft
        setDraft(acceptedDraft)
        setBaseline(acceptedDraft)
        setSourceFingerprint(updatedSettings ? adminSettingsFingerprint(updatedSettings) : serverFingerprint)
        setSourceRevision(updatedSettings?.revision ?? sourceRevision)
        setServerConflict(false)
        setConflictSettings(null)
      })
      .catch((error: unknown) => {
        if (error instanceof AdminSettingsConflictError) {
          setServerConflict(true)
          setConflictSettings(error.latestSettings)
          setSettingsError(null)
          return
        }
        setSettingsError(adminErrorText(error, '设置保存失败'))
      })
      .finally(() => setSubmitting(false))
  }

  return (
    <section className="admin-settings-section admin-workspace-panel" aria-label="admin settings">
      <AdminWorkspaceHeading title="站点设置" />
      <form className="admin-settings-form admin-node-edit-form is-sectioned admin-workspace-form" aria-label="外观配置" aria-busy={submitting} inert={submitting ? true : undefined} onSubmit={handleSubmit}>
        <div className="admin-workspace-card admin-settings-card">
          <section className="admin-settings-card-section" aria-label="站点设置">
            <div className="admin-form-grid">
              <label><span>站点标题</span><input name="site-title" autoComplete="off" value={draft.siteTitle} onChange={(event) => updateDraft({ siteTitle: event.currentTarget.value })} /></label>
              <label><span>头像 / Logo URL</span><input name="logo-url" autoComplete="off" value={draft.logoUrl} onChange={(event) => updateDraft({ logoUrl: event.currentTarget.value })} placeholder="可留空" /></label>
              <label className="admin-form-span-2"><span>Agent 接入 URL</span><input name="agent-controller-url" autoComplete="off" value={draft.agentControllerUrl} onChange={(event) => updateDraft({ agentControllerUrl: event.currentTarget.value })} placeholder="留空则使用当前后台访问地址" /></label>
              <label className="admin-form-span-2">
                <span>自定义 CSS</span>
                <textarea className="admin-code-field" name="custom-code" value={draft.customCode} onChange={(event) => updateDraft({ customCode: event.currentTarget.value })} spellCheck={false} placeholder={'<style>\n.home-top-card { border-color: #2563eb; }\n</style>'} />
              </label>
            </div>
          </section>
          <AdminFormSection className="admin-settings-card-section admin-settings-appearance-card" title="界面外观">
            <div className="admin-form-grid">
              <AdminThemeModeSelector value={previewTheme} onChange={(theme) => updateDraft({ theme })} />
              <AdminCardThemeSelector value={draft.serverCardTheme} onChange={(serverCardTheme) => updateDraft({ serverCardTheme })} />
              <label><span>电脑端背景图 URL</span><input name="desktop-background-url" autoComplete="off" value={draft.desktopBackgroundUrl} onChange={(event) => updateDraft({ desktopBackgroundUrl: event.currentTarget.value })} placeholder="可留空" /></label>
              <label><span>手机端背景图 URL</span><input name="mobile-background-url" autoComplete="off" value={draft.mobileBackgroundUrl} onChange={(event) => updateDraft({ mobileBackgroundUrl: event.currentTarget.value })} placeholder="可留空，默认跟随电脑端" /></label>
            </div>
            <div className="admin-appearance-layout">
              <div className="admin-appearance-main">
                <div className="admin-appearance-top">
                  <AdminAppearancePresetSlider value={appearance.appearancePreset} onChange={updateAppearancePreset} />
                  <label className="admin-color-field">
                    <span>主题色</span>
                    <span className="admin-color-field__row">
                      <input name="theme-color" type="color" value={appearance.themeColor} onChange={(event) => updateAppearance({ themeColor: event.currentTarget.value })} />
                      <strong>{appearance.themeColor.toUpperCase()}</strong>
                    </span>
                  </label>
                </div>
                <div className="admin-style-grid">
                  <AdminStyleRangeField name="card-opacity" label="卡片透明度" value={appearance.cardOpacity} min={0.2} max={1} step={0.01} onChange={(value) => updateAppearance({ cardOpacity: value })} formatValue={(value) => `${Math.round(value * 100)}%`} />
                  <AdminStyleRangeField name="card-blur" label="卡片模糊度" value={appearance.cardBlur} min={0} max={40} step={1} onChange={(value) => updateAppearance({ cardBlur: value })} formatValue={(value) => `${Math.round(value)} px`} />
                  <AdminStyleRangeField name="card-radius" label="卡片圆角" value={appearance.cardRadius} min={8} max={36} step={1} onChange={(value) => updateAppearance({ cardRadius: value })} formatValue={(value) => `${Math.round(value)} px`} />
                  <AdminStyleRangeField name="border-strength" label="边框强度" value={appearance.borderStrength} min={0} max={1} step={0.01} onChange={(value) => updateAppearance({ borderStrength: value })} formatValue={(value) => `${Math.round(value * 100)}%`} />
                  <AdminStyleRangeField name="shadow-strength" label="阴影强度" value={appearance.shadowStrength} min={0} max={1} step={0.01} onChange={(value) => updateAppearance({ shadowStrength: value })} formatValue={(value) => `${Math.round(value * 100)}%`} />
                  <AdminStyleRangeField name="background-overlay" label="背景遮罩" value={appearance.backgroundOverlay} min={0} max={0.8} step={0.01} onChange={(value) => updateAppearance({ backgroundOverlay: value })} formatValue={(value) => `${Math.round(value * 100)}%`} />
                </div>
              </div>
              <AdminAppearancePreview appearance={appearance} draft={draft} settings={settings} theme={previewTheme} />
            </div>
            {serverConflict && <p className="admin-inline-note is-error" role="alert">检测到服务端设置已更新。<button type="button" onClick={loadLatestSettings}>载入最新设置</button></p>}
            {settingsError && <p className="admin-install-error">{settingsError}</p>}
            <AdminActionFooter><button type="submit" disabled={submitting || serverConflict || !dirty}>{submitting ? '保存中…' : '保存设置'}</button></AdminActionFooter>
          </AdminFormSection>
        </div>
      </form>
    </section>
  )
}

function AdminThemeModeSelector({ value, onChange }: { value: AdminSettings['theme']; onChange: (value: AdminSettings['theme']) => void }) {
  return (
    <div className="admin-appearance-preset-field admin-theme-mode-field">
      <span>主题</span>
      <input type="hidden" name="theme" value={value} />
      <SlidingSelector
        ariaLabel="站点主题"
        role="group"
        className="admin-theme-mode-slider sliding-selector--medium"
        options={themeOptions.map((option) => ({
          value: option.value,
          content: <span className="admin-appearance-preset-option"><strong>{option.label}</strong></span>,
        }))}
        value={value}
        onChange={(nextValue) => onChange(nextValue === 'light' || nextValue === 'dark' ? nextValue : 'system')}
      />
    </div>
  )
}

function AdminCardThemeSelector({ value, onChange }: { value: ServerCardTheme; onChange: (value: ServerCardTheme) => void }) {
  return (
    <div className="admin-appearance-preset-field admin-card-theme-field">
      <span>服务器卡片</span>
      <input type="hidden" name="server-card-theme" value={value} />
      <SlidingSelector
        ariaLabel="服务器卡片主题"
        role="group"
        className="admin-appearance-preset-slider sliding-selector--large"
        options={serverCardThemeOptions.map((option) => ({
          value: option.value,
          content: <span className="admin-appearance-preset-option"><strong>{option.label}</strong></span>,
        }))}
        value={value}
        onChange={(nextValue) => onChange(nextValue === 'capsule' ? 'capsule' : 'classic')}
      />
    </div>
  )
}

function AdminAppearancePresetSlider({ value, onChange }: { value: AppearancePreset; onChange: (value: string) => void }) {
  return (
    <div className="admin-appearance-preset-field">
      <span>主题样式</span>
      <input type="hidden" name="appearance-preset" value={value} />
      <SlidingSelector
        ariaLabel="外观模板"
        role="group"
        className="admin-appearance-preset-slider sliding-selector--large"
        options={appearancePresetOptions.map((option) => {
          return {
            value: option.value,
            content: (
              <span className="admin-appearance-preset-option">
                <strong>{option.label}</strong>
              </span>
            ),
          }
        })}
        value={value}
        onChange={onChange}
      />
    </div>
  )
}

function AdminAppearancePreview({ appearance, draft, settings, theme }: { appearance: AppearanceValues; draft: AdminSettingsDraft; settings: AdminSettings; theme: AdminSettings['theme'] }) {
  const previewShellStyle = shellStyleForSettings({
    ...settings,
    ...appearance,
    siteTitle: draft.siteTitle,
    theme,
    backgroundUrl: 'preview',
    desktopBackgroundUrl: 'preview',
    mobileBackgroundUrl: '',
  }) as Record<string, string | number> | undefined
  const isDarkPreview = resolvedTheme(theme) === 'dark'
  const previewWallpaper = (draft.desktopBackgroundUrl || draft.mobileBackgroundUrl || '').trim()
  const rawPreviewBorder = String(previewShellStyle?.['--border'] ?? appearance.themeColor)
  const rawPreviewOverlay = String(previewShellStyle?.['--zeno-background-overlay-color'] ?? 'transparent')
  const previewStyle = {
    '--appearance-preview-color': String(previewShellStyle?.['--blue'] ?? appearance.themeColor),
    '--appearance-preview-radius': String(previewShellStyle?.['--radius-card'] ?? `${Math.max(10, appearance.cardRadius - 4)}px`),
    '--appearance-preview-surface': String(previewShellStyle?.['--page-surface'] ?? 'rgba(255, 255, 255, 0.7)'),
    '--appearance-preview-border': isDarkPreview && appearance.borderStrength > 0 ? `color-mix(in srgb, ${rawPreviewBorder} 36%, rgba(203, 213, 225, 0.52))` : rawPreviewBorder,
    '--appearance-preview-shadow': String(previewShellStyle?.['--zeno-card-shadow'] ?? 'none'),
    '--appearance-preview-filter': String(previewShellStyle?.['--zeno-card-filter'] ?? (appearance.cardBlur > 0 ? `blur(${appearance.cardBlur}px) saturate(1.06)` : 'none')),
    '--appearance-preview-overlay': isDarkPreview ? `color-mix(in srgb, ${rawPreviewOverlay} 65%, rgba(4, 10, 20, 0.72))` : rawPreviewOverlay,
    '--appearance-preview-foreground': String(previewShellStyle?.['--foreground'] ?? 'var(--foreground)'),
    '--appearance-preview-muted': String(previewShellStyle?.['--muted'] ?? 'var(--muted)'),
    '--appearance-preview-image': previewWallpaper === '' ? "url('/assets/appearance-preview.webp')" : `url("${previewWallpaper.replaceAll('"', '%22')}")`,
  } as CSSProperties
  return (
    <div className={`admin-appearance-preview${isDarkPreview ? ' is-dark' : ''}`} style={previewStyle} aria-hidden="true">
      <div className="admin-appearance-preview__bar">
        <span className="admin-appearance-preview__logo" />
        <strong>{draft.siteTitle.trim() || 'Zeno'}</strong>
        <span className="admin-appearance-preview__dots"><i /><i /></span>
      </div>
      <div className="admin-appearance-preview__card">
        <div className="admin-appearance-preview__head">
          <span className="admin-appearance-preview__flag" />
          <strong>预览节点</strong>
          <em>在线 33 天</em>
        </div>
        <div className="admin-appearance-preview__meters">
          {[['CPU', 38, 'is-good'], ['内存', 72, 'is-warning'], ['存储', 19, 'is-good']].map(([label, value, tone]) => (
            <div key={label} className="admin-appearance-preview__meter">
              <span>{label}</span>
              <i><b className={String(tone)} style={{ width: `${value}%` }} /></i>
              <em>{value}%</em>
            </div>
          ))}
        </div>
        <div className="admin-appearance-preview__rates">
          <span><i className="is-up" />上传 14.68 KB/s</span>
          <span><i className="is-down" />下载 16.20 KB/s</span>
        </div>
      </div>
      <span className="admin-appearance-preview__caption">实时预览 · {Math.round(appearance.cardOpacity * 100)}% · {appearance.cardBlur}px · 圆角 {appearance.cardRadius}px</span>
    </div>
  )
}

function AdminStyleRangeField({ name, label, value, min, max, step, onChange, formatValue }: { name: string; label: string; value: number; min: number; max: number; step: number; onChange: (value: number) => void; formatValue: (value: number) => string }) {
  return (
    <label className="admin-style-range">
      <span className="admin-style-range__head"><span>{label}</span><strong>{formatValue(value)}</strong></span>
      <input name={name} type="range" min={min} max={max} step={step} value={value} onChange={(event) => onChange(Number.parseFloat(event.currentTarget.value))} />
    </label>
  )
}
