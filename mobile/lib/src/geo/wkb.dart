import 'dart:typed_data';

/// A minimal OGC WKB reader for the geometries the pack ships in features.geom.
///
/// The pack stores ST_AsBinary(geom) -- OGC WKB, no SRID (the SRID lives per
/// class in the metadata table). This parser also tolerates PostGIS EWKB (the
/// high-bit Z/M/SRID flags) and ISO Z/M (type + 1000/2000/3000) defensively, in
/// case a class column ever carries 3D. Coordinates are surfaced GeoJSON-style so
/// Slice 2b (MapLibre) can reuse the same output.
class WkbGeometry {
  WkbGeometry(this.type, this.coordinates);

  /// Build one from a GeoJSON geometry object. The reader already surfaces
  /// coordinates GeoJSON-style, so a plain geometry is a direct wrap; this lets a
  /// feature CREATED offline (whose geometry is GeoJSON, not packed WKB) reuse the
  /// exact same highlight/overlay path as a packed feature. GeometryCollection is
  /// the one case whose members are sub-geometries rather than numbers.
  factory WkbGeometry.fromGeoJson(Map<String, dynamic> json) {
    final type = (json['type'] as String?) ?? '';
    if (type == 'GeometryCollection') {
      final members = <WkbGeometry>[
        for (final g in (json['geometries'] as List? ?? const []))
          if (g is Map<String, dynamic>) WkbGeometry.fromGeoJson(g),
      ];
      return WkbGeometry(type, members);
    }
    return WkbGeometry(type, json['coordinates']);
  }

  /// GeoJSON-style type: Point, LineString, Polygon, MultiPoint,
  /// MultiLineString, MultiPolygon, GeometryCollection.
  final String type;

  /// GeoJSON-style nested coordinates:
  ///   Point           -> [x, y]
  ///   LineString      -> [[x, y], ...]
  ///   Polygon         -> [[[x, y], ...], ...]        (rings)
  ///   Multi*          -> one more level of nesting
  ///   GeometryCollection -> List<WkbGeometry>
  final dynamic coordinates;

  /// Flattened list of every [x, y] position, for summaries / bounds.
  List<List<double>> positions() {
    final out = <List<double>>[];
    void walk(dynamic node) {
      if (node is WkbGeometry) {
        walk(node.coordinates);
      } else if (node is List) {
        if (node.isNotEmpty && node.first is num) {
          out.add([(node[0] as num).toDouble(), (node[1] as num).toDouble()]);
        } else {
          for (final child in node) {
            walk(child);
          }
        }
      }
    }

    walk(coordinates);
    return out;
  }

  static WkbGeometry parse(Uint8List bytes) => _WkbReader(bytes).readGeometry();
}

const _typeNames = {
  1: 'Point',
  2: 'LineString',
  3: 'Polygon',
  4: 'MultiPoint',
  5: 'MultiLineString',
  6: 'MultiPolygon',
  7: 'GeometryCollection',
};

class _WkbReader {
  _WkbReader(Uint8List bytes) : _data = ByteData.sublistView(bytes);

  final ByteData _data;
  int _offset = 0;
  Endian _endian = Endian.little;

  int _readByte() {
    final v = _data.getUint8(_offset);
    _offset += 1;
    return v;
  }

  int _readUint32() {
    final v = _data.getUint32(_offset, _endian);
    _offset += 4;
    return v;
  }

  double _readDouble() {
    final v = _data.getFloat64(_offset, _endian);
    _offset += 8;
    return v;
  }

  WkbGeometry readGeometry() {
    _endian = _readByte() == 1 ? Endian.little : Endian.big;
    final raw = _readUint32();

    // EWKB high-bit flags (PostGIS). OGC WKB from ST_AsBinary won't set these.
    final hasZ = (raw & 0x80000000) != 0;
    final hasM = (raw & 0x40000000) != 0;
    final hasSrid = (raw & 0x20000000) != 0;
    if (hasSrid) _readUint32(); // srid -- ignored (metadata table is authoritative)

    var base = raw & 0x0fffffff;
    var dims = 2;
    if (hasZ) dims++;
    if (hasM) dims++;
    // ISO WKB Z/M encoding: type offset by 1000/2000/3000.
    if (base >= 3000) {
      base -= 3000;
      dims = 4;
    } else if (base >= 2000) {
      base -= 2000;
      dims = 3;
    } else if (base >= 1000) {
      base -= 1000;
      dims = 3;
    }

    final name = _typeNames[base] ?? 'Unknown';
    switch (base) {
      case 1:
        return WkbGeometry(name, _readPoint(dims));
      case 2:
        return WkbGeometry(name, _readLine(dims));
      case 3:
        return WkbGeometry(name, _readPolygon(dims));
      case 4:
        return WkbGeometry(name, _readMulti(dims, expectPoint: true));
      case 5:
      case 6:
        return WkbGeometry(name, _readMulti(dims, expectPoint: false));
      case 7:
        final n = _readUint32();
        return WkbGeometry(
          name,
          List<WkbGeometry>.generate(n, (_) => readGeometry()),
        );
      default:
        throw FormatException('unsupported WKB geometry type $base');
    }
  }

  List<double> _readPoint(int dims) {
    final x = _readDouble();
    final y = _readDouble();
    for (var i = 2; i < dims; i++) {
      _readDouble(); // skip Z / M
    }
    return [x, y];
  }

  List<List<double>> _readLine(int dims) {
    final n = _readUint32();
    return List<List<double>>.generate(n, (_) => _readPoint(dims));
  }

  List<List<List<double>>> _readPolygon(int dims) {
    final rings = _readUint32();
    return List<List<List<double>>>.generate(rings, (_) => _readLine(dims));
  }

  // A Multi* geometry: each member is a full sub-geometry (byte order + type
  // repeated), so recurse and take the coordinates of each.
  List<dynamic> _readMulti(int dims, {required bool expectPoint}) {
    final n = _readUint32();
    return List<dynamic>.generate(n, (_) => readGeometry().coordinates);
  }
}
