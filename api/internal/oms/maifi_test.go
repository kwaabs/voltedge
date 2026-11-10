package oms

import (
	"testing"
	"time"
)

// t0 anchors a sequence; ops are expressed as minute offsets for readability.
var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func ops(spec ...struct {
	min float64
	typ string
},
) []deviceOp {
	out := make([]deviceOp, len(spec))
	for i, s := range spec {
		out[i] = deviceOp{At: t0.Add(time.Duration(s.min * float64(time.Minute))), OpType: s.typ}
	}
	return out
}

func op(min float64, typ string) struct {
	min float64
	typ string
} {
	return struct {
		min float64
		typ string
	}{min, typ}
}

func TestMomentaryCounts(t *testing.T) {
	const threshold = 5 * time.Minute

	cases := []struct {
		name            string
		ops             []deviceOp
		protectiveOnly  bool
		wantEvents      int
		wantInterrupts  int
	}{
		{
			name:           "trip then reclose within window is one momentary",
			ops:            ops(op(0, opTrip), op(0.5, opReclose)),
			protectiveOnly: true,
			wantEvents:     1,
			wantInterrupts: 1,
		},
		{
			name:           "trip to lockout is sustained, not momentary",
			ops:            ops(op(0, opTrip), op(1, opReclose), op(2, opTrip), op(3, opLockout)),
			protectiveOnly: true,
			wantEvents:     0,
			wantInterrupts: 0,
		},
		{
			name:           "re-strike sequence within window collapses to one event, two interrupts",
			ops:            ops(op(0, opTrip), op(1, opReclose), op(2, opTrip), op(3, opReclose)),
			protectiveOnly: true,
			wantEvents:     1,
			wantInterrupts: 2,
		},
		{
			name:           "restore after the threshold is sustained (ends open within window)",
			ops:            ops(op(0, opTrip), op(6, opReclose)),
			protectiveOnly: true,
			wantEvents:     0,
			wantInterrupts: 0,
		},
		{
			name:           "two separated momentaries are two events",
			ops:            ops(op(0, opTrip), op(0.5, opReclose), op(30, opTrip), op(30.5, opReclose)),
			protectiveOnly: true,
			wantEvents:     2,
			wantInterrupts: 2,
		},
		{
			name:           "manual open/close excluded under protective-only",
			ops:            ops(op(0, opOpen), op(1, opClose)),
			protectiveOnly: true,
			wantEvents:     0,
			wantInterrupts: 0,
		},
		{
			name:           "manual open/close counts under pure-duration",
			ops:            ops(op(0, opOpen), op(1, opClose)),
			protectiveOnly: false,
			wantEvents:     1,
			wantInterrupts: 1,
		},
		{
			name:           "ends still open within window is not momentary",
			ops:            ops(op(0, opTrip), op(1, opReclose), op(2, opTrip)),
			protectiveOnly: true,
			wantEvents:     0,
			wantInterrupts: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotE, gotI := momentaryCounts(c.ops, threshold, c.protectiveOnly)
			if gotE != c.wantEvents || gotI != c.wantInterrupts {
				t.Fatalf("momentaryCounts = (events=%d, interrupts=%d), want (events=%d, interrupts=%d)",
					gotE, gotI, c.wantEvents, c.wantInterrupts)
			}
		})
	}
}
