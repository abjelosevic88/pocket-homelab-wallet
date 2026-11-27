package server

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// What Pocket Search, Pocket Day and Pocket Hub ask Wallet (the capabilities
// value in links.go): one route each, in the shape every Pocket app answers
// in. They read what the wallet's own pages read, as the request's person,
// and change nothing.
//
// None of these answers ever carries a card or document number, a barcode,
// a booking reference, a PIN or a security code: only names, kinds, issuers,
// places, dates and gift card balances, as the app's own lists show them.

// kindWords name a card's kind in plain words, as people say it.
var kindWords = map[string]string{
	"loyalty": "loyalty card", "membership": "membership card", "gift": "gift card", "ticket": "ticket", "boarding": "boarding pass",
	"id": "ID card", "passport": "passport", "licence": "driving licence", "insurance": "insurance card", "other": "card",
}

// searchKinds are the kinds a search answer can carry, in the add dialog's order.
func searchKinds() []string {
	out := make([]string, 0, len(store.Kinds))
	for _, k := range store.Kinds {
		out = append(out, kindWords[k.Key])
	}
	return out
}

func kindWord(k string) string {
	if w, ok := kindWords[k]; ok {
		return w
	}
	return "card"
}

// capital starts a line with a capital letter ("ID card" stays as it is).
func capital(s string) string {
	for i, r := range s {
		return string(unicode.ToUpper(r)) + s[i+len(string(r)):]
	}
	return s
}

func cardKey(c store.Card) string { return itoa(c.ID) }

// cardURL opens a card, pass or ID in the app.
func cardURL(c store.Card) string { return "/cards/" + cardKey(c) }

// dots joins the parts of a line that are there: "Konzum · Expires 3 Mar 2027".
func dots(parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// count is "1 pass", "3 passes".
func count(n int, one, many string) string {
	return strconv.Itoa(n) + " " + plural(n, one, many)
}

// money is minor units of a currency for people: 3800 BAM → "38.00 BAM".
func money(minor int64, cur string) string {
	sign := ""
	if minor < 0 {
		sign, minor = "-", -minor
	}
	return strings.TrimSpace(fmt.Sprintf("%s%d.%02d %s", sign, minor/100, minor%100, cur))
}

// longDate is "3 Mar 2027".
func longDate(day string) string {
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return day
	}
	return t.Format("2 Jan 2006")
}

// passStart is a pass's start as the person reads it: "30 Nov 2026 20:00", or
// the day alone when it has no time.
func passStart(c store.Card) string {
	day := c.Day()
	if day == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(t.Format("2 Jan 2006") + " " + clock(c.StartsAt))
}

// clock is the "HH:MM" of a "YYYY-MM-DDTHH:MM" time, "" without one.
func clock(at string) string {
	if len(at) >= 16 {
		return at[11:16]
	}
	return ""
}

// place is where a pass is for: the venue, or "from → to" for a trip.
func place(c store.Card) string {
	switch {
	case c.Venue != "" && c.Destination != "":
		return c.Venue + " → " + c.Destination
	case c.Destination != "":
		return c.Destination
	}
	return c.Venue
}

// cardMoney is a gift card's balance in its own currency (or the person's).
func cardMoney(c store.Card, fallback string) string {
	if c.Kind != "gift" || c.Balance == nil {
		return ""
	}
	return money(*c.Balance, firstNonEmpty(c.Currency, fallback)) + " left"
}

// expiresWords is how long a card or paper has: "expires in 12 days".
func expiresWords(left int) string {
	switch {
	case left < -1:
		return fmt.Sprintf("expired %d days ago", -left)
	case left == -1:
		return "expired yesterday"
	case left == 0:
		return "expires today"
	case left == 1:
		return "expires tomorrow"
	}
	return fmt.Sprintf("expires in %d days", left)
}

// ---------- search ----------

// searchWords are the words of a query: letters and digits only, lower case.
func searchWords(q string) []string {
	return strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// pocketSearch finds the person's cards, passes and IDs by their name, the
// shop or issuer, the kind, the place, the tags and the notes. Every word
// must be found somewhere. Unlike the app's own search it never matches a
// card number, barcode or booking reference, so an answer cannot give one
// away. The archive is searched too, after what is in use.
func (s *Server) pocketSearch(r *http.Request, q string, limit int) ([]core.SearchItem, error) {
	words := searchWords(q)
	if len(words) == 0 {
		return nil, nil
	}
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	cs, err := s.store.ListCards(ctx, uid, store.CardFilter{Archived: "all", Sort: "used"})
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := []core.SearchItem{}
	for _, c := range cs {
		name, issuer := strings.ToLower(c.Name), strings.ToLower(c.Issuer)
		kind := strings.ToLower(kindWord(c.Kind) + " " + store.KindLabel(c.Kind))
		rest := strings.ToLower(strings.Join([]string{c.Venue, c.Destination, c.TripName, c.Tags, c.Note}, " "))
		score, all := 0.0, true
		for _, w := range words {
			switch {
			case strings.Contains(name, w):
				score += 3
			case strings.Contains(issuer, w):
				score += 2
			case strings.Contains(kind, w):
				score++
			case strings.Contains(rest, w):
				score += 0.5
			default:
				all = false
			}
			if !all {
				break
			}
		}
		if !all {
			continue
		}
		if strings.HasPrefix(name, strings.Join(words, " ")) {
			score++
		}
		if c.Archived {
			score /= 4
		}
		it := core.SearchItem{ID: cardKey(c), Title: c.Name, Kind: kindWord(c.Kind), URL: cardURL(c), Score: score, Date: c.UpdatedAt}
		var when string
		if c.Group == store.GroupPasses {
			when = dots(passStart(c), place(c))
			if d := c.Day(); d != "" {
				it.Date = d
			}
		}
		var expires string
		if c.ExpiresOn != "" {
			if left := daysLeft(c.ExpiresOn, now); left < 0 {
				expires = "Expired " + longDate(c.ExpiresOn)
			} else {
				expires = "Expires " + longDate(c.ExpiresOn)
			}
		}
		issuerLine := c.Issuer
		if strings.EqualFold(c.Issuer, c.Name) {
			issuerLine = ""
		}
		archived := ""
		if c.Archived {
			archived = "In the archive"
		}
		it.Snippet = dots(issuerLine, when, expires, cardMoney(c, st.Currency), archived)
		out = append(out, it)
	}
	// the best match first; the app's own order (most used) among equals
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------- day ----------

// pocketDay is the wallet's part of a day: the tickets and boarding passes
// for it (the same passes as GET /day/{date}), the cards and papers that
// expire that day, and what was spent from gift cards that day. Pass times
// are the wall-clock times on the pass; a spend's time is in the caller's
// time zone when it said one, else the server's.
func (s *Server) pocketDay(r *http.Request, date string, tz *time.Location) (*core.Day, error) {
	ctx := r.Context()
	uid := actorID(ctx)
	loc := time.Local
	if tz != nil {
		loc = tz
	}
	from, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil, nil
	}
	to := from.AddDate(0, 0, 1)
	st, _ := s.store.GetSettings(ctx, uid)
	day := &core.Day{Date: date}

	passes, err := s.store.ListCards(ctx, uid, store.CardFilter{Group: store.GroupPasses, From: date, To: date, Archived: "all", Sort: "starts"})
	if err != nil {
		return nil, err
	}
	for _, c := range passes {
		it := core.DayItem{ID: "pass:" + cardKey(c), Time: clock(c.StartsAt), Title: c.Name, Sub: place(c), Kind: kindWord(c.Kind), URL: cardURL(c)}
		if len(c.EndsAt) >= 16 && c.EndsAt[:10] == date {
			it.End = clock(c.EndsAt)
		}
		day.Items = append(day.Items, it)
	}

	expiring, err := s.store.ListCards(ctx, uid, store.CardFilter{ExpiresOn: date, Archived: "all", Sort: "name"})
	if err != nil {
		return nil, err
	}
	for _, c := range expiring {
		issuer := c.Issuer
		if strings.EqualFold(issuer, c.Name) {
			issuer = ""
		}
		day.Items = append(day.Items, core.DayItem{ID: "expires:" + cardKey(c), Title: c.Name, Sub: dots(capital(kindWord(c.Kind)), issuer), Kind: "expires", URL: cardURL(c)})
	}

	const utc = "2006-01-02T15:04:05"
	log, err := s.store.BalanceLogBetween(ctx, uid, from.UTC().Format(utc), to.UTC().Format(utc))
	if err != nil {
		return nil, err
	}
	cards := map[int64]*store.Card{}
	spent := map[string]int64{}
	var currencies []string
	nSpends := 0
	for _, e := range log {
		at, err := time.Parse(time.RFC3339Nano, e.CreatedAt)
		if err != nil || e.Delta >= 0 || at.Before(from) || !at.Before(to) {
			continue
		}
		c, ok := cards[e.CardID]
		if !ok {
			c, _ = s.store.GetCard(ctx, uid, e.CardID)
			cards[e.CardID] = c
		}
		if c == nil {
			continue
		}
		cur := firstNonEmpty(c.Currency, st.Currency)
		if _, ok := spent[cur]; !ok {
			currencies = append(currencies, cur)
		}
		spent[cur] += -e.Delta
		nSpends++
		day.Items = append(day.Items, core.DayItem{ID: "spend:" + itoa(e.ID), Time: at.In(loc).Format("15:04"), Title: c.Name,
			Sub: dots(e.Note, money(e.Balance, cur)+" left"), Kind: "spent", Amount: -e.Delta, Currency: cur, URL: cardURL(*c)})
	}

	if len(day.Items) == 0 {
		return nil, nil
	}
	var parts []string
	if len(passes) > 0 {
		parts = append(parts, count(len(passes), "pass", "passes"))
	}
	switch len(expiring) {
	case 0:
	case 1:
		parts = append(parts, expiring[0].Name+" expires")
	default:
		parts = append(parts, strconv.Itoa(len(expiring))+" cards and papers expire")
	}
	if nSpends > 0 {
		which := "from a gift card"
		if len(cards) > 1 {
			which = "from gift cards"
		}
		if len(currencies) == 1 {
			parts = append(parts, money(spent[currencies[0]], currencies[0])+" spent "+which)
		} else {
			parts = append(parts, count(nSpends, "gift card spend", "gift card spends"))
		}
	}
	day.Summary = capital(strings.Join(parts, ", "))
	return day, nil
}

// ---------- widget ----------

// widgetSoonDays is how far ahead the widget looks for what runs out.
const widgetSoonDays = 60

// pocketWidget is what is coming up, as the home page has it: today's and
// tomorrow's tickets and boarding passes first, then the cards and papers
// that expire in the next 60 days, then what is left on gift cards. Someone
// with nothing in the wallet gets nothing to show.
func (s *Server) pocketWidget(r *http.Request) (*core.WidgetData, error) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	now := time.Now()
	today := now.Format("2006-01-02")
	tomorrow := store.AddDays(today, 1)
	const rows = 8

	upcoming := s.upcoming(ctx, uid, now, 0)
	soon, err := s.store.ListCards(ctx, uid, store.CardFilter{Expiring: store.AddDays(today, widgetSoonDays), Sort: "expires"})
	if err != nil {
		return nil, err
	}
	gifts, err := s.store.ListCards(ctx, uid, store.CardFilter{Kind: "gift", Sort: st.Sort})
	if err != nil {
		return nil, err
	}
	if len(upcoming) == 0 && len(soon) == 0 && len(gifts) == 0 {
		if one, err := s.store.ListCards(ctx, uid, store.CardFilter{Limit: 1}); err != nil || len(one) == 0 {
			return nil, err
		}
	}

	w := &core.WidgetData{URL: "/"}
	seen := map[int64]bool{}
	add := func(c store.Card, detail string) {
		if seen[c.ID] || len(w.Items) == rows {
			return
		}
		seen[c.ID] = true
		w.Items = append(w.Items, core.WidgetItem{ID: cardKey(c), Title: c.Name, Detail: detail, URL: cardURL(c)})
	}

	for _, c := range upcoming {
		var when string
		switch c.Day() {
		case today:
			when = "today"
		case tomorrow:
			when = "tomorrow"
		default:
			continue
		}
		if w.Line == "" {
			w.Line = strings.TrimSpace(capital(kindWord(c.Kind)) + " for " + when + " " + clock(c.StartsAt))
		}
		add(c, dots(strings.TrimSpace(capital(when)+" "+clock(c.StartsAt)), place(c)))
	}
	nSoon := 0
	for _, c := range soon {
		left := daysLeft(c.ExpiresOn, now)
		if left < 0 {
			continue
		}
		nSoon++
		if w.Line == "" {
			w.Line = c.Name + " " + expiresWords(left)
		}
		add(c, capital(expiresWords(left)))
	}
	nGifts := 0
	for _, c := range gifts {
		if c.Balance == nil || *c.Balance <= 0 {
			continue
		}
		nGifts++
		add(c, cardMoney(c, st.Currency))
	}

	w.Metrics = []core.Metric{{Label: "Passes coming up", Value: strconv.Itoa(len(upcoming))}, {Label: "Expire within 60 days", Value: strconv.Itoa(nSoon)}}
	if nGifts > 0 {
		w.Metrics = append(w.Metrics, core.Metric{Label: "Gift cards", Value: strconv.Itoa(nGifts)})
	}
	return w, nil
}
