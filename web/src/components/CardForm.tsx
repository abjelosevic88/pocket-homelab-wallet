// Adding or changing a card: what it is first, then only the fields that
// kind needs. A barcode can be scanned, a pass read from a booking email.
import React, { useEffect, useMemo, useState } from 'react'
import { api, sendFile, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { announceChange, useData } from '../lib/data'
import type { Field as CardField, Group, Picked, Trip, WalletCard } from '../lib/types'
import { cardColor, inkOn, kindIcon, shrinkImage } from '../lib/wallet'
import { fmtRange } from '../lib/format'
import { Icon } from './icons'
import { Field, Modal, Spinner, useDebounce, useToast } from './ui'
import { Select } from './Select'
import { Scanner, type Found } from './Scanner'

const SWATCHES = ['#0b7a68', '#1d4ed8', '#0058a3', '#7c3aed', '#be185d', '#e30613', '#c2410c', '#f39200', '#e8b600', '#4d7c0f', '#0e7490', '#334155', '#111827']

type Form = {
  kind: string; name: string; issuer: string; number: string; holder: string; code_format: string; code_value: string; color: string
  note: string; tags: string; fields: CardField[]; valid_from: string; expires_on: string; day: string; time: string; end_day: string; end_time: string
  venue: string; destination: string; seat: string; gate: string; ref: string; balance: string; currency: string; points: string; trip_id: number | null; trip_name: string
}

const empty = (kind: string, currency: string): Form => ({ kind, name: '', issuer: '', number: '', holder: '', code_format: '', code_value: '', color: '', note: '', tags: '', fields: [],
  valid_from: '', expires_on: '', day: '', time: '', end_day: '', end_time: '', venue: '', destination: '', seat: '', gate: '', ref: '', balance: '', currency, points: '', trip_id: null, trip_name: '' })

function fromCard(c: WalletCard): Form {
  return {
    kind: c.kind, name: c.name, issuer: c.issuer, number: c.number, holder: c.holder, code_format: c.code_format, code_value: c.code_value === c.number.replace(/ /g, '') ? '' : c.code_value,
    color: c.color, note: c.note, tags: c.tags, fields: c.fields, valid_from: c.valid_from, expires_on: c.expires_on,
    day: c.starts_at.slice(0, 10), time: c.starts_at.slice(11, 16), end_day: c.ends_at.slice(0, 10), end_time: c.ends_at.slice(11, 16),
    venue: c.venue, destination: c.destination, seat: c.seat, gate: c.gate, ref: c.ref,
    balance: c.balance != null ? (c.balance / 100).toFixed(2) : '', currency: c.currency, points: c.points != null ? String(c.points) : '', trip_id: c.trip_id, trip_name: c.trip_name,
  }
}

const when = (d: string, t: string) => d ? (t ? `${d}T${t}` : d) : ''

function toBody(f: Form, group: Group) {
  const b: Record<string, unknown> = { kind: f.kind, name: f.name, issuer: f.issuer, number: f.number, holder: f.holder, code_format: f.code_format, code_value: f.code_value,
    color: f.color, note: f.note, tags: f.tags, fields: f.fields.filter(x => x.value.trim()), valid_from: f.valid_from, expires_on: f.expires_on }
  if (group === 'passes') {
    Object.assign(b, { starts_at: when(f.day, f.time), ends_at: when(f.end_day || (f.end_time ? f.day : ''), f.end_time), venue: f.venue, destination: f.destination, seat: f.seat, gate: f.gate, ref: f.ref, trip_id: f.trip_id, trip_name: f.trip_name })
  }
  if (f.kind === 'gift') {
    const v = f.balance.replace(',', '.').trim()
    b.balance = v === '' ? null : Math.round(Number(v) * 100)
    b.currency = f.currency
  }
  if (group === 'cards') b.points = f.points.trim() === '' ? null : Math.round(Number(f.points))
  return b
}

export function CardModal({ card, kind: startKind, scanFirst, onClose, onSaved }: { card?: WalletCard; kind?: string; scanFirst?: boolean; onClose: () => void; onSaved?: (c: WalletCard) => void }) {
  const { kinds } = useData()
  const { settings } = useApp()
  const { toast } = useToast()
  const [f, setF] = useState<Form>(() => card ? fromCard(card) : empty(startKind || '', settings?.currency || 'EUR'))
  const [busy, setBusy] = useState(false)
  const [scan, setScan] = useState(!!scanFirst)
  const [reading, setReading] = useState(false)
  const [photos, setPhotos] = useState<{ front?: File; back?: File }>({})
  const [more, setMore] = useState(!!card && (card.fields.length > 0 || !!card.note))
  const kind = kinds.find(k => k.key === f.kind)
  const group: Group = kind?.group || 'cards'
  const set = (patch: Partial<Form>) => setF(x => ({ ...x, ...patch }))

  if (!f.kind) {
    return (
      <Modal title="What are you adding?" onClose={onClose} size="sm">
        <div className="kind-pick">
          {kinds.map(k => (
            <button key={k.key} type="button" className="kind-opt" onClick={() => set({ kind: k.key })}>
              <span className="kind-ico"><Icon name={kindIcon(k.key)} size={18} /></span>
              <span className="grow"><b>{k.label}</b><span className="muted small block-line">{k.hint}</span></span>
              <Icon name="chevron-right" size={16} className="muted" />
            </button>
          ))}
        </div>
      </Modal>
    )
  }

  const found = (x: Found) => {
    setScan(false)
    const typed = f.number.trim()
    set({ code_format: x.format, code_value: typed && typed.replace(/ /g, '') !== x.value ? x.value : '', number: typed || (x.value.length <= 30 ? x.value : '') })
    toast('Barcode read', 'ok')
  }

  const save = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    try {
      let c = card ? await api.put<WalletCard>(`${V1}/cards/${card.id}`, toBody(f, group)) : await api.post<WalletCard>(`${V1}/cards`, toBody(f, group))
      for (const side of ['front', 'back'] as const) {
        const file = photos[side]
        if (file) c = await sendFile<WalletCard>('PUT', `${V1}/cards/${c.id}/images/${side}`, await shrinkImage(file, 2000, 0.88))
      }
      toast(card ? 'Saved' : `${c.name} added`, 'ok')
      announceChange()
      onSaved?.(c)
      onClose()
    } catch (err) { toast((err as Error).message, 'err') } finally { setBusy(false) }
  }

  const label = kind ? (kind.key === 'id' ? 'ID card' : kind.label.charAt(0).toLowerCase() + kind.label.slice(1)) : 'card'
  const title = card ? `Edit ${card.name}` : `Add ${/^[aeiouIA]/.test(label) ? 'an' : 'a'} ${label}`
  return (
    <Modal title={title} onClose={onClose} size="lg" footer={<div className="form-actions">
      {!card && <button type="button" className="btn ghost" onClick={() => set({ kind: '' })}><Icon name="chevron-left" size={15} />Another kind</button>}
      <span className="grow" />
      <button type="button" className="btn" onClick={onClose}>Cancel</button>
      <button type="submit" form="card-form" className="btn primary" disabled={busy}>{busy ? 'Saving…' : card ? 'Save' : 'Add'}</button>
    </div>}>
      <form id="card-form" onSubmit={save} className="grid form-grid">
        {group === 'passes' && !card && <ReadEmail onDraft={d => set(d)} busy={reading} setBusy={setReading} />}
        <div className="two-col tight">
          <Field label={group === 'passes' ? (f.kind === 'boarding' ? 'Flight' : 'What') : group === 'ids' ? 'Name' : 'Name on the card'}>
            <input value={f.name} onChange={e => set({ name: e.target.value })} autoFocus={!card} maxLength={200}
              placeholder={{ loyalty: 'Konzum Klub', membership: 'Gym', gift: 'Zara gift card', ticket: 'Dubioza kolektiv', boarding: 'Sarajevo → Vienna', id: 'ID card', passport: 'Passport', licence: 'Driving licence', insurance: 'Health insurance', other: '' }[f.kind] || ''} />
          </Field>
          <Field label={group === 'passes' ? (f.kind === 'boarding' ? 'Airline' : 'Sold by') : group === 'ids' ? 'Issued by' : 'Shop or club'}>
            <input value={f.issuer} onChange={e => set({ issuer: e.target.value })} maxLength={200} />
          </Field>
        </div>

        {group === 'passes' && <PassFields f={f} set={set} />}

        <div className="two-col tight">
          <Field label={group === 'ids' ? 'Document number' : group === 'passes' ? 'Ticket number' : 'Card number'} help={group === 'cards' ? 'As printed; the barcode carries it unless you say otherwise.' : undefined}>
            <input value={f.number} onChange={e => set({ number: e.target.value })} inputMode={group === 'cards' ? 'text' : undefined} className="mono" maxLength={200} autoComplete="off" />
          </Field>
          {(group !== 'cards' || f.kind === 'membership') && <Field label="Name on it"><input value={f.holder} onChange={e => set({ holder: e.target.value })} maxLength={200} /></Field>}
          {group === 'cards' && f.kind !== 'membership' && f.kind !== 'gift' && <Field label="Points" help="If the shop shows them."><input value={f.points} onChange={e => set({ points: e.target.value.replace(/[^\d]/g, '') })} inputMode="numeric" /></Field>}
          {f.kind === 'gift' && <Field label="On the card" help={`In ${f.currency || 'your currency'}.`}>
            <div className="input-group"><input value={f.balance} onChange={e => set({ balance: e.target.value.replace(/[^\d.,]/g, '') })} inputMode="decimal" placeholder="50.00" /><input value={f.currency} onChange={e => set({ currency: e.target.value.toUpperCase().slice(0, 3) })} className="cur-in" aria-label="Currency" /></div>
          </Field>}
        </div>

        {group !== 'ids' || f.code_format ? <BarcodeFields f={f} set={set} onScan={() => setScan(true)} /> : (
          <button type="button" className="link-btn small left" onClick={() => set({ code_format: 'pdf417' })}><Icon name="barcode" size={13} /> This document has a barcode to show</button>
        )}

        <div className="two-col tight">
          {(group === 'ids' || f.kind === 'membership' || f.kind === 'insurance') && <Field label="Valid from"><input type="date" value={f.valid_from} onChange={e => set({ valid_from: e.target.value })} /></Field>}
          {group !== 'passes' && <Field label={group === 'ids' ? 'Valid until' : 'Runs out on'} help={group === 'ids' ? 'You get a reminder before it runs out (Settings → Notifications).' : 'If it does.'}><input type="date" value={f.expires_on} onChange={e => set({ expires_on: e.target.value })} /></Field>}
        </div>

        {group === 'ids' && !card && <div className="two-col tight">
          {(['front', 'back'] as const).map(side => (
            <Field key={side} label={side === 'front' ? 'Photo of the front' : 'Photo of the back'} help="Kept on your server only.">
              <label className="btn sm"><Icon name="camera" size={14} />{photos[side] ? photos[side]!.name.slice(0, 24) : 'Choose a photo'}<input type="file" accept="image/*" hidden onChange={e => { const file = e.target.files?.[0]; if (file) setPhotos(p => ({ ...p, [side]: file })) }} /></label>
            </Field>
          ))}
        </div>}

        <ColorPick value={f.color} name={f.name || f.issuer} onChange={color => set({ color })} />

        {!more && <button type="button" className="link-btn small left" onClick={() => setMore(true)}><Icon name="plus" size={13} /> Note, tags and more details</button>}
        {more && <>
          <Field label="Note"><textarea rows={2} value={f.note} onChange={e => set({ note: e.target.value })} /></Field>
          <Field label="Tags" help="Separated by commas: groceries, travel."><input value={f.tags} onChange={e => set({ tags: e.target.value })} /></Field>
          <FieldsEditor fields={f.fields} onChange={fields => set({ fields })} />
        </>}
      </form>
      {scan && <Scanner onFound={found} onClose={() => setScan(false)} />}
    </Modal>
  )
}

function PassFields({ f, set }: { f: Form; set: (p: Partial<Form>) => void }) {
  const { settings } = useApp()
  const travelOn = !!settings?.travel_via
  const [trips, setTrips] = useState<Picked<Trip> | null>(null)
  useEffect(() => { if (travelOn) api.get<Picked<Trip>>(`${V1}/links/travel/trips`).then(setTrips).catch(() => {}) }, [travelOn])
  const air = f.kind === 'boarding'
  return (
    <>
      <div className="two-col tight">
        <Field label={air ? 'Departs' : 'Starts'}><div className="input-group"><input type="date" value={f.day} onChange={e => set({ day: e.target.value })} /><input type="time" value={f.time} onChange={e => set({ time: e.target.value })} className="time-in" aria-label="Time" /></div></Field>
        <Field label={air ? 'Lands' : 'Ends'} help="Optional."><div className="input-group"><input type="date" value={f.end_day} onChange={e => set({ end_day: e.target.value })} /><input type="time" value={f.end_time} onChange={e => set({ end_time: e.target.value })} className="time-in" aria-label="End time" /></div></Field>
      </div>
      <div className="two-col tight">
        <Field label={air ? 'From' : 'Where'}><input value={f.venue} onChange={e => set({ venue: e.target.value })} placeholder={air ? 'SJJ Sarajevo' : 'Zetra Olympic Hall'} /></Field>
        <Field label={air ? 'To' : 'To (a train or bus)'}><input value={f.destination} onChange={e => set({ destination: e.target.value })} placeholder={air ? 'VIE Vienna' : ''} /></Field>
      </div>
      <div className="three-col tight">
        <Field label="Seat"><input value={f.seat} onChange={e => set({ seat: e.target.value })} placeholder={air ? '14C' : 'Row 8, seat 14'} /></Field>
        <Field label={air ? 'Gate' : 'Entrance'}><input value={f.gate} onChange={e => set({ gate: e.target.value })} /></Field>
        <Field label={air ? 'Flight number' : 'Booking'}><input value={f.ref} onChange={e => set({ ref: e.target.value })} placeholder={air ? 'OS 784' : ''} /></Field>
      </div>
      {travelOn && <Field label="Trip" help={f.trip_id ? undefined : 'Left empty, a pass during a trip in Pocket Travel is filed with it.'}>
        <Select ariaLabel="Trip" value={f.trip_id ? String(f.trip_id) : ''} clearLabel="No trip" placeholder="No trip"
          onChange={v => { const t = trips?.items.find(x => String(x.id) === v); set({ trip_id: t ? t.id : null, trip_name: t ? (t.name || t.destination) : '' }) }}
          options={[...(trips?.items || []).map(t => ({ value: String(t.id), label: t.name || t.destination, hint: t.start_date ? fmtRange(t.start_date, t.end_date || t.start_date) : '' })),
            ...(f.trip_id && !trips?.items.some(t => t.id === f.trip_id) ? [{ value: String(f.trip_id), label: f.trip_name || 'A trip' }] : [])]} />
      </Field>}
    </>
  )
}

function BarcodeFields({ f, set, onScan }: { f: Form; set: (p: Partial<Form>) => void; onScan: () => void }) {
  const { formats } = useData()
  const value = f.code_value || f.number.replace(/ /g, '')
  const dv = useDebounce(`${f.code_format}|${value}`, 350)
  const [preview, setPreview] = useState<{ url?: string; err?: string }>({})
  const [own, setOwn] = useState(!!f.code_value)
  useEffect(() => {
    if (!f.code_format || !value) { setPreview({}); return }
    let live = true, url = ''
    fetch(`${V1}/barcode`, { method: 'POST', body: JSON.stringify({ format: f.code_format, value }), headers: { 'Content-Type': 'application/json' }, credentials: 'same-origin' })
      .then(async r => { if (!live) return; if (r.ok) { url = URL.createObjectURL(await r.blob()); setPreview({ url }) } else setPreview({ err: (await r.json().catch(() => ({}))).error || 'That cannot be drawn' }) })
      .catch(() => {})
    return () => { live = false; if (url) URL.revokeObjectURL(url) }
  }, [dv]) // eslint-disable-line react-hooks/exhaustive-deps
  const opts = useMemo(() => [{ value: '', label: 'No barcode' }, ...formats.map(x => ({ value: x.key, label: x.label, hint: x.hint }))], [formats])
  return (
    <div className="barcode-box">
      <div className="row wrap">
        <div className="grow" style={{ minWidth: 180 }}><Select ariaLabel="Kind of barcode" value={f.code_format} onChange={v => set({ code_format: v })} options={opts} /></div>
        <button type="button" className="btn" onClick={onScan}><Icon name="camera" size={15} />Scan it</button>
      </div>
      {f.code_format && <>
        {!own && <button type="button" className="link-btn small left" onClick={() => setOwn(true)}>The barcode carries something other than the number</button>}
        {own && <Field label="What the barcode carries"><input value={f.code_value} onChange={e => set({ code_value: e.target.value })} className="mono" placeholder={f.number.replace(/ /g, '')} /></Field>}
        <div className="barcode-preview">
          {preview.url && <img src={preview.url} alt="Barcode preview" />}
          {preview.err && <span className="bad small">{preview.err}</span>}
          {!preview.url && !preview.err && <span className="muted small">{value ? <Spinner /> : 'Type the number or scan the card to see the barcode.'}</span>}
        </div>
      </>}
    </div>
  )
}

function ColorPick({ value, name, onChange }: { value: string; name: string; onChange: (v: string) => void }) {
  const shown = value || cardColor({ color: '', name, issuer: '' })
  return (
    <Field label="Colour">
      <div className="swatches" role="radiogroup" aria-label="Colour">
        {SWATCHES.map(c => (
          <button key={c} type="button" role="radio" aria-checked={value === c} className={`swatch ${value === c ? 'on' : ''}`} style={{ background: c, color: inkOn(c) }} onClick={() => onChange(c)} aria-label={c}>
            {value === c && <Icon name="check" size={14} strokeWidth={2.4} />}
          </button>
        ))}
        <label className={`swatch custom ${value && !SWATCHES.includes(value) ? 'on' : ''}`} style={{ background: value && !SWATCHES.includes(value) ? value : undefined }} title="Another colour">
          <Icon name="palette" size={14} /><input type="color" value={shown} onChange={e => onChange(e.target.value)} aria-label="Another colour" />
        </label>
      </div>
    </Field>
  )
}

function FieldsEditor({ fields, onChange }: { fields: CardField[]; onChange: (f: CardField[]) => void }) {
  const rows = [...fields, { label: '', value: '' }]
  return (
    <Field label="More details" help="Anything else printed on it: terminal, membership level, emergency number.">
      <div className="fields-edit">
        {rows.map((x, i) => (
          <div key={i} className="row">
            <input value={x.label} placeholder="Label" onChange={e => { const n = [...rows]; n[i] = { ...x, label: e.target.value }; onChange(n.filter(r => r.label || r.value)) }} className="fe-label" />
            <input value={x.value} placeholder="Value" onChange={e => { const n = [...rows]; n[i] = { ...x, value: e.target.value }; onChange(n.filter(r => r.label || r.value)) }} className="grow" />
          </div>
        ))}
      </div>
    </Field>
  )
}

/** "Paste the booking email": AI fills in the pass. */
function ReadEmail({ onDraft, busy, setBusy }: { onDraft: (d: Partial<Form>) => void; busy: boolean; setBusy: (b: boolean) => void }) {
  const { toast } = useToast()
  const [open, setOpen] = useState(false)
  const [text, setText] = useState('')
  const [ai, setAI] = useState<{ ready: boolean; detail: string } | null>(null)
  useEffect(() => { if (open && !ai) api.get<{ ready: boolean; detail: string }>(`${V1}/ai/status`).then(setAI).catch(() => {}) }, [open, ai])
  const read = async () => {
    setBusy(true)
    try {
      const r = await api.post<{ ok: boolean; error?: string; draft?: WalletCard & { note: string } }>(`${V1}/ai/read`, { text })
      if (!r.ok || !r.draft) { toast(r.error || 'Nothing could be read', 'err'); return }
      const d = r.draft
      onDraft({ kind: d.kind, name: d.name, issuer: d.issuer, number: d.number, holder: d.holder, day: d.starts_at.slice(0, 10), time: d.starts_at.slice(11, 16),
        end_day: d.ends_at.slice(0, 10), end_time: d.ends_at.slice(11, 16), venue: d.venue, destination: d.destination, seat: d.seat, gate: d.gate, ref: d.ref,
        expires_on: d.expires_on, code_value: d.code_value, code_format: d.code_value ? 'qr' : '', fields: d.fields || [], note: d.note || '' })
      setOpen(false)
      toast('Filled in from the email: check it before you add it', 'ok')
    } catch (e) { toast((e as Error).message, 'err') } finally { setBusy(false) }
  }
  if (!open) return <button type="button" className="callout info read-email" onClick={() => setOpen(true)}><Icon name="sparkles" size={15} /><span><b>Paste the booking email</b> and AI fills this in.</span></button>
  return (
    <div className="read-email-box">
      <Field label="The booking email or the ticket's text" help="Only this text goes to the AI.">
        <textarea rows={6} value={text} onChange={e => setText(e.target.value)} autoFocus placeholder="Your booking confirmation… Flight OS 784, Sarajevo (SJJ) 06:00 → Vienna (VIE) 07:25…" />
      </Field>
      {ai && !ai.ready && <p className="bad small">No AI is connected{ai.detail ? ` (${ai.detail})` : ''}: Settings → AI.</p>}
      <div className="row"><button type="button" className="btn primary sm" onClick={read} disabled={busy || text.trim().length < 20 || (ai !== null && !ai.ready)}>{busy ? <><Spinner size={12} /> Reading…</> : <><Icon name="sparkles" size={14} />Fill it in</>}</button><button type="button" className="btn sm ghost" onClick={() => setOpen(false)}>Cancel</button></div>
    </div>
  )
}
