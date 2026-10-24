import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { calculateAnchoredPopoverStyle } from './anchoredPopover'
import { overlayExitDuration } from '../hooks/useOverlayPresence'
import { AdminModal, AdminModalPresence } from '../components/admin/AdminPrimitives'

const trigger = { top: 510, bottom: 544, left: 340, right: 374, width: 34, height: 34 }
describe('shared popup geometry and presence', () => {
  it('flips a currency-sized panel and keeps its end alignment inside a phone viewport', () => {
    expect(calculateAnchoredPopoverStyle(trigger, { width: 390, height: 568 }, { width: 248, height: 124 }, 8, 7, 'end')).toMatchObject({ left: 126, top: 379, width: 248 })
  })
  it('centers against the trigger without letting a long menu escape a tiny viewport', () => {
    expect(calculateAnchoredPopoverStyle(trigger, { width: 240, height: 160 }, { width: 248, height: 500 }, 8, 7, 'center')).toEqual({ position: 'fixed', top: 8, left: 8, width: 224, maxHeight: 144, overflowY: 'auto' })
  })
  it('retains the 120ms exit except when reduced motion requests immediate removal', () => {
    expect(overlayExitDuration(false)).toBe(120)
    expect(overlayExitDuration(true)).toBe(0)
  })
  it('does not render absent modal content and retains the existing dialog/close contract', () => {
    expect(renderToStaticMarkup(<AdminModalPresence>{false}</AdminModalPresence>)).toBe('')
    const html = renderToStaticMarkup(<AdminModalPresence><AdminModal title="编辑" closeDisabled onClose={() => {}}><input name="unchanged" /></AdminModal></AdminModalPresence>)
    expect(html).toContain('data-overlay-state="open"')
    expect(html).toContain('aria-modal="true"')
    expect(html).toContain('aria-label="关闭弹窗" disabled=""')
    expect(html).toContain('name="unchanged"')
  })
})
