import { type CSSProperties, type RefObject, useLayoutEffect, useState } from 'react'
import { calculateAnchoredPopoverStyle, measureAnchoredPopoverHeight, type RectLike } from '../lib/anchoredPopover'

type Alignment = 'start' | 'center' | 'end'
export function useAnchoredPopoverPosition({ open, triggerRef, popoverRef, width, fallbackHeight, margin = 12, gap = 8, align = 'start', refreshKey = '' }: {
  open: boolean
  triggerRef: RefObject<HTMLButtonElement | null>
  popoverRef: RefObject<HTMLDivElement | null>
  width: (rect: RectLike, viewportWidth: number) => number
  fallbackHeight: number
  margin?: number
  gap?: number
  align?: Alignment | ((viewportWidth: number) => Alignment)
  refreshKey?: string
}) {
  const [position, setPosition] = useState<{ style: CSSProperties; side: 'top' | 'bottom' }>({ style: {}, side: 'bottom' })
  useLayoutEffect(() => {
    if (!open) return
    const update = () => {
      const trigger = triggerRef.current
      const popover = popoverRef.current
      if (!trigger || !popover) return
      const rect = trigger.getBoundingClientRect()
      const viewport = window.visualViewport
      const viewportWidth = Math.min(window.innerWidth, document.documentElement.clientWidth || window.innerWidth, viewport?.width ?? Infinity)
      const viewportHeight = Math.min(window.innerHeight, viewport?.height ?? Infinity)
      const offsetLeft = viewport?.offsetLeft ?? 0
      const offsetTop = viewport?.offsetTop ?? 0
      const localRect = { ...rect.toJSON(), top: rect.top - offsetTop, bottom: rect.bottom - offsetTop, left: rect.left - offsetLeft, right: rect.right - offsetLeft, width: rect.width, height: rect.height }
      const style = calculateAnchoredPopoverStyle(localRect, { width: viewportWidth, height: viewportHeight }, { width: width(rect, viewportWidth), height: measureAnchoredPopoverHeight(popover, fallbackHeight) }, margin, gap, typeof align === 'function' ? align(viewportWidth) : align)
      const side = Number(style.top) < localRect.top ? 'top' : 'bottom'
      style.top = Number(style.top) + offsetTop
      style.left = Number(style.left) + offsetLeft
      style.transformOrigin = side === 'top' ? 'center bottom' : 'center top'
      setPosition({ style, side })
    }
    update()
    const observer = new ResizeObserver(update)
    if (popoverRef.current) observer.observe(popoverRef.current)
    if (triggerRef.current) observer.observe(triggerRef.current)
    window.addEventListener('resize', update)
    window.addEventListener('scroll', update, true)
    window.visualViewport?.addEventListener('resize', update)
    window.visualViewport?.addEventListener('scroll', update)
    return () => {
      observer.disconnect()
      window.removeEventListener('resize', update)
      window.removeEventListener('scroll', update, true)
      window.visualViewport?.removeEventListener('resize', update)
      window.visualViewport?.removeEventListener('scroll', update)
    }
  }, [open, triggerRef, popoverRef, width, fallbackHeight, margin, gap, align, refreshKey])
  return position
}
