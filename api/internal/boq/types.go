package boq

// Wire shapes for the Bill of Quantities engine (Slice 1). A BoQ prices a scope
// of the network: it multiplies scoped quantities (discrete feature counts and
// linear geometry lengths) by catalog rates and rolls the result up into a
// grouped, totalled schedule, presented in a chosen currency.

// Request is the POST /boq body. A caller either prices an explicit set of
// features (FeatureRefs -- a trace result or map selection) or scans whole
// classes (FeatureClasses, optionally narrowed by Scope). FeatureRefs, when
// present, takes precedence and defines the classes. DisplayCurrency is a
// currencies.code; empty means the base currency.
type Request struct {
	FeatureClasses  []string     `json:"feature_classes"`
	Scope           *Scope       `json:"scope"`
	FeatureRefs     []FeatureRef `json:"feature_refs"`
	DisplayCurrency string       `json:"display_currency"`
}

// Scope is the optional spatial narrowing for a class scan -- the same
// territory/radius/bbox shapes the aggregate/query scopes use. Kind is
// "none"/"territory"/"radius"/"bbox"; an empty or "none" Kind scans every class
// in full (no spatial limit).
type Scope struct {
	Kind        string    `json:"kind"`
	TerritoryID string    `json:"territory_id"`
	Center      []float64 `json:"center"`   // [lon, lat] for radius
	RadiusM     float64   `json:"radius_m"` // radius in metres
	BBox        []float64 `json:"bbox"`     // [min_lon, min_lat, max_lon, max_lat]
}

// FeatureRef names one feature to price by its class code and id -- how a trace
// result or a map multi-select hands its exact features to the BoQ.
type FeatureRef struct {
	Class string `json:"class"`
	ID    string `json:"id"`
}

// Line is one priced row of the schedule: a (class, subtype, material) bucket
// with its quantity and the rate applied. UnitRate and LineTotal are in the
// response's Currency (already converted from the base currency).
type Line struct {
	FeatureClass     string  `json:"feature_class"`
	FeatureClassName string  `json:"feature_class_name"`
	Subtype          *int    `json:"subtype,omitempty"`
	SubtypeName      string  `json:"subtype_name,omitempty"`
	MaterialCode     *string `json:"material_code,omitempty"`
	MaterialName     string  `json:"material_name,omitempty"`
	UnitBasis        string  `json:"unit_basis"` // "each" | "per_km"
	Quantity         float64 `json:"quantity"`
	Unit             string  `json:"unit"` // "each" | "km"
	UnitRate         float64 `json:"unit_rate"`
	LineTotal        float64 `json:"line_total"`
	// Overridden marks a line priced off a per-feature cost override
	// (metadata.feature_cost_overrides) rather than the class/material catalog --
	// surfaced so the schedule can flag the hand-set prices.
	Overridden bool `json:"overridden,omitempty"`
}

// UnpricedLine is a bucket whose quantity was counted but no rate resolved --
// surfaced explicitly (never folded into the total as zero) so a costing gap is
// visible and actionable.
type UnpricedLine struct {
	FeatureClass     string  `json:"feature_class"`
	FeatureClassName string  `json:"feature_class_name"`
	Subtype          *int    `json:"subtype,omitempty"`
	SubtypeName      string  `json:"subtype_name,omitempty"`
	MaterialCode     *string `json:"material_code,omitempty"`
	UnitBasis        string  `json:"unit_basis"`
	Quantity         float64 `json:"quantity"`
	Unit             string  `json:"unit"`
	Reason           string  `json:"reason"`
}

// --- Asset register (the per-feature sibling of the priced schedule) ---

// RegisterRequest is the POST /boq/asset-register body. It carries the same scope
// shapes as a priced BoQ (a feature_refs set, or whole classes optionally
// narrowed by Scope), plus IncludeValuation -- when true every row also carries
// its unit cost and value, resolved through the same catalog/override/material
// path the priced schedule uses. DisplayCurrency only matters when valuation is on.
type RegisterRequest struct {
	FeatureClasses   []string     `json:"feature_classes"`
	Scope            *Scope       `json:"scope"`
	FeatureRefs      []FeatureRef `json:"feature_refs"`
	IncludeValuation bool         `json:"include_valuation"`
	DisplayCurrency  string       `json:"display_currency"`
}

// RegisterRow is one asset's itemised row: its identity and a flat set of common
// columns present on every class, plus optional valuation. Pointer valuation
// fields are nil (omitted) when IncludeValuation is false.
type RegisterRow struct {
	FeatureClass     string   `json:"feature_class"`
	FeatureClassName string   `json:"feature_class_name"`
	FeatureID        string   `json:"feature_id"`
	Subtype          *int     `json:"subtype,omitempty"`
	SubtypeName      string   `json:"subtype_name,omitempty"`
	Name             string   `json:"name,omitempty"`
	Status           string   `json:"status,omitempty"`
	MaterialCode     *string  `json:"material_code,omitempty"`
	MaterialName     string   `json:"material_name,omitempty"`
	Quantity         float64  `json:"quantity"`
	Unit             string   `json:"unit"` // "each" | "km"
	// Longitude/Latitude is a point's location, a line's START endpoint, or a
	// polygon's representative point; ToLongitude/ToLatitude is a line's END
	// endpoint (omitted for points and polygons).
	Longitude        *float64 `json:"longitude,omitempty"`
	Latitude         *float64 `json:"latitude,omitempty"`
	ToLongitude      *float64 `json:"to_longitude,omitempty"`
	ToLatitude       *float64 `json:"to_latitude,omitempty"`
	TerritoryID      string   `json:"territory_id,omitempty"`
	TerritoryName    string   `json:"territory_name,omitempty"`
	CircuitID        string   `json:"circuit_id,omitempty"`
	Ownership        string   `json:"ownership,omitempty"`
	ExternalRef      string   `json:"external_ref,omitempty"`
	CreatedAt        string   `json:"created_at,omitempty"`
	// Valuation (present only when IncludeValuation): UnitRate and Value are in
	// the response Currency; PricedSource is "catalog" | "override" | "unpriced".
	UnitRate     *float64 `json:"unit_rate,omitempty"`
	Value        *float64 `json:"value,omitempty"`
	PricedSource string   `json:"priced_source,omitempty"`
}

// RegisterResponse is the itemised asset register. TotalValue and the currency
// fields are meaningful only when IncludeValuation was requested. Truncated marks
// a register capped at the row limit (a huge whole-network scope).
type RegisterResponse struct {
	Rows             []RegisterRow `json:"rows"`
	Count            int           `json:"count"`
	Truncated        bool          `json:"truncated"`
	IncludeValuation bool          `json:"include_valuation"`
	Currency         string        `json:"currency,omitempty"`
	CurrencyName     string        `json:"currency_name,omitempty"`
	BaseCurrency     string        `json:"base_currency,omitempty"`
	PerBase          float64       `json:"per_base,omitempty"`
	TotalValue       *float64      `json:"total_value,omitempty"`
	Currencies       []CurrencyOut `json:"currencies"`
}

// CurrencyOut is one row of the currency picker echoed on every response.
type CurrencyOut struct {
	Code    string  `json:"code"`
	Name    string  `json:"name"`
	PerBase float64 `json:"per_base"`
	IsBase  bool    `json:"is_base"`
}

// Response is the priced schedule. Total/subtotals are in Currency; TotalBase is
// the same figure in the base currency (the denomination every stored rate uses).
type Response struct {
	Currency     string         `json:"currency"`
	CurrencyName string         `json:"currency_name"`
	BaseCurrency string         `json:"base_currency"`
	PerBase      float64        `json:"per_base"`
	Lines        []Line         `json:"lines"`
	Unpriced     []UnpricedLine `json:"unpriced"`
	Total        float64        `json:"total"`
	TotalBase    float64        `json:"total_base"`
	PricedCount  int            `json:"priced_count"`
	Currencies   []CurrencyOut  `json:"currencies"`
}
