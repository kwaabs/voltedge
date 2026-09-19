# Feature registry

`runtime.feature_registry` (migration 0108) is a **single cross-class index of
every feature row** in the system — one `(feature_class_code, feature_id)` per
feature, across every compiled `gis.<code>` table. It answers two questions
cheaply, without the caller knowing which per-class table holds a row:

- **"What is this id?"** — resolve a bare feature id (the kind faults / oms / wam
  carry as `asset_id`, and connectivity carries as endpoints) to its class.
- **"What live assets exist (of this class)?"** — a typeahead for an asset-id
  field, so a user picks an asset instead of pasting a raw uuid and hoping.

## Why it exists

Features live in per-class physical tables (`gis.mv_conductor`,
`gis.dist_transformer`, …) minted by the metadata compiler. A feature id is a
globally-unique `gen_random_uuid()`, but nothing told you *which* table a given
id was in — every subsystem that references an asset by id (faults, OMS, WAM,
the connectivity graph) held a bare `(class, id)` pair and had to already know
the class to look the row up. The registry is the one place that maps an id to
its class for all of them.

## Sync: a compiler-attached DB trigger, not app code

The registry is kept in step by a **database trigger**, `runtime.sync_feature_registry()`,
that the compiler attaches to every `gis.<code>` table (`AFTER INSERT OR UPDATE
OR DELETE`, per row). It is deliberately **not** maintained by the Go CRUD layer.

Features are written through many paths — the REST CRUD API, field-sync replay,
MCP tools, and (heavily) **raw-SQL seeds** that `INSERT` straight into `gis.*`.
Only a DB-level trigger catches all of them; an app-level sync would silently
drift the moment a seed ran. This is the same reasoning behind
`gis.sync_tile_function` (the Martin MVT functions), and the compiler re-attaches
the registry trigger on **every** compile the same way it re-syncs the tile
function — a fresh `CREATE TABLE` or a field `ADD COLUMN` alike leaves a correct
trigger behind (idempotent via `DROP TRIGGER IF EXISTS`).

One generic trigger function serves every table shape:

- The **class code** is passed as the trigger argument (`TG_ARGV[0]`) — the gis
  row carries no code column.
- **`territory_id`** exists only on geometric tables; the function reads it via
  `to_jsonb(NEW) ->> 'territory_id'`, which yields `NULL` when the column is
  absent, so no per-class branching is needed. (The compiler's and migration's
  *backfill* SELECTs do branch, because a bare `SELECT territory_id` from a
  table without the column is a hard error.)

`DELETE` removes the registry row; `INSERT`/`UPDATE` upsert it (mirroring
`status` and `deleted_at`, so a **soft delete** keeps the registry row with
`deleted_at` set, and a **purge** — a hard `DELETE FROM gis.<table>` — removes
it). `compiler.Uncompile` clears a class's registry rows after dropping the
table, since the `DROP ... CASCADE` takes the trigger but not the separate
registry rows.

## Schema (migration 0108)

`runtime.feature_registry`:

| column               | notes                                                            |
|----------------------|------------------------------------------------------------------|
| `feature_id`         | **PRIMARY KEY** — gis ids are globally unique, so an id alone identifies the class. No FK (the target table varies by class — the same polymorphic-reference stance as `runtime.feature_connectivity`). |
| `feature_class_code` | the owning class code                                            |
| `status`             | mirrors the gis row (`draft` / `published`)                      |
| `deleted_at`         | set once soft-deleted; pickers filter `deleted_at IS NULL`       |
| `created_at`         | mirrored for newest-first ordering                               |
| `territory_id`       | geometric classes only; `NULL` otherwise                         |
| `registered_at`      | when the registry first saw the row                              |

Partial indexes: `(feature_class_code) WHERE deleted_at IS NULL` (the live-asset
scan) and `(territory_id) WHERE territory_id IS NOT NULL` (Phase-58 spatial
scoping of a picker).

## REST surface (`/feature-registry`, features:read)

```
GET /feature-registry?id=<uuid>                resolve one id -> {feature_id, feature_class_code, class_name, ...} (404 if unknown)
GET /feature-registry?q=<prefix>&class=&live=  typeahead: id prefix, optional class, live-only
```

Read-only (no write verbs — the trigger owns every write) and gated on the same
`features:read` capability as the feature read layer, since it is just an index
of those features. `class_name` is joined from `metadata.feature_classes`.

**Deliberately no per-feature label.** A feature's display name is computed per
class from a `LabelDef` expression at read time in the CRUD layer; a plpgsql
trigger can't cheaply reproduce that, so the registry indexes ids, not names. A
consumer that needs the human label resolves it lazily against the per-class
endpoint once a candidate id is chosen.

**v1 does not territory-scope the typeahead** to the caller's Phase-58 access
(the store supports it via `SearchFilter.TerritoryIDs`, but the HTTP layer passes
none). Wiring the session's resolved territories in is a documented follow-up.

## Frontend

`web/src/hooks/use-feature-registry.ts` — `useFeatureRegistrySearch` (typeahead,
disabled until there's a class or query) and `useFeatureRegistryLookup` (exact
id, disabled under 8 chars, treats 404 as a `null` result).

`web/src/components/map/asset-id-field.tsx` — `AssetIdField`, a drop-in asset-id
input that resolves the id as it's typed/pasted, shows the class inline
(✓ / "No asset with this id"), and auto-fills a paired asset-class field. First
wired into the Faults board's "Log fault" dialog; the OMS/WAM/connectivity
asset-id fields are the intended next consumers.

## Owed

`go build`, `npm run build`, apply migration 0108 (`task migrate`), and a
**recompile of all classes** so every existing `gis.<code>` table gets its
registry trigger attached and its rows backfilled (the 0108 DO-block already does
this once at migration time; the recompile is only needed for classes compiled
*after* an older schema, and is harmless/idempotent otherwise).
