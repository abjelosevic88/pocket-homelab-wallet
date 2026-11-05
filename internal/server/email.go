package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-wallet/internal/notify"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// ---- passes that arrive by email ----
//
// Airlines, trains, venues and shops send .pkpass files. With Pocket Email
// linked, Wallet looks for new ones every two hours (and when asked) and
// adds them; a pass sent again with a new gate or seat updates the card.
// Each attachment is read once.

// mailResult says what a look in the email found.
type mailResult struct {
	Connected bool          `json:"connected"`
	Detail    string        `json:"detail,omitempty"`
	Seen      int           `json:"seen"`
	Added     []*store.Card `json:"added"`
	Updated   int           `json:"updated"`
}

func (s *Server) scanMail(ctx context.Context, uid int64, st store.Settings) mailResult {
	res := mailResult{Added: []*store.Card{}}
	conn, err := s.clients(ctx, uid).conn(ctx, appEmail, scopeMailPasses)
	if err != nil {
		res.Detail = err.Error()
		return res
	}
	res.Connected = true
	files, err := conn.client.MailPasses(ctx, 100)
	if err != nil {
		res.Detail = "Pocket Email: " + err.Error()
		return res
	}
	days := st.MailDays
	if days <= 0 {
		days = 60
	}
	cut := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
	for _, f := range files {
		if len(f.Date) >= 10 && f.Date[:10] < cut {
			continue
		}
		res.Seen++
		key := fmt.Sprintf("mail:%d:%d", uid, f.ID)
		if _, _, done := s.store.CacheGet(ctx, key); done {
			continue
		}
		data, err := conn.client.MailFileData(ctx, f.ID)
		if err != nil {
			s.log.Debug("mail pass", "user", uid, "attachment", f.ID, "err", err)
			continue
		}
		_ = s.store.CachePut(ctx, key, "1")
		passes, err := parsePkpasses(data)
		if err != nil {
			continue
		}
		for _, p := range passes {
			if p.Card.StartsAt != "" && store.GroupOf(p.Card.Kind) == store.GroupPasses && passOver(p.Card, time.Now()) {
				continue // a flight that already left is not worth adding
			}
			c, created, err := s.importPass(ctx, uid, p)
			if err != nil {
				continue
			}
			if created {
				res.Added = append(res.Added, c)
			} else {
				res.Updated++
			}
		}
	}
	return res
}

// scanMailAll looks in everybody's email who has it switched on.
func (s *Server) scanMailAll(ctx context.Context) {
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	for _, uid := range ids {
		st, err := s.store.GetSettings(ctx, uid)
		if err != nil || !st.MailScan {
			continue
		}
		c, cancel := context.WithTimeout(ctx, 2*time.Minute)
		res := s.scanMail(c, uid, st)
		cancel()
		if len(res.Added) > 0 && st.NotifyPasses && st.NotifyURL != "" {
			var lines []string
			for _, card := range res.Added {
				lines = append(lines, "• "+store.KindLabel(card.Kind)+": "+passLine(*card))
			}
			n := notify.New(st.NotifyURL, st.NotifyFormat)
			title := "Added from your email"
			if err := n.Send(ctx, notify.Message{Title: title, Body: strings.Join(lines, "\n"), Priority: "info", Tags: []string{"tickets"}, Link: s.cfg.BaseURL + "/passes"}); err != nil {
				s.log.Warn("notify", "user", uid, "err", err)
			}
		}
	}
}

// handleMailScan looks in the caller's email now.
func (s *Server) handleMailScan(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	writeJSON(w, http.StatusOK, s.scanMail(ctx, uid, st))
}
