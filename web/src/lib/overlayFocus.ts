export const focusWithoutScroll = (node: HTMLElement | null | undefined) => { if (node?.isConnected) node.focus({ preventScroll: true }) }
export function overlayTabStops(root: HTMLElement): HTMLElement[] {
  return Array.from(root.querySelectorAll<HTMLElement>('a[href],button:not([disabled]),input:not([disabled]):not([type="hidden"]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])'))
    .filter(node => node.tabIndex >= 0 && !node.closest('[inert],[hidden],[aria-hidden="true"]') && node.getClientRects().length > 0)
}

export function focusAfterTrigger(trigger: HTMLButtonElement | null, backwards: boolean) {
  if (!trigger) return
  const modal = trigger.closest<HTMLElement>('.admin-modal')
  const stops = overlayTabStops(modal ?? document.body).filter(node => !node.closest('[data-anchored-overlay]'))
  const index = stops.indexOf(trigger)
  const next = index + (backwards ? -1 : 1)
  focusWithoutScroll(stops[next] ?? (modal ? stops[(next + stops.length) % stops.length] : trigger))
}

/** Scroll only the popup, never the page or the parent edit form. */
export function focusInPopover(root: HTMLElement, node: HTMLElement | null) {
  focusWithoutScroll(node)
  if (!node) return
  const box = root.getBoundingClientRect()
  const item = node.getBoundingClientRect()
  if (item.top < box.top) root.scrollTop -= box.top - item.top
  else if (item.bottom > box.bottom) root.scrollTop += item.bottom - box.bottom
}
