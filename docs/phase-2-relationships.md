# Phase 2 (slice 5) — Explicit-confirm structural relationships

**Goal:** the only path that turns a candidate structural/spatial
relationship (e.g. a pole supporting a conductor span) into a recorded
fact, gated on `metadata.relationship_classes` -- same explicit-confirm
discipline as api/internal/connectivity, adapted for a metadata table
that has no `is_valid` flag and does carry a `cardinality` rule.
**Status:** done and verified against a live server and two real feature
classes (`POLE`, `MV_CONDUCTOR`, reusing what Phase 2 slices 1 and 4
already seeded), including the zero-trust rejection, cardinality
enforcement (both permitting and rejecting), kind disambiguation,
idempotent establish, listing from both sides, revoke, and the guards
against a deleted or nonexistent feature. See "Try it yourself" below.

## What was built

- **`migrations/0017_create_feature_relationships.sql`** —
  `runtime.feature_relationships`, alongside `feature_connectivity` in
  the schema migration 0016 reserved. `origin_feature_id`/
  `destination_feature_id` are polymorphic (no FK), same reasoning as
  connectivity's `from`/`to` ids. A `UNIQUE` constraint on
  `(relationship_class_id, origin_feature_id, destination_feature_id)`
  makes establish idempotent -- no `NULLS NOT DISTINCT` needed here
  (unlike connectivity's constraint), since none of these three columns
  is ever null.
- **`api/internal/metadata`** — `RelationshipClass` model, and
  `LoadRelationshipClasses(ctx, db, originFCID, destFCID)`, returning
  every row for a pairing (there can legitimately be more than one, with
  different `kind`).
- **`api/internal/relationship`** (new package, deliberately parallel to
  `connectivity`, not sharing code with it -- the two packages' matching
  logic isn't actually the same shape once you look past the similar
  surface):
  - `rules.go` — `matchClass`: zero trust on an empty result set: rejects if no `relationship_classes`
    row exists for the origin/destination feature-class pairing.
    Disambiguates by `kind` when more than one class matches, requiring
    the request to say which if there's more than one legal answer.
  - `cardinality.go` — `checkCardinality`: enforces
    `relationship_classes.cardinality` (`one_to_one`/`one_to_many`/
    `many_to_many`) against what's already established under that class,
    something `network_rules`-based connectivity has no equivalent of.
  - `sql.go` — `FeatureRelationship` bun model (fixed schema, known at
    compile time, same reasoning as connectivity's `Connectivity`
    model); `resolveFeature` (existence + not-deleted check, no terminal
    concept here since `relationship_classes` doesn't have one);
    `establishRelationship`, `listRelationships`, `revokeRelationship`;
    batched enrichment.
  - `handler.go` — `chi.Router` mounted at `/relationships`:
    `POST /` (establish), `GET /?feature_class=&feature_id=` (list),
    `DELETE /{id}` (revoke).
- Wired into `cmd/server/main.go` as
  `r.Mount("/relationships", relationship.Routes(db))`.
- **`seed/relationship_example.sql`** — two `relationship_classes` rows
  over the already-seeded `POLE`/`MV_CONDUCTOR` pair: the real one from
  docs/metadata-core.md's POLE sample (`spatial_support`,
  `many_to_many`), and a second, synthetic `composite`-kind row
  (`one_to_many`) that exists purely to give `checkCardinality`
  something real to reject against -- see "Decisions made" for why the
  real relationship isn't `one_to_many` itself.

## Zero trust, adapted for a table with no `is_valid` flag

`relationship_classes` (migrations/0010) has no `is_valid` column --
unlike `network_rules`, there's no "exists but disabled" state to
report separately. Legality collapses to one question: does a
`relationship_classes` row exist for this origin/destination
feature-class pairing at all? `matchClass` rejects outright when it
doesn't, with the same "zero trust default" framing connectivity uses,
because the underlying principle is identical even though the mechanism
generating "no" is simpler.

What `relationship_classes` has that `network_rules` doesn't is
`cardinality` -- a second, independent way a request can be legal-in-
kind but still rejected. `checkCardinality` treats a `one_to_one` or
`one_to_many` class's cap as just as real a gate as the class's mere
existence: matching the right `kind` gets a request past `matchClass`,
but it still has to clear `checkCardinality` before anything is written.
Both checks have to pass; neither is sufficient alone.

## How this was validated

Ran the actual compiled server against a real Postgres, with `POLE`,
`MV_CONDUCTOR`, and both `relationship_classes` rows seeded, real
feature rows created (two poles, two conductors, one transformer), and
hit it with real HTTP requests:

- **Ambiguous request, no `kind`** — `POLE` → `MV_CONDUCTOR` with no
  `kind` in the body, while two classes exist for that pairing → `400`,
  `"multiple relationship classes match this pairing [spatial_support
  composite] -- specify kind to disambiguate"`. Confirmed *before*
  testing the happy path, since it's what proves disambiguation is a
  real code path and not just a comment.
- **The valid case** — same pairing, `kind: spatial_support` → `200`,
  response names the matched class and echoes both endpoints.
- **`many_to_many` permits reuse on both sides** — the same origin pole
  related to a second conductor, and a second pole related to the same
  first conductor, both under the `spatial_support` class → both `200`.
  Confirms `many_to_many` genuinely has no cap, not just that the first
  call worked.
- **`one_to_many` cardinality, permitting and then rejecting** — a pole
  related to a conductor under the `composite` class (`one_to_many`) →
  `200` (first for that destination); a *different* pole then attempting
  the same conductor under the same class → `400`, `"cardinality
  violation: ... is one_to_many and this destination already has a
  different origin under this relationship class"`.
- **Zero trust** — `DIST_XFMR` → `POLE`, a pairing with no
  `relationship_classes` row at all → `400`, `"no relationship class
  permits this relationship -- zero trust default: absence of a
  relationship_classes row means the relationship is invalid"`.
- **Idempotent establish** — re-establishing the identical
  origin/destination/kind → identical `id` and `established_at`, not a
  duplicate row.
- **List from both sides** — listing by the pole's id returned all three
  of its relationships (two `spatial_support`, one `composite`), each
  correctly enriched with feature-class codes, the matched class's name,
  kind, and cardinality.
- **Revoke** — `204`, then a second revoke on the same id → `404`.
- **Guards** — a nonexistent feature id → `404`; a soft-deleted feature
  (via Phase 2 slice 3's delete endpoint) → `400`,
  `"... is deleted -- restore it before relating"`.

No new bugs surfaced during this testing -- the alias lesson from
connectivity's `ON CONFLICT DO UPDATE` bug was applied up front (see
`FeatureRelationship`'s `alias:feature_relationships` tag), and the rest
of the package follows connectivity's already-verified shape closely
enough that nothing new broke.

## Decisions made

- **`many_to_many`, not `one_to_many`, for the real POLE-supports-
  MV_CONDUCTOR relationship** — a real conductor span is physically
  supported by more than one pole (one at each end), and a real pole
  supports more than one span. A literal `one_to_many` cardinality (one
  origin, many destinations, but each destination capped to a single
  origin) would wrongly forbid a second pole from ever supporting an
  already-supported conductor -- correct multiplicity language for that
  reality is `many_to_many`. The docs/metadata-core.md sample doesn't
  actually specify a cardinality for this pairing, so this fills that
  gap with the value that matches the real-world shape rather than
  literally guessing `one_to_many` from the word "supports."
- **A second, synthetic `relationship_classes` row for the cardinality
  demo** — enforcing `cardinality` at all needed a real `one_to_many` (or
  `one_to_one`) case to prove against, and introducing a third feature
  class just for that felt like scope creep for this slice. Reusing
  `POLE`/`MV_CONDUCTOR` under a second, clearly-labeled `kind` avoided
  that without inventing new domain modeling. It's marked as a test
  fixture in the seed file's own comments, not presented as a real
  utility relationship.
- **`kind` disambiguates, not a `relationship_class_id`** — a client
  establishing a relationship generally knows the *kind* of relationship
  it means ("this pole supports this conductor") more naturally than a
  specific class id it would have to look up first. Falls back to
  "there's only one legal answer" when no `kind` is given and exactly
  one class matches, so the common case (one class per pairing) never
  needs it at all.
- **No soft-delete/restore pair for relationships, same as connections**
  — a relationship is a binary fact, established or not, with no
  draft/published life cycle of its own. `revokeRelationship` is a real,
  permanent removal.
- **Deliberately parallel code, not shared code, with `connectivity`** —
  the two packages read as similar (resolve endpoints, match a metadata
  rule, idempotent upsert, batched list enrichment) but the actual
  matching logic differs enough -- terminals and an `is_valid` flag on
  one side, cardinality enforcement on the other -- that forcing a shared
  abstraction would have made both harder to read for a marginal
  reduction in line count.

## What's still open

- **Attachments** -- RustFS-backed, a genuinely different design surface
  (upload flow, size/type limits) not yet worked through in enough
  detail to build.
- **No `PATCH` for a relationship** -- re-established or revoked, never
  edited in place, same as connections.
- **`ST_DWithin` plausibility check not implemented** --
  docs/metadata-core.md's POLE sample mentions geometric plausibility
  (`ST_DWithin`) as a soft check alongside the explicit establish
  requirement. This slice enforces the metadata-level legality
  (`relationship_classes` + cardinality) but doesn't yet warn when two
  features being related are implausibly far apart. Worth adding as a
  non-blocking warning in the response, not a rejection -- explicit
  confirm should still win over a geometry heuristic.

## Try it yourself

Everything below is the exact sequence used to validate this slice. See
[`docs/phase-2-crud-engine.md`](./phase-2-crud-engine.md)'s "Try it
yourself" intro for the `Invoke-RestMethod`-not-`curl.exe` note and the
recompile-on-a-persistent-environment note -- both apply here too.

### 1. Set up, seed, and compile

```
task infra-up
task migrate
task seed-pole
task seed-connectivity
task seed-relationships
task compile CODE=POLE
task compile CODE=MV_CONDUCTOR
task api-dev
```

### 2. Create two poles and two conductors

From a second terminal:
```powershell
function New-Pole($lon, $lat) {
  $body = @{
    subtype = 1
    geometry = @{ type = "Point"; coordinates = @($lon, $lat) }
    attributes = @{ pole_material = "WOOD"; pole_use = "DISTRIBUTION"; height_class = "CLASS_1"; condition_rating = "GOOD" }
  } | ConvertTo-Json -Depth 5
  (Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/POLE" -ContentType "application/json" -Body $body).id
}
function New-Conductor($lon1, $lat1, $lon2, $lat2) {
  $body = @{
    subtype = 1
    geometry = @{ type = "LineString"; coordinates = @(@($lon1, $lat1), @($lon2, $lat2)) }
    attributes = @{ voltage_class = "11KV"; condition_rating = "GOOD" }
  } | ConvertTo-Json -Depth 5
  (Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/MV_CONDUCTOR" -ContentType "application/json" -Body $body).id
}

$POLE1 = New-Pole -0.1276 51.5072
$POLE2 = New-Pole -0.1256 51.5052
$MV1 = New-Conductor -0.1276 51.5072 -0.1266 51.5062
$MV2 = New-Conductor -0.1266 51.5062 -0.1256 51.5052
```

### 3. Ambiguous request -- no `kind`, two classes match

```powershell
$body = @{
  origin      = @{ feature_class = "POLE"; feature_id = $POLE1 }
  destination = @{ feature_class = "MV_CONDUCTOR"; feature_id = $MV1 }
} | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/relationships" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }
```
`400`, listing both kinds that matched.

### 4. Establish with `kind` -- the valid case

```powershell
$body = @{
  origin      = @{ feature_class = "POLE"; feature_id = $POLE1 }
  destination = @{ feature_class = "MV_CONDUCTOR"; feature_id = $MV1 }
  kind        = "spatial_support"
} | ConvertTo-Json -Depth 5
$rel = Invoke-RestMethod -Method Post -Uri "http://localhost:3704/relationships" -ContentType "application/json" -Body $body
$rel
```

### 5. `many_to_many` permits reuse on both sides

```powershell
# same pole, a different conductor
$body = @{ origin = @{ feature_class = "POLE"; feature_id = $POLE1 }; destination = @{ feature_class = "MV_CONDUCTOR"; feature_id = $MV2 }; kind = "spatial_support" } | ConvertTo-Json -Depth 5
Invoke-RestMethod -Method Post -Uri "http://localhost:3704/relationships" -ContentType "application/json" -Body $body

# a different pole, the same first conductor
$body = @{ origin = @{ feature_class = "POLE"; feature_id = $POLE2 }; destination = @{ feature_class = "MV_CONDUCTOR"; feature_id = $MV1 }; kind = "spatial_support" } | ConvertTo-Json -Depth 5
Invoke-RestMethod -Method Post -Uri "http://localhost:3704/relationships" -ContentType "application/json" -Body $body
```
Both succeed.

### 6. Cardinality enforcement -- `composite` kind, `one_to_many`

```powershell
# POLE1 -> MV1 under composite: first for this destination, succeeds
$body = @{ origin = @{ feature_class = "POLE"; feature_id = $POLE1 }; destination = @{ feature_class = "MV_CONDUCTOR"; feature_id = $MV1 }; kind = "composite" } | ConvertTo-Json -Depth 5
Invoke-RestMethod -Method Post -Uri "http://localhost:3704/relationships" -ContentType "application/json" -Body $body

# POLE2 -> MV1 under composite: MV1 already has a different origin under this class -- rejected
$body = @{ origin = @{ feature_class = "POLE"; feature_id = $POLE2 }; destination = @{ feature_class = "MV_CONDUCTOR"; feature_id = $MV1 }; kind = "composite" } | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/relationships" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }
```

### 7. Zero trust -- no relationship_classes row at all

```powershell
$xfmrBody = @{ subtype = 1; geometry = @{ type = "Point"; coordinates = @(-0.1270, 51.5067) }; attributes = @{ primary_voltage = "11KV"; condition_rating = "GOOD" } } | ConvertTo-Json -Depth 5
$XFMR = (Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/DIST_XFMR" -ContentType "application/json" -Body $xfmrBody).id

$body = @{ origin = @{ feature_class = "DIST_XFMR"; feature_id = $XFMR }; destination = @{ feature_class = "POLE"; feature_id = $POLE1 } } | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/relationships" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }
```

### 8. Idempotent re-establish, list, revoke

```powershell
# idempotent -- compare id/established_at against $rel from step 4
$body = @{ origin = @{ feature_class = "POLE"; feature_id = $POLE1 }; destination = @{ feature_class = "MV_CONDUCTOR"; feature_id = $MV1 }; kind = "spatial_support" } | ConvertTo-Json -Depth 5
Invoke-RestMethod -Method Post -Uri "http://localhost:3704/relationships" -ContentType "application/json" -Body $body

# list from the pole's side
(Invoke-RestMethod -Uri "http://localhost:3704/relationships?feature_class=POLE&feature_id=$POLE1").relationships

# revoke, then confirm a second revoke 404s
Invoke-RestMethod -Method Delete -Uri "http://localhost:3704/relationships/$($rel.id)"
try { Invoke-RestMethod -Method Delete -Uri "http://localhost:3704/relationships/$($rel.id)" }
catch { $_.ErrorDetails.Message }
```

### 9. Guards

```powershell
# nonexistent feature
$body = @{ origin = @{ feature_class = "POLE"; feature_id = "00000000-0000-0000-0000-000000000000" }; destination = @{ feature_class = "MV_CONDUCTOR"; feature_id = $MV1 }; kind = "spatial_support" } | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/relationships" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }

# a soft-deleted feature can't be related
Invoke-RestMethod -Method Delete -Uri "http://localhost:3704/features/POLE/$POLE1"
$body = @{ origin = @{ feature_class = "POLE"; feature_id = $POLE1 }; destination = @{ feature_class = "MV_CONDUCTOR"; feature_id = $MV1 }; kind = "spatial_support" } | ConvertTo-Json -Depth 5
try { Invoke-RestMethod -Method Post -Uri "http://localhost:3704/relationships" -ContentType "application/json" -Body $body }
catch { $_.ErrorDetails.Message }
Invoke-RestMethod -Method Post -Uri "http://localhost:3704/features/POLE/$POLE1/restore"   # clean up
```
