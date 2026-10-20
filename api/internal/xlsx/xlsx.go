// Package xlsx is a tiny, dependency-free writer for multi-sheet .xlsx
// (OOXML SpreadsheetML) workbooks. It exists because the repo carries no
// spreadsheet library and the asset register wants real Excel tabs (one sheet
// per feature class) rather than a flat CSV. It covers exactly what a tabular
// export needs -- inline strings and numbers, one sheet per class, A1 cell refs
// -- and nothing else (no styles, formulas, merges or shared-string table).
//
// The emitted package is the minimal set of parts Excel and LibreOffice accept:
// [Content_Types].xml, _rels/.rels, xl/workbook.xml, xl/_rels/workbook.xml.rels
// and one xl/worksheets/sheetN.xml per sheet, zipped together.
package xlsx

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Cell is one cell: either a string (the default) or a number (IsNum). A cell is
// built with Str or Num; the zero Cell is an empty string.
type Cell struct {
	S     string
	N     float64
	IsNum bool
}

// Str returns a text cell.
func Str(s string) Cell { return Cell{S: s} }

// Num returns a numeric cell.
func Num(n float64) Cell { return Cell{N: n, IsNum: true} }

// Sheet is one worksheet: a name and its rows (each a slice of cells).
type Sheet struct {
	name string
	rows [][]Cell
}

// AppendRow adds one row of cells to the sheet.
func (s *Sheet) AppendRow(cells []Cell) { s.rows = append(s.rows, cells) }

// Workbook is a collection of sheets, written out as a single .xlsx byte stream.
type Workbook struct {
	sheets []*Sheet
	used   map[string]bool
}

// New returns an empty workbook.
func New() *Workbook { return &Workbook{used: map[string]bool{}} }

// AddSheet appends a new sheet, sanitising the name to Excel's rules (<=31
// chars, none of []:*?/\, non-empty) and making it unique within the workbook.
func (wb *Workbook) AddSheet(name string) *Sheet {
	s := &Sheet{name: wb.uniqueName(sanitiseSheetName(name))}
	wb.sheets = append(wb.sheets, s)
	return s
}

// Bytes renders the workbook to a .xlsx byte stream.
func (wb *Workbook) Bytes() ([]byte, error) {
	// A workbook with no sheets is invalid; give it one empty sheet so the file
	// still opens rather than erroring.
	if len(wb.sheets) == 0 {
		wb.AddSheet("Sheet1")
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	add := func(name, content string) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write([]byte(content))
		return err
	}

	if err := add("[Content_Types].xml", wb.contentTypes()); err != nil {
		return nil, err
	}
	if err := add("_rels/.rels", rootRels); err != nil {
		return nil, err
	}
	if err := add("xl/workbook.xml", wb.workbookXML()); err != nil {
		return nil, err
	}
	if err := add("xl/_rels/workbook.xml.rels", wb.workbookRels()); err != nil {
		return nil, err
	}
	for i, s := range wb.sheets {
		if err := add(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1), sheetXML(s)); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (wb *Workbook) contentTypes() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	b.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	b.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	b.WriteString(`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`)
	for i := range wb.sheets {
		fmt.Fprintf(&b, `<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i+1)
	}
	b.WriteString(`</Types>`)
	return b.String()
}

const rootRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
	`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
	`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
	`</Relationships>`

func (wb *Workbook) workbookXML() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">`)
	b.WriteString(`<sheets>`)
	for i, s := range wb.sheets {
		fmt.Fprintf(&b, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, escapeAttr(s.name), i+1, i+1)
	}
	b.WriteString(`</sheets></workbook>`)
	return b.String()
}

func (wb *Workbook) workbookRels() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)
	for i := range wb.sheets {
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i+1, i+1)
	}
	b.WriteString(`</Relationships>`)
	return b.String()
}

func sheetXML(s *Sheet) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for r, row := range s.rows {
		fmt.Fprintf(&b, `<row r="%d">`, r+1)
		for c, cell := range row {
			ref := colLetter(c) + strconv.Itoa(r+1)
			if cell.IsNum {
				fmt.Fprintf(&b, `<c r="%s"><v>%s</v></c>`, ref, strconv.FormatFloat(cell.N, 'f', -1, 64))
			} else if cell.S == "" {
				// An empty string cell carries no value element.
				fmt.Fprintf(&b, `<c r="%s"/>`, ref)
			} else {
				fmt.Fprintf(&b, `<c r="%s" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, ref, escapeText(cell.S))
			}
		}
		b.WriteString(`</row>`)
	}
	b.WriteString(`</sheetData></worksheet>`)
	return b.String()
}

// colLetter renders a 0-based column index as an Excel column label (0->A,
// 25->Z, 26->AA).
func colLetter(i int) string {
	s := ""
	i++
	for i > 0 {
		i--
		s = string(rune('A'+i%26)) + s
		i /= 26
	}
	return s
}

func (wb *Workbook) uniqueName(name string) string {
	key := strings.ToLower(name)
	if !wb.used[key] {
		wb.used[key] = true
		return name
	}
	// Append " (n)" within the 31-char limit until unique.
	for n := 2; ; n++ {
		suffix := fmt.Sprintf(" (%d)", n)
		trimmed := name
		if len(trimmed)+len(suffix) > 31 {
			trimmed = trimmed[:31-len(suffix)]
		}
		cand := trimmed + suffix
		if ck := strings.ToLower(cand); !wb.used[ck] {
			wb.used[ck] = true
			return cand
		}
	}
}

// sanitiseSheetName strips the characters Excel forbids in a sheet name and
// bounds it to 31 characters, defaulting an empty result to "Sheet".
func sanitiseSheetName(name string) string {
	repl := func(r rune) rune {
		switch r {
		case '[', ']', ':', '*', '?', '/', '\\':
			return ' '
		}
		return r
	}
	name = strings.TrimSpace(strings.Map(repl, name))
	if name == "" {
		name = "Sheet"
	}
	if len(name) > 31 {
		name = strings.TrimSpace(name[:31])
	}
	return name
}

// escapeText escapes a string for XML character data, dropping control
// characters XML 1.0 forbids (external_ref and free-text fields can carry them).
func escapeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 {
				b.WriteRune(r)
			}
			// else: drop a forbidden control char
		}
	}
	return b.String()
}

// escapeAttr escapes a string for an XML attribute value (sheet names).
func escapeAttr(s string) string {
	s = escapeText(s)
	return strings.ReplaceAll(s, `"`, "&quot;")
}
