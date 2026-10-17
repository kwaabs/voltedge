package externallink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/externalsystem"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
	"github.com/uptrace/bun"
)

// placeholderRe matches {name} tokens in a system's path_template.
var placeholderRe = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

// FetchFeatureExternalData resolves a feature's external_ref for ONE external system
// into live data (the Slice-2 fetch of the jsonb-on-the-feature design, migration
// 0145): read external_ref off the feature row, take the value for `systemCode`, fill
// that system's path_template from it, and fetch through the same SSRF-hardened proxy
// the api-link path uses. The caller MUST have already enforced read scope on the
// feature (the handler does, via crud.EnforceFeatureReadScope) -- this function trusts
// that the feature is visible to the actor.
func FetchFeatureExternalData(ctx context.Context, db bun.IDB, cipher *secretbox.Cipher, loaded *metadata.Loaded, featureID, systemCode string) (*linkDataResponse, error) {
	systemCode = strings.TrimSpace(systemCode)
	if systemCode == "" {
		return nil, invalid("system is required")
	}
	if loaded.FeatureClass.PhysicalTable == nil {
		return nil, invalid("feature class %s is not compiled", loaded.FeatureClass.Code)
	}

	// 1. Read external_ref straight off the feature's physical table. The table name
	// comes from compiled metadata (trusted), the id is a bound parameter.
	var raw []byte
	err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT external_ref FROM %s WHERE id = ? AND deleted_at IS NULL", *loaded.FeatureClass.PhysicalTable),
		featureID,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("feature %s not found", featureID)
		}
		return nil, fmt.Errorf("externallink: feature-fetch: read external_ref: %w", err)
	}
	if len(raw) == 0 {
		return nil, notFound("feature %s has no external references", featureID)
	}
	var refs map[string]json.RawMessage
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil, invalid("the feature's external_ref is not a JSON object")
	}
	val, ok := refs[systemCode]
	if !ok {
		return nil, notFound("feature %s has no reference for system %q", featureID, systemCode)
	}

	// 2. Load the system and build the request path from its template + the ref value.
	sys, err := loadSystemByCode(ctx, db, systemCode)
	if err != nil {
		return nil, err
	}
	if sys.PathTemplate == nil || strings.TrimSpace(*sys.PathTemplate) == "" {
		return nil, invalid("system %s has no fetch path configured (it is push-only or link-only)", systemCode)
	}
	path, err := fillPathTemplate(*sys.PathTemplate, val)
	if err != nil {
		return nil, err
	}
	// The FILLED path must still be relative: a hostile/garbled external_ref value
	// could otherwise smuggle a scheme or host and repoint the request at a different
	// origin (SSRF). Reuse the same relative-path check the api-link path uses; the
	// network-level IP guard in safeClient is the second line of defence.
	if _, err := validateResourcePath(path); err != nil {
		return nil, err
	}

	// 3. Fetch -- shared proxy core (auth, SSRF client, size cap, normalize).
	return doFetch(ctx, sys, cipher, path)
}

// fillPathTemplate substitutes {name} tokens in a system's path_template from a
// feature's external_ref value for that system. A SCALAR string ref fills {id} and
// {ref}; an OBJECT ref fills each {name} from its matching field (scalar fields only).
// A referenced token the ref doesn't provide is an error -- a clear message beats a
// silently wrong URL. Each substituted value is path-escaped so an id with a slash or
// space can't change the URL structure.
func fillPathTemplate(template string, val json.RawMessage) (string, error) {
	fields := map[string]string{}
	var s string
	if err := json.Unmarshal(val, &s); err == nil {
		fields["id"] = s
		fields["ref"] = s
	} else {
		var obj map[string]any
		if err := json.Unmarshal(val, &obj); err != nil {
			return "", invalid("external_ref value for this system must be a string or an object")
		}
		for k, v := range obj {
			switch v.(type) {
			case string, float64, bool:
				fields[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	var missing string
	out := placeholderRe.ReplaceAllStringFunc(template, func(tok string) string {
		name := tok[1 : len(tok)-1]
		if v, ok := fields[name]; ok {
			return url.PathEscape(v)
		}
		if missing == "" {
			missing = name
		}
		return tok
	})
	if missing != "" {
		return "", invalid("path template references {%s}, which this feature's external_ref for the system doesn't provide", missing)
	}
	return out, nil
}

// loadSystemByCode loads a catalogued external system by its code (the key used in a
// feature's external_ref), translating a miss into this package's notFoundError --
// the by-code twin of loadSystemByID.
func loadSystemByCode(ctx context.Context, db bun.IDB, code string) (*externalsystem.ExternalSystem, error) {
	var sys externalsystem.ExternalSystem
	if err := db.NewSelect().Model(&sys).Where("code = ?", code).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("external system %q is not configured", code)
		}
		return nil, fmt.Errorf("externallink: feature-fetch: load system: %w", err)
	}
	return &sys, nil
}
