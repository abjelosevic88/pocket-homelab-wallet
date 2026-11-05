package store

// Kind is what a card is. Its group decides where it lives in the app:
// cards (shown at the till), passes (a ticket for a day and time) or ids
// (documents with a number and an end date).
type Kind struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Group string `json:"group"`
	Icon  string `json:"icon"`
	Hint  string `json:"hint"`
}

// Groups.
const (
	GroupCards  = "cards"
	GroupPasses = "passes"
	GroupIDs    = "ids"
)

// Kinds are every kind of card, in the order the add dialog shows them.
var Kinds = []Kind{
	{"loyalty", "Loyalty card", GroupCards, "tag", "A shop's points or discount card"},
	{"membership", "Membership", GroupCards, "users", "Gym, library, club, car sharing"},
	{"gift", "Gift card or voucher", GroupCards, "gift", "Money to spend in a shop, with what is left"},
	{"ticket", "Ticket", GroupPasses, "ticket", "A concert, match, museum, train or bus"},
	{"boarding", "Boarding pass", GroupPasses, "plane", "A flight, with gate and seat"},
	{"id", "ID card", GroupIDs, "id-card", "Your identity card"},
	{"passport", "Passport", GroupIDs, "passport", "Number, end date and a photo of the page"},
	{"licence", "Driving licence", GroupIDs, "car", "Number, categories and end date"},
	{"insurance", "Insurance or health card", GroupIDs, "shield", "Health insurance, travel or car insurance card"},
	{"other", "Something else", GroupCards, "card", "Any other card with a number or a barcode"},
}

// ValidKind reports whether k is a known kind.
func ValidKind(k string) bool {
	_, ok := kindByKey[k]
	return ok
}

// KindLabel is a kind's name for people.
func KindLabel(k string) string {
	if v, ok := kindByKey[k]; ok {
		return v.Label
	}
	return k
}

// GroupOf is the group a kind is in.
func GroupOf(k string) string {
	if v, ok := kindByKey[k]; ok {
		return v.Group
	}
	return GroupCards
}

// KindsIn lists the kinds of one group.
func KindsIn(group string) []string {
	var out []string
	for _, k := range Kinds {
		if k.Group == group {
			out = append(out, k.Key)
		}
	}
	return out
}

var kindByKey = func() map[string]Kind {
	m := map[string]Kind{}
	for _, k := range Kinds {
		m[k.Key] = k
	}
	return m
}()
