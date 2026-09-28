# Hardcover API

Docs: https://docs.hardcover.app/

## Basics
- v1 **GraphQL** API: `POST https://api.hardcover.app/v1/graphql`
- Header: `Authorization: Bearer <access_token>`
- Tokens from OAuth work the same as a personal access token (PAT).

## Reading progress data
Progress lives on `user_books` → `user_book_reads`
(`progress`, `progress_pages`, `started_at`). `status_id = 2` = currently reading.

Example from the docs:
```graphql
query CurrentlyReading {
  user_books(where: { status_id: { _eq: 2 }, user_id: { _eq: 10714 } }) {
    book { title pages }
    user_book_reads(order_by: { id: desc }, limit: 1) {
      progress
      progress_pages
      started_at
    }
  }
}
```

## Progress mutations (from the KOReader plugin source)
Source: `Billiam/hardcoverapp.koplugin`, `hardcover/lib/hardcover_api.lua`.
Not checked against official docs (blocked in the dev sandbox), but
`me`, `user_books` (status 2) and `update_user_book_read` **work on the live API**
(2026-09-28). `insert_user_book_read` is still **UNVERIFIED**.
Used in `internal/hardcover/client.go`.

- Progress is set in **pages** (`progress_pages`). Page = percent × edition
  pages (the plugin does the same).
- Status IDs: 1 want to read, 2 currently reading, 3 read, 5 DNF.
- `me { id username }` returns a list with one user.
- Update an open read:
  ```graphql
  mutation ($id: Int!, $pages: Int, $editionId: Int, $startedAt: date) {
    update_user_book_read(id: $id, object: {progress_pages: $pages, edition_id: $editionId, started_at: $startedAt}) {
      error
      user_book_read { id progress_pages }
    }
  }
  ```
- Start a new read: `insert_user_book_read(user_book_id: $id, user_book_read: {...})`,
  same fields.
- Open read = newest `user_book_reads` row with `finished_at = null`.
- Page count: read's edition → user book's edition → `book.pages`.

**UNVERIFIED / TODO:**
- Book lookup by ASIN / ISBN (see "Searching" guide and "ISBN and ASIN" page).
- Rate limits.
- Needed OAuth scopes. Scope names confirmed from live server metadata (see
  findings). Prototype asks for `read:me read:library write:library
  read:catalog`; not yet tested.
- Shape of `book.cached_contributors` (assumed `[{"author":{"name":...}}]`).

## OAuth: Device Authorization Grant (use this on Kindle)
Guide: https://docs.hardcover.app/api/oauth/getting-started-device/

Setup: create an app at https://hardcover.app/account/developer-apps/new,
type "Mobile, desktop, or CLI", enable Device Authorization Grant.
Public client: **no secret**. The client ID is public by design.

Endpoints (also at `https://api.hardcover.app/.well-known/oauth-authorization-server`):
```
DEVICE_ENDPOINT  = https://api.hardcover.app/oauth2/device
TOKEN_ENDPOINT   = https://api.hardcover.app/oauth2/token
REVOKE_ENDPOINT  = https://api.hardcover.app/oauth2/revoke
GRAPHQL_ENDPOINT = https://api.hardcover.app/v1/graphql
```

Flow:
1. `POST DEVICE_ENDPOINT` with `client_id`, `scope`.
   Response: `device_code`, `user_code`, `verification_uri`,
   `verification_uri_complete` (for QR), `expires_in`, `interval`.
2. Show `verification_uri` + `user_code` as text (and QR if possible).
   User approves at https://hardcover.app/link.
3. Poll `TOKEN_ENDPOINT` every `interval` s with
   `grant_type=urn:ietf:params:oauth:grant-type:device_code`, `device_code`, `client_id`.
   - `authorization_pending` → keep polling
   - `slow_down` → keep polling, add 5 s to interval
   - anything else → stop
4. Response: `access_token` (`hc_at_…`), `refresh_token` (`hc_rt_…`), `expires_in`.
5. Refresh with `grant_type=refresh_token` at the token endpoint.
6. On sign-out, revoke (access and refresh are revoked together).

Consent lasts one year for the same or smaller scope set.
