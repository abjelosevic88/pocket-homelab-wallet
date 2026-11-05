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

// pushes runs every minute: once a day, at each person's own hour, one
// message with today's tickets and flights and what runs out soon; and,
// a while before a pass starts, "your pass is ready" with a link that
// opens it full screen.
func (s *Server) pushes(ctx context.Context) {
	ids, err := s.store.SettingsUsers(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for _, uid := range ids {
		st, err := s.store.GetSettings(ctx, uid)
		if err != nil {
			continue
		}
		s.passSoon(ctx, uid, st, now)
		if now.Hour() != st.NotifyHour || now.Minute() > 10 {
			continue
		}
		if !s.once(ctx, fmt.Sprintf("push:%d:%s", uid, store.Today())) {
			continue
		}
		s.dailyCheck(ctx, uid, st, now, false)
	}
}

// expiryStep is the reminder an end date is due for: the smallest of
// (0, 1, 7, the person's days) that is not before it. -1 = none yet.
func expiryStep(left, days int) int {
	if left < 0 {
		return -1
	}
	for _, t := range []int{0, 1, 7, days} {
		if left <= t {
			return t
		}
	}
	return -1
}

func passLine(c store.Card) string {
	var b strings.Builder
	if len(c.StartsAt) > 10 {
		b.WriteString(c.StartsAt[11:] + " ")
	}
	b.WriteString(c.Name)
	if c.Ref != "" && !strings.Contains(c.Name, c.Ref) {
		b.WriteString(" (" + c.Ref + ")")
	}
	var more []string
	if c.Kind != "boarding" && c.Venue != "" && !strings.Contains(c.Name, c.Venue) {
		more = append(more, c.Venue)
	}
	if c.Gate != "" {
		more = append(more, "gate "+c.Gate)
	}
	if c.Seat != "" {
		more = append(more, "seat "+c.Seat)
	}
	if len(more) > 0 {
		b.WriteString(", " + strings.Join(more, ", "))
	}
	return b.String()
}

func runsOut(left int) string {
	switch {
	case left == 0:
		return "runs out today"
	case left == 1:
		return "runs out tomorrow"
	}
	return fmt.Sprintf("runs out in %d days", left)
}

// dailyCheck builds and sends one person's daily message (force: send what
// is due again, for "Send it now").
func (s *Server) dailyCheck(ctx context.Context, uid int64, st store.Settings, now time.Time, force bool) (string, bool) {
	today := now.Format("2006-01-02")
	var lines []string
	if st.NotifyPasses {
		for _, c := range s.upcoming(ctx, uid, now, 0) {
			if c.Day() != today {
				continue
			}
			lines = append(lines, "• Today "+passLine(c))
			if !force {
				s.publish(ctx, uid, "wallet.pass_today", "card:"+itoa(c.ID), cardPayload(&c))
			}
		}
	}
	if st.NotifyExpiry {
		soon, _ := s.store.ListCards(ctx, uid, store.CardFilter{Expiring: store.AddDays(today, st.ExpiryDays), Sort: "expires"})
		for _, c := range soon {
			left := daysLeft(c.ExpiresOn, now)
			step := expiryStep(left, st.ExpiryDays)
			if step < 0 {
				continue
			}
			if !force && !s.once(ctx, fmt.Sprintf("exp:%d:%d:%s:%d", uid, c.ID, c.ExpiresOn, step)) {
				continue
			}
			who := ""
			if c.Holder != "" && c.Group == store.GroupIDs {
				who = " (" + c.Holder + ")"
			}
			lines = append(lines, fmt.Sprintf("• %s%s %s", c.Name, who, runsOut(left)))
			if !force {
				p := cardPayload(&c)
				p["days_left"] = left
				s.publish(ctx, uid, "wallet.expiring", "card:"+itoa(c.ID), p)
			}
		}
	}
	body := strings.Join(lines, "\n")
	if len(lines) == 0 || st.NotifyURL == "" {
		return body, false
	}
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Your wallet today", Body: body, Priority: "info", Tags: []string{"tickets"}, Link: s.cfg.BaseURL + "/"}); err != nil {
		s.log.Warn("notify", "user", uid, "err", err)
		return body, false
	}
	return body, true
}

// passSoon sends "your pass is ready" a while before a timed pass starts.
func (s *Server) passSoon(ctx context.Context, uid int64, st store.Settings, now time.Time) {
	if st.NotifyBefore <= 0 {
		return
	}
	for _, c := range s.upcoming(ctx, uid, now, 6) {
		if len(c.StartsAt) <= 10 {
			continue
		}
		start, err := time.ParseInLocation("2006-01-02T15:04", c.StartsAt, time.Local)
		if err != nil || now.Before(start.Add(-time.Duration(st.NotifyBefore)*time.Minute)) || now.After(start) {
			continue
		}
		if !s.once(ctx, fmt.Sprintf("soon:%d:%d:%s", uid, c.ID, c.StartsAt)) {
			continue
		}
		s.publish(ctx, uid, "wallet.pass_soon", "card:"+itoa(c.ID), cardPayload(&c))
		if st.NotifyURL == "" {
			continue
		}
		title := "Your ticket is ready"
		if c.Kind == "boarding" {
			title = "Your boarding pass is ready"
		}
		link := strings.TrimRight(s.cfg.BaseURL, "/") + "/cards/" + itoa(c.ID) + "?show=1"
		n := notify.New(st.NotifyURL, st.NotifyFormat)
		if err := n.Send(ctx, notify.Message{Title: title, Body: passLine(c), Priority: "high", Tags: []string{"tickets"}, Link: link}); err != nil {
			s.log.Warn("notify", "user", uid, "err", err)
		}
	}
}

// once reports true the first time a key is seen.
func (s *Server) once(ctx context.Context, key string) bool {
	if _, _, ok := s.store.CacheGet(ctx, "once:"+key); ok {
		return false
	}
	_ = s.store.CachePut(ctx, "once:"+key, "1")
	return true
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	n := notify.New(st.NotifyURL, st.NotifyFormat)
	if err := n.Send(ctx, notify.Message{Title: "Pocket Wallet", Body: notifyTestBody, Priority: "info", Tags: []string{"tickets"}}); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCheckNow runs the daily check for the caller now and says what it sent.
func (s *Server) handleCheckNow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	st, _ := s.store.GetSettings(ctx, uid)
	body, sent := s.dailyCheck(ctx, uid, st, time.Now(), true)
	writeJSON(w, http.StatusOK, map[string]any{"sent": sent, "body": body})
}

const notifyTestBody = "Notifications work. Today's tickets and flights, \"your pass is ready\" before they start and IDs or cards that run out soon will arrive here."
