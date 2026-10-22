// Latency series colours. Every index, starting with the first, walks the hue
// circle by the golden angle (≈137.508°) so colours never repeat, alternating
// between two bright saturation/lightness tones so neighbours also differ in
// tone. Colours are never darkened.
const goldenAngle = 137.50776405003785
const startHue = 142
const brightTones = [
  { saturation: 0.74, lightness: 0.56 },
  { saturation: 0.88, lightness: 0.66 },
] as const

function channelHex(value: number): string {
  return Math.round(Math.max(0, Math.min(1, value)) * 255).toString(16).padStart(2, '0')
}

function hslToHex(hue: number, saturation: number, lightness: number): string {
  const chroma = (1 - Math.abs(2 * lightness - 1)) * saturation
  const segment = hue / 60
  const second = chroma * (1 - Math.abs((segment % 2) - 1))
  const [r, g, b] = segment < 1 ? [chroma, second, 0]
    : segment < 2 ? [second, chroma, 0]
      : segment < 3 ? [0, chroma, second]
        : segment < 4 ? [0, second, chroma]
          : segment < 5 ? [second, 0, chroma]
            : [chroma, 0, second]
  const match = lightness - chroma / 2
  return `#${channelHex(r + match)}${channelHex(g + match)}${channelHex(b + match)}`
}

export function latencySeriesColor(index: number): string {
  const step = Number.isFinite(index) && index > 0 ? Math.trunc(index) : 0
  const hue = (startHue + step * goldenAngle) % 360
  const tone = brightTones[step % brightTones.length]
  return hslToHex(hue, tone.saturation, tone.lightness)
}

/**
 * Returns a colour lookup keyed by id, following a stable global order (the
 * admin display order of targets or nodes). Ids missing from the global order
 * are appended after it in first-seen order so that they still get unique colours.
 */
export function seriesColorMap(globalOrder: readonly string[], ids: readonly string[] = []): Map<string, string> {
  const colors = new Map<string, string>()
  let next = 0
  for (const id of [...globalOrder, ...ids]) {
    if (colors.has(id)) continue
    colors.set(id, latencySeriesColor(next))
    next += 1
  }
  return colors
}
