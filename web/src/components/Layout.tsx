// The app shell: sidebar, top bar, search palette, shortcuts — the Pocket Calendar layout.
import { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { Outlet, useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { api, sendFile, V1 } from '../lib/api'
import type { WalletCard } from '../lib/types'
import { useApp } from '../lib/app-context'
import { DataProvider } from '../lib/data'
import { Icon } from './icons'
import { Sidebar } from './Sidebar'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { ShortcutsSheet } from './ShortcutsSheet'
import { CardModal } from './CardForm'
import { Avatar, type Anchor, type MenuItem, Menu, MenuButton, modalOpen, rectOf, useIsMobile, useMediaQuery, useToast } from './ui'
import { AskButton } from './AskButton'

import { announceChange } from '../lib/data'
export { announceChange, useOnChange } from '../lib/data'

const SIDEBAR_KEY = 'pwc-sidebar'

/** The page's own name for the top bar breadcrumb. */
const CrumbCtx = createContext<(t: string, section?: string) => void>(() => {})
/** The page's name in the top bar, under a section other than the path's when given ("passes"). */
export function useCrumb(title: string | undefined, section?: string) {
  const set = useContext(CrumbCtx)
  useEffect(() => { set(title || '', section); return () => set('') }, [title, section, set])
}

/** What can be added: a kind of card, a .pkpass file, or a card by scanning it first. */
export type CreateKind = string

/** Open a create dialog from anywhere (a page's button). */
export const openCreate = (k: CreateKind) => window.dispatchEvent(new CustomEvent('pwc:create', { detail: { kind: k } }))

/** The add menu: the sidebar's "Add" button and the phone's + button. */
export function createItems(onCreate: (k: CreateKind) => void): MenuItem[] {
  return [
    { label: 'Scan a card', icon: 'camera', hint: 'S', onSelect: () => onCreate('scan') },
    { label: 'A card, by hand', icon: 'card', hint: 'A', onSelect: () => onCreate('pick') },
    { label: 'A ticket or boarding pass', icon: 'ticket', hint: 'T', onSelect: () => onCreate('ticket') },
    { label: 'An ID or passport', icon: 'id-card', hint: 'I', onSelect: () => onCreate('id') },
    'sep',
    { label: 'A .pkpass file', icon: 'upload', onSelect: () => onCreate('pkpass') },
  ]
}

export default function Layout() {
  return <DataProvider><Shell /></DataProvider>
}

function Shell() {
  const navigate = useNavigate()
  const loc = useLocation()
  const [sp, setSp] = useSearchParams()
  const { toast } = useToast()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const drawerOpenedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { drawerOpenedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const closeDrawer = () => { if (drawer) setSidebarOpen(false) }
  useEffect(() => { document.querySelector('.content')?.scrollTo({ top: 0 }) }, [loc.pathname])

  const [crumb, setCrumbRaw] = useState('')
  const [crumbSection, setCrumbSection] = useState('')
  const setCrumb = useCallback((t: string, section?: string) => { setCrumbRaw(t); setCrumbSection(section || '') }, [])

  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  const [modal, setModal] = useState<{ kind: CreateKind } | null>(null)
  const passFile = useRef<HTMLInputElement>(null)
  const [fabMenu, setFabMenu] = useState<Anchor | null>(null)

  const create = (k: CreateKind) => {
    closeDrawer()
    if (k === 'pkpass') { passFile.current?.click(); return }
    setModal({ kind: k })
  }
  const importPasses = async (files: FileList) => {
    let last: WalletCard | null = null, added = 0, updated = 0
    for (const f of Array.from(files)) {
      try {
        const r = await sendFile<{ cards: WalletCard[]; added: number; updated: number }>('POST', `${V1}/import/pkpass`, f, 'application/vnd.apple.pkpass')
        added += r.added; updated += r.updated; last = r.cards[r.cards.length - 1] || last
      } catch (e) { toast(`${f.name}: ${(e as Error).message}`, 'err') }
    }
    if (added + updated > 0) {
      toast(added ? `${added} added${updated ? `, ${updated} brought up to date` : ''}` : `${updated} brought up to date`, 'ok')
      announceChange()
      if (last) navigate(`/cards/${last.id}`)
    }
  }
  const createRef = useRef(create)
  createRef.current = create

  // /?add=card (the PWA shortcut) opens a dialog
  useEffect(() => { const a = sp.get('add'); if (a) { createRef.current(a === 'card' ? 'pick' : a); setSp(new URLSearchParams(), { replace: true }) } }, []) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const open = () => setShortcuts(true)
    const make = (e: Event) => { const d = (e as CustomEvent<{ kind: CreateKind }>).detail; createRef.current(d.kind) }
    window.addEventListener('pwc:shortcuts', open)
    window.addEventListener('pwc:create', make)
    return () => { window.removeEventListener('pwc:shortcuts', open); window.removeEventListener('pwc:create', make) }
  }, [])
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); setPalette(p => !p); return }
      if (e.metaKey || e.ctrlKey || e.altKey) return
      if (t.closest('input, textarea, select, [contenteditable="true"]') || modalOpen() || document.querySelector('.menu, .popover, .sel-pop')) return
      const go = (to: string) => { e.preventDefault(); navigate(to) }
      const mk = (k: CreateKind) => { e.preventDefault(); createRef.current(k) }
      switch (e.key) {
        case '/': e.preventDefault(); setPalette(true); break
        case '?': e.preventDefault(); setShortcuts(true); break
        case 's': mk('scan'); break
        case 'a': mk('pick'); break
        case 't': mk('ticket'); break
        case 'i': mk('id'); break
        case 'h': go('/'); break
        case 'c': go('/cards'); break
        case 'p': go('/passes'); break
        case 'd': go('/ids'); break
        case ',': go('/settings'); break
        case '[': e.preventDefault(); toggleSidebar(); break
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }) // eslint-disable-line react-hooks/exhaustive-deps

  const commands: PaletteCommand[] = [
    { id: 'scan', label: 'Scan a card', icon: 'camera', hint: 'S', keywords: 'new add loyalty barcode camera', run: () => create('scan') },
    { id: 'card', label: 'Add a card by hand', icon: 'card', hint: 'A', keywords: 'new loyalty membership gift voucher', run: () => create('pick') },
    { id: 'ticket', label: 'Add a ticket or boarding pass', icon: 'ticket', hint: 'T', keywords: 'flight concert train event pass', run: () => create('ticket') },
    { id: 'id', label: 'Add an ID or passport', icon: 'id-card', hint: 'I', keywords: 'document licence insurance', run: () => create('id') },
    { id: 'pkpass', label: 'Add a .pkpass file', icon: 'upload', keywords: 'apple wallet pass import', run: () => create('pkpass') },
    { id: 'home', label: 'Wallet', icon: 'home', hint: 'H', run: () => navigate('/') },
    { id: 'cards', label: 'Cards', icon: 'card', hint: 'C', keywords: 'loyalty shop gift', run: () => navigate('/cards') },
    { id: 'passes', label: 'Passes', icon: 'ticket', hint: 'P', keywords: 'tickets flights boarding upcoming', run: () => navigate('/passes') },
    { id: 'ids', label: 'IDs and documents', icon: 'id-card', hint: 'D', keywords: 'passport licence insurance expiry', run: () => navigate('/ids') },
    { id: 'archive', label: 'Archive', icon: 'archive', run: () => navigate('/archive') },
    { id: 'settings', label: 'Settings', icon: 'settings', hint: ',', run: () => navigate('/settings') },
    { id: 'apps', label: 'Settings: connected apps', icon: 'layers', keywords: 'travel calendar email money core', run: () => navigate('/settings/apps') },
    { id: 'import', label: 'Move in from Catima', icon: 'download', keywords: 'import csv stocard export', run: () => navigate('/settings/data') },
    { id: 'notify', label: 'Settings: notifications', icon: 'bell', keywords: 'push reminder expiry', run: () => navigate('/settings/notifications') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  const close = () => setModal(null)
  return (
    <CrumbCtx.Provider value={setCrumb}>
      <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
        {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - drawerOpenedAt.current > 400) setSidebarOpen(false) }} />}
        <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
          <Sidebar onCreate={create} onNavigate={closeDrawer} />
        </div>
        <div className="main">
          <TopBar onMenu={toggleSidebar} onPalette={() => setPalette(true)} mobile={mobile} compact={narrow} sidebarOpen={sidebarOpen} crumb={crumb} crumbSection={crumbSection} />
          <main className="content"><div className="content-inner"><Outlet /></div></main>
        </div>
        {mobile && <button type="button" className="fab" aria-label="Add" title="Add" aria-haspopup="menu" aria-expanded={!!fabMenu} onClick={e => setFabMenu(fabMenu ? null : rectOf(e.currentTarget))}><Icon name="plus" size={24} strokeWidth={2.2} /></button>}
        {fabMenu && <Menu anchor={fabMenu} align="right" items={createItems(create)} onClose={() => setFabMenu(null)} width={250} />}
        {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} />}
        {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
        {modal && <CardModal kind={['pick', 'scan'].includes(modal.kind) ? (modal.kind === 'scan' ? 'loyalty' : '') : modal.kind} scanFirst={modal.kind === 'scan'} onClose={close} onSaved={c => navigate(`/cards/${c.id}`)} />}
        <input ref={passFile} type="file" accept=".pkpass,.pkpasses,application/vnd.apple.pkpass,application/vnd.apple.pkpasses" multiple hidden onChange={e => { const f = e.target.files; if (f?.length) importPasses(f); e.target.value = '' }} />
      </div>
    </CrumbCtx.Provider>
  )
}

const SECTION: Record<string, [string, string]> = { '': ['Wallet', '/'], cards: ['Cards', '/cards'], passes: ['Passes', '/passes'], ids: ['IDs and documents', '/ids'], archive: ['Archive', '/archive'] }

function TopBar({ onMenu, onPalette, mobile, compact, sidebarOpen, crumb, crumbSection }: { onMenu: () => void; onPalette: () => void; mobile: boolean; compact: boolean; sidebarOpen: boolean; crumb: string; crumbSection: string }) {
  const { user, themeMode, setThemeMode, status, setUser } = useApp()
  const navigate = useNavigate()
  const loc = useLocation()
  const [section, sectionTo] = SECTION[crumbSection || loc.pathname.split('/')[1] || ''] || ['', '/']
  const signOut = async () => { await api.post(`${V1}/auth/logout`).catch(() => {}); setUser(null); navigate('/login') }
  const avatarMenu = (): MenuItem[] => [
    { heading: user?.name || user?.email || '' },
    { label: 'Profile', icon: 'user', onSelect: () => navigate('/settings/profile') },
    { label: 'Settings', icon: 'settings', onSelect: () => navigate('/settings'), hint: ',' },
    { label: 'Keyboard shortcuts', icon: 'keyboard', onSelect: () => window.dispatchEvent(new CustomEvent('pwc:shortcuts')), hint: '?' },
    'sep',
    { heading: 'Appearance' },
    { label: 'System', checked: themeMode === 'system', onSelect: () => setThemeMode('system') },
    { label: 'Light', checked: themeMode === 'light', onSelect: () => setThemeMode('light') },
    { label: 'Dark', checked: themeMode === 'dark', onSelect: () => setThemeMode('dark') },
    ...(status?.auth_mode === 'local' ? ['sep' as const, { label: 'Sign out', icon: 'log-out', onSelect: signOut }] : []),
  ]
  return (
    <header className="topbar">
      <button type="button" className="btn ghost icon-only" onClick={onMenu} aria-label={sidebarOpen ? 'Hide the sidebar' : 'Show the sidebar'} title="Menu ([)"><Icon name={mobile || compact ? 'menu' : 'sidebar'} size={18} /></button>
      <h1 className="range-title">
        {crumb ? <><button type="button" className="crumb" onClick={() => navigate(sectionTo)}>{section}</button><Icon name="chevron-right" size={16} className="crumb-sep" /><span className="ellipsis">{crumb}</span></> : section}
      </h1>
      <span className="spacer" />
      <button type="button" className="btn ghost icon-only" onClick={onPalette} aria-label="Search" title="Search (⌘K or /)"><Icon name="search" size={18} /></button>
      <AskButton />
      <MenuButton label="Account" className="avatar-btn" align="right" width={220} items={avatarMenu}>
        <Avatar name={user?.name} email={user?.email} size={30} />
      </MenuButton>
    </header>
  )
}
