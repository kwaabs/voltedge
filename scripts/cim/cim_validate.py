#!/usr/bin/env python3
"""Structural validator for a VoltEdge CIM/XML export (CDPSM-style, CIM16 namespace).

Standard library only. Usage:

    python3 cim_validate.py voltedge-export.xml            # human-readable report
    python3 cim_validate.py voltedge-export.xml --json     # machine-readable
    python3 cim_validate.py voltedge-export.xml --strict   # warnings fail too
    python3 cim_validate.py eq.xml gl.xml                  # a split model, validated as one

Exit status: 0 = no errors, 1 = errors (or warnings with --strict), 2 = unreadable file.

It checks what a downstream consumer will trip over first -- see docs/cim-export-guide.md
(Chapter 9). It is a structural check, not a full CGMES/CDPSM schema validation.
"""
import argparse
import json
import sys
import xml.etree.ElementTree as ET
from collections import defaultdict

RDF = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
ID, ABOUT, RESOURCE = f"{{{RDF}}}ID", f"{{{RDF}}}about", f"{{{RDF}}}resource"
TWO_TERMINAL = {"ACLineSegment", "LoadBreakSwitch", "Breaker", "Disconnector", "Fuse", "Recloser",
                "Sectionaliser", "GroundDisconnector", "Switch", "Junction"}


def local(tag):
    return tag.rsplit("}", 1)[-1]


def rid(el):
    """The object's identifier without the '#', '_' or 'urn:uuid:' decoration."""
    v = el.get(ID) or el.get(ABOUT) or ""
    return norm(v)


def norm(v):
    for p in ("urn:uuid:", "#"):
        if v.startswith(p):
            v = v[len(p):]
    return v[1:] if v.startswith("_") else v


class Report:
    def __init__(self):
        self.errors, self.warnings, self.info = [], [], []

    def err(self, code, msg):
        self.errors.append({"code": code, "message": msg})

    def warn(self, code, msg):
        self.warnings.append({"code": code, "message": msg})


def load(paths):
    """Every top-level element of one or more files (a CGMES-style split is validated as one model)."""
    if isinstance(paths, str):
        paths = [paths]
    out = []
    for p in paths:
        try:
            out.extend(ET.parse(p).getroot())
        except OSError as e:
            raise SystemExit(f"cannot read {p}: {e}")
    return out


def validate(path):
    r = Report()
    objs = {}                      # id -> element
    by_class = defaultdict(list)
    for el in load(path):
        if local(el.tag) == "FullModel":
            continue
        i = rid(el)
        if not i:
            r.err("E-ID", f"<{local(el.tag)}> has no rdf:ID / rdf:about")
            continue
        if i in objs:
            r.err("E-DUP", f"duplicate id {i} ({local(el.tag)} and {local(objs[i].tag)})")
        objs[i] = el
        by_class[local(el.tag)].append(el)

    def child(el, name):
        for c in el:
            if local(c.tag) == name:
                return c
        return None

    def children(el, name):
        return [c for c in el if local(c.tag) == name]

    def ref(el, name):
        c = child(el, name)
        return norm(c.get(RESOURCE, "")) if c is not None else None

    def text(el, name):
        c = child(el, name)
        return c.text.strip() if c is not None and c.text else None

    # 1. every association resolves to an object (enumeration literals are absolute IRIs)
    dangling = 0
    for i, el in objs.items():
        for c in el:
            v = c.get(RESOURCE)
            if v is None or v.startswith("http"):
                continue
            if norm(v) not in objs:
                dangling += 1
                r.err("E-REF", f"{local(el.tag)} {i}: {local(c.tag)} points at missing object {norm(v)}")

    # 2. mRID literal, when present, equals the id
    for i, el in objs.items():
        m = text(el, "IdentifiedObject.mRID")
        if m is not None and m != i:
            r.warn("W-MRID", f"{local(el.tag)} {i}: IdentifiedObject.mRID {m} differs from its rdf:ID")

    # 3. terminals
    terms_of = defaultdict(list)   # equipment id -> [terminal element]
    node_terms = defaultdict(list)
    for t in by_class["Terminal"]:
        eq, cn, seq = ref(t, "Terminal.ConductingEquipment"), ref(t, "Terminal.ConnectivityNode"), text(t, "Terminal.sequenceNumber")
        if not eq:
            r.err("E-TERM", f"Terminal {rid(t)} has no ConductingEquipment")
        if not cn:
            r.err("E-TERM", f"Terminal {rid(t)} has no ConnectivityNode")
        if seq is None:
            r.warn("W-SEQ", f"Terminal {rid(t)} has no sequenceNumber")
        if eq:
            terms_of[eq].append(t)
        if cn:
            node_terms[cn].append(t)

    for eid, ts in terms_of.items():
        seqs = sorted(int(text(t, "Terminal.sequenceNumber") or 0) for t in ts)
        if seqs and seqs[0] != 1:
            r.warn("W-SEQ1", f"{label(objs, eid)}: terminal sequence numbers start at {seqs[0]}, not 1")
        if len(set(seqs)) != len(seqs):
            r.err("E-SEQDUP", f"{label(objs, eid)}: duplicate terminal sequence numbers {seqs}")

    # 4. two-terminal equipment: exactly 2 terminals, on different nodes (CGMES constraint 452)
    for cls in TWO_TERMINAL:
        for el in by_class[cls]:
            eid = rid(el)
            ts = terms_of.get(eid, [])
            if len(ts) != 2:
                r.err("E-2TERM", f"{label(objs, eid)}: {cls} has {len(ts)} terminals, expected 2")
                continue
            nodes = {ref(t, "Terminal.ConnectivityNode") for t in ts}
            if len(nodes) == 1:
                r.err("E-452", f"{label(objs, eid)}: both terminals sit on the same ConnectivityNode (CGMES constraint 452)")

    # 5. transformers: >= 2 windings, each bound to one of its own terminals
    ends_of = defaultdict(list)
    for e in by_class["PowerTransformerEnd"]:
        tx = ref(e, "PowerTransformerEnd.PowerTransformer")
        ends_of[tx].append(e)
        tm = ref(e, "TransformerEnd.Terminal")
        if tm and tm in objs and ref(objs[tm], "Terminal.ConductingEquipment") != tx:
            r.err("E-END", f"PowerTransformerEnd {rid(e)} uses a terminal that belongs to a different equipment")
    for el in by_class["PowerTransformer"]:
        n = len(ends_of.get(rid(el), []))
        if n < 2:
            r.err("E-WIND", f"{label(objs, rid(el))}: PowerTransformer has {n} winding(s), a valid transformer needs at least 2")

    # 6. connectivity nodes: empty nodes are errors, single-terminal nodes are open ends
    open_ends = []
    for n in by_class["ConnectivityNode"]:
        k = len(node_terms.get(rid(n), []))
        if k == 0:
            r.err("E-NODE", f"ConnectivityNode {rid(n)} has no terminals")
        elif k == 1:
            t = node_terms[rid(n)][0]
            open_ends.append(label(objs, ref(t, "Terminal.ConductingEquipment")) + f" (terminal {text(t, 'Terminal.sequenceNumber')})")
    if open_ends:
        r.warn("W-OPEN", f"{len(open_ends)} open end(s) on single-terminal nodes: " + "; ".join(sorted(open_ends)[:12]) + (" …" if len(open_ends) > 12 else ""))

    # 7. voltage consistency across a node (a line cannot step voltage)
    volts = {}
    for b in by_class["BaseVoltage"]:
        v = text(b, "BaseVoltage.nominalVoltage")
        try:
            volts[rid(b)] = float(v)
        except (TypeError, ValueError):
            r.err("E-BV", f"BaseVoltage {rid(b)} has no numeric nominalVoltage")
    end_bv = {}
    for e in by_class["PowerTransformerEnd"]:
        tm, bv = ref(e, "TransformerEnd.Terminal"), ref(e, "TransformerEnd.BaseVoltage")
        if tm and bv:
            end_bv[tm] = bv
    for n, ts in node_terms.items():
        seen = {}
        for t in ts:
            eid = ref(t, "Terminal.ConductingEquipment")
            bv = end_bv.get(rid(t)) or (ref(objs[eid], "ConductingEquipment.BaseVoltage") if eid in objs else None)
            if bv in volts:
                seen[label(objs, eid)] = volts[bv]
        if len(set(seen.values())) > 1:
            r.warn("W-VOLT", f"node {n}: joins different voltages {seen}")

    # 8. units sanity: a CGMES-style file would say 11 / 0.415, VoltEdge says 11000 / 415
    if volts and max(volts.values()) < 1000:
        r.warn("W-UNIT", f"largest BaseVoltage is {max(volts.values())}: file looks like kV, not the SI volts VoltEdge writes")

    # 9. conductors with no length / impedance (information, not an error)
    nolen = [label(objs, rid(e)) for e in by_class["ACLineSegment"] if child(e, "Conductor.length") is None]
    if nolen:
        r.warn("W-LEN", f"{len(nolen)} ACLineSegment(s) have no Conductor.length (unplaced?): " + "; ".join(nolen[:8]))
    noimp = [e for e in by_class["ACLineSegment"] if child(e, "ACLineSegment.r") is None]
    if noimp:
        r.info.append(f"{len(noimp)} of {len(by_class['ACLineSegment'])} ACLineSegment(s) carry no impedance (no material spec)")

    r.info.insert(0, f"{len(objs)} objects; {len(by_class['Terminal'])} terminals on {len(by_class['ConnectivityNode'])} nodes; "
                     f"{len(by_class['ACLineSegment'])} lines; {len(by_class['PowerTransformer'])} transformers; "
                     f"{len(by_class['PowerTransformerEnd'])} windings; {dangling} dangling reference(s)")
    return r


def summary(path):
    """Reconciliation totals to compare against the consuming tool's own counts."""
    objs, by_class = {}, defaultdict(list)
    for el in load(path):
        if local(el.tag) != "FullModel":
            objs[rid(el)] = el
            by_class[local(el.tag)].append(el)
    def child(el, name):
        for c in el:
            if local(c.tag) == name:
                return c
    def num(el, name):
        c = child(el, name)
        try:
            return float(c.text)
        except (AttributeError, TypeError, ValueError):
            return None
    volts = {i: num(e, "BaseVoltage.nominalVoltage") for i, e in objs.items() if local(e.tag) == "BaseVoltage"}
    km = defaultdict(float)
    for e in by_class["ACLineSegment"]:
        c = child(e, "ConductingEquipment.BaseVoltage")
        v = volts.get(norm(c.get(RESOURCE, "")) if c is not None else "")
        km[v] += (num(e, "Conductor.length") or 0.0) / 1000.0
    ends = defaultdict(list)
    for e in by_class["PowerTransformerEnd"]:
        c = child(e, "PowerTransformerEnd.PowerTransformer")
        ends[norm(c.get(RESOURCE, ""))].append(e)
    kva = sum(max((num(x, "PowerTransformerEnd.ratedS") or 0.0) for x in es) for es in ends.values()) / 1000.0 if ends else 0.0
    lines = ["counts by class:"]
    for cls in sorted(by_class):
        lines.append(f"  {cls:<24}{len(by_class[cls])}")
    lines.append("line length by base voltage (km):")
    for v in sorted((k for k in km if k is not None)):
        lines.append(f"  {v:>10g} V  {km[v]:.3f}")
    if None in km:
        lines.append(f"  {'(no BaseVoltage)':>16}  {km[None]:.3f}")
    lines.append(f"transformer capacity (kVA, sum of each transformer's largest winding rating): {kva:,.1f}")
    return "\n".join(lines)


def label(objs, eid):
    el = objs.get(eid)
    if el is None:
        return str(eid)
    for c in el:
        if local(c.tag) == "IdentifiedObject.name" and c.text:
            return f"{c.text.strip()} [{local(el.tag)}]"
    return f"{eid} [{local(el.tag)}]"


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("file", nargs="+", help="one CIM/XML file, or the files of a split model (EQ + GL)")
    ap.add_argument("--json", action="store_true")
    ap.add_argument("--strict", action="store_true", help="treat warnings as failures")
    ap.add_argument("--summary", action="store_true", help="also print reconciliation totals (counts, km, kVA)")
    a = ap.parse_args(argv)
    try:
        r = validate(a.file)
    except ET.ParseError as e:
        print(f"NOT WELL-FORMED: {e}", file=sys.stderr)
        return 2
    except SystemExit as e:
        print(e, file=sys.stderr)
        return 2
    if a.json:
        print(json.dumps({"errors": r.errors, "warnings": r.warnings, "info": r.info}, indent=2))
    else:
        for line in r.info:
            print("info   ", line)
        for w in r.warnings:
            print(f"WARN   {w['code']}: {w['message']}")
        for e in r.errors:
            print(f"ERROR  {e['code']}: {e['message']}")
        print(f"\n{len(r.errors)} error(s), {len(r.warnings)} warning(s)")
        if a.summary:
            print("\n" + summary(a.file))
    return 1 if r.errors or (a.strict and r.warnings) else 0


if __name__ == "__main__":
    sys.exit(main())
