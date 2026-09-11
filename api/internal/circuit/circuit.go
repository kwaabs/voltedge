package circuit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/uptrace/bun"
)

// InvalidError is a client-fixable problem (blank name, unknown network, a
// non-positive voltage, a half-filled source reference, a duplicate name in a
// network) -- distinct from a plain error so an HTTP caller can map it to a
// 400 rather than a 500, the same split api/internal/territory and
// api/internal/crud draw. Callers test with IsInvalid.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) error {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}

// IsInvalid reports whether err is (or wraps) an InvalidError.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

// ListCircuits returns the catalog ordered by network then name. An optional
// network filter ("MV"/"LV") narrows it -- the circuit_id picker on an MV
// class shows only MV circuits. The set is small (circuits, not features), so
// it's fetched whole rather than paged.
func ListCircuits(ctx context.Context, db bun.IDB, network string) ([]Circuit, error) {
	var circuits []Circuit
	q := db.NewSelect().Model(&circuits).Order("network", "name")
	if network != "" {
		q = q.Where("network = ?", network)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("circuit: listing circuits: %w", err)
	}
	return circuits, nil
}

// GetCircuit fetches one circuit by id.
func GetCircuit(ctx context.Context, db bun.IDB, id string) (*Circuit, error) {
	c := new(Circuit)
	if err := db.NewSelect().Model(c).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("circuit %q does not exist", id)
		}
		return nil, fmt.Errorf("circuit: loading circuit %s: %w", id, err)
	}
	return c, nil
}

// CreateParams is the input to CreateCircuit. VoltageKV and the source
// reference are optional; when a source is given, both its class and id must
// be present (a class without an id, or vice versa, points nowhere).
type CreateParams struct {
	Name               string
	Network            string
	VoltageKV          *float64
	SourceFeatureClass *string
	SourceFeatureID    *string
}

// CreateCircuit inserts a circuit after validating its shape and rejecting a
// duplicate name within the same network (the DB's unique index is the final
// guard; this pre-check turns the raw constraint violation into a clear 400).
func CreateCircuit(ctx context.Context, db bun.IDB, p CreateParams) (*Circuit, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, invalidf("circuit name is required")
	}
	if err := validateNetwork(p.Network); err != nil {
		return nil, err
	}
	if err := validateVoltage(p.VoltageKV); err != nil {
		return nil, err
	}
	if err := validateSource(p.SourceFeatureClass, p.SourceFeatureID); err != nil {
		return nil, err
	}
	if err := ensureNameFree(ctx, db, p.Network, name, ""); err != nil {
		return nil, err
	}

	// Raw INSERT listing only the columns we set, so the DB defaults for id
	// (gen_random_uuid) and the timestamps fire -- the same approach territory
	// uses rather than binding zero values through the bun model.
	const query = `
INSERT INTO gis.circuits (name, network, voltage_kv, source_feature_class, source_feature_id)
VALUES (?, ?, ?, ?, ?)
RETURNING id`
	var id string
	if err := db.QueryRowContext(ctx, query, name, p.Network, p.VoltageKV, p.SourceFeatureClass, p.SourceFeatureID).Scan(&id); err != nil {
		return nil, fmt.Errorf("circuit: inserting circuit: %w", err)
	}
	return GetCircuit(ctx, db, id)
}

// UpdateParams is the input to UpdateCircuit. Every field is optional -- only
// what's set is changed. Network is intentionally absent: a circuit's network
// is its identity namespace (and, later, which classes' features may join it),
// so it is immutable -- change it by deleting and recreating. ClearSource
// removes the source reference; otherwise a set pair replaces it.
type UpdateParams struct {
	Name               *string
	VoltageKV          *float64
	ClearVoltage       bool
	SourceFeatureClass *string
	SourceFeatureID    *string
	ClearSource        bool
}

// UpdateCircuit edits a circuit's name, voltage, and/or source reference. A
// rename is re-checked for a duplicate within the (unchanged) network.
func UpdateCircuit(ctx context.Context, db bun.IDB, id string, p UpdateParams) (*Circuit, error) {
	existing, err := GetCircuit(ctx, db, id)
	if err != nil {
		return nil, err
	}

	var sets []string
	var args []any

	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" {
			return nil, invalidf("circuit name cannot be empty")
		}
		if err := ensureNameFree(ctx, db, existing.Network, name, id); err != nil {
			return nil, err
		}
		sets = append(sets, "name = ?")
		args = append(args, name)
	}

	switch {
	case p.ClearVoltage:
		sets = append(sets, "voltage_kv = NULL")
	case p.VoltageKV != nil:
		if err := validateVoltage(p.VoltageKV); err != nil {
			return nil, err
		}
		sets = append(sets, "voltage_kv = ?")
		args = append(args, *p.VoltageKV)
	}

	switch {
	case p.ClearSource:
		sets = append(sets, "source_feature_class = NULL", "source_feature_id = NULL")
	case p.SourceFeatureClass != nil || p.SourceFeatureID != nil:
		if err := validateSource(p.SourceFeatureClass, p.SourceFeatureID); err != nil {
			return nil, err
		}
		sets = append(sets, "source_feature_class = ?", "source_feature_id = ?")
		args = append(args, *p.SourceFeatureClass, *p.SourceFeatureID)
	}

	if len(sets) == 0 {
		return nil, invalidf("update body has nothing to change")
	}
	sets = append(sets, "updated_at = now()")
	args = append(args, id)

	query := fmt.Sprintf("UPDATE gis.circuits SET %s WHERE id = ?", strings.Join(sets, ", "))
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return nil, fmt.Errorf("circuit: updating circuit %s: %w", id, err)
	}
	return GetCircuit(ctx, db, id)
}

// DeleteCircuit removes a circuit. Features that reference it via their
// circuit_id column are not checked here (that field arrives in a later
// slice); like territory's delete, membership cleanup is a write-path concern,
// not this catalog's.
func DeleteCircuit(ctx context.Context, db bun.IDB, id string) error {
	if _, err := GetCircuit(ctx, db, id); err != nil {
		return err
	}
	if _, err := db.NewDelete().Model((*Circuit)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("circuit: deleting circuit %s: %w", id, err)
	}
	return nil
}

// validateNetwork rejects anything but the two known namespaces.
func validateNetwork(network string) error {
	switch network {
	case NetworkMV, NetworkLV:
		return nil
	default:
		return invalidf("circuit network must be %q or %q, got %q", NetworkMV, NetworkLV, network)
	}
}

// validateVoltage rejects a non-positive nominal voltage (NULL is fine --
// unknown until surveyed).
func validateVoltage(kv *float64) error {
	if kv != nil && *kv <= 0 {
		return invalidf("circuit voltage must be positive, got %g", *kv)
	}
	return nil
}

// validateSource enforces that the source reference is all-or-nothing: a class
// without an id (or an id without a class) points at no real feature.
func validateSource(class, id *string) error {
	classSet := class != nil && strings.TrimSpace(*class) != ""
	idSet := id != nil && strings.TrimSpace(*id) != ""
	if classSet != idSet {
		return invalidf("circuit source needs both a feature class and a feature id, or neither")
	}
	return nil
}

// ensureNameFree rejects a name already used by another circuit in the same
// network, case-insensitively (matching the unique index on (network,
// lower(name))). excludeID is the row being updated, skipped so a no-op
// rename to its own name is allowed.
func ensureNameFree(ctx context.Context, db bun.IDB, network, name, excludeID string) error {
	q := db.NewSelect().Model((*Circuit)(nil)).
		Where("network = ?", network).
		Where("lower(name) = lower(?)", name)
	if excludeID != "" {
		q = q.Where("id <> ?", excludeID)
	}
	exists, err := q.Exists(ctx)
	if err != nil {
		return fmt.Errorf("circuit: checking name uniqueness: %w", err)
	}
	if exists {
		return invalidf("a %s circuit named %q already exists", network, name)
	}
	return nil
}
