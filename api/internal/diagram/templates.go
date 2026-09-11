package diagram

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// DiagramTemplate mirrors runtime.diagram_templates (migration 0049) -- a
// reusable diagram layout skeleton. Its Payload is written whole and read
// whole, so it stays an opaque json.RawMessage on the model; the templatePayload
// shape below is what saveAsTemplate/applyTemplate marshal into and out of it.
type DiagramTemplate struct {
	bun.BaseModel `bun:"table:runtime.diagram_templates,alias:diagram_templates"`

	ID          string          `bun:"id,pk,nullzero"`
	Name        string          `bun:"name"`
	Description *string         `bun:"description"`
	Payload     json.RawMessage `bun:"payload"`
	CreatedAt   time.Time       `bun:"created_at,nullzero"`
}

// templateElement is one entry in a template's payload: the same structural
// shape as a diagram_element, minus the row identity (id/diagram) and the
// runtime binding columns (bound_class/bound_id -- a saved template is by
// definition unbound). A placement becomes one of these with kind = "slot" and
// slot_class set; a real element is copied field-for-field.
type templateElement struct {
	Kind        string          `json:"kind"`
	X           float64         `json:"x"`
	Y           float64         `json:"y"`
	Width       *float64        `json:"width,omitempty"`
	Height      *float64        `json:"height,omitempty"`
	Rotation    float64         `json:"rotation"`
	Path        json.RawMessage `json:"path,omitempty"`
	Text        *string         `json:"text,omitempty"`
	Style       json.RawMessage `json:"style,omitempty"`
	ZIndex      int             `json:"z_index"`
	SlotClass   *string         `json:"slot_class,omitempty"`
	SlotSubtype *int            `json:"slot_subtype,omitempty"`
	SlotRole    *string         `json:"slot_role,omitempty"`
}

// templatePayload is the whole skeleton stored in diagram_templates.payload.
type templatePayload struct {
	Elements []templateElement `json:"elements"`
}

// createTemplateRequest is the POST /diagrams/{id}/save-as-template body.
type createTemplateRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// applyTemplateRequest is the POST /diagrams/{id}/apply-template body.
type applyTemplateRequest struct {
	TemplateID string `json:"template_id"`
}

// templateSummary is the shape returned for one template in the library list --
// enough to render the "apply" picker (name, what it contains) without shipping
// the whole payload down for every row.
type templateSummary struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Description  *string   `json:"description,omitempty"`
	ElementCount int       `json:"element_count"`
	SlotCount    int       `json:"slot_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// saveAsTemplate captures one diagram as a reusable skeleton: every placed
// feature becomes an empty, class-tagged slot (its feature id deliberately
// dropped -- the template binds to different real features each time), and every
// non-feature element is copied verbatim. The result is one opaque payload row.
func saveAsTemplate(ctx context.Context, db bun.IDB, diagramInstanceID string, req createTemplateRequest) (*templateSummary, error) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, invalid("name is required")
	}

	inst := new(DiagramInstance)
	if err := db.NewSelect().Model(inst).Where("id = ?", diagramInstanceID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("diagram instance %s not found", diagramInstanceID)
		}
		return nil, fmt.Errorf("diagram: save as template: %w", err)
	}

	placements, err := listPlacements(ctx, db, diagramInstanceID)
	if err != nil {
		return nil, err
	}
	elements, err := listElements(ctx, db, diagramInstanceID)
	if err != nil {
		return nil, err
	}

	payload := templatePayload{Elements: make([]templateElement, 0, len(placements)+len(elements))}
	slotCount := 0
	for _, p := range placements {
		fc := p.FeatureClass
		payload.Elements = append(payload.Elements, templateElement{
			Kind:      "slot",
			X:         p.X,
			Y:         p.Y,
			Rotation:  p.Rotation,
			Path:      p.Path,
			SlotClass: &fc,
		})
		slotCount++
	}
	for _, e := range elements {
		if e.Kind == "slot" {
			slotCount++
		}
		payload.Elements = append(payload.Elements, templateElement{
			Kind:        e.Kind,
			X:           e.X,
			Y:           e.Y,
			Width:       e.Width,
			Height:      e.Height,
			Rotation:    e.Rotation,
			Path:        e.Path,
			Text:        e.Text,
			Style:       e.Style,
			ZIndex:      e.ZIndex,
			SlotClass:   e.SlotClass,
			SlotSubtype: e.SlotSubtype,
			SlotRole:    e.SlotRole,
		})
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("diagram: save as template: marshalling payload: %w", err)
	}

	tmpl := &DiagramTemplate{
		Name:        strings.TrimSpace(req.Name),
		Description: req.Description,
		Payload:     raw,
	}
	if _, err := db.NewInsert().Model(tmpl).Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("diagram: save as template: %w", err)
	}

	return &templateSummary{
		ID:           tmpl.ID,
		Name:         tmpl.Name,
		Description:  tmpl.Description,
		ElementCount: len(payload.Elements),
		SlotCount:    slotCount,
		CreatedAt:    tmpl.CreatedAt,
	}, nil
}

// listTemplates returns the whole template library, newest first. It has to
// unmarshal each payload to count its slots/elements for the summary, which is
// cheap for a modest library (templates are authored by hand, not at scale) and
// keeps the row itself schema-free.
func listTemplates(ctx context.Context, db bun.IDB) ([]templateSummary, error) {
	var rows []DiagramTemplate
	if err := db.NewSelect().Model(&rows).Order("created_at DESC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("diagram: list templates: %w", err)
	}

	out := make([]templateSummary, 0, len(rows))
	for i := range rows {
		var payload templatePayload
		// A payload that somehow won't parse still lists, just with zero counts,
		// rather than failing the whole library over one bad row.
		_ = json.Unmarshal(rows[i].Payload, &payload)
		slots := 0
		for _, e := range payload.Elements {
			if e.Kind == "slot" {
				slots++
			}
		}
		out = append(out, templateSummary{
			ID:           rows[i].ID,
			Name:         rows[i].Name,
			Description:  rows[i].Description,
			ElementCount: len(payload.Elements),
			SlotCount:    slots,
			CreatedAt:    rows[i].CreatedAt,
		})
	}
	return out, nil
}

// applyTemplate instantiates one template's skeleton onto one diagram: every
// payload entry becomes a runtime.diagram_elements row (slots unbound, furniture
// as-is). The elements are inserted in one statement so a diagram never ends up
// with half a template applied. Existing content on the diagram is left
// untouched -- a template is additive scaffolding, not a replacement.
func applyTemplate(ctx context.Context, db bun.IDB, diagramInstanceID, templateID string) ([]elementResponse, error) {
	inst := new(DiagramInstance)
	if err := db.NewSelect().Model(inst).Where("id = ?", diagramInstanceID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("diagram instance %s not found", diagramInstanceID)
		}
		return nil, fmt.Errorf("diagram: apply template: %w", err)
	}

	tmpl := new(DiagramTemplate)
	if err := db.NewSelect().Model(tmpl).Where("id = ?", templateID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("diagram template %s not found", templateID)
		}
		return nil, fmt.Errorf("diagram: apply template: %w", err)
	}

	var payload templatePayload
	if err := json.Unmarshal(tmpl.Payload, &payload); err != nil {
		return nil, fmt.Errorf("diagram: apply template: reading payload: %w", err)
	}
	if len(payload.Elements) == 0 {
		return []elementResponse{}, nil
	}

	rows := make([]DiagramElement, 0, len(payload.Elements))
	for _, te := range payload.Elements {
		if !elementKinds[te.Kind] {
			return nil, invalid("template contains an unknown element kind %q", te.Kind)
		}
		style := te.Style
		if len(style) == 0 || string(style) == "null" {
			// The style column is NOT NULL default '{}'; a slot (or any element
			// saved without style) must not insert SQL NULL.
			style = json.RawMessage("{}")
		}
		rows = append(rows, DiagramElement{
			DiagramInstanceID: diagramInstanceID,
			Kind:              te.Kind,
			X:                 te.X,
			Y:                 te.Y,
			Width:             te.Width,
			Height:            te.Height,
			Rotation:          te.Rotation,
			Path:              te.Path,
			Text:              te.Text,
			Style:             style,
			ZIndex:            te.ZIndex,
			SlotClass:         te.SlotClass,
			SlotSubtype:       te.SlotSubtype,
			SlotRole:          te.SlotRole,
		})
	}

	if _, err := db.NewInsert().Model(&rows).Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("diagram: apply template: %w", err)
	}

	out := make([]elementResponse, 0, len(rows))
	for i := range rows {
		out = append(out, toElementResponse(&rows[i]))
	}
	return out, nil
}

// deleteTemplate removes one template from the library -- a real delete, same as
// the rest of this package. Applied copies already live as their own
// diagram_elements rows, so deleting the template never touches any diagram.
func deleteTemplate(ctx context.Context, db bun.IDB, templateID string) error {
	res, err := db.NewDelete().Model((*DiagramTemplate)(nil)).Where("id = ?", templateID).Exec(ctx)
	if err != nil {
		return fmt.Errorf("diagram: delete template: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("diagram: delete template: %w", err)
	}
	if n == 0 {
		return notFound("diagram template %s not found", templateID)
	}
	return nil
}
