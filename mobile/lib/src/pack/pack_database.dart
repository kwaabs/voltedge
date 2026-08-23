import 'dart:convert';
import 'dart:typed_data';

import 'package:sqlite3/sqlite3.dart';

import '../geo/wkb.dart';

/// A count of features in one class within the pack.
class ClassCount {
  const ClassCount(this.code, this.name, this.count);
  final String code;
  final String? name; // display name from the metadata table
  final int count;
}

/// One class's shape, as the map style builder needs it. geometryType is the
/// server's 'point' | 'line' | 'polygon' (never 'none' -- a geometryless class is
/// never packed, see internal/fieldpack/builder.go).
class ClassMeta {
  const ClassMeta(this.code, this.name, this.geometryType);
  final String code;
  final String? name;
  final String geometryType;
}

/// One feature row read from the pack. geom is decoded lazily via [geometry].
class FeatureRecord {
  const FeatureRecord({
    required this.id,
    required this.featureClass,
    required this.name,
    required this.status,
    required this.subtype,
    required this.territoryId,
    required this.geomBytes,
    required this.attributes,
  });

  final String id;
  final String featureClass;
  final String? name;
  final String? status;
  final int? subtype;
  final String? territoryId;
  final Uint8List? geomBytes;
  final Map<String, dynamic> attributes;

  /// Decode the OGC WKB geometry on demand. Null when the row has no geometry
  /// or the bytes fail to parse (surfaced to the caller rather than thrown).
  WkbGeometry? geometry() {
    final bytes = geomBytes;
    if (bytes == null || bytes.isEmpty) return null;
    try {
      return WkbGeometry.parse(bytes);
    } catch (_) {
      return null;
    }
  }
}

/// PackDatabase opens a downloaded data.sqlite pack read-only and answers the
/// queries the browse UI needs. The pack is server-built with the by-id and
/// by-class indexes already in the file (see internal/fieldpack/builder.go), so
/// every lookup here is index-backed -- no 100k scans.
class PackDatabase {
  PackDatabase._(this._db);

  final Database _db;

  factory PackDatabase.open(String path) =>
      PackDatabase._(sqlite3.open(path, mode: OpenMode.readOnly));

  /// The manifest key/value table as a flat map.
  Map<String, String> manifest() {
    final rows = _db.select('SELECT key, value FROM manifest');
    return {
      for (final r in rows) r['key'] as String: r['value'] as String,
    };
  }

  /// Per-class feature counts, joined to the metadata display name, ordered by
  /// class code.
  List<ClassCount> classCounts() {
    final rows = _db.select('''
      SELECT f.feature_class AS code, m.name AS name, COUNT(*) AS n
      FROM features f
      LEFT JOIN metadata m ON m.code = f.feature_class
      GROUP BY f.feature_class, m.name
      ORDER BY f.feature_class
    ''');
    return [
      for (final r in rows)
        ClassCount(
          r['code'] as String,
          r['name'] as String?,
          (r['n'] as int),
        ),
    ];
  }

  /// Every class in the pack with its geometry type -- one MapLibre style layer
  /// per row. Ordered so polygons draw under lines under points is decided by the
  /// style builder, not here; this is just the class inventory.
  List<ClassMeta> classMetas() {
    final rows = _db.select('''
      SELECT code, name, geometry_type FROM metadata ORDER BY code
    ''');
    return [
      for (final r in rows)
        ClassMeta(
          r['code'] as String,
          r['name'] as String?,
          (r['geometry_type'] as String?) ?? 'point',
        ),
    ];
  }

  /// A page of features for one class, ordered by id. limit/offset page the
  /// list so even a 100k-class pack renders instantly.
  List<FeatureRecord> featuresOfClass(
    String code, {
    int limit = 200,
    int offset = 0,
  }) {
    final rows = _db.select(
      '''
      SELECT id, feature_class, name, status, subtype, territory_id, geom, attrs
      FROM features
      WHERE feature_class = ?
      ORDER BY id
      LIMIT ? OFFSET ?
      ''',
      [code, limit, offset],
    );
    return [for (final r in rows) _toRecord(r)];
  }

  /// One feature by id (the tap-select lookup Slice 2b feeds from the map).
  FeatureRecord? featureById(String id) {
    final rows = _db.select(
      '''
      SELECT id, feature_class, name, status, subtype, territory_id, geom, attrs
      FROM features WHERE id = ?
      ''',
      [id],
    );
    if (rows.isEmpty) return null;
    return _toRecord(rows.first);
  }

  /// Rows in a table (for the manifest/detail summaries). Cheap COUNT(*).
  int countTable(String table) {
    final rows = _db.select('SELECT COUNT(*) AS n FROM $table');
    return rows.first['n'] as int;
  }

  FeatureRecord _toRecord(Row r) {
    final attrsText = r['attrs'] as String?;
    Map<String, dynamic> attrs = const {};
    if (attrsText != null && attrsText.isNotEmpty) {
      final decoded = jsonDecode(attrsText);
      if (decoded is Map<String, dynamic>) attrs = decoded;
    }
    final geom = r['geom'];
    return FeatureRecord(
      id: r['id'] as String,
      featureClass: r['feature_class'] as String,
      name: r['name'] as String?,
      status: r['status'] as String?,
      subtype: r['subtype'] as int?,
      territoryId: r['territory_id'] as String?,
      geomBytes: geom is Uint8List ? geom : null,
      attributes: attrs,
    );
  }

  void close() => _db.dispose();
}
