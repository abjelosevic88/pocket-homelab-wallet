package server

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-wallet/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/pocket"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// ---- settings ----

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

// publicSettings is what one person sees: their settings with the secrets
// masked and, per Pocket app, the mode in effect (<app>_mode), whether it
// works and how (<app>_via: core | direct | "") and why not (<app>_detail).
func (s *Server) publicSettings(ctx context.Context, u *store.User, st store.Settings) map[string]any {
	out := map[string]any{
		"core_configured": s.core.Configured(), "core_registered": s.coreStatus()["registered"],
		"title": st.Title, "date_format": st.DateFormat, "time_format": st.TimeFormat, "week_start": st.WeekStart,
		"currency": st.Currency, "sort": st.Sort, "hide_numbers": st.HideNumbers,
		"notify_passes": st.NotifyPasses, "notify_before": st.NotifyBefore, "notify_expiry": st.NotifyExpiry, "expiry_days": st.ExpiryDays, "notify_hour": st.NotifyHour,
		"notify_url": st.NotifyURL, "notify_format": st.NotifyFormat, "notify_configured": st.NotifyURL != "",
		"travel_trips": st.TravelTrips, "calendar_passes": st.CalendarPasses, "calendar_id": st.CalendarID,
		"mail_scan": st.MailScan, "mail_days": st.MailDays, "money_spend": st.MoneySpend,
		"ai_provider": st.AIProvider, "ai_pocket_model": st.AIPocketModel, "openai_api_key": mask(st.OpenAIKey), "openai_model": st.OpenAIModel, "openai_base_url": st.OpenAIBaseURL,
		"setup_complete": st.SetupComplete, "base_url": s.cfg.BaseURL,
	}
	for _, a := range pocketApps {
		mode, via, detail := s.connStatus(ctx, u, st, a)
		out[a.slug+"_mode"], out[a.slug+"_via"], out[a.slug+"_detail"] = mode, via, detail
		out[a.slug+"_configured"] = via != ""
		_, url, token, public := connSettings(st, a)
		out[a.slug+"_url"], out[a.slug+"_token"], out[a.slug+"_public_url"] = url, mask(token), public
		if public == "" && via != "" {
			public = s.resolvePublic(ctx, a.slug)
		}
		out[a.slug+"_open_url"] = public
	}
	return out
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.GetSettings(r.Context(), actorID(r.Context()))
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), userFrom(r.Context()), st))
}

// handlePutSettings saves the caller's personal settings. The box-wide title
// is only taken from an admin.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r.Context())
	isAdmin := me.Role == "admin"
	cur, err := s.store.GetSettings(r.Context(), me.ID)
	if err != nil {
		s.fail(w, err, "settings")
		return
	}
	raw := map[string]any{}
	var in store.Settings
	keys, err := decodeKeys(r, &raw)
	if err != nil || remarshal(raw, &in) != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	bad := func(msg string) { writeErr(w, http.StatusBadRequest, msg) }
	if keys["title"] && isAdmin {
		cur.Title = strings.TrimSpace(in.Title)
		if cur.Title == "" {
			cur.Title = store.DefaultTitle
		}
	}
	if keys["date_format"] {
		cur.DateFormat = in.DateFormat
	}
	if keys["time_format"] {
		cur.TimeFormat = in.TimeFormat
	}
	if keys["week_start"] && (in.WeekStart == 1 || in.WeekStart == 7 || in.WeekStart == 6) {
		cur.WeekStart = in.WeekStart
	}
	if keys["currency"] {
		if c := cleanCurrency(in.Currency); c != "" {
			cur.Currency = c
		} else {
			bad("currency is a three-letter code like EUR")
			return
		}
	}
	if keys["sort"] && oneOf(in.Sort, []string{"used", "name", "recent"}) {
		cur.Sort = in.Sort
	}
	if keys["notify_before"] {
		if in.NotifyBefore < 0 || in.NotifyBefore > 24*60 {
			bad("that is 0 to 24 hours before")
			return
		}
		cur.NotifyBefore = in.NotifyBefore
	}
	if keys["expiry_days"] {
		if in.ExpiryDays < 1 || in.ExpiryDays > 365 {
			bad("that is 1 to 365 days")
			return
		}
		cur.ExpiryDays = in.ExpiryDays
	}
	if keys["mail_days"] {
		if in.MailDays < 1 || in.MailDays > 365 {
			bad("that is 1 to 365 days")
			return
		}
		cur.MailDays = in.MailDays
	}
	if keys["notify_hour"] {
		if in.NotifyHour < 0 || in.NotifyHour > 23 {
			bad("the hour is 0 to 23")
			return
		}
		cur.NotifyHour = in.NotifyHour
	}
	secret := func(k string, dst *string, v string) {
		if keys[k] && !strings.Contains(v, "••") {
			*dst = strings.TrimSpace(v)
		}
	}
	url := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimRight(strings.TrimSpace(v), "/")
		}
	}
	text := func(k string, dst *string, v string) {
		if keys[k] {
			*dst = strings.TrimSpace(v)
		}
	}
	boolIn := func(k string, dst *bool, v bool) {
		if keys[k] {
			*dst = v
		}
	}
	idIn := func(k string, dst *int64, v int64) {
		if keys[k] && v >= 0 {
			*dst = v
		}
	}
	boolIn("hide_numbers", &cur.HideNumbers, in.HideNumbers)
	boolIn("notify_passes", &cur.NotifyPasses, in.NotifyPasses)
	boolIn("notify_expiry", &cur.NotifyExpiry, in.NotifyExpiry)
	text("notify_url", &cur.NotifyURL, in.NotifyURL)
	if keys["notify_format"] && in.NotifyFormat != "" {
		cur.NotifyFormat = in.NotifyFormat
	}
	boolIn("travel_trips", &cur.TravelTrips, in.TravelTrips)
	boolIn("calendar_passes", &cur.CalendarPasses, in.CalendarPasses)
	idIn("calendar_id", &cur.CalendarID, in.CalendarID)
	boolIn("mail_scan", &cur.MailScan, in.MailScan)
	boolIn("money_spend", &cur.MoneySpend, in.MoneySpend)
	// each Pocket app: mode, URL, token, public URL
	inMap := map[string]string{}
	for k, v := range raw {
		if sv, ok := v.(string); ok {
			inMap[k] = sv
		}
	}
	for _, a := range pocketApps {
		c := cur.Conns[a.slug]
		if v := inMap[a.slug+"_mode"]; keys[a.slug+"_mode"] && (v == modeCore || v == modeDirect || v == modeOff) {
			c.Mode = v
		}
		url(a.slug+"_url", &c.URL, inMap[a.slug+"_url"])
		secret(a.slug+"_token", &c.Token, inMap[a.slug+"_token"])
		url(a.slug+"_public_url", &c.PublicURL, inMap[a.slug+"_public_url"])
		if c != (store.Conn{}) {
			cur.Conns[a.slug] = c
		}
	}
	if keys["ai_provider"] {
		if !validAIProvider(in.AIProvider) {
			bad("AI is automatic, Pocket AI or OpenAI")
			return
		}
		cur.AIProvider = in.AIProvider
	}
	if keys["ai_pocket_model"] {
		if !validPocketModel(in.AIPocketModel) {
			bad("unknown model")
			return
		}
		cur.AIPocketModel = in.AIPocketModel
	}
	secret("openai_api_key", &cur.OpenAIKey, in.OpenAIKey)
	text("openai_model", &cur.OpenAIModel, in.OpenAIModel)
	url("openai_base_url", &cur.OpenAIBaseURL, in.OpenAIBaseURL)
	if err := s.store.SaveUserSettings(r.Context(), me.ID, cur); err != nil {
		s.fail(w, err, "save settings")
		return
	}
	if isAdmin && keys["title"] {
		if err := s.store.SaveBoxSettings(r.Context(), cur); err != nil {
			s.fail(w, err, "save settings")
			return
		}
	}
	for k := range keys {
		if isModeKey(k) {
			s.reportModesSoon()
			break
		}
	}
	writeJSON(w, http.StatusOK, s.publicSettings(r.Context(), me, cur))
}

// handleIntegrationTest checks a connection with the caller's own saved
// settings (or the URL/key typed into the form).
func (s *Server) handleIntegrationTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Which  string `json:"which"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Format string `json:"format"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	uid := actorID(r.Context())
	st, _ := s.store.GetSettings(r.Context(), uid)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	keyOr := func(saved string) string {
		if strings.Contains(in.Key, "••") || in.Key == "" {
			return saved
		}
		return in.Key
	}
	urlOr := func(saved string) string {
		if strings.TrimSpace(in.URL) == "" {
			return saved
		}
		return strings.TrimRight(strings.TrimSpace(in.URL), "/")
	}
	var msg string
	var err error
	if a, ok := appBySlug(in.Which); ok {
		mode, savedURL, savedTok, _ := connSettings(st, a)
		typed := in.Key != "" && !strings.Contains(in.Key, "••")
		var conn *siblingConn
		if tok := keyOr(savedTok); tok != "" && (typed || s.connMode(mode, savedTok) == modeDirect) {
			base := urlOr(savedURL)
			if base == "" {
				base = s.resolveSibling(ctx, a.slug)
			}
			conn = &siblingConn{client: pocket.New(base, tok, 15*time.Second), via: modeDirect}
		} else {
			conn, err = s.clients(ctx, uid).conn(ctx, a, a.scopes...)
		}
		if err == nil {
			how := "direct connection"
			if conn.via == modeCore {
				how = "through Pocket Core"
			}
			var v string
			if v, err = conn.client.Health(ctx); err == nil {
				msg = fmt.Sprintf("%s %s, %s", a.name, v, how)
				switch a.slug {
				case "calendar":
					var cals []pocket.Calendar
					if cals, err = conn.client.Calendars(ctx); err == nil {
						msg += fmt.Sprintf(", %d calendars you can write to", len(cals))
					}
				case "travel":
					var trips []pocket.Trip
					if trips, err = conn.client.Trips(ctx); err == nil {
						msg += fmt.Sprintf(", %d trips", len(trips))
					}
				case "email":
					var files []pocket.MailFile
					if files, err = conn.client.MailPasses(ctx, 100); err == nil {
						msg += fmt.Sprintf(", %d %s in your email", len(files), plural(len(files), "pass", "passes"))
					}
				case "money":
					var pays []pocket.Payment
					if pays, err = conn.client.Payments(ctx, time.Now().AddDate(0, 0, -7).Format("2006-01-02"), store.Today()); err == nil {
						msg += fmt.Sprintf(", %d payments in the last week", len(pays))
					}
				}
			}
		}
	} else if in.Which == "openai" {
		c := ai.New(urlOr(st.OpenAIBaseURL), keyOr(st.OpenAIKey), st.OpenAIModel, 20*time.Second)
		var ids []string
		if ids, err = c.Models(ctx); err == nil {
			msg = fmt.Sprintf("The key works (%d models)", len(ids))
		}
	} else if in.Which == "notify" {
		f := in.Format
		if f == "" {
			f = st.NotifyFormat
		}
		n := notify.New(urlOr(st.NotifyURL), f)
		err = n.Send(ctx, notify.Message{Title: "Pocket Wallet", Body: notifyTestBody, Priority: "info", Tags: []string{"tickets"}})
		msg = "Test message sent via " + n.Format
	} else {
		writeErr(w, http.StatusBadRequest, "unknown integration")
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

// ---- system / backup / export ----

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	var size int64
	if fi, err := os.Stat(s.cfg.DBPath); err == nil {
		size = fi.Size()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"core":      s.coreStatus(),
		"auth_mode": s.cfg.AuthMode, "data_dir": s.cfg.DataDir, "db_path": s.cfg.DBPath, "db_bytes": size,
		"uptime": time.Since(s.started).Round(time.Second).String(), "timezone": time.Now().Location().String(),
		"counts": s.store.CountsFor(r.Context(), actorID(r.Context())),
	})
}

// handleBackup downloads the whole database, an admin-only box operation.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	dir := filepath.Join(s.cfg.DataDir, "backups")
	name := fmt.Sprintf("pocket-wallet-%s.db", time.Now().Format("20060102-150405"))
	dst := filepath.Join(dir, name)
	if err := s.store.Backup(r.Context(), dst); err != nil {
		s.fail(w, err, "backup")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	http.ServeFile(w, r, dst)
}

// exportDoc is the JSON export of one person's wallet, pictures included.
type exportDoc struct {
	App      string               `json:"app"`
	Version  string               `json:"version"`
	Exported string               `json:"exported_at"`
	Cards    []store.Card         `json:"cards"`
	Balance  []store.BalanceEntry `json:"balance_log"`
	Images   map[string]string    `json:"images"` // file name → base64
}

func (s *Server) export(ctx context.Context, uid int64) exportDoc {
	doc := exportDoc{App: "pocket-wallet", Version: Version, Exported: store.Now(), Images: map[string]string{}}
	doc.Cards, _ = s.store.ListCards(ctx, uid, store.CardFilter{Archived: "all", Sort: "recent"})
	doc.Balance, _ = s.store.AllBalanceLog(ctx, uid)
	names, _ := s.store.ImageNames(ctx, uid)
	for n := range names {
		if b, err := os.ReadFile(filepath.Join(s.imageDir(uid), n)); err == nil {
			doc.Images[n] = base64.StdEncoding.EncodeToString(b)
		}
	}
	return doc
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-wallet-export.json"`)
	writeJSON(w, http.StatusOK, s.export(r.Context(), actorID(r.Context())))
}

// handleExportCSV writes the cards as CSV rows (the same columns the CSV
// import reads back). Pictures are only in the JSON export.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cs, err := s.store.ListCards(ctx, actorID(ctx), store.CardFilter{Archived: "all", Sort: "name"})
	if err != nil {
		s.fail(w, err, "export")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pocket-wallet.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write(csvColumns)
	num := func(p *int64, minor bool) string {
		if p == nil {
			return ""
		}
		if minor {
			return strconv.FormatFloat(float64(*p)/100, 'f', 2, 64)
		}
		return strconv.FormatInt(*p, 10)
	}
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return ""
	}
	for _, c := range cs {
		_ = cw.Write([]string{c.Name, c.Kind, c.Issuer, c.Number, c.Holder, c.CodeFormat, c.CodeValue, c.Color, c.ValidFrom, c.ExpiresOn,
			c.StartsAt, c.EndsAt, c.Venue, c.Destination, c.Seat, c.Gate, c.Ref, num(c.Balance, true), c.Currency, num(c.Points, false), c.Tags, c.Note, yes(c.Pinned), yes(c.Archived)})
	}
	cw.Flush()
}
