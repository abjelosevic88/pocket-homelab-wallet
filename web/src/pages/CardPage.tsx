// One card: its code, its photos, what is on it, what it is linked to.
import React, { useEffect, useState } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { api, sendFile, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { announceChange, useData } from '../lib/data'
import type { BalanceEntry, Spent, WalletCard } from '../lib/types'
import { fmtDate, fmtDateTime } from '../lib/format'
import { expiryState, imageURL, kindIcon, kindLabel, maskNumber, money, shrinkImage, whenText } from '../lib/wallet'
import { CardFace, CodeView, ExpiryPill, ShowCard } from '../components/Cards'
import { CardModal } from '../components/CardForm'
import { useCrumb } from '../components/Layout'
import { Icon } from '../components/icons'
import { Card, Confirm, Loading, MenuButton, copyText, useAsync, useToast, type MenuItem } from '../components/ui'

const SOURCES: Record<string, string> = { pkpass: 'Added from a .pkpass file', catima: 'Moved in from Catima', import: 'Imported', email: 'Found in your email' }

export default function CardPage() {
  const { id } = useParams()
  const [sp, setSp] = useSearchParams()
  const navigate = useNavigate()
  const { toast } = useToast()
  const { settings } = useApp()
  const { kinds } = useData()
  const { data: c, error, reload, setData } = useAsync(() => api.get<WalletCard>(`${V1}/cards/${id}`), [id])
  useCrumb(c?.name, c?.group === 'passes' ? 'passes' : c?.group === 'ids' ? 'ids' : 'cards')
  const [show, setShow] = useState(false)
  const [edit, setEdit] = useState(false)
  const [del, setDel] = useState(false)
  const [reveal, setReveal] = useState(false)
  useEffect(() => { if (c && sp.get('show') === '1') { setShow(true); setSp(new URLSearchParams(), { replace: true }) } }, [c]) // eslint-disable-line react-hooks/exhaustive-deps
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error === 'not found' ? 'This card is not in your wallet.' : error}</span></div>
  if (!c) return <Loading />

  const put = async (patch: Partial<WalletCard>, msg: string) => {
    try { setData(await api.put<WalletCard>(`${V1}/cards/${c.id}`, patch)); announceChange(); toast(msg, 'ok') } catch (e) { toast((e as Error).message, 'err') }
  }
  const calendar = async (remove: boolean) => {
    try { setData(await api.post<WalletCard>(`${V1}/cards/${c.id}/calendar${remove ? '?remove=1' : ''}`)); toast(remove ? 'Taken off your calendar' : 'On your calendar', 'ok') } catch (e) { toast((e as Error).message, 'err') }
  }
  const remove = async () => {
    try { await api.del(`${V1}/cards/${c.id}`); announceChange(); toast(`${c.name} removed`, 'ok'); navigate(c.group === 'passes' ? '/passes' : c.group === 'ids' ? '/ids' : '/cards') } catch (e) { toast((e as Error).message, 'err') }
  }
  const calOn = !!settings?.calendar_via
  const menu: MenuItem[] = [
    { label: c.pinned ? 'Unpin' : 'Pin to the top', icon: 'star', onSelect: () => put({ pinned: !c.pinned }, c.pinned ? 'Unpinned' : 'Pinned') },
    ...(c.group === 'passes' && c.starts_at && calOn ? [c.cal_event_id ? { label: 'Take it off my calendar', icon: 'calendar', onSelect: () => calendar(true) } : { label: 'Put it on my calendar', icon: 'calendar', onSelect: () => calendar(false) }] : []),
    { label: c.archived ? 'Take out of the archive' : 'Archive', icon: 'archive', onSelect: () => put({ archived: !c.archived }, c.archived ? 'Back in your wallet' : 'Archived') },
    'sep',
    { label: 'Remove', icon: 'trash', danger: true, onSelect: () => setDel(true) },
  ]
  const hide = c.group === 'ids' && settings?.hide_numbers !== false && !reveal
  const travel = settings?.travel_open_url, cal = settings?.calendar_open_url
  const facts: [string, string, React.ReactNode][] = []
  if (c.group === 'passes') {
    if (c.starts_at) facts.push(['clock', c.kind === 'boarding' ? 'Departs' : 'When', <>{whenText(c.starts_at)}{c.ends_at && <span className="muted"> → {c.ends_at.slice(0, 10) === c.starts_at.slice(0, 10) ? c.ends_at.slice(11) : whenText(c.ends_at)}</span>}</>])
    if (c.venue) facts.push(['pin', c.kind === 'boarding' || c.destination ? 'From' : 'Where', c.venue])
    if (c.destination) facts.push(['navigation', 'To', c.destination])
    if (c.seat) facts.push(['users', 'Seat', c.seat])
    if (c.gate) facts.push(['flag', c.kind === 'boarding' ? 'Gate' : 'Entrance', c.gate])
    if (c.ref) facts.push(['ticket', c.kind === 'boarding' ? 'Flight' : 'Booking', c.ref])
    if (c.trip_id) facts.push(['plane', 'Trip', travel ? <a href={`${travel.replace(/\/$/, '')}/trips/${c.trip_id}`} target="_blank" rel="noreferrer">{c.trip_name || 'Open in Pocket Travel'} <Icon name="external" size={12} /></a> : c.trip_name])
    if (c.cal_event_id) facts.push(['calendar', 'Calendar', cal ? <a href={`${cal.replace(/\/$/, '')}/?date=${c.starts_at.slice(0, 10)}`} target="_blank" rel="noreferrer">On your calendar <Icon name="external" size={12} /></a> : 'On your calendar'])
  }
  if (c.holder) facts.push(['user', 'Name on it', c.holder])
  if (c.number) facts.push(['card', c.group === 'ids' ? 'Number' : 'Card number', <span className="row" style={{ gap: 6 }}>
    <span className="mono">{hide ? maskNumber(c.number) : c.number}</span>
    {hide && <button type="button" className="link-btn small" onClick={() => setReveal(true)}>Show</button>}
    <button type="button" className="btn ghost icon-only sm" title="Copy" aria-label="Copy the number" onClick={async () => toast(await copyText(c.number) ? 'Copied' : 'Could not copy', 'ok')}><Icon name="copy" size={13} /></button>
  </span>])
  if (c.points != null) facts.push(['star', 'Points', c.points.toLocaleString()])
  if (c.valid_from) facts.push(['calendar', 'Valid from', fmtDate(c.valid_from)])
  if (c.expires_on) facts.push(['hourglass', c.group === 'ids' ? 'Valid until' : 'Runs out', <span className="row wrap" style={{ gap: 8 }}>{fmtDate(c.expires_on)}<ExpiryPill day={c.expires_on} /></span>])
  for (const f of c.fields) facts.push(['dot', f.label || 'Detail', f.value])
  const st = expiryState(c.expires_on, 30)

  return (
    <div className="stack loose">
      <div className="detail-head">
        <CardFace c={c} size={52} />
        <div className="grow" style={{ minWidth: 0 }}>
          <h1 className="page-title ellipsis">{c.name}{c.pinned && <Icon name="star" size={16} className="muted" />}</h1>
          <p className="muted small kind-line"><Icon name={kindIcon(c.kind)} size={13} />{kindLabel(kinds, c.kind)}{c.issuer && c.issuer !== c.name ? ` · ${c.issuer}` : ''}{c.archived ? ' · archived' : ''}</p>
        </div>
        <div className="row toolbar">
          <button type="button" className="btn primary" onClick={() => setShow(true)}><Icon name="maximize" size={15} />Show</button>
          <button type="button" className="btn" onClick={() => setEdit(true)}><Icon name="edit" size={15} /><span className="hide-xs">Edit</span></button>
          <MenuButton label="More" items={menu} align="right" width={240}><Icon name="more" size={16} /></MenuButton>
        </div>
      </div>
      {(st === 'gone' || st === 'due') && <div className="callout warn"><Icon name="alert" size={15} /><span>{c.name} {st === 'gone' ? 'has run out' : 'runs out very soon'} ({fmtDate(c.expires_on)}).</span></div>}

      <div className="two-grid">
        <div className="stack">
          {c.code_format ? <button type="button" className="code-card" onClick={() => setShow(true)} aria-label="Show the code full screen"><CodeView c={c} /></button>
            : c.group !== 'ids' && <div className="callout info"><Icon name="info" size={15} /><span>No barcode on this one. <button type="button" className="link-btn" onClick={() => setEdit(true)}>Add or scan one</button></span></div>}
          <Photos c={c} onChange={setData} />
        </div>
        <div className="stack">
          {facts.length > 0 && <Card><div className="facts">{facts.map(([icon, label, v], i) => <div key={i} className="fact"><Icon name={icon} size={15} /><span className="fact-l">{label}</span><span className="grow">{v}</span></div>)}</div></Card>}
          {c.kind === 'gift' && <Balance c={c} onChange={setData} />}
          {c.group === 'cards' && settings?.money_spend && settings.money_via && <SpentHere c={c} />}
          {(c.note || c.tags) && <Card>{c.note && <p className="notes" style={{ marginTop: 0 }}>{c.note}</p>}{c.tags && <div className="chips mt">{c.tags.split(',').map(t => <span key={t} className="chip">{t.trim()}</span>)}</div>}</Card>}
          <p className="muted small">{SOURCES[c.source] || (c.source ? `Added by Pocket ${c.source[0].toUpperCase()}${c.source.slice(1)}` : 'Added by hand')} · {fmtDateTime(c.created_at)}{c.uses > 0 ? ` · shown ${c.uses} ${c.uses === 1 ? 'time' : 'times'}` : ''}</p>
        </div>
      </div>
      {show && <ShowCard c={c} onClose={() => { setShow(false); reload() }} />}
      {edit && <CardModal card={c} onClose={() => setEdit(false)} onSaved={setData} />}
      {del && <Confirm title={`Remove ${c.name}?`} confirmLabel="Remove" message={c.cal_event_id ? 'It goes from your wallet and from your calendar.' : 'It goes from your wallet, with its photos.'} onCancel={() => setDel(false)} onConfirm={() => { setDel(false); remove() }} />}
    </div>
  )
}

function Photos({ c, onChange }: { c: WalletCard; onChange: (c: WalletCard) => void }) {
  const { toast } = useToast()
  const [busy, setBusy] = useState('')
  const sides: ('front' | 'back')[] = ['front', 'back']
  if (c.group === 'passes' && !c.front_image && !c.back_image) return null
  const upload = async (side: 'front' | 'back', file: File) => {
    setBusy(side)
    try { onChange(await sendFile<WalletCard>('PUT', `${V1}/cards/${c.id}/images/${side}`, await shrinkImage(file, 2000, 0.88))); toast('Photo saved', 'ok') } catch (e) { toast((e as Error).message, 'err') } finally { setBusy('') }
  }
  const drop = async (side: 'front' | 'back') => { try { onChange(await api.del<WalletCard>(`${V1}/cards/${c.id}/images/${side}`)) } catch (e) { toast((e as Error).message, 'err') } }
  return (
    <div className="photos">
      {sides.map(side => {
        const url = imageURL(c, side)
        return (
          <div key={side} className="photo">
            {url ? <a href={url} target="_blank" rel="noreferrer"><img src={url} alt={`${c.name}, ${side}`} loading="lazy" /></a> : <span className="photo-empty"><Icon name="camera" size={20} /></span>}
            <div className="row" style={{ gap: 4 }}>
              <span className="muted small grow">{side === 'front' ? 'Front' : 'Back'}</span>
              <label className="btn ghost sm">{busy === side ? 'Saving…' : url ? 'Replace' : 'Add a photo'}<input type="file" accept="image/*" hidden onChange={e => { const f = e.target.files?.[0]; e.target.value = ''; if (f) upload(side, f) }} /></label>
              {url && <button type="button" className="btn ghost icon-only sm" aria-label="Remove the photo" onClick={() => drop(side)}><Icon name="trash" size={13} /></button>}
            </div>
          </div>
        )
      })}
    </div>
  )
}

function Balance({ c, onChange }: { c: WalletCard; onChange: (c: WalletCard) => void }) {
  const { toast } = useToast()
  const { data: log, reload } = useAsync(() => api.get<BalanceEntry[]>(`${V1}/cards/${c.id}/balance`), [c.id, c.balance])
  const [amount, setAmount] = useState('')
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const cur = c.currency || 'EUR'
  const go = async (sign: 1 | -1) => {
    const v = Math.round(Number(amount.replace(',', '.')) * 100)
    if (!v) return
    setBusy(true)
    try { const r = await api.post<{ card: WalletCard }>(`${V1}/cards/${c.id}/balance`, { delta: sign * v, note }); onChange(r.card); setAmount(''); setNote(''); reload(); announceChange() } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  return (
    <Card title="On the card" actions={<span className="big-num tnum">{c.balance != null ? money(c.balance, cur) : '—'}</span>}>
      <div className="row wrap balance-form">
        <input value={amount} onChange={e => setAmount(e.target.value.replace(/[^\d.,]/g, ''))} inputMode="decimal" placeholder="Amount" aria-label="Amount" className="num-sm" />
        <input value={note} onChange={e => setNote(e.target.value)} placeholder="What for (optional)" aria-label="What for" className="grow" />
        <button type="button" className="btn" onClick={() => go(-1)} disabled={busy || !amount || c.balance == null}>Spent</button>
        <button type="button" className="btn ghost" onClick={() => go(1)} disabled={busy || !amount}>Added</button>
      </div>
      {log && log.length > 0 && <div className="list mt">{log.map(e => (
        <div key={e.id} className="list-row bal-row">
          <span className="row-main grow"><span className="ellipsis">{e.note || (e.delta < 0 ? 'Spent' : 'Added')}</span><span className="muted small">{fmtDateTime(e.created_at)}</span></span>
          <span className={`tnum ${e.delta < 0 ? '' : 'ok'}`}>{e.delta > 0 ? '+' : '−'}{money(Math.abs(e.delta), cur)}</span>
          <span className="muted small tnum hide-xs">{money(e.balance, cur)}</span>
        </div>
      ))}</div>}
    </Card>
  )
}

function SpentHere({ c }: { c: WalletCard }) {
  const { data } = useAsync(() => api.get<Spent>(`${V1}/cards/${c.id}/spent`), [c.id])
  if (!data || !data.connected || !data.count) return null
  return (
    <Card>
      <div className="fact"><Icon name="wallet" size={15} /><span className="grow">
        <b>{(data.sums || []).map(s => money(s.amount, s.currency)).join(' + ')}</b> at {c.issuer || c.name} in the last 90 days, {data.count} {data.count === 1 ? 'payment' : 'payments'}{data.last ? `, the last on ${fmtDate(data.last)}` : ''}.
        {data.open_url && <> <a href={data.open_url} target="_blank" rel="noreferrer">Pocket Money <Icon name="external" size={12} /></a></>}
      </span></div>
    </Card>
  )
}
