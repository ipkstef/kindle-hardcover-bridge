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

**UNVERIFIED / TODO:**
- Exact mutation(s) to update progress (check the User Books schema page and the
  KOReader plugin / NickelHardcover source).
- Whether progress can be set by percent, or only by pages (then we need edition
  page count to convert).
- Book lookup by ASIN / ISBN (see "Searching" guide and "ISBN and ASIN" page).
- Rate limits.
- Needed OAuth scopes for writing progress (see "Actions & Scopes" page). The
  demo uses only `read:me:content`, which is not enough for writes.

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
