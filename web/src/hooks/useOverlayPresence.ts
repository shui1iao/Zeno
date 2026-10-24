import { createContext, useCallback, useContext, useEffect, useState } from 'react'

export const OverlayOwnerContext = createContext(true)
export const useOverlayOwnerActive = () => useContext(OverlayOwnerContext)
export const overlayExitDuration = (reduced: boolean) => reduced ? 0 : 120

/** Keep the real surface mounted through its exit, including conditional modals. */
export function useOverlayPresence(open: boolean) {
  const [retained, setRetained] = useState(open)
  useEffect(() => {
    if (open) { setRetained(true); return }
    const media = window.matchMedia('(prefers-reduced-motion: reduce)')
    const finish = () => setRetained(false)
    if (overlayExitDuration(media.matches) === 0) { finish(); return }
    // animationend is authoritative; the fallback covers a missing stylesheet or background tab.
    const timer = window.setTimeout(finish, 500)
    const changed = () => { if (media.matches) finish() }
    media.addEventListener('change', changed)
    return () => { window.clearTimeout(timer); media.removeEventListener('change', changed) }
  }, [open])
  const finishExit = useCallback(() => { if (!open) setRetained(false) }, [open])
  return { present: open || retained, state: open ? 'open' as const : 'closed' as const, finishExit }
}
