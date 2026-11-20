package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/config"
)

// moveLinks starts Wallet behind a fake Core whose pcx_<who>_<export|import|read>
// tokens are Pocket Move links acting for alice@ or bob@ (both members on
// the Core side, whatever they are here).
func moveLinks(t *testing.T) *app {
	t.Helper()
	coreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/auth/introspect" {
			io.WriteString(w, `{}`)
			return
		}
		var in struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		parts := strings.SplitN(strings.TrimPrefix(in.Token, "pcx_"), "_", 2)
		if len(parts) != 2 {
			io.WriteString(w, `{"active":false}`)
			return
		}
		scope := map[string]string{"export": core.ScopeMoveExport, "import": core.ScopeMoveImport, "read": "wallet:read"}[parts[1]]
		email := parts[0] + "@example.com"
		fmt.Fprintf(w, `{"active":true,"kind":"delegated","scope":%q,"audience":"wallet","user":{"id":1,"sub":%q,"email":%q,"name":%q,"role":"member"},"app":{"id":7,"slug":"move","name":"Pocket Move"}}`, scope, email, email, parts[0])
	}))
	t.Cleanup(coreSrv.Close)
	return newApp(t, func(c *config.Config) { c.CoreURL = coreSrv.URL })
}

func TestMoveLinkScopes(t *testing.T) {
	a := moveLinks(t)
	exp, imp, read := "pcx_bob_export", "pcx_bob_import", "pcx_bob_read"
	for _, c := range []struct {
		tok, method, path string
		want              int
	}{
		{exp, "GET", core.PortablePath, 200},
		{exp, "GET", "/api/v1/export.json", 200},
		{exp, "GET", "/api/v1/export.csv", 200},
		{exp, "POST", "/api/v1/import", 403},
		{exp, "POST", "/api/v1/import/pkpass", 403},
		{exp, "GET", "/api/v1/settings", 403},
		{exp, "GET", "/api/v1/cards", 403},
		{exp, "GET", "/api/v1/backup", 403},
		{exp, "GET", "/api/v1/users", 403},
		{imp, "GET", core.PortablePath, 200},
		{imp, "GET", "/api/v1/export.json", 403},
		{imp, "GET", "/api/v1/export.csv", 403},
		{imp, "PUT", "/api/v1/settings", 403},
		{imp, "POST", "/api/v1/cards", 403},
		{imp, "DELETE", "/api/v1/cards/1", 403},
		{read, "GET", core.PortablePath, 403},
		{read, "GET", "/api/v1/export.json", 403},
	} {
		if code, _, body := a.raw(c.tok, c.method, c.path, "application/json", []byte("{}")); code != c.want {
			t.Errorf("%s %s %s → %d, want %d (%s)", c.tok, c.method, c.path, code, c.want, body)
		}
	}
	_, _, body := a.raw(exp, "GET", core.PortablePath, "", nil)
	var p core.Portable
	if err := json.Unmarshal(body, &p); err != nil || len(p.Exports) != 2 || len(p.Imports) != 4 || p.Exports[0].Kind != core.KindPocket {
		t.Fatalf("portable: %s", body)
	}
	// Register offers move:export and move:import.
	m := a.s.manifest()
	if m.Portable == nil {
		t.Fatal("manifest has no Portable")
	}
}

// Move brings Alice's wallet to Bob: each import adds once, card numbers
// never cross from one person to the other.
func TestMoveImportsAddOnce(t *testing.T) {
	a := moveLinks(t)
	a.seed(a.alice)

	code, _, export := a.raw("pcx_alice_export", "GET", "/api/v1/export.json", "", nil)
	if code != 200 || !strings.Contains(string(export), "A4471029") {
		t.Fatalf("alice's export: %d %.200s", code, export)
	}
	code, _, csvOut := a.raw("pcx_alice_export", "GET", "/api/v1/export.csv", "", nil)
	if code != 200 || !strings.Contains(string(csvOut), "A4471029") {
		t.Fatalf("alice's csv: %d %.200s", code, csvOut)
	}
	for _, p := range []string{"/api/v1/export.json", "/api/v1/export.csv"} {
		if _, _, bob := a.raw("pcx_bob_export", "GET", p, "", nil); strings.Contains(string(bob), "A4471029") || strings.Contains(string(bob), "Konzum") {
			t.Fatalf("bob's %s holds alice's cards: %s", p, bob)
		}
	}

	imp := func(path, ctype string, body []byte) map[string]any {
		t.Helper()
		code, _, out := a.raw("pcx_bob_import", "POST", path, ctype, body)
		if code != 200 {
			t.Fatalf("POST %s → %d %s", path, code, out)
		}
		var m map[string]any
		_ = json.Unmarshal(out, &m)
		return m
	}
	first := imp("/api/v1/import", "application/json", export)
	if num(first, "cards") != 4 {
		t.Fatalf("first import: %v", first)
	}
	second := imp("/api/v1/import", "application/json", export)
	if num(second, "cards") != 0 || num(second, "already") != 4 {
		t.Fatalf("second import: %v", second)
	}
	if c := imp("/api/v1/import", "text/csv", csvOut); num(c, "cards") != 4 {
		t.Fatalf("csv import: %v", c)
	}
	if c := imp("/api/v1/import", "text/csv", csvOut); num(c, "cards") != 0 || num(c, "already") != 4 {
		t.Fatalf("second csv import: %v", c)
	}
	p1 := imp("/api/v1/import/pkpass", "application/vnd.apple.pkpass", makePkpass(t, "B2"))
	p2 := imp("/api/v1/import/pkpass", "application/vnd.apple.pkpass", makePkpass(t, "B2"))
	if num(p1, "added") != 1 || num(p2, "added") != 0 {
		t.Fatalf("pkpass imports: %v then %v", p1, p2)
	}

	if l := a.list(a.bob, "/api/v1/cards?archived=all"); len(l) != 9 {
		t.Fatalf("bob's cards: %d", len(l))
	}
	if l := a.list(a.alice, "/api/v1/cards?archived=all"); len(l) != 4 {
		t.Fatalf("alice's cards changed: %d", len(l))
	}
}
