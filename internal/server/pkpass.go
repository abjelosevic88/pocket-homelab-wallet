package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// ---- Apple Wallet passes (.pkpass) ----
//
// A .pkpass is a zip with pass.json, pictures and translations. Airlines,
// trains, venues and shops send them by email; Wallet reads the barcode,
// the times, seat and gate, the colours and the logo. The signature is not
// checked: the pass is yours, and the code is what the gate scans.

type pkField struct {
	Key          string          `json:"key"`
	Label        string          `json:"label"`
	Value        json.RawMessage `json:"value"`
	CurrencyCode string          `json:"currencyCode"`
	DateStyle    string          `json:"dateStyle"`
	TimeStyle    string          `json:"timeStyle"`
}

type pkStyle struct {
	TransitType     string    `json:"transitType"`
	HeaderFields    []pkField `json:"headerFields"`
	PrimaryFields   []pkField `json:"primaryFields"`
	SecondaryFields []pkField `json:"secondaryFields"`
	AuxiliaryFields []pkField `json:"auxiliaryFields"`
	BackFields      []pkField `json:"backFields"`
}

type pkBarcode struct {
	Message string `json:"message"`
	Format  string `json:"format"`
	AltText string `json:"altText"`
}

type pkPass struct {
	PassTypeIdentifier string                     `json:"passTypeIdentifier"`
	SerialNumber       string                     `json:"serialNumber"`
	OrganizationName   string                     `json:"organizationName"`
	Description        string                     `json:"description"`
	LogoText           string                     `json:"logoText"`
	BackgroundColor    string                     `json:"backgroundColor"`
	RelevantDate       string                     `json:"relevantDate"`
	ExpirationDate     string                     `json:"expirationDate"`
	Voided             bool                       `json:"voided"`
	Barcode            *pkBarcode                 `json:"barcode"`
	Barcodes           []pkBarcode                `json:"barcodes"`
	BoardingPass       *pkStyle                   `json:"boardingPass"`
	EventTicket        *pkStyle                   `json:"eventTicket"`
	StoreCard          *pkStyle                   `json:"storeCard"`
	Coupon             *pkStyle                   `json:"coupon"`
	Generic            *pkStyle                   `json:"generic"`
	Semantics          map[string]json.RawMessage `json:"semantics"`
}

// parsedPass is a pass turned into a card, with its logo.
type parsedPass struct {
	Card store.Card
	Logo []byte
}

var pkFormats = map[string]string{"PKBarcodeFormatQR": "qr", "PKBarcodeFormatPDF417": "pdf417", "PKBarcodeFormatAztec": "aztec", "PKBarcodeFormatCode128": "code128"}

// parsePkpasses reads a .pkpass, or a .pkpasses bundle of several.
func parsePkpasses(data []byte) ([]parsedPass, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.New("that is not a .pkpass file")
	}
	files := map[string]*zip.File{}
	var inner []*zip.File
	for _, f := range zr.File {
		files[f.Name] = f
		if strings.HasSuffix(strings.ToLower(f.Name), ".pkpass") {
			inner = append(inner, f)
		}
	}
	if files["pass.json"] == nil && len(inner) > 0 {
		var out []parsedPass
		for _, f := range inner {
			b, err := readZip(f, 10<<20)
			if err != nil {
				continue
			}
			if ps, err := parsePkpasses(b); err == nil {
				out = append(out, ps...)
			}
		}
		if len(out) == 0 {
			return nil, errors.New("there is no pass in that file")
		}
		return out, nil
	}
	p, err := parsePkpass(files)
	if err != nil {
		return nil, err
	}
	return []parsedPass{*p}, nil
}

func readZip(f *zip.File, limit int64) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("too large")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, limit))
}

func parsePkpass(files map[string]*zip.File) (*parsedPass, error) {
	f := files["pass.json"]
	if f == nil {
		return nil, errors.New("that .pkpass has no pass.json")
	}
	raw, err := readZip(f, 2<<20)
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var p pkPass
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("pass.json: %w", err)
	}
	tr := passStrings(files)
	t := func(s string) string {
		if v, ok := tr[s]; ok {
			return v
		}
		return s
	}
	c := store.Card{Fields: []store.Field{}, Source: "pkpass"}
	if p.PassTypeIdentifier != "" || p.SerialNumber != "" {
		c.SourceRef = p.PassTypeIdentifier + ":" + p.SerialNumber
	}
	var style *pkStyle
	switch {
	case p.BoardingPass != nil:
		style, c.Kind = p.BoardingPass, "ticket"
		if p.BoardingPass.TransitType == "PKTransitTypeAir" {
			c.Kind = "boarding"
		}
	case p.EventTicket != nil:
		style, c.Kind = p.EventTicket, "ticket"
	case p.StoreCard != nil:
		style, c.Kind = p.StoreCard, "loyalty"
	case p.Coupon != nil:
		style, c.Kind = p.Coupon, "gift"
	case p.Generic != nil:
		style, c.Kind = p.Generic, "other"
	default:
		style, c.Kind = &pkStyle{}, "other"
	}
	c.Issuer = t(p.OrganizationName)
	c.Color = rgbHex(p.BackgroundColor)
	bc := p.Barcode
	if len(p.Barcodes) > 0 {
		bc = &p.Barcodes[0]
	}
	if bc != nil && pkFormats[bc.Format] != "" && bc.Message != "" {
		c.CodeFormat, c.CodeValue = pkFormats[bc.Format], bc.Message
		c.Number = t(bc.AltText)
	}
	if when := passTime(p.RelevantDate); when != "" && c.Kind != "loyalty" {
		c.StartsAt = when
	}
	if d := passTime(p.ExpirationDate); d != "" {
		c.ExpiresOn = d[:10]
	}

	// every field, in the order a pass shows them
	type fv struct {
		key, label, value string
		back              bool
		cur               string
		num               *float64
	}
	var all []fv
	add := func(fs []pkField, back bool) {
		for _, x := range fs {
			v, num := fieldValue(x, t)
			if v == "" {
				continue
			}
			all = append(all, fv{strings.ToLower(x.Key), strings.TrimSpace(t(x.Label)), v, back, x.CurrencyCode, num})
		}
	}
	add(style.HeaderFields, false)
	add(style.PrimaryFields, false)
	add(style.SecondaryFields, false)
	add(style.AuxiliaryFields, false)
	add(style.BackFields, true)

	match := func(x fv, words ...string) bool {
		k, l := x.key, strings.ToLower(x.label)
		for _, w := range words {
			if strings.Contains(k, w) || strings.Contains(l, w) {
				return true
			}
		}
		return false
	}
	setOnce := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	// the two primary fields of a boarding pass are where from and where to
	if p.BoardingPass != nil && len(style.PrimaryFields) >= 2 {
		from, to := style.PrimaryFields[0], style.PrimaryFields[len(style.PrimaryFields)-1]
		fv1, _ := fieldValue(from, t)
		fv2, _ := fieldValue(to, t)
		c.Venue = strings.TrimSpace(fv1 + " " + t(from.Label))
		c.Destination = strings.TrimSpace(fv2 + " " + t(to.Label))
		c.Name = firstNonEmpty(t(from.Label), fv1) + " → " + firstNonEmpty(t(to.Label), fv2)
	}
	for _, x := range all {
		switch {
		case x.back:
		case match(x, "seat", "sitz", "sjedi", "place", "row"):
			if !strings.Contains(c.Seat, x.value) {
				c.Seat = strings.TrimSpace(c.Seat + " " + x.value)
			}
			continue
		case match(x, "gate", "gatter", "izlaz"):
			setOnce(&c.Gate, x.value)
			continue
		case match(x, "flight", "flug", "let ", "train", "zug", "pnr", "booking", "confirmation", "reservation", "buchung"):
			setOnce(&c.Ref, x.value)
		case match(x, "passenger", "holder", "guest", "putnik", "attendee") || x.key == "name" || x.key == "membername" || x.key == "member_name":
			setOnce(&c.Holder, x.value)
		case match(x, "venue", "location", "where", "ort", "mjesto") && c.Kind == "ticket":
			setOnce(&c.Venue, x.value)
		case match(x, "balance", "guthaben", "stanje") && x.num != nil && x.cur != "":
			if c.Kind == "loyalty" {
				c.Kind = "gift"
			}
			b := int64(math.Round(*x.num * 100))
			c.Balance, c.Currency = &b, strings.ToUpper(x.cur)
		case match(x, "points", "punkte", "bodovi") && x.num != nil:
			n := int64(*x.num)
			c.Points = &n
		case match(x, "number", "card", "nummer", "broj", "member id", "account") && c.Number == "":
			c.Number = x.value
		case match(x, "date", "time", "datum", "departure", "boarding", "doors", "start") && c.StartsAt == "" && c.Kind != "loyalty":
			if w := passTime(x.value); w != "" {
				c.StartsAt = w
			}
		}
		if len(c.Fields) < 25 && len(x.value) <= 300 {
			c.Fields = append(c.Fields, store.Field{Label: x.label, Value: x.value})
		}
	}
	// semantic tags (iOS 12+) are the most precise when there
	if v := semString(p.Semantics, "flightCode", "flightNumber"); v != "" && c.Ref == "" {
		c.Ref = v
	}
	if v := semString(p.Semantics, "departureGate"); v != "" && c.Gate == "" {
		c.Gate = v
	}
	if v := semString(p.Semantics, "venueName"); v != "" && c.Kind == "ticket" && c.Venue == "" {
		c.Venue = v
	}
	if v := semString(p.Semantics, "eventName"); v != "" && c.Name == "" {
		c.Name = v
	}
	for _, k := range []string{"currentDepartureDate", "originalDepartureDate", "eventStartDate"} {
		if w := passTime(semString(p.Semantics, k)); w != "" {
			c.StartsAt = w
			break
		}
	}
	for _, k := range []string{"currentArrivalDate", "originalArrivalDate", "eventEndDate"} {
		if w := passTime(semString(p.Semantics, k)); w != "" && w > c.StartsAt {
			c.EndsAt = w
			break
		}
	}
	if c.Name == "" {
		for _, x := range all {
			if !x.back && x.value != "" && passTime(x.value) == "" && c.Kind == "ticket" {
				c.Name = x.value // an event ticket's first field is the event
				break
			}
		}
	}
	c.Name = firstNonEmpty(c.Name, t(p.LogoText), t(p.Description), c.Issuer, store.KindLabel(c.Kind))
	if len(c.Name) > 200 {
		c.Name = c.Name[:200]
	}
	if p.Voided {
		c.Archived = true
	}
	if c.CodeFormat != "" {
		if _, err := encodeCode(c.CodeFormat, c.CodeValue); err != nil {
			c.CodeFormat, c.CodeValue = "", ""
		}
	}
	out := &parsedPass{Card: c}
	for _, n := range []string{"logo@2x.png", "logo.png", "icon@2x.png", "icon.png"} {
		if f := files[n]; f != nil {
			if b, err := readZip(f, 2<<20); err == nil {
				out.Logo = b
				break
			}
		}
	}
	return out, nil
}

// fieldValue is a field's value as text (and as a number when it is one).
func fieldValue(f pkField, t func(string) string) (string, *float64) {
	var s string
	if err := json.Unmarshal(f.Value, &s); err == nil {
		s = strings.TrimSpace(t(s))
		if f.DateStyle != "" || f.TimeStyle != "" {
			if w := passTime(s); w != "" {
				if f.DateStyle == "PKDateStyleNone" && len(w) > 10 {
					return w[11:], nil
				}
				if f.TimeStyle == "PKDateStyleNone" || len(w) == 10 {
					return w[:10], nil
				}
				return strings.Replace(w, "T", " ", 1), nil
			}
		}
		return s, nil
	}
	var n float64
	if err := json.Unmarshal(f.Value, &n); err == nil {
		if f.CurrencyCode != "" {
			return strconv.FormatFloat(n, 'f', 2, 64) + " " + strings.ToUpper(f.CurrencyCode), &n
		}
		return strconv.FormatFloat(n, 'f', -1, 64), &n
	}
	return "", nil
}

func semString(sem map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		var s string
		if raw, ok := sem[k]; ok && json.Unmarshal(raw, &s) == nil && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// passTime reads an ISO 8601 time from a pass as the wall clock where it
// happens ("2026-12-01T06:40+01:00" → "2026-12-01T06:40").
func passTime(v string) string {
	v = strings.TrimSpace(v)
	if len(v) < 10 {
		return ""
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05Z0700", "2006-01-02T15:04Z0700", "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t.Format("2006-01-02T15:04")
		}
	}
	if t, err := time.Parse("2006-01-02", v); err == nil && len(v) == 10 {
		return t.Format("2006-01-02")
	}
	return ""
}

var reRGB = regexp.MustCompile(`rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)`)

// rgbHex turns "rgb(7, 53, 144)" or "#073590" into "#073590".
func rgbHex(v string) string {
	v = strings.TrimSpace(v)
	if reColor.MatchString(v) {
		return strings.ToLower(v)
	}
	m := reRGB.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	n := func(s string) int {
		x, _ := strconv.Atoi(s)
		if x > 255 {
			x = 255
		}
		return x
	}
	return fmt.Sprintf("#%02x%02x%02x", n(m[1]), n(m[2]), n(m[3]))
}

// passStrings reads the pass's translations: English first, else the first one.
func passStrings(files map[string]*zip.File) map[string]string {
	var pick *zip.File
	for name, f := range files {
		if path.Base(name) != "pass.strings" {
			continue
		}
		if strings.HasPrefix(name, "en.lproj/") || strings.HasPrefix(name, "en-") {
			pick = f
			break
		}
		if pick == nil || name < pick.Name {
			pick = f
		}
	}
	out := map[string]string{}
	if pick == nil {
		return out
	}
	b, err := readZip(pick, 1<<20)
	if err != nil {
		return out
	}
	text := decodeStrings(b)
	for _, m := range reStrings.FindAllStringSubmatch(text, -1) {
		out[unescape(m[1])] = unescape(m[2])
	}
	return out
}

var reStrings = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"\s*=\s*"((?:[^"\\]|\\.)*)"\s*;`)

func unescape(s string) string {
	r := strings.NewReplacer(`\"`, `"`, `\n`, "\n", `\\`, `\`)
	return r.Replace(s)
}

// decodeStrings reads a .strings file in UTF-8 or UTF-16 (either byte order).
func decodeStrings(b []byte) string {
	if len(b) >= 2 && ((b[0] == 0xff && b[1] == 0xfe) || (b[0] == 0xfe && b[1] == 0xff)) {
		var order binary.ByteOrder = binary.LittleEndian
		if b[0] == 0xfe {
			order = binary.BigEndian
		}
		b = b[2:]
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = order.Uint16(b[2*i:])
		}
		return string(utf16.Decode(u))
	}
	return string(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")))
}

// importPass saves a parsed pass: a pass seen before (same serial) is
// brought up to date — a new gate or seat — keeping the person's own notes.
func (s *Server) importPass(ctx context.Context, uid int64, p parsedPass) (*store.Card, bool, error) {
	c := p.Card
	if c.SourceRef != "" {
		if old, err := s.store.CardBySource(ctx, uid, c.Source, c.SourceRef); err == nil {
			before := *old
			old.Kind, old.Name, old.Issuer, old.Number, old.CodeFormat, old.CodeValue = c.Kind, c.Name, c.Issuer, firstNonEmpty(c.Number, old.Number), c.CodeFormat, c.CodeValue
			old.Color, old.Fields, old.StartsAt, old.EndsAt, old.ExpiresOn = firstNonEmpty(c.Color, old.Color), c.Fields, c.StartsAt, c.EndsAt, c.ExpiresOn
			old.Venue, old.Destination, old.Seat, old.Gate, old.Ref, old.Holder = c.Venue, c.Destination, c.Seat, c.Gate, c.Ref, firstNonEmpty(c.Holder, old.Holder)
			if c.Balance != nil {
				old.Balance, old.Currency = c.Balance, c.Currency
			}
			if c.Points != nil {
				old.Points = c.Points
			}
			if c.Archived {
				old.Archived = true
			}
			if err := s.store.UpdateCard(ctx, uid, old); err != nil {
				return nil, false, err
			}
			s.afterSave(ctx, uid, old, &before)
			return old, false, nil
		}
	}
	if err := s.store.CreateCard(ctx, uid, &c); err != nil {
		return nil, false, err
	}
	if len(p.Logo) > 0 {
		if name, err := s.saveImage(uid, c.ID, "logo", p.Logo); err == nil {
			_ = s.store.SetImage(ctx, uid, c.ID, "logo", name)
			c.LogoImage = name
		}
	}
	s.afterSave(ctx, uid, &c, nil)
	return &c, true, nil
}

// handleImportPkpass takes one .pkpass (or .pkpasses) file as the body.
func (s *Server) handleImportPkpass(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 20<<20))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "the file is too large")
		return
	}
	passes, err := parsePkpasses(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cards := []*store.Card{}
	added := 0
	for _, p := range passes {
		c, created, err := s.importPass(ctx, uid, p)
		if err != nil {
			s.fail(w, err, "import pass")
			return
		}
		if created {
			added++
		}
		cards = append(cards, c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"cards": cards, "added": added, "updated": len(cards) - added})
}
