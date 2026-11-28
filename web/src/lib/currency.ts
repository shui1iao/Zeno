import { isPositiveFinite } from './number'

export const homeCurrencyStorageKey = 'zeno_home_currency'

export const currencyOptions = [
  { value: 'CNY', label: '人民币 CNY', shortLabel: 'CNY', flagCode: 'CN' },
  { value: 'USD', label: '美元 USD', shortLabel: 'USD', flagCode: 'US' },
  { value: 'HKD', label: '港币 HKD', shortLabel: 'HKD', flagCode: 'HK' },
  { value: 'EUR', label: '欧元 EUR', shortLabel: 'EUR', flagCode: 'EU' },
  { value: 'GBP', label: '英镑 GBP', shortLabel: 'GBP', flagCode: 'GB' },
  { value: 'JPY', label: '日元 JPY', shortLabel: 'JPY', flagCode: 'JP' },
  { value: 'SGD', label: '新加坡元 SGD', shortLabel: 'SGD', flagCode: 'SG' },
  { value: 'AUD', label: '澳元 AUD', shortLabel: 'AUD', flagCode: 'AU' },
  { value: 'CAD', label: '加元 CAD', shortLabel: 'CAD', flagCode: 'CA' },
  { value: 'KRW', label: '韩元 KRW', shortLabel: 'KRW', flagCode: 'KR' },
] as const

export type CurrencyCode = typeof currencyOptions[number]['value']
export type CurrencyRates = Record<string, number>

const currencyCodes = new Set<string>(currencyOptions.map((option) => option.value))
const hiddenDisplayCurrencyCodes = new Set<CurrencyCode>(['SGD', 'KRW'])

const currencySymbols: Record<CurrencyCode, string> = {
  CNY: '¥',
  USD: '$',
  HKD: 'HK$',
  EUR: '€',
  GBP: '£',
  JPY: 'JP¥',
  SGD: 'S$',
  AUD: 'A$',
  CAD: 'C$',
  KRW: '₩',
}

export function normalizeCurrencyCode(value: string | null | undefined): CurrencyCode {
  const normalized = (value ?? '').trim().toUpperCase()
  return currencyCodes.has(normalized) ? normalized as CurrencyCode : 'CNY'
}

export function normalizeCurrencyRates(value: unknown): CurrencyRates {
  const rates: CurrencyRates = { CNY: 1 }
  if (!value || typeof value !== 'object') return rates
  Object.entries(value as Record<string, unknown>).forEach(([rawCurrency, rawRate]) => {
    const currency = rawCurrency.trim().toUpperCase()
    if (!currencyCodes.has(currency) || typeof rawRate !== 'number' || !Number.isFinite(rawRate) || rawRate <= 0) return
    rates[currency] = rawRate
  })
  rates.CNY = 1
  return rates
}

export function availableCurrencyOptions(rates: CurrencyRates) {
  return currencyOptions.filter((option) => !hiddenDisplayCurrencyCodes.has(option.value) && (option.value === 'CNY' || isPositiveFinite(rates[option.value])))
}

export function storedHomeCurrency(storage: Pick<Storage, 'getItem'> | null | undefined = typeof window === 'undefined' ? null : window.localStorage): CurrencyCode {
  try {
    return normalizeCurrencyCode(storage?.getItem(homeCurrencyStorageKey))
  } catch {
    return 'CNY'
  }
}

export function rememberHomeCurrency(currency: CurrencyCode, storage: Pick<Storage, 'setItem'> | null | undefined = typeof window === 'undefined' ? null : window.localStorage) {
  try {
    storage?.setItem(homeCurrencyStorageKey, currency)
  } catch {
    // The selector still works for the current page when browser storage is unavailable.
  }
}

export function convertCurrencyAmount(amount: number | null | undefined, source: string | null | undefined, target: CurrencyCode, rates: CurrencyRates): number | null {
  if (amount === null || amount === undefined || !Number.isFinite(amount) || amount < 0) return null
  const sourceCurrency = normalizeCurrencyCode(source)
  const sourceRate = rates[sourceCurrency]
  const targetRate = rates[target]
  if (!isPositiveFinite(sourceRate) || !isPositiveFinite(targetRate)) return null
  return amount * sourceRate / targetRate
}

export function billingCycleMonths(value: string | null | undefined): number {
  const cycle = (value ?? '').trim()
  if (cycle === '') return 0
  if (cycle.includes('五年') || cycle.includes('5年') || cycle.includes('5 年')) return 60
  if (cycle.includes('三年') || cycle.includes('3年') || cycle.includes('3 年')) return 36
  if (cycle.includes('两年') || cycle.includes('二年') || cycle.includes('2年') || cycle.includes('2 年')) return 24
  if (cycle.includes('半年') || cycle.includes('半 年')) return 6
  if (cycle.includes('季')) return 3
  if (cycle.includes('年')) return 12
  if (cycle.includes('月')) return 1
  return 0
}

export function formatCurrencyAmount(value: number | null | undefined, currency: CurrencyCode, options: { fixed?: boolean; spaced?: boolean } = {}): string {
  const normalized = value !== null && value !== undefined && Number.isFinite(value) && value > 0 ? value : 0
  const fixed = normalized.toFixed(2)
  const amount = options.fixed ? fixed : fixed.replace(/\.00$/, '').replace(/(\.\d)0$/, '$1')
  return `${currencySymbols[currency]}${options.spaced ? ' ' : ''}${amount}`
}

/** Renewal price with its billing cycle, e.g. "¥ 30 / 月付"; the cycle part is dropped when blank. */
export function formatCycleAmount(amount: number, currency: CurrencyCode, cycle: string | null | undefined): string {
  const cycleText = (cycle ?? '').trim()
  return `${formatCurrencyAmount(amount, currency, { spaced: true })}${cycleText ? ` / ${cycleText}` : ''}`
}
