# sc-nosqli results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

**Summary:** No NoSQL injection vulnerabilities found — UWAS uses no MongoDB/Elasticsearch/CouchDB/DynamoDB, and its only NoSQL-adjacent component (a custom Redis L3 cache client) uses a safe, length-prefixed RESP encoder with no EVAL/Lua or user-constructed query logic.

No issues found by sc-nosqli.

## Scope checked

- **NoSQL drivers in dependencies:** `go.mod` and `web/dashboard/package.json` contain no `mongo`, `redis` (third-party), `elasticsearch`, `couch`, `dynamo`, or `aws-sdk` packages. The persistent datastore is MySQL/MariaDB (`internal/database/manager.go`) — SQL, out of scope for this NoSQL skill (see sc-sqli).
- **No NoSQL query sinks:** grep for `MongoClient`, `mongo.Connect`, `elasticsearch.New`, `cloudant`, `dynamodb.`, `$where`, `$regex`, `query_string`, `painless` returned no real query construction. Matches for `$gt`/`$lt` were in `internal/rewrite/condition.go` (Apache mod_rewrite comparison operators) and FastCGI env handling — unrelated to NoSQL.

## Defenses / safe code observed

- `internal/cache/redis_resp.go` — Custom RESP wire-protocol client. Commands are encoded as RESP arrays of **length-prefixed bulk strings** (`writeArray`: `$<len>\r\n<value>\r\n`). Because every argument is length-prefixed, embedded CRLF or control characters in a key/value are treated as literal data, so command injection via the RESP stream is not possible. There is **no `EVAL`/`EVALSHA`/Lua scripting** and no dynamic command-name construction — command verbs (`GET`, `SET`, `DEL`, `KEYS`, `AUTH`, `SELECT`) are hardcoded constants.
- `internal/cache/redis.go` — Only stores/retrieves serialized HTTP cache responses keyed by internally-generated cache keys. `PurgeByTag`/`PurgeAll` build a Redis `KEYS` glob pattern, but the pattern is always scoped under the configured cache prefix and only drives cache invalidation (`DEL`), so even a maliciously-shaped tag has negligible impact (no data read/exfiltration, no query semantics).
