# References and prior art

## Kindle
- KindleModding, apps and services (LIPC list):
  https://kindlemodding.org/kindle-apps-and-services/index.html
- `kindle-reading-dashboard` (GitHub) — reads stock-reader state from `cc.db`,
  APNX page mapping. **Find exact repo URL.** Main source for field names.

## Hardcover
- Docs home: https://docs.hardcover.app/
- OAuth for native/device clients:
  https://docs.hardcover.app/api/oauth/getting-started-device/
- Getting progress of books:
  https://docs.hardcover.app/api/guides/gettingbooksprogress/
- Searching: https://docs.hardcover.app/api/guides/searching/
- User Books schema: https://docs.hardcover.app/api/graphql/schemas/userbooks/
- Editions schema: https://docs.hardcover.app/api/graphql/schemas/editions/
- Actions & scopes: https://docs.hardcover.app/api/graphql/actions/
- ISBN and ASIN: https://docs.hardcover.app/librarians/resources/isbnandasin/

## Prior art (study these)
- **NickelHardcover** (RedHatter) — Hardcover integration for the stock Kobo
  reader. Closest match to this project's approach.
- **Hardcover KOReader Plugin** (Billiam) — shows the Hardcover progress
  mutations and book matching logic. We do not use KOReader, but the API code is
  useful.
- Hardcover showcase: https://docs.hardcover.app/showcase
