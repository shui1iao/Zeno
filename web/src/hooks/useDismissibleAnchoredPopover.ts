import { useEffect, useRef } from 'react'

interface AnchoredPopoverStack {
  push: (id: number, dismiss: () => void) => void
  remove: (id: number) => void
  isTopmost: (id: number) => boolean
}

export function createAnchoredPopoverStack(): AnchoredPopoverStack {
  const entries: Array<{ id: number; dismiss: () => void }> = []
  return {
    push: (id, dismiss) => {
      const existingIndex = entries.findIndex((entry) => entry.id === id)
      if (existingIndex >= 0) entries.splice(existingIndex, 1)
      const previous = entries.pop()
      previous?.dismiss()
      entries.push({ id, dismiss })
    },
    remove: (id) => {
      const index = entries.findIndex((entry) => entry.id === id)
      if (index >= 0) entries.splice(index, 1)
    },
    isTopmost: (id) => entries.at(-1)?.id === id,
  }
}

export function consumeAnchoredPopoverEscape(event: Pick<KeyboardEvent, 'key' | 'preventDefault' | 'stopPropagation' | 'stopImmediatePropagation'>): boolean {
  if (event.key !== 'Escape') return false
  event.preventDefault()
  event.stopPropagation()
  event.stopImmediatePropagation()
  return true
}

let nextAnchoredPopoverId = 1
const anchoredPopoverStack = createAnchoredPopoverStack()

export function useDismissibleAnchoredPopover<TTrigger extends HTMLElement, TPopover extends HTMLElement>(
  open: boolean,
  triggerRef: React.RefObject<TTrigger | null>,
  popoverRef: React.RefObject<TPopover | null>,
  onDismiss: () => void,
) {
  const idRef = useRef<number | null>(null)
  const dismissRef = useRef(onDismiss)
  dismissRef.current = onDismiss
  if (idRef.current === null) {
    idRef.current = nextAnchoredPopoverId
    nextAnchoredPopoverId += 1
  }

  useEffect(() => {
    if (!open || typeof window === 'undefined') return undefined
    const id = idRef.current as number
    anchoredPopoverStack.push(id, () => dismissRef.current())

    const handlePointerDown = (event: PointerEvent) => {
      if (!anchoredPopoverStack.isTopmost(id)) return
      const target = event.target as Node | null
      if (target && (triggerRef.current?.contains(target) || popoverRef.current?.contains(target))) return
      dismissRef.current()
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (!anchoredPopoverStack.isTopmost(id) || !consumeAnchoredPopoverEscape(event)) return
      dismissRef.current()
      window.requestAnimationFrame(() => triggerRef.current?.focus({ preventScroll: true }))
    }

    // Capture on window so Escape is consumed before a parent modal can close.
    window.addEventListener('pointerdown', handlePointerDown, true)
    window.addEventListener('keydown', handleKeyDown, true)
    return () => {
      anchoredPopoverStack.remove(id)
      window.removeEventListener('pointerdown', handlePointerDown, true)
      window.removeEventListener('keydown', handleKeyDown, true)
    }
  }, [open, triggerRef, popoverRef])
}
