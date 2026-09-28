# hcbridge prototype (option B)

Goal: prove the Hardcover side end to end on a real Kindle.
Device-code sign-in, then one manual progress update. No daemon yet.

## One-time setup (user)
1. Create an app at https://hardcover.app/account/developer-apps/new
   - Type: "Mobile, desktop, or CLI"
   - Enable "Device Authorization Grant"
2. Copy the **client ID** (public, no secret).
3. Put it in `extensions/hcbridge/client_id.txt` on the Kindle drive
   (or build with `HC_CLIENT_ID=... ./scripts/build.sh`).

## Build
`./scripts/build.sh` → `dist/hcbridge.zip` (Go 1.23, `GOARM=6`, ~10 MB).

## KUAL menu "Hardcover"
| Item | Does |
|---|---|
| Sign in | Shows URL + code on screen. Approve on phone. Saves token to `/var/local/hcbridge/token.json` (0600). |
| Identify current book | Runs the match waterfall (`docs/roadmap.md` §2). Shows the Hardcover book, the method, and your shelf. Writes nothing. |
| Sync current book now | Reads current book from `cc.db`, finds it with the match waterfall; if it is on "Currently Reading", sets page = percent × edition pages (forward only). |
| Who am I | Shows the signed-in user. |
| Sign out | Deletes the token. |

Log: `/mnt/us/hcbridge.log` (no tokens in it).

## Rules
- Only books already in "Currently Reading" on Hardcover are synced.
- No match, or more than one match → skip. Never guess.
- Progress in `cc.db` changes only after going Home. Go Home before "Sync".

## Test plan
1. Sign in → screen shows "Signed in as @user".
2. On Hardcover, set your current Kindle book to "Currently Reading".
3. Read a few pages, go Home, then KUAL → Sync. Screen shows page X of Y.
4. Check the page on hardcover.app.
5. Send `hcbridge.log`.
