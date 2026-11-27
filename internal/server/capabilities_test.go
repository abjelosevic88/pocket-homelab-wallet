package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/config"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// fakeCapCore is a Pocket Core that vouches for link tokens
// pcx_<alice|bob>_<search|day|widget|read|write> from Pocket Hub and
// remembers the manifest Wallet registers with.
type fakeCapCore struct {
	*httptest.Server
	mu       sync.Mutex
	manifest map[string]any
}

func newCapApp(t *testing.T) (*app, *fakeCapCore) {
	t.Helper()
	fc := &fakeCapCore{}
	scopes := map[string]string{"search": core.ScopeSearch, "day": core.ScopeDay, "widget": core.ScopeWidget, "capture": core.ScopeCapture,
		"read": "wallet:read", "write": "wallet:write"}
	fc.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/apps/register":
			var m map[string]any
			_ = json.NewDecoder(r.Body).Decode(&m)
			fc.mu.Lock()
			fc.manifest = m
			fc.mu.Unlock()
			io.WriteString(w, `{}`)
		case "/api/v1/auth/introspect":
			var in struct{ Token string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			parts := strings.Split(in.Token, "_")
			if len(parts) != 3 || parts[0] != "pcx" || scopes[parts[2]] == "" {
				io.WriteString(w, `{"active":false}`)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"active": true, "kind": "delegated", "audience": "wallet", "scope": scopes[parts[2]],
				"user": map[string]any{"id": 1, "sub": parts[1] + "@example.com", "email": parts[1] + "@example.com", "name": parts[1], "role": "member"},
				"app":  map[string]any{"id": 9, "slug": "hub", "name": "Pocket Hub"}})
		default:
			io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(fc.Close)
	return newApp(t, func(c *config.Config) { c.CoreURL, c.CoreRegistrationKey = fc.URL, "key" }), fc
}

// items are the rows of a capability answer.
func items(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	l, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("no items list in %v", m)
	}
	out := []map[string]any{}
	for _, v := range l {
		out = append(out, v.(map[string]any))
	}
	return out
}

// item finds the row with that title.
func item(t *testing.T, m map[string]any, title string) map[string]any {
	t.Helper()
	for _, it := range items(t, m) {
		if it["title"] == title {
			return it
		}
	}
	t.Fatalf("no %q in %v", title, m["items"])
	return nil
}

// secrets are the numbers and codes seed and the tests give a person's
// cards; no capability answer may ever carry one of them.
var secrets = []string{"A4471029", "290001734552", "ENTRIO", "B7731", "KX9PQ2", "7781", "4402"}

// noSecrets fails when a raw answer carries a number, code or reference.
func (a *app) noSecrets(tok, path string) map[string]any {
	a.t.Helper()
	code, _, body := a.raw(tok, "GET", path, "", nil)
	if code != 200 {
		a.t.Fatalf("GET %s → %d %s", path, code, body)
	}
	for _, s := range secrets {
		if strings.Contains(string(body), s) {
			a.t.Fatalf("GET %s gives away %q: %s", path, s, body)
		}
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		a.t.Fatal(err)
	}
	return m
}

// papers gives a person a driving licence, a boarding pass for tomorrow with
// a booking reference, and a membership with a PIN in its details.
func (a *app) papers(tok string) map[string]string {
	a.t.Helper()
	today := store.Today()
	lic := a.ok(tok, "POST", "/api/v1/cards", map[string]any{"kind": "licence", "name": "Driving licence", "issuer": "MUP KS", "number": "B7731", "holder": "Alice",
		"expires_on": store.AddDays(today, 45), "note": "Categories B and AM"})
	fly := a.ok(tok, "POST", "/api/v1/cards", map[string]any{"kind": "boarding", "name": "Flight to Vienna", "issuer": "Austrian", "starts_at": store.AddDays(today, 1) + "T07:15",
		"venue": "SJJ Sarajevo", "destination": "VIE Vienna", "ref": "KX9PQ2", "seat": "12A", "code_format": "qr", "code_value": "M1ALICE KX9PQ2"})
	gym := a.ok(tok, "POST", "/api/v1/cards", map[string]any{"kind": "membership", "name": "Gym", "number": "4402", "fields": []map[string]any{{"label": "PIN", "value": "7781"}}})
	return map[string]string{"licence": id(lic), "flight": id(fly), "gym": id(gym)}
}

func TestPocketSearch(t *testing.T) {
	a := newApp(t, nil)
	ids := a.seed(a.alice)
	more := a.papers(a.alice)
	shop := items(t, a.noSecrets(a.alice, "/api/v1/pocket/search?q=konzum"))
	if len(shop) != 1 || shop[0]["id"] != ids["shop"] || shop[0]["title"] != "Konzum Klub" || shop[0]["kind"] != "loyalty card" || shop[0]["url"] != "/cards/"+ids["shop"] {
		t.Fatalf("konzum: %v", shop)
	}
	pass := item(t, a.noSecrets(a.alice, "/api/v1/pocket/search?q=passport"), "Passport")
	if pass["kind"] != "passport" || pass["snippet"] != "Expires "+longDate(store.AddDays(store.Today(), 20)) {
		t.Fatalf("passport: %v", pass)
	}
	gift := item(t, a.noSecrets(a.alice, "/api/v1/pocket/search?q=gift+card"), "BBI gift card")
	if gift["kind"] != "gift card" || gift["snippet"] != "50.00 BAM left" {
		t.Fatalf("gift card: %v", gift)
	}
	gig := item(t, a.noSecrets(a.alice, "/api/v1/pocket/search?q=zetra"), "Dubioza kolektiv")
	if gig["kind"] != "ticket" || gig["date"] != store.AddDays(store.Today(), 3) || !strings.HasSuffix(gig["snippet"].(string), "20:00 · Zetra") {
		t.Fatalf("ticket: %v", gig)
	}
	fly := item(t, a.noSecrets(a.alice, "/api/v1/pocket/search?q=boarding"), "Flight to Vienna")
	if fly["kind"] != "boarding pass" || !strings.HasPrefix(fly["snippet"].(string), "Austrian · ") || !strings.HasSuffix(fly["snippet"].(string), "07:15 · SJJ Sarajevo → VIE Vienna") {
		t.Fatalf("boarding pass: %v", fly)
	}
	lic := item(t, a.noSecrets(a.alice, "/api/v1/pocket/search?q=categories"), "Driving licence")
	if lic["id"] != more["licence"] || lic["kind"] != "driving licence" || lic["snippet"] != "MUP KS · Expires "+longDate(store.AddDays(store.Today(), 45)) {
		t.Fatalf("licence by its note: %v", lic)
	}
	// a name before a kind
	a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"kind": "loyalty", "name": "Passport Photo Studio"})
	if got := items(t, a.noSecrets(a.alice, "/api/v1/pocket/search?q=passport")); len(got) != 2 || got[0]["title"] != "Passport" || got[1]["title"] != "Passport Photo Studio" {
		t.Fatalf("order: %v", got)
	}
	// never found by a number, a barcode, a booking reference or a PIN
	for _, q := range []string{"A4471029", "4471", "290001734552", "ENTRIO", "B7731", "KX9PQ2", "7781", "4402", "alice", "??", "*", "·"} {
		if got := items(t, a.noSecrets(a.alice, "/api/v1/pocket/search?q="+q)); len(got) != 0 {
			t.Fatalf("found by %q: %v", q, got)
		}
	}
	// the archive comes after what is in use
	a.ok(a.alice, "PUT", "/api/v1/cards/"+ids["passport"], map[string]any{"archived": true})
	if got := items(t, a.noSecrets(a.alice, "/api/v1/pocket/search?q=passport")); len(got) != 2 || got[0]["title"] != "Passport Photo Studio" ||
		!strings.HasSuffix(got[1]["snippet"].(string), "In the archive") {
		t.Fatalf("archived: %v", got)
	}
	if got := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=a&limit=2", nil)); len(got) != 2 {
		t.Fatalf("limit 2: %v", got)
	}
	// another person's cards never appear
	for _, q := range []string{"konzum", "passport", "gift", "zetra", "vienna", "licence"} {
		if got := items(t, a.ok(a.bob, "GET", "/api/v1/pocket/search?q="+q, nil)); len(got) != 0 {
			t.Fatalf("bob finds alice's %q: %v", q, got)
		}
	}
	a.expect(401, "", "GET", "/api/v1/pocket/search?q=konzum", nil)
}

func TestPocketDay(t *testing.T) {
	a := newApp(t, nil)
	ids := a.seed(a.alice)
	more := a.papers(a.alice)
	today := store.Today()
	gone := a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"kind": "insurance", "name": "Health card", "issuer": "ZZO", "number": "4402", "expires_on": today})
	a.ok(a.alice, "POST", "/api/v1/cards/"+ids["gift"]+"/balance", map[string]any{"delta": -1200, "note": "Coffee"})
	a.ok(a.alice, "POST", "/api/v1/cards/"+ids["gift"]+"/balance", map[string]any{"delta": 500, "note": "Topped up"})

	day := a.noSecrets(a.alice, "/api/v1/pocket/day/"+today)
	if day["date"] != today || day["summary"] != "Health card expires, 12.00 BAM spent from a gift card" || len(items(t, day)) != 2 {
		t.Fatalf("today: %v", day)
	}
	if it := item(t, day, "Health card"); it["id"] != "expires:"+id(gone) || it["kind"] != "expires" || it["sub"] != "Insurance card · ZZO" || it["url"] != "/cards/"+id(gone) || it["time"] != nil {
		t.Fatalf("expires: %v", it)
	}
	spend := item(t, day, "BBI gift card")
	if spend["kind"] != "spent" || num(spend, "amount") != 1200 || spend["currency"] != "BAM" || spend["sub"] != "Coffee · 38.00 BAM left" || spend["url"] != "/cards/"+ids["gift"] ||
		len(spend["time"].(string)) != 5 || !strings.HasPrefix(spend["id"].(string), "spend:") {
		t.Fatalf("spend: %v", spend)
	}

	tomorrow := a.noSecrets(a.alice, "/api/v1/pocket/day/"+store.AddDays(today, 1))
	if tomorrow["summary"] != "1 pass" {
		t.Fatalf("tomorrow: %v", tomorrow)
	}
	if it := item(t, tomorrow, "Flight to Vienna"); it["id"] != "pass:"+more["flight"] || it["time"] != "07:15" || it["sub"] != "SJJ Sarajevo → VIE Vienna" || it["kind"] != "boarding pass" {
		t.Fatalf("flight: %v", it)
	}
	if it := item(t, a.noSecrets(a.alice, "/api/v1/pocket/day/"+store.AddDays(today, 3)), "Dubioza kolektiv"); it["time"] != "20:00" || it["sub"] != "Zetra" || it["kind"] != "ticket" {
		t.Fatalf("ticket: %v", it)
	}
	if d := a.noSecrets(a.alice, "/api/v1/pocket/day/"+store.AddDays(today, 20)); d["summary"] != "Passport expires" || item(t, d, "Passport")["kind"] != "expires" {
		t.Fatalf("the passport's day: %v", d)
	}
	// a spend falls on the caller's day
	for _, zone := range []string{"Pacific/Kiritimati", "Pacific/Pago_Pago"} {
		loc, _ := time.LoadLocation(zone)
		d := a.ok(a.alice, "GET", "/api/v1/pocket/day/"+time.Now().In(loc).Format("2006-01-02")+"?tz="+zone, nil)
		if !strings.Contains(d["summary"].(string), "12.00 BAM spent") {
			t.Fatalf("%s: %v", zone, d)
		}
	}
	// a day with nothing, and somebody else's day
	if d := a.ok(a.alice, "GET", "/api/v1/pocket/day/2019-03-04", nil); d["summary"] != nil || len(items(t, d)) != 0 {
		t.Fatalf("an empty day: %v", d)
	}
	for _, d := range []string{today, store.AddDays(today, 1), store.AddDays(today, 20)} {
		if got := a.ok(a.bob, "GET", "/api/v1/pocket/day/"+d, nil); got["summary"] != nil || len(items(t, got)) != 0 {
			t.Fatalf("bob sees alice's day: %v", got)
		}
	}
	a.expect(400, a.alice, "GET", "/api/v1/pocket/day/tomorrow", nil)
	// the old route still answers as before
	if old := a.ok(a.alice, "GET", "/api/v1/day/"+store.AddDays(today, 1), nil); len(old["passes"].([]any)) != 1 || old["passes"].([]any)[0].(map[string]any)["ref"] != "KX9PQ2" {
		t.Fatalf("/day changed: %v", old)
	}
}

func TestPocketWidget(t *testing.T) {
	a := newApp(t, nil)
	// an empty wallet: nothing to show
	if w := a.ok(a.bob, "GET", "/api/v1/pocket/widget", nil); w["line"] != nil || w["metrics"] != nil || len(items(t, w)) != 0 {
		t.Fatalf("empty: %v", w)
	}
	ids := a.seed(a.alice)
	w := a.noSecrets(a.alice, "/api/v1/pocket/widget")
	if w["line"] != "Passport expires in 20 days" || w["url"] != "/" {
		t.Fatalf("widget: %v", w)
	}
	rows := items(t, w)
	if len(rows) != 2 || rows[0]["title"] != "Passport" || rows[0]["detail"] != "Expires in 20 days" || rows[0]["id"] != ids["passport"] || rows[0]["url"] != "/cards/"+ids["passport"] ||
		rows[1]["title"] != "BBI gift card" || rows[1]["detail"] != "50.00 BAM left" {
		t.Fatalf("rows: %v", rows)
	}
	more := a.papers(a.alice)
	a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"kind": "ticket", "name": "Museum", "starts_at": store.Today(), "venue": "Zemaljski muzej"})
	a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"kind": "id", "name": "Old ID", "expires_on": store.AddDays(store.Today(), -5)})
	a.ok(a.alice, "POST", "/api/v1/cards", map[string]any{"kind": "loyalty", "name": "Later", "expires_on": store.AddDays(store.Today(), 90)})
	w = a.noSecrets(a.alice, "/api/v1/pocket/widget")
	rows = items(t, w)
	want := [][2]string{{"Museum", "Today · Zemaljski muzej"}, {"Flight to Vienna", "Tomorrow 07:15 · SJJ Sarajevo → VIE Vienna"}, {"Passport", "Expires in 20 days"},
		{"Driving licence", "Expires in 45 days"}, {"BBI gift card", "50.00 BAM left"}}
	if len(rows) != len(want) {
		t.Fatalf("rows: %v", rows)
	}
	for i, r := range rows {
		if r["title"] != want[i][0] || r["detail"] != want[i][1] {
			t.Fatalf("row %d: %v, want %v", i, r, want[i])
		}
	}
	if w["line"] != "Ticket for today" {
		t.Fatalf("line: %v", w["line"])
	}
	ms := w["metrics"].([]any)
	if len(ms) != 3 || ms[0].(map[string]any)["value"] != "3" || ms[1].(map[string]any)["value"] != "2" || ms[2].(map[string]any)["value"] != "1" {
		t.Fatalf("metrics: %v", ms)
	}
	// without today's ticket, the flight leads
	museum := items(t, a.ok(a.alice, "GET", "/api/v1/pocket/search?q=museum", nil))[0]["id"].(string)
	a.ok(a.alice, "DELETE", "/api/v1/cards/"+museum, nil)
	if w := a.ok(a.alice, "GET", "/api/v1/pocket/widget", nil); w["line"] != "Boarding pass for tomorrow 07:15" || items(t, w)[0]["id"] != more["flight"] {
		t.Fatalf("flight first: %v", w)
	}
	// a wallet with only an old loyalty card: nothing coming up, nothing to say
	a.ok(a.bob, "POST", "/api/v1/cards", map[string]any{"kind": "loyalty", "name": "Bob's shop"})
	if w := a.ok(a.bob, "GET", "/api/v1/pocket/widget", nil); w["line"] != nil || len(items(t, w)) != 0 || len(w["metrics"].([]any)) != 2 {
		t.Fatalf("bob's widget: %v", w)
	}
}

// A link token with one pocket:* scope reaches its one route and nothing else.
func TestPocketLinkTokens(t *testing.T) {
	a, _ := newCapApp(t)
	ids := a.seed(a.alice)
	today := store.Today()
	for _, c := range []struct {
		tok, method, path string
		body              any
		want              int
	}{
		{"pcx_alice_search", "GET", "/api/v1/pocket/search?q=konzum", nil, 200},
		{"pcx_alice_day", "GET", "/api/v1/pocket/day/" + today, nil, 200},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/widget", nil, 200},
		{"pcx_alice_widget", "GET", "/api/v1/auth/me", nil, 200},
		// another capability's route
		{"pcx_alice_search", "GET", "/api/v1/pocket/day/" + today, nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/search?q=konzum", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/search?q=konzum", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/pocket/day/" + today, nil, 403},
		// no quick capture here, whoever asks: the route is not there
		{"pcx_alice_capture", "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "req-0000000000000001", "text": "x"}, 404},
		{"pcx_alice_search", "POST", "/api/v1/pocket/capture", map[string]any{"request_id": "req-0000000000000001", "text": "x"}, 404},
		{"pcx_alice_capture", "GET", "/api/v1/pocket/widget", nil, 403},
		// the rest of the app
		{"pcx_alice_widget", "GET", "/api/v1/overview", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/summary", nil, 403},
		{"pcx_alice_widget", "GET", "/api/v1/expiring", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/search?q=konzum", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/cards", nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/cards/" + ids["passport"], nil, 403},
		{"pcx_alice_search", "GET", "/api/v1/cards/" + ids["shop"] + "/code.svg", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/day/" + today, nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/passes", nil, 403},
		{"pcx_alice_day", "GET", "/api/v1/cards/" + ids["gift"] + "/balance", nil, 403},
		{"pcx_alice_widget", "POST", "/api/v1/cards", map[string]any{"name": "x"}, 403},
		{"pcx_alice_widget", "GET", "/api/v1/settings", nil, 403},
		// the app's own scopes do not open the new routes
		{"pcx_alice_read", "GET", "/api/v1/pocket/search?q=konzum", nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/day/" + today, nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_write", "GET", "/api/v1/pocket/widget", nil, 403},
		{"pcx_alice_read", "GET", "/api/v1/search?q=konzum", nil, 200},
		{"pcx_nobody", "GET", "/api/v1/pocket/widget", nil, 401},
	} {
		if code, m, _ := a.call(c.tok, c.method, c.path, c.body); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%v)", c.tok, c.method, c.path, code, c.want, m)
		}
	}
	// the token acts for its person only, and never gives a number away
	if got := items(t, a.noSecrets("pcx_alice_search", "/api/v1/pocket/search?q=passport")); len(got) != 1 || got[0]["title"] != "Passport" {
		t.Fatalf("alice through the link: %v", got)
	}
	if w := a.noSecrets("pcx_alice_widget", "/api/v1/pocket/widget"); len(items(t, w)) != 2 {
		t.Fatalf("alice's widget through the link: %v", w)
	}
	if got := items(t, a.ok("pcx_bob_search", "GET", "/api/v1/pocket/search?q=konzum", nil)); len(got) != 0 {
		t.Fatalf("bob through the link finds alice's card: %v", got)
	}
	if w := a.ok("pcx_bob_widget", "GET", "/api/v1/pocket/widget", nil); len(items(t, w)) != 0 {
		t.Fatalf("bob's widget through the link: %v", w)
	}
	if d := a.ok("pcx_bob_day", "GET", "/api/v1/pocket/day/"+store.AddDays(today, 3), nil); len(items(t, d)) != 0 {
		t.Fatalf("bob's day through the link: %v", d)
	}
}

func TestPocketLinkScopes(t *testing.T) {
	req := func(m, p string) *http.Request { return httptest.NewRequest(m, p, nil) }
	for _, c := range []struct {
		scope string
		r     *http.Request
		want  bool
	}{
		{core.ScopeSearch, req("GET", "/api/v1/pocket/search?q=x"), true},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/2026-01-02"), true},
		{core.ScopeWidget, req("GET", "/api/v1/pocket/widget"), true},
		{core.ScopeWidget, req("GET", "/api/v1/auth/me"), true},
		{core.ScopeCapture, req("POST", "/api/v1/pocket/capture"), false},
		{core.ScopeSearch, req("POST", "/api/v1/pocket/search"), false},
		{core.ScopeDay, req("GET", "/api/v1/pocket/day/today"), false},
		{core.ScopeDay, req("GET", "/api/v1/day/2026-01-02"), false},
		{core.ScopeWidget, req("GET", "/api/v1/summary"), false},
		{core.ScopeSearch, req("GET", "/api/v1/search"), false},
		{"wallet:read", req("GET", "/api/v1/pocket/widget"), false},
		{"wallet:read", req("GET", "/api/v1/pocket/search"), false},
		{"wallet:write", req("POST", "/api/v1/pocket/capture"), false},
	} {
		if got := linkAllows([]string{c.scope}, c.r); got != c.want {
			t.Errorf("%s %s %s = %v, want %v", c.scope, c.r.Method, c.r.URL.Path, got, c.want)
		}
	}
}

// Wallet tells Core what it can do, so Search, Day and Hub find it on their own.
func TestManifestCapabilities(t *testing.T) {
	a, fc := newCapApp(t)
	if err := a.s.registerWithCore(context.Background()); err != nil {
		t.Fatal(err)
	}
	fc.mu.Lock()
	m := fc.manifest
	fc.mu.Unlock()
	if m["slug"] != "wallet" || m["category"] != "Money" {
		t.Fatalf("manifest: %v %v", m["slug"], m["category"])
	}
	search, _ := m["search"].(map[string]any)
	day, _ := m["day"].(map[string]any)
	widget, _ := m["widget"].(map[string]any)
	if search == nil || day == nil || widget == nil || m["capture"] != nil {
		t.Fatalf("capabilities: %v %v %v %v", search, day, widget, m["capture"])
	}
	if b := search["bangs"].([]any); len(b) != 2 || b[0] != "wallet" || b[1] != "card" {
		t.Fatalf("bangs: %v", b)
	}
	if k := search["kinds"].([]any); len(k) != len(store.Kinds) || k[0] != "loyalty card" || k[4] != "boarding pass" {
		t.Fatalf("kinds: %v", k)
	}
	if day["label"] != "Wallet" || day["one"] != "pass" || day["many"] != "passes" || widget["title"] != "Wallet" {
		t.Fatalf("day %v, widget %v", day, widget)
	}
	offered := map[string]bool{}
	for _, o := range m["offers"].([]any) {
		offered[o.(map[string]any)["scope"].(string)] = true
	}
	for _, sc := range []string{"wallet:read", "wallet:write", core.ScopeMoveExport, core.ScopeSearch, core.ScopeDay, core.ScopeWidget} {
		if !offered[sc] {
			t.Errorf("%s is not offered", sc)
		}
	}
	if offered[core.ScopeCapture] {
		t.Error("pocket:capture is offered")
	}
}
