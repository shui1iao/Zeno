import { type RefObject, useEffect } from 'react'
import { focusAfterTrigger, focusInPopover, focusWithoutScroll, overlayTabStops } from '../lib/overlayFocus'
import { useDismissibleAnchoredPopover } from './useDismissibleAnchoredPopover'
import { useOverlayOwnerActive, useOverlayPresence } from './useOverlayPresence'

/** Focus is separate from selection: opening never selects a value or focuses an input. */
export function useAnchoredPopover(open: boolean, triggerRef: RefObject<HTMLButtonElement | null>, popoverRef: RefObject<HTMLDivElement | null>, dismiss: () => void, mode: 'listbox' | 'calendar') {
  const ownerActive = useOverlayOwnerActive()
  const active = open && ownerActive
  const presence = useOverlayPresence(active)
  useDismissibleAnchoredPopover(active, triggerRef, popoverRef, dismiss)
  useEffect(() => {
    if (!active) return
    const root = popoverRef.current
    if (!root) return
    const frame = requestAnimationFrame(() => focusInPopover(root, root.querySelector<HTMLElement>('[aria-selected="true"], .admin-date-grid .is-selected') ?? root.querySelector<HTMLElement>('.admin-date-grid .is-today') ?? root.querySelector<HTMLElement>('.admin-date-grid button') ?? root.querySelector<HTMLElement>('button:not([disabled])') ?? root))
    const keys = (event: KeyboardEvent) => {
      if (event.key === 'Tab') {
        const stops = overlayTabStops(root)
        const current = document.activeElement
        if (mode === 'listbox' || (!event.shiftKey && current === stops.at(-1)) || (event.shiftKey && (current === stops[0] || current === root))) {
          event.preventDefault()
          event.stopImmediatePropagation()
          dismiss()
          focusAfterTrigger(triggerRef.current, event.shiftKey)
        } else {
          // A portaled child must not be mistaken for an out-of-modal Tab.
          event.stopPropagation()
        }
      }
      if (mode !== 'listbox') return
      const options = Array.from(root.querySelectorAll<HTMLButtonElement>('[role="option"]:not([disabled])'))
      const index = Math.max(0, options.indexOf(document.activeElement as HTMLButtonElement))
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1 : ['ArrowDown', 'ArrowRight'].includes(event.key) ? (index + 1) % options.length : ['ArrowUp', 'ArrowLeft'].includes(event.key) ? (index - 1 + options.length) % options.length : null
      if (next !== null) { event.preventDefault(); event.stopPropagation(); focusInPopover(root, options[next]) }
    }
    root.addEventListener('keydown', keys)
    return () => {
      cancelAnimationFrame(frame)
      root.removeEventListener('keydown', keys)
      if (root.contains(document.activeElement) || document.activeElement === document.body) focusWithoutScroll(triggerRef.current)
    }
  }, [active, triggerRef, popoverRef, mode])
  return { ...presence, active }
}
