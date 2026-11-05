// The three drawers of the wallet — cards, passes, IDs — and the archive.
import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { announceChange, useData, useOnChange } from '../lib/data'
import type { MailResult, WalletCard } from '../lib/types'
import { whenText } from '../lib/wallet'
import { CardFace, CardTile, IdRow, PassRow, ShowCard } from '../components/Cards'
import { openCreate } from '../components/Layout'
import { Icon } from '../components/icons'
import { Card, Empty, Loading, PageHeader, Segmented, useAsync, useToast } from '../components/ui'
import { Link } from 'react-router-dom'

export function CardsPage() {
  const { settings } = useApp()
  const { kinds } = useData()
  const [sp, setSp] = useSearchParams()
  const kind = sp.get('kind') || ''
  const sort = (sp.get('sort') || settings?.sort || 'used') as 'used' | 'name' | 'recent'
  const [q, setQ] = useState('')
  const { data, reload } = useAsync(() => api.get<WalletCard[]>(`${V1}/cards?group=cards&sort=${sort}`), [sort])
  useOnChange(reload)
  const [show, setShow] = useState<WalletCard | null>(null)
  const n = q.trim().toLowerCase()
  const shown = useMemo(() => (data || []).filter(c => (!kind || c.kind === kind) && (!n || [c.name, c.issuer, c.number, c.tags].some(v => v.toLowerCase().includes(n)))), [data, kind, n])
  const count = (k: string) => (data || []).filter(c => c.kind === k).length
  const set = (k: string, v: string) => { const p = new URLSearchParams(sp); if (v) p.set(k, v); else p.delete(k); setSp(p, { replace: true }) }
  return (
    <div className="stack">
      <PageHeader title="Cards" sub="Shop and membership cards, gift cards and vouchers. Tap one to show its code." actions={<button type="button" className="btn primary" onClick={() => openCreate('loyalty')}><Icon name="plus" size={15} />Add a card</button>} />
      <div className="row wrap filters">
        <input className="search-in" value={q} onChange={e => setQ(e.target.value)} placeholder="Find a card…" aria-label="Find a card" />
        <Segmented size="sm" value={sort} onChange={v => set('sort', v)} options={[{ value: 'used', label: 'Most used' }, { value: 'name', label: 'A–Z' }, { value: 'recent', label: 'Newest' }]} />
      </div>
      <div className="chips scroll">
        <button type="button" className={`chip ${!kind ? 'on' : ''}`} onClick={() => set('kind', '')}>All <span className="n">{data?.length ?? ''}</span></button>
        {kinds.filter(k => k.group === 'cards' && count(k.key) > 0).map(k => (
          <button key={k.key} type="button" className={`chip ${kind === k.key ? 'on' : ''}`} onClick={() => set('kind', k.key)}>{k.label} <span className="n">{count(k.key)}</span></button>
        ))}
      </div>
      {!data ? <Loading /> : shown.length ? <div className="ctiles">{shown.map(c => <CardTile key={c.id} c={c} onShow={setShow} />)}</div>
        : <Empty icon="card" title={n || kind ? 'No card matches' : 'No cards yet'}>{!n && !kind && <button type="button" className="btn primary" onClick={() => openCreate('loyalty')}>Add your first card</button>}</Empty>}
      {show && <ShowCard c={show} onClose={() => setShow(null)} />}
    </div>
  )
}

export function PassesPage() {
  const { settings } = useApp()
  const { toast } = useToast()
  const [past, setPast] = useState(false)
  const { data: next, reload } = useAsync(() => api.get<WalletCard[]>(`${V1}/passes`), [])
  const { data: old, reload: reloadOld } = useAsync(() => past ? api.get<WalletCard[]>(`${V1}/cards?group=passes&archived=all&sort=starts`) : Promise.resolve(null), [past])
  useOnChange(() => { reload(); reloadOld() })
  const [busy, setBusy] = useState(false)
  const mailOn = !!settings?.email_via
  const look = async () => {
    setBusy(true)
    try {
      const r = await api.post<MailResult>(`${V1}/mail/scan`)
      if (!r.connected) toast(r.detail || 'Pocket Email is not connected', 'err')
      else if (r.added.length) { toast(`Added ${r.added.length} from your email`, 'ok'); announceChange() }
      else toast(r.updated ? `${r.updated} brought up to date` : r.seen ? 'Nothing new in your email' : 'No passes (.pkpass) in your email', 'ok')
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  const nextIds = new Set((next || []).map(c => c.id))
  const gone = (old || []).filter(c => !nextIds.has(c.id)).reverse()
  const groups: { day: string; items: WalletCard[] }[] = []
  for (const c of next || []) {
    const d = c.starts_at.slice(0, 10)
    const g = groups[groups.length - 1]
    if (g && g.day === d) g.items.push(c); else groups.push({ day: d, items: [c] })
  }
  return (
    <div className="stack">
      <PageHeader title="Passes" sub="Tickets and boarding passes, the next first. They go on your calendar and with their trip when those apps are connected."
        actions={<>
          {mailOn && <button type="button" className="btn" onClick={look} disabled={busy}><Icon name="mail" size={15} />{busy ? 'Looking…' : 'Look in my email'}</button>}
          <button type="button" className="btn primary" onClick={() => openCreate('ticket')}><Icon name="plus" size={15} />Add a pass</button>
        </>} />
      {!next ? <Loading /> : groups.length ? (
        <Card flush>
          <div className="list">
            {groups.map(g => <div key={g.day}><div className="day-h">{whenText(g.day, false)}</div>{g.items.map(c => <PassRow key={c.id} c={c} />)}</div>)}
          </div>
        </Card>
      ) : <Empty icon="ticket" title="Nothing coming up">
        <p className="muted small">Add a ticket by hand, from a .pkpass file{mailOn ? ', or let Wallet find them in your email' : ''}.</p>
        <div className="row wrap" style={{ justifyContent: 'center' }}><button type="button" className="btn primary" onClick={() => openCreate('ticket')}>Add a pass</button><button type="button" className="btn" onClick={() => openCreate('pkpass')}>A .pkpass file</button></div>
      </Empty>}
      <div className="row"><button type="button" className="btn ghost sm" onClick={() => setPast(p => !p)}><Icon name={past ? 'chevron-up' : 'chevron-down'} size={14} />{past ? 'Hide the past ones' : 'Show the past ones'}</button></div>
      {past && (old ? gone.length ? <Card flush><div className="list">{gone.map(c => <PassRow key={c.id} c={c} />)}</div></Card> : <p className="muted small">No past passes.</p> : <Loading />)}
    </div>
  )
}

export function IdsPage() {
  const { settings } = useApp()
  const [reveal, setReveal] = useState(false)
  const { data, reload } = useAsync(() => api.get<WalletCard[]>(`${V1}/cards?group=ids`), [])
  useOnChange(reload)
  const hide = settings?.hide_numbers !== false && !reveal
  const byHolder = useMemo(() => {
    const m = new Map<string, WalletCard[]>()
    for (const c of data || []) { const k = c.holder || ''; m.set(k, [...(m.get(k) || []), c]) }
    return [...m.entries()].sort((a, b) => (a[0] === '' ? 1 : b[0] === '' ? -1 : a[0].localeCompare(b[0])))
  }, [data])
  return (
    <div className="stack">
      <PageHeader title="IDs and documents" sub="ID cards, passports, driving licences and insurance cards: the number at hand and a reminder before they run out."
        actions={<>
          {settings?.hide_numbers !== false && <button type="button" className="btn" onClick={() => setReveal(r => !r)}><Icon name="eye" size={15} />{reveal ? 'Hide numbers' : 'Show numbers'}</button>}
          <button type="button" className="btn primary" onClick={() => openCreate('id')}><Icon name="plus" size={15} />Add</button>
        </>} />
      {!data ? <Loading /> : data.length ? byHolder.map(([holder, items]) => (
        <Card key={holder} title={holder || 'Without a name'} flush>
          <div className="list">{items.map(c => <IdRow key={c.id} c={c} hide={hide} />)}</div>
        </Card>
      )) : <Empty icon="id-card" title="No documents yet"><button type="button" className="btn primary" onClick={() => openCreate('id')}>Add your ID card or passport</button></Empty>}
    </div>
  )
}

export function ArchivePage() {
  const { data, reload } = useAsync(() => api.get<WalletCard[]>(`${V1}/cards?archived=only&sort=recent`), [])
  useOnChange(reload)
  return (
    <div className="stack">
      <PageHeader title="Archive" sub="Cards you no longer use and passes put away. They stay searchable; nothing here reminds you." />
      {!data ? <Loading /> : data.length ? (
        <Card flush>
          <div className="list">
            {data.map(c => (
              <Link key={c.id} to={`/cards/${c.id}`} className="list-row is-archived">
                <CardFace c={c} size={32} />
                <span className="row-main grow"><span className="row-title ellipsis">{c.name}</span><span className="muted small ellipsis">{[c.issuer !== c.name && c.issuer, c.starts_at && whenText(c.starts_at)].filter(Boolean).join(' · ')}</span></span>
              </Link>
            ))}
          </div>
        </Card>
      ) : <Empty icon="archive" title="Nothing in the archive" />}
    </div>
  )
}
