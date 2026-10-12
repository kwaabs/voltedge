package oms

import (
	"fmt"
	"math"
)

// IEEE 1366 major-event-day (MED) normalisation -- the "2.5 beta" method. Raw
// SAIDI/SAIFI (indices.go) lump catastrophic days (a major storm) in with ordinary
// operations, so a single event can dominate a year's figure. IEEE 1366 defines a
// statistical threshold above which a day is a "major event" and is reported
// separately, leaving a normalised index that reflects day-to-day reliability.
//
// The method, over a reference set of daily SAIDI values:
//
//	take ln(SAIDI_d) for every day with SAIDI_d > 0   (zero-outage days are excluded)
//	alpha = mean of those logs
//	beta  = sample standard deviation of those logs   (n-1)
//	T_MED = exp(alpha + 2.5*beta)
//
// A day whose SAIDI exceeds T_MED is a Major Event Day; its interruptions are
// excluded from the normalised SAIDI/SAIFI and reported as the major-event count.
//
// HONEST LIMITS. (1) IEEE 1366 specifies the reference period as the FIVE YEARS
// preceding the reporting year; this computes the threshold from the daily SAIDI
// values IN THE REPORT WINDOW, so a short window gives a less stable threshold
// (flagged via MEDApplicable + the note). (2) A multi-day outage's clipped
// customer-minutes are attributed to its START day rather than split across each
// calendar day it spans, so MED classification and exclusion operate on whole days
// keyed by outage start -- consistent (the daily rows sum back to the headline
// figures) and conservative for storm detection, but a documented deviation from a
// strict per-calendar-day clip.

// minMEDPositiveDays is the fewest positive-SAIDI days for which the 2.5-beta
// threshold is defined: the sample standard deviation needs at least two points.
// Below this the threshold is not computed and nothing is excluded.
const minMEDPositiveDays = 2

// medBeta is IEEE 1366's multiplier on the log-SAIDI standard deviation. 2.5
// corresponds (lognormally) to roughly the 2.3-sigma upper tail -- the standard's
// chosen balance between excluding genuine catastrophes and not discarding ordinary
// bad days.
const medBeta = 2.5

// majorEventDayThreshold computes the IEEE 1366 major-event-day threshold T_MED
// (in daily-SAIDI units) from a window's per-day SAIDI values. Only days with
// SAIDI > 0 enter the log-statistics, per the standard. ok is false when fewer
// than minMEDPositiveDays positive days exist, so beta -- hence the threshold -- is
// undefined; the caller then excludes nothing.
func majorEventDayThreshold(dailySAIDI []float64) (threshold, alpha, beta float64, positiveDays int, ok bool) {
	logs := make([]float64, 0, len(dailySAIDI))
	for _, s := range dailySAIDI {
		if s > 0 {
			logs = append(logs, math.Log(s))
		}
	}
	positiveDays = len(logs)
	if positiveDays < minMEDPositiveDays {
		return 0, 0, 0, positiveDays, false
	}

	var sum float64
	for _, l := range logs {
		sum += l
	}
	alpha = sum / float64(positiveDays)

	var ss float64
	for _, l := range logs {
		d := l - alpha
		ss += d * d
	}
	beta = math.Sqrt(ss / float64(positiveDays-1)) // sample std dev (n-1)
	threshold = math.Exp(alpha + medBeta*beta)
	return threshold, alpha, beta, positiveDays, true
}

// dailyReliability is one calendar day's interruption totals over the report
// window (keyed by outage start day; see the HONEST LIMITS note): the SAIDI
// numerator (customer-minutes) and the SAIFI numerator (customers whose outage
// began that day).
type dailyReliability struct {
	customerMinutes  float64
	customersStarted int
}

// medOutcome is the MED normalisation for one report: the threshold, the count of
// major event days, and the normalised SAIDI/SAIFI (major days excluded). When the
// threshold is not computable (too few outage days), Applicable is false and the
// normalised figures equal the raw ones -- nothing is excluded.
type medOutcome struct {
	ThresholdSAIDI float64
	MajorEventDays int
	SAIDINorm      float64
	SAIFINorm      float64
	Applicable     bool
	Note           string
}

// computeMED runs the 2.5-beta classification over a window's daily rows and the
// system customer denominator, returning the normalised indices. It is pure (no
// database), so the whole method is unit-testable; the caller supplies the daily
// rows from one grouped query.
func computeMED(daily []dailyReliability, totalCustomers int) medOutcome {
	if totalCustomers <= 0 || len(daily) == 0 {
		return medOutcome{Note: "no customers served or no outage days in the window -- MED not applicable"}
	}

	dailySAIDI := make([]float64, len(daily))
	for i, d := range daily {
		dailySAIDI[i] = d.customerMinutes / float64(totalCustomers)
	}
	threshold, _, _, positiveDays, ok := majorEventDayThreshold(dailySAIDI)

	var totalMin, medMin float64
	var totalCust, medCust, majorDays int
	for i, d := range daily {
		totalMin += d.customerMinutes
		totalCust += d.customersStarted
		if ok && dailySAIDI[i] > threshold {
			majorDays++
			medMin += d.customerMinutes
			medCust += d.customersStarted
		}
	}

	out := medOutcome{ThresholdSAIDI: threshold, MajorEventDays: majorDays, Applicable: ok}
	if ok {
		out.SAIDINorm = (totalMin - medMin) / float64(totalCustomers)
		out.SAIFINorm = float64(totalCust-medCust) / float64(totalCustomers)
		out.Note = fmt.Sprintf("IEEE 1366 2.5β over %d outage days in the window; %d classified as major event days and excluded (strict method uses a 5-year reference)", positiveDays, majorDays)
	} else {
		// Threshold undefined -> exclude nothing; normalised equals raw.
		out.SAIDINorm = totalMin / float64(totalCustomers)
		out.SAIFINorm = float64(totalCust) / float64(totalCustomers)
		out.Note = "too few outage days for an IEEE 1366 2.5β threshold -- no days excluded (normalised = raw)"
	}
	return out
}
