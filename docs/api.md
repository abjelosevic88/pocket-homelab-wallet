# API

Everything is under `/api/v1`, JSON in and out, with a session cookie, a personal token (`Authorization: Bearer pwc_…`, Settings → API & shortcuts) or a Pocket Core link token. Amounts are minor units (cents). Times of passes are local where they happen: `2026-12-01T06:40`.

## Read

| | |
|---|---|
| `GET /summary` | today's passes, the next one, what runs out within your reminder days, gift cards with money left (Homepage, Pocket AI) |
| `GET /overview` | the home page: cards, IDs, upcoming passes, what runs out in 60 days, counts |
| `GET /cards?group=cards\|passes\|ids&kind=&q=&tag=&archived=only\|all&from=&to=&trip_id=&expiring=YYYY-MM-DD&sort=used\|name\|recent\|starts\|expires&limit=` | list |
| `GET /cards/{id}` | one card |
| `GET /cards/{id}/code.svg` | its barcode, black on white SVG |
| `GET /cards/{id}/images/front\|back\|logo` | its pictures |
| `GET /cards/{id}/balance` | a gift card's history |
| `GET /passes` | passes from now on (or `?from=&to=` / `?trip_id=`) — what Pocket Travel reads |
| `GET /day/YYYY-MM-DD` | the passes of one day — what Pocket Day reads |
| `GET /expiring?days=60&group=ids` | what runs out, with `days_left` — what Pocket Renewals reads |
| `GET /search?q=` | cards by name, shop, number, place or booking reference |
| `GET /kinds` | the kinds of cards and barcodes |

## Change

| | |
|---|---|
| `POST /cards` | `{"kind":"loyalty","name":"Lidl Plus","number":"4012345","code_format":"code128"}`; kinds: loyalty, membership, gift, ticket, boarding, id, passport, licence, insurance, other. `code_value` defaults to the number. Another app may send `source_ref` to add the same card only once |
| `PUT /cards/{id}` | any of the fields; `pinned`, `archived` |
| `DELETE /cards/{id}` | also takes its event off the calendar |
| `POST /cards/{id}/balance` | `{"delta":-1250,"note":"Coffee"}` |
| `PUT /cards/{id}/images/front\|back` | the body is a JPEG or PNG |
| `POST /cards/{id}/calendar` | put a pass on Pocket Calendar now (`?remove=1` takes it off) |
| `POST /cards/{id}/used` | count a time it was shown |
| `POST /import/pkpass` | the body is a .pkpass (or .pkpasses); a pass seen before is updated |
| `POST /import` | a Catima export (.zip or catima.csv), a CSV, a Pocket Wallet export or a .pkpass |
| `POST /scan` | the body is a photo; `{"found":{"format":"ean13","value":"…"}}` or `{"found":null}` |
| `POST /barcode` | `{"format":"qr","value":"…"}` → SVG, to preview |
| `POST /ai/read` | `{"text":"<booking email>"}` → a draft pass (not saved) |
| `POST /mail/scan` | look for passes in Pocket Email now |

Link tokens: `wallet:read` covers the reads above (not settings, export or the email look); `wallet:write` covers `POST /cards`, `PUT`/`DELETE /cards/{id}` of cards that app added, `POST /cards/{id}/balance` and `POST /import/pkpass`.
