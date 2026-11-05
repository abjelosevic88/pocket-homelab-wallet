package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// ---- moving in: Catima, CSV, a Pocket Wallet export, .pkpass ----

// handleImport takes one file as the body and works out what it is: a
// Pocket Wallet export (JSON), a Catima export (.zip or catima.csv), a
// .pkpass, or a CSV with a header row.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 60<<20))
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "the file is too large")
		return
	}
	counts, err := s.importAny(ctx, uid, body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, counts)
}

func (s *Server) importAny(ctx context.Context, uid int64, body []byte) (map[string]int, error) {
	trim := bytes.TrimSpace(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")))
	switch {
	case bytes.HasPrefix(body, []byte("PK\x03\x04")):
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return nil, errors.New("that zip file cannot be opened")
		}
		for _, f := range zr.File {
			if f.Flags&0x1 != 0 {
				return nil, errors.New("that file has a password; export it again without one")
			}
		}
		for _, f := range zr.File {
			if path.Base(f.Name) == "catima.csv" {
				return s.importCatima(ctx, uid, zr, f)
			}
		}
		passes, err := parsePkpasses(body)
		if err != nil {
			return nil, errors.New("that zip is neither a Catima export nor a .pkpass")
		}
		counts := map[string]int{}
		for _, p := range passes {
			if _, created, err := s.importPass(ctx, uid, p); err == nil {
				if created {
					counts["cards"]++
				} else {
					counts["updated"]++
				}
			}
		}
		return counts, nil
	case bytes.HasPrefix(trim, []byte("{")):
		var doc exportDoc
		if err := json.Unmarshal(trim, &doc); err != nil {
			return nil, fmt.Errorf("that JSON file cannot be read: %w", err)
		}
		if doc.App != "pocket-wallet" {
			return nil, errors.New("that JSON file is not a Pocket Wallet export")
		}
		return s.importPocket(ctx, uid, doc), nil
	}
	text := string(trim)
	if isCatimaCSV(text) {
		return s.importCatimaCSV(ctx, uid, text, nil)
	}
	return s.importCSV(ctx, uid, text)
}

// ---- Catima ----

func isCatimaCSV(text string) bool {
	for _, line := range strings.SplitN(text, "\n", 12) {
		l := strings.ToLower(line)
		if strings.HasPrefix(l, "_id,store,") && strings.Contains(l, "cardid") {
			return true
		}
	}
	return false
}

func (s *Server) importCatima(ctx context.Context, uid int64, zr *zip.Reader, csvFile *zip.File) (map[string]int, error) {
	b, err := readZip(csvFile, 20<<20)
	if err != nil {
		return nil, err
	}
	images := map[string]*zip.File{}
	for _, f := range zr.File {
		images[path.Base(f.Name)] = f
	}
	return s.importCatimaCSV(ctx, uid, string(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))), images)
}

var catimaFormats = map[string]string{"AZTEC": "aztec", "CODABAR": "codabar", "CODE_39": "code39", "CODE_93": "code93", "CODE_128": "code128",
	"DATA_MATRIX": "datamatrix", "EAN_8": "ean8", "EAN_13": "ean13", "ITF": "itf", "PDF_417": "pdf417", "QR_CODE": "qr", "UPC_A": "upca", "UPC_E": "upce"}

// importCatimaCSV reads Catima's export: a version line, then sections
// separated by blank lines — groups, cards, which card is in which group.
func (s *Server) importCatimaCSV(ctx context.Context, uid int64, text string, images map[string]*zip.File) (map[string]int, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var cardRows [][]string
	var cardHead []string
	groupsOf := map[string][]string{}
	for _, sec := range splitSections(text) {
		rd := csv.NewReader(strings.NewReader(sec))
		rd.FieldsPerRecord = -1
		rows, err := rd.ReadAll()
		if err != nil || len(rows) == 0 {
			continue
		}
		head := lowerAll(rows[0])
		switch {
		case len(head) > 1 && head[0] == "_id" && head[1] == "store":
			cardHead, cardRows = head, rows[1:]
		case len(head) == 2 && head[0] == "cardid" && head[1] == "groupid":
			for _, r := range rows[1:] {
				if len(r) == 2 {
					groupsOf[r[0]] = append(groupsOf[r[0]], r[1])
				}
			}
		}
	}
	if cardHead == nil {
		return nil, errors.New("there are no cards in that Catima export")
	}
	col := map[string]int{}
	for i, h := range cardHead {
		col[h] = i
	}
	get := func(r []string, k string) string {
		if i, ok := col[k]; ok && i < len(r) {
			return strings.TrimSpace(r[i])
		}
		return ""
	}
	counts := map[string]int{}
	for _, r := range cardRows {
		id := get(r, "_id")
		c := store.Card{Kind: "loyalty", Source: "catima", SourceRef: id, Fields: []store.Field{}}
		c.Name, c.Issuer = get(r, "store"), get(r, "store")
		c.Note, c.Number = get(r, "note"), get(r, "cardid")
		if c.Name == "" {
			counts["skipped"]++
			continue
		}
		c.CodeFormat = catimaFormats[strings.ToUpper(get(r, "barcodetype"))]
		c.CodeValue = firstNonEmpty(get(r, "barcodeid"), c.Number)
		if c.CodeFormat != "" {
			if _, err := encodeCode(c.CodeFormat, c.CodeValue); err != nil {
				c.CodeFormat = ""
			}
		}
		if c.CodeFormat == "" {
			c.CodeValue = ""
		}
		c.ValidFrom, c.ExpiresOn = millisDate(get(r, "validfrom")), millisDate(get(r, "expiry"))
		if bal := get(r, "balance"); bal != "" {
			if f, err := strconv.ParseFloat(strings.ReplaceAll(bal, ",", "."), 64); err == nil && f != 0 {
				if cur := cleanCurrency(get(r, "balancetype")); cur != "" {
					m := int64(math.Round(f * 100))
					c.Kind, c.Balance, c.Currency = "gift", &m, cur
				} else {
					p := int64(math.Round(f))
					c.Points = &p
				}
			}
		}
		if hc := get(r, "headercolor"); hc != "" {
			if n, err := strconv.ParseInt(hc, 10, 64); err == nil {
				c.Color = fmt.Sprintf("#%06x", uint32(n)&0xffffff)
			}
		}
		c.Pinned = get(r, "starstatus") == "1"
		c.Archived = get(r, "archive") == "1" || get(r, "archivestatus") == "1"
		if lu := get(r, "lastused"); lu != "" {
			if n, err := strconv.ParseInt(lu, 10, 64); err == nil && n > 0 {
				c.LastUsedAt = time.Unix(n, 0).UTC().Format(time.RFC3339Nano)
			}
		}
		c.Tags = strings.Join(groupsOf[id], ", ")
		if old, err := s.store.CardBySource(ctx, uid, "catima", id); err == nil && id != "" {
			_ = old
			counts["already"]++
			continue
		}
		if err := s.store.CreateCard(ctx, uid, &c); err != nil {
			counts["skipped"]++
			continue
		}
		counts["cards"]++
		for side, names := range map[string][]string{"front": {"card_" + id + "_front.png", "card_" + id + "_front.jpg"},
			"back": {"card_" + id + "_back.png", "card_" + id + "_back.jpg"}, "logo": {"card_" + id + "_icon.png", "card_" + id + "_icon.jpg"}} {
			for _, n := range names {
				if f := images[n]; f != nil {
					if b, err := readZip(f, 10<<20); err == nil {
						if name, err := s.saveImage(uid, c.ID, side, b); err == nil {
							_ = s.store.SetImage(ctx, uid, c.ID, side, name)
						}
					}
					break
				}
			}
		}
		s.publish(ctx, uid, "wallet.card_saved", "card:"+itoa(c.ID), cardPayload(&c))
	}
	return counts, nil
}

func splitSections(text string) []string {
	var out []string
	for _, sec := range strings.Split(text, "\n\n") {
		sec = strings.TrimSpace(sec)
		if sec != "" {
			out = append(out, sec)
		}
	}
	return out
}

func lowerAll(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = strings.ToLower(strings.TrimSpace(s))
	}
	return out
}

// millisDate turns Catima's epoch milliseconds into a day.
func millisDate(v string) string {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return ""
	}
	return time.UnixMilli(n).Format("2006-01-02")
}

// ---- CSV ----

// csvColumns are the columns of the CSV export, which the import reads back.
var csvColumns = []string{"name", "kind", "issuer", "number", "holder", "code_format", "code_value", "color", "valid_from", "expires_on",
	"starts_at", "ends_at", "venue", "destination", "seat", "gate", "ref", "balance", "currency", "points", "tags", "note", "pinned", "archived"}

// csvAliases map common column names of other apps to ours.
var csvAliases = map[string]string{"store": "name", "title": "name", "card": "name", "card name": "name", "type": "kind", "company": "issuer", "shop": "issuer",
	"card number": "number", "cardid": "number", "card_number": "number", "barcode": "code_value", "barcode value": "code_value", "barcode_value": "code_value",
	"barcode type": "code_format", "barcode_type": "code_format", "barcodetype": "code_format", "format": "code_format", "expiry": "expires_on", "expires": "expires_on",
	"expiry date": "expires_on", "valid until": "expires_on", "notes": "note", "group": "tags", "groups": "tags", "favourite": "pinned", "favorite": "pinned", "starred": "pinned"}

func (s *Server) importCSV(ctx context.Context, uid int64, text string) (map[string]int, error) {
	rd := csv.NewReader(strings.NewReader(text))
	rd.FieldsPerRecord = -1
	rd.LazyQuotes = true
	rows, err := rd.ReadAll()
	if err != nil || len(rows) < 2 {
		return nil, errors.New("that file is not a CSV with a header row and cards under it")
	}
	col := map[string]int{}
	for i, h := range lowerAll(rows[0]) {
		if a, ok := csvAliases[h]; ok {
			h = a
		}
		if _, seen := col[h]; !seen {
			col[h] = i
		}
	}
	if _, ok := col["name"]; !ok {
		return nil, errors.New("the CSV needs a name column")
	}
	get := func(r []string, k string) string {
		if i, ok := col[k]; ok && i < len(r) {
			return strings.TrimSpace(r[i])
		}
		return ""
	}
	counts := map[string]int{}
	for _, r := range rows[1:] {
		in := cardInput{}
		set := func(k string, dst **string) {
			if v := get(r, k); v != "" {
				*dst = &v
			}
		}
		for k, dst := range map[string]**string{"name": &in.Name, "issuer": &in.Issuer, "number": &in.Number, "holder": &in.Holder, "color": &in.Color,
			"valid_from": &in.ValidFrom, "expires_on": &in.ExpiresOn, "starts_at": &in.StartsAt, "ends_at": &in.EndsAt, "venue": &in.Venue,
			"destination": &in.Destination, "seat": &in.Seat, "gate": &in.Gate, "ref": &in.Ref, "currency": &in.Currency, "tags": &in.Tags, "note": &in.Note,
			"code_value": &in.CodeValue} {
			set(k, dst)
		}
		if k := strings.ToLower(get(r, "kind")); store.ValidKind(k) {
			in.Kind = &k
		}
		if f := strings.ToLower(strings.ReplaceAll(get(r, "code_format"), "_", "")); f != "" {
			if v, ok := catimaFormats[strings.ToUpper(get(r, "code_format"))]; ok {
				f = v
			}
			if validFormat(f) {
				in.CodeFormat = &f
			}
		}
		keys := map[string]bool{}
		if v := get(r, "balance"); v != "" {
			if f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64); err == nil {
				m := int64(math.Round(f * 100))
				in.Balance, keys["balance"] = &m, true
			}
		}
		if v := get(r, "points"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				in.Points, keys["points"] = &n, true
			}
		}
		yes := func(v string) *bool {
			b := oneOf(strings.ToLower(v), []string{"1", "yes", "true", "y", "x"})
			return &b
		}
		in.Pinned, in.Archived = yes(get(r, "pinned")), yes(get(r, "archived"))
		c := store.Card{Kind: "loyalty", Source: "import", Fields: []store.Field{}}
		if msg := in.apply(&c, keys); msg != "" {
			counts["skipped"]++
			continue
		}
		c.SourceRef = "csv:" + strings.ToLower(c.Name+"|"+c.Number+"|"+c.StartsAt)
		if _, err := s.store.CardBySource(ctx, uid, "import", c.SourceRef); err == nil {
			counts["already"]++
			continue
		}
		if err := s.store.CreateCard(ctx, uid, &c); err != nil {
			counts["skipped"]++
			continue
		}
		counts["cards"]++
	}
	return counts, nil
}

// ---- a Pocket Wallet export ----

// importPocket merges a Pocket Wallet export into the caller's own cards.
// Cards keep their identity (the export's ids, remembered per person), so
// importing the same file twice adds nothing. Calendar and trip links are
// not carried over: they belong to the account the file came from.
func (s *Server) importPocket(ctx context.Context, uid int64, doc exportDoc) map[string]int {
	counts := map[string]int{}
	ids := map[int64]int64{}
	for _, c := range doc.Cards {
		old := c.ID
		key := fmt.Sprintf("import:%d:card:%d", uid, old)
		if v, _, ok := s.store.CacheGet(ctx, key); ok {
			if n, _ := strconv.ParseInt(v, 10, 64); n > 0 {
				if _, err := s.store.GetCard(ctx, uid, n); err == nil {
					counts["already"]++
					continue
				}
			}
		}
		if c.Source == "" || c.SourceRef == "" {
			c.Source, c.SourceRef = "import", fmt.Sprintf("pocket:%d", old)
		}
		if _, err := s.store.CardBySource(ctx, uid, c.Source, c.SourceRef); err == nil {
			counts["already"]++
			continue
		}
		front, back, logo := c.FrontImage, c.BackImage, c.LogoImage
		c.CalEventID, c.TripID, c.TripName, c.FrontImage, c.BackImage, c.LogoImage = nil, nil, "", "", "", ""
		if !store.ValidKind(c.Kind) {
			c.Kind = "other"
		}
		if err := s.store.CreateCard(ctx, uid, &c); err != nil {
			counts["skipped"]++
			continue
		}
		ids[old] = c.ID
		_ = s.store.CachePut(ctx, key, itoa(c.ID))
		counts["cards"]++
		for side, name := range map[string]string{"front": front, "back": back, "logo": logo} {
			if data, ok := doc.Images[name]; ok && name != "" {
				if b, err := base64.StdEncoding.DecodeString(data); err == nil {
					if n, err := s.saveImage(uid, c.ID, side, b); err == nil {
						_ = s.store.SetImage(ctx, uid, c.ID, side, n)
					}
				}
			}
		}
	}
	for _, e := range doc.Balance {
		if id, ok := ids[e.CardID]; ok {
			e.CardID = id
			_ = s.store.InsertBalanceEntry(ctx, uid, e)
		}
	}
	return counts
}
