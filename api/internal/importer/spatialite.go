package importer

// SpatiaLite (.sqlite/.db) source support -- the second v2 SQLite-family format,
// after GeoPackage (geopackage.go). A SpatiaLite database is also an SQLite file,
// so it reuses the whole GeoPackage machinery -- the temp-file download, the
// sqliteReader, layer picking, the dry-run sample, and the deferred-decode design
// (a bad geometry is a per-row error in AssembleGeometry, not a scan abort). Only
// two things differ from GeoPackage and live here:
//
//  1. Layer metadata. SpatiaLite catalogs its feature tables in its OWN
//     geometry_columns table (f_table_name / f_geometry_column / geometry_type /
//     srid), NOT GeoPackage's gpkg_contents + gpkg_geometry_columns.
//
//  2. The geometry blob. SpatiaLite wraps geometry in its own BLOB format, not the
//     GeoPackage GPB. decodeSpatiaLiteBlob converts the classic (uncompressed)
//     SpatiaLite BLOB into the standard WKB the shared wkbToGeoJSON expects. The
//     compressed and TinyPoint variants are reported as a per-row error (still
//     non-fatal thanks to the deferred decode) rather than silently mis-read.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// listSpatiaLiteLayers returns a SpatiaLite database's feature tables from its
// geometry_columns catalog. geometry_type is CAST to text because SpatiaLite has
// stored it as either a numeric code (1=POINT, 2=LINESTRING, ... with 1000/2000/
// 3000 offsets for Z/M/ZM) or, in older schemas, a type name; spatialiteGeometryLabel
// normalizes either into a human label.
func listSpatiaLiteLayers(ctx context.Context, sdb *sql.DB) ([]GpkgLayer, error) {
	rows, err := sdb.QueryContext(ctx, `
SELECT f_table_name, f_geometry_column, CAST(geometry_type AS TEXT), srid
FROM geometry_columns
ORDER BY f_table_name`)
	if err != nil {
		return nil, invalidf("could not read SpatiaLite layer list -- is this a valid SpatiaLite database? (%v)", err)
	}
	defer rows.Close()

	var out []GpkgLayer
	for rows.Next() {
		var (
			l       GpkgLayer
			rawType string
		)
		if err := rows.Scan(&l.Name, &l.GeometryColumn, &rawType, &l.SRID); err != nil {
			return nil, fmt.Errorf("scanning SpatiaLite layer metadata: %w", err)
		}
		l.GeometryType = spatialiteGeometryLabel(rawType)
		out = append(out, l)
	}
	return out, rows.Err()
}

// spatialiteGeometryLabel turns a geometry_columns.geometry_type cell into a label.
// A numeric code maps to its base geometry name (dimension offsets stripped); a
// value that is already a name passes through unchanged.
func spatialiteGeometryLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	n, err := strconv.Atoi(raw)
	if err != nil {
		if raw == "" {
			return "GEOMETRY"
		}
		return raw
	}
	switch n % 1000 {
	case 1:
		return "POINT"
	case 2:
		return "LINESTRING"
	case 3:
		return "POLYGON"
	case 4:
		return "MULTIPOINT"
	case 5:
		return "MULTILINESTRING"
	case 6:
		return "MULTIPOLYGON"
	case 7:
		return "GEOMETRYCOLLECTION"
	default:
		return "GEOMETRY"
	}
}

// SpatiaLite BLOB markers (spatialite spec, gaiaGeom):
const (
	slMarkStart  = 0x00 // first byte of a classic BLOB
	slMarkMBR    = 0x7C // between the MBR and the geometry body
	slMarkEntity = 0x69 // before each element inside a multi/collection geometry
	slMarkEnd    = 0xFE // last byte of the BLOB
)

// decodeSpatiaLiteBlob converts a classic (uncompressed) SpatiaLite geometry BLOB
// into standard WKB, so the shared wkbToGeoJSON can round-trip it through PostGIS.
//
// Layout: 0x00 start | endian (0x00 big / 0x01 little) | srid int32 | MBR (4x
// float64) | 0x7C | body | 0xFE. The body is a WKB-like <int32 type><payload>,
// but inside a multi/collection each element is preceded by a 0x69 entity marker
// (where standard WKB has a per-geometry byte-order byte). We rewrite the body as
// standard WKB, copying coordinate runs verbatim (classic BLOB coordinates are
// plain IEEE-754 doubles in the declared byte order, identical to WKB) and swapping
// each 0x69 marker for the WKB byte-order byte.
func decodeSpatiaLiteBlob(blob []byte) (wkb []byte, srid int, empty bool, err error) {
	// Minimum classic BLOB: 39-byte header + 4-byte type + 1-byte end marker.
	if len(blob) < 44 {
		return nil, 0, false, fmt.Errorf("SpatiaLite geometry blob too short (%d bytes)", len(blob))
	}
	if blob[0] != slMarkStart {
		// TinyPoint BLOBs start with a different marker (0x80); nothing else is
		// classic. Report clearly rather than mis-decoding.
		return nil, 0, false, fmt.Errorf("unsupported SpatiaLite geometry blob (start byte 0x%02X; only the classic format is supported)", blob[0])
	}
	if blob[len(blob)-1] != slMarkEnd {
		return nil, 0, false, fmt.Errorf("SpatiaLite geometry blob missing end marker")
	}

	var order binary.ByteOrder
	switch blob[1] {
	case 0x00:
		order = binary.BigEndian
	case 0x01:
		order = binary.LittleEndian
	default:
		return nil, 0, false, fmt.Errorf("invalid SpatiaLite endianness byte 0x%02X", blob[1])
	}
	srid = int(int32(order.Uint32(blob[2:6])))
	if blob[38] != slMarkMBR {
		return nil, 0, false, fmt.Errorf("SpatiaLite geometry blob missing MBR marker")
	}

	r := &slBlobReader{b: blob, pos: 39, order: order}
	var out bytes.Buffer
	if err := r.geometry(&out, false); err != nil {
		return nil, 0, false, err
	}
	return out.Bytes(), srid, false, nil
}

// slBlobReader walks a SpatiaLite BLOB body, emitting standard WKB.
type slBlobReader struct {
	b     []byte
	pos   int
	order binary.ByteOrder
}

func (r *slBlobReader) need(n int) error {
	if n < 0 || r.pos+n > len(r.b) {
		return fmt.Errorf("SpatiaLite geometry blob truncated")
	}
	return nil
}

// u32 reads (and consumes) the next uint32 in the BLOB's byte order.
func (r *slBlobReader) u32() (uint32, error) {
	if err := r.need(4); err != nil {
		return 0, err
	}
	v := r.order.Uint32(r.b[r.pos : r.pos+4])
	r.pos += 4
	return v, nil
}

// wkbOrderByte is the WKB byte-order flag matching the BLOB's endianness, so the
// coordinate bytes we copy verbatim stay consistent with the header we emit.
func (r *slBlobReader) wkbOrderByte() byte {
	if r.order == binary.LittleEndian {
		return 1
	}
	return 0
}

// copyCoords copies numPoints * dims IEEE-754 doubles from the BLOB straight into
// the WKB output (same byte order, so no re-encode).
func (r *slBlobReader) copyCoords(out *bytes.Buffer, numPoints, dims int) error {
	n := numPoints * dims * 8
	if err := r.need(n); err != nil {
		return err
	}
	out.Write(r.b[r.pos : r.pos+n])
	r.pos += n
	return nil
}

// geometry parses one geometry from the current position and appends its WKB. When
// entity is true (an element inside a multi/collection), it first consumes the
// 0x69 entity marker that SpatiaLite writes before each element.
func (r *slBlobReader) geometry(out *bytes.Buffer, entity bool) error {
	if entity {
		if err := r.need(1); err != nil {
			return err
		}
		if r.b[r.pos] != slMarkEntity {
			return fmt.Errorf("SpatiaLite geometry: expected entity marker, got 0x%02X", r.b[r.pos])
		}
		r.pos++
	}

	typ, err := r.u32()
	if err != nil {
		return err
	}
	if typ >= 1_000_000 {
		return fmt.Errorf("compressed SpatiaLite geometry is not supported")
	}
	base, dims := classifySpatiaLiteType(typ)
	if base < 1 || base > 7 {
		return fmt.Errorf("unknown SpatiaLite geometry type %d", typ)
	}

	// WKB header: byte-order flag + geometry type (ISO codes match SpatiaLite's,
	// so the value is copied through unchanged).
	out.WriteByte(r.wkbOrderByte())
	putSpatiaLiteU32(out, r.order, typ)

	switch base {
	case 1: // Point
		return r.copyCoords(out, 1, dims)
	case 2: // LineString
		n, err := r.u32()
		if err != nil {
			return err
		}
		putSpatiaLiteU32(out, r.order, n)
		return r.copyCoords(out, int(n), dims)
	case 3: // Polygon
		nrings, err := r.u32()
		if err != nil {
			return err
		}
		putSpatiaLiteU32(out, r.order, nrings)
		for i := uint32(0); i < nrings; i++ {
			np, err := r.u32()
			if err != nil {
				return err
			}
			putSpatiaLiteU32(out, r.order, np)
			if err := r.copyCoords(out, int(np), dims); err != nil {
				return err
			}
		}
		return nil
	default: // 4,5,6 Multi* and 7 GeometryCollection
		nitems, err := r.u32()
		if err != nil {
			return err
		}
		putSpatiaLiteU32(out, r.order, nitems)
		for i := uint32(0); i < nitems; i++ {
			if err := r.geometry(out, true); err != nil {
				return err
			}
		}
		return nil
	}
}

// classifySpatiaLiteType splits a SpatiaLite/ISO geometry-type code into its base
// kind (1..7) and coordinate dimension count (2 for XY, 3 for XYZ/XYM, 4 for XYZM).
func classifySpatiaLiteType(typ uint32) (base, dims int) {
	switch {
	case typ >= 3000:
		return int(typ - 3000), 4
	case typ >= 2000:
		return int(typ - 2000), 3
	case typ >= 1000:
		return int(typ - 1000), 3
	default:
		return int(typ), 2
	}
}

// putSpatiaLiteU32 appends a uint32 in the given byte order.
func putSpatiaLiteU32(out *bytes.Buffer, order binary.ByteOrder, v uint32) {
	var buf [4]byte
	order.PutUint32(buf[:], v)
	out.Write(buf[:])
}
