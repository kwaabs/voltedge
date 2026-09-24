package mapannotation

// agentexports.go is the thin exported surface the agenttools registry adapts
// onto -- the same package-private annotation operations the REST handlers call
// (list/get/create/update/delete over bun.IDB), re-exposed as exported wrappers
// returning `any` so api/internal/agenttools can drive them without reaching
// into this package's unexported types. Every wrapper is a straight pass-through
// to the private op; the only real work is List's, which has to accept a plain
// []float64 bbox (the private bbox struct is unexported) and rebuild it here.

import (
	"context"

	"github.com/uptrace/bun"
)

// AnnotationInput is the exported alias for the create/update request body, so
// an agent tool can unmarshal directly into the same shape the REST layer uses
// (kind, geometry, text, style, z_index, territory_id) without this package
// having to export the struct itself.
type AnnotationInput = annotationRequest

// List returns annotations, optionally narrowed to a territory and/or a lon/lat
// viewport. bbox, when supplied, must be [minX, minY, maxX, maxY] (len 4); a nil
// bbox means no spatial filter. A wrong-length bbox is a validation error, not a
// panic.
func List(ctx context.Context, db bun.IDB, territoryID *string, bbox []float64) (any, error) {
	if bbox == nil {
		return listAnnotations(ctx, db, territoryID, nil)
	}
	if len(bbox) != 4 {
		return nil, invalid("bbox must have exactly 4 numbers [minX, minY, maxX, maxY], got %d", len(bbox))
	}
	// annotationBoxFromSlice names the (unexported) bbox type in its own scope --
	// inside List the `bbox` parameter shadows the type, so it can't be named here.
	return listAnnotations(ctx, db, territoryID, annotationBoxFromSlice(bbox))
}

// annotationBoxFromSlice builds the package-private bbox from a validated len-4
// slice. It lives in its own function so the bbox *type* is visible: inside
// List the parameter is named `bbox` (per the tool contract), which shadows the
// type name there.
func annotationBoxFromSlice(b []float64) *bbox {
	return &bbox{MinX: b[0], MinY: b[1], MaxX: b[2], MaxY: b[3]}
}

// Get reads one annotation by id.
func Get(ctx context.Context, db bun.IDB, id string) (any, error) {
	return getAnnotation(ctx, db, id)
}

// Create inserts a new annotation from the request body and returns the
// persisted shape.
func Create(ctx context.Context, db bun.IDB, req AnnotationInput) (any, error) {
	return createAnnotation(ctx, db, req)
}

// Update applies a partial change to the annotation with the given id.
func Update(ctx context.Context, db bun.IDB, id string, req AnnotationInput) (any, error) {
	return updateAnnotation(ctx, db, id, req)
}

// Delete removes one annotation by id.
func Delete(ctx context.Context, db bun.IDB, id string) error {
	return deleteAnnotation(ctx, db, id)
}
