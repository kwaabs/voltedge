#!/usr/bin/env python3
"""Attach demand to the EnergyConsumer objects of a VoltEdge CIM export.

VoltEdge exports no loads (CIM keeps them in the steady-state hypothesis), so a power-flow
run needs them from elsewhere -- billing, AMI, or an assumption. This tool reads a CSV

    name,p_kw,q_kvar
    SP-0457,4.2,1.1

matches each row to an EnergyConsumer by IdentifiedObject.name and writes
EnergyConsumer.p and EnergyConsumer.q (SI watts / vars by default; megawatts / megavars with
--units cgmes). Rows with no matching consumer are reported and ignored.

    python3 cim_add_loads.py in.xml loads.csv out.xml [--units cgmes]

See docs/cim-export-guide.md, Chapter 8.
"""
import argparse
import csv
import sys
import xml.etree.ElementTree as ET

CIM_FALLBACK = "http://iec.ch/TC57/2013/CIM-schema-cim16#"


def local(tag):
    return tag.rsplit("}", 1)[-1]


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("src")
    ap.add_argument("csv")
    ap.add_argument("dst")
    ap.add_argument("--units", choices=["si", "cgmes"], default="si")
    a = ap.parse_args(argv)
    ns = {}
    for _, (p, u) in ET.iterparse(a.src, events=["start-ns"]):
        ns[p] = u
    for p, u in ns.items():
        ET.register_namespace(p, u)
    cim = ns.get("cim", CIM_FALLBACK)
    root = ET.parse(a.src).getroot()
    by_name = {}
    for el in root:
        if local(el.tag) == "EnergyConsumer":
            for c in el:
                if local(c.tag) == "IdentifiedObject.name" and c.text:
                    by_name[c.text.strip()] = el
    scale = 1e-6 if a.units == "cgmes" else 1.0
    done, missing = 0, []
    with open(a.csv, newline="", encoding="utf-8") as f:
        for row in csv.DictReader(f):
            el = by_name.get(row["name"].strip())
            if el is None:
                missing.append(row["name"])
                continue
            for attr, key in (("EnergyConsumer.p", "p_kw"), ("EnergyConsumer.q", "q_kvar")):
                for old in [c for c in el if local(c.tag) == attr]:
                    el.remove(old)
                v = ET.SubElement(el, f"{{{cim}}}{attr}")
                v.text = f"{float(row[key]) * 1000.0 * scale:.9g}"
            done += 1
    ET.indent(root, space="  ")
    ET.ElementTree(root).write(a.dst, encoding="UTF-8", xml_declaration=True)
    print(f"set demand on {done} consumer(s); {len(missing)} CSV row(s) matched nothing" + (f": {', '.join(missing[:10])}" if missing else ""))
    return 0


if __name__ == "__main__":
    sys.exit(main())
