package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

const cardCols = `id, kind, name, issuer, number, holder, code_format, code_value, color, note, tags, fields, pinned, archived,
	valid_from, expires_on, starts_at, ends_at, venue, destination, seat, gate, ref, balance, currency, points,
	trip_id, trip_name, cal_event_id, front_image, back_image, logo_image, uses, last_used_at, source, source_ref, created_at, updated_at`

type rowScanner interface{ Scan(...any) error }

func scanCard(r rowScanner, uid int64) (Card, error) {
	var c Card
	var fields string
	var pinned, archived int
	var balance, points, trip, cal sql.NullInt64
	err := r.Scan(&c.ID, &c.Kind, &c.Name, &c.Issuer, &c.Number, &c.Holder, &c.CodeFormat, &c.CodeValue, &c.Color, &c.Note, &c.Tags, &fields, &pinned, &archived,
		&c.ValidFrom, &c.ExpiresOn, &c.StartsAt, &c.EndsAt, &c.Venue, &c.Destination, &c.Seat, &c.Gate, &c.Ref, &balance, &c.Currency, &points,
		&trip, &c.TripName, &cal, &c.FrontImage, &c.BackImage, &c.LogoImage, &c.Uses, &c.LastUsedAt, &c.Source, &c.SourceRef, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return c, err
	}
	c.UserID = uid
	c.Pinned, c.Archived = pinned == 1, archived == 1
	c.Balance, c.Points, c.TripID, c.CalEventID = nullInt(balance), nullInt(points), nullInt(trip), nullInt(cal)
	_ = json.Unmarshal([]byte(fields), &c.Fields)
	if c.Fields == nil {
		c.Fields = []Field{}
	}
	c.Group = GroupOf(c.Kind)
	return c, nil
}

func nullInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// nullAny keeps a nil pointer NULL and a set one (even 0) as its value.
func nullAny(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func fieldsJSON(f []Field) string {
	clean := []Field{}
	for _, x := range f {
		x.Label, x.Value = strings.TrimSpace(x.Label), strings.TrimSpace(x.Value)
		if x.Value != "" {
			clean = append(clean, x)
		}
	}
	b, _ := json.Marshal(clean)
	return string(b)
}

// CardFilter narrows a listing.
type CardFilter struct {
	Group    string // cards | passes | ids
	Kind     string
	Q        string
	Tag      string
	Archived string // "" active only | "only" | "all"
	Pinned   bool
	From, To string // passes starting on these days (YYYY-MM-DD, inclusive)
	TripID   int64
	Expiring string // runs out on or before this day (YYYY-MM-DD)
	Sort     string // used | name | recent | starts | expires
	Limit    int
}

// ListCards lists one person's cards.
func (s *Store) ListCards(ctx context.Context, uid int64, f CardFilter) ([]Card, error) {
	where := []string{"user_id = ?"}
	args := []any{uid}
	switch f.Archived {
	case "only":
		where = append(where, "archived = 1")
	case "all":
	default:
		where = append(where, "archived = 0")
	}
	if f.Group != "" {
		kinds := KindsIn(f.Group)
		if len(kinds) == 0 {
			return []Card{}, nil
		}
		where = append(where, "kind IN ("+strings.TrimSuffix(strings.Repeat("?,", len(kinds)), ",")+")")
		for _, k := range kinds {
			args = append(args, k)
		}
	}
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, f.Kind)
	}
	if f.Pinned {
		where = append(where, "pinned = 1")
	}
	if f.From != "" {
		where = append(where, "starts_at >= ?")
		args = append(args, f.From)
	}
	if f.To != "" {
		where = append(where, "starts_at <> '' AND substr(starts_at, 1, 10) <= ?")
		args = append(args, f.To)
	}
	if f.TripID > 0 {
		where = append(where, "trip_id = ?")
		args = append(args, f.TripID)
	}
	if f.Expiring != "" {
		where = append(where, "expires_on <> '' AND expires_on <= ?")
		args = append(args, f.Expiring)
	}
	if f.Tag != "" {
		where = append(where, "(', ' || lower(tags) || ',') LIKE ?")
		args = append(args, "%, "+strings.ToLower(f.Tag)+",%")
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		where = append(where, `(lower(name) LIKE ? OR lower(issuer) LIKE ? OR lower(number) LIKE ? OR lower(holder) LIKE ? OR lower(note) LIKE ?
			OR lower(tags) LIKE ? OR lower(venue) LIKE ? OR lower(destination) LIKE ? OR lower(ref) LIKE ? OR lower(code_value) = ?)`)
		args = append(args, like, like, like, like, like, like, like, like, like, strings.ToLower(q))
	}
	order := "pinned DESC, uses DESC, last_used_at DESC, lower(name)"
	switch f.Sort {
	case "name":
		order = "lower(name), id"
	case "recent":
		order = "created_at DESC, id DESC"
	case "starts":
		order = "starts_at = '', starts_at, lower(name)"
	case "expires":
		order = "expires_on = '', expires_on, lower(name)"
	}
	q := `SELECT ` + cardCols + ` FROM cards WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + order
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Card{}
	for rows.Next() {
		c, err := scanCard(rows, uid)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCard reads one of a person's cards.
func (s *Store) GetCard(ctx context.Context, uid, id int64) (*Card, error) {
	c, err := scanCard(s.DB.QueryRowContext(ctx, `SELECT `+cardCols+` FROM cards WHERE id = ? AND user_id = ?`, id, uid), uid)
	if err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// CardBySource finds the card an import or another app added before.
func (s *Store) CardBySource(ctx context.Context, uid int64, source, ref string) (*Card, error) {
	c, err := scanCard(s.DB.QueryRowContext(ctx, `SELECT `+cardCols+` FROM cards WHERE user_id = ? AND source = ? AND source_ref = ?`, uid, source, ref), uid)
	if err != nil {
		return nil, notFound(err)
	}
	return &c, nil
}

// CreateCard adds a card.
func (s *Store) CreateCard(ctx context.Context, uid int64, c *Card) error {
	now := Now()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO cards (user_id, kind, name, issuer, number, holder, code_format, code_value, color, note, tags, fields, pinned, archived,
		valid_from, expires_on, starts_at, ends_at, venue, destination, seat, gate, ref, balance, currency, points, trip_id, trip_name, cal_event_id,
		front_image, back_image, logo_image, uses, last_used_at, source, source_ref, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		uid, c.Kind, c.Name, c.Issuer, c.Number, c.Holder, c.CodeFormat, c.CodeValue, c.Color, c.Note, JoinTags(c.Tags), fieldsJSON(c.Fields), boolInt(c.Pinned), boolInt(c.Archived),
		c.ValidFrom, c.ExpiresOn, c.StartsAt, c.EndsAt, c.Venue, c.Destination, c.Seat, c.Gate, c.Ref, nullAny(c.Balance), c.Currency, nullAny(c.Points),
		nullI(c.TripID), c.TripName, nullI(c.CalEventID), c.FrontImage, c.BackImage, c.LogoImage, c.Uses, c.LastUsedAt, c.Source, c.SourceRef, now, now)
	if err != nil {
		return conflict(err)
	}
	c.ID, _ = res.LastInsertId()
	c.UserID, c.CreatedAt, c.UpdatedAt, c.Group = uid, now, now, GroupOf(c.Kind)
	c.Tags = JoinTags(c.Tags)
	if c.Fields == nil {
		c.Fields = []Field{}
	}
	return nil
}

// UpdateCard saves every field of a card the person owns.
func (s *Store) UpdateCard(ctx context.Context, uid int64, c *Card) error {
	now := Now()
	res, err := s.DB.ExecContext(ctx, `UPDATE cards SET kind=?, name=?, issuer=?, number=?, holder=?, code_format=?, code_value=?, color=?, note=?, tags=?, fields=?, pinned=?, archived=?,
		valid_from=?, expires_on=?, starts_at=?, ends_at=?, venue=?, destination=?, seat=?, gate=?, ref=?, balance=?, currency=?, points=?, trip_id=?, trip_name=?, cal_event_id=?,
		front_image=?, back_image=?, logo_image=?, updated_at=? WHERE id = ? AND user_id = ?`,
		c.Kind, c.Name, c.Issuer, c.Number, c.Holder, c.CodeFormat, c.CodeValue, c.Color, c.Note, JoinTags(c.Tags), fieldsJSON(c.Fields), boolInt(c.Pinned), boolInt(c.Archived),
		c.ValidFrom, c.ExpiresOn, c.StartsAt, c.EndsAt, c.Venue, c.Destination, c.Seat, c.Gate, c.Ref, nullAny(c.Balance), c.Currency, nullAny(c.Points),
		nullI(c.TripID), c.TripName, nullI(c.CalEventID), c.FrontImage, c.BackImage, c.LogoImage, now, c.ID, uid)
	if err != nil {
		return conflict(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	c.UpdatedAt, c.Group, c.Tags = now, GroupOf(c.Kind), JoinTags(c.Tags)
	return nil
}

// DeleteCard removes a card (its balance history goes with it).
func (s *Store) DeleteCard(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM cards WHERE id = ? AND user_id = ?`, id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkUsed counts one more time the card was shown.
func (s *Store) MarkUsed(ctx context.Context, uid, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE cards SET uses = uses + 1, last_used_at = ? WHERE id = ? AND user_id = ?`, Now(), id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetCalEvent remembers (or forgets, nil) the card's calendar event.
func (s *Store) SetCalEvent(ctx context.Context, uid, id int64, ev *int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE cards SET cal_event_id = ? WHERE id = ? AND user_id = ?`, nullI(ev), id, uid)
	return err
}

// SetTrip files a card with a trip (nil = with none).
func (s *Store) SetTrip(ctx context.Context, uid, id int64, trip *int64, name string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE cards SET trip_id = ?, trip_name = ? WHERE id = ? AND user_id = ?`, nullI(trip), name, id, uid)
	return err
}

// SetImage stores the file name of a card's front, back or logo picture.
func (s *Store) SetImage(ctx context.Context, uid, id int64, side, name string) error {
	col := map[string]string{"front": "front_image", "back": "back_image", "logo": "logo_image"}[side]
	if col == "" {
		return ErrNotFound
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE cards SET `+col+` = ?, updated_at = ? WHERE id = ? AND user_id = ?`, name, Now(), id, uid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AddBalance spends from (negative) or tops up a gift card and logs it.
func (s *Store) AddBalance(ctx context.Context, uid, cardID, delta int64, note string) (*BalanceEntry, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var bal sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT balance FROM cards WHERE id = ? AND user_id = ?`, cardID, uid).Scan(&bal); err != nil {
		return nil, notFound(err)
	}
	e := BalanceEntry{CardID: cardID, Delta: delta, Balance: bal.Int64 + delta, Note: strings.TrimSpace(note), CreatedAt: Now()}
	if _, err := tx.ExecContext(ctx, `UPDATE cards SET balance = ?, updated_at = ? WHERE id = ? AND user_id = ?`, e.Balance, e.CreatedAt, cardID, uid); err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO balance_log (user_id, card_id, delta, balance, note, created_at) VALUES (?,?,?,?,?,?)`, uid, cardID, delta, e.Balance, e.Note, e.CreatedAt)
	if err != nil {
		return nil, err
	}
	e.ID, _ = res.LastInsertId()
	return &e, tx.Commit()
}

// BalanceLog lists a gift card's history, newest first.
func (s *Store) BalanceLog(ctx context.Context, uid, cardID int64) ([]BalanceEntry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, card_id, delta, balance, note, created_at FROM balance_log WHERE user_id = ? AND card_id = ? ORDER BY created_at DESC, id DESC`, uid, cardID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BalanceEntry{}
	for rows.Next() {
		var e BalanceEntry
		if err := rows.Scan(&e.ID, &e.CardID, &e.Delta, &e.Balance, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AllBalanceLog lists every balance entry of a person (export).
func (s *Store) AllBalanceLog(ctx context.Context, uid int64) ([]BalanceEntry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, card_id, delta, balance, note, created_at FROM balance_log WHERE user_id = ? ORDER BY id`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BalanceEntry{}
	for rows.Next() {
		var e BalanceEntry
		if err := rows.Scan(&e.ID, &e.CardID, &e.Delta, &e.Balance, &e.Note, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// InsertBalanceEntry keeps an imported history line as it was.
func (s *Store) InsertBalanceEntry(ctx context.Context, uid int64, e BalanceEntry) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO balance_log (user_id, card_id, delta, balance, note, created_at) VALUES (?,?,?,?,?,?)`, uid, e.CardID, e.Delta, e.Balance, e.Note, firstNonEmpty(e.CreatedAt, Now()))
	return err
}

// Tags lists the tags a person uses, most used first.
func (s *Store) Tags(ctx context.Context, uid int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT tags FROM cards WHERE user_id = ? AND tags <> ''`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	count := map[string]int{}
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		for _, x := range SplitTags(t) {
			count[x]++
		}
	}
	out := make([]string, 0, len(count))
	for t := range count {
		out = append(out, t)
	}
	sortByCount(out, count)
	return out, rows.Err()
}

// ImageNames lists every picture file a person's cards point to.
func (s *Store) ImageNames(ctx context.Context, uid int64) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT front_image, back_image, logo_image FROM cards WHERE user_id = ?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var a, b, c string
		if err := rows.Scan(&a, &b, &c); err != nil {
			return nil, err
		}
		for _, n := range []string{a, b, c} {
			if n != "" {
				out[n] = true
			}
		}
	}
	return out, rows.Err()
}

func sortByCount(v []string, count map[string]int) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && (count[v[j]] > count[v[j-1]] || (count[v[j]] == count[v[j-1]] && v[j] < v[j-1])); j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
