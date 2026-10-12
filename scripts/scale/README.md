# Scale measurement harness (Bet 3)

Turn the deployment-and-scaling guide's *predicted* bottlenecks into *measured
facts* at a real target size (a mid-size DISCO is ~50k–500k features, not 1M).
These are measurement tools you run against a live DB — there's nothing to
build into the app.

## Slice 1 — tiles · `tile-scale-test.sql`

The guide names uncached per-request `ST_AsMVT` as the #1 bottleneck. The one
thing that decides whether it's merely "CPU per render" (addressable by caching
/ read replicas) or "fundamentally broken" is whether the tile query uses a
**GiST index** on `geom`:

- **Index used** → cost scales with *features-in-the-tile* (`O(log N + k)`). Tile
  time stays ~flat as total feature count grows. Good — the scaling fix is
  caching/replicas, not a redesign.
- **No index** → sequential scan, `O(total rows)` on *every tile*. Catastrophic,
  and a must-fix before anything else.

Run it at each scale point and compare section **D** (no index) to **E** (index).
Via the Taskfile (runs through the `gitbash` sidecar, like the seed tasks):

```bash
task tile-scale-test NUM=10000
task tile-scale-test NUM=100000
task tile-scale-test NUM=500000
```

Add `GEOMTYPE=line` to generate conductor-like multi-vertex polylines instead of
points -- the heavier `ST_AsMVTGeom` (line-clipping) case:

```bash
task tile-scale-test NUM=100000 GEOMTYPE=line
task tile-scale-test NUM=500000 GEOMTYPE=line
```

Or directly through the sidecar (the repo is mounted at `/workspace`):

```bash
docker compose exec -T gitbash psql "<container DSN>" \
  -v num=100000 -f /workspace/scripts/scale/tile-scale-test.sql
```

It uses an **isolated `scaletest` schema that it drops at the end** — it never
touches real `gis.*` data, the feature registry, or the map.

**What to report back:** (1) Section A — any real `gis.*` table showing `(NONE)`
for GiST indexes; (2) for D and E at each `num` — the plan node (Seq Scan vs
Index Scan) and the `Execution Time`. That curve is the headline result.

**Pass gate** (Book of Tests §8.2): tile render p95 < ~150 ms, and render time
roughly *flat* across `num` (confirming the index scan). If D grows ~linearly
with `num` while E stays flat, that's exactly the expected index story — and it
tells us the tile work left is caching/replicas, per the scaling guide.

## Slice 2 — trace · `trace-scale-test.sql`

The trace walker (`api/internal/trace/graph.go`) runs **one indexed query per
node** (`neighborsOf`) in a BFS loop capped at **`maxTraceHops = 2000`** — and
the loop **truncates silently** when it hits the cap (no `truncated` flag in the
result). A feeder with >2000 reachable nodes (one serving 2000+ customers has
2000+ service points alone) returns a wrong, incomplete answer with no warning.

This builds an isolated synthetic radial feeder and measures the per-hop lookup,
plus a set-based full-downstream traversal (reachable count, depth, whether it
exceeds the cap):

```bash
task trace-scale-test NUM=1500     # under the cap
task trace-scale-test NUM=5000     # over the cap -- silent truncation
task trace-scale-test NUM=100000   # a large feeder
```

**What to report:** from section B — `reachable_nodes`, `max_depth`, and
`exceeds_hop_cap_2000`; from A/B the plan node and times. The headline is whether
real-sized feeders exceed 2000 nodes (they will) — which makes the silent
truncation a correctness bug to fix (flag truncation; raise/configure the cap).

## Slice 3 — analysis compute kernels · `api/internal/powerflow/scale_bench_test.go`

The tile and trace harnesses above measure **database** round-trip scaling. The
analysis *solve* has a separate cost that the DB can't show, so this slice measures
it in memory (no DB — plain `go test`):

```bash
cd api
# the convergence + monotonicity profiles (always run):
go test ./internal/powerflow/ -run 'Scale' -v
# the timing curves:
go test ./internal/powerflow/ -run X -bench 'SweepRadialChain|NodalRadialChain' -benchmem
```

**What it shows** (measured, 13th-gen i7, 2026-10-12):

- **Balanced sweep is ~O(N)** — two linear tree passes. `BenchmarkSweepRadialChain`:
  **0.40 ms @ 1k, 7.75 ms @ 10k, 90 ms @ 50k** nodes, ~zero allocations.
  `TestSweepScaleMonotonic` confirms a 20k-segment feeder still solves to a finite,
  monotonic profile. **Verdict: scales to whole-network counts.**
- **Nodal solver is O(N²)** — it carries a *dense* N×N Y-bus and sums over every bus
  per Gauss-Seidel update. `BenchmarkNodalRadialChain` Gauss-Seidel: **76 ms @ 100,
  1.05 s @ 400, 8.9 s @ 1000** buses — unmistakably quadratic. **Newton is worse on
  memory**: its dense Jacobian allocates **15 MB @ 100, 211 MB @ 400, 1.3 GB @ 1000**.
  `TestNodalSolverScaleProfile` logs the iteration count over a physical feeder range:
  Gauss-Seidel's climbs with length, Newton's stays at a handful — so Newton is the
  right solver for a long/meshed feeder, but its O(N²) Jacobian memory is the wall.

**Headline finding to report:** the sweep scales; the **nodal solver's dense Y-bus
(and Newton's dense Jacobian) is the ceiling — substation-scale (hundreds of buses),
not whole-network**. If whole-network nodal is ever needed, the fix is a **sparse**
Y-bus/Jacobian (store and iterate only the non-zero neighbours) — a known, bounded
change, not a redesign. A single radial *chain* is also only physical to a modest
length (its total impedance grows with N); real large networks branch into trees.

## Slice 4 — concurrency · `api/internal/powerflow/concurrency_db_test.go`

Many simultaneous DB-backed analysis requests over the shared connection pool, to
find the saturation point the scaling guide predicts (an *unbounded* app pool, so
the ceiling is the primary's `max_connections` / CPU). Needs a seeded DB — runs in
the CI bootstrap job (they are `TestEndToEnd*`), or locally:

```bash
cd api
$env:DATABASE_URL="postgres://postgres:voltedge_dev@localhost:3700/postgres?sslmode=disable"
# correctness under load -- run under -race to make it a data-race probe:
go test ./internal/powerflow/ -run EndToEndConcurrent -race -v
# the throughput-vs-concurrency saturation curve (logged):
go test ./internal/powerflow/ -run EndToEndConcurrencyProfile -v
```

**What it shows:**

- **Correctness under concurrency is asserted** — 320 simultaneous identical fault
  runs must every one match the single-threaded baseline. Under `-race` this is a
  data-race probe over the whole DB-backed path (buildTree, per-node resolve, the
  metadata/curve caches). A mismatch or a race report is a real concurrency bug.
- **Throughput vs concurrency is measured** — the profile logs req/s and error count
  at concurrency 1→64, so the **saturation knee** is a curve you read. Errors only at
  *high* concurrency point at the primary's connection limit (the unbounded-pool
  prediction); the floor (single-threaded) is asserted.

**What to report:** the req/s curve (where it plateaus or dips) and any error count
at the top levels — that's the pool/primary ceiling, and it tells you whether the
scaling fix is a **bounded pool / PgBouncer** (per the scaling guide) or more headroom.

**Measured (2026-10-12, seeded Docker PG, i7-1355U):** throughput is **flat at ~5
req/s from concurrency 1→64** (concurrency=2 was even *slower* than 1), **zero
errors** at 64. So it is **not** connection exhaustion — the single primary
saturates at low concurrency, exactly the scaling guide's "the primary is the
ceiling" prediction; adding client concurrency buys nothing. A single fault run on
the tiny seeded network already takes ~165 ms, which points at the second lever: the
analysis path is **query-chatty** (one query per node in `buildTree`/resolve — an
N+1 pattern), so latency is round-trip-bound and grows with node count. **Fixes, in
order:** (1) batch the per-node resolves into set-based queries (cuts per-request
latency and primary load); (2) horizontal scale — read replicas / caching — per the
scaling guide. Note the `-race` correctness probe needs `CGO_ENABLED=1` + a C
compiler (or Linux CI); the non-race consistency assert still catches result-
corrupting races.

## Next slices (not built yet)

- **Load flow at scale, end-to-end from the DB** — the in-memory benchmark measures
  the *kernel*; a DB-backed variant (seed a large feeder, resolve it, then solve)
  would also fold in the per-node resolve-query cost, the way `trace-scale-test.sql`
  does for the walk. Needs a large-feeder seed generator.
