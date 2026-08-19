package powerflow

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Constant air properties this calc holds fixed rather than deriving
// from the actual film temperature (Tc+Ta)/2 the way full IEEE 738
// does via its own polynomial correlations -- the deliberate
// simplification this endpoint makes, same tradeoff load flow's own
// linearized voltage-drop approximation already documents: real
// physics, a simplified input. Values are typical air properties near
// a moderate film temperature (~40 degC, sea level), the range most
// planning-level ampacity ratings fall in.
const (
	airDensityKgM3       = 1.029  // rho_f, kg/m^3
	airViscosityPaS      = 1.9e-5 // mu_f, Pa*s
	airThermalCondWMK    = 0.0280 // k_f, W/(m*K)
	stefanBoltzmannWM2K4 = 5.670374419e-8
	resistanceRefTempC   = 20.0 // reference temp resistance_ohm_per_km is assumed measured at -- undocumented in the catalog itself, this project's own stated convention
)

// ampacityRequest is POST /analysis/ampacity's body. AmbientTempC is a
// *float64, not a plain float64 like every other request in this
// package -- 0 degC is a real, common ambient temperature (freezing
// point), so unlike nominal_voltage_v's own "<=0 means missing" gate,
// there's no sentinel invalid value to lean on here; explicit presence
// is the only honest way to require it, same reasoning nominal_voltage_v
// itself is required for. WindSpeedMps/SolarHeatFluxWM2 default to
// their JSON zero value when omitted -- see ampacity.go's own doc
// comment on why that's conservative for wind speed (omitted wind
// leaves only natural convection, the weaker cooling path, so ampacity
// comes out lower/safer) but NOT for solar flux (omitted solar
// silently assumes a clear night, which OVERSTATES ampacity for a
// daytime rating -- a caller doing a summer-noon rating must supply
// their own solar figure).
type ampacityRequest struct {
	Conductor        featureRef `json:"conductor"`
	AmbientTempC     *float64   `json:"ambient_temp_c"`
	WindSpeedMps     float64    `json:"wind_speed_mps"`
	WindAngleDeg     float64    `json:"wind_angle_deg"`
	SolarHeatFluxWM2 float64    `json:"solar_heat_flux_w_m2"`
}

// ampacityResponse reports the heat-balance terms alongside the final
// current, not just the answer -- a caller (or this project's own
// future work folding this in as the load-flow engine's dynamic
// ampacity source) can see which loss/gain term is actually driving
// the rating, the same transparency api/internal/powerflow's other two
// endpoints already give via CumulativeKW/KVAR and ROhmTotal/XOhmTotal
// rather than a bare final number. StaticAmpacityAmps echoes the
// material catalog's own static figure (if set) purely for comparison
// -- this endpoint never reads or writes it.
type ampacityResponse struct {
	Conductor          featureRef `json:"conductor"`
	MaterialSpecCode   string     `json:"material_spec_code"`
	MaxOperatingTempC  float64    `json:"max_operating_temp_c"`
	AmbientTempC       float64    `json:"ambient_temp_c"`
	ConvectiveLossWM   float64    `json:"convective_loss_w_per_m"`
	RadiativeLossWM    float64    `json:"radiative_loss_w_per_m"`
	SolarGainWM        float64    `json:"solar_gain_w_per_m"`
	ResistanceOhmPerM  float64    `json:"resistance_ohm_per_m"`
	AmpacityAmps       float64    `json:"ampacity_amps"`
	StaticAmpacityAmps *float64   `json:"static_ampacity_amps,omitempty"`
}

// runAmpacity computes a conductor feature's own first-principles
// steady-state ampacity via IEEE 738's heat-balance equation
// (qc + qr = qs + I^2*R(Tc)), evaluated directly at Tc =
// max_operating_temp_c rather than solved by iteration: with this
// calc's own constant-air-properties simplification, qc and qr are
// direct functions of (Tc, Ta) that don't depend on current at all, so
// there's no root-find needed -- I = sqrt((qc+qr-qs) / R(Tc)) is
// already closed-form. See docs/phase-8-ampacity.md.
func runAmpacity(ctx context.Context, db bun.IDB, req ampacityRequest) (*ampacityResponse, error) {
	if req.Conductor.FeatureClass == "" || req.Conductor.FeatureID == "" {
		return nil, invalid("conductor.feature_class and conductor.feature_id are required")
	}
	if req.AmbientTempC == nil {
		return nil, invalid("ambient_temp_c is required")
	}
	if req.WindSpeedMps < 0 {
		return nil, invalid("wind_speed_mps must not be negative")
	}
	if req.SolarHeatFluxWM2 < 0 {
		return nil, invalid("solar_heat_flux_w_m2 must not be negative")
	}

	loaded, err := metadata.Load(ctx, db, req.Conductor.FeatureClass)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("feature class %q does not exist", req.Conductor.FeatureClass)
		}
		return nil, err
	}
	if loaded.FeatureClass.NetworkRole != "edge" {
		return nil, invalid("%q is not a conductor class (network_role != edge) -- ampacity only applies to a branch that carries current along its own length", req.Conductor.FeatureClass)
	}

	row, err := crud.GetFeature(ctx, db, loaded, req.Conductor.FeatureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return nil, notFound("feature %s (%s) not found", req.Conductor.FeatureID, req.Conductor.FeatureClass)
		}
		return nil, err
	}
	attrs, _ := row["attributes"].(map[string]any)

	spec, err := resolveAmpacityMaterialSpec(ctx, db, loaded, attrs)
	if err != nil {
		return nil, err
	}
	if spec == nil {
		return nil, notFound("no material spec resolved for feature %s (%s) -- ampacity needs a material-linked field set on this feature", req.Conductor.FeatureID, req.Conductor.FeatureClass)
	}
	missing := missingAmpacityFields(spec)
	if len(missing) > 0 {
		return nil, invalid("material spec %q is missing required ampacity input(s): %v", spec.Code, missing)
	}

	Tc := *spec.MaxOperatingTempC
	Ta := *req.AmbientTempC
	deltaT := Tc - Ta
	dMeters := *spec.DiameterMm / 1000

	rOhmPerM := (*spec.ResistanceOhmPerKm / 1000) * (1 + *spec.ResistanceTempCoefficientPerC*(Tc-resistanceRefTempC))

	var qc, qr, qs float64
	if deltaT > 0 {
		qr = math.Pi * dMeters * *spec.Emissivity * stefanBoltzmannWM2K4 *
			(math.Pow(Tc+273.15, 4) - math.Pow(Ta+273.15, 4))
		qc = convectiveLossWM(dMeters, req.WindSpeedMps, req.WindAngleDeg, deltaT)
	}
	qs = *spec.Absorptivity * req.SolarHeatFluxWM2 * dMeters

	netW := qc + qr - qs
	var ampacityAmps float64
	if netW > 0 && rOhmPerM > 0 {
		ampacityAmps = math.Sqrt(netW / rOhmPerM)
	}

	return &ampacityResponse{
		Conductor:          req.Conductor,
		MaterialSpecCode:   spec.Code,
		MaxOperatingTempC:  Tc,
		AmbientTempC:       Ta,
		ConvectiveLossWM:   qc,
		RadiativeLossWM:    qr,
		SolarGainWM:        qs,
		ResistanceOhmPerM:  rOhmPerM,
		AmpacityAmps:       ampacityAmps,
		StaticAmpacityAmps: spec.AmpacityAmps,
	}, nil
}

// convectiveLossWM is IEEE 738's own SI forced/natural convection
// formulas, using this file's own constant air properties in place of
// values derived from the actual film temperature. IEEE 738 takes the
// greater of its forced-convection figure (two forms, itself taking
// the greater of the two -- qc1 dominates at low wind speed, qc2 at
// high) and its natural-convection figure, so a near-zero wind speed
// naturally falls through to the natural-convection floor rather than
// needing an explicit "no wind" branch.
func convectiveLossWM(dMeters, windSpeedMps, windAngleDeg, deltaT float64) float64 {
	reynolds := dMeters * windSpeedMps * airDensityKgM3 / airViscosityPaS

	phi := windAngleDeg * math.Pi / 180
	kAngle := 1.194 - math.Cos(phi) + 0.194*math.Cos(2*phi) + 0.368*math.Sin(2*phi)

	qc1 := kAngle * (1.01 + 1.35*math.Pow(reynolds, 0.52)) * airThermalCondWMK * deltaT
	qc2 := kAngle * 0.754 * math.Pow(reynolds, 0.6) * airThermalCondWMK * deltaT
	qcForced := math.Max(qc1, qc2)

	qcNatural := 0.0205 * math.Sqrt(airDensityKgM3) * math.Pow(dMeters, 0.75) * math.Pow(deltaT, 1.25)

	return math.Max(qcForced, qcNatural)
}

// ampacityRequiredFields names every metadata.MaterialSpec column this
// calc reads, paired with a resolver so missingAmpacityFields can name
// exactly which one(s) are unset on an incompletely-cataloged spec
// rather than a caller having to guess.
var ampacityRequiredFields = []struct {
	name string
	set  func(*metadata.MaterialSpec) bool
}{
	{"max_operating_temp_c", func(s *metadata.MaterialSpec) bool { return s.MaxOperatingTempC != nil }},
	{"emissivity", func(s *metadata.MaterialSpec) bool { return s.Emissivity != nil }},
	{"absorptivity", func(s *metadata.MaterialSpec) bool { return s.Absorptivity != nil }},
	{"resistance_temp_coefficient_per_c", func(s *metadata.MaterialSpec) bool { return s.ResistanceTempCoefficientPerC != nil }},
	{"resistance_ohm_per_km", func(s *metadata.MaterialSpec) bool { return s.ResistanceOhmPerKm != nil }},
	{"diameter_mm", func(s *metadata.MaterialSpec) bool { return s.DiameterMm != nil }},
}

func missingAmpacityFields(spec *metadata.MaterialSpec) []string {
	var missing []string
	for _, f := range ampacityRequiredFields {
		if !f.set(spec) {
			missing = append(missing, f.name)
		}
	}
	return missing
}

// resolveAmpacityMaterialSpec scans a feature class's own fields for
// whichever currently holds a material-linked domain value on this
// specific feature -- the same by-field-name scanning
// resolveConductorImpedance already does, kept as this function's own
// independent copy rather than a shared extraction: resolveConductorImpedance's
// own loop stops at the first field whose resolved spec has resistance
// or reactance set, skipping past one that doesn't; this endpoint's
// own required-field set is different (thermal/geometric, not R/X), so
// sharing the loop would mean threading a filter predicate through
// already-shipped, hand-verified code for a two-caller's worth of
// reuse -- not worth the risk. Returns nil, not an error, when nothing
// on this feature resolves.
func resolveAmpacityMaterialSpec(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, attrs map[string]any) (*metadata.MaterialSpec, error) {
	for _, f := range loaded.Fields {
		if f.DomainID == nil {
			continue
		}
		code, ok := attrs[f.Name].(string)
		if !ok || code == "" {
			continue
		}
		spec, err := resolveMaterialSpecByCode(ctx, db, *f.DomainID, code)
		if err != nil {
			return nil, err
		}
		if spec != nil {
			return spec, nil
		}
	}
	return nil, nil
}
