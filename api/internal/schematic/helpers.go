package schematic

import (
	"strconv"
	"strings"
)

// quoteIdent double-quotes a SQL identifier (mirrors crud.quoteIdent). Used only
// for the fixed optionalCols names, never request input.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// toFloat normalizes a scanned numeric value to float64. ST_X/ST_Y come back as
// double precision (float64), but numeric columns (rated_kva/demand_kw) can
// arrive as a string or []byte through the driver, so handle those too.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case int:
		return float64(n), true
	case []byte:
		return parseFloat(string(n))
	case string:
		return parseFloat(n)
	default:
		return 0, false
	}
}

func parseFloat(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// toFloatPtr returns a *float64 for a nullable numeric column, nil when absent.
func toFloatPtr(v any) *float64 {
	if v == nil {
		return nil
	}
	f, ok := toFloat(v)
	if !ok {
		return nil
	}
	return &f
}

// toInt normalizes a scanned integer value (subtype), mirroring crud/connectivity.
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int64:
		return int(n), true
	case int32:
		return int(n), true
	case int:
		return n, true
	case float64:
		return int(n), true
	case []byte:
		i, err := strconv.Atoi(strings.TrimSpace(string(n)))
		return i, err == nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		return i, err == nil
	default:
		return 0, false
	}
}

// toString normalizes a scanned text/uuid value to a Go string.
func toString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return ""
	}
}

// toStrPtr returns a *string for a nullable text column, nil when absent or empty
// (an empty device_state/circuit_ref carries no overlay signal).
func toStrPtr(v any) *string {
	if v == nil {
		return nil
	}
	s := toString(v)
	if s == "" {
		return nil
	}
	return &s
}
