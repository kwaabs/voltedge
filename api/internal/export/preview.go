package export

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// FeaturePreview is the result of rendering one real feature through the CIM
// encoder (see PreviewFeature). When Sampled is false the class had nothing to
// render -- either no instance exists, or it is a geometry-less class the CIM
// export never emits at all -- and Note explains which; XML is then empty.
type FeaturePreview struct {
	ClassCode string
	FeatureID string
	XML       string
	Sampled   bool
	Note      string
}

// PreviewFeature renders exactly one real feature of classCode through the real
// CIM encoder and returns the CIM/XML bytes a full export would produce for it.
//
// It runs the whole writeCIM pipeline on a single-feature layer set rather than
// calling the per-feature emitter in isolation, so the output is a complete,
// self-consistent CIM document -- header, scaffold, the feature's BaseVoltage,
// the equipment element, and its synthesized Terminals/ConnectivityNodes -- with
// no dangling references. The topology pass synthesizes each feature's expected
// terminal set from metadata even with zero connectivity rows, so an isolated
// feature still renders valid singleton terminals/nodes; the only thing lost
// versus a full export is cross-feature node merging, which is irrelevant to a
// per-class preview.
//
// The first feature of the class (in the default export order) is the sample.
// Everything is read under one REPEATABLE READ read-only snapshot, exactly as
// runExport does, then encoded after the tx closes. profileCode "" selects the
// default profile.
//
// A geometry-less class, a class with no rows, or a bad code is not an error:
// PreviewFeature returns Sampled=false with a Note, because "nothing to show" is
// a legitimate, expected answer for the preview. err is reserved for real
// failures (mapping not configured, DB/encoder errors).
func PreviewFeature(ctx context.Context, db *bun.DB, classCode, profileCode string) (FeaturePreview, error) {
	out := FeaturePreview{ClassCode: classCode}

	var (
		layers  []Layer
		mapping *metadata.CIMMapping
		conns   []connRow
		contain map[string]string
		specs   map[string]*metadata.MaterialSpec
	)
	txErr := db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true},
		func(ctx context.Context, tx bun.Tx) error {
			// No bbox, no territory filter, no draft filter -- the CIM-mapping
			// preview is an admin config tool sampling one real feature to show the
			// mapping's output shape, so it reads the whole class. gatherLayers
			// reports a geometry-less class (and a bad code) via a non-zero status;
			// for the preview that is "nothing to sample", not a failure, so surface
			// the message as a Note and stop.
			gathered, status, msg := gatherLayers(ctx, tx, []string{classCode}, nil, nil, nil)
			if status != 0 {
				out.Note = msg
				return nil
			}
			// Exactly one requested class -> at most one layer. Keep only its first
			// feature; connectivity/containment then load scoped to just that one.
			if len(gathered) == 0 || len(gathered[0].Features) == 0 {
				out.Note = "no feature instance of this class exists to sample"
				return nil
			}
			gathered[0].Features = gathered[0].Features[:1]
			layers = gathered[:1]

			m, err := metadata.LoadCIMMapping(ctx, tx, profileCode)
			if err != nil {
				return fmt.Errorf("CIM export is not configured: %w", err)
			}
			mapping = m
			if conns, err = loadConnectivity(ctx, tx, layers); err != nil {
				return err
			}
			if contain, err = loadContainment(ctx, tx, layers); err != nil {
				return err
			}
			if specs, err = loadMaterialSpecs(ctx, tx); err != nil {
				return err
			}
			return nil
		})
	if txErr != nil {
		return FeaturePreview{}, txErr
	}
	if layers == nil {
		return out, nil // not sampled -- Note is set
	}

	var buf bytes.Buffer
	if _, err := writeCIM(&buf, "VoltEdge CIM preview", layers, mapping, conns, contain, specs, time.Now()); err != nil {
		return FeaturePreview{}, err
	}
	out.FeatureID = layers[0].Features[0].ID
	out.XML = buf.String()
	out.Sampled = true
	return out, nil
}
