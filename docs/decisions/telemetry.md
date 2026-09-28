# Design: telemetry to S3

Status: **draft for user decision** (2026-09-28). Nothing is built.

## Goal
Learn how the daemon works on many Kindles (models, firmware, kernels), to
meet the "lowest common denominator" rule: which event sources work, which
cc.db/fmcache formats exist, error and skip rates, API call counts.

## Hard rules
- **Opt-in** (proposed default OFF), shown at sign-in and in the menu.
- **No personal data:** no titles, authors, book keys, ISBNs, highlight text,
  Hardcover/Amazon account IDs, device serial, e-mail, tokens, IP-derived data
  kept by us.
- Never blocks or slows syncing; tiny (≤ 2 KB gzip per upload).

## Transport — do NOT put AWS keys in the binary
The binary is public; any key in it can be extracted (bucket spam, cost,
cannot revoke per device). Options:
| Option | Verdict |
|---|---|
| **A) Small ingest endpoint** (AWS Lambda Function URL or API Gateway) validates schema + size, rate-limits per install ID, writes to S3 | **Recommended** |
| B) Same endpoint hands out short-lived S3 presigned PUT URLs | OK, a bit more moving parts |
| C) Embedded write-only IAM key scoped to a prefix | Reject (abuse, no revoke) |
| D) Public-write bucket | Never |

## Payload (daily summary, JSON, schema v1)
```json
{"schema":1,"app":"3f83ac6","install":"<random UUID made on device>",
 "day":"2026-09-28","model_prefix":"G092","fw":"5.17.1","kernel":"4.9.77",
 "go":"go1.23.12","caps":{"lipc_appmgrd":true,"lipc_powerd":true,"inotify":true,"fmcache":true},
 "counts":{"scans":41,"sent":5,"unchanged":3,"skipped":{"not_found":1,"backward":2},
   "finished":1,"rereads":0,"clips":4,"ratings":2,"shelves":1,
   "api_calls":23,"http_429":0,"errors":{"network":1,"auth":0,"api":0}},
 "match":{"cache":9,"isbn13":1,"library":0,"search":0,"none":1},
 "formats":{"ccdb_schema_hash":"…","fmcache_schemas":["goodreads_book_ratings","goodreads_autoshelvings"]}}
```
`model_prefix` is the first 4 chars of the model code (model family, not
serial). `install` is random, reset on sign-out.

## Storage
`s3://<bucket>/telemetry/v1/dt=YYYY-MM-DD/<install>.json.gz`; lifecycle
expiry 90 days; query with Athena. Cost: negligible.

## Device side
Counters in memory + one small file; upload once a day when Wi-Fi is up and
after a normal sync; keep at most 7 unsent days; drop on failure after that.

## Open decisions (user)
1. Who runs the endpoint/bucket (you), and region.
2. Opt-in (proposed) or opt-out.
3. The field list above — add/remove.
