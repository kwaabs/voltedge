package powerflow

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// concurrency_db_test.go is the "behaviour under simultaneous load" slice of the
// scale work (scripts/scale/README.md Slice 4). The in-memory benchmarks
// (scale_bench_test.go) measure a SINGLE solve's cost; these drive the real
// DB-backed analysis path from many goroutines at once, over the shared connection
// pool, against the seeded network. Two complementary checks, each matched to what
// can honestly be asserted:
//
//   - CORRECTNESS under concurrency is asserted: many simultaneous identical fault
//     runs must every one return the SAME answer as the single-threaded baseline.
//     A shared-state bug (a non-synchronised cache in the metadata/curve layer, a
//     map written concurrently) would corrupt some results or trip the race
//     detector. RUN THIS UNDER `-race` for the real value -- a mismatch or a race
//     report is a genuine concurrency defect.
//   - THROUGHPUT vs concurrency is measured, not asserted: it is hardware- and
//     pool-config-dependent, so the profile logs req/s and error counts across
//     concurrency levels so the operator can SEE the saturation knee (the scaling
//     guide predicts an unbounded app pool, so the ceiling is the primary's
//     max_connections / CPU, not the app).
//
// Both skip without DATABASE_URL (testDB) and, being TestEndToEnd*, run in the CI
// bootstrap job's seeded-Postgres step.

// faultAtNode pulls one node's resolved fault current out of a fault response, or
// NaN when the node or its current is absent -- so a dropped node shows up as a
// mismatch rather than a panic.
func faultAtNode(resp *faultCurrentResponse, featureID string) float64 {
	for i := range resp.Nodes {
		if resp.Nodes[i].FeatureID == featureID {
			if resp.Nodes[i].FaultCurrentAmps == nil {
				return math.NaN()
			}
			return *resp.Nodes[i].FaultCurrentAmps
		}
	}
	return math.NaN()
}

// TestEndToEndConcurrentFaultConsistency fires many simultaneous identical fault
// runs and asserts every one matches the single-threaded baseline at MV-3. Run with
// `-race` to turn this into a data-race probe over the whole DB-backed analysis path
// (buildTree, the per-node resolve, the curve/metadata caches).
func TestEndToEndConcurrentFaultConsistency(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	req := faultCurrentRequest{
		Start:           featureRef{FeatureClass: "MV_BREAKER", FeatureID: seedBRK1},
		NominalVoltageV: 11000,
	}

	base, err := runFaultCurrent(ctx, db, req)
	if err != nil {
		t.Fatalf("baseline fault run: %v", err)
	}
	baseline := faultAtNode(base, seedMV3)
	if math.IsNaN(baseline) || baseline <= 0 {
		t.Fatalf("baseline MV-3 fault current = %v, want a positive number", baseline)
	}

	const workers, perWorker = 32, 10
	var (
		wg      sync.WaitGroup
		errCnt  int64
		mismCnt int64
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				resp, err := runFaultCurrent(ctx, db, req)
				if err != nil {
					atomic.AddInt64(&errCnt, 1)
					continue
				}
				if v := faultAtNode(resp, seedMV3); math.Abs(v-baseline) > 1e-6 {
					atomic.AddInt64(&mismCnt, 1)
				}
			}
		}()
	}
	wg.Wait()

	if errCnt != 0 {
		t.Errorf("%d of %d concurrent fault runs errored", errCnt, workers*perWorker)
	}
	if mismCnt != 0 {
		t.Errorf("%d of %d concurrent fault runs disagreed with the baseline %.2f A -- shared-state/concurrency bug", mismCnt, workers*perWorker, baseline)
	}
	t.Logf("%d concurrent fault runs (%d goroutines) all consistent at %.1f A", workers*perWorker, workers, baseline)
}

// TestEndToEndConcurrencyProfile logs throughput and error count as concurrency
// rises, so the connection-pool / primary saturation knee is a measured curve. It
// asserts only the floor (single-threaded must succeed); errors appearing at HIGH
// concurrency are a reportable finding (e.g. the primary's max_connections with the
// unbounded app pool), surfaced in the log rather than failing the run.
func TestEndToEndConcurrencyProfile(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	req := faultCurrentRequest{
		Start:           featureRef{FeatureClass: "MV_BREAKER", FeatureID: seedBRK1},
		NominalVoltageV: 11000,
	}

	const perLevel = 40 // requests per concurrency level
	for _, c := range []int{1, 2, 4, 8, 16, 32, 64} {
		var (
			wg     sync.WaitGroup
			errCnt int64
		)
		reps := perLevel / c
		if reps < 1 {
			reps = 1
		}
		start := time.Now()
		for g := 0; g < c; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < reps; i++ {
					if _, err := runFaultCurrent(ctx, db, req); err != nil {
						atomic.AddInt64(&errCnt, 1)
					}
				}
			}()
		}
		wg.Wait()
		elapsed := time.Since(start)
		total := c * reps
		var rps float64
		if elapsed > 0 {
			rps = float64(total) / elapsed.Seconds()
		}
		t.Logf("concurrency=%2d: %d reqs in %-12v %7.0f req/s  errors=%d", c, total, elapsed.Round(time.Millisecond), rps, errCnt)

		if c == 1 && errCnt != 0 {
			t.Fatalf("single-threaded fault run errored %d time(s) -- setup problem, not a saturation signal", errCnt)
		}
	}
}
