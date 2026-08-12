package relationship

import "github.com/kwaabs/voltedge/api/internal/metadata"

// matchClass picks the relationship_classes row that authorizes a
// specific origin/destination relationship, or explains why none does.
// Zero trust, same principle as api/internal/connectivity's network_rules
// matching: classes is every relationship_classes row for this origin/
// destination feature-class pairing (loaded via
// metadata.LoadRelationshipClasses). Empty means no relationship_classes
// row was ever authored for this pairing -- rejected outright, not a
// special case.
//
// Unlike network_rules, there's no valid/invalid split to report
// separately here -- relationship_classes has no such flag. But there
// can legally be more than one kind of relationship for the same
// pairing, so a request naming no kind is only accepted when exactly one
// class matches; naming a kind that doesn't match, or leaving it out
// when more than one class applies, both get a specific, actionable
// error rather than silently picking one.
func matchClass(classes []metadata.RelationshipClass, kind *string) (*metadata.RelationshipClass, error) {
	if len(classes) == 0 {
		return nil, invalid("no relationship class permits this relationship -- zero trust default: absence of a relationship_classes row means the relationship is invalid")
	}

	if kind != nil {
		for i := range classes {
			if classes[i].Kind == *kind {
				return &classes[i], nil
			}
		}
		return nil, invalid("no relationship class of kind %q matches this origin/destination pairing", *kind)
	}

	if len(classes) > 1 {
		kinds := make([]string, len(classes))
		for i, c := range classes {
			kinds[i] = c.Kind
		}
		return nil, invalid("multiple relationship classes match this pairing %v -- specify kind to disambiguate", kinds)
	}

	return &classes[0], nil
}
