// Small shared stroke icons used across public pages.

export function BackIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="m15 18-6-6 6-6" />
    </svg>
  )
}

export function PacketLossIcon() {
  return (
    <svg className="packet-loss-icon" viewBox="0 0 24 24" aria-hidden="true">
      <path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3" />
      <path d="M12 9v4" />
      <path d="M12 17h.01" />
    </svg>
  )
}

/** Same thresholds as the home-card packet-loss strip: ≥1% warning, ≥5% danger. */
export function packetLossTone(value: number | null | undefined): 'good' | 'warning' | 'danger' | 'empty' {
  if (value === null || value === undefined || !Number.isFinite(value)) return 'empty'
  if (value >= 5) return 'danger'
  if (value >= 1) return 'warning'
  return 'good'
}
