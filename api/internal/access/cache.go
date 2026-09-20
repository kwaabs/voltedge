package access

import (
	"context"
	"sync"
	"time"

	"github.com/uptrace/bun"
)

// effectiveCacheTTL bounds how long a cached effective-territory set is served
// before it is recomputed even absent an explicit invalidation. Map tiles
// (Phase 58 Route A) arrive in bursts -- many per pan/zoom -- and each one
// loads the session and so needs the effective set; without a cache the
// recursive resolver (EffectiveIDs) would run per tile. The TTL is only a
// backstop: grant and manager mutations call InvalidateAll for true instant
// revocation, so it can be generous.
const effectiveCacheTTL = 60 * time.Second

type effectiveCacheEntry struct {
	ids       []string
	expiresAt time.Time
}

var (
	effectiveCacheMu sync.RWMutex
	effectiveCache   = map[string]effectiveCacheEntry{}

	// Private-drafts (Phase 61): the subordinate-set cache, keyed by user, under
	// its own mutex. Same rationale as the effective-territory cache -- every
	// scoped request (and every tile) resolves the viewer's subordinate set to
	// build the draft-visibility predicate, so a burst shares one resolver run.
	// Flushed by the same InvalidateAll, since a manager change reshapes exactly
	// these sets too.
	subordinateCacheMu sync.RWMutex
	subordinateCache   = map[string]effectiveCacheEntry{}
)

// CachedEffectiveIDs is EffectiveIDs behind a process-local, TTL-bounded cache
// keyed by user. The session middleware (and the API-token resolver) call it on
// every request to populate SessionUser.AllowedTerritories, so a burst of tile
// requests for one user shares a single resolver run.
//
// The returned slice is the cached instance -- callers read it (to build a
// `territory_id = ANY(...)` predicate) and MUST NOT mutate it.
func CachedEffectiveIDs(ctx context.Context, db bun.IDB, userID string) ([]string, error) {
	now := time.Now()

	effectiveCacheMu.RLock()
	e, ok := effectiveCache[userID]
	effectiveCacheMu.RUnlock()
	if ok && now.Before(e.expiresAt) {
		return e.ids, nil
	}

	ids, err := EffectiveIDs(ctx, db, userID)
	if err != nil {
		return nil, err
	}

	effectiveCacheMu.Lock()
	effectiveCache[userID] = effectiveCacheEntry{ids: ids, expiresAt: now.Add(effectiveCacheTTL)}
	effectiveCacheMu.Unlock()
	return ids, nil
}

// CachedSubordinates is Subordinates behind a process-local, TTL-bounded cache
// keyed by user -- the draft-visibility counterpart of CachedEffectiveIDs. The
// session/token resolvers call it to populate SessionUser.Subordinates, so a
// burst of requests (or tiles) for one user shares a single resolver run.
//
// The returned slice is the cached instance -- callers read it (to build the
// draft-visibility predicate) and MUST NOT mutate it.
func CachedSubordinates(ctx context.Context, db bun.IDB, userID string) ([]string, error) {
	now := time.Now()

	subordinateCacheMu.RLock()
	e, ok := subordinateCache[userID]
	subordinateCacheMu.RUnlock()
	if ok && now.Before(e.expiresAt) {
		return e.ids, nil
	}

	ids, err := Subordinates(ctx, db, userID)
	if err != nil {
		return nil, err
	}

	subordinateCacheMu.Lock()
	subordinateCache[userID] = effectiveCacheEntry{ids: ids, expiresAt: now.Add(effectiveCacheTTL)}
	subordinateCacheMu.Unlock()
	return ids, nil
}

// InvalidateAll drops every cached effective set. It is called after any grant
// or manager mutation COMMITS (a grant/revoke changes the grantee and everyone
// who manages them up the org tree; a manager change reshapes whole subtrees),
// so the next request recomputes from fresh state -- the "instant revocation"
// the phase requires. A full flush rather than per-user invalidation is
// deliberate: these are rare admin actions, and recomputing every active user's
// set once is far cheaper than getting partial invalidation of the manager tree
// subtly wrong. It must run AFTER the transaction commits, or a concurrent
// request could repopulate the cache from pre-commit state.
func InvalidateAll() {
	effectiveCacheMu.Lock()
	effectiveCache = map[string]effectiveCacheEntry{}
	effectiveCacheMu.Unlock()

	// The subordinate-set cache (Private-drafts) is reshaped by the same manager
	// mutations that reshape effective territories, so flush it in lockstep.
	subordinateCacheMu.Lock()
	subordinateCache = map[string]effectiveCacheEntry{}
	subordinateCacheMu.Unlock()
}
