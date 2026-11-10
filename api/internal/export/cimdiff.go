package export

// CIM difference-model engine (incremental export). VoltEdge produces FULL CIM
// models; to feed an external system incrementally, this diffs the current full
// model against a stored BASELINE (what a sync target last received) at the
// RDF-statement level and emits a dm:DifferenceModel (forwardDifferences to add,
// reverseDifferences to remove). It leans entirely on the encoder's determinism
// (M1 content-hashed model identity + M4 stable ordering) and on stable per-object
// mRIDs: a changed attribute becomes "remove old statement + add new statement" on
// the SAME subject, exactly what a difference model expresses.
//
// The encoder emits a flat shape (cim.go emit helpers): every object is
//   <cim:Class rdf:ID="_<uuid>"> <cim:Class.attr>val</cim:Class.attr>
//     <cim:Class.ref rdf:resource="#_<uuid>"/> ... </cim:Class>
// so parsing back to (subject, predicate, object) statements is unambiguous. The
// md:FullModel header is skipped (its mRID/created are expected to differ).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// dmNS is the CIM difference-model namespace (IEC 61970-552).
const dmNS = "http://iec.ch/TC57/61970-552/DifferenceModel/1#"

// stmtKind distinguishes the three statement shapes the encoder emits.
type stmtKind byte

const (
	stmtType stmtKind = 'T' // rdf:type (from the object's element name)
	stmtLit  stmtKind = 'L' // a literal child (<cim:X.attr>val</cim:X.attr>)
	stmtRef  stmtKind = 'R' // a resource child (<cim:X.ref rdf:resource="..."/>)
)

// cimStatement is one RDF statement extracted from a CIM model. subject is the
// object's rdf:ID value including the leading "_" (e.g. "_<uuid>"); predicate is a
// QName ("cim:ACLineSegment.r" or "rdf:type"); object is the literal text, the
// resource value ("#_<uuid>" or an enum URI), or the class QName for a type.
type cimStatement struct {
	subject   string
	predicate string
	kind      stmtKind
	object    string
}

func (s cimStatement) key() string {
	return s.subject + "\x00" + s.predicate + "\x00" + string(s.kind) + "\x00" + s.object
}

// parseStatements reads a CIM RDF/XML document and returns its body as a set of
// statements keyed by key(). The md:FullModel header is excluded. An object with
// no rdf:ID is skipped (the encoder always emits one, so this only guards against
// a stray element). Entities are decoded by encoding/xml, so values come back
// unescaped -- writeDifferenceModel re-escapes on the way out.
func parseStatements(r io.Reader) (map[string]cimStatement, error) {
	dec := xml.NewDecoder(r)
	out := map[string]cimStatement{}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("cim diff: parsing baseline/current model: %w", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		// The rdf:RDF root: descend into it (its children are the objects).
		if se.Name.Local == "RDF" && se.Name.Space == rdfNS {
			continue
		}
		// The model header carries no equipment statements -- skip the subtree.
		if se.Name.Space == mdNS {
			if err := dec.Skip(); err != nil {
				return nil, err
			}
			continue
		}
		subject := attrVal(se.Attr, rdfNS, "ID")
		if subject == "" {
			if err := dec.Skip(); err != nil {
				return nil, err
			}
			continue
		}
		subject = "_" + strings.TrimPrefix(subject, "_") // normalise (rdf:ID drops the "_")
		addStmt(out, cimStatement{subject, "rdf:type", stmtType, qname(se.Name)})
		if err := parseObjectChildren(dec, subject, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// parseObjectChildren consumes one object's children up to and including its
// closing tag, adding a statement per child.
func parseObjectChildren(dec *xml.Decoder, subject string, out map[string]cimStatement) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("cim diff: truncated object %s: %w", subject, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			pred := qname(t.Name)
			if res := attrVal(t.Attr, rdfNS, "resource"); res != "" {
				addStmt(out, cimStatement{subject, pred, stmtRef, res})
				if err := dec.Skip(); err != nil {
					return err
				}
			} else {
				txt, err := childText(dec)
				if err != nil {
					return err
				}
				addStmt(out, cimStatement{subject, pred, stmtLit, txt})
			}
		case xml.EndElement:
			return nil
		}
	}
}

// childText returns the character data of the current element, consuming up to its
// closing tag.
func childText(dec *xml.Decoder) (string, error) {
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			if err := dec.Skip(); err != nil { // not expected in our output; stay safe
				return "", err
			}
		case xml.EndElement:
			return b.String(), nil
		}
	}
}

func addStmt(out map[string]cimStatement, s cimStatement) { out[s.key()] = s }

// qname maps a decoded namespace URI back to the prefix the encoder uses. Our
// documents declare only rdf/cim/md, so anything else is the CIM namespace.
func qname(n xml.Name) string {
	switch n.Space {
	case rdfNS:
		return "rdf:" + n.Local
	case mdNS:
		return "md:" + n.Local
	default:
		return "cim:" + n.Local
	}
}

func attrVal(attrs []xml.Attr, ns, local string) string {
	for _, a := range attrs {
		if a.Name.Local == local && (a.Name.Space == ns || a.Name.Space == "") {
			return a.Value
		}
	}
	return ""
}

// diffStatements returns the forward (add) and reverse (remove) sets: statements
// in current but not baseline, and in baseline but not current. Both are sorted by
// key so the difference model is deterministic.
func diffStatements(baseline, current map[string]cimStatement) (forward, reverse []cimStatement) {
	for k, s := range current {
		if _, ok := baseline[k]; !ok {
			forward = append(forward, s)
		}
	}
	for k, s := range baseline {
		if _, ok := current[k]; !ok {
			reverse = append(reverse, s)
		}
	}
	sortStmts(forward)
	sortStmts(reverse)
	return forward, reverse
}

func sortStmts(s []cimStatement) {
	sort.Slice(s, func(i, j int) bool { return s[i].key() < s[j].key() })
}

// DiffResult reports the size of a difference for logging/status.
type DiffResult struct {
	Forward int
	Reverse int
}

// writeDifferenceModel serialises a dm:DifferenceModel. The model mRID is a hash
// of the two statement sets (stable identity for this delta); generated stamps
// Model.created (the job's created_at). Returns the forward/reverse counts.
func writeDifferenceModel(w io.Writer, forward, reverse []cimStatement, profile *metadata.CimProfile, generated time.Time) (DiffResult, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&b, "<rdf:RDF xmlns:rdf=%q xmlns:cim=%q xmlns:md=%q xmlns:dm=%q>\n",
		rdfNS, profile.CimNamespace, mdNS, dmNS)

	fmt.Fprintf(&b, "  <dm:DifferenceModel rdf:about=\"urn:uuid:%s\">\n", differenceMRID(forward, reverse))
	fmt.Fprintf(&b, "    <md:Model.created>%s</md:Model.created>\n", generated.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "    <md:Model.modelingAuthoritySet>%s</md:Model.modelingAuthoritySet>\n", xmlEscape(profile.ModelAuthority))
	fmt.Fprintf(&b, "    <md:Model.profile>%s</md:Model.profile>\n", xmlEscape(profile.ProfileURI))

	writeDifferenceSet(&b, "dm:reverseDifferences", reverse, profile)
	writeDifferenceSet(&b, "dm:forwardDifferences", forward, profile)

	b.WriteString("  </dm:DifferenceModel>\n")
	b.WriteString("</rdf:RDF>\n")
	if _, err := io.WriteString(w, b.String()); err != nil {
		return DiffResult{}, err
	}
	return DiffResult{Forward: len(forward), Reverse: len(reverse)}, nil
}

// writeDifferenceSet emits one <dm:forwardDifferences|reverseDifferences
// rdf:parseType="Statements"> block, grouping statements by subject into
// rdf:Description fragments (sorted; empty set => an empty element).
func writeDifferenceSet(b *strings.Builder, element string, stmts []cimStatement, profile *metadata.CimProfile) {
	if len(stmts) == 0 {
		fmt.Fprintf(b, "    <%s rdf:parseType=\"Statements\"></%s>\n", element, element)
		return
	}
	fmt.Fprintf(b, "    <%s rdf:parseType=\"Statements\">\n", element)
	// Stable subject order; stmts is already key-sorted so a subject's statements
	// are contiguous and ordered.
	var subjects []string
	bySubject := map[string][]cimStatement{}
	for _, s := range stmts {
		if _, seen := bySubject[s.subject]; !seen {
			subjects = append(subjects, s.subject)
		}
		bySubject[s.subject] = append(bySubject[s.subject], s)
	}
	sort.Strings(subjects)
	for _, subj := range subjects {
		fmt.Fprintf(b, "      <rdf:Description rdf:about=\"#%s\">\n", subj)
		for _, s := range bySubject[subj] {
			switch s.kind {
			case stmtType:
				// object is a "cim:Class" QName -> an rdf:type resource URI.
				fmt.Fprintf(b, "        <rdf:type rdf:resource=\"%s%s\"/>\n",
					profile.CimNamespace, strings.TrimPrefix(s.object, "cim:"))
			case stmtRef:
				fmt.Fprintf(b, "        <%s rdf:resource=\"%s\"/>\n", s.predicate, s.object)
			default: // stmtLit
				fmt.Fprintf(b, "        <%s>%s</%s>\n", s.predicate, xmlEscape(s.object), s.predicate)
			}
		}
		b.WriteString("      </rdf:Description>\n")
	}
	fmt.Fprintf(b, "    </%s>\n", element)
}

// differenceMRID derives a stable mRID for the delta from its statement sets, so
// two runs that produce the same change produce the same difference identity.
func differenceMRID(forward, reverse []cimStatement) string {
	h := sha256.New()
	io.WriteString(h, "forward\x00")
	for _, s := range forward {
		io.WriteString(h, s.key())
		h.Write([]byte{'\n'})
	}
	io.WriteString(h, "reverse\x00")
	for _, s := range reverse {
		io.WriteString(h, s.key())
		h.Write([]byte{'\n'})
	}
	return uuidV5("difference:" + hex.EncodeToString(h.Sum(nil)))
}
