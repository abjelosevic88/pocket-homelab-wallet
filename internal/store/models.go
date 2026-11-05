package store

import "strings"

// User is an account on this box.
type User struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"` // admin | member
	CreatedAt    string `json:"created_at"`
}

// APIToken is a personal access token (hash stored, plaintext shown once).
type APIToken struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"user_id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"`
	LastUsedAt *string `json:"last_used_at"`
	CreatedAt  string  `json:"created_at"`
}

// Card is one thing in the wallet: a loyalty card, a gift card, a ticket, a
// boarding pass, an ID card, a passport. Kind decides which fields matter.
type Card struct {
	ID          int64   `json:"id"`
	UserID      int64   `json:"-"`
	Kind        string  `json:"kind"`
	Name        string  `json:"name"`
	Issuer      string  `json:"issuer"`
	Number      string  `json:"number"`
	Holder      string  `json:"holder"`
	CodeFormat  string  `json:"code_format"`
	CodeValue   string  `json:"code_value"`
	Color       string  `json:"color"`
	Note        string  `json:"note"`
	Tags        string  `json:"tags"`
	Fields      []Field `json:"fields"`
	Pinned      bool    `json:"pinned"`
	Archived    bool    `json:"archived"`
	ValidFrom   string  `json:"valid_from"`
	ExpiresOn   string  `json:"expires_on"`
	StartsAt    string  `json:"starts_at"`
	EndsAt      string  `json:"ends_at"`
	Venue       string  `json:"venue"`
	Destination string  `json:"destination"`
	Seat        string  `json:"seat"`
	Gate        string  `json:"gate"`
	Ref         string  `json:"ref"`
	Balance     *int64  `json:"balance"`
	Currency    string  `json:"currency"`
	Points      *int64  `json:"points"`
	TripID      *int64  `json:"trip_id"`
	TripName    string  `json:"trip_name"`
	CalEventID  *int64  `json:"cal_event_id"`
	FrontImage  string  `json:"front_image"`
	BackImage   string  `json:"back_image"`
	LogoImage   string  `json:"logo_image"`
	Uses        int     `json:"uses"`
	LastUsedAt  string  `json:"last_used_at"`
	Source      string  `json:"source"`
	SourceRef   string  `json:"source_ref"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`

	// Filled on the way out.
	Group string `json:"group"` // cards | passes | ids
}

// Field is one more detail printed on a card or pass ("Terminal: 2").
type Field struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Day is the date a pass is for (YYYY-MM-DD), "" when it has no time.
func (c Card) Day() string {
	if len(c.StartsAt) >= 10 {
		return c.StartsAt[:10]
	}
	return ""
}

// BalanceEntry is money spent from or added to a gift card.
type BalanceEntry struct {
	ID        int64  `json:"id"`
	CardID    int64  `json:"card_id"`
	Delta     int64  `json:"delta"`
	Balance   int64  `json:"balance"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

// Conn is how a person reaches one sibling Pocket app: "core" (an app link
// through Pocket Core), "direct" (URL + token) or "off"; "" = not chosen yet.
type Conn struct {
	Mode      string `json:"mode"`
	URL       string `json:"url"`
	Token     string `json:"token"`
	PublicURL string `json:"public_url"`
}

// Settings are the box-wide values plus one person's preferences and their
// own connections.
type Settings struct {
	Title         string `json:"title"`
	SetupComplete bool   `json:"setup_complete"`

	DateFormat  string `json:"date_format"`
	TimeFormat  string `json:"time_format"`
	WeekStart   int    `json:"week_start"` // 1 = Monday, 7 = Sunday
	Currency    string `json:"currency"`
	Sort        string `json:"sort"`         // cards: used | name | recent
	HideNumbers bool   `json:"hide_numbers"` // ID and passport numbers stay hidden until tapped

	// Notifications.
	NotifyPasses bool   `json:"notify_passes"` // the morning of a ticket or flight
	NotifyBefore int    `json:"notify_before"` // minutes before it starts: "your pass is ready" (0 = no)
	NotifyExpiry bool   `json:"notify_expiry"` // an ID or card runs out soon
	ExpiryDays   int    `json:"expiry_days"`   // how soon
	NotifyHour   int    `json:"notify_hour"`
	NotifyURL    string `json:"notify_url"`
	NotifyFormat string `json:"notify_format"` // apprise | ntfy | json

	// Pocket Travel: passes during a trip are filed with it.
	TravelTrips bool `json:"travel_trips"`

	// Pocket Calendar: tickets and flights as events.
	CalendarPasses bool  `json:"calendar_passes"`
	CalendarID     int64 `json:"calendar_id"` // 0 = the person's default calendar

	// Pocket Email: passes (.pkpass) that arrive by email.
	MailScan bool `json:"mail_scan"`
	MailDays int  `json:"mail_days"` // how far back to look

	// Pocket Money: what you spent at the shop of a loyalty card.
	MoneySpend bool `json:"money_spend"`

	// Each sibling Pocket app, by slug.
	Conns map[string]Conn `json:"conns"`

	// AI for reading a booking email into a pass: Pocket AI through Core, or an
	// OpenAI-compatible API with the person's own key.
	AIProvider    string `json:"ai_provider"` // auto | pocket | openai
	AIPocketModel string `json:"ai_pocket_model"`
	OpenAIKey     string `json:"openai_api_key"`
	OpenAIModel   string `json:"openai_model"`
	OpenAIBaseURL string `json:"openai_base_url"`
}

// SplitTags turns "a, b ,c" into clean lowercase tags.
func SplitTags(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(s, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// JoinTags is the stored form of tags.
func JoinTags(s string) string { return strings.Join(SplitTags(s), ", ") }
