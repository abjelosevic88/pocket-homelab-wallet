package server

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/abjelosevic88/pocket-homelab-wallet/internal/store"
)

// withCheck adds the EAN check digit to 12 (or 7) digits.
func withCheck(digits string) string {
	sum := 0
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if (len(digits)-1-i)%2 == 0 {
			d *= 3
		}
		sum += d
	}
	return digits + fmt.Sprint((10-sum%10)%10)
}

// bcbp is the text of a boarding pass barcode (IATA BCBP), made up.
func bcbp(pnr, from, to, carrier, flight, date, seat string, seq int) string {
	return fmt.Sprintf("M1HODZIC/AMAR         E%-7s%s%s%-3s%-5s%03dY%04s%04d 100", pnr, from, to, carrier, flight, dayOfYear(date), seat, seq)
}

// seedDemo fills the owner's wallet with shop cards from Sarajevo, a gym
// and library card, two gift cards, this week's cinema, concert and match,
// a trip to Vienna with its boarding passes and train ticket, past passes
// and the family's ID papers — one passport running out soon (DEMO_DATA,
// for `make run`, the CI smoke test and the screenshots). Dates are
// relative to today, so the demo always looks current. Names and numbers
// are made up.
func (s *Server) seedDemo(ctx context.Context, uid int64) error {
	// with AUTH_MODE=none this runs inside the first request; finish even
	// when that client goes away
	ctx = context.WithoutCancel(ctx)
	if cs, _ := s.store.ListCards(ctx, uid, store.CardFilter{Archived: "all", Limit: 1}); len(cs) > 0 {
		return nil
	}
	rng := rand.New(rand.NewSource(42))
	today := store.Today()
	day := func(n int) string { return store.AddDays(today, n) }
	at := func(n int, hm string) string { return day(n) + "T" + hm }
	num := func(v int64) *int64 { return &v }
	trip := int64(3)
	holder := "Amar Hodžić"

	cards := []store.Card{
		// at the till
		{Kind: "loyalty", Name: "Konzum Klub", Issuer: "Konzum", Number: withCheck("290001734552"), CodeFormat: "ean13", Color: "#e30613", Points: num(1840), Tags: "groceries"},
		{Kind: "loyalty", Name: "Bingo kartica", Issuer: "Bingo", Number: "BG 7731 0042 118", CodeFormat: "code128", CodeValue: "BG77310042118", Color: "#f39200", Tags: "groceries"},
		{Kind: "loyalty", Name: "dm active beauty", Issuer: "dm", Number: withCheck("400554031872"), CodeFormat: "ean13", Color: "#163a74", Points: num(312), Tags: "drugstore"},
		{Kind: "loyalty", Name: "IKEA Family", Issuer: "IKEA", Number: "6275 9801 2345 6789", CodeFormat: "code128", CodeValue: "6275980123456789", Color: "#0058a3", Tags: "home"},
		{Kind: "loyalty", Name: "Decathlon", Issuer: "Decathlon", Number: withCheck("209912345678"), CodeFormat: "ean13", Color: "#0082c3", Tags: "sport"},
		{Kind: "loyalty", Name: "Lidl Plus", Issuer: "Lidl", Number: "LP-4021-8830", CodeFormat: "qr", CodeValue: "LIDLPLUS:AT:40218830:7F3K", Color: "#0050aa", Tags: "groceries, travel", Note: "Works in Austria and Croatia."},
		{Kind: "loyalty", Name: "Shell ClubSmart", Issuer: "Shell", Number: "7077 1900 5521 0034", CodeFormat: "code128", CodeValue: "7077190055210034", Color: "#e8b600", Tags: "car"},
		{Kind: "loyalty", Name: "Müller", Issuer: "Müller", Number: withCheck("401234567890"), CodeFormat: "ean13", Color: "#f07d00", Tags: "drugstore"},
		{Kind: "membership", Name: "Fit Life gym", Issuer: "Fit Life Sarajevo", Number: "FL-002931", Holder: holder, CodeFormat: "qr", CodeValue: "FITLIFE|002931|HODZIC", Color: "#111827", ExpiresOn: day(19), Tags: "sport", Note: "Monthly; renew at the desk."},
		{Kind: "membership", Name: "City library", Issuer: "Biblioteka Sarajeva", Number: "A0019274", Holder: holder, CodeFormat: "code39", CodeValue: "A0019274", Color: "#7c4d2b", ExpiresOn: day(240)},
		{Kind: "membership", Name: "Nextbike", Issuer: "Nextbike Sarajevo", Number: "38761552190", CodeFormat: "qr", CodeValue: "https://nextbike.net/u/38761552190", Color: "#0090c0", Tags: "city"},
		{Kind: "gift", Name: "BBI Centar gift card", Issuer: "BBI Centar", Number: "9900 1122 3344", CodeFormat: "code128", CodeValue: "990011223344", Color: "#8a1538", Balance: num(6450), Currency: "BAM", ExpiresOn: day(160), Note: "Birthday present from the office."},
		{Kind: "gift", Name: "Cineplexx voucher", Issuer: "Cineplexx", Number: "CX-55-KL9Q", CodeFormat: "qr", CodeValue: "CXV-55-KL9Q-2", Color: "#c8102e", Balance: num(2000), Currency: "BAM", ExpiresOn: day(45)},

		// passes
		{Kind: "ticket", Name: "Dune: Part Three", Issuer: "Cineplexx Sarajevo", StartsAt: at(0, "19:30"), EndsAt: at(0, "22:15"), Venue: "Cineplexx, Sarajevo City Center", Seat: "Hall 4, row F, seats 7–8", Ref: "CX81923", CodeFormat: "qr", CodeValue: "CPX-81923-F7F8", Color: "#c8102e"},
		{Kind: "ticket", Name: "Dubioza kolektiv", Issuer: "Entrio", StartsAt: at(3, "20:00"), Venue: "Zetra Olympic Hall", Seat: "Standing", Ref: "ENT-2219-8812", CodeFormat: "qr", CodeValue: "ENTRIO:2219-8812:GA:01", Color: "#5b21b6", Holder: holder},
		{Kind: "ticket", Name: "FK Sarajevo – FK Željezničar", Issuer: "FK Sarajevo", StartsAt: at(9, "18:00"), Venue: "Asim Ferhatović Hase Stadium", Seat: "East stand, sector 3, row 12, seat 8", CodeFormat: "qr", CodeValue: "FKS-DERBY-E3-12-8", Color: "#7a1f3d"},
		{Kind: "boarding", Name: "Sarajevo → Vienna", Issuer: "Austrian Airlines", StartsAt: at(12, "06:00"), EndsAt: at(12, "07:25"), Venue: "SJJ Sarajevo", Destination: "VIE Vienna", Seat: "14C", Ref: "OS 784", Holder: holder,
			CodeFormat: "pdf417", CodeValue: bcbp("K7Q2PL", "SJJ", "VIE", "OS", "0784", day(12), "014C", 42), Color: "#d81e05",
			Fields: []store.Field{{Label: "Booking reference", Value: "K7Q2PL"}, {Label: "Boarding", Value: "05:20"}, {Label: "Group", Value: "B"}}},
		{Kind: "ticket", Name: "Albertina", Issuer: "Albertina Museum", StartsAt: at(13, "15:00"), Venue: "Albertinaplatz 1, Vienna", Ref: "ALB-77120", CodeFormat: "qr", CodeValue: "ALB-77120-2A", Color: "#262626"},
		{Kind: "ticket", Name: "Vienna → Salzburg", Issuer: "ÖBB", StartsAt: at(14, "09:08"), EndsAt: at(14, "11:30"), Venue: "Wien Hbf", Destination: "Salzburg Hbf", Seat: "Car 26, seat 45", Ref: "RJX 762", Holder: holder,
			CodeFormat: "aztec", CodeValue: "#UT01OEBB0000001K3J9QX2LMP7RS4TV8WY", Color: "#e2002a"},
		{Kind: "boarding", Name: "Vienna → Sarajevo", Issuer: "Austrian Airlines", StartsAt: at(16, "21:40"), EndsAt: at(16, "23:05"), Venue: "VIE Vienna", Destination: "SJJ Sarajevo", Seat: "9A", Ref: "OS 783", Holder: holder,
			CodeFormat: "pdf417", CodeValue: bcbp("K7Q2PL", "VIE", "SJJ", "OS", "0783", day(16), "009A", 17), Color: "#d81e05",
			Fields: []store.Field{{Label: "Booking reference", Value: "K7Q2PL"}}},
		{Kind: "boarding", Name: "Sarajevo → Lisbon", Issuer: "Turkish Airlines", StartsAt: at(-96, "07:10"), Venue: "SJJ Sarajevo", Destination: "LIS Lisbon", Seat: "22F", Ref: "TK 1022",
			CodeFormat: "pdf417", CodeValue: bcbp("ZT81QA", "SJJ", "IST", "TK", "1022", day(-96), "022F", 101), Color: "#c70a0c"},
		{Kind: "ticket", Name: "Sarajevo Film Festival: opening", Issuer: "SFF", StartsAt: at(-80, "21:00"), Venue: "National Theatre Sarajevo", Seat: "Row 8, seat 14", CodeFormat: "qr", CodeValue: "SFF-OPEN-08-14", Color: "#b91c1c"},

		// ID papers
		{Kind: "id", Name: "ID card", Issuer: "Bosnia and Herzegovina", Number: "1E9K4P772", Holder: holder, ValidFrom: day(-1460), ExpiresOn: day(1460), Color: "#1f3a93"},
		{Kind: "passport", Name: "Passport", Issuer: "Bosnia and Herzegovina", Number: "A4471029", Holder: holder, ValidFrom: day(-3600), ExpiresOn: day(46), Color: "#0b2f6b", Note: "Renew before the Vienna trip: book at the CIPS office."},
		{Kind: "passport", Name: "Passport", Issuer: "Bosnia and Herzegovina", Number: "A5528114", Holder: "Lejla Hodžić", ValidFrom: day(-900), ExpiresOn: day(2700), Color: "#0b2f6b"},
		{Kind: "licence", Name: "Driving licence", Issuer: "Bosnia and Herzegovina", Number: "0712-BA-338541", Holder: holder, ExpiresOn: day(1100), Color: "#be5a8c", Fields: []store.Field{{Label: "Categories", Value: "AM, B, BE"}}},
		{Kind: "insurance", Name: "Health insurance", Issuer: "ZZO Kantona Sarajevo", Number: "1203988175042", Holder: holder, ExpiresOn: day(24), Color: "#0f766e", Note: "Stamp it at the employer each quarter."},
		{Kind: "insurance", Name: "Travel insurance", Issuer: "Uniqa", Number: "PZO-2026-118823", Holder: holder, ValidFrom: day(-165), ExpiresOn: day(200), Color: "#1d4ed8",
			Fields: []store.Field{{Label: "Emergency line", Value: "+387 33 290 800"}, {Label: "Cover", Value: "Europe, €30,000"}}},
	}
	now := time.Now().UTC()
	for i := range cards {
		c := &cards[i]
		if c.Fields == nil {
			c.Fields = []store.Field{}
		}
		if c.CodeFormat != "" && c.CodeValue == "" {
			c.CodeValue = c.Number
		}
		if d := c.Day(); d != "" && d >= day(11) && d <= day(17) {
			c.TripID, c.TripName = &trip, "Vienna and Salzburg"
		}
		if store.GroupOf(c.Kind) == store.GroupCards {
			c.Uses = rng.Intn(40)
			if c.Uses > 0 {
				c.LastUsedAt = now.Add(-time.Duration(rng.Intn(30*24)) * time.Hour).Format(time.RFC3339Nano)
			}
		}
		c.Pinned = c.Name == "Konzum Klub" || c.Name == "Fit Life gym"
		if err := s.store.CreateCard(ctx, uid, c); err != nil {
			return err
		}
		_, _ = s.store.DB.ExecContext(ctx, `UPDATE cards SET created_at = ? WHERE id = ?`, now.Add(-time.Duration(len(cards)-i)*36*time.Hour).Format(time.RFC3339Nano), c.ID)
	}
	// what was on the gift cards and what was spent from them
	for _, c := range cards {
		if c.Kind != "gift" || c.Balance == nil {
			continue
		}
		spends := []struct {
			d    int64
			note string
		}{{-2350, "Zara"}, {-1200, "Coffee and cake"}}
		if c.Name == "Cineplexx voucher" {
			spends = spends[:0]
			spends = append(spends, struct {
				d    int64
				note string
			}{-1000, "Two tickets"})
		}
		bal := *c.Balance
		for _, sp := range spends {
			bal -= sp.d
		}
		ins := func(delta, balance int64, note string, when time.Time) {
			_, _ = s.store.DB.ExecContext(ctx, `INSERT INTO balance_log (user_id, card_id, delta, balance, note, created_at) VALUES (?,?,?,?,?,?)`,
				uid, c.ID, delta, balance, note, when.Format(time.RFC3339Nano))
		}
		ins(bal, bal, "On the card when it came", now.AddDate(0, 0, -60))
		for k, sp := range spends {
			bal += sp.d
			ins(sp.d, bal, sp.note, now.AddDate(0, 0, -40+k*17))
		}
	}

	st, _ := s.store.GetSettings(ctx, uid)
	st.Currency = "BAM"
	return s.store.SaveUserSettings(ctx, uid, st)
}

func dayOfYear(d string) int {
	t, err := store.ParseDate(d)
	if err != nil {
		return 1
	}
	return t.YearDay()
}
