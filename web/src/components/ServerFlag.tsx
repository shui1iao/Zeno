import { useMemo } from 'react'

interface ServerFlagProps {
  countryCode?: string
  className?: string
}

function normalizeFlagCode(countryCode: string | undefined): string {
  const code = (countryCode ?? '').trim().toUpperCase()
  if (!/^[A-Z]{2}$/.test(code)) return ''
  return code === 'TW' ? 'CN' : code
}

function unicodeFlagIcon(countryCode: string): string {
  const base = 127397
  return countryCode
    .split('')
    .map((char) => String.fromCodePoint(char.charCodeAt(0) + base))
    .join('')
}

function forceUseSvgFlag(): boolean {
  if (typeof window === 'undefined') return false
  return (window as Window & { ForceUseSvgFlag?: boolean }).ForceUseSvgFlag === true
}

let cachedEmojiFlagSupport: boolean | null = null

function detectEmojiFlagSupport(): boolean {
  if (typeof document === 'undefined') return false
  if (cachedEmojiFlagSupport !== null) return cachedEmojiFlagSupport
  try {
    const canvas = document.createElement('canvas')
    const ctx = canvas.getContext('2d')
    if (!ctx) {
      cachedEmojiFlagSupport = false
      return false
    }
    ctx.fillStyle = '#000'
    ctx.textBaseline = 'top'
    ctx.font = '32px Arial'
    ctx.fillText('🇺🇸', 0, 0)
    cachedEmojiFlagSupport = ctx.getImageData(16, 16, 1, 1).data[3] !== 0
  } catch {
    cachedEmojiFlagSupport = false
  }
  return cachedEmojiFlagSupport
}

export function ServerFlag({ countryCode, className = '' }: ServerFlagProps) {
  const flagCode = useMemo(() => normalizeFlagCode(countryCode), [countryCode])
  const shouldForceSvg = forceUseSvgFlag()
  const supportsEmojiFlags = !shouldForceSvg && detectEmojiFlagSupport()

  if (!flagCode) return null

  return (
    <span className={`server-flag ${className}`.trim()} aria-label={`${flagCode} flag`}>
      {!supportsEmojiFlags ? (
        <img className="server-flag__image" src={`/assets/flags/${flagCode.toLowerCase()}.svg`} alt="" />
      ) : (
        unicodeFlagIcon(flagCode)
      )}
    </span>
  )
}
