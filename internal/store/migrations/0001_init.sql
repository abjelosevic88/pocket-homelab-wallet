CREATE TABLE users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  email         TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL DEFAULT '',
  password_hash TEXT NOT NULL DEFAULT '',
  role          TEXT NOT NULL DEFAULT 'member',
  created_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at    TEXT
);

CREATE TABLE sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL DEFAULT '',
  token_hash   TEXT NOT NULL UNIQUE,
  prefix       TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

-- Box-wide settings (title, setup flag); everything else is personal.
CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE user_settings (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  value   TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, key)
);

CREATE TABLE cache (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL DEFAULT '',
  fetched_at TEXT NOT NULL
);



-- ---------- Pocket Wallet: everything below is per person ----------

-- One thing in the wallet: a loyalty or membership card, a gift card, a
-- ticket or boarding pass, an ID card, a passport, a driving licence, an
-- insurance card. What it is decides which fields matter (store/kinds.go).
CREATE TABLE cards (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL DEFAULT 'loyalty',       -- see store/kinds.go
  name         TEXT NOT NULL,                         -- "Lidl Plus", "Sarajevo → Memmingen", "Passport"
  issuer       TEXT NOT NULL DEFAULT '',              -- the shop, airline, club or authority
  number       TEXT NOT NULL DEFAULT '',              -- the card or document number as printed
  holder       TEXT NOT NULL DEFAULT '',              -- the name on it
  code_format  TEXT NOT NULL DEFAULT '',              -- qr | code128 | ean13 | … | '' no barcode (barcode.go)
  code_value   TEXT NOT NULL DEFAULT '',              -- what the barcode carries
  color        TEXT NOT NULL DEFAULT '',              -- #rrggbb of the tile
  note         TEXT NOT NULL DEFAULT '',
  tags         TEXT NOT NULL DEFAULT '',
  fields       TEXT NOT NULL DEFAULT '[]',            -- more details: [{"label":"Terminal","value":"2"}]
  pinned       INTEGER NOT NULL DEFAULT 0,
  archived     INTEGER NOT NULL DEFAULT 0,
  valid_from   TEXT NOT NULL DEFAULT '',              -- YYYY-MM-DD
  expires_on   TEXT NOT NULL DEFAULT '',              -- YYYY-MM-DD
  starts_at    TEXT NOT NULL DEFAULT '',              -- tickets: YYYY-MM-DDTHH:MM, local time where it happens
  ends_at      TEXT NOT NULL DEFAULT '',
  venue        TEXT NOT NULL DEFAULT '',              -- where, or where from ("SJJ Sarajevo")
  destination  TEXT NOT NULL DEFAULT '',              -- where to ("FMM Memmingen")
  seat         TEXT NOT NULL DEFAULT '',
  gate         TEXT NOT NULL DEFAULT '',
  ref          TEXT NOT NULL DEFAULT '',              -- booking reference, flight or train number
  balance      INTEGER,                               -- gift cards: minor units of currency
  currency     TEXT NOT NULL DEFAULT '',
  points       INTEGER,                               -- loyalty points
  trip_id      INTEGER,                               -- the Pocket Travel trip it belongs to
  trip_name    TEXT NOT NULL DEFAULT '',
  cal_event_id INTEGER,                               -- the event on Pocket Calendar
  front_image  TEXT NOT NULL DEFAULT '',              -- file names in data/images/<user id>/
  back_image   TEXT NOT NULL DEFAULT '',
  logo_image   TEXT NOT NULL DEFAULT '',
  uses         INTEGER NOT NULL DEFAULT 0,            -- how often it was shown
  last_used_at TEXT NOT NULL DEFAULT '',
  source       TEXT NOT NULL DEFAULT '',              -- '' by hand | pkpass | catima | import | email | <app slug>
  source_ref   TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX cards_user ON cards(user_id, archived, kind, name);
CREATE INDEX cards_starts ON cards(user_id, starts_at);
CREATE INDEX cards_expires ON cards(user_id, expires_on);
CREATE UNIQUE INDEX cards_source ON cards(user_id, source, source_ref) WHERE source_ref <> '';

-- What was spent from (or added to) a gift card.
CREATE TABLE balance_log (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  card_id    INTEGER NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
  delta      INTEGER NOT NULL,                        -- minor units, negative = spent
  balance    INTEGER NOT NULL,                        -- what was left after
  note       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX balance_log_card ON balance_log(user_id, card_id, created_at);
