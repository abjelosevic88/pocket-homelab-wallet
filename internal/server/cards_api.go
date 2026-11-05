package server

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/abjelosevic88/pocket-homelab-wallet/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// cardInput is a card as the forms and other apps send it; nil = not sent.
type cardInput struct {
	Kind        *string        `json:"kind"`
	Name        *string        `json:"name"`
	Issuer      *string        `json:"issuer"`
	Number      *string        `json:"number"`
	Holder      *string        `json:"holder"`
	CodeFormat  *string        `json:"code_format"`
	CodeValue   *string        `json:"code_value"`
	Color       *string        `json:"color"`
	Note        *string        `json:"note"`
	Tags        *string        `json:"tags"`
	Fields      *[]store.Field `json:"fields"`
	Pinned      *bool          `json:"pinned"`
	Archived    *bool          `json:"archived"`
	ValidFrom   *string        `json:"valid_from"`
	ExpiresOn   *string        `json:"expires_on"`
	StartsAt    *string        `json:"starts_at"`
	EndsAt      *string        `json:"ends_at"`
	Venue       *string        `json:"venue"`
	Destination *string        `json:"destination"`
	Seat        *string        `json:"seat"`
	Gate        *string        `json:"gate"`
	Ref         *string        `json:"ref"`
	Balance     *int64         `json:"balance"`
	Currency    *string        `json:"currency"`
	Points      *int64         `json:"points"`
	TripID      *int64         `json:"trip_id"`
	TripName    *string        `json:"trip_name"`
	SourceRef   *string        `json:"source_ref"`
}

var (
	reColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	reWhen  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}(T\d{2}:\d{2})?$`)
)

func validWhen(v string) bool {
	if v == "" {
		return true
	}
	if !reWhen.MatchString(v) {
		return false
	}
	layout := "2006-01-02"
	if len(v) > 10 {
		layout = "2006-01-02T15:04"
	}
	_, err := time.Parse(layout, v)
	return err == nil
}

// apply copies what was sent onto c and says what is wrong, if anything.
func (in cardInput) apply(c *store.Card, keys map[string]bool) string {
	if in.Kind != nil {
		if !store.ValidKind(*in.Kind) {
			return "unknown kind of card"
		}
		c.Kind = *in.Kind
	}
	trimP(&c.Name, in.Name)
	trimP(&c.Issuer, in.Issuer)
	trimP(&c.Number, in.Number)
	trimP(&c.Holder, in.Holder)
	if in.CodeFormat != nil {
		f := strings.ToLower(strings.TrimSpace(*in.CodeFormat))
		if !validFormat(f) {
			return "unknown kind of barcode"
		}
		c.CodeFormat = f
	}
	if in.CodeValue != nil {
		c.CodeValue = strings.TrimSpace(*in.CodeValue)
	}
	if in.Color != nil {
		v := strings.TrimSpace(*in.Color)
		if v != "" && !reColor.MatchString(v) {
			return "the colour is like #1f6feb"
		}
		c.Color = strings.ToLower(v)
	}
	trimP(&c.Note, in.Note)
	trimP(&c.Tags, in.Tags)
	if in.Fields != nil {
		if len(*in.Fields) > 40 {
			return "that is too many details"
		}
		c.Fields = *in.Fields
	}
	if in.Pinned != nil {
		c.Pinned = *in.Pinned
	}
	if in.Archived != nil {
		c.Archived = *in.Archived
	}
	for _, d := range []struct {
		dst *string
		v   *string
	}{{&c.ValidFrom, in.ValidFrom}, {&c.ExpiresOn, in.ExpiresOn}} {
		if d.v != nil {
			v := strings.TrimSpace(*d.v)
			if len(v) > 10 {
				v = v[:10]
			}
			if !validDate(v) {
				return "dates are like 2027-03-31"
			}
			*d.dst = v
		}
	}
	for _, d := range []struct {
		dst *string
		v   *string
	}{{&c.StartsAt, in.StartsAt}, {&c.EndsAt, in.EndsAt}} {
		if d.v != nil {
			v := strings.TrimSpace(*d.v)
			if len(v) > 16 {
				v = v[:16]
			}
			if !validWhen(v) {
				return "a time is like 2026-12-01T18:30"
			}
			*d.dst = v
		}
	}
	trimP(&c.Venue, in.Venue)
	trimP(&c.Destination, in.Destination)
	trimP(&c.Seat, in.Seat)
	trimP(&c.Gate, in.Gate)
	trimP(&c.Ref, in.Ref)
	if keys["balance"] {
		c.Balance = in.Balance
	}
	if keys["points"] {
		c.Points = in.Points
	}
	if in.Currency != nil {
		v := strings.TrimSpace(*in.Currency)
		if v != "" && cleanCurrency(v) == "" {
			return "the currency is a three-letter code like EUR"
		}
		c.Currency = cleanCurrency(v)
	}
	if keys["trip_id"] {
		c.TripID = in.TripID
		if c.TripID == nil || *c.TripID == 0 {
			c.TripID, c.TripName = nil, ""
		}
	}
	if in.TripName != nil && c.TripID != nil {
		c.TripName = strings.TrimSpace(*in.TripName)
	}
	// a barcode carries the card number unless something else was given
	if c.CodeFormat != "" && c.CodeValue == "" {
		c.CodeValue = strings.ReplaceAll(c.Number, " ", "")
	}
	if c.CodeFormat == "" && c.CodeValue != "" {
		c.CodeFormat = guessFormat(c.CodeValue)
	}
	if c.CodeFormat != "" {
		if c.CodeValue == "" {
			return "type the number the barcode carries, or choose no barcode"
		}
		if _, err := encodeCode(c.CodeFormat, c.CodeValue); err != nil {
			return "this barcode cannot hold that: " + err.Error()
		}
	}
	if c.Name == "" {
		c.Name = firstNonEmpty(c.Issuer, store.KindLabel(c.Kind))
	}
	if len(c.Name) > 200 || len(c.Note) > 20000 || len(c.CodeValue) > 4000 || len(c.Number) > 200 {
		return "that is too long"
	}
	if c.EndsAt != "" && c.StartsAt != "" && c.EndsAt < c.StartsAt {
		return "it ends before it starts"
	}
	return ""
}

func cardPayload(c *store.Card) map[string]any {
	out := map[string]any{"card_id": c.ID, "kind": c.Kind, "group": c.Group, "name": c.Name, "issuer": c.Issuer,
		"path": "/cards/" + itoa(c.ID), "title": c.Name}
	if c.ExpiresOn != "" {
		out["expires_on"] = c.ExpiresOn
	}
	if c.StartsAt != "" {
		out["starts_at"] = c.StartsAt
	}
	if c.Venue != "" {
		out["venue"] = c.Venue
	}
	if c.Destination != "" {
		out["destination"] = c.Destination
	}
	if c.TripID != nil {
		out["trip_id"] = *c.TripID
	}
	return out
}

// linkMayChange: a link token may only change what its own app added.
func linkMayChange(ctx context.Context, source string) bool {
	app := linkApp(ctx)
	return app == "" || app == source
}

// cardFilter reads the listing's query string.
func cardFilter(r *http.Request) store.CardFilter {
	q := r.URL.Query()
	f := store.CardFilter{Group: q.Get("group"), Kind: q.Get("kind"), Q: strings.TrimSpace(q.Get("q")), Tag: q.Get("tag"), Archived: q.Get("archived"),
		Pinned: queryBool(r, "pinned"), From: q.Get("from"), To: q.Get("to"), Expiring: q.Get("expiring"), Sort: q.Get("sort"), Limit: queryInt(r, "limit")}
	f.TripID, _ = strconv.ParseInt(q.Get("trip_id"), 10, 64)
	if f.Archived == "1" || f.Archived == "true" {
		f.Archived = "only"
	}
	return f
}

func (s *Server) handleListCards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	f := cardFilter(r)
	if f.Sort == "" {
		st, _ := s.store.GetSettings(ctx, actorID(ctx))
		f.Sort = st.Sort
		if f.Group == store.GroupPasses {
			f.Sort = "starts"
		} else if f.Group == store.GroupIDs {
			f.Sort = "expires"
		}
	}
	cs, err := s.store.ListCards(ctx, actorID(ctx), f)
	if err != nil {
		s.fail(w, err, "cards")
		return
	}
	writeJSON(w, http.StatusOK, cs)
}

func (s *Server) handleGetCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.GetCard(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "card")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleCreateCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in cardInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c := store.Card{Kind: "loyalty", Source: linkApp(ctx), Fields: []store.Field{}}
	if msg := in.apply(&c, keys); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if in.SourceRef != nil && c.Source != "" {
		c.SourceRef = strings.TrimSpace(*in.SourceRef)
		// another app sending the same card twice gets the one it added
		if old, err := s.store.CardBySource(ctx, uid, c.Source, c.SourceRef); err == nil && c.SourceRef != "" {
			writeJSON(w, http.StatusOK, old)
			return
		}
	}
	if err := s.store.CreateCard(ctx, uid, &c); err != nil {
		s.fail(w, err, "add card")
		return
	}
	s.afterSave(ctx, uid, &c, nil)
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleUpdateCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, err := s.store.GetCard(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "card")
		return
	}
	if !linkMayChange(ctx, c.Source) {
		writeErr(w, http.StatusForbidden, "this app may only change the cards it added")
		return
	}
	before := *c
	var in cardInput
	keys, err := decodeKeys(r, &in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if msg := in.apply(c, keys); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateCard(ctx, uid, c); err != nil {
		s.fail(w, err, "save card")
		return
	}
	s.afterSave(ctx, uid, c, &before)
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleDeleteCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, err := s.store.GetCard(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "card")
		return
	}
	if !linkMayChange(ctx, c.Source) {
		writeErr(w, http.StatusForbidden, "this app may only remove the cards it added")
		return
	}
	if c.CalEventID != nil {
		s.calendarRemove(ctx, uid, c)
	}
	if err := s.store.DeleteCard(ctx, uid, c.ID); err != nil {
		s.fail(w, err, "remove card")
		return
	}
	for _, n := range []string{c.FrontImage, c.BackImage, c.LogoImage} {
		s.removeImage(uid, n)
	}
	s.publish(ctx, uid, "wallet.card_removed", "card:"+itoa(c.ID), cardPayload(c))
	w.WriteHeader(http.StatusNoContent)
}

// handleCardUsed counts a time the card was shown at the till.
func (s *Server) handleCardUsed(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.store.MarkUsed(ctx, actorID(ctx), idParam(r, "id")); err != nil {
		s.fail(w, err, "card")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// afterSave files a pass with its trip, keeps its calendar event in step
// and tells the other apps.
func (s *Server) afterSave(ctx context.Context, uid int64, c *store.Card, before *store.Card) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
	defer cancel()
	st, _ := s.store.GetSettings(ctx, uid)
	if c.Group == store.GroupPasses && c.TripID == nil && c.Day() != "" && st.TravelTrips && (before == nil || before.Day() != c.Day()) {
		if t := s.tripOn(ctx, uid, c.Day()); t != nil {
			c.TripID, c.TripName = &t.ID, firstNonEmpty(t.Name, t.Destination)
			_ = s.store.SetTrip(ctx, uid, c.ID, c.TripID, c.TripName)
		}
	}
	if c.Group == store.GroupPasses && c.StartsAt != "" && !c.Archived && (c.CalEventID != nil || st.CalendarPasses) {
		if before == nil || c.CalEventID == nil || calendarChanged(before, c) {
			s.calendarSync(ctx, uid, c)
		}
	} else if c.CalEventID != nil && (c.Archived || c.StartsAt == "") {
		s.calendarRemove(ctx, uid, c)
	}
	s.publish(ctx, uid, "wallet.card_saved", "card:"+itoa(c.ID), cardPayload(c))
}

func calendarChanged(a, b *store.Card) bool {
	return a.Name != b.Name || a.StartsAt != b.StartsAt || a.EndsAt != b.EndsAt || a.Venue != b.Venue || a.Destination != b.Destination ||
		a.Seat != b.Seat || a.Gate != b.Gate || a.Ref != b.Ref
}

// tripOn is the Pocket Travel trip a day falls in, if any.
func (s *Server) tripOn(ctx context.Context, uid int64, day string) *pocket.Trip {
	conn, err := optional(s.clients(ctx, uid).conn(ctx, appTravel, scopeTripsRead))
	if err != nil || conn == nil {
		return nil
	}
	trips, err := conn.client.Trips(ctx)
	if err != nil {
		return nil
	}
	for _, t := range trips {
		if t.StartDate != "" && t.StartDate <= day && day <= firstNonEmpty(t.EndDate, t.StartDate) {
			t := t
			return &t
		}
	}
	return nil
}

func passEvent(c *store.Card, calendarID int64, link string) pocket.Event {
	title := c.Name
	if c.Kind == "boarding" && c.Ref != "" && !strings.Contains(title, c.Ref) {
		title = c.Ref + " " + title
	}
	var lines []string
	for _, kv := range [][2]string{{"From", c.Venue}, {"To", c.Destination}, {"Seat", c.Seat}, {"Gate", c.Gate}, {"Booking", c.Ref}} {
		if kv[1] != "" && !(kv[0] == "From" && c.Kind != "boarding") {
			lines = append(lines, kv[0]+": "+kv[1])
		}
	}
	if link != "" {
		lines = append(lines, "Show the pass: "+link)
	}
	loc := c.Venue
	return pocket.Event{CalendarID: calendarID, Title: title, Description: strings.Join(lines, "\n"), Location: loc, URL: link, Start: c.StartsAt, End: c.EndsAt}
}

// calendarSync puts a pass on the person's calendar or moves its event.
func (s *Server) calendarSync(ctx context.Context, uid int64, c *store.Card) {
	conn, err := optional(s.clients(ctx, uid).conn(ctx, appCalendar, scopeCalendarWrite))
	if err != nil || conn == nil {
		if err != nil {
			s.log.Warn("calendar", "user", uid, "err", err)
		}
		return
	}
	st, _ := s.store.GetSettings(ctx, uid)
	link := ""
	if s.cfg.BaseURL != "" {
		link = strings.TrimRight(s.cfg.BaseURL, "/") + "/cards/" + itoa(c.ID)
	}
	ev := passEvent(c, st.CalendarID, link)
	s.calMu.Lock()
	defer s.calMu.Unlock()
	if c.CalEventID != nil {
		err := conn.client.UpdateEvent(ctx, *c.CalEventID, ev)
		if err == nil {
			return
		}
		if !errors.Is(err, pocket.ErrNotFound) {
			s.log.Warn("calendar update", "user", uid, "err", err)
			return
		}
	}
	id, err := conn.client.CreateEvent(ctx, ev)
	if err != nil {
		s.log.Warn("calendar add", "user", uid, "err", err)
		return
	}
	c.CalEventID = &id
	_ = s.store.SetCalEvent(ctx, uid, c.ID, &id)
}

// calendarRemove takes a pass's event off the calendar.
func (s *Server) calendarRemove(ctx context.Context, uid int64, c *store.Card) {
	conn, err := optional(s.clients(ctx, uid).conn(ctx, appCalendar, scopeCalendarWrite))
	if err == nil && conn != nil && c.CalEventID != nil {
		if err := conn.client.DeleteEvent(ctx, *c.CalEventID); err != nil {
			s.log.Warn("calendar remove", "user", uid, "err", err)
			return
		}
	}
	c.CalEventID = nil
	_ = s.store.SetCalEvent(ctx, uid, c.ID, nil)
}

// handleCardCalendar puts a pass on the calendar now (or takes it off with ?remove=1).
func (s *Server) handleCardCalendar(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, err := s.store.GetCard(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "card")
		return
	}
	if queryBool(r, "remove") {
		s.calendarRemove(ctx, uid, c)
		writeJSON(w, http.StatusOK, c)
		return
	}
	if c.StartsAt == "" {
		writeErr(w, http.StatusBadRequest, "give it a day and time first")
		return
	}
	if _, err := s.clients(ctx, uid).conn(ctx, appCalendar, scopeCalendarWrite); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.calendarSync(ctx, uid, c)
	if c.CalEventID == nil {
		writeErr(w, http.StatusBadGateway, "Pocket Calendar did not take the event")
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// ---- barcodes ----

func (s *Server) handleCardCode(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, err := s.store.GetCard(ctx, actorID(ctx), idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "card")
		return
	}
	if c.CodeFormat == "" {
		writeErr(w, http.StatusNotFound, "this card has no barcode")
		return
	}
	svg, err := codeSVG(c.CodeFormat, c.CodeValue)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeSVG(w, svg, r.URL.Query().Get("v") != "")
}

// handlePreviewCode draws a barcode for the form, before it is saved.
func (s *Server) handlePreviewCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Format string `json:"format"`
		Value  string `json:"value"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !validFormat(in.Format) || in.Format == "" {
		writeErr(w, http.StatusBadRequest, "unknown kind of barcode")
		return
	}
	svg, err := codeSVG(in.Format, in.Value)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeSVG(w, svg, false)
}

// handleScan reads the barcode in a photo (the body, a JPEG or PNG).
func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 12<<20))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "the photo is too large")
		return
	}
	found, err := readCode(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"found": found})
}

// ---- gift card balance ----

func (s *Server) handleBalanceLog(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	if _, err := s.store.GetCard(ctx, uid, idParam(r, "id")); err != nil {
		s.fail(w, err, "card")
		return
	}
	log, err := s.store.BalanceLog(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "balance")
		return
	}
	writeJSON(w, http.StatusOK, log)
}

// handleAddBalance spends from a gift card ({"delta": -1250}) or tops it up.
func (s *Server) handleAddBalance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Delta int64  `json:"delta"`
		Note  string `json:"note"`
	}
	if err := decode(r, &in); err != nil || in.Delta == 0 {
		writeErr(w, http.StatusBadRequest, "say how much was spent or added")
		return
	}
	c, err := s.store.GetCard(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "card")
		return
	}
	if c.Balance == nil && in.Delta < 0 {
		writeErr(w, http.StatusBadRequest, "set what is on the card first")
		return
	}
	e, err := s.store.AddBalance(ctx, uid, c.ID, in.Delta, in.Note)
	if err != nil {
		s.fail(w, err, "balance")
		return
	}
	c.Balance = &e.Balance
	p := cardPayload(c)
	p["balance"], p["delta"], p["currency"] = e.Balance, e.Delta, c.Currency
	s.publish(ctx, uid, "wallet.card_saved", "card:"+itoa(c.ID), p)
	writeJSON(w, http.StatusOK, map[string]any{"entry": e, "card": c})
}

// ---- pictures: the front and back of a card, a pass's logo ----

func (s *Server) imageDir(uid int64) string {
	return filepath.Join(s.cfg.DataDir, "images", itoa(uid))
}

func (s *Server) removeImage(uid int64, name string) {
	if name == "" || strings.ContainsAny(name, `/\`) {
		return
	}
	_ = os.Remove(filepath.Join(s.imageDir(uid), name))
}

// saveImage keeps a picture (JPEG, PNG or GIF) and returns its file name.
func (s *Server) saveImage(uid, cardID int64, side string, data []byte) (string, error) {
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", errors.New("that is not a picture this app can keep (JPEG or PNG)")
	}
	ext := map[string]string{"jpeg": ".jpg", "png": ".png", "gif": ".gif"}[format]
	if ext == "" {
		return "", errors.New("pictures are JPEG or PNG")
	}
	dir := s.imageDir(uid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := itoa(cardID) + "-" + side + "-" + store.RandomToken(6) + ext
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", err
	}
	return name, nil
}

func imageOf(c *store.Card, side string) string {
	switch side {
	case "front":
		return c.FrontImage
	case "back":
		return c.BackImage
	case "logo":
		return c.LogoImage
	}
	return ""
}

func (s *Server) handleGetImage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	c, err := s.store.GetCard(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "card")
		return
	}
	name := imageOf(c, chi.URLParam(r, "side"))
	if name == "" {
		writeErr(w, http.StatusNotFound, "no picture")
		return
	}
	f, err := os.Open(filepath.Join(s.imageDir(uid), name))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no picture")
		return
	}
	defer f.Close()
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	http.ServeContent(w, r, name, time.Time{}, f)
}

func (s *Server) handlePutImage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	side := chi.URLParam(r, "side")
	c, err := s.store.GetCard(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "card")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 12<<20))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "the picture is too large")
		return
	}
	name, err := s.saveImage(uid, c.ID, side, body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	old := imageOf(c, side)
	if err := s.store.SetImage(ctx, uid, c.ID, side, name); err != nil {
		s.removeImage(uid, name)
		s.fail(w, err, "picture")
		return
	}
	s.removeImage(uid, old)
	c, _ = s.store.GetCard(ctx, uid, c.ID)
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleDeleteImage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	side := chi.URLParam(r, "side")
	c, err := s.store.GetCard(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "card")
		return
	}
	old := imageOf(c, side)
	if err := s.store.SetImage(ctx, uid, c.ID, side, ""); err != nil {
		s.fail(w, err, "picture")
		return
	}
	s.removeImage(uid, old)
	c, _ = s.store.GetCard(ctx, uid, c.ID)
	writeJSON(w, http.StatusOK, c)
}

// ---- Pocket Money: what you spent at the shop ----

// handleCardSpent sums the payments in Pocket Money whose payee looks like
// the card's shop, over the last 90 days.
func (s *Server) handleCardSpent(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	uid := actorID(ctx)
	c, err := s.store.GetCard(ctx, uid, idParam(r, "id"))
	if err != nil {
		s.fail(w, err, "card")
		return
	}
	st, _ := s.store.GetSettings(ctx, uid)
	if !st.MoneySpend || c.Group != store.GroupCards {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false})
		return
	}
	conn, err := s.clients(ctx, uid).conn(ctx, appMoney, scopeTxRead)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": false, "detail": err.Error()})
		return
	}
	words := shopWords(firstNonEmpty(c.Issuer, c.Name))
	if len(words) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"connected": true, "count": 0})
		return
	}
	from := time.Now().AddDate(0, 0, -90).Format("2006-01-02")
	pays, err := conn.client.Payments(ctx, from, store.Today())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"connected": true, "detail": err.Error()})
		return
	}
	sums := map[string]int64{}
	count, last := 0, ""
	for _, p := range pays {
		if !payeeMatches(p.Payee+" "+p.Description, words) {
			continue
		}
		amt, cur := p.Paid()
		sums[cur] += amt
		count++
		if p.Date > last {
			last = p.Date
		}
	}
	type sum struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	}
	out := []sum{}
	for cur, amt := range sums {
		out = append(out, sum{amt, cur})
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "count": count, "last": last, "since": from, "sums": out,
		"open_url": s.siblingLink(ctx, uid, appMoney, "/")})
}

// shopWords are the words of a shop name worth matching ("Lidl Plus" → lidl).
func shopWords(name string) []string {
	skip := map[string]bool{"plus": true, "card": true, "club": true, "karta": true, "kartica": true, "member": true, "membership": true, "loyalty": true,
		"the": true, "and": true, "app": true, "rewards": true, "bonus": true, "gift": true, "d.o.o.": true, "doo": true}
	var out []string
	for _, w := range strings.Fields(strings.ToLower(name)) {
		w = strings.Trim(w, ".,:;!?()&-'\"")
		if len(w) >= 3 && !skip[w] {
			out = append(out, w)
		}
	}
	if len(out) > 2 {
		out = out[:2]
	}
	return out
}

func payeeMatches(text string, words []string) bool {
	t := " " + strings.ToLower(text) + " "
	for _, w := range words {
		if !strings.Contains(t, w) {
			return false
		}
	}
	return true
}
