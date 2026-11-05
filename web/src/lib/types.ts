export interface User { id: number; email: string; name: string; role: 'admin' | 'member'; created_at: string }
export interface APIToken { id: number; user_id: number; name: string; prefix: string; last_used_at: string | null; created_at: string }

export type ConnMode = 'core' | 'direct' | 'off'
export type ConnVia = '' | 'core' | 'direct'
export type AppSlug = 'travel' | 'calendar' | 'email' | 'money'

type ConnFields = { [K in AppSlug as `${K}_mode`]: ConnMode } & { [K in AppSlug as `${K}_via`]: ConnVia } & { [K in AppSlug as `${K}_detail` | `${K}_url` | `${K}_token` | `${K}_public_url` | `${K}_open_url`]: string } & { [K in AppSlug as `${K}_configured`]: boolean }

export type Settings = ConnFields & {
  core_configured: boolean
  core_registered?: boolean
  title: string; date_format: string; time_format: string; week_start: number; currency: string
  sort: 'used' | 'name' | 'recent'; hide_numbers: boolean
  notify_passes: boolean; notify_before: number; notify_expiry: boolean; expiry_days: number; notify_hour: number
  notify_url: string; notify_format: string; notify_configured: boolean
  travel_trips: boolean; calendar_passes: boolean; calendar_id: number
  mail_scan: boolean; mail_days: number; money_spend: boolean
  ai_provider: 'auto' | 'pocket' | 'openai'; ai_pocket_model: string; openai_api_key: string; openai_model: string; openai_base_url: string
  setup_complete: boolean; base_url: string
}

export type Group = 'cards' | 'passes' | 'ids'

export interface Kind { key: string; label: string; group: Group; icon: string; hint: string }
export interface CodeFormat { key: string; label: string; two_d: boolean; hint: string }
export interface Field { label: string; value: string }

export interface WalletCard {
  id: number; kind: string; group: Group; name: string; issuer: string; number: string; holder: string
  code_format: string; code_value: string; color: string; note: string; tags: string; fields: Field[]
  pinned: boolean; archived: boolean; valid_from: string; expires_on: string
  starts_at: string; ends_at: string; venue: string; destination: string; seat: string; gate: string; ref: string
  balance: number | null; currency: string; points: number | null
  trip_id: number | null; trip_name: string; cal_event_id: number | null
  front_image: string; back_image: string; logo_image: string
  uses: number; last_used_at: string; source: string; source_ref: string; created_at: string; updated_at: string
}

export interface SoonCard extends WalletCard { days_left: number }

export interface BalanceEntry { id: number; card_id: number; delta: number; balance: number; note: string; created_at: string }

export interface Overview {
  cards: WalletCard[]; ids: WalletCard[]; next: WalletCard[]; expiring: SoonCard[]
  counts: { cards: number; passes: number; ids: number; archived: number; upcoming: number }
  today: string; currency: string
}

export interface Trip { id: number; name: string; destination: string; start_date: string; end_date: string }
export interface CalendarItem { id: number; name: string; color: string; writable: boolean; is_default: boolean }
export interface Picked<T> { connected: boolean; detail?: string; items: T[] }
export interface Spent { connected: boolean; detail?: string; count?: number; last?: string; since?: string; sums?: { amount: number; currency: string }[]; open_url?: string }
export interface MailResult { connected: boolean; detail?: string; seen: number; added: WalletCard[]; updated: number }
