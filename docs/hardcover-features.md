# Hardcover features beyond progress (research 2026-09-28)

Sources: official docs repo `github.com/hardcoverapp/hardcover-docs`
(`schema.graphql`, `capabilities.json`, `*.mdx`), Hardcover KOReader plugin
(KO), audiobookshelf-hardcover-sync (ABS, Go). Not yet tested live unless noted.

## Journal (notes, quotes)
```graphql
insert_reading_journal(object: ReadingJournalCreateType!)  # → { errors id reading_journal }
input ReadingJournalCreateType { book_id: Int! event: String! privacy_setting_id: Int!
  tags: [BasicTag]! entry: String edition_id: Int action_at: date metadata: jsonb }
```
- Events: `note`, `quote` (KO uses these), also `status_read`, `rated`,
  `progress_updated`, … (list says "etc.").
- Quote position: `metadata: {"position":{"type":"pages","value":123,"possible":400}}`
  (KO). Other position types **UNVERIFIED**.
- Scope: `write:library`.

## Privacy
- IDs: 1 Public, 2 Followers, 3 Private.
- Set per journal entry (required) and per `user_books` row (covers status,
  rating, review together). No account "default journal privacy" field.
- **Ratings cannot be private alone**: private rating = private shelf entry.
- `user_books.private_notes`: per-book note only the owner sees.

## Rating / review
- `update_user_book(id, object: {rating})`, 0–5 in halves, `null` clears.
  Scope `write:library`.
- Review: `review_markdown`, `review_has_spoilers`, `reviewed_at` on
  `update_user_book`; needs `write:reviews` too.

## Finish a book
- Status IDs: 1 Want to Read, 2 Reading, 3 Read, 4 Paused, 5 DNF, 6 Ignored.
- Order (ABS; avoids an extra empty finished read):
  1. `update_user_book_read(id, object: {progress_pages, finished_at})`
  2. `update_user_book(id, object: {status_id: 3})`
- No auto-finish at the last page documented. **No webhooks.** The daemon must
  detect "finished" itself (Kindle percent / `p_readState`).

## Re-read
- `insert_user_book_read(user_book_id, user_book_read: {...})` + status 2.
- KO bug: `createRead` checks `update_user_book_read` → always nil. Ours checks
  `insert_user_book_read` (correct).

## Scopes and limits
- Official "E-Reader / Sync Client" preset:
  `read:catalog read:library write:library read:me:content`.
  Add `write:reviews` only for reviews. More scopes later = user must sign in
  again, so request all up front.
- Rate limits per user (all apps): free 5,000/day, burst 10, 60/min;
  max 5 top-level operations per request, 1 `search`. 429 on limit,
  403 `insufficient_scope`.

## Other
- No reading-time/session type. `progress_seconds` is for audiobooks.
- `update_goal_progress`, `insert_list_book`, `edition_owned` exist.
