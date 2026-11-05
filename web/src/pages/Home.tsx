// The wallet: passes coming up, your cards to tap and show, what runs out soon.
import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useOnChange } from '../lib/data'
import type { Overview, WalletCard } from '../lib/types'
import { CardTile, ExpiryPill, IdRow, PassRow, ShowCard, CardFace } from '../components/Cards'
import { openCreate } from '../components/Layout'
import { Icon } from '../components/icons'
import { Card, Loading, useAsync } from '../components/ui'

export default function Home() {
  const { settings } = useApp()
  const { data, error, reload } = useAsync(() => api.get<Overview>(`${V1}/overview`), [])
  useOnChange(reload)
  const [q, setQ] = useState('')
  const [show, setShow] = useState<WalletCard | null>(null)
  const n = q.trim().toLowerCase()
  const hit = (c: WalletCard) => !n || [c.name, c.issuer, c.number, c.holder, c.tags, c.venue, c.destination, c.ref].some(v => v.toLowerCase().includes(n))
  const cards = useMemo(() => (data?.cards || []).filter(hit), [data, n]) // eslint-disable-line react-hooks/exhaustive-deps
  if (error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{error}</span></div>
  if (!data) return <Loading />
  const next = data.next.filter(hit)
  const ids = data.ids.filter(hit)
  const soon = data.expiring.filter(c => c.days_left <= 60 && hit(c))
  const empty = data.counts.cards + data.counts.passes + data.counts.ids === 0
  if (empty) return <Welcome />
  return (
    <div className="stack loose">
      <div className="home-search">
        <Icon name="search" size={16} />
        <input value={q} onChange={e => setQ(e.target.value)} placeholder="Find a card, a ticket, an ID…" aria-label="Find a card" />
        {q && <button type="button" className="btn ghost icon-only sm" onClick={() => setQ('')} aria-label="Clear"><Icon name="x" size={14} /></button>}
      </div>

      {next.length > 0 && (
        <Card title="Coming up" flush actions={<Link to="/passes" className="btn ghost sm">All passes<Icon name="chevron-right" size={14} /></Link>}>
          <div className="list">{next.slice(0, 4).map(c => <PassRow key={c.id} c={c} />)}</div>
        </Card>
      )}

      <section>
        <div className="sec-head"><h2>Cards</h2><span className="muted small">{cards.length}</span><span className="grow" /><Link to="/cards" className="btn ghost sm">All cards<Icon name="chevron-right" size={14} /></Link></div>
        {cards.length ? <div className="ctiles">{cards.map(c => <CardTile key={c.id} c={c} onShow={setShow} />)}</div>
          : <p className="muted">{n ? 'No card matches.' : 'No shop cards yet.'} <button type="button" className="link-btn" onClick={() => openCreate('loyalty')}>Add one</button></p>}
      </section>

      {soon.length > 0 && (
        <Card title="Runs out soon" flush>
          <div className="list">
            {soon.map(c => (
              <Link key={c.id} to={`/cards/${c.id}`} className="list-row">
                <CardFace c={c} size={32} />
                <span className="row-main grow"><span className="row-title ellipsis">{c.name}{c.holder && c.group === 'ids' ? <span className="muted small"> · {c.holder}</span> : null}</span></span>
                <ExpiryPill day={c.expires_on} soon={60} />
              </Link>
            ))}
          </div>
        </Card>
      )}

      {ids.length > 0 && (
        <Card title="IDs and documents" flush actions={<Link to="/ids" className="btn ghost sm">All<Icon name="chevron-right" size={14} /></Link>}>
          <div className="list">{ids.slice(0, 5).map(c => <IdRow key={c.id} c={c} hide={settings?.hide_numbers !== false} />)}</div>
        </Card>
      )}
      {show && <ShowCard c={show} onClose={() => setShow(null)} />}
    </div>
  )
}

function Welcome() {
  return (
    <div className="welcome card">
      <div className="welcome-body">
        <img src="/favicon.svg" alt="" width={56} height={56} />
        <div className="col">
          <h2>Your wallet is empty</h2>
          <p className="muted">Add the cards you show at the till, your tickets and boarding passes, and your ID papers. A shop card takes a few seconds: scan its barcode with the camera.</p>
          <div className="row wrap mt">
            <button type="button" className="btn primary" onClick={() => openCreate('loyalty')}><Icon name="plus" size={15} />Add a shop card</button>
            <button type="button" className="btn" onClick={() => openCreate('ticket')}><Icon name="ticket" size={15} />A ticket or boarding pass</button>
            <button type="button" className="btn" onClick={() => openCreate('pkpass')}><Icon name="upload" size={15} />A .pkpass file</button>
            <Link to="/settings/data" className="btn"><Icon name="download" size={15} />Move in from Catima</Link>
          </div>
        </div>
      </div>
    </div>
  )
}
