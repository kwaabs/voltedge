// Command cimdict parses an authoritative CIM ontology in Turtle (.ttl) and
// emits the reference-dictionary seeds that populate the metadata.cim_ref_*
// tables (migration 0120): one seed for CIM classes, one for their properties
// (attributes + associations).
//
// This exists so the CIM export mapping editor (Phase 16) is no longer limited
// to hand-typed cim_class / cim_attribute strings. The dictionary is the FULL
// CIM complement, and -- crucially -- it is GENERATED from the schema file, never
// authored by hand: every row is traceable to schema/cim/IEC_CIM.ttl, so we do
// not "make up" the CIM vocabulary from memory.
//
// The input is the TNO IEC CIM ontology (http://www.iec.ch/TC57/CIM#), a regular
// OWL/RDFS Turtle file:
//
//	cim:ACDCConverter rdf:type owl:Class ;
//	                  owl:equivalentClass [ owl:Restriction ... ] , [ ... ] ;
//	                  rdfs:subClassOf cim:ConductingEquipment ;
//	                  rdfs:comment "..." .
//
//	cim:ACDCConverter.numberOfValves rdf:type owl:DatatypeProperty ;
//	                                 rdfs:domain cim:ACDCConverter ;
//	                                 rdfs:range xsd:nonNegativeInteger ;
//	                                 rdfs:comment "..." .
//
//	cim:ACLineSegment.length rdf:type owl:ObjectProperty ;  // association
//	                         rdfs:domain cim:ACLineSegment ;
//	                         rdfs:range cim:Length ; ...
//
// Usage (Bash is EPERM-sandboxed here, so the user runs this):
//
//	go run ./cmd/cimdict                      # defaults, from api/ working dir
//	go run ./cmd/cimdict -in ../schema/cim/IEC_CIM.ttl -out ../seed
//
// It is deterministic (sorted output) so re-running produces a stable diff.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	cimNamespace = "http://www.iec.ch/TC57/CIM#"
	// sourceLabel is written into every row's `source` column for provenance.
	// It is the repo-relative path of the parsed file, not the absolute -in path.
	sourceLabel = "schema/cim/IEC_CIM.ttl"
)

func main() {
	in := flag.String("in", filepath.FromSlash("../schema/cim/IEC_CIM.ttl"), "path to the CIM ontology Turtle file")
	outDir := flag.String("out", filepath.FromSlash("../seed"), "directory to write the seed .sql files into")
	flag.Parse()

	raw, err := os.ReadFile(*in)
	if err != nil {
		fatal("read %s: %v", *in, err)
	}

	stmts, err := parseTurtle(string(raw))
	if err != nil {
		fatal("parse %s: %v", *in, err)
	}

	classes, attrs := collect(stmts)

	classSQL := renderClasses(classes)
	if err := os.WriteFile(filepath.Join(*outDir, "cim_reference_classes.sql"), []byte(classSQL), 0o644); err != nil {
		fatal("write classes seed: %v", err)
	}
	attrSQL := renderAttributes(attrs)
	if err := os.WriteFile(filepath.Join(*outDir, "cim_reference_attributes.sql"), []byte(attrSQL), 0o644); err != nil {
		fatal("write attributes seed: %v", err)
	}

	fmt.Printf("cimdict: %d classes, %d attributes parsed from %s\n", len(classes), len(attrs), *in)
	fmt.Printf("  wrote %s\n", filepath.Join(*outDir, "cim_reference_classes.sql"))
	fmt.Printf("  wrote %s\n", filepath.Join(*outDir, "cim_reference_attributes.sql"))
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "cimdict: "+format+"\n", a...)
	os.Exit(1)
}

// ---------------------------------------------------------------------------
// Model
// ---------------------------------------------------------------------------

type cimClass struct {
	Name        string // unqualified, e.g. ACDCConverter
	Parent      string // superclass local name, or "" for a root
	Description string
}

type cimAttr struct {
	Class         string // owning class local name
	Name          string // property name after the first dot, e.g. numberOfValves
	IsAssociation bool   // owl:ObjectProperty -> true; owl:DatatypeProperty -> false
	Range         string // range local name (a datatype or a class), or ""
	Description   string
}

// ---------------------------------------------------------------------------
// Turtle tokenizer + statement parser
//
// Turtle is small but has two teeth we must respect:
//   - a '.' can be INSIDE a prefixed local name (cim:ACDCConverter.baseS); it is
//     a statement terminator only when followed by whitespace/EOF.
//   - blank nodes [ ... ] (the owl:Restriction cardinality blocks) nest and must
//     be balanced-skipped, not parsed.
// ---------------------------------------------------------------------------

type tokKind int

const (
	tName      tokKind = iota // prefixed name (cim:Foo), keyword, number, or full <IRI>
	tString                   // a literal string value (quotes + any ^^type/@lang stripped)
	tPunct                    // one of ; , . [ ] ( )
	tDirective                // @prefix / @base
)

type token struct {
	kind tokKind
	val  string
}

func parseTurtle(s string) ([]statement, error) {
	toks, err := tokenize(s)
	if err != nil {
		return nil, err
	}
	return parseStatements(toks)
}

func tokenize(s string) ([]token, error) {
	var toks []token
	i, n := 0, len(s)
	for i < n {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '#': // comment to end of line
			for i < n && s[i] != '\n' {
				i++
			}
		case c == '@': // @prefix / @base directive
			j := i + 1
			for j < n && !isSpace(s[j]) {
				j++
			}
			toks = append(toks, token{tDirective, s[i:j]})
			i = j
		case c == '"' || c == '\'':
			val, next, err := scanString(s, i)
			if err != nil {
				return nil, err
			}
			toks = append(toks, token{tString, val})
			i = next
		case c == '<': // full IRI
			j := i + 1
			for j < n && s[j] != '>' {
				j++
			}
			if j >= n {
				return nil, fmt.Errorf("unterminated IRI at offset %d", i)
			}
			toks = append(toks, token{tName, s[i+1 : j]})
			i = j + 1
		case c == ';' || c == ',' || c == '[' || c == ']' || c == '(' || c == ')':
			toks = append(toks, token{tPunct, string(c)})
			i++
		case c == '.':
			// Terminator only if not glued to a name-continuation char.
			if i+1 < n && isNameByte(s[i+1]) {
				name, next := scanName(s, i)
				toks = append(toks, token{tName, name})
				i = next
			} else {
				toks = append(toks, token{tPunct, "."})
				i++
			}
		default:
			name, next := scanName(s, i)
			if next == i { // no progress -> unknown byte, skip it defensively
				i++
				continue
			}
			toks = append(toks, token{tName, name})
			i = next
		}
	}
	return toks, nil
}

// scanString reads a Turtle string literal starting at s[i] (a quote), handling
// both single ("..." / '...') and triple ("""...""" / '''...''') forms with
// backslash escapes, then consumes any ^^datatype or @lang suffix. It returns
// the decoded string value and the offset just past the whole literal.
func scanString(s string, i int) (string, int, error) {
	q := s[i]
	n := len(s)
	triple := i+2 < n && s[i+1] == q && s[i+2] == q
	var b strings.Builder
	if triple {
		i += 3
		for i < n {
			if s[i] == '\\' && i+1 < n {
				b.WriteByte(unescape(s[i+1]))
				i += 2
				continue
			}
			if s[i] == q && i+2 < n && s[i+1] == q && s[i+2] == q {
				i += 3
				return finishLiteral(s, i, b.String())
			}
			// tolerate a lone/paired quote inside a triple-quoted string
			if s[i] == q && !(i+2 < n && s[i+1] == q && s[i+2] == q) {
				b.WriteByte(s[i])
				i++
				continue
			}
			b.WriteByte(s[i])
			i++
		}
		return "", i, fmt.Errorf("unterminated triple-quoted string")
	}
	i++ // past opening quote
	for i < n {
		if s[i] == '\\' && i+1 < n {
			b.WriteByte(unescape(s[i+1]))
			i += 2
			continue
		}
		if s[i] == q {
			i++
			return finishLiteral(s, i, b.String())
		}
		if s[i] == '\n' {
			return "", i, fmt.Errorf("newline in single-quoted string")
		}
		b.WriteByte(s[i])
		i++
	}
	return "", i, fmt.Errorf("unterminated string")
}

// finishLiteral consumes an optional ^^<datatype> or @lang suffix after a string
// literal and returns the (value, offset-past-suffix).
func finishLiteral(s string, i int, val string) (string, int, error) {
	n := len(s)
	if i+1 < n && s[i] == '^' && s[i+1] == '^' {
		i += 2
		if i < n && s[i] == '<' {
			for i < n && s[i] != '>' {
				i++
			}
			if i < n {
				i++
			}
		} else {
			_, i = scanName(s, i)
		}
	} else if i < n && s[i] == '@' {
		i++
		for i < n && (isLetter(s[i]) || s[i] == '-') {
			i++
		}
	}
	return val, i, nil
}

func unescape(c byte) byte {
	switch c {
	case 'n':
		return '\n'
	case 't':
		return '\t'
	case 'r':
		return '\r'
	default:
		return c // \" \\ \' and anything else -> literal
	}
}

// scanName reads a prefixed name / keyword starting at s[i], including embedded
// dots that are followed by a name byte (so cim:Foo.bar stays one token but a
// trailing '.' terminator is left for the caller).
func scanName(s string, i int) (string, int) {
	n := len(s)
	start := i
	for i < n {
		c := s[i]
		if c == '.' {
			if i+1 < n && isNameByte(s[i+1]) {
				i++
				continue
			}
			break
		}
		if isNameByte(c) {
			i++
			continue
		}
		break
	}
	return s[start:i], i
}

func isSpace(c byte) bool  { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }
func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }

// isNameByte covers the bytes a prefixed name / IRI-ish token may contain, apart
// from the special-cased '.'. Kept generous on purpose.
func isNameByte(c byte) bool {
	return isLetter(c) || isDigit(c) || c == '_' || c == '-' || c == ':' ||
		c == '%' || c == '/' || c == '#' || c == '~'
}

// statement is one Turtle triple-block: a subject and its predicate/object list.
type statement struct {
	subject string
	preds   []predObj
}

type predObj struct {
	pred    string
	objects []objValue
}

type objValue struct {
	val      string
	isString bool
	isBlank  bool // a [ ... ] or ( ... ) node we skipped
}

func parseStatements(toks []token) ([]statement, error) {
	var stmts []statement
	i, n := 0, len(toks)
	for i < n {
		t := toks[i]
		if t.kind == tDirective {
			// skip @prefix/@base ... through the terminating '.'
			i++
			for i < n && !(toks[i].kind == tPunct && toks[i].val == ".") {
				i++
			}
			if i < n {
				i++
			}
			continue
		}
		if t.kind == tPunct { // stray punctuation, skip
			i++
			continue
		}
		st := statement{subject: t.val}
		i++
		// predicate-object list
		for i < n {
			if toks[i].kind != tName {
				// unexpected; bail this statement to the next '.'
				break
			}
			po := predObj{pred: toks[i].val}
			i++
			// objects, comma-separated
			for i < n {
				ov, ni, err := readObject(toks, i)
				if err != nil {
					return nil, err
				}
				po.objects = append(po.objects, ov)
				i = ni
				if i < n && toks[i].kind == tPunct && toks[i].val == "," {
					i++
					continue
				}
				break
			}
			st.preds = append(st.preds, po)
			if i < n && toks[i].kind == tPunct && toks[i].val == ";" {
				i++
				continue
			}
			break
		}
		// consume terminating '.'
		for i < n && !(toks[i].kind == tPunct && toks[i].val == ".") {
			i++
		}
		if i < n {
			i++
		}
		stmts = append(stmts, st)
	}
	return stmts, nil
}

// readObject reads one object, balance-skipping a blank node [ ... ] or
// collection ( ... ). Returns the object value and the offset past it.
func readObject(toks []token, i int) (objValue, int, error) {
	n := len(toks)
	if i >= n {
		return objValue{}, i, fmt.Errorf("unexpected end of tokens reading object")
	}
	t := toks[i]
	if t.kind == tPunct && (t.val == "[" || t.val == "(") {
		open, close := t.val, "]"
		if open == "(" {
			close = ")"
		}
		depth := 0
		for i < n {
			if toks[i].kind == tPunct && toks[i].val == open {
				depth++
			} else if toks[i].kind == tPunct && toks[i].val == close {
				depth--
				if depth == 0 {
					i++
					break
				}
			}
			i++
		}
		return objValue{isBlank: true}, i, nil
	}
	return objValue{val: t.val, isString: t.kind == tString}, i + 1, nil
}

// ---------------------------------------------------------------------------
// Collect classes + attributes from parsed statements
// ---------------------------------------------------------------------------

func collect(stmts []statement) ([]cimClass, []cimAttr) {
	classByName := map[string]cimClass{}
	attrByKey := map[string]cimAttr{} // "Class.name" -> attr

	for _, st := range stmts {
		rdfType := firstNamedObject(st, "rdf:type")
		switch localName(rdfType) {
		case "Class":
			name := localName(st.subject)
			if name == "" {
				continue
			}
			classByName[name] = cimClass{
				Name:        name,
				Parent:      localName(firstCimObject(st, "rdfs:subClassOf")),
				Description: cleanText(firstStringObject(st, "rdfs:comment")),
			}
		case "ObjectProperty", "DatatypeProperty":
			isAssoc := localName(rdfType) == "ObjectProperty"
			subjLocal := localName(st.subject)
			cls, attrName := splitProperty(subjLocal)
			if cls == "" || attrName == "" {
				continue
			}
			key := cls + "." + attrName
			if _, seen := attrByKey[key]; seen {
				continue // dedupe UNIQUE(class_name, name)
			}
			attrByKey[key] = cimAttr{
				Class:         cls,
				Name:          attrName,
				IsAssociation: isAssoc,
				Range:         localName(firstNamedObject(st, "rdfs:range")),
				Description:   cleanText(firstStringObject(st, "rdfs:comment")),
			}
		}
	}

	classes := make([]cimClass, 0, len(classByName))
	for _, c := range classByName {
		classes = append(classes, c)
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i].Name < classes[j].Name })

	// Only keep attributes whose owning class exists in the class set, so the
	// cim_ref_attributes.class_name FK to cim_ref_classes.name always resolves.
	attrs := make([]cimAttr, 0, len(attrByKey))
	for _, a := range attrByKey {
		if _, ok := classByName[a.Class]; ok {
			attrs = append(attrs, a)
		}
	}
	sort.Slice(attrs, func(i, j int) bool {
		if attrs[i].Class != attrs[j].Class {
			return attrs[i].Class < attrs[j].Class
		}
		return attrs[i].Name < attrs[j].Name
	})
	return classes, attrs
}

// splitProperty resolves the owning class and the attribute name for a property
// whose subject local name is like "ACDCConverter.numberOfValves": CIM names a
// property OwningClass.member, so the class is the part before the first dot and
// the attribute name is the part after it. We deliberately do NOT use rdfs:domain
// here -- in this ontology the domain of an OBJECT property points at the far end
// of the association (e.g. cim:ACDCConverter.ACDCConverterDCTerminal has
// rdfs:domain cim:ACDCConverterDCTerminal), so grouping by domain would file the
// association under the wrong class. For datatype properties the subject prefix
// and the domain agree, so this is consistent for both.
func splitProperty(subjLocal string) (string, string) {
	dot := strings.Index(subjLocal, ".")
	if dot < 0 {
		return "", ""
	}
	return subjLocal[:dot], subjLocal[dot+1:]
}

func firstNamedObject(st statement, pred string) string {
	for _, po := range st.preds {
		if po.pred != pred {
			continue
		}
		for _, o := range po.objects {
			if !o.isBlank && !o.isString {
				return o.val
			}
		}
	}
	return ""
}

// firstCimObject is like firstNamedObject but only returns objects in the cim:
// namespace -- used for rdfs:subClassOf, whose object may instead be a blank
// owl:Restriction node we want to ignore.
func firstCimObject(st statement, pred string) string {
	for _, po := range st.preds {
		if po.pred != pred {
			continue
		}
		for _, o := range po.objects {
			if o.isBlank || o.isString {
				continue
			}
			if strings.HasPrefix(o.val, "cim:") || strings.Contains(o.val, cimNamespace) {
				return o.val
			}
		}
	}
	return ""
}

func firstStringObject(st statement, pred string) string {
	for _, po := range st.preds {
		if po.pred != pred {
			continue
		}
		for _, o := range po.objects {
			if o.isString {
				return o.val
			}
		}
	}
	return ""
}

// localName strips a prefix (cim:Foo -> Foo) or an IRI fragment/path
// (http://.../CIM#Foo -> Foo) to the unqualified local name.
func localName(s string) string {
	if s == "" {
		return ""
	}
	if h := strings.LastIndex(s, "#"); h >= 0 {
		return s[h+1:]
	}
	if strings.Contains(s, "://") { // a full IRI with no fragment
		if sl := strings.LastIndex(s, "/"); sl >= 0 {
			return s[sl+1:]
		}
	}
	if c := strings.LastIndex(s, ":"); c >= 0 {
		return s[c+1:]
	}
	return s
}

var wsRun = regexp.MustCompile(`\s+`)

// cleanText collapses whitespace runs (including the newlines in triple-quoted
// comments) into single spaces so descriptions stay one-line and diff-stable.
func cleanText(s string) string {
	return strings.TrimSpace(wsRun.ReplaceAllString(s, " "))
}

// ---------------------------------------------------------------------------
// SQL rendering
// ---------------------------------------------------------------------------

const rowsPerInsert = 400

func sqlStr(s string) string {
	if s == "" {
		return "NULL"
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func renderClasses(classes []cimClass) string {
	var b strings.Builder
	b.WriteString("-- cim_reference_classes.sql\n")
	b.WriteString("-- GENERATED by api/cmd/cimdict from " + sourceLabel + " -- DO NOT EDIT BY HAND.\n")
	b.WriteString("-- Re-generate with: go run ./cmd/cimdict   (from the api/ directory)\n")
	b.WriteString("-- The full CIM class complement (reference layer for migration 0120).\n")
	fmt.Fprintf(&b, "-- %d classes, namespace %s\n\n", len(classes), cimNamespace)
	b.WriteString("DELETE FROM metadata.cim_ref_classes;  -- cascades to cim_ref_attributes\n\n")

	cols := "INSERT INTO metadata.cim_ref_classes (name, parent_name, namespace, is_abstract, description, source) VALUES\n"
	for start := 0; start < len(classes); start += rowsPerInsert {
		end := start + rowsPerInsert
		if end > len(classes) {
			end = len(classes)
		}
		b.WriteString(cols)
		for i := start; i < end; i++ {
			c := classes[i]
			fmt.Fprintf(&b, "  (%s, %s, %s, false, %s, %s)",
				sqlStr(c.Name), sqlStr(c.Parent), sqlStr(cimNamespace),
				sqlStr(c.Description), sqlStr(sourceLabel))
			if i < end-1 {
				b.WriteString(",\n")
			} else {
				b.WriteString(";\n\n")
			}
		}
	}
	return b.String()
}

func renderAttributes(attrs []cimAttr) string {
	var b strings.Builder
	b.WriteString("-- cim_reference_attributes.sql\n")
	b.WriteString("-- GENERATED by api/cmd/cimdict from " + sourceLabel + " -- DO NOT EDIT BY HAND.\n")
	b.WriteString("-- Re-generate with: go run ./cmd/cimdict   (from the api/ directory)\n")
	b.WriteString("-- Every CIM class property: datatype properties (is_association=false) and\n")
	b.WriteString("-- object properties / associations (is_association=true). multiplicity is left\n")
	b.WriteString("-- NULL -- the ontology carries it only as owl:Restriction cardinality blocks and\n")
	b.WriteString("-- we do not fabricate it. Run AFTER cim_reference_classes.sql (FK to it).\n")
	fmt.Fprintf(&b, "-- %d properties, source %s\n\n", len(attrs), sourceLabel)
	b.WriteString("DELETE FROM metadata.cim_ref_attributes;\n\n")

	cols := "INSERT INTO metadata.cim_ref_attributes (class_name, name, is_association, range, multiplicity, description, source) VALUES\n"
	for start := 0; start < len(attrs); start += rowsPerInsert {
		end := start + rowsPerInsert
		if end > len(attrs) {
			end = len(attrs)
		}
		b.WriteString(cols)
		for i := start; i < end; i++ {
			a := attrs[i]
			fmt.Fprintf(&b, "  (%s, %s, %t, %s, NULL, %s, %s)",
				sqlStr(a.Class), sqlStr(a.Name), a.IsAssociation,
				sqlStr(a.Range), sqlStr(a.Description), sqlStr(sourceLabel))
			if i < end-1 {
				b.WriteString(",\n")
			} else {
				b.WriteString(";\n\n")
			}
		}
	}
	return b.String()
}
