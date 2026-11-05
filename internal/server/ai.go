package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// aiFor is the person's AI: Pocket AI through Core, their own
// OpenAI-compatible key, or nil with why.
func (s *Server) aiFor(ctx context.Context, uid int64, st store.Settings) (*ai.Client, string, string) {
	email := ""
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		email = u.Email
	}
	pocket, status := s.core.SelectAI(ctx, email, st.AIProvider, st.OpenAIKey != "")
	if pocket != nil {
		if !status.Ready {
			return nil, "", "Pocket AI: " + status.Detail
		}
		c := ai.New("", "", "", 150*time.Second)
		c.Pocket = pocket.WithModel(st.AIPocketModel)
		return c, "pocket", ""
	}
	if st.OpenAIKey == "" {
		return nil, "", status.Detail
	}
	return ai.New(st.OpenAIBaseURL, st.OpenAIKey, st.OpenAIModel, 150*time.Second), "openai", ""
}

func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	cl, used, why := s.aiFor(ctx, actorID(ctx), st)
	writeJSON(w, http.StatusOK, map[string]any{"ready": cl != nil, "provider": used, "detail": why})
}

func (s *Server) handleAIModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	c, status := s.core.SelectAI(ctx, userFrom(ctx).Email, "pocket", false)
	if c == nil || !status.Ready {
		writeErr(w, http.StatusServiceUnavailable, status.Detail)
		return
	}
	models, err := c.Models(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

func validAIProvider(v string) bool { return core.ValidAIProvider(v) }

func validPocketModel(v string) bool { return core.ValidInferenceModel(v) }

const aiVoice = `You read a booking confirmation, an e-ticket or a membership email that one person pasted, and fill in a wallet card from it. Only use what the text says; leave a field empty when it is not there, never guess a seat, gate or number. Times are the local time where the event or departure happens, written YYYY-MM-DDTHH:MM (or YYYY-MM-DD when there is no time). When the text holds several flights or tickets, take the first one that has not happened yet, and say so in the note.`

// aiDraft is what the AI read from a pasted text: a card not saved yet.
type aiDraft struct {
	Kind        string        `json:"kind"`
	Name        string        `json:"name"`
	Issuer      string        `json:"issuer"`
	Number      string        `json:"number"`
	Holder      string        `json:"holder"`
	StartsAt    string        `json:"starts_at"`
	EndsAt      string        `json:"ends_at"`
	Venue       string        `json:"venue"`
	Destination string        `json:"destination"`
	Seat        string        `json:"seat"`
	Gate        string        `json:"gate"`
	Ref         string        `json:"ref"`
	ExpiresOn   string        `json:"expires_on"`
	CodeValue   string        `json:"code_value"`
	Fields      []store.Field `json:"fields"`
	Note        string        `json:"note"`
}

// handleAIRead turns a pasted booking email or ticket text into a draft card
// for the form. Only the pasted text goes to the AI.
func (s *Server) handleAIRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		Text string `json:"text"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if len(in.Text) < 20 {
		writeErr(w, http.StatusBadRequest, "paste the booking email or the ticket's text")
		return
	}
	if len(in.Text) > 30000 {
		in.Text = in.Text[:30000]
	}
	st, _ := s.store.GetSettings(ctx, uid)
	cl, used, why := s.aiFor(ctx, uid, st)
	if cl == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": why})
		return
	}
	var kinds []string
	for _, k := range store.Kinds {
		kinds = append(kinds, k.Key+" ("+k.Label+")")
	}
	prompt := fmt.Sprintf(`Today is %s.

Kinds: %s.

Text:
"""
%s
"""

Answer with JSON only: {"kind":"boarding","name":"Sarajevo → Memmingen","issuer":"Ryanair","number":"","holder":"","starts_at":"2026-12-01T06:40","ends_at":"2026-12-01T08:05","venue":"SJJ Sarajevo","destination":"FMM Memmingen","seat":"","gate":"","ref":"FR 4512","expires_on":"","code_value":"","fields":[{"label":"Booking reference","value":"K7Q2PL"}],"note":""}
For a flight, venue is where from and destination where to, ref the flight number; put the booking reference in fields. For a concert or match, name is the event, venue the place. code_value only when the text spells out what a barcode carries.`,
		time.Now().Format("Monday 2 January 2006"), strings.Join(kinds, ", "), in.Text)
	var out aiDraft
	if err := cl.JSON(ctx, aiVoice, prompt, &out); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if !store.ValidKind(out.Kind) {
		out.Kind = "ticket"
	}
	for _, p := range []*string{&out.StartsAt, &out.EndsAt} {
		if !validWhen(*p) {
			*p = ""
		}
	}
	if !validDate(out.ExpiresOn) {
		out.ExpiresOn = ""
	}
	if out.Fields == nil {
		out.Fields = []store.Field{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "provider": used, "draft": out})
}
