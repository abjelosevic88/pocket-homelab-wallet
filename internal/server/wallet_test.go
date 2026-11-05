package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"

	"github.com/abjelosevic88/pocket-homelab-wallet/internal/config"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// seed gives a person a shop card, a gift card, a concert ticket and a passport.
func (a *app) seed(tok string) map[string]string {
	a.t.Helper()
	shop := a.ok(tok, "POST", "/api/v1/cards", map[string]any{"kind": "loyalty", "name": "Konzum Klub", "number": withCheck("290001734552"), "code_format": "ean13", "tags": "Groceries"})
	gift := a.ok(tok, "POST", "/api/v1/cards", map[string]any{"kind": "gift", "name": "BBI gift card", "balance": 5000, "currency": "bam"})
	gig := a.ok(tok, "POST", "/api/v1/cards", map[string]any{"kind": "ticket", "name": "Dubioza kolektiv", "starts_at": store.AddDays(store.Today(), 3) + "T20:00", "venue": "Zetra", "code_format": "qr", "code_value": "ENTRIO:1"})
	pass := a.ok(tok, "POST", "/api/v1/cards", map[string]any{"kind": "passport", "name": "Passport", "number": "A4471029", "expires_on": store.AddDays(store.Today(), 20)})
	return map[string]string{"shop": id(shop), "gift": id(gift), "gig": id(gig), "passport": id(pass)}
}

func (a *app) raw(auth, method, path, ctype string, body []byte) (int, http.Header, []byte) {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.url+path, bytes.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	req.Header.Set("Authorization", "Bearer "+auth)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header, b
}

func pngOf(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPerPersonIsolation(t *testing.T) {
	a := newApp(t, nil)
	ids := a.seed(a.alice)
	if code, _, _ := a.raw(a.alice, "PUT", "/api/v1/cards/"+ids["passport"]+"/images/front", "image/png", pngOf(t, image.NewRGBA(image.Rect(0, 0, 4, 4)))); code != 200 {
		t.Fatalf("picture: %d", code)
	}
	a.ok(a.alice, "POST", "/api/v1/cards/"+ids["gift"]+"/balance", map[string]any{"delta": -1200, "note": "Coffee"})
	// Bob sees none of it
	for _, p := range []string{"/api/v1/cards", "/api/v1/passes", "/api/v1/expiring?days=60", "/api/v1/search?q=konzum", "/api/v1/cards?archived=all"} {
		if l := a.list(a.bob, p); len(l) != 0 {
			t.Fatalf("bob sees alice's cards at %s: %v", p, l)
		}
	}
	if d := a.ok(a.bob, "GET", "/api/v1/day/"+store.AddDays(store.Today(), 3), nil); len(d["passes"].([]any)) != 0 {
		t.Fatalf("bob's day has alice's ticket: %v", d)
	}
	if o := a.ok(a.bob, "GET", "/api/v1/overview", nil); len(o["cards"].([]any)) != 0 || len(o["next"].([]any)) != 0 || len(o["ids"].([]any)) != 0 {
		t.Fatalf("bob's overview: %v", o)
	}
	// and can touch none of it
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/cards/" + ids["shop"]}, {"PUT", "/api/v1/cards/" + ids["shop"]}, {"DELETE", "/api/v1/cards/" + ids["shop"]},
		{"GET", "/api/v1/cards/" + ids["shop"] + "/code.svg"}, {"POST", "/api/v1/cards/" + ids["shop"] + "/used"},
		{"GET", "/api/v1/cards/" + ids["gift"] + "/balance"}, {"POST", "/api/v1/cards/" + ids["gift"] + "/balance"},
		{"GET", "/api/v1/cards/" + ids["passport"] + "/images/front"}, {"DELETE", "/api/v1/cards/" + ids["passport"] + "/images/front"},
		{"POST", "/api/v1/cards/" + ids["gig"] + "/calendar"}, {"GET", "/api/v1/cards/" + ids["shop"] + "/spent"},
	} {
		a.expect(404, a.bob, c.method, c.path, map[string]any{"name": "x", "delta": -1})
	}
	if code, _, _ := a.raw(a.bob, "PUT", "/api/v1/cards/"+ids["passport"]+"/images/back", "image/png", pngOf(t, image.NewRGBA(image.Rect(0, 0, 4, 4)))); code != 404 {
		t.Fatalf("bob put a picture on alice's passport: %d", code)
	}
	// and Alice's things are all still there
	got := a.ok(a.alice, "GET", "/api/v1/cards/"+ids["gift"], nil)
	if num(got, "balance") != 3800 {
		t.Fatalf("gift balance: %v", got["balance"])
	}
	if code, _, _ := a.raw(a.alice, "GET", "/api/v1/cards/"+ids["passport"]+"/images/front", "", nil); code != 200 {
		t.Fatalf("alice's picture: %d", code)
	}
}

func TestCardRules(t *testing.T) {
	a := newApp(t, nil)
	// the barcode carries the card number when nothing else is given
	c := a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"name": "IKEA Family", "number": "6275 9801 2345", "code_format": "code128"})
	if c["code_value"] != "627598012345" || c["kind"] != "loyalty" || c["group"] != "cards" {
		t.Fatalf("card: %v", c)
	}
	// a typed value without a format gets one
	e := a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"name": "dm", "code_value": withCheck("400554031872")})
	if e["code_format"] != "ean13" {
		t.Fatalf("guessed format: %v", e["code_format"])
	}
	// no name: the shop, else the kind
	if n := a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"issuer": "Shell"}); n["name"] != "Shell" {
		t.Fatalf("name: %v", n["name"])
	}
	a.expect(400, a.alice, "POST", "/api/v1/cards", map[string]any{"name": "X", "code_format": "ean13", "code_value": "12AB"})
	a.expect(400, a.alice, "POST", "/api/v1/cards", map[string]any{"name": "X", "kind": "spaceship"})
	a.expect(400, a.alice, "POST", "/api/v1/cards", map[string]any{"name": "X", "code_format": "hologram", "code_value": "1"})
	a.expect(400, a.alice, "POST", "/api/v1/cards", map[string]any{"name": "X", "expires_on": "next year"})
	a.expect(400, a.alice, "POST", "/api/v1/cards", map[string]any{"name": "X", "starts_at": "2026-12-01 18:00"})
	a.expect(400, a.alice, "POST", "/api/v1/cards", map[string]any{"name": "X", "color": "red"})
	a.expect(400, a.alice, "POST", "/api/v1/cards", map[string]any{"name": "X", "kind": "ticket", "starts_at": "2026-12-01T18:00", "ends_at": "2026-12-01T17:00"})
	// the code is drawn as SVG
	code, h, body := a.raw(a.alice, "GET", "/api/v1/cards/"+id(c)+"/code.svg", "", nil)
	if code != 200 || h.Get("Content-Type") != "image/svg+xml" || !bytes.HasPrefix(body, []byte("<svg")) || bytes.Contains(body, []byte("<script")) {
		t.Fatalf("svg: %d %s %.80s", code, h.Get("Content-Type"), body)
	}
	// and previewed before it is saved
	if code, _, body := a.raw(a.alice, "POST", "/api/v1/barcode", "application/json", []byte(`{"format":"qr","value":"hello"}`)); code != 200 || !bytes.HasPrefix(body, []byte("<svg")) {
		t.Fatalf("preview: %d", code)
	}
	a.expect(400, a.alice, "POST", "/api/v1/barcode", map[string]any{"format": "ean8", "value": "123"})
	// showing it counts, and the most used comes first
	for i := 0; i < 3; i++ {
		if code, _, _ := a.raw(a.alice, "POST", "/api/v1/cards/"+id(e)+"/used", "", nil); code != 204 {
			t.Fatalf("used: %d", code)
		}
	}
	if l := a.list(a.alice, "/api/v1/cards?group=cards"); id(l[0].(map[string]any)) != id(e) {
		t.Fatalf("most used first: %v", l[0])
	}
	// gift cards: spending needs something on the card first
	g := a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"kind": "gift", "name": "Voucher"})
	a.expect(400, a.alice, "POST", "/api/v1/cards/"+id(g)+"/balance", map[string]any{"delta": -100})
	a.ok(a.alice, "POST", "/api/v1/cards/"+id(g)+"/balance", map[string]any{"delta": 2500, "note": "Loaded"})
	r := a.ok(a.alice, "POST", "/api/v1/cards/"+id(g)+"/balance", map[string]any{"delta": -700})
	if num(r["card"].(map[string]any), "balance") != 1800 || len(a.list(a.alice, "/api/v1/cards/"+id(g)+"/balance")) != 2 {
		t.Fatalf("balance: %v", r)
	}
}

func TestLinkScopes(t *testing.T) {
	req := func(m, p string) *http.Request { return httptest.NewRequest(m, p, nil) }
	read, write := []string{"wallet:read"}, []string{"wallet:write"}
	for _, c := range []struct {
		scopes []string
		r      *http.Request
		want   bool
	}{
		{nil, req("GET", "/api/v1/auth/me"), true},
		{read, req("GET", "/api/v1/summary"), true},
		{read, req("GET", "/api/v1/cards"), true},
		{read, req("GET", "/api/v1/cards/3"), true},
		{read, req("GET", "/api/v1/cards/3/code.svg"), true},
		{read, req("GET", "/api/v1/cards/3/images/front"), true},
		{read, req("GET", "/api/v1/passes"), true},
		{read, req("GET", "/api/v1/expiring"), true},
		{read, req("GET", "/api/v1/day/2026-01-02"), true},
		{read, req("POST", "/api/v1/cards"), false},
		{write, req("GET", "/api/v1/cards"), false},
		{write, req("POST", "/api/v1/cards"), true},
		{write, req("PUT", "/api/v1/cards/4"), true},
		{write, req("DELETE", "/api/v1/cards/4"), true},
		{write, req("POST", "/api/v1/cards/4/balance"), true},
		{write, req("POST", "/api/v1/import/pkpass"), true},
		{write, req("PUT", "/api/v1/cards/4/images/front"), false},
		{write, req("POST", "/api/v1/cards/4/calendar"), false},
		{append(read, write...), req("GET", "/api/v1/settings"), false},
		{append(read, write...), req("POST", "/api/v1/tokens"), false},
		{append(read, write...), req("GET", "/api/v1/export.json"), false},
		{append(read, write...), req("POST", "/api/v1/import"), false},
		{append(read, write...), req("POST", "/api/v1/mail/scan"), false},
		{append(read, write...), req("GET", "/api/v1/links/travel/trips"), false},
	} {
		if got := linkAllows(c.scopes, c.r); got != c.want {
			t.Errorf("%v %s %s = %v, want %v", c.scopes, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
}

func TestExportImportIdempotent(t *testing.T) {
	a := newApp(t, nil)
	ids := a.seed(a.alice)
	a.ok(a.alice, "POST", "/api/v1/cards/"+ids["gift"]+"/balance", map[string]any{"delta": -1000})
	if code, _, _ := a.raw(a.alice, "PUT", "/api/v1/cards/"+ids["passport"]+"/images/front", "image/png", pngOf(t, image.NewRGBA(image.Rect(0, 0, 4, 4)))); code != 200 {
		t.Fatal("picture")
	}
	code, doc, _ := a.call(a.alice, "GET", "/api/v1/export.json", nil)
	if code != 200 || doc["app"] != "pocket-wallet" || len(doc["images"].(map[string]any)) != 1 {
		t.Fatalf("export: %d %v", code, doc["app"])
	}
	first := a.ok(a.bob, "POST", "/api/v1/import", doc)
	if num(first, "cards") != 4 {
		t.Fatalf("first import: %v", first)
	}
	second := a.ok(a.bob, "POST", "/api/v1/import", doc)
	if num(second, "cards") != 0 || num(second, "already") != 4 {
		t.Fatalf("second import: %v", second)
	}
	cards := a.list(a.bob, "/api/v1/cards?archived=all&sort=name")
	var gift, passport map[string]any
	for _, c := range cards {
		m := c.(map[string]any)
		switch m["kind"] {
		case "gift":
			gift = m
		case "passport":
			passport = m
		}
	}
	if num(gift, "balance") != 4000 || len(a.list(a.bob, "/api/v1/cards/"+id(gift)+"/balance")) != 1 {
		t.Fatalf("bob's gift card: %v", gift)
	}
	if passport["front_image"] == "" {
		t.Fatalf("the passport's picture did not come along: %v", passport)
	}
	a.expect(400, a.bob, "POST", "/api/v1/import", map[string]any{"app": "pocket-home"})
}

func TestCSVRoundTrip(t *testing.T) {
	a := newApp(t, nil)
	a.seed(a.alice)
	code, _, csv := a.raw(a.alice, "GET", "/api/v1/export.csv", "", nil)
	if code != 200 || !strings.HasPrefix(string(csv), "name,kind,issuer") {
		t.Fatalf("csv: %d %.60s", code, csv)
	}
	_, _, body := a.raw(a.bob, "POST", "/api/v1/import", "text/csv", csv)
	var res map[string]float64
	res = nil
	_ = json.Unmarshal(body, &res)
	if res["cards"] != 4 {
		t.Fatalf("csv import: %s", body)
	}
	_, _, body = a.raw(a.bob, "POST", "/api/v1/import", "text/csv", csv)
	res = nil
	_ = json.Unmarshal(body, &res)
	if res["cards"] != 0 || res["already"] != 4 {
		t.Fatalf("second csv import: %s", body)
	}
	// another app's columns
	other := "Store,Card number,Barcode type,Notes\nTesco,634004024,CODE_128,clubcard\n"
	_, _, body = a.raw(a.bob, "POST", "/api/v1/import", "text/csv", []byte(other))
	res = nil
	_ = json.Unmarshal(body, &res)
	if res["cards"] != 1 {
		t.Fatalf("aliases: %s", body)
	}
	if l := a.list(a.bob, "/api/v1/search?q=tesco"); len(l) != 1 || l[0].(map[string]any)["code_format"] != "code128" || l[0].(map[string]any)["code_value"] != "634004024" {
		t.Fatalf("tesco: %v", l)
	}
}

func TestCatimaImport(t *testing.T) {
	a := newApp(t, nil)
	csv := "2\n\n_id\nShops\nTravel\n\n_id,store,note,validfrom,expiry,balance,balancetype,cardid,barcodeid,barcodetype,barcodeencoding,headercolor,starstatus,lastused,archive\n" +
		"1,Lidl,,,,0,,4012345,,CODE_128,,-16776961,1,1700000000,0\n" +
		"2,Zara,gift from Ana,,1893456000000,25.50,EUR,ZR-1,ZR-1-PAY,QR_CODE,,-65536,0,0,0\n" +
		"3,Old gym,,,,0,,9,,,,-1,0,0,1\n\ncardId,groupId\n1,Shops\n2,Shops\n"
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("catima.csv")
	_, _ = w.Write([]byte(csv))
	w, _ = zw.Create("card_1_icon.png")
	_, _ = w.Write(pngOf(t, image.NewRGBA(image.Rect(0, 0, 8, 8))))
	_ = zw.Close()
	_, _, body := a.raw(a.alice, "POST", "/api/v1/import", "application/zip", zb.Bytes())
	var res map[string]float64
	res = nil
	_ = json.Unmarshal(body, &res)
	if res["cards"] != 3 {
		t.Fatalf("catima: %s", body)
	}
	cards := a.list(a.alice, "/api/v1/cards?archived=all&sort=name")
	byName := map[string]map[string]any{}
	for _, c := range cards {
		m := c.(map[string]any)
		byName[m["name"].(string)] = m
	}
	lidl, zara, gym := byName["Lidl"], byName["Zara"], byName["Old gym"]
	if lidl["code_format"] != "code128" || lidl["code_value"] != "4012345" || lidl["pinned"] != true || lidl["color"] != "#0000ff" || lidl["tags"] != "shops" || lidl["logo_image"] == "" {
		t.Fatalf("lidl: %v", lidl)
	}
	if zara["kind"] != "gift" || num(zara, "balance") != 2550 || zara["currency"] != "EUR" || zara["code_value"] != "ZR-1-PAY" || zara["code_format"] != "qr" || zara["expires_on"] == "" {
		t.Fatalf("zara: %v", zara)
	}
	if gym["archived"] != true || gym["code_format"] != "" {
		t.Fatalf("gym: %v", gym)
	}
	_, _, body = a.raw(a.alice, "POST", "/api/v1/import", "application/zip", zb.Bytes())
	res = nil
	_ = json.Unmarshal(body, &res)
	if res["cards"] != 0 {
		t.Fatalf("catima twice: %s", body)
	}
}

// makePkpass builds a small boarding pass the way airlines do: labels in a
// UTF-16 en.lproj/pass.strings, a PDF417 barcode, a logo.
func makePkpass(t *testing.T, gate string) []byte {
	t.Helper()
	pass := map[string]any{
		"formatVersion": 1, "passTypeIdentifier": "pass.com.example.air", "serialNumber": "K7Q2PL-1",
		"organizationName": "Example Air", "description": "Boarding pass", "backgroundColor": "rgb(216, 30, 5)",
		"relevantDate": "2026-12-01T06:00+01:00",
		"barcodes":     []map[string]any{{"message": "M1HODZIC/AMAR         EK7Q2PL SJJVIEOS 0784 335Y014C0042 100", "format": "PKBarcodeFormatPDF417", "messageEncoding": "iso-8859-1"}},
		"boardingPass": map[string]any{
			"transitType":     "PKTransitTypeAir",
			"headerFields":    []map[string]any{{"key": "gate", "label": "GATE", "value": gate}},
			"primaryFields":   []map[string]any{{"key": "origin", "label": "from_city", "value": "SJJ"}, {"key": "destination", "label": "to_city", "value": "VIE"}},
			"secondaryFields": []map[string]any{{"key": "passenger", "label": "PASSENGER", "value": "Amar Hodzic"}, {"key": "seat", "label": "SEAT", "value": "14C"}},
			"auxiliaryFields": []map[string]any{{"key": "flight", "label": "FLIGHT", "value": "OS 784"}, {"key": "boarding", "label": "BOARDING", "value": "2026-12-01T05:20+01:00", "timeStyle": "PKDateStyleShort", "dateStyle": "PKDateStyleNone"}},
			"backFields":      []map[string]any{{"key": "pnr", "label": "Booking reference", "value": "K7Q2PL"}},
		},
	}
	j, _ := json.Marshal(pass)
	strs := "\"from_city\" = \"Sarajevo\";\n\"to_city\" = \"Vienna\";\n"
	u := utf16.Encode([]rune(strs))
	sb := []byte{0xff, 0xfe}
	for _, r := range u {
		sb = binary.LittleEndian.AppendUint16(sb, r)
	}
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	for name, data := range map[string][]byte{"pass.json": j, "en.lproj/pass.strings": sb, "logo.png": pngOf(t, image.NewRGBA(image.Rect(0, 0, 8, 8))), "manifest.json": []byte("{}")} {
		w, _ := zw.Create(name)
		_, _ = w.Write(data)
	}
	_ = zw.Close()
	return zb.Bytes()
}

func TestPkpassImport(t *testing.T) {
	a := newApp(t, nil)
	_, _, body := a.raw(a.alice, "POST", "/api/v1/import/pkpass", "application/vnd.apple.pkpass", makePkpass(t, "B2"))
	var res struct {
		Cards []map[string]any `json:"cards"`
		Added int              `json:"added"`
	}
	res.Cards, res.Added = nil, 0
	_ = json.Unmarshal(body, &res)
	if res.Added != 1 || len(res.Cards) != 1 {
		t.Fatalf("pkpass: %s", body)
	}
	c := res.Cards[0]
	want := map[string]string{"kind": "boarding", "name": "Sarajevo → Vienna", "venue": "SJJ Sarajevo", "destination": "VIE Vienna", "seat": "14C", "gate": "B2",
		"ref": "OS 784", "holder": "Amar Hodzic", "starts_at": "2026-12-01T06:00", "code_format": "pdf417", "color": "#d81e05", "issuer": "Example Air", "source": "pkpass"}
	for k, v := range want {
		if c[k] != v {
			t.Errorf("%s = %v, want %q", k, c[k], v)
		}
	}
	if c["logo_image"] == "" {
		t.Error("no logo")
	}
	if f, _ := json.Marshal(c["fields"]); !strings.Contains(string(f), "K7Q2PL") || !strings.Contains(string(f), "05:20") {
		t.Errorf("fields: %s", f)
	}
	// the airline sends it again with a new gate: the same card, updated
	a.ok(a.alice, "PUT", "/api/v1/cards/"+id(c), map[string]any{"note": "window if possible"})
	_, _, body = a.raw(a.alice, "POST", "/api/v1/import/pkpass", "application/vnd.apple.pkpass", makePkpass(t, "C7"))
	res.Cards, res.Added = nil, 0
	_ = json.Unmarshal(body, &res)
	if res.Added != 0 || res.Cards[0]["gate"] != "C7" || res.Cards[0]["note"] != "window if possible" || id(res.Cards[0]) != id(c) {
		t.Fatalf("second pkpass: %s", body)
	}
	// the general import knows a .pkpass too
	_, _, body = a.raw(a.alice, "POST", "/api/v1/import", "", makePkpass(t, "C9"))
	if !strings.Contains(string(body), `"updated":1`) {
		t.Fatalf("import of a pkpass: %s", body)
	}
	a.expect(400, a.alice, "POST", "/api/v1/import/pkpass", map[string]any{"not": "a zip"})
}

func TestScanPhoto(t *testing.T) {
	a := newApp(t, nil)
	bc, err := qr.Encode("LIDLPLUS:40218830", qr.M, qr.Auto)
	if err != nil {
		t.Fatal(err)
	}
	bc, _ = barcode.Scale(bc, 300, 300)
	// on a grey photo, off centre
	img := image.NewRGBA(image.Rect(0, 0, 600, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 600; x++ {
			img.Set(x, y, color.RGBA{200, 200, 200, 255})
		}
	}
	for y := 0; y < 340; y++ {
		for x := 0; x < 340; x++ {
			img.Set(x+120, y+60, color.White)
		}
	}
	for y := 0; y < 300; y++ {
		for x := 0; x < 300; x++ {
			img.Set(x+140, y+80, bc.At(x, y))
		}
	}
	_, _, body := a.raw(a.alice, "POST", "/api/v1/scan", "image/png", pngOf(t, img))
	if !strings.Contains(string(body), `"format":"qr"`) || !strings.Contains(string(body), "LIDLPLUS:40218830") {
		t.Fatalf("scan: %s", body)
	}
	// a photo with nothing on it
	_, _, body = a.raw(a.alice, "POST", "/api/v1/scan", "image/png", pngOf(t, image.NewRGBA(image.Rect(0, 0, 50, 50))))
	if !strings.Contains(string(body), `"found":null`) {
		t.Fatalf("empty scan: %s", body)
	}
	// every format the form offers draws (and the 1D ones read back)
	for _, f := range codeFormats {
		v := map[string]string{"ean13": withCheck("400554031872"), "ean8": withCheck("9638507"), "upca": "03600029145", "upce": "04252614",
			"itf": "12345678", "codabar": "A40156B", "code39": "A0019274", "code93": "TEST93"}[f.Key]
		if v == "" {
			v = "HELLO-123"
		}
		if _, err := codeSVG(f.Key, v); err != nil {
			t.Errorf("%s: %v", f.Key, err)
		}
	}
}

func TestUpcomingDayAndExpiring(t *testing.T) {
	a := newApp(t, nil)
	today := store.Today()
	a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"kind": "ticket", "name": "Gone", "starts_at": store.AddDays(today, -2) + "T20:00"})
	a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"kind": "ticket", "name": "Later", "starts_at": store.AddDays(today, 5) + "T20:00"})
	a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"kind": "boarding", "name": "Soon", "starts_at": store.AddDays(today, 1) + "T06:00", "ref": "OS 784", "seat": "14C"})
	a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"kind": "id", "name": "ID card", "expires_on": store.AddDays(today, 10), "holder": "Alice"})
	a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"kind": "passport", "name": "Passport", "expires_on": store.AddDays(today, 400)})
	next := a.list(a.alice, "/api/v1/passes")
	if len(next) != 2 || next[0].(map[string]any)["name"] != "Soon" {
		t.Fatalf("upcoming: %v", next)
	}
	if d := a.ok(a.alice, "GET", "/api/v1/day/"+store.AddDays(today, -2), nil); len(d["passes"].([]any)) != 1 {
		t.Fatalf("day: %v", d)
	}
	if l := a.list(a.alice, "/api/v1/expiring"); len(l) != 1 || num(l[0].(map[string]any), "days_left") != 10 {
		t.Fatalf("expiring: %v", l)
	}
	sum := a.ok(a.alice, "GET", "/api/v1/summary", nil)
	if sum["next"].(map[string]any)["name"] != "Soon" || len(sum["expiring"].([]any)) != 1 || num(sum, "upcoming") != 2 {
		t.Fatalf("summary: %v", sum)
	}
	// the daily message: tomorrow's flight is not today's, the ID is due
	st, _ := a.st.GetSettings(context.Background(), a.ids["Alice"])
	body, _ := a.s.dailyCheck(context.Background(), a.ids["Alice"], st, time.Now(), false)
	if !strings.Contains(body, "ID card (Alice) runs out in 10 days") || strings.Contains(body, "Soon") {
		t.Fatalf("daily: %q", body)
	}
	if again, _ := a.s.dailyCheck(context.Background(), a.ids["Alice"], st, time.Now(), false); again != "" {
		t.Fatalf("sent twice: %q", again)
	}
	for _, c := range []struct{ left, days, want int }{{40, 30, -1}, {30, 30, 30}, {12, 30, 30}, {7, 30, 7}, {3, 30, 7}, {1, 30, 1}, {0, 30, 0}, {-1, 30, -1}} {
		if got := expiryStep(c.left, c.days); got != c.want {
			t.Errorf("expiryStep(%d, %d) = %d, want %d", c.left, c.days, got, c.want)
		}
	}
}

func TestSettingsPerPerson(t *testing.T) {
	a := newApp(t, nil)
	a.ok(a.alice, "PUT", "/api/v1/settings", map[string]any{"currency": "BAM", "notify_passes": false, "sort": "name", "expiry_days": 60, "travel_mode": "off", "hide_numbers": false})
	al := a.ok(a.alice, "GET", "/api/v1/settings", nil)
	bo := a.ok(a.bob, "GET", "/api/v1/settings", nil)
	if al["currency"] != "BAM" || al["notify_passes"] != false || al["sort"] != "name" || num(al, "expiry_days") != 60 || al["travel_mode"] != "off" || al["hide_numbers"] != false {
		t.Fatalf("alice: %v", al)
	}
	if bo["currency"] != "EUR" || bo["notify_passes"] != true || bo["sort"] != "used" || num(bo, "expiry_days") != 30 || bo["travel_mode"] == "off" || bo["hide_numbers"] != true {
		t.Fatalf("bob got alice's settings: %v", bo)
	}
	a.expect(400, a.alice, "PUT", "/api/v1/settings", map[string]any{"expiry_days": 0})
	a.expect(400, a.alice, "PUT", "/api/v1/settings", map[string]any{"notify_before": -5})
	// a member can't rename the box
	a.ok(a.bob, "PUT", "/api/v1/settings", map[string]any{"title": "Bob's"})
	if a.ok(a.alice, "GET", "/api/v1/settings", nil)["title"] != "Pocket Wallet" {
		t.Fatal("member renamed the box")
	}
}

func TestDemoSeeds(t *testing.T) {
	a := newApp(t, func(c *config.Config) {
		c.DemoData = true
		c.AuthMode = "local"
		c.AdminEmail = "demo@example.com"
		c.AdminPassword = "demo-password"
	})
	ctx := context.Background()
	owner := a.s.store.OwnerID(ctx)
	all, err := a.s.store.ListCards(ctx, owner, store.CardFilter{Archived: "all"})
	if err != nil || len(all) < 25 {
		t.Fatalf("demo cards: %d %v", len(all), err)
	}
	for _, c := range all {
		if c.CodeFormat != "" {
			if _, err := codeSVG(c.CodeFormat, c.CodeValue); err != nil {
				t.Errorf("%s: %v", c.Name, err)
			}
		}
	}
	if next := a.s.upcoming(ctx, owner, time.Now(), 0); len(next) < 5 {
		t.Fatalf("demo upcoming: %d", len(next))
	}
	soon, _ := a.s.store.ListCards(ctx, owner, store.CardFilter{Expiring: store.AddDays(store.Today(), 60)})
	if len(soon) < 3 {
		t.Fatalf("demo expiring: %d", len(soon))
	}
	var gift *store.Card
	for i := range all {
		if all[i].Name == "BBI Centar gift card" {
			gift = &all[i]
		}
	}
	log, _ := a.s.store.BalanceLog(ctx, owner, gift.ID)
	if len(log) != 3 || log[0].Balance != *gift.Balance {
		t.Fatalf("demo gift log: %+v (balance %d)", log, *gift.Balance)
	}
}

// Saving a card queues wallet.card_saved on the Core outbox, with the
// owner's email.
func TestCardSavedEvent(t *testing.T) {
	cfg := testConfig(t.TempDir())
	cfg.CoreURL = "http://core.invalid"
	st := openTestStore(t, cfg)
	s := New(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	ctx := context.Background()
	if err := s.core.Init(ctx); err != nil {
		t.Fatal(err)
	}
	u := &store.User{Email: "ana@example.com", Name: "Ana", Role: "admin"}
	if err := st.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	tok, _, _ := st.CreateAPIToken(ctx, u.ID, "t")
	ts := httptest.NewServer(s.Router())
	t.Cleanup(ts.Close)
	a := &app{t: t, url: ts.URL, s: s, st: st}
	c := a.ok(tok, "POST", "/api/v1/cards", map[string]any{"kind": "passport", "name": "Passport", "number": "A1", "expires_on": "2030-01-01"})
	a.expect(204, tok, "DELETE", "/api/v1/cards/"+id(c), nil)
	rows, err := st.DB.QueryContext(ctx, `SELECT body FROM pocket_outbox ORDER BY created_at, rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var topics []string
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatal(err)
		}
		var ev struct {
			Topic   string         `json:"topic"`
			Payload map[string]any `json:"payload"`
		}
		_ = json.Unmarshal([]byte(body), &ev)
		if ev.Payload["user_email"] != "ana@example.com" || ev.Payload["expires_on"] != "2030-01-01" {
			t.Fatalf("event: %s", body)
		}
		if strings.Contains(body, "A1") {
			t.Fatalf("the document number went on the bus: %s", body)
		}
		topics = append(topics, ev.Topic)
	}
	if strings.Join(topics, " ") != "wallet.card_saved wallet.card_removed" {
		t.Fatalf("topics: %v", topics)
	}
}

// The Core manifest must say wallet: a fork once registered as its parent.
func TestManifestSlug(t *testing.T) {
	a := newApp(t, nil)
	m := a.s.manifest()
	if m.Slug != "wallet" || m.Name != "Pocket Wallet" || m.EnvPrefix != "WALLET" {
		t.Fatalf("manifest: %s %s %s", m.Slug, m.Name, m.EnvPrefix)
	}
	for _, o := range m.Offers {
		if !strings.HasPrefix(o.Scope, "wallet:") {
			t.Fatalf("offer %s", o.Scope)
		}
	}
	for _, tp := range m.Publishes {
		if !strings.HasPrefix(tp, "wallet.") {
			t.Fatalf("topic %s", tp)
		}
	}
}
