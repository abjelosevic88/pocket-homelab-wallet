package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// daysLeft is how many days remain until a YYYY-MM-DD date (negative = past).
func daysLeft(day string, now time.Time) int {
	t, err := time.ParseInLocation("2006-01-02", day, time.Local)
	if err != nil {
		return 0
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	return int(t.Sub(today).Hours() / 24)
}

// soonItem is something that runs out, with how long is left.
type soonItem struct {
	store.Card
	DaysLeft int `json:"days_left"`
}

func withDays(cs []store.Card, now time.Time) []soonItem {
	out := make([]soonItem, 0, len(cs))
	for _, c := range cs {
		out = append(out, soonItem{c, daysLeft(c.ExpiresOn, now)})
	}
	return out
}

func (s *Server) handleKinds(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"kinds": store.Kinds, "formats": codeFormats})
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.store.Tags(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "tags")
		return
	}
	writeJSON(w, http.StatusOK, tags)
}

// upcoming lists the passes from now on (a pass stays "up next" until two
// hours after it starts, or until it ends).
func (s *Server) upcoming(ctx context.Context, uid int64, now time.Time, limit int) []store.Card {
	cs, _ := s.store.ListCards(ctx, uid, store.CardFilter{Group: store.GroupPasses, From: now.AddDate(0, 0, -1).Format("2006-01-02"), Sort: "starts"})
	out := []store.Card{}
	for _, c := range cs {
		if passOver(c, now) {
			continue
		}
		out = append(out, c)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

// passOver: the event is behind us.
func passOver(c store.Card, now time.Time) bool {
	end := c.EndsAt
	if end == "" {
		end = c.StartsAt
	}
	if len(end) == 10 {
		return end < now.Format("2006-01-02")
	}
	t, err := time.ParseInLocation("2006-01-02T15:04", end, time.Local)
	if err != nil {
		return false
	}
	if c.EndsAt == "" {
		t = t.Add(2 * time.Hour)
	}
	return t.Before(now)
}

// handleOverview is the home page: pinned and most used cards, the passes
// coming up and what runs out soon.
func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	now := time.Now()
	cards, err := s.store.ListCards(ctx, uid, store.CardFilter{Group: store.GroupCards, Sort: st.Sort})
	if err != nil {
		s.fail(w, err, "overview")
		return
	}
	ids, _ := s.store.ListCards(ctx, uid, store.CardFilter{Group: store.GroupIDs, Sort: "expires"})
	soon, _ := s.store.ListCards(ctx, uid, store.CardFilter{Expiring: store.AddDays(store.Today(), maxInt(st.ExpiryDays, 60)), Sort: "expires"})
	next := s.upcoming(ctx, uid, now, 8)
	all, _ := s.store.ListCards(ctx, uid, store.CardFilter{Archived: "all"})
	archived := 0
	for _, c := range all {
		if c.Archived {
			archived++
		}
	}
	passes, _ := s.store.ListCards(ctx, uid, store.CardFilter{Group: store.GroupPasses})
	writeJSON(w, http.StatusOK, map[string]any{
		"cards": cards, "ids": ids, "next": next, "expiring": withDays(soon, now),
		"counts":   map[string]int{"cards": len(cards), "passes": len(passes), "ids": len(ids), "archived": archived, "upcoming": len(s.upcoming(ctx, uid, now, 0))},
		"today":    store.Today(),
		"currency": st.Currency,
	})
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// handleSummary is the short version for dashboards, the briefing and
// Pocket AI: counts, today's passes, the next one and what runs out soon.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	now := time.Now()
	today := store.Today()
	next := s.upcoming(ctx, uid, now, 0)
	todays := []store.Card{}
	var after *store.Card
	for i, c := range next {
		if c.Day() == today {
			todays = append(todays, c)
		} else if after == nil {
			after = &next[i]
		}
	}
	soon, _ := s.store.ListCards(ctx, uid, store.CardFilter{Expiring: store.AddDays(today, st.ExpiryDays), Sort: "expires"})
	counts := map[string]int{}
	all, _ := s.store.ListCards(ctx, uid, store.CardFilter{})
	for _, c := range all {
		counts[c.Group]++
	}
	gifts := []map[string]any{}
	for _, c := range all {
		if c.Kind == "gift" && c.Balance != nil && *c.Balance > 0 {
			gifts = append(gifts, map[string]any{"id": c.ID, "name": c.Name, "balance": *c.Balance, "currency": firstNonEmpty(c.Currency, st.Currency), "expires_on": c.ExpiresOn})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"date": today, "counts": counts, "upcoming": len(next), "today": brief(todays), "next": briefOne(after),
		"expiring": briefSoon(withDays(soon, now)), "gift_cards": gifts, "url": s.cfg.BaseURL,
	})
}

// brief is a pass or card without its barcode and pictures.
func brief(cs []store.Card) []map[string]any {
	out := make([]map[string]any, 0, len(cs))
	for i := range cs {
		out = append(out, briefOne(&cs[i]))
	}
	return out
}

func briefOne(c *store.Card) map[string]any {
	if c == nil {
		return nil
	}
	m := map[string]any{"id": c.ID, "kind": c.Kind, "kind_label": store.KindLabel(c.Kind), "name": c.Name, "issuer": c.Issuer, "path": "/cards/" + itoa(c.ID)}
	for k, v := range map[string]string{"starts_at": c.StartsAt, "ends_at": c.EndsAt, "venue": c.Venue, "destination": c.Destination, "seat": c.Seat,
		"gate": c.Gate, "ref": c.Ref, "expires_on": c.ExpiresOn, "holder": c.Holder, "trip_name": c.TripName} {
		if v != "" {
			m[k] = v
		}
	}
	if c.TripID != nil {
		m["trip_id"] = *c.TripID
	}
	m["has_code"] = c.CodeFormat != ""
	return m
}

func briefSoon(items []soonItem) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for i := range items {
		m := briefOne(&items[i].Card)
		m["days_left"] = items[i].DaysLeft
		out = append(out, m)
	}
	return out
}

// handleDay is one day for Pocket Day's journal: the passes for it.
func (s *Server) handleDay(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	day := chi.URLParam(r, "date")
	if !validDate(day) || day == "" {
		writeErr(w, http.StatusBadRequest, "the date is like 2026-11-05")
		return
	}
	cs, err := s.store.ListCards(ctx, actorID(ctx), store.CardFilter{Group: store.GroupPasses, From: day, To: day, Archived: "all", Sort: "starts"})
	if err != nil {
		s.fail(w, err, "day")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"date": day, "passes": brief(cs), "url": s.cfg.BaseURL})
}

// handlePasses lists passes: from today on by default, or a range, or a trip's.
func (s *Server) handlePasses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	f := cardFilter(r)
	f.Group, f.Sort = store.GroupPasses, "starts"
	if f.TripID == 0 && f.From == "" && f.To == "" {
		writeJSON(w, http.StatusOK, s.upcoming(ctx, uid, time.Now(), f.Limit))
		return
	}
	if f.Archived == "" {
		f.Archived = "all"
	}
	cs, err := s.store.ListCards(ctx, uid, f)
	if err != nil {
		s.fail(w, err, "passes")
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

// handleExpiring lists what runs out within ?days= (default: the person's setting).
func (s *Server) handleExpiring(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	days := queryInt(r, "days")
	if days <= 0 {
		days = st.ExpiryDays
	}
	cs, err := s.store.ListCards(ctx, uid, store.CardFilter{Expiring: store.AddDays(store.Today(), days), Group: r.URL.Query().Get("group"), Sort: "expires"})
	if err != nil {
		s.fail(w, err, "expiring")
		return
	}
	writeJSON(w, http.StatusOK, withDays(cs, time.Now()))
}

// handleSearch finds cards by name, shop, number, place or booking reference.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []store.Card{})
		return
	}
	limit := queryInt(r, "limit")
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	cs, err := s.store.ListCards(ctx, actorID(ctx), store.CardFilter{Q: q, Archived: "all", Sort: "used", Limit: limit})
	if err != nil {
		s.fail(w, err, "search")
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

// handlePickTrips lists the person's trips in Pocket Travel for the pass form.
func (s *Server) handlePickTrips(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	conn, err := s.clients(ctx, actorID(ctx)).conn(ctx, appTravel, scopeTripsRead)
	if err != nil {
		pick(w, nil, err, appTravel)
		return
	}
	trips, err := conn.client.Trips(ctx)
	if err == nil {
		keep := trips[:0]
		cut := time.Now().AddDate(0, -6, 0).Format("2006-01-02")
		for _, t := range trips {
			if !t.Archived && firstNonEmpty(t.EndDate, t.StartDate, "9999") >= cut {
				keep = append(keep, t)
			}
		}
		trips = keep
	}
	pick(w, trips, err, appTravel)
}
