package rmu

// backfill.go is the one-shot promotion for RMUs that were placed BEFORE the
// cable side of the fan-out existed (migration 0123 + seed/rmu_templates.sql).
// A fresh RMU now comes with a real MV_CONDUCTOR on each way's cable terminal
// (fanout.go), but every RMU already on the map has a bare, unwired T1. This
// walks those existing RMUs and, for each contained switched way missing its
// cable, creates the MV_CONDUCTOR + wires it exactly as the live fan-out does,
// then rebuilds the RMU's single-line so the promoted cables show on the mimic.
//
// It is the electrical twin of the fan-out cable step, run after the fact:
// same crud.CreateFeature + connectivity.EstablishConnection, same T1 rule
// (MV_CONDUCTOR -> MV_SWITCH.T1), driven by the same template cable definition.
// It is idempotent -- a way that already has an MV_CONDUCTOR on its cable side
// is left untouched (and still contributes to the rebuilt diagram) -- so a
// re-run only fills gaps. It reads the acting user from ctx like every write
// primitive; run from a CLI (api/cmd/rmubackfill) it writes created_by NULL,
// the standard system/out-of-band stamp (auth.ActorID returns "").

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/diagram"
	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// BackfillOptions tunes a backfill run.
type BackfillOptions struct {
	// RMUFeatureClass is the RMU class to scan; defaults to "RMU".
	RMUFeatureClass string
	// DryRun reports what would change without writing anything (no conductor
	// created, no connection wired, no diagram rebuilt).
	DryRun bool
}

// BackfillReport summarises a run for the CLI to print.
type BackfillReport struct {
	DryRun             bool     `json:"dry_run"`
	RMUsScanned        int      `json:"rmus_scanned"`
	RMUsWithCableTmpl  int      `json:"rmus_with_cable_template"`
	WaysExamined       int      `json:"ways_examined"`
	ConductorsCreated  int      `json:"conductors_created"`
	ConductorsExisting int      `json:"conductors_existing"`
	DiagramsRebuilt    int      `json:"diagrams_rebuilt"`
	DiagramsSkipped    int      `json:"diagrams_skipped"`
	Errors             []string `json:"errors,omitempty"`
}

// wayCable pairs a switched way with the id of the MV_CONDUCTOR on its cable
// side (existing or freshly created), in the order the ways are visited -- the
// order the diagram rebuild feeds switch and conductor slots so each cable
// binds under its own way.
type wayCable struct {
	wayID  string
	condID string
}

// BackfillCableConductors promotes the bare cable side of every already-placed
// RMU into a real MV_CONDUCTOR, mirroring the fan-out's new cable step, and
// rebuilds each affected single-line. It never fails the whole run for one RMU:
// a per-RMU error is recorded in the report and the walk continues, so one bad
// row can't strand the rest.
func BackfillCableConductors(ctx context.Context, db *bun.DB, opts BackfillOptions) (*BackfillReport, error) {
	rmuClass := opts.RMUFeatureClass
	if rmuClass == "" {
		rmuClass = "RMU"
	}
	report := &BackfillReport{DryRun: opts.DryRun}

	rmuLoaded, err := metadata.Load(ctx, db, rmuClass)
	if err != nil {
		return nil, fmt.Errorf("load rmu class %q: %w", rmuClass, err)
	}
	if rmuLoaded.FeatureClass.PhysicalTable == nil {
		return nil, fmt.Errorf("rmu class %q has not been compiled -- no physical table", rmuClass)
	}
	rmuTable := *rmuLoaded.FeatureClass.PhysicalTable

	// Every live RMU + its configuration (the config selects the way template).
	// rmuConfigField is a fixed lower_snake_case field name, not user input, but
	// it is quoted defensively (a free removal of any reserved-word surprise),
	// the same way crud/compiler quote their identifiers.
	type rmuRow struct {
		ID     string         `bun:"id"`
		Config sql.NullString `bun:"config"`
	}
	var rmus []rmuRow
	if err := db.NewRaw(
		`SELECT id::text AS id, "`+rmuConfigField+`" AS config `+
			"FROM "+rmuTable+" WHERE deleted_at IS NULL",
	).Scan(ctx, &rmus); err != nil {
		return nil, fmt.Errorf("list rmus: %w", err)
	}
	report.RMUsScanned = len(rmus)

	// Class ids used across every RMU -- resolved once. MV_SWITCH and MV_BUSBAR
	// are the way/bus classes; MV_CONDUCTOR is the cable class the check keys on.
	swLoaded, err := metadata.Load(ctx, db, "MV_SWITCH")
	if err != nil {
		return nil, fmt.Errorf("load MV_SWITCH: %w", err)
	}
	busLoaded, err := metadata.Load(ctx, db, "MV_BUSBAR")
	if err != nil {
		return nil, fmt.Errorf("load MV_BUSBAR: %w", err)
	}

	for _, r := range rmus {
		if !r.Config.Valid || r.Config.String == "" {
			continue // no config -> no template -> nothing to promote
		}
		if err := backfillOneRMU(ctx, db, opts, report, rmuClass, rmuLoaded, swLoaded, busLoaded, r.ID, r.Config.String); err != nil {
			report.Errors = append(report.Errors, fmt.Sprintf("rmu %s: %v", r.ID, err))
		}
	}
	return report, nil
}

func backfillOneRMU(
	ctx context.Context,
	db *bun.DB,
	opts BackfillOptions,
	report *BackfillReport,
	rmuClass string,
	rmuLoaded, swLoaded, busLoaded *metadata.Loaded,
	rmuID, config string,
) error {
	tmpl, err := metadata.LoadRMUTemplate(ctx, db, rmuClass, config)
	if err != nil {
		return fmt.Errorf("load template for config %q: %w", config, err)
	}
	if tmpl == nil || len(tmpl.Ways) == 0 {
		return nil
	}

	// The representative cable definition: the first way that names one. Every
	// way in the seeded RMU templates carries the same cable, so one def covers
	// them all; a template with no cable at all (a future non-cabled config) is
	// simply skipped.
	var cableDef *metadata.RMUTemplateWay
	for i := range tmpl.Ways {
		if tmpl.Ways[i].CableFeatureClass != nil {
			cableDef = &tmpl.Ways[i]
			break
		}
	}
	if cableDef == nil {
		return nil
	}
	report.RMUsWithCableTmpl++

	cableLoaded, err := metadata.Load(ctx, db, *cableDef.CableFeatureClass)
	if err != nil {
		return fmt.Errorf("load cable class %q: %w", *cableDef.CableFeatureClass, err)
	}
	cableTerminal := "T1"
	if cableDef.CableTerminal != nil && *cableDef.CableTerminal != "" {
		cableTerminal = *cableDef.CableTerminal
	}

	// The contained switched ways and the internal bus, from the containment
	// relationships the fan-out wrote (RMU -> MV_SWITCH, RMU -> MV_BUSBAR).
	wayIDs, err := containedFeatureIDs(ctx, db, rmuLoaded.FeatureClass.ID, rmuID, swLoaded.FeatureClass.ID)
	if err != nil {
		return fmt.Errorf("list contained ways: %w", err)
	}
	if len(wayIDs) == 0 {
		return nil
	}
	busIDs, err := containedFeatureIDs(ctx, db, rmuLoaded.FeatureClass.ID, rmuID, busLoaded.FeatureClass.ID)
	if err != nil {
		return fmt.Errorf("list contained bus: %w", err)
	}

	// Ensure a cable on each way. Existing ones are recorded (for the rebuild)
	// but not touched; missing ones are created + wired inside one per-RMU
	// transaction so a failure leaves no half-cabled RMU.
	pairs := make([]wayCable, 0, len(wayIDs))
	ensure := func(exec bun.IDB) error {
		for _, wayID := range wayIDs {
			report.WaysExamined++
			existing, err := existingCableConductor(ctx, exec, wayID, cableLoaded.FeatureClass.ID)
			if err != nil {
				return err
			}
			if existing != "" {
				report.ConductorsExisting++
				pairs = append(pairs, wayCable{wayID: wayID, condID: existing})
				continue
			}
			if opts.DryRun {
				report.ConductorsCreated++ // would create
				continue
			}
			condEnv, err := crud.CreateFeature(ctx, exec, cableLoaded, crud.CreateFeatureInput{
				Subtype:    cableDef.CableSubtypeCode,
				Attributes: cableDef.CableAttributes,
			})
			if err != nil {
				return fmt.Errorf("create cable for way %s: %w", wayID, err)
			}
			condID, err := featureID(condEnv)
			if err != nil {
				return err
			}
			term := cableTerminal
			if _, err := connectivity.EstablishConnection(ctx, exec, connectivity.EstablishConnectionInput{
				From: connectivity.Endpoint{FeatureClass: *cableDef.CableFeatureClass, FeatureID: condID},
				To:   connectivity.Endpoint{FeatureClass: "MV_SWITCH", FeatureID: wayID, Terminal: &term},
			}); err != nil {
				return fmt.Errorf("wire cable to way %s.%s: %w", wayID, term, err)
			}
			report.ConductorsCreated++
			pairs = append(pairs, wayCable{wayID: wayID, condID: condID})
		}
		return nil
	}

	if opts.DryRun {
		if err := ensure(db); err != nil {
			return err
		}
		return nil
	}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return ensure(tx)
	}); err != nil {
		return err
	}

	// Rebuild the single-line so the promoted cables render. Every way now has a
	// recorded conductor (existing or new); feed bus + (way, its cable) in visit
	// order into the same builder the fan-out uses, replacing the old instance.
	instanceID, err := diagram.FindContainerInstanceID(ctx, db, rmuClass, rmuID)
	if err != nil {
		return fmt.Errorf("find diagram: %w", err)
	}
	if instanceID == "" || len(pairs) != len(wayIDs) {
		// No auto-generated diagram to rebuild, or some way still lacks a cable
		// (only possible if a create was skipped) -- leave the diagram alone.
		report.DiagramsSkipped++
		return nil
	}

	components := make([]diagram.AssemblyComponent, 0, len(pairs)*2+1)
	if len(busIDs) > 0 {
		components = append(components, diagram.AssemblyComponent{FeatureClass: "MV_BUSBAR", FeatureID: busIDs[0]})
	}
	for _, p := range pairs {
		components = append(components,
			diagram.AssemblyComponent{FeatureClass: "MV_SWITCH", FeatureID: p.wayID},
			diagram.AssemblyComponent{FeatureClass: *cableDef.CableFeatureClass, FeatureID: p.condID},
		)
	}

	if _, err := diagram.GenerateAssemblyDiagram(ctx, db, diagram.GenerateAssemblyDiagramInput{
		ContainerClass:    rmuClass,
		ContainerID:       rmuID,
		DiagramType:       diagram.AssemblyDiagramType,
		TemplateName:      rmuDiagramTemplates[config],
		Components:        components,
		ReplaceInstanceID: instanceID,
	}); err != nil {
		return fmt.Errorf("rebuild diagram: %w", err)
	}
	report.DiagramsRebuilt++
	return nil
}

// containedFeatureIDs returns the ids of features of destClassID that the given
// origin feature contains, via the runtime.feature_relationships the fan-out
// wrote. Ordered by established_at so the walk is stable across runs.
func containedFeatureIDs(ctx context.Context, db bun.IDB, originClassID, originID, destClassID string) ([]string, error) {
	var rows []struct {
		ID string `bun:"id"`
	}
	if err := db.NewRaw(
		"SELECT destination_feature_id::text AS id FROM runtime.feature_relationships "+
			"WHERE origin_feature_class_id = ? AND origin_feature_id = ? AND destination_feature_class_id = ? "+
			"ORDER BY established_at",
		originClassID, originID, destClassID,
	).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

// existingCableConductor returns the id of the MV_CONDUCTOR already wired to the
// given way's cable side, or "" if none. The T1 rule stores the conductor
// canonically as the FROM side (MV_CONDUCTOR -> MV_SWITCH.T1), so a row whose TO
// feature is the way and whose FROM class is the conductor class is the cable.
func existingCableConductor(ctx context.Context, db bun.IDB, wayID, condClassID string) (string, error) {
	var rows []struct {
		ID string `bun:"id"`
	}
	if err := db.NewRaw(
		"SELECT from_feature_id::text AS id FROM runtime.feature_connectivity "+
			"WHERE to_feature_id = ? AND from_feature_class_id = ? LIMIT 1",
		wayID, condClassID,
	).Scan(ctx, &rows); err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0].ID, nil
}
