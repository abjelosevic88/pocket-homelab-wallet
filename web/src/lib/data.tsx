// The kinds of cards and barcodes, and how many passes are coming up,
// loaded once for every form and the sidebar; any change in the app
// ("pwc:changed") reloads them.
import React, { createContext, useCallback, useContext, useEffect, useState } from 'react'
import { api, V1 } from './api'
import type { CodeFormat, Kind, Overview } from './types'

interface Data { kinds: Kind[]; formats: CodeFormat[]; counts: Overview['counts'] | null; reload: () => void }
const Ctx = createContext<Data>({ kinds: [], formats: [], counts: null, reload: () => {} })
export const useData = () => useContext(Ctx)

/** Something was added or changed: counts and pages reload. */
export const announceChange = () => window.dispatchEvent(new CustomEvent('pwc:changed'))
export function useOnChange(cb: () => void) {
  const ref = React.useRef(cb)
  ref.current = cb
  useEffect(() => {
    const h = () => ref.current()
    window.addEventListener('pwc:changed', h)
    return () => window.removeEventListener('pwc:changed', h)
  }, [])
}

export function DataProvider({ children }: { children: React.ReactNode }) {
  const [kinds, setKinds] = useState<Kind[]>([])
  const [formats, setFormats] = useState<CodeFormat[]>([])
  const [counts, setCounts] = useState<Overview['counts'] | null>(null)
  const reload = useCallback(() => {
    api.get<Overview>(`${V1}/overview`).then(o => setCounts(o.counts)).catch(() => {})
  }, [])
  useEffect(() => {
    api.get<{ kinds: Kind[]; formats: CodeFormat[] }>(`${V1}/kinds`).then(r => { setKinds(r.kinds); setFormats(r.formats) }).catch(() => {})
    reload()
  }, [reload])
  useOnChange(reload)
  return <Ctx.Provider value={{ kinds, formats, counts, reload }}>{children}</Ctx.Provider>
}
