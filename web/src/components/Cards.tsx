// A card as a coloured tile, as a row in a list, its barcode, and the
// full-screen view for the till or the gate.
import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import type { WalletCard } from '../lib/types'
import { cardColor, cardLine, codeURL, expiryState, expiryText, imageURL, initials, inkOn, keepAwake, kindIcon, maskNumber, whenText } from '../lib/wallet'
import { useData } from '../lib/data'
import { Icon } from './icons'
import { copyText, useToast } from './ui'

/** The card's face: its colour, its logo or initials. */
export function CardFace({ c, size = 40 }: { c: WalletCard; size?: number }) {
  const bg = cardColor(c)
  const logo = imageURL(c, 'logo')
  return (
    <span className="card-face" style={{ width: size, height: size, background: bg, color: inkOn(bg), fontSize: size * 0.36 }} aria-hidden="true">
      {logo ? <img src={logo} alt="" loading="lazy" /> : c.group === 'cards' ? initials(c.issuer || c.name) : <Icon name={kindIcon(c.kind)} size={size * 0.5} />}
    </span>
  )
}

/** A card in the grid, like in a wallet: tap to show its code. */
export function CardTile({ c, onShow }: { c: WalletCard; onShow?: (c: WalletCard) => void }) {
  const bg = cardColor(c)
  const ink = inkOn(bg)
  const logo = imageURL(c, 'logo')
  const exp = expiryState(c.expires_on, 30)
  return (
    <div className="ctile" style={{ background: bg, color: ink }}>
      <button type="button" className="ctile-main" onClick={() => onShow?.(c)} aria-label={`Show ${c.name}`}>
        <span className="ctile-top">
          {logo ? <img src={logo} alt="" className="ctile-logo" loading="lazy" /> : <span className="ctile-mono">{initials(c.issuer || c.name)}</span>}
          {c.pinned && <Icon name="star" size={13} className="ctile-pin" />}
        </span>
        <span className="ctile-name">{c.name}</span>
        <span className="ctile-sub">{cardLine(c) || (c.code_format ? 'Tap to show the code' : c.number || '')}</span>
        {(exp === 'due' || exp === 'gone') && <span className="ctile-warn"><Icon name="alert" size={12} />{expiryText(c.expires_on)}</span>}
      </button>
      <Link to={`/cards/${c.id}`} className="ctile-more" aria-label={`Open ${c.name}`} title="Details"><Icon name="more" size={16} /></Link>
    </div>
  )
}

/** A pass: the day block, where and when, seat. */
export function PassRow({ c }: { c: WalletCard }) {
  const day = c.starts_at.slice(0, 10)
  const d = day ? new Date(day + 'T00:00:00') : null
  return (
    <Link to={`/cards/${c.id}`} className={`list-row pass-row ${c.archived ? 'is-archived' : ''}`}>
      <span className="date-block" style={{ ['--c' as string]: cardColor(c) }}>
        {d ? <><b>{d.getDate()}</b><span>{d.toLocaleDateString(undefined, { month: 'short' })}</span></> : <Icon name={kindIcon(c.kind)} size={18} />}
      </span>
      <span className="row-main grow">
        <span className="row-title"><Icon name={kindIcon(c.kind)} size={14} className="muted" /><span className="ellipsis">{c.name}</span></span>
        <span className="muted small ellipsis">{[whenText(c.starts_at), c.kind === 'boarding' ? c.ref : c.venue, c.seat && `seat ${c.seat}`, c.gate && `gate ${c.gate}`].filter(Boolean).join(' · ')}</span>
      </span>
      {c.trip_name && <span className="list-tag hide-xs"><Icon name="plane" size={12} />{c.trip_name}</span>}
      {c.code_format && <Icon name={c.code_format === 'qr' || c.code_format === 'aztec' || c.code_format === 'datamatrix' ? 'qr' : 'barcode'} size={16} className="muted" />}
    </Link>
  )
}

/** An ID paper: whose, the number (hidden until asked), when it runs out. */
export function IdRow({ c, hide }: { c: WalletCard; hide: boolean }) {
  return (
    <Link to={`/cards/${c.id}`} className={`list-row ${c.archived ? 'is-archived' : ''}`}>
      <CardFace c={c} size={36} />
      <span className="row-main grow">
        <span className="row-title"><span className="ellipsis">{c.name}</span>{c.holder && <span className="muted small ellipsis">{c.holder}</span>}</span>
        <span className="muted small ellipsis mono">{c.number ? (hide ? maskNumber(c.number) : c.number) : c.issuer}</span>
      </span>
      {c.expires_on && <ExpiryPill day={c.expires_on} />}
    </Link>
  )
}

export function ExpiryPill({ day, soon = 60 }: { day: string; soon?: number }) {
  const st = expiryState(day, soon)
  const icon = st === 'gone' || st === 'due' ? 'alert' : st === 'soon' ? 'clock' : 'check'
  return <span className={`pill st-${st === 'gone' ? 'overdue' : st}`}><Icon name={icon} size={12} />{expiryText(day)}</span>
}

/** The barcode on white, with the number under it. */
export function CodeView({ c, big }: { c: WalletCard; big?: boolean }) {
  const { formats } = useData()
  const twoD = formats.find(f => f.key === c.code_format)?.two_d ?? ['qr', 'aztec', 'datamatrix', 'pdf417'].includes(c.code_format)
  const wide = c.code_format === 'pdf417'
  const shown = c.number || (c.code_value.length <= 40 ? c.code_value : '')
  return (
    <div className={`code-view ${twoD && !wide ? 'square' : 'wide'} ${big ? 'big' : ''}`}>
      <img src={codeURL(c)} alt={`Barcode for ${c.name}`} draggable={false} />
      {shown && <div className="code-text mono">{shown}</div>}
    </div>
  )
}

/** Full screen, white, for the scanner: the code as large as it fits. */
export function ShowCard({ c, onClose }: { c: WalletCard; onClose: () => void }) {
  const { toast } = useToast()
  const [side, setSide] = useState<'code' | 'front' | 'back'>(c.code_format ? 'code' : c.front_image ? 'front' : 'code')
  useEffect(() => {
    api.post(`${V1}/cards/${c.id}/used`).catch(() => {})
    let release = () => {}
    keepAwake().then(r => { release = r })
    const h = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', h)
    document.body.classList.add('showing-card')
    return () => { release(); window.removeEventListener('keydown', h); document.body.classList.remove('showing-card') }
  }, [c.id, onClose])
  const bg = cardColor(c)
  const pics = [c.code_format && 'code', c.front_image && 'front', c.back_image && 'back'].filter(Boolean) as ('code' | 'front' | 'back')[]
  return createPortal(
    <div className="show" role="dialog" aria-modal="true" aria-label={c.name}>
      <header className="show-head" style={{ background: bg, color: inkOn(bg) }}>
        <CardFace c={c} size={34} />
        <div className="grow">
          <b className="ellipsis">{c.name}</b>
          <span className="small ellipsis">{c.group === 'passes' ? [whenText(c.starts_at), c.seat && `seat ${c.seat}`, c.gate && `gate ${c.gate}`].filter(Boolean).join(' · ') : c.issuer !== c.name ? c.issuer : ''}</span>
        </div>
        <button type="button" className="show-close" onClick={onClose} aria-label="Close"><Icon name="x" size={22} /></button>
      </header>
      <div className="show-body" onClick={onClose}>
        {side === 'code' && c.code_format && <CodeView c={c} big />}
        {side === 'code' && !c.code_format && <div className="show-number mono">{c.number || 'No barcode or number on this card'}</div>}
        {side !== 'code' && <img className="show-photo" src={imageURL(c, side)} alt={`${c.name}, ${side}`} />}
        {c.holder && side === 'code' && <div className="show-holder">{c.holder}</div>}
        <p className="show-hint">Turn the screen brightness up. Tap anywhere to close.</p>
      </div>
      <footer className="show-foot">
        {pics.length > 1 && pics.map(p => <button key={p} type="button" className={`chip ${side === p ? 'on' : ''}`} onClick={() => setSide(p)}>{p === 'code' ? 'Code' : p === 'front' ? 'Front' : 'Back'}</button>)}
        {c.number && <button type="button" className="chip" onClick={async () => toast(await copyText(c.number) ? 'Number copied' : 'Could not copy', 'ok')}><Icon name="copy" size={13} />Copy number</button>}
      </footer>
    </div>,
    document.body,
  )
}
