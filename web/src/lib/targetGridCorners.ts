// Which outer corners of a bordered tile grid a tile touches, so only those
// tiles round their corners to match the grid frame.
export function latencyTargetCorners(index: number, total: number, columns: number): string {
  if (total <= 0) return ''
  const firstRowIsFull = total >= columns
  const lastRowStart = Math.floor((total - 1) / columns) * columns
  const lastRowIsFull = total % columns === 0
  const corners: string[] = []
  if (index === 0) corners.push('top-left')
  if (firstRowIsFull && index === columns - 1) corners.push('top-right')
  if (index === lastRowStart) corners.push('bottom-left')
  if (lastRowIsFull && index === total - 1) corners.push('bottom-right')
  return corners.join(' ')
}
