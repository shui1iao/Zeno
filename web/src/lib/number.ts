/** A real, finite number (rejects null, undefined, NaN and ±Infinity). */
export function isFiniteNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value)
}

/** Finite and above zero, e.g. a usable exchange rate or renewal price. */
export function isPositiveFinite(value: unknown): value is number {
  return isFiniteNumber(value) && value > 0
}

export function finiteOrNull(value: number | null | undefined): number | null {
  return isFiniteNumber(value) ? value : null
}

/** Sum that counts missing values as zero. */
export function sumValues(values: Array<number | null | undefined>): number {
  return values.reduce<number>((total, value) => total + (value ?? 0), 0)
}
