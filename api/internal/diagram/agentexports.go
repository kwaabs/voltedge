package diagram

// This file is the diagram engine's agent-facing export surface: thin exported
// wrappers over the package's own unexported operation functions, plus exported
// type aliases for their request bodies. It exists so api/internal/agenttools
// (the shared MCP / in-app-assistant tool registry) can drive the exact same
// diagram operations the REST handlers do, without exporting the internal
// request/response types themselves.
//
// Each wrapper calls the private function and returns its result as `any` (plus
// the error), so the concrete response types (instanceResponse, placementResponse,
// elementResponse, templateSummary, sectionalizeResponse) stay package-private --
// an agent tool serializes whatever shape it gets. The request types, by
// contrast, are surfaced as aliases: an alias is the *same* type, so it carries
// the identical json tags, and agenttools can json.Unmarshal a tool's arguments
// straight into one. Only the create/update/body-carrying operations need an
// alias; the read/delete/rename operations take plain string ids.

import (
	"context"

	"github.com/uptrace/bun"
)

// Exported request-body aliases. Each is the same type as the unexported request
// struct it aliases, so it carries the identical json tags -- agenttools
// unmarshals a tool call's arguments straight into these.
type (
	// CreateInstanceInput is the body for CreateInstance (container feature +
	// diagram_type code + optional name).
	CreateInstanceInput = createInstanceRequest
	// PlaceFeatureInput is the body for PlaceFeature (feature ref + x/y/rotation +
	// optional drawn path).
	PlaceFeatureInput = placeRequest
	// UpdatePlacementInput is the body for UpdatePlacement (label visibility +
	// label transform only; geometry is owned by the place/move upsert).
	UpdatePlacementInput = updatePlacementRequest
	// ElementInput is the body for both CreateElement and UpdateElement (a
	// non-feature diagram element: kind, x/y, size, style, ...).
	ElementInput = elementRequest
	// SaveAsTemplateInput is the body for SaveAsTemplate (template name +
	// description).
	SaveAsTemplateInput = createTemplateRequest
	// SectionalizeInput is the body for SectionalizeBus (cut point, new-bus fill,
	// optional bridging sectionalizer).
	SectionalizeInput = sectionalizeRequest
)

// CreateInstance creates a diagram instance for a container feature.
func CreateInstance(ctx context.Context, db bun.IDB, req CreateInstanceInput) (any, error) {
	return createInstance(ctx, db, req)
}

// GetInstance fetches one diagram instance by id.
func GetInstance(ctx context.Context, db bun.IDB, id string) (any, error) {
	return getInstance(ctx, db, id)
}

// ListInstances returns every diagram instance hosted by one container feature.
func ListInstances(ctx context.Context, db bun.IDB, containerFeatureClassCode, containerFeatureID string) (any, error) {
	return listInstances(ctx, db, containerFeatureClassCode, containerFeatureID)
}

// DeleteInstance deletes a diagram instance (cascades to its placements).
func DeleteInstance(ctx context.Context, db bun.IDB, id string) error {
	return deleteInstance(ctx, db, id)
}

// RenameInstance updates a diagram instance's display name (a nil name clears it).
func RenameInstance(ctx context.Context, db bun.IDB, id string, name *string) (any, error) {
	return renameInstance(ctx, db, id, name)
}

// PlaceFeature places (or moves) one feature on one diagram.
func PlaceFeature(ctx context.Context, db bun.IDB, diagramInstanceID string, req PlaceFeatureInput) (any, error) {
	return placeFeature(ctx, db, diagramInstanceID, req)
}

// UpdatePlacement applies a geometry-free label change to one placement.
func UpdatePlacement(ctx context.Context, db bun.IDB, diagramInstanceID, placementID string, req UpdatePlacementInput) (any, error) {
	return updatePlacement(ctx, db, diagramInstanceID, placementID, req)
}

// ListPlacements returns every feature placed on one diagram.
func ListPlacements(ctx context.Context, db bun.IDB, diagramInstanceID string) (any, error) {
	return listPlacements(ctx, db, diagramInstanceID)
}

// RemovePlacement un-places one feature from one diagram.
func RemovePlacement(ctx context.Context, db bun.IDB, diagramInstanceID, placementID string) error {
	return removePlacement(ctx, db, diagramInstanceID, placementID)
}

// CreateElement adds one non-feature element to a diagram.
func CreateElement(ctx context.Context, db bun.IDB, diagramInstanceID string, req ElementInput) (any, error) {
	return createElement(ctx, db, diagramInstanceID, req)
}

// UpdateElement applies a partial change to one element.
func UpdateElement(ctx context.Context, db bun.IDB, diagramInstanceID, elementID string, req ElementInput) (any, error) {
	return updateElement(ctx, db, diagramInstanceID, elementID, req)
}

// ListElements returns every element on one diagram, back-to-front.
func ListElements(ctx context.Context, db bun.IDB, diagramInstanceID string) (any, error) {
	return listElements(ctx, db, diagramInstanceID)
}

// RemoveElement deletes one element from one diagram.
func RemoveElement(ctx context.Context, db bun.IDB, diagramInstanceID, elementID string) error {
	return removeElement(ctx, db, diagramInstanceID, elementID)
}

// SaveAsTemplate captures one diagram as a reusable skeleton.
func SaveAsTemplate(ctx context.Context, db bun.IDB, diagramInstanceID string, req SaveAsTemplateInput) (any, error) {
	return saveAsTemplate(ctx, db, diagramInstanceID, req)
}

// ListTemplates returns the whole reusable template library.
func ListTemplates(ctx context.Context, db bun.IDB) (any, error) {
	return listTemplates(ctx, db)
}

// ApplyTemplate instantiates one template's slots + furniture onto one diagram.
func ApplyTemplate(ctx context.Context, db bun.IDB, diagramInstanceID, templateID string) (any, error) {
	return applyTemplate(ctx, db, diagramInstanceID, templateID)
}

// DeleteTemplate removes one template from the library.
func DeleteTemplate(ctx context.Context, db bun.IDB, templateID string) error {
	return deleteTemplate(ctx, db, templateID)
}

// SectionalizeBus cuts a drawn busbar placement in two, dividing its
// connectivity. Unlike the rest of this package it owns its own transaction, so
// it keeps the concrete *bun.DB parameter the private function requires.
func SectionalizeBus(ctx context.Context, db *bun.DB, diagramInstanceID, placementID string, req SectionalizeInput) (any, error) {
	return sectionalizeBus(ctx, db, diagramInstanceID, placementID, req)
}
