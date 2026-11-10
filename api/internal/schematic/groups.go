package schematic

import (
	"context"
	"fmt"
	"strings"

	"github.com/uptrace/bun"
)

// OneLineGroup is a fenceable grouping of one-line nodes -- a container (a substation
// / bay holding equipment) or an assembly (an anchor feature + its components). Nodes
// carry a GroupID matching one of these; the client draws a labelled fence around each
// group's members. Membership comes from the authoritative relationship graph
// (runtime.feature_relationships, kind in containment/spatial_support), NOT any stored column.
type OneLineGroup struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // "container" | "assembly"
}

type groupRel struct {
	bun.BaseModel `bun:"table:runtime.feature_relationships,alias:fr"`
	OriginClassID string `bun:"origin_feature_class_id"`
	OriginID      string `bun:"origin_feature_id"`
	DestID        string `bun:"destination_feature_id"`
}

func humanizeCode(code string) string {
	return strings.Title(strings.ToLower(strings.ReplaceAll(code, "_", " "))) //nolint:staticcheck // Title is fine for ASCII codes
}

func shortID(id string) string {
	if len(id) > 6 {
		return id[:6]
	}
	return id
}

// resolveGroups groups the given one-line nodes by containment: a child rolls up to
// its container; a node that is itself a container/anchor (it has children) forms its
// own group with its children. Returns groupOf (nodeId -> groupId) for grouped nodes
// and the metadata for each group that has >= 2 member nodes on the one-line (a lone
// device isn't a group). The layout is unchanged (Light fencing); the client boxes a
// group around wherever its members landed.
func resolveGroups(ctx context.Context, db bun.IDB, nodeIDs []string, nodeClassCode map[string]string) (map[string]string, []OneLineGroup, error) {
	if len(nodeIDs) == 0 {
		return map[string]string{}, nil, nil
	}

	classes, err := allClasses(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[string]*classMeta, len(classes))
	byCode := make(map[string]*classMeta, len(classes))
	for i := range classes {
		byID[classes[i].id] = &classes[i]
		byCode[classes[i].code] = &classes[i]
	}

	// Grouping edges: origin = container/anchor, destination = child.
	//   - 'containment'     -> substations / bays holding equipment.
	//   - 'spatial_support' -> an assembly's components held by their anchor.
	// (A tower-arrangement also uses spatial_support, but its children are conductors,
	// which are wires not nodes, so such a group never reaches the >=2-node threshold.)
	var rels []groupRel
	if err := db.NewSelect().Model(&rels).
		Join("JOIN metadata.relationship_classes rc ON rc.id = fr.relationship_class_id").
		Where("rc.kind IN (?)", bun.In([]string{"containment", "spatial_support"})).
		Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("schematic: group edges: %w", err)
	}

	type parentRef struct{ id, classID string }
	parentOf := make(map[string]parentRef, len(rels))
	hasChildren := make(map[string]bool, len(rels))
	for _, r := range rels {
		parentOf[r.DestID] = parentRef{id: r.OriginID, classID: r.OriginClassID}
		hasChildren[r.OriginID] = true
	}

	// groupOf per node + the resolved class id of each group feature.
	groupOf := make(map[string]string)
	groupClassID := make(map[string]string)
	for _, id := range nodeIDs {
		if hasChildren[id] {
			// An anchor/container that is itself a one-line node -> its own group.
			groupOf[id] = id
			if cm := byCode[nodeClassCode[id]]; cm != nil {
				groupClassID[id] = cm.id
			}
			continue
		}
		if p, ok := parentOf[id]; ok {
			groupOf[id] = p.id
			groupClassID[p.id] = p.classID
		}
	}

	// Keep only groups with >= 2 member nodes.
	count := make(map[string]int)
	for _, g := range groupOf {
		count[g]++
	}
	for id := range groupOf {
		if count[groupOf[id]] < 2 {
			delete(groupOf, id)
		}
	}

	// Which group features are assemblies? Read assembly_ref per group feature (a text
	// system column on every gis table; null = not an assembly), batched per class.
	idsByClass := make(map[string][]string) // classID -> group ids
	seen := make(map[string]bool)
	for _, gid := range groupOf {
		if seen[gid] {
			continue
		}
		seen[gid] = true
		idsByClass[groupClassID[gid]] = append(idsByClass[groupClassID[gid]], gid)
	}

	assemblyRef := make(map[string]string) // group id -> assembly_ref (code), "" if none
	for classID, ids := range idsByClass {
		cm := byID[classID]
		if cm == nil {
			continue
		}
		var rows []struct {
			ID  string  `bun:"id"`
			Ref *string `bun:"assembly_ref"`
		}
		if err := db.NewSelect().
			ColumnExpr("id::text AS id").ColumnExpr("assembly_ref").
			TableExpr(cm.table).
			Where("id IN (?)", bun.In(ids)).
			Scan(ctx, &rows); err != nil {
			return nil, nil, fmt.Errorf("schematic: reading assembly_ref for %s: %w", cm.code, err)
		}
		for _, r := range rows {
			if r.Ref != nil && *r.Ref != "" {
				assemblyRef[r.ID] = *r.Ref
			}
		}
	}

	// Human label per group: an assembly uses its template name; a container uses its
	// class name + a short id (so two substations are distinguishable).
	groups := make([]OneLineGroup, 0, len(idsByClass))
	for gid := range seen {
		classID := groupClassID[gid]
		cm := byID[classID]
		code := ""
		if cm != nil {
			code = cm.code
		}
		if ref := assemblyRef[gid]; ref != "" {
			label := assemblyLabel(ctx, db, classID, ref)
			if label == "" {
				label = humanizeCode(ref)
			}
			groups = append(groups, OneLineGroup{ID: gid, Label: label, Kind: "assembly"})
			continue
		}
		groups = append(groups, OneLineGroup{ID: gid, Label: fmt.Sprintf("%s %s", humanizeCode(code), shortID(gid)), Kind: "container"})
	}

	return groupOf, groups, nil
}

// assemblyLabel resolves the human name of an assembly kit from its anchor class +
// template code (metadata.assembly_templates, unique on (anchor_feature_class_id, code)).
func assemblyLabel(ctx context.Context, db bun.IDB, anchorClassID, code string) string {
	var name string
	err := db.NewSelect().
		ColumnExpr("name").
		TableExpr("metadata.assembly_templates").
		Where("anchor_feature_class_id = ? AND code = ?", anchorClassID, code).
		Limit(1).
		Scan(ctx, &name)
	if err != nil {
		return ""
	}
	return name
}
