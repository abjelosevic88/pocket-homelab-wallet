// What every page needs to show a card: its colour, the words under it,
// when a pass is, how long until something runs out, the barcode URL.
import { V1 } from './api'
import { addDays, fmtClock, fmtDate, todayISO, weekday } from './format'
import type { Kind, WalletCard } from './types'

export const KIND_ICON: Record<string, string> = { loyalty: 'tag', membership: 'users', gift: 'gift', ticket: 'ticket', boarding: 'plane', id: 'id-card', passport: 'passport', licence: 'car', insurance: 'shield', other: 'card' }
export const kindIcon = (k: string) => KIND_ICON[k] || 'card'
export const kindLabel = (kinds: Kind[], k: string) => kinds.find(x => x.key === k)?.label || k

/** A tile colour: the card's own, else one picked from its name. */
const PALETTE = ['#0b7a68', '#1d4ed8', '#7c3aed', '#be185d', '#b45309', '#0e7490', '#4d7c0f', '#9f1239', '#334155', '#c2410c']
export function cardColor(c: Pick<WalletCard, 'color' | 'name' | 'issuer'>): string {
  if (c.color) return c.color
  const s = (c.issuer || c.name || '?').toLowerCase()
  let h = 0
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0
  return PALETTE[h % PALETTE.length]
}

/** Black or white text, whichever reads better on a colour. */
export function inkOn(hex: string): string {
  const m = /^#?([0-9a-f]{6})$/i.exec(hex)
  if (!m) return '#fff'
  const n = parseInt(m[1], 16)
  const lin = (v: number) => { const c = v / 255; return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4 }
  const L = 0.2126 * lin(n >> 16) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255)
  return L > 0.45 ? '#16181d' : '#ffffff'
}

export function initials(name: string): string {
  const w = name.replace(/[^\p{L}\p{N} ]/gu, ' ').split(/\s+/).filter(Boolean)
  return ((w[0]?.[0] || '?') + (w.length > 1 ? w[1][0] : w[0]?.[1] || '')).toUpperCase()
}

export const codeURL = (c: WalletCard) => `${V1}/cards/${c.id}/code.svg?v=${encodeURIComponent(c.updated_at)}`
export const imageURL = (c: WalletCard, side: 'front' | 'back' | 'logo') => {
  const n = side === 'front' ? c.front_image : side === 'back' ? c.back_image : c.logo_image
  return n ? `${V1}/cards/${c.id}/images/${side}?n=${encodeURIComponent(n)}` : ''
}

/** Money in minor units: 6450 BAM → "64.50 KM". */
export function money(minor: number, cur: string): string {
  try { return new Intl.NumberFormat(undefined, { style: 'currency', currency: cur || 'EUR' }).format(minor / 100) } catch { return `${(minor / 100).toFixed(2)} ${cur}` }
}

/** "Today", "Tomorrow", "Sat 12 Dec" (+ the time). */
export function whenText(startsAt: string, withTime = true): string {
  if (!startsAt) return ''
  const day = startsAt.slice(0, 10)
  const t = startsAt.length > 10 ? startsAt.slice(11, 16) : ''
  const today = todayISO()
  let d: string
  if (day === today) d = 'Today'
  else if (day === addDays(today, 1)) d = 'Tomorrow'
  else if (day === addDays(today, -1)) d = 'Yesterday'
  else d = `${weekday(day)} ${fmtDate(day, day.slice(0, 4) !== today.slice(0, 4))}`
  return withTime && t ? `${d}, ${fmtClock(t)}` : d
}

export function daysLeft(day: string): number {
  if (!day) return Infinity
  const a = new Date(todayISO() + 'T00:00:00'), b = new Date(day + 'T00:00:00')
  return Math.round((b.getTime() - a.getTime()) / 86400000)
}

/** "runs out in 46 days", "ran out 3 days ago". */
export function expiryText(day: string): string {
  const n = daysLeft(day)
  if (n === Infinity) return ''
  if (n < -1) return `ran out ${-n} days ago`
  if (n === -1) return 'ran out yesterday'
  if (n === 0) return 'runs out today'
  if (n === 1) return 'runs out tomorrow'
  if (n < 60) return `runs out in ${n} days`
  return `valid until ${fmtDate(day)}`
}

/** soon | due | ok, for the colour of an end date (with words, never alone). */
export function expiryState(day: string, soonDays = 60): 'gone' | 'due' | 'soon' | 'ok' | '' {
  const n = daysLeft(day)
  if (n === Infinity) return ''
  if (n < 0) return 'gone'
  if (n <= 7) return 'due'
  if (n <= soonDays) return 'soon'
  return 'ok'
}

/** The line under a card's name. */
export function cardLine(c: WalletCard): string {
  const bits: string[] = []
  if (c.group === 'passes') {
    if (c.starts_at) bits.push(whenText(c.starts_at))
    if (c.kind === 'boarding' && c.ref) bits.push(c.ref)
    else if (c.venue && !c.name.includes(c.venue)) bits.push(c.venue)
    if (c.seat) bits.push(`seat ${c.seat}`)
    return bits.join(' · ')
  }
  if (c.balance != null && c.kind === 'gift') bits.push(money(c.balance, c.currency))
  if (c.points != null) bits.push(`${c.points.toLocaleString()} points`)
  if (c.holder && c.group === 'ids') bits.push(c.holder)
  if (c.expires_on) bits.push(expiryText(c.expires_on))
  if (!bits.length && c.issuer && c.issuer !== c.name) bits.push(c.issuer)
  return bits.join(' · ')
}

export function maskNumber(n: string): string {
  if (n.length <= 4) return '••••'
  return '•••• ' + n.slice(-3)
}

/** A photo made small enough to send (JPEG, longest side ≤ max). */
export async function shrinkImage(file: Blob, max = 1600, quality = 0.86): Promise<Blob> {
  const url = URL.createObjectURL(file)
  try {
    const img = await new Promise<HTMLImageElement>((ok, bad) => { const i = new Image(); i.onload = () => ok(i); i.onerror = () => bad(new Error('That photo cannot be opened')); i.src = url })
    const scale = Math.min(1, max / Math.max(img.naturalWidth, img.naturalHeight))
    const w = Math.round(img.naturalWidth * scale), h = Math.round(img.naturalHeight * scale)
    const cv = document.createElement('canvas')
    cv.width = w; cv.height = h
    cv.getContext('2d')!.drawImage(img, 0, 0, w, h)
    return await new Promise<Blob>((ok, bad) => cv.toBlob(b => b ? ok(b) : bad(new Error('That photo cannot be read')), 'image/jpeg', quality))
  } finally { URL.revokeObjectURL(url) }
}

// ---- the browser's own barcode reader, where there is one (Android, desktop Chrome) ----

const DETECTOR_FORMATS: Record<string, string> = { qr_code: 'qr', aztec: 'aztec', pdf417: 'pdf417', data_matrix: 'datamatrix', code_128: 'code128', ean_13: 'ean13', ean_8: 'ean8', upc_a: 'upca', upc_e: 'upce', code_39: 'code39', code_93: 'code93', itf: 'itf', codabar: 'codabar' }

interface Detector { detect: (src: CanvasImageSource) => Promise<{ rawValue: string; format: string }[]> }
let detector: Detector | null | undefined

export async function browserDetector(): Promise<Detector | null> {
  if (detector !== undefined) return detector
  const BD = (window as unknown as { BarcodeDetector?: { new(o: { formats: string[] }): Detector; getSupportedFormats: () => Promise<string[]> } }).BarcodeDetector
  if (!BD) return (detector = null)
  try {
    const have = await BD.getSupportedFormats()
    const formats = Object.keys(DETECTOR_FORMATS).filter(f => have.includes(f))
    detector = formats.length ? new BD({ formats }) : null
  } catch { detector = null }
  return detector
}

export async function detectIn(src: CanvasImageSource): Promise<{ format: string; value: string } | null> {
  const d = await browserDetector()
  if (!d) return null
  try {
    const found = await d.detect(src)
    const f = found.find(x => DETECTOR_FORMATS[x.format] && x.rawValue)
    return f ? { format: DETECTOR_FORMATS[f.format], value: f.rawValue } : null
  } catch { return null }
}

/** Keep the screen on while a code is shown (where the browser can). */
export async function keepAwake(): Promise<() => void> {
  try {
    const wl = await (navigator as unknown as { wakeLock?: { request: (t: string) => Promise<{ release: () => Promise<void> }> } }).wakeLock?.request('screen')
    return () => { wl?.release().catch(() => {}) }
  } catch { return () => {} }
}
