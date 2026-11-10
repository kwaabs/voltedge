package oms

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/trace"
)

// MAIFI -- the Momentary Average Interruption Frequency Index (IEEE 1366), the
// momentary counterpart to the sustained SAIFI/SAIDI in indices.go. A momentary
// interruption is a brief loss of supply (voltage-zero up to a few minutes) caused
// by an interrupting device operating and then restoring -- a recloser that trips
// on a transient fault and successfully recloses, say. Unlike a sustained outage it
// never becomes an oms.incident, so it leaves no row in the customer_interruptions
// fact table; its ONLY footprint is the device-operation log (deviceops,
// ops.operations, migration 0147), which records every trip / reclose / open /
// close / lockout on a protective or switching device.
//
// This computes MAIFI straight off that log, over the SAME analysis window and the
// SAME customer denominator N as the sustained indices, so the three frequency
// figures (SAIFI, MAIFI, MAIFI_E) are comparable:
//
//	MAIFI    = Σ (momentary EVENTS × customers below the device) / N
//	MAIFI_E  = Σ (momentary interruptionS × customers below the device) / N
//
// where a momentary EVENT groups a rapid re-strike sequence (several operations
// within the sustained threshold) into one, and MAIFI_E counts each interrupting
// operation. customers-below is the device's downstream load cone, counted by the
// same AffectedCustomers used for outage prediction.
//
// HONEST LIMITS. (1) The customer cone is traced over the CURRENT topology, not
// frozen at the moment the device operated -- the same conservative approximation
// AffectedCustomers already documents; it drifts only if the network below the
// device changed materially since. (2) MAIFI is only as complete as the operation
// log's feeds -- crew entry today, SCADA/relay later -- so a recloser whose
// operations are not logged contributes nothing; the report flags an empty result
// rather than implying zero momentaries. (3) Per-circuit scope is not yet supported
// for MAIFI (reclosers carry no circuit membership), so a circuit-scoped report
// marks MAIFI not-applicable rather than mixing scopes.

// DefaultSustainedThresholdMin is the IEEE 1366 cutoff (minutes) between a momentary
// and a sustained interruption: a device that re-closes within this window counts as
// momentary, longer counts as sustained (and is handled by the incident path).
// Overridable via the sustained_threshold_min query param.
const DefaultSustainedThresholdMin = 5.0

// op_type values from the device-operation log (migration 0147) that bear on a
// momentary: the two interrupting ops, the two restoring ops, and lockout (which
// terminates a reclose sequence as a SUSTAINED outage, excluded from MAIFI).
const (
	opTrip    = "trip"
	opOpen    = "open"
	opReclose = "reclose"
	opClose   = "close"
	opLockout = "lockout"
)

// deviceOp is one logged operation reduced to what momentary sessionization needs:
// when it happened and what it was. Ordered chronologically per device.
type deviceOp struct {
	At     time.Time
	OpType string
}

// momentaryCounts sessionizes ONE device's chronologically-ordered operations into
// momentary interruption events and interrupting operations. It is pure (no
// database), so the whole classification is unit-testable; the caller supplies one
// device's ops from a single grouped read.
//
// A momentary EVENT starts at an interrupting op (trip/open) and consumes every op
// within threshold of that start. The event counts as momentary when the device
// ENDS the window restored (a reclose/close was the last thing it did) and NO
// lockout occurred inside it -- a lockout, or a window that ends still-open, is a
// sustained outage and is not counted here. When protectiveOnly is set, the event
// must also contain an auto-protection op (trip or reclose), so routine manual
// open/close (e.g. a quick test) does not inflate MAIFI; pure-duration mode drops
// that requirement and counts any ≤threshold interrupt→restore.
func momentaryCounts(ops []deviceOp, threshold time.Duration, protectiveOnly bool) (events, interruptingOps int) {
	i := 0
	for i < len(ops) {
		if ops[i].OpType != opTrip && ops[i].OpType != opOpen {
			i++
			continue
		}
		start := ops[i].At
		var lockedOut, endedRestored, hasProtective bool
		interrupts := 0
		j := i
		for j < len(ops) && ops[j].At.Sub(start) <= threshold {
			switch ops[j].OpType {
			case opTrip:
				hasProtective, endedRestored = true, false
				interrupts++
			case opOpen:
				endedRestored = false
				interrupts++
			case opReclose:
				hasProtective, endedRestored = true, true
			case opClose:
				endedRestored = true
			case opLockout:
				lockedOut, endedRestored = true, false
			}
			j++
			if lockedOut {
				break
			}
		}
		momentary := endedRestored && !lockedOut
		if protectiveOnly && !hasProtective {
			momentary = false
		}
		if momentary {
			events++
			interruptingOps += interrupts
		}
		if j <= i { // progress guard (the starting op is always consumed)
			j = i + 1
		}
		i = j
	}
	return events, interruptingOps
}

// maifiResult is the computed momentary summary: the two indices plus the raw
// aggregates behind them, so the caller can echo exactly what went in.
type maifiResult struct {
	MAIFI                 float64
	MAIFIEvent            float64 // MAIFI_E (per interrupting operation)
	MomentaryEvents       int     // Σ events across devices (raw count)
	InterruptingOps       int     // Σ interrupting ops across devices
	CustomerInterruptions int     // Σ events × customers-below (the MAIFI numerator)
	Devices               int     // distinct devices that had a momentary in-window
}

// computeMAIFI reads the device-operation log over [from, to), sessionizes each
// device's operations into momentary events (pure Go, momentaryCounts), traces each
// contributing device's downstream customer cone ONCE (distinct reclosers are few,
// so this is cheap), and totals the two indices over N. protectiveOnly selects the
// momentary definition (default) vs pure-duration.
func computeMAIFI(ctx context.Context, db bun.IDB, from, to time.Time, threshold time.Duration, protectiveOnly bool, totalCustomers int) (maifiResult, error) {
	var res maifiResult

	var rows []struct {
		DeviceClass string    `bun:"device_class"`
		DeviceID    string    `bun:"device_id"`
		OpType      string    `bun:"op_type"`
		OccurredAt  time.Time `bun:"occurred_at"`
	}
	const q = `SELECT device_class, device_id, op_type, occurred_at
FROM ops.operations
WHERE op_type IN ('trip','open','reclose','close','lockout')
  AND occurred_at >= ? AND occurred_at < ?
ORDER BY device_class, device_id, occurred_at`
	if err := db.NewRaw(q, from, to).Scan(ctx, &rows); err != nil {
		return res, fmt.Errorf("oms: reading device operations for MAIFI: %w", err)
	}

	// Group chronological ops by device, preserving first-seen order.
	type key struct{ class, id string }
	grouped := make(map[key][]deviceOp)
	order := make([]key, 0)
	for _, r := range rows {
		k := key{r.DeviceClass, r.DeviceID}
		if _, seen := grouped[k]; !seen {
			order = append(order, k)
		}
		grouped[k] = append(grouped[k], deviceOp{At: r.OccurredAt, OpType: r.OpType})
	}

	var numEvents, numOps float64 // Σ events×customers, Σ interruptingOps×customers
	for _, k := range order {
		events, interrupting := momentaryCounts(grouped[k], threshold, protectiveOnly)
		if events == 0 {
			continue
		}
		_, customers, err := AffectedCustomers(ctx, db, trace.FeatureRef{FeatureClass: k.class, FeatureID: k.id})
		if err != nil {
			return res, fmt.Errorf("oms: tracing customers below %s/%s for MAIFI: %w", k.class, k.id, err)
		}
		res.Devices++
		res.MomentaryEvents += events
		res.InterruptingOps += interrupting
		numEvents += float64(events) * float64(customers)
		numOps += float64(interrupting) * float64(customers)
	}
	res.CustomerInterruptions = int(numEvents)
	if totalCustomers > 0 {
		res.MAIFI = numEvents / float64(totalCustomers)
		res.MAIFIEvent = numOps / float64(totalCustomers)
	}
	return res, nil
}
