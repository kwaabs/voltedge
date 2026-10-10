"""Self-test for cim_validate.py and cim_adapt.py against the real Oakwood sample.

    python3 -m unittest scripts/cim/test_cim_tools.py        (from the repo root)
    python3 test_cim_tools.py                                 (from this directory)
"""
import os
import re
import shutil
import sys
import tempfile
import unittest
import xml.etree.ElementTree as ET

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import cim_adapt  # noqa: E402
import cim_validate  # noqa: E402

SAMPLE = os.path.join(HERE, "samples", "oakwood.xml")


def mutate(fn):
    with open(SAMPLE, encoding="utf-8") as f:
        s = f.read()
    p = os.path.join(tempfile.mkdtemp(), "m.xml")
    with open(p, "w", encoding="utf-8") as f:
        f.write(fn(s))
    return p


def read(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


def codes(path):
    r = cim_validate.validate(path)
    return {e["code"] for e in r.errors}, {w["code"] for w in r.warnings}


class Validate(unittest.TestCase):
    def test_sample_is_clean(self):
        errs, warns = codes(SAMPLE)
        self.assertEqual(errs, set())
        self.assertEqual(warns, {"W-OPEN"})  # the switch's unconnected source side

    def test_dangling_reference(self):
        p = mutate(lambda s: s.replace('rdf:resource="#_9341c867-aae5-52b9-859e-147a13a6b332"', 'rdf:resource="#_deadbeef"', 1))
        self.assertIn("E-REF", codes(p)[0])

    def test_two_terminal_same_node(self):  # CGMES constraint 452
        a, b = "_dd9b94ac-9a46-589f-8455-d98cba48c075", "_45a2ecbf-ff58-572e-9983-1002455369e2"
        p = mutate(lambda x: x.replace(f'<cim:Terminal.ConnectivityNode rdf:resource="#{b}"/>\n    <cim:Terminal.sequenceNumber>2</cim:Terminal.sequenceNumber>',
                                       f'<cim:Terminal.ConnectivityNode rdf:resource="#{a}"/>\n    <cim:Terminal.sequenceNumber>2</cim:Terminal.sequenceNumber>', 1))
        self.assertIn("E-452", codes(p)[0])

    def test_transformer_needs_two_windings(self):
        def f(s):
            i = s.index('<cim:PowerTransformerEnd rdf:ID="_2c7b3b77')
            j = s.index("</cim:PowerTransformerEnd>", i) + len("</cim:PowerTransformerEnd>\n")
            return s[:i] + s[j:]
        self.assertIn("E-WIND", codes(mutate(f))[0])

    def test_voltage_mismatch_on_a_node(self):
        v11, v415 = "_6890e289-f3f8-5807-9e5b-f077ac483ef1", "_a1a477c4-6c96-5796-9312-afc7ef5b20f4"
        p = mutate(lambda s: s.replace(f'<cim:ConductingEquipment.BaseVoltage rdf:resource="#{v11}"/>', f'<cim:ConductingEquipment.BaseVoltage rdf:resource="#{v415}"/>', 1))
        self.assertIn("W-VOLT", codes(p)[1])

    def test_kv_units_are_flagged(self):
        d = tempfile.mkdtemp()
        out = os.path.join(d, "kv.xml")
        cim_adapt.main([SAMPLE, out, "--units", "cgmes"])
        self.assertIn("W-UNIT", codes(out)[1])

    def test_empty_node(self):
        p = mutate(lambda s: s.replace("</rdf:RDF>", '<cim:ConnectivityNode rdf:ID="_orphan"/>\n</rdf:RDF>'))
        self.assertIn("E-NODE", codes(p)[0])

    def test_not_well_formed(self):
        p = mutate(lambda s: s.replace("</rdf:RDF>", ""))
        with self.assertRaises(ET.ParseError):
            cim_validate.validate(p)


class Summary(unittest.TestCase):
    def test_summary_totals(self):
        s = cim_validate.summary(SAMPLE)
        self.assertIn("ACLineSegment           2", s)
        self.assertIn("11000 V  0.285", s)
        self.assertIn("(no BaseVoltage)  0.063", s)  # the LV span carries no BaseVoltage in the shipped mapping
        self.assertIn("315.0", s)


class Loads(unittest.TestCase):
    def test_adds_p_q_by_name(self):
        import cim_add_loads
        d = tempfile.mkdtemp()
        csvp, out = os.path.join(d, "l.csv"), os.path.join(d, "o.xml")
        with open(csvp, "w") as f:
            f.write("name,p_kw,q_kvar\nSP-0457,4.2,1.1\nNOPE,1,1\n")
        cim_add_loads.main([SAMPLE, csvp, out])
        s = read(out)
        self.assertIn(">4200</cim:EnergyConsumer.p>", s)
        self.assertIn(">1100</cim:EnergyConsumer.q>", s)
        cim_add_loads.main([SAMPLE, csvp, out, "--units", "cgmes"])
        self.assertIn(">0.0042</cim:EnergyConsumer.p>", read(out))


class Adapt(unittest.TestCase):
    def setUp(self):
        self.d = tempfile.mkdtemp()

    def tearDown(self):
        shutil.rmtree(self.d, ignore_errors=True)

    def run_adapt(self, *args):
        out = os.path.join(self.d, "out.xml")
        cim_adapt.main([SAMPLE, out, *args])
        return read(out)

    def test_units_cgmes(self):
        s = self.run_adapt("--units", "cgmes")
        self.assertIn(">11</cim:BaseVoltage.nominalVoltage>", s)
        self.assertIn(">0.415</cim:BaseVoltage.nominalVoltage>", s)
        self.assertIn(">0.315</cim:PowerTransformerEnd.ratedS>", s)
        self.assertIn(">0.284603</cim:Conductor.length>", s)

    def test_urn_ids_keep_enum_resources(self):
        s = self.run_adapt("--id-style", "urn")
        self.assertNotIn('rdf:ID="_', s)
        self.assertTrue(re.search(r'about="urn:uuid:[0-9a-f-]{36}"', s))
        self.assertIn("#SinglePhaseKind.A", s)  # enumeration literal untouched
        # no object lost: same count of identified objects
        self.assertEqual(len(re.findall(r"about=", s)), len(re.findall(r"rdf:ID=", read(SAMPLE))) + 1)  # + FullModel

    def test_strip_mrid(self):
        self.assertNotIn("IdentifiedObject.mRID", self.run_adapt("--strip-mrid"))

    def test_split_needs_profiles(self):
        with self.assertRaises(SystemExit):
            cim_adapt.main([SAMPLE, "x", "--split", "a.xml", "b.xml"])

    def test_split(self):
        eq, gl = os.path.join(self.d, "eq.xml"), os.path.join(self.d, "gl.xml")
        cim_adapt.main([SAMPLE, "x", "--split", eq, gl, "--eq-profile", "urn:eq", "--gl-profile", "urn:gl"])
        e, g = read(eq), read(gl)
        self.assertIn("urn:eq", e)
        self.assertIn("urn:gl", g)
        self.assertIn("PositionPoint", g)
        self.assertNotIn("PositionPoint", e)
        self.assertIn("ACLineSegment", e)
        self.assertNotIn("ACLineSegment", g)
        # alone, the EQ file points at GL objects it does not contain; together they are clean
        self.assertIn("E-REF", {x["code"] for x in cim_validate.validate(eq).errors})
        self.assertEqual({x["code"] for x in cim_validate.validate([eq, gl]).errors}, set())


if __name__ == "__main__":
    unittest.main()
