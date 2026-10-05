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

## Next slices (not built yet)

- **Load flow at scale** — the balanced sweep over a large radial feeder (rides on
  a valid-feeder generator).
- **Concurrency** — many simultaneous tile/read requests (pgbench-style), to find
  the connection-pool / primary saturation point the guide predicts.
