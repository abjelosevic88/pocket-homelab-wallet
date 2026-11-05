// Package pocket talks to sibling Pocket apps: Pocket Travel (trips),
// Pocket Calendar (tickets and flights as events), Pocket Email (passes that
// arrive as .pkpass attachments) and Pocket Money (what you spent at the shop
// of a loyalty card). Same bearer-token JSON API everywhere.
package pocket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a bearer-token JSON client for one Pocket app.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New creates a client.
func New(baseURL, token string, timeout time.Duration) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Token: token, HTTP: &http.Client{Timeout: timeout}}
}

// Configured reports whether URL and token are set.
func (c *Client) Configured() bool { return c != nil && c.BaseURL != "" && c.Token != "" }

// ErrNotFound is a 404 from the sibling app.
var ErrNotFound = errors.New("not found")

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, v any) error {
	if !c.Configured() {
		return errors.New("not configured")
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errors.New("token rejected (401)")
	}
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		b, _ := io.ReadAll(io.LimitReader(res.Body, 2000))
		_ = json.Unmarshal(b, &e)
		if e.Error != "" {
			return fmt.Errorf("HTTP %d: %s", res.StatusCode, e.Error)
		}
		return fmt.Errorf("HTTP %d", res.StatusCode)
	}
	if v == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// Health is the connection test for any Pocket app: /healthz plus a token check.
func (c *Client) Health(ctx context.Context) (string, error) {
	var out struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if !c.Configured() {
		return "", errors.New("not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/healthz", nil)
	if err != nil {
		return "", err
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	var me map[string]any
	if err := c.do(ctx, http.MethodGet, "/api/v1/auth/me", nil, nil, &me); err != nil {
		return out.Version, err
	}
	return out.Version, nil
}

// Do is a raw JSON call (for a connection test).
func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body, v any) error {
	return c.do(ctx, method, path, q, body, v)
}

// list decodes a list that may be bare or wrapped in {key:[…]} / {results:[…]}.
func list[T any](raw json.RawMessage, keys ...string) ([]T, error) {
	var out []T
	if err := json.Unmarshal(raw, &out); err == nil {
		return out, nil
	}
	var wrap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return nil, err
	}
	for _, k := range append(keys, "results", "items") {
		if b, ok := wrap[k]; ok {
			if err := json.Unmarshal(b, &out); err != nil {
				return nil, err
			}
			return out, nil
		}
	}
	return []T{}, nil
}

// raw downloads a body as bytes (an attachment), at most limit bytes.
func (c *Client) raw(ctx context.Context, path string, q url.Values, limit int64) ([]byte, string, error) {
	if !c.Configured() {
		return nil, "", errors.New("not configured")
	}
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return nil, "", errors.New("token rejected (401)")
	case res.StatusCode == http.StatusNotFound:
		return nil, "", ErrNotFound
	case res.StatusCode >= 300:
		return nil, "", fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(b)) > limit {
		return nil, "", errors.New("too large")
	}
	return b, res.Header.Get("Content-Type"), nil
}

// ---------------- Pocket Travel ----------------

// Trip is a trip in Pocket Travel.
type Trip struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Destination string `json:"destination"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
	Status      string `json:"status"`
	Archived    bool   `json:"archived"`
}

// Trips lists the person's trips.
func (c *Client) Trips(ctx context.Context) ([]Trip, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/trips", nil, nil, &raw); err != nil {
		return nil, err
	}
	return list[Trip](raw, "trips")
}

// ---------------- Pocket Calendar ----------------

// Calendar is one of the person's calendars.
type Calendar struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Writable bool   `json:"writable"`
	Default  bool   `json:"is_default"`
}

// Calendars lists the calendars the person can write to.
func (c *Client) Calendars(ctx context.Context) ([]Calendar, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/v1/calendars", nil, nil, &raw); err != nil {
		return nil, err
	}
	all, err := list[Calendar](raw, "calendars")
	if err != nil {
		return nil, err
	}
	out := []Calendar{}
	for _, cal := range all {
		if cal.Writable {
			out = append(out, cal)
		}
	}
	return out, nil
}

// Event is a ticket or a flight on the calendar: from its start to its end
// (or two hours), in the time zone of the box.
type Event struct {
	CalendarID  int64
	Title       string
	Description string
	Location    string
	URL         string
	Start       string // YYYY-MM-DDTHH:MM, or YYYY-MM-DD for all day
	End         string
}

func (e Event) body() map[string]any {
	b := map[string]any{"title": e.Title, "description": e.Description, "location": e.Location, "url": e.URL, "categories": []string{"Wallet"}}
	start, err := time.Parse("2006-01-02T15:04", e.Start)
	if err != nil {
		b["all_day"], b["start"], b["busy"] = true, e.Start, false
	} else {
		end, err := time.Parse("2006-01-02T15:04", e.End)
		if err != nil || !end.After(start) {
			end = start.Add(2 * time.Hour)
		}
		b["all_day"], b["start"], b["end"] = false, start.Format("2006-01-02T15:04"), end.Format("2006-01-02T15:04")
	}
	if e.CalendarID > 0 {
		b["calendar_id"] = e.CalendarID
	}
	return b
}

// CreateEvent adds the event and returns its id.
func (c *Client) CreateEvent(ctx context.Context, e Event) (int64, error) {
	var out struct {
		EventID int64 `json:"event_id"`
		ID      any   `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/events", nil, e.body(), &out); err != nil {
		return 0, err
	}
	if out.EventID > 0 {
		return out.EventID, nil
	}
	// a bare {"id": 12} or an occurrence id like "12:20261104"
	var id int64
	_, _ = fmt.Sscan(strings.SplitN(fmt.Sprint(out.ID), ":", 2)[0], &id)
	return id, nil
}

// UpdateEvent moves or renames the event.
func (c *Client) UpdateEvent(ctx context.Context, id int64, e Event) error {
	b := e.body()
	delete(b, "calendar_id")
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/api/v1/events/%d", id), nil, b, nil)
}

// DeleteEvent removes the event; one already gone counts as deleted.
func (c *Client) DeleteEvent(ctx context.Context, id int64) error {
	err := c.do(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/events/%d", id), nil, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ---------------- Pocket Email ----------------

// MailFile is an attachment in Pocket Email.
type MailFile struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	Filename  string `json:"filename"`
	MIME      string `json:"mime"`
	Size      int64  `json:"size"`
	Subject   string `json:"subject"`
	Date      string `json:"date"`
	From      struct {
		Name string `json:"name"`
		Addr string `json:"addr"`
	} `json:"from"`
}

// MailPasses lists the .pkpass files in the person's email, newest first.
func (c *Client) MailPasses(ctx context.Context, limit int) ([]MailFile, error) {
	var raw json.RawMessage
	q := url.Values{"limit": {fmt.Sprint(limit)}}
	if err := c.do(ctx, http.MethodGet, "/api/v1/passes", q, nil, &raw); err != nil {
		return nil, err
	}
	all, err := list[MailFile](raw, "passes")
	if err != nil {
		return nil, err
	}
	out := []MailFile{}
	for _, f := range all {
		if strings.HasSuffix(strings.ToLower(f.Filename), ".pkpass") || strings.Contains(strings.ToLower(f.MIME), "pkpass") {
			out = append(out, f)
		}
	}
	return out, nil
}

// MailFileData downloads one attachment (at most 10 MB).
func (c *Client) MailFileData(ctx context.Context, id int64) ([]byte, error) {
	b, _, err := c.raw(ctx, fmt.Sprintf("/api/v1/passes/%d", id), nil, 10<<20)
	return b, err
}

// ---------------- Pocket Money ----------------

// Payment is an expense in Pocket Money.
type Payment struct {
	ID              int64  `json:"id"`
	Kind            string `json:"kind"`
	Date            string `json:"date"`
	Amount          int64  `json:"amount"`   // minor units of Currency (the account's)
	Currency        string `json:"currency"` // the account's
	ForeignAmount   int64  `json:"foreign_amount"`
	ForeignCurrency string `json:"foreign_currency"`
	Payee           string `json:"payee"`
	Description     string `json:"description"`
}

// Paid is what was paid in the currency it was paid in.
func (p Payment) Paid() (int64, string) {
	amt, cur := p.Amount, p.Currency
	if p.ForeignAmount != 0 && p.ForeignCurrency != "" {
		amt, cur = p.ForeignAmount, p.ForeignCurrency
	}
	if amt < 0 {
		amt = -amt
	}
	return amt, cur
}

// Payments lists the expenses between two days (YYYY-MM-DD).
func (c *Client) Payments(ctx context.Context, from, to string) ([]Payment, error) {
	var raw json.RawMessage
	q := url.Values{"from": {from}, "to": {to}, "kind": {"expense"}, "limit": {"1000"}}
	if err := c.do(ctx, http.MethodGet, "/api/v1/transactions", q, nil, &raw); err != nil {
		return nil, err
	}
	return list[Payment](raw, "items", "transactions")
}
