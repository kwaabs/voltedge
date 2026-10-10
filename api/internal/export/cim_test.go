package export

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// oakwoodFixture is the Oakwood Zone example from docs/cim-export-guide.md: an
// 11 kV load-break switch inside the Oakwood Primary substation feeding a span of
// 11 kV overhead line into TX-Feeder7-014 (315 kVA, 11 kV/415 V), whose LV side
// runs a 95 mm² ABC service cable to one service point (with a meter). It is built
// from literals -- no database -- so the encoder can be exercised and the guide's
// sample file regenerated (CIM_SAMPLE_OUT=/path go test -run TestCIMOakwoodSample).
func oakwoodFixture() (layers []Layer, mapping *metadata.CIMMapping, conns []connRow, specs map[string]*metadata.MaterialSpec) {
	id := func(n string) string { return uuidV5("oakwood-sample:" + n) }
	pt := func(x, y float64) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"type": "Point", "coordinates": []float64{x, y}})
		return b
	}
	ln := func(c ...[2]float64) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"type": "LineString", "coordinates": c})
		return b
	}
	poly := func(c ...[2]float64) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"type": "Polygon", "coordinates": [][][2]float64{c}})
		return b
	}
	sub1 := 1
	term := func(cls, code, role string) metadata.Terminal {
		return metadata.Terminal{ID: "term:" + cls + ":" + code, FeatureClassID: cls, Code: code, Role: role}
	}

	layers = []Layer{
		{Code: "SUBSTATION", Name: "Substation", GeometryType: "polygon", ClassID: "cls-sub", NetworkRole: "none", Features: []Feature{{
			ID: id("sub"), Name: "Oakwood Primary",
			Geometry: poly([2]float64{-0.2010, 5.5995}, [2]float64{-0.1980, 5.5995}, [2]float64{-0.1980, 5.6020}, [2]float64{-0.2010, 5.6020}, [2]float64{-0.2010, 5.5995}),
		}}},
		{Code: "DIST_SWITCH", Name: "Distribution switch", GeometryType: "point", ClassID: "cls-sw", NetworkRole: "junction",
			Terminals: []metadata.Terminal{term("cls-sw", "T1", "in"), term("cls-sw", "T2", "out")},
			Subtypes:  []metadata.Subtype{{ID: "st-lbs", FeatureClassID: "cls-sw", Code: 1, Name: "Load-break"}},
			Features: []Feature{{ID: id("sw"), Name: "SW-F7-03", SubtypeCode: &sub1, Geometry: pt(-0.1990, 5.6006),
				Attributes: map[string]any{"device_state": "CLOSED", "manufacturer": "Acme", "rated_amps": 630.0}}}},
		{Code: "MV_CONDUCTOR", Name: "MV conductor", GeometryType: "line", ClassID: "cls-mv", NetworkRole: "edge", Features: []Feature{{
			ID: id("mv"), Name: "C-F7-01", Geometry: ln([2]float64{-0.1990, 5.6006}, [2]float64{-0.1978, 5.6009}, [2]float64{-0.1965, 5.6012}),
			Attributes: map[string]any{"voltage_class": "11KV", "phase": "L1L2L3N"}}}},
		{Code: "DIST_XFMR", Name: "Distribution transformer", GeometryType: "point", ClassID: "cls-tx", NetworkRole: "junction",
			Terminals: []metadata.Terminal{term("cls-tx", "T1", "in"), term("cls-tx", "T2", "out"), term("cls-tx", "T3", "ground")},
			Features: []Feature{{ID: id("tx"), Name: "TX-Feeder7-014", Geometry: pt(-0.1965, 5.6012),
				Attributes: map[string]any{"primary_voltage": "11KV", "secondary_voltage": 415.0, "rated_kva": 315.0, "serial_no": "T-77-0412"}}}},
		{Code: "LV_CONDUCTOR", Name: "LV conductor", GeometryType: "line", ClassID: "cls-lv", NetworkRole: "edge", Features: []Feature{{
			ID: id("lv"), Name: "LV-014-01", Geometry: ln([2]float64{-0.1965, 5.6012}, [2]float64{-0.1961, 5.6016}),
			Attributes: map[string]any{"conductor_spec": "ABC_95", "phase": "L1L2L3N"}}}},
		{Code: "SERVICE_POINT", Name: "Service point", GeometryType: "point", ClassID: "cls-sp", NetworkRole: "junction",
			Terminals: []metadata.Terminal{term("cls-sp", "T1", "in")},
			Features:  []Feature{{ID: id("sp"), Name: "SP-0457", Geometry: pt(-0.1961, 5.6016), Attributes: map[string]any{"customer_class": "residential"}}}},
		{Code: "METER", Name: "Meter", GeometryType: "point", ClassID: "cls-mt", NetworkRole: "none", Features: []Feature{{
			ID: id("mt"), Name: "M-0457", Geometry: pt(-0.1961, 5.6016)}}},
	}

	cm := func(cls, cim, kind string, sub *int) metadata.CimClassMapping {
		return metadata.CimClassMapping{FeatureClassCode: cls, CimClass: cim, CimKind: kind, SubtypeCode: sub}
	}
	one := 2
	classes := []metadata.CimClassMapping{
		cm("SUBSTATION", "Substation", "container", nil),
		cm("DIST_SWITCH", "LoadBreakSwitch", "conducting_junction", nil),
		cm("DIST_SWITCH", "Recloser", "conducting_junction", &one),
		cm("MV_CONDUCTOR", "ACLineSegment", "conducting_edge", nil),
		cm("DIST_XFMR", "PowerTransformer", "conducting_junction", nil),
		cm("LV_CONDUCTOR", "ACLineSegment", "conducting_edge", nil),
		cm("SERVICE_POINT", "EnergyConsumer", "conducting_junction", nil),
		cm("METER", "UsagePoint", "asset", nil),
	}
	v11 := map[string]any{"11KV": 11000.0, "33KV": 33000.0}
	scale1000 := 1000.0
	am := func(cls, field, attr, transform string) metadata.CimAttributeMapping {
		return metadata.CimAttributeMapping{FeatureClassCode: cls, FieldName: field, CimAttribute: attr, Transform: transform}
	}
	attrs := []metadata.CimAttributeMapping{
		{FeatureClassCode: "MV_CONDUCTOR", FieldName: "voltage_class", CimAttribute: "BaseVoltage.nominalVoltage", Transform: "domain_map", ValueMap: v11},
		am("MV_CONDUCTOR", "voltage_class", "Conductor.length", "from_geometry"),
		am("MV_CONDUCTOR", "phase", "ACLineSegmentPhase.phase", "phase"),
		{FeatureClassCode: "DIST_XFMR", FieldName: "primary_voltage", CimAttribute: "BaseVoltage.nominalVoltage", Transform: "domain_map", ValueMap: v11},
		am("DIST_XFMR", "secondary_voltage", "BaseVoltage.nominalVoltage.secondary", "direct"),
		{FeatureClassCode: "DIST_XFMR", FieldName: "rated_kva", CimAttribute: "PowerTransformerEnd.ratedS", Transform: "scale", ScaleFactor: &scale1000},
		{FeatureClassCode: "DIST_SWITCH", FieldName: "device_state", CimAttribute: "Switch.normalOpen", Transform: "boolean", ValueMap: map[string]any{"OPEN": true, "CLOSED": false}},
		am("LV_CONDUCTOR", "conductor_spec", "Conductor.length", "from_geometry"),
		am("LV_CONDUCTOR", "conductor_spec", "ACLineSegment.r", "material_spec"),
		am("LV_CONDUCTOR", "conductor_spec", "ACLineSegment.x", "material_spec"),
		am("LV_CONDUCTOR", "phase", "ACLineSegmentPhase.phase", "phase"),
	}
	profile := &metadata.CimProfile{Code: "CDPSM_CIM16", Name: "CDPSM (IEC 61968-13), CIM16",
		CimNamespace: "http://iec.ch/TC57/2013/CIM-schema-cim16#", ProfileURI: "http://iec.ch/TC57/61968-13/CDPSM/2013#",
		ModelAuthority: "https://voltedge.local/ModelAuthoritySet", CoordinateCRS: "urn:ogc:def:crs:EPSG::4326",
		UnmappedAttrPolicy: "drop", IsDefault: true}
	mapping = metadata.NewCIMMapping(profile, classes, attrs)

	cr := func(fc, ff string, ft *string, tc, tf string, tt *string) connRow {
		return connRow{FromFeatureClassID: fc, FromFeatureID: ff, FromTerminalID: ft, ToFeatureClassID: tc, ToFeatureID: tf, ToTerminalID: tt}
	}
	s := func(v string) *string { return &v }
	conns = []connRow{
		cr("cls-sw", id("sw"), s("term:cls-sw:T2"), "cls-mv", id("mv"), nil),
		cr("cls-mv", id("mv"), nil, "cls-tx", id("tx"), s("term:cls-tx:T1")),
		cr("cls-tx", id("tx"), s("term:cls-tx:T2"), "cls-lv", id("lv"), nil),
		cr("cls-lv", id("lv"), nil, "cls-sp", id("sp"), s("term:cls-sp:T1")),
	}
	r, x := 0.32, 0.08
	specs = map[string]*metadata.MaterialSpec{"ABC_95": {Code: "ABC_95", Name: "ABC 95 mm²", ResistanceOhmPerKm: &r, ReactanceOhmPerKm: &x}}
	return
}

func TestCIMOakwoodSample(t *testing.T) {
	layers, mapping, conns, specs := oakwoodFixture()
	var buf bytes.Buffer
	stats, err := writeCIM(&buf, "Oakwood Zone", layers, mapping, conns, nil, specs, nil, time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("writeCIM: %v", err)
	}
	out := buf.String()
	if p := os.Getenv("CIM_SAMPLE_OUT"); p != "" {
		if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Well-formed XML.
	dec := xml.NewDecoder(strings.NewReader(out))
	for {
		if _, err := dec.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("output is not well-formed XML: %v", err)
		}
	}

	for _, want := range []string{
		`<cim:ACLineSegment rdf:ID="_`, `<cim:PowerTransformer rdf:ID="_`, `<cim:LoadBreakSwitch rdf:ID="_`,
		`<cim:Substation rdf:ID="_`, `<cim:EnergyConsumer rdf:ID="_`, `<cim:UsagePoint rdf:ID="_`,
		`<cim:BaseVoltage.nominalVoltage>11000</cim:BaseVoltage.nominalVoltage>`,
		`<cim:BaseVoltage.nominalVoltage>415</cim:BaseVoltage.nominalVoltage>`,
		`<cim:PowerTransformerEnd.ratedS>315000</cim:PowerTransformerEnd.ratedS>`,
		`<cim:Switch.normalOpen>false</cim:Switch.normalOpen>`,
		`<cim:ACLineSegment.r>`, `<cim:ACLineSegment.x>`, `<cim:Conductor.length>`,
		`rdf:resource="http://iec.ch/TC57/2013/CIM-schema-cim16#SinglePhaseKind.A"`,
		`<md:Model.profile>http://iec.ch/TC57/61968-13/CDPSM/2013#</md:Model.profile>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q", want)
		}
	}
	// Topology: 9 terminals (switch 2, MV span 2, transformer 2 -- its ground
	// terminal is excluded -- LV span 2, service point 1) meeting at 5 nodes: four
	// joins plus the switch's unconnected source side. The guide quotes these counts.
	if stats.Nodes != 5 || stats.Endpoints != 9 {
		t.Errorf("topology = %d nodes / %d endpoints, want 5 / 9", stats.Nodes, stats.Endpoints)
	}
	if n := strings.Count(out, "<cim:PowerTransformerEnd rdf:ID="); n != 2 {
		t.Errorf("PowerTransformerEnd count = %d, want 2 (ground terminal excluded)", n)
	}
	// ratedS is a winding property: on each PowerTransformerEnd, never as a literal
	// on the PowerTransformer element itself.
	if n := strings.Count(out, "<cim:PowerTransformerEnd.ratedS>"); n != 2 {
		t.Errorf("PowerTransformerEnd.ratedS count = %d, want 2 (one per winding, none on the transformer)", n)
	}
	if i := strings.Index(out, "<cim:PowerTransformer rdf:ID"); i < 0 || strings.Contains(out[i:i+strings.Index(out[i:], "</cim:PowerTransformer>")], "ratedS") {
		t.Errorf("PowerTransformerEnd.ratedS leaked onto the PowerTransformer element")
	}
	// Unmapped feature fields are dropped under the default policy.
	if strings.Contains(out, "ve:") || strings.Contains(out, "serial_no") {
		t.Errorf("drop policy must not carry unmapped fields")
	}
	// Deterministic: a second run is byte-identical.
	var buf2 bytes.Buffer
	if _, err := writeCIM(&buf2, "Oakwood Zone", layers, mapping, conns, nil, specs, nil, time.Date(2026, 10, 10, 6, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if buf.String() != buf2.String() {
		t.Errorf("CIM output is not deterministic across runs")
	}
}
