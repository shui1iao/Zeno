import type { CSSProperties } from 'react'

export type RectLike = Pick<DOMRect, 'top' | 'right' | 'bottom' | 'left' | 'width' | 'height'>
type ViewportSize = { width: number; height: number }
type PopoverSize = { width: number; height: number }

export function calculateAnchoredPopoverStyle(trigger: RectLike, viewport: ViewportSize, popover: PopoverSize, margin = 12, gap = 8, align: 'start' | 'center' | 'end' = 'start'): CSSProperties {
  const viewportWidth = Math.max(0, viewport.width)
  const viewportHeight = Math.max(0, viewport.height)
  const horizontalMargin = Math.min(Math.max(0, margin), viewportWidth / 2)
  const verticalMargin = Math.min(Math.max(0, margin), viewportHeight / 2)
  const width = Math.min(Math.max(0, popover.width), Math.max(0, viewportWidth - horizontalMargin * 2))
  const height = Math.min(Math.max(0, popover.height), Math.max(0, viewportHeight - verticalMargin * 2))
  const maxLeft = Math.max(horizontalMargin, viewportWidth - horizontalMargin - width)
  const preferredLeft = align === 'end' ? trigger.right - width : align === 'center' ? trigger.left + (trigger.width - width) / 2 : trigger.left
  const left = Math.min(Math.max(horizontalMargin, preferredLeft), maxLeft)
  const popoverGap = Math.max(0, gap)
  const belowTop = trigger.bottom + popoverGap
  const aboveTop = trigger.top - height - popoverGap
  const maxTop = Math.max(verticalMargin, viewportHeight - verticalMargin - height)
  const preferredTop = belowTop + height <= viewportHeight - verticalMargin || aboveTop < verticalMargin ? belowTop : aboveTop
  const top = Math.min(Math.max(verticalMargin, preferredTop), maxTop)
  const style: CSSProperties = { position: 'fixed', top, left, width }
  if (height < popover.height) {
    style.maxHeight = height
    style.overflowY = 'auto'
  }
  return style
}

export function measureAnchoredPopoverHeight(popover: HTMLDivElement | null, fallbackHeight: number): number {
  if (!popover) return fallbackHeight
  // Re-measure the visible CSS box, not its scrollable content. Remove only our
  // previous viewport cap so a resize can restore the menu's natural height.
  const previousMaxHeight = popover.style.maxHeight
  popover.style.maxHeight = ''
  try {
    return popover.offsetHeight || fallbackHeight
  } finally {
    popover.style.maxHeight = previousMaxHeight
  }
}
