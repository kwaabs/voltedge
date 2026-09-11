# Input-hardening & injection-surface assessment

_Status: assessment only — no code changed. Dated 2026-08-28._

This note records the result of an end-to-end review of VoltEdge's input surface
(login form → feature attribute fields → admin/metadata writes) against injection
and input-abuse attacks. The headline: **the classic injection vectors are already
well-defended by context-specific controls at each sink.** A naive "sanitize all
inputs" pass (regex-stripping quotes/HTML on the way in) would add no real security
and would corrupt legitimate data (names with apostrophes, passwords needing special
characters, JSON/XML payloads). Defense is applied at the sink, where the grammar is
known — not by mangling every inbound string.

## What is already defended (no action needed)

### SQL injection — parameterized everywhere
- Feature CRUD builds dynamic SQL, but **values always travel as `?` placeholders**
  (bun binds them), never string-interpolated. See [crud/sql.go](../api/internal/crud/sql.go)
  and [crud/create.go](../api/internal/crud/create.go).
- The only interpolated tokens are **identifiers** (feature-class table/column names),
  which are either compiler-generated (`physicalTable` → e.g. `gis.pole`) or run through
  [`quoteIdent`](../api/internal/crud/sql.go) (`"` + doubled-quote escaping), so an
  attacker-controlled identifier cannot break out of the quoting.
- Feature field **values** are additionally shape-checked before write in
  [crud/validate.go](../api/internal/crud/validate.go) (`validateForCreate` →
  `validateValue`): per-type checks (text/boolean/integer/numeric/date/timestamp/
  uuid/jsonb), domain-membership for coded fields, range checks for range domains, and
  rejection of unknown/computed/invisible fields. So even the values that reach the DB
  are typed and bounded to the schema.
- **Conclusion:** stripping quotes from feature values or login email would be the wrong
  fix and would break real data. No change warranted.

### Cross-site scripting (XSS) — React auto-escaping
- The web app renders all dynamic text through React's JSX, which HTML-escapes by default.
- There are **zero** `dangerouslySetInnerHTML` sinks in `web/src`. There is no stored-XSS
  path where feature/label data is injected as raw HTML.
- **Conclusion:** HTML-escaping inputs on the way *in* would double-encode and mangle
  stored data. No change warranted.

### Authentication — strong, do not touch
[auth/password.go](../api/internal/auth/password.go) and
[auth/login.go](../api/internal/auth/login.go) already implement the OWASP baseline:
- **Argon2id** password hashing (19 MiB / 2 iterations / 1 lane), with `crypto/subtle`
  constant-time comparison.
- **Timing-equalized** failure paths (`dummyArgon2idHash` runs a hash even when the user
  doesn't exist) so response time doesn't leak account existence.
- **Generic error** `"invalid email or password"` — no user enumeration.
- **Account lockout** — 5 failures → 15-minute lock (`maxFailedLogins`, `lockoutDuration`).
- CSRF tokens, httpOnly cookies, and a `SecurityHeaders` middleware are in place.
- The email is used only as a **parameterized** query value.
- **Conclusion:** passwords must accept every character; sanitizing login inputs would
  weaken auth, not strengthen it. No change warranted.

### SSRF — hardened in the external-links proxy
The kind=`api` external-link fetch ([externallink/fetch.go](../api/internal/externallink/fetch.go))
blocks private/loopback/link-local/multicast/unspecified IPs via a `net.Dialer.Control`
hook on the initial connection *and every redirect hop* (defeats DNS-rebinding), caps the
response at 2 MiB, times out at 10 s, and limits redirects. `resource_path` is validated
to reject any scheme/host so it cannot change the request origin.

## Genuine gaps identified (deferred — documented, not implemented)

These are the only real findings. They were **not** implemented per the decision to
document only; revisit when prioritized.

1. **No request-body-size cap on REST handlers (highest value).**
   ~30 handlers decode JSON with a bare `json.NewDecoder(r.Body).Decode()` and no
   `http.MaxBytesReader`. The middleware stack in
   [cmd/server/main.go](../api/cmd/server/main.go) (Logger, Recoverer, SecurityHeaders,
   SessionLoader) has no global body cap. Only the MCP server
   (`io.LimitReader(r.Body, 1<<20)`) and the outbound LLM/fetch clients bound their
   bodies. An unbounded JSON body on any endpoint (login included) is a
   memory-exhaustion DoS vector.
   - _Recommended fix (when prioritized):_ a global body-limit middleware wrapping
     `r.Body` in `http.MaxBytesReader`, added to the main.go stack. Optionally tighter
     per-route caps (small for auth/admin, larger for geometry/import).

2. **`text` feature values have no maximum length.**
   `validateValue` accepts arbitrary-length strings for `text` fields. Postgres `text`
   is unbounded by design, but an upper sanity bound (e.g. 64 KiB) would limit stored
   size and pairs naturally with the body cap above.

3. **Invalid `uuid` values surface as a 500, not a clean 400.**
   `validateValue`'s `uuid` case only asserts the value is a string, then passes it
   through; a malformed UUID reaches Postgres and returns a raw error. Format-validating
   the UUID in `validateValue` would yield a clean 400. Minor robustness, not a
   vulnerability.

## Bottom line
The injection posture (SQLi, XSS, auth) is solid and should be left as-is. The one
security-relevant improvement is a **request-body-size limit**; the two validation items
are robustness polish. None were implemented in this pass — this note is the record.
