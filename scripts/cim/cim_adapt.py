#!/usr/bin/env python3
"""Adapt a VoltEdge CIM/XML export for a stricter or differently-conventioned consumer.

Standard library only. VoltEdge writes SI base units (V, VA, m, ohm), `rdf:ID="_uuid"`
identifiers and an IdentifiedObject.mRID literal on every object. Pick the transforms a
recipient needs:

    python3 cim_adapt.py in.xml out.xml --units cgmes            # V->kV, VA->MVA, m->km
    python3 cim_adapt.py in.xml out.xml --id-style urn           # rdf:about/resource "urn:uuid:..."
    python3 cim_adapt.py in.xml out.xml --strip-mrid --strip-ve  # drop mRID literals and ve:* fields
    python3 cim_adapt.py in.xml out.xml --units cgmes --id-style urn --strip-mrid
    python3 cim_adapt.py in.xml out_base --split EQ.xml GL.xml --eq-profile URI --gl-profile URI

--units cgmes divides BaseVoltage.nominalVoltage and PowerTransformerEnd.ratedU by 1000
(kV), PowerTransformerEnd.ratedS by 1,000,000 (MVA) and Conductor.length by 1000 (km);
ohms are unchanged. Check the target tool's profile for the exact units it expects.

The adapter never invents data: it only re-spells, rescales, removes and splits.
See docs/cim-export-guide.md, Chapter 8.
"""
import argparse
import re
import sys
import xml.etree.ElementTree as ET

RDF = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
MD = "http://iec.ch/TC57/61970-552/ModelDescription/1#"
ID, ABOUT, RESOURCE = f"{{{RDF}}}ID", f"{{{RDF}}}about", f"{{{RDF}}}resource"

KV = {"BaseVoltage.nominalVoltage", "PowerTransformerEnd.ratedU"}
MVA = {"PowerTransformerEnd.ratedS"}
KM = {"Conductor.length"}
GL_CLASSES = {"Location", "PositionPoint", "CoordinateSystem"}


def local(tag):
    return tag.rsplit("}", 1)[-1]


def fmt(x):
    s = f"{x:.9f}".rstrip("0").rstrip(".")
    return s if s else "0"


def convert_units(root):
    for el in root.iter():
        name = local(el.tag)
        if el.text is None:
            continue
        try:
            v = float(el.text)
        except ValueError:
            continue
        if name in KV:
            el.text = fmt(v / 1000.0)
        elif name in MVA:
            el.text = fmt(v / 1_000_000.0)
        elif name in KM:
            el.text = fmt(v / 1000.0)


def urn_ids(root, cim_ns):
    for el in root.iter():
        if ID in el.attrib:
            v = el.attrib.pop(ID)
            el.set(ABOUT, "urn:uuid:" + v.lstrip("_"))
        r = el.attrib.get(RESOURCE)
        if r is not None and r.startswith("#_"):
            el.set(RESOURCE, "urn:uuid:" + r[2:])


def strip(root, mrid, ve):
    for parent in list(root.iter()):
        for c in list(parent):
            n = c.tag
            if (mrid and local(n) == "IdentifiedObject.mRID") or (ve and n.startswith("{https://voltedge.local/CIM-extension/")):
                parent.remove(c)


def split(root, eq_profile, gl_profile):
    """Return (eq_root, gl_root): GL classes in one document, everything else in the other."""
    def clone(profile):
        new = ET.Element(root.tag, root.attrib)
        for c in root:
            if local(c.tag) == "FullModel":
                fm = ET.fromstring(ET.tostring(c))
                for p in fm:
                    if local(p.tag) == "Model.profile":
                        p.text = profile
                new.append(fm)
        return new
    eq, gl = clone(eq_profile), clone(gl_profile)
    for c in root:
        if local(c.tag) == "FullModel":
            continue
        (gl if local(c.tag) in GL_CLASSES else eq).append(c)
    return eq, gl


def namespaces(path):
    ns = {}
    for _, (p, u) in ET.iterparse(path, events=["start-ns"]):
        ns[p] = u
    return ns


def write(root, path, ns):
    for p, u in ns.items():
        ET.register_namespace(p, u)
    ET.indent(root, space="  ")
    ET.ElementTree(root).write(path, encoding="UTF-8", xml_declaration=True)


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("src")
    ap.add_argument("dst", help="output file (or, with --split, a base name)")
    ap.add_argument("--units", choices=["si", "cgmes"], default="si")
    ap.add_argument("--id-style", choices=["underscore", "urn"], default="underscore")
    ap.add_argument("--strip-mrid", action="store_true")
    ap.add_argument("--strip-ve", action="store_true")
    ap.add_argument("--split", nargs=2, metavar=("EQ_FILE", "GL_FILE"))
    ap.add_argument("--eq-profile", help="Model.profile URI to write in the equipment file")
    ap.add_argument("--gl-profile", help="Model.profile URI to write in the location file")
    a = ap.parse_args(argv)
    if a.split and not (a.eq_profile and a.gl_profile):
        ap.error("--split needs --eq-profile and --gl-profile (the adapter will not guess profile URIs)")
    ns = namespaces(a.src)
    tree = ET.parse(a.src)
    root = tree.getroot()
    cim_ns = next((u for p, u in ns.items() if p == "cim"), "")
    if a.units == "cgmes":
        convert_units(root)
    if a.id_style == "urn":
        urn_ids(root, cim_ns)
    strip(root, a.strip_mrid, a.strip_ve)
    if a.split:
        eq, gl = split(root, a.eq_profile, a.gl_profile)
        write(eq, a.split[0], ns)
        write(gl, a.split[1], ns)
        print(f"wrote {a.split[0]} and {a.split[1]}")
    else:
        write(root, a.dst, ns)
        print(f"wrote {a.dst}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
