package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Wallet as
// the target: another Pocket app the person allowed in Core may act here for
// them with a pcx_ token, only within these scopes. Every route not listed
// in linkRoutes stays closed to it (403).

var linkOffers = []core.Offer{
	{Scope: "wallet:read", Name: "See your wallet", Description: "Read your loyalty and gift cards, tickets, boarding passes and IDs: names, numbers, barcodes, dates, seats and when they run out — for your trips, your journal, your renewals, dashboards and Pocket AI."},
	{Scope: "wallet:write", Name: "Add cards and passes", Description: "Add a card, ticket or boarding pass (also from a .pkpass file) and spend from a gift card. It may change or remove only what that app added itself."},
}

// portable is what Pocket Move may copy out and bring in for a person
// (docs/move.md in pocket-homelab-core); Register adds move:export/import.
// Both exports hold the person's own card numbers and codes, so they only
// ever answer for the person the link acts for.
var portable = &core.Portable{
	Exports: []core.PortableExport{
		{Path: "/api/v1/export.json", File: "wallet.json", Kind: core.KindPocket, Title: "Your cards, passes and IDs with their barcodes, pictures and gift card history"},
		{Path: "/api/v1/export.csv", File: "wallet.csv", Kind: core.KindCSV, Title: "Your cards, passes and IDs as a table (no pictures)"},
	},
	Imports: []core.PortableImport{
		{Path: "/api/v1/import", Kind: core.KindPocket, ContentType: "application/json", MaxBytes: 60 << 20, Title: "Adds the cards, passes and IDs from another box; cards you already have are left alone"},
		{Path: "/api/v1/import", Kind: core.KindCSV, ContentType: "text/csv", MaxBytes: 60 << 20, Title: "Adds cards from a table with a name column; rows you already have are skipped"},
		{Path: "/api/v1/import", Kind: "catima", ContentType: "application/zip", MaxBytes: 60 << 20, Title: "Adds the cards from a Catima export, with their pictures and groups"},
		{Path: "/api/v1/import/pkpass", Kind: "pkpass", ContentType: "application/vnd.apple.pkpass", MaxBytes: 20 << 20, Title: "Adds an Apple Wallet pass (.pkpass or .pkpasses); a pass you already have is updated, not doubled"},
	},
}

// capabilities are what Pocket Search, Pocket Day and Pocket Hub may ask
// (docs/capabilities.md in pocket-homelab-core); Register adds the pocket:*
// offers, each opens its one route (capabilities.go). No quick capture: a
// card or a pass needs its form, and a gift card spend needs its card.
var capabilities = core.Capabilities{
	Search: &core.Searchable{What: "Your cards, passes and IDs, by name, shop or issuer, kind, place and notes (never by their numbers)", Kinds: searchKinds(), Bangs: []string{"wallet", "card"}},
	Day:    &core.DayFeed{Label: "Wallet", One: "pass", Many: "passes", What: "The tickets and boarding passes for that day, the cards and IDs that expire that day and what you spent from gift cards"},
	Widget: &core.Widget{Title: "Wallet", What: "Today's and tomorrow's tickets and boarding passes, the cards and IDs that expire in the next 60 days and what is left on your gift cards"},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(overview|summary|cards|passes|expiring|search|tags|kinds|day/\d{4}-\d{2}-\d{2})$`), "wallet:read"},
	{"GET", regexp.MustCompile(`^/api/v1/cards/\d+(/code\.svg|/balance|/images/(front|back|logo))?$`), "wallet:read"},
	{"POST", regexp.MustCompile(`^/api/v1/(cards|import/pkpass)$`), "wallet:write"},
	{"PUT DELETE", regexp.MustCompile(`^/api/v1/cards/\d+$`), "wallet:write"},
	{"POST", regexp.MustCompile(`^/api/v1/cards/\d+/balance$`), "wallet:write"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	if portable.LinkAllows(scopes, r) || capabilities.LinkAllows(scopes, r) {
		return true
	}
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.Audience != "wallet" || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}
