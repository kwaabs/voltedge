package powerflow

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/uptrace/bun"
)

// Time-current curve (TCC) point loading and interpolation for the
// coordination engine. The curves are *tabulated* (migration 0076 /
// seed/protection_curves_example.sql): a curve is its stored (current, time)
// points and this package reads a device's operate time off them by log-log
// interpolation -- it never evaluates an IEC/IEEE IDMT formula at runtime.
// The standard relay shapes were evaluated from their defining formula once,
// at seed time, into those stored rows; a manufacturer fuse curve is loaded
// as measured points exactly the same way.

// curve point bands (metadata.protection_curve_points.band). A relay carries a
// single operate curve; a fuse carries two bounding curves and coordination
// grades a downstream fuse's total_clear against an upstream device.
const (
	bandOperate    = "operate"
	bandMinMelt    = "minimum_melt"
	bandTotalClear = "total_clear"
)

// curvePoint is one stored (current, time) sample. current is a multiple of
// pickup for a relay curve (quantity = multiple) or absolute amps for a fuse
// curve (quantity = ampere); the caller knows which and passes the right x.
type curvePoint struct {
	current float64
	timeS   float64
}

// curvePointRow is a read-only projection of metadata.protection_curve_points
// joined to its curve header by code. The numeric columns scan into float64
// the same way metadata.MaterialSpec's numeric columns do.
type curvePointRow struct {
	bun.BaseModel `bun:"table:metadata.protection_curve_points,alias:p"`

	Current float64 `bun:"current"`
	TimeS   float64 `bun:"time_s"`
}

// curveCache loads a curve's points once per run and memoises them by
// (code, band) -- a coordination walk may hit the same relay curve on several
// devices, and there is no reason to re-query.
type curveCache struct {
	byKey map[string][]curvePoint
}

func newCurveCache() *curveCache {
	return &curveCache{byKey: map[string][]curvePoint{}}
}

// points returns a curve's stored points for one band, ascending by current.
// An unknown code or a band with no rows returns an empty slice (no error) --
// the caller degrades that device to "operate time unresolved" rather than
// failing the whole study.
func (c *curveCache) points(ctx context.Context, db bun.IDB, code, band string) ([]curvePoint, error) {
	key := code + "|" + band
	if pts, ok := c.byKey[key]; ok {
		return pts, nil
	}

	var rows []curvePointRow
	if err := db.NewSelect().Model(&rows).
		Join("JOIN metadata.protection_curves c ON c.id = p.curve_id").
		Where("c.code = ?", code).
		Where("p.band = ?", band).
		OrderExpr("p.current ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("powerflow: loading curve %q band %q: %w", code, band, err)
	}

	pts := make([]curvePoint, 0, len(rows))
	for _, r := range rows {
		pts = append(pts, curvePoint{current: r.Current, timeS: r.TimeS})
	}
	// Defensive: rely on ordering by value, not on the DB's collation of a
	// numeric column, so interpolation's monotonic-x assumption always holds.
	sort.Slice(pts, func(i, j int) bool { return pts[i].current < pts[j].current })
	c.byKey[key] = pts
	return pts, nil
}

// interpLogLog reads a curve's operate time at current x by interpolating its
// stored points on a log-log axis -- the axis a TCC is a straight line on for
// an IDMT relay, so linear-in-log-log interpolation between tabulated points
// tracks the true characteristic far more faithfully than linear-in-linear
// would between sparse samples. x and every point's current must be > 0 (the
// table's own CHECK guarantees the points; the caller guarantees x).
//
// extrapolated is true when x falls outside the tabulated current range: the
// result is then clamped to the nearest endpoint's time rather than
// extrapolated off the end of the curve, and the flag lets the caller mark
// that device's operate time as resting on the edge of its data. A single-point
// curve returns that point's time for any x (extrapolated unless x equals it).
func interpLogLog(pts []curvePoint, x float64) (timeS float64, extrapolated bool) {
	if len(pts) == 0 || x <= 0 {
		return 0, true
	}
	if len(pts) == 1 {
		return pts[0].timeS, x != pts[0].current
	}

	// Below the first / above the last tabulated current: clamp to the endpoint
	// (a relay curve flattens toward its definite-minimum time at high multiples
	// and shoots up near pickup at low ones -- clamping is the safe read of
	// both, and honestly flagged).
	if x <= pts[0].current {
		return pts[0].timeS, x < pts[0].current
	}
	last := pts[len(pts)-1]
	if x >= last.current {
		return last.timeS, x > last.current
	}

	// Find the bracketing pair and interpolate in log-log space.
	for i := 1; i < len(pts); i++ {
		hi := pts[i]
		if x <= hi.current {
			lo := pts[i-1]
			// A zero-time endpoint (a definite-minimum floor stored as 0) can't be
			// carried into log space; treat the segment as linear-in-log-current
			// on time instead of failing.
			if lo.timeS <= 0 || hi.timeS <= 0 {
				f := (math.Log(x) - math.Log(lo.current)) / (math.Log(hi.current) - math.Log(lo.current))
				return lo.timeS + f*(hi.timeS-lo.timeS), false
			}
			lx, hx := math.Log(lo.current), math.Log(hi.current)
			lt, ht := math.Log(lo.timeS), math.Log(hi.timeS)
			f := (math.Log(x) - lx) / (hx - lx)
			return math.Exp(lt + f*(ht-lt)), false
		}
	}
	// Unreachable given the endpoint clamps above, but return the last point
	// rather than a zero value if it ever is.
	return last.timeS, true
}
