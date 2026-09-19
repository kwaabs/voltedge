import 'dart:convert';
import 'dart:typed_data';

import 'package:sqlite3/sqlite3.dart';

import '../faults/faults_models.dart';
import '../geo/wkb.dart';
import '../wam/wam_models.dart';

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

/// A link from one feature to a neighbour in the pack -- either a connectivity
/// edge or a relationship. Carries just enough to render a tappable row and,
/// on tap, look the neighbour up by [featureId] and select it on the map.
class NeighborLink {
  const NeighborLink({
    required this.kind,
    required this.featureClass,
    required this.featureId,
    this.name,
    this.detail,
  });

  /// 'connectivity' or 'relationship' -- decides which section it renders under.
  final String kind;
  final String featureClass;
  final String featureId;

  /// The neighbour's display name, if it has one (LEFT JOINed from features).
  final String? name;

  /// A short qualifier: the terminal for a connection, or the relationship
  /// class for a relationship. Shown as secondary text.
  final String? detail;
}

/// One field's definition for a class, parsed from the pack's `metadata.fields`
/// JSON (the server-side []metadata.Field, marshalled with Go's default
/// capitalized keys -- no json tags on that struct). Just enough for the offline
/// edit form (Slice 3a) to render the right control and decide editability.
class FieldDef {
  const FieldDef({
    required this.name,
    required this.dataType,
    required this.nullable,
    required this.isComputed,
    required this.visibleOnCreate,
    required this.visibleOnUpdate,
    required this.editableAfterPublish,
    this.domainId,
    this.requiredForSubtypes = const [],
  });

  final String name;

  /// Server field type: text | numeric | integer | boolean | date | timestamp |
  /// uuid | jsonb (see api/internal/admin/types.go). Drives the input control.
  final String dataType;
  final bool nullable;
  final bool isComputed;

  /// Whether the field is offered on the CREATE form (the create slice's analog
  /// of visibleOnUpdate for the editor). Present in every pack's blob -- the
  /// builder marshals the whole metadata.Field, which has always carried it.
  final bool visibleOnCreate;
  final bool visibleOnUpdate;
  final bool editableAfterPublish;

  /// The coded domain this field draws from, if any. Pack schema v2+ also ships
  /// the domain's VALUES (see `domain_values`), so the editor renders a dropdown;
  /// on an older pack with no values the field falls back to a plain input.
  final String? domainId;

  /// The subtype codes for which this field is required (pack schema v3+, from
  /// the server's subtype_field_rules). The edit form marks a field required --
  /// and blocks Save on it -- when the feature's subtype is in this list. Empty
  /// on an older pack, so requiredness simply isn't enforced offline there.
  final List<int> requiredForSubtypes;
}

/// One allowed value of a coded domain, from the pack's `domain_values` table
/// (schema v2+). [code] is what's stored on the feature; [label] is what the
/// dropdown shows.
class DomainValue {
  const DomainValue({required this.code, required this.label});
  final String code;
  final String label;
}

/// One subtype of a class, from the pack's `metadata.subtypes` blob (Go-marshalled
/// []metadata.Subtype, capitalized keys). [code] is stored on the feature; [name]
/// is shown in the create form's subtype picker.
class SubtypeDef {
  const SubtypeDef({required this.code, required this.name});
  final int code;
  final String name;
}

/// One named connection point on a junction class, from the pack's
/// `metadata.terminals` blob (Go-marshalled []metadata.Terminal, capitalized keys)
/// -- e.g. a DIST_XFMR's T1/T2/T3. [code] is what an establish_connection op sends
/// as its endpoint `terminal` (the server resolves it via Loaded.ResolveTerminal,
/// which matches on Code); [role] is the human label shown in the picker.
/// [subtypeCode] is null for a terminal that applies to every subtype of the class,
/// or the specific subtype code it's scoped to (Phase 21 Slice 13).
class TerminalDef {
  const TerminalDef({
    required this.code,
    required this.role,
    required this.phaseCapable,
    this.subtypeCode,
  });

  final String code;
  final String role;
  final bool phaseCapable;
  final int? subtypeCode;

  /// "T1 — Primary" style label for the terminal picker; just the code when the
  /// terminal has no role text.
  String get label => role.isNotEmpty ? '$code — $role' : code;
}

/// The resolved style of one symbology rule (the pack's `symbology.style_def`
/// JSON -- a direct port of web/src/lib/symbology.ts's StyleDef). icon_ref/color/
/// scale apply to a point, width/dash to a line (plus color, shared), fill_color/
/// stroke_color to a polygon; not every field is relevant to every geometry type.
class StyleDef {
  const StyleDef({
    this.color,
    this.scale,
    this.width,
    this.dash,
    this.fillColor,
    this.strokeColor,
    this.iconRef,
  });

  final String? color;
  final double? scale;
  final double? width;
  final String? dash;
  final String? fillColor;
  final String? strokeColor;
  final String? iconRef;

  factory StyleDef.fromJson(Map<String, dynamic> j) => StyleDef(
        color: j['color'] as String?,
        scale: (j['scale'] as num?)?.toDouble(),
        width: (j['width'] as num?)?.toDouble(),
        dash: j['dash'] as String?,
        fillColor: j['fill_color'] as String?,
        strokeColor: j['stroke_color'] as String?,
        iconRef: j['icon_ref'] as String?,
      );
}

/// One map-context symbology rule for a class, from the pack's `symbology` table
/// (pack schema v4+). A port of web/src/lib/symbology.ts's SymbologyRuleSchema:
/// [subtypeCode] and [domainField]+[domainValueCode] are the match axes (either or
/// both null; the class-level default rule has both null), and [specificity]
/// mirrors the web's most-specific-match-wins order. min/max zoom are carried but
/// not yet enforced on-device (MapLibre can't zoom-gate a data-driven branch).
class SymbologyRule {
  const SymbologyRule({
    this.subtypeCode,
    this.domainField,
    this.domainValueCode,
    this.minZoom,
    this.maxZoom,
    required this.style,
  });

  final int? subtypeCode;
  final String? domainField;
  final String? domainValueCode;
  final int? minZoom;
  final int? maxZoom;
  final StyleDef style;

  /// (subtype + domain_value) > subtype alone > domain_value alone > neither,
  /// exactly as web/src/lib/symbology.ts's specificity() scores it.
  int get specificity =>
      (subtypeCode != null ? 2 : 0) + (domainField != null ? 1 : 0);
}

/// One image attachment carried inside the pack (pack schema v6+, from the
/// `attachments` table). [thumb] is the server-generated JPEG the device renders
/// offline; it's null when the image committed without a thumbnail (the UI shows
/// a placeholder) or on a pre-v6 pack. The full-resolution original never rides
/// the pack -- it's fetched on demand via a presigned URL when online.
class PackAttachment {
  const PackAttachment({
    required this.id,
    required this.featureClass,
    required this.featureId,
    required this.filename,
    required this.contentType,
    required this.byteSize,
    this.thumb,
  });

  final String id;
  final String featureClass;
  final String featureId;
  final String? filename;
  final String? contentType;
  final int byteSize;
  final Uint8List? thumb;
}

/// One relationship-class definition from the pack's `relationship_classes` table
/// (pack schema v7+): a legal (origin, destination) class pairing with its [kind]
/// (spatial_support | containment | composite) and [cardinality]. The offline
/// relate flow reads these to offer a KIND picker when a pair allows more than one
/// -- mirroring the server's directional origin->destination match (no swap).
class RelationshipClassDef {
  const RelationshipClassDef({
    required this.originClass,
    required this.destinationClass,
    required this.kind,
    required this.cardinality,
    this.name,
  });

  final String originClass;
  final String destinationClass;
  final String kind;
  final String cardinality;
  final String? name;

  /// A human label for the KIND picker: the class's own name when it has one,
  /// else the kind slug prettified ("spatial_support" -> "Spatial support").
  String get label {
    final n = name;
    if (n != null && n.isNotEmpty) return n;
    if (kind.isEmpty) return kind;
    final spaced = kind.replaceAll('_', ' ');
    return spaced[0].toUpperCase() + spaced.substring(1);
  }
}

/// One connectivity rule from the pack's `network_rules` table (pack schema v7+):
/// a (from, to) class pairing -- optionally narrowed by subtype/terminal on either
/// end -- flagged [isValid] or not. A null subtype/terminal is a wildcard. The
/// offline connect flow matches these most-specific-first and ENFORCES the
/// verdict -- an edge the rules reject is blocked whenever the pack ships rules;
/// the server re-validates on sync regardless. A port of the server's
/// api/internal/connectivity match, minus the phase gate (deferred).
class NetworkRuleDef {
  const NetworkRuleDef({
    required this.fromClass,
    required this.toClass,
    required this.isValid,
    this.fromSubtype,
    this.fromTerminal,
    this.toSubtype,
    this.toTerminal,
    this.phaseRule,
    this.name,
  });

  final String fromClass;
  final String toClass;
  final bool isValid;
  final int? fromSubtype;
  final String? fromTerminal;
  final int? toSubtype;
  final String? toTerminal;
  final String? phaseRule;
  final String? name;
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
    this.geometryOverride,
  });

  final String id;
  final String featureClass;
  final String? name;
  final String? status;
  final int? subtype;
  final String? territoryId;
  final Uint8List? geomBytes;
  final Map<String, dynamic> attributes;

  /// A pre-decoded geometry, set for a feature CREATED offline (whose geometry is
  /// GeoJSON, never packed WKB in [geomBytes]). When present it wins over [geomBytes]
  /// so the map highlight/overlay treats a create exactly like a packed feature.
  final WkbGeometry? geometryOverride;

  /// Decode the OGC WKB geometry on demand. Null when the row has no geometry
  /// or the bytes fail to parse (surfaced to the caller rather than thrown).
  WkbGeometry? geometry() {
    if (geometryOverride != null) return geometryOverride;
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

  // Cached "does this pack ship any rules" answers -- they can't change while a
  // pack is open, and the connect/relate flows ask on every target tap.
  bool? _packsNetworkRules;
  bool? _packsRelationshipClasses;

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

  /// The `network_role` of a class from the packed metadata -- one of 'edge',
  /// 'junction', 'container', 'none'. Only `edge` (connects to junctions) and
  /// `junction` (carries terminals) participate in electrical connectivity;
  /// `container` hosts a diagram and `none` is fully outside the network model
  /// (e.g. a POLE). The map gates its Connect action on this so a non-electrical
  /// feature no longer offers a connection the server rules would reject.
  ///
  /// Returns null when the class isn't in the pack, or when an older pack was
  /// built before the column existed (the query is wrapped so a missing column
  /// degrades to "unknown" rather than throwing) -- the caller keeps the button
  /// for an unknown role so an old pack isn't silently stripped of connectivity.
  String? networkRoleFor(String code) {
    try {
      final rows = _db.select(
          'SELECT network_role FROM metadata WHERE code = ?', [code]);
      if (rows.isEmpty) return null;
      final v = rows.first['network_role'] as String?;
      return (v == null || v.isEmpty) ? null : v;
    } catch (_) {
      return null;
    }
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

  /// The bounding box [minLon, minLat, maxLon, maxLat] of every feature's
  /// geometry in the pack, or null if the pack has no decodable geometry.
  ///
  /// The map frames to THIS, not the requested scope bbox: a pack built over a
  /// wide (or world) bbox would otherwise open zoomed out past where tiles exist
  /// (tippecanoe minzoom), showing a blank map with the data lost off-screen.
  /// Framing to the actual feature extent lands the camera where the tiles are.
  /// Geometries are decoded on the device (WKB has no queryable min/max), so this
  /// is an O(features) scan -- fine at open time; a manifest-recorded extent is a
  /// later optimization for very large packs.
  List<double>? featureExtent() {
    final rows = _db.select('SELECT geom FROM features');
    double? minX, minY, maxX, maxY;
    for (final r in rows) {
      final geom = r['geom'];
      if (geom is! Uint8List || geom.isEmpty) continue;
      WkbGeometry g;
      try {
        g = WkbGeometry.parse(geom);
      } catch (_) {
        continue;
      }
      for (final p in g.positions()) {
        final x = p[0], y = p[1];
        minX = (minX == null || x < minX) ? x : minX;
        minY = (minY == null || y < minY) ? y : minY;
        maxX = (maxX == null || x > maxX) ? x : maxX;
        maxY = (maxY == null || y > maxY) ? y : maxY;
      }
    }
    if (minX == null || minY == null || maxX == null || maxY == null) {
      return null;
    }
    return [minX, minY, maxX, maxY];
  }

  /// Connectivity neighbours of a feature: every connection with [id] on either
  /// end, resolved to the OTHER endpoint (class, id, its name, and the neighbour's
  /// terminal). Both edges are packed only when both endpoints are in the packed
  /// set (see internal/fieldpack/builder.go), so this is the pack's own view of
  /// connectivity, not the full server graph.
  List<NeighborLink> connectionsFor(String id) {
    final rows = _db.select(
      '''
      SELECT
        CASE WHEN c.from_id = ? THEN c.to_class    ELSE c.from_class    END AS other_class,
        CASE WHEN c.from_id = ? THEN c.to_id       ELSE c.from_id       END AS other_id,
        CASE WHEN c.from_id = ? THEN c.to_terminal ELSE c.from_terminal END AS other_terminal,
        f.name AS other_name
      FROM connectivity c
      LEFT JOIN features f
        ON f.id = (CASE WHEN c.from_id = ? THEN c.to_id ELSE c.from_id END)
      WHERE c.from_id = ? OR c.to_id = ?
      ''',
      [id, id, id, id, id, id],
    );
    return [
      for (final r in rows)
        NeighborLink(
          kind: 'connectivity',
          featureClass: (r['other_class'] as String?) ?? '',
          featureId: (r['other_id'] as String?) ?? '',
          name: r['other_name'] as String?,
          detail: (r['other_terminal'] as String?)?.isNotEmpty == true
              ? 'terminal ${r['other_terminal']}'
              : null,
        ),
    ];
  }

  /// Whether the pack's connectivity graph carries an edge between [a] and [b]
  /// (either direction). The fresh-pack reconcile (PackStore.reconcileSyncedEdits,
  /// Slice 13b) uses this to confirm a synced offline connection now rides the new
  /// snapshot before dropping its redundant local row.
  bool hasConnection(String a, String b) {
    final rows = _db.select(
      'SELECT 1 FROM connectivity WHERE (from_id = ? AND to_id = ?) '
      'OR (from_id = ? AND to_id = ?) LIMIT 1',
      [a, b, b, a],
    );
    return rows.isNotEmpty;
  }

  /// Whether the pack carries a relationship edge between [a] and [b] (either
  /// direction). The relationship sibling of [hasConnection]: the fresh-pack
  /// reconcile (PackStore.reconcileSyncedEdits, Slice 14) uses this to confirm a
  /// synced offline relationship now rides the new snapshot before dropping its
  /// redundant local row.
  bool hasRelationship(String a, String b) {
    final rows = _db.select(
      'SELECT 1 FROM relationships WHERE (origin_id = ? AND destination_id = ?) '
      'OR (origin_id = ? AND destination_id = ?) LIMIT 1',
      [a, b, b, a],
    );
    return rows.isNotEmpty;
  }

  /// Relationship neighbours of a feature: every relationship with [id] as origin
  /// or destination, resolved to the other end plus the relationship class id.
  List<NeighborLink> relationshipsFor(String id) {
    final rows = _db.select(
      '''
      SELECT
        CASE WHEN r.origin_id = ? THEN r.destination_class ELSE r.origin_class END AS other_class,
        CASE WHEN r.origin_id = ? THEN r.destination_id    ELSE r.origin_id    END AS other_id,
        r.relationship_class_id AS rel_class,
        f.name AS other_name
      FROM relationships r
      LEFT JOIN features f
        ON f.id = (CASE WHEN r.origin_id = ? THEN r.destination_id ELSE r.origin_id END)
      WHERE r.origin_id = ? OR r.destination_id = ?
      ''',
      [id, id, id, id, id],
    );
    return [
      for (final r in rows)
        NeighborLink(
          kind: 'relationship',
          featureClass: (r['other_class'] as String?) ?? '',
          featureId: (r['other_id'] as String?) ?? '',
          name: r['other_name'] as String?,
          detail: (r['rel_class'] as String?)?.isNotEmpty == true
              ? 'class ${r['rel_class']}'
              : null,
        ),
    ];
  }

  /// The active field definitions for a class, from the packed `metadata.fields`
  /// blob. Used by the offline edit form (Slice 3a) to know each field's type and
  /// whether it's editable. The pack only carries fields the class actually has,
  /// so an unknown class code yields an empty list.
  List<FieldDef> fieldsFor(String code) {
    final rows =
        _db.select('SELECT fields FROM metadata WHERE code = ?', [code]);
    if (rows.isEmpty) return const [];
    final raw = rows.first['fields'] as String?;
    if (raw == null || raw.isEmpty) return const [];
    final decoded = jsonDecode(raw);
    if (decoded is! List) return const [];
    final out = <FieldDef>[];
    for (final f in decoded) {
      if (f is! Map) continue;
      final name = (f['Name'] as String?) ?? '';
      if (name.isEmpty) continue;
      out.add(FieldDef(
        name: name,
        dataType: (f['DataType'] as String?) ?? 'text',
        nullable: f['Nullable'] == true,
        isComputed: f['IsComputed'] == true,
        visibleOnCreate: f['VisibleOnCreate'] == true,
        visibleOnUpdate: f['VisibleOnUpdate'] == true,
        editableAfterPublish: f['EditableAfterPublish'] == true,
        domainId: f['DomainID'] as String?,
        requiredForSubtypes: [
          for (final c in (f['RequiredForSubtypes'] as List? ?? const []))
            if (c is num) c.toInt(),
        ],
      ));
    }
    return out;
  }

  /// The subtypes of a class, from the packed `metadata.subtypes` blob (ordered
  /// by code). Empty for a class with no subtypes -- the create form then skips
  /// the subtype picker. Keys are Go-capitalized (Code/Name), same as the fields
  /// blob, because the builder marshals []metadata.Subtype with default tags.
  List<SubtypeDef> subtypesFor(String code) {
    final rows =
        _db.select('SELECT subtypes FROM metadata WHERE code = ?', [code]);
    if (rows.isEmpty) return const [];
    final raw = rows.first['subtypes'] as String?;
    if (raw == null || raw.isEmpty) return const [];
    final decoded = jsonDecode(raw);
    if (decoded is! List) return const [];
    final out = <SubtypeDef>[];
    for (final s in decoded) {
      if (s is! Map) continue;
      final c = s['Code'];
      if (c is! num) continue;
      out.add(SubtypeDef(
        code: c.toInt(),
        name: (s['Name'] as String?)?.isNotEmpty == true
            ? s['Name'] as String
            : 'Subtype ${c.toInt()}',
      ));
    }
    out.sort((a, b) => a.code.compareTo(b.code));
    return out;
  }

  /// The named terminals of a class, from the packed `metadata.terminals` blob,
  /// scoped to a feature's [subtypeCode] (Phase 21 Slice 13). A terminal with no
  /// SubtypeID applies to every subtype and is always returned; a subtype-scoped
  /// one is returned only when it matches [subtypeCode] (and never when the feature
  /// has no subtype, since we can't confirm it applies). Empty for a class with no
  /// terminals -- the connect flow then wires the endpoint with no terminal, which
  /// the server's wildcard-null rules accept. Keys are Go-capitalized because the
  /// builder marshals []metadata.Terminal with default tags; the SubtypeID -> code
  /// map is read from the same class's `subtypes` blob (terminals carry the UUID).
  List<TerminalDef> terminalsFor(String code, {int? subtypeCode}) {
    final rows = _db.select(
        'SELECT terminals, subtypes FROM metadata WHERE code = ?', [code]);
    if (rows.isEmpty) return const [];
    final rawT = rows.first['terminals'] as String?;
    if (rawT == null || rawT.isEmpty) return const [];
    final decodedT = jsonDecode(rawT);
    if (decodedT is! List) return const [];

    // Map subtype UUID -> code so a terminal's SubtypeID can be matched against the
    // feature's subtype code (the device never sees subtype UUIDs otherwise).
    final subtypeCodeById = <String, int>{};
    final rawS = rows.first['subtypes'] as String?;
    if (rawS != null && rawS.isNotEmpty) {
      final decodedS = jsonDecode(rawS);
      if (decodedS is List) {
        for (final s in decodedS) {
          if (s is Map && s['ID'] is String && s['Code'] is num) {
            subtypeCodeById[s['ID'] as String] = (s['Code'] as num).toInt();
          }
        }
      }
    }

    final out = <TerminalDef>[];
    for (final t in decodedT) {
      if (t is! Map) continue;
      final tCode = t['Code'];
      if (tCode is! String || tCode.isEmpty) continue;
      final subId = t['SubtypeID'] as String?;
      final tSubCode = subId == null ? null : subtypeCodeById[subId];
      if (subId != null && (subtypeCode == null || tSubCode != subtypeCode)) {
        continue; // subtype-scoped terminal that doesn't apply to this feature
      }
      out.add(TerminalDef(
        code: tCode,
        role: (t['Role'] as String?) ?? '',
        phaseCapable: t['PhaseCapable'] == true,
        subtypeCode: tSubCode,
      ));
    }
    out.sort((a, b) => a.code.compareTo(b.code));
    return out;
  }

  /// The relationship-class definitions for an (origin, destination) class pair
  /// from the pack's `relationship_classes` table (pack schema v7+), directional
  /// exactly as the server matches -- origin->destination, no swap. More than one
  /// row means the pair supports several KINDs and the relate flow should let the
  /// user pick. Returns empty for a pre-v7 pack with no table (the missing-table
  /// error is swallowed, same as [domainValuesFor]), so the flow skips the picker.
  List<RelationshipClassDef> relationshipClassesFor(
      String originClass, String destinationClass) {
    try {
      final rows = _db.select(
        '''
        SELECT origin_class, destination_class, kind, cardinality, name
        FROM relationship_classes
        WHERE origin_class = ? AND destination_class = ?
        ORDER BY kind
        ''',
        [originClass, destinationClass],
      );
      return [
        for (final r in rows)
          RelationshipClassDef(
            originClass: r['origin_class'] as String,
            destinationClass: r['destination_class'] as String,
            kind: (r['kind'] as String?) ?? '',
            cardinality: (r['cardinality'] as String?) ?? '',
            name: r['name'] as String?,
          ),
      ];
    } on SqliteException {
      return const [];
    }
  }

  /// Whether this pack ships ANY connectivity rules -- the `network_rules` table
  /// exists and holds at least one row (pack schema v7+ built with connectivity
  /// in scope). This is the enforce/allow switch for the offline connect flow:
  /// when true, the flow BLOCKS a connection the packed rules reject (zero trust,
  /// matching the server); when false there's nothing to judge by, so an edge is
  /// created optimistically and the server stays the sole authority. Cached.
  bool get packsNetworkRules =>
      _packsNetworkRules ??= _tableHasRows('network_rules');

  /// Whether this pack ships ANY relationship-class definitions -- the
  /// `relationship_classes` table exists and holds at least one row (v7+ with
  /// relationships in scope). The relate-flow sibling of [packsNetworkRules]:
  /// when true, a pair no packed class permits is refused offline; when false the
  /// relate flow stays optimistic and lets the server resolve/reject the class.
  bool get packsRelationshipClasses =>
      _packsRelationshipClasses ??= _tableHasRows('relationship_classes');

  /// Whether [table] exists in the pack and holds at least one row. A missing
  /// table (an older pack that predates it) or any read error is treated as "no
  /// rows", so an old pack degrades to the optimistic path rather than crashing.
  bool _tableHasRows(String table) {
    try {
      return _db.select('SELECT 1 FROM $table LIMIT 1').isNotEmpty;
    } on SqliteException {
      return false;
    }
  }

  /// The connectivity rules for a (from, to) class pair from the pack's
  /// `network_rules` table (pack schema v7+). Both orientations are returned --
  /// the server matches either direction (matchRuleEitherDirection), so the caller
  /// (network_rules.dart) picks the most specific across both. Returns empty for a
  /// pre-v7 pack with no table (missing-table error swallowed, same as
  /// [domainValuesFor]), so the connect flow simply skips the advisory check.
  List<NetworkRuleDef> networkRulesFor(String fromClass, String toClass) {
    try {
      final rows = _db.select(
        '''
        SELECT from_class, from_subtype, from_terminal,
               to_class, to_subtype, to_terminal, phase_rule, is_valid, name
        FROM network_rules
        WHERE (from_class = ? AND to_class = ?)
           OR (from_class = ? AND to_class = ?)
        ''',
        [fromClass, toClass, toClass, fromClass],
      );
      return [
        for (final r in rows)
          NetworkRuleDef(
            fromClass: r['from_class'] as String,
            toClass: r['to_class'] as String,
            isValid: (r['is_valid'] as num?)?.toInt() == 1,
            fromSubtype: (r['from_subtype'] as num?)?.toInt(),
            fromTerminal: r['from_terminal'] as String?,
            toSubtype: (r['to_subtype'] as num?)?.toInt(),
            toTerminal: r['to_terminal'] as String?,
            phaseRule: r['phase_rule'] as String?,
            name: r['name'] as String?,
          ),
      ];
    } on SqliteException {
      return const [];
    }
  }

  /// The allowed values of a coded domain, ordered as the server sorted them
  /// (sort_order). Returns empty for a free-text/range field, an unknown domain,
  /// or a pre-v2 pack that predates the `domain_values` table (the missing-table
  /// error is swallowed so an old pack degrades to plain inputs, not a crash).
  List<DomainValue> domainValuesFor(String domainId) {
    try {
      final rows = _db.select(
        '''
        SELECT code, label FROM domain_values
        WHERE domain_id = ?
        ORDER BY sort_order
        ''',
        [domainId],
      );
      return [
        for (final r in rows)
          DomainValue(
            code: r['code'] as String,
            label: (r['label'] as String?)?.isNotEmpty == true
                ? r['label'] as String
                : r['code'] as String,
          ),
      ];
    } on SqliteException {
      return const [];
    }
  }

  /// Every map-context symbology rule in the pack, grouped by feature class code
  /// (pack schema v4+). The map-style builder (pack_style.dart) compiles each
  /// class's rules into MapLibre data-driven paint. Returns an empty map for a
  /// pre-v4 pack that predates the `symbology` table (the missing-table error is
  /// swallowed, so the map degrades to the placeholder per-class colors instead of
  /// crashing), same as [domainValuesFor] does for pre-v2 packs.
  Map<String, List<SymbologyRule>> symbologyByClass() {
    try {
      final rows = _db.select('''
        SELECT feature_class, subtype_code, domain_field, domain_value_code,
               min_zoom, max_zoom, style_def
        FROM symbology
      ''');
      final out = <String, List<SymbologyRule>>{};
      for (final r in rows) {
        final defRaw = r['style_def'] as String?;
        Map<String, dynamic> def = const {};
        if (defRaw != null && defRaw.isNotEmpty) {
          final decoded = jsonDecode(defRaw);
          if (decoded is Map<String, dynamic>) def = decoded;
        }
        final rule = SymbologyRule(
          subtypeCode: (r['subtype_code'] as num?)?.toInt(),
          domainField: r['domain_field'] as String?,
          domainValueCode: r['domain_value_code'] as String?,
          minZoom: (r['min_zoom'] as num?)?.toInt(),
          maxZoom: (r['max_zoom'] as num?)?.toInt(),
          style: StyleDef.fromJson(def),
        );
        (out[r['feature_class'] as String] ??= <SymbologyRule>[]).add(rule);
      }
      return out;
    } on SqliteException {
      return const {};
    }
  }

  /// The DB-authored SVG markup for every icon the pack ships, keyed by
  /// `icon_ref` (pack schema v5+). A symbology rule's `style_def.icon_ref` points
  /// at one of these; the map substitutes the rule's color into the `{{COLOR}}`
  /// placeholder and rasterizes it (icon_atlas.dart). Returns an empty map for a
  /// pre-v5 pack with no `icons` table -- the missing-table error is swallowed
  /// (same as [symbologyByClass] for pre-v4), so the map degrades to the built-in
  /// drawn shapes rather than crashing.
  Map<String, String> iconSvgByRef() {
    try {
      final rows = _db.select('SELECT icon_ref, svg FROM icons');
      final out = <String, String>{};
      for (final r in rows) {
        final ref = r['icon_ref'] as String?;
        final svg = r['svg'] as String?;
        if (ref != null && svg != null) out[ref] = svg;
      }
      return out;
    } on SqliteException {
      return const {};
    }
  }

  /// The image attachments the pack carries for a feature (pack schema v6+),
  /// each with its offline thumbnail bytes. Ordered by id for a stable grid.
  /// Returns empty for a pre-v6 pack that predates the `attachments` table (the
  /// missing-table error is swallowed, same as [symbologyByClass] for pre-v4),
  /// so an old pack simply shows no packed attachments rather than crashing.
  List<PackAttachment> attachmentsFor(String featureId) {
    try {
      final rows = _db.select(
        '''
        SELECT id, feature_class, feature_id, filename, content_type, byte_size, thumb
        FROM attachments WHERE feature_id = ? ORDER BY id
        ''',
        [featureId],
      );
      return [
        for (final r in rows)
          PackAttachment(
            id: r['id'] as String,
            featureClass: (r['feature_class'] as String?) ?? '',
            featureId: (r['feature_id'] as String?) ?? featureId,
            filename: r['filename'] as String?,
            contentType: r['content_type'] as String?,
            byteSize: (r['byte_size'] as num?)?.toInt() ?? 0,
            thumb: r['thumb'] is Uint8List ? r['thumb'] as Uint8List : null,
          ),
      ];
    } on SqliteException {
      return const [];
    }
  }

  /// Which of [ids] exist as features in this pack. Index-backed by the primary
  /// key, so it's cheap even for a long id list. Used by the fresh-pack reconcile
  /// (PackStore.reconcileSyncedEdits) to confirm a synced offline edit is now
  /// baked into the new snapshot before its stale local copy is dropped.
  Set<String> existingFeatureIds(List<String> ids) {
    if (ids.isEmpty) return const {};
    final placeholders = List.filled(ids.length, '?').join(',');
    final rows =
        _db.select('SELECT id FROM features WHERE id IN ($placeholders)', ids);
    return {for (final r in rows) r['id'] as String};
  }

  /// Which of [ids] the pack carries as attachments (pack schema v6+). Used by the
  /// fresh-pack reconcile (PackStore.reconcileSyncedEdits) to confirm a synced
  /// captured photo now rides the new snapshot's `attachments` table before its
  /// redundant local file+row are dropped. Swallows the missing-table error on a
  /// pre-v6 pack (returns empty -> nothing pruned), same as [attachmentsFor].
  Set<String> existingAttachmentIds(List<String> ids) {
    if (ids.isEmpty) return const {};
    final placeholders = List.filled(ids.length, '?').join(',');
    try {
      final rows = _db.select(
          'SELECT id FROM attachments WHERE id IN ($placeholders)', ids);
      return {for (final r in rows) r['id'] as String};
    } on SqliteException {
      return const {};
    }
  }

  // ---- WAM read-context (pack schema v8, requester had wam:read) -----------
  //
  // The four wam_* device tables ship ONLY when the pack requester holds
  // wam:read (see internal/fieldpack/builder.go PackScope.IncludeWAM). A pre-v8
  // pack -- or a v8 pack built for a user without the perm -- has no such table,
  // so every read below swallows the missing-table SqliteException and degrades
  // to empty, exactly as [domainValuesFor]/[symbologyByClass] do for their gated
  // tables. WAM never FKs into gis; an asset is a bare (asset_class, asset_id)
  // ref the caller resolves against `features` when it needs to show it.

  /// Whether this pack ships WAM read-context at all -- the `wam_work_orders`
  /// table exists (v8 pack built for a wam:read user). The mobile WAM surfaces
  /// gate their entry point on this so a plain pack simply hides the work board
  /// rather than showing an empty one. Cached for the life of the open pack.
  bool get packsWam => _packsWam ??= _tableExists('wam_work_orders');
  bool? _packsWam;

  /// Whether this pack ships faults read-context at all -- the `faults_events`
  /// table exists (built when the pack requester holds faults:read, see
  /// internal/fieldpack/builder.go). The mobile faults surfaces gate their entry
  /// point on this so a plain pack simply hides the fault board rather than showing
  /// an empty one. Independent of [packsWam] -- a pack can carry faults, WAM, both,
  /// or neither. Cached for the life of the open pack.
  bool get packsFaults => _packsFaults ??= _tableExists('faults_events');
  bool? _packsFaults;

  /// The requester's own field work board: every work order the pack carries
  /// (the server already filtered to the requester's non-terminal orders, any
  /// bbox -- a job is a job wherever its asset sits). Newest scheduled first,
  /// then by id for stability. Empty on a pack with no WAM context.
  List<PackWorkOrder> workOrders() {
    try {
      final rows = _db.select('''
        SELECT id, reference, work_type, status, priority, title, description,
               asset_class, asset_id, defect_id, assigned_to, scheduled_for,
               due_by, started_at, completion_notes
        FROM wam_work_orders
        ORDER BY scheduled_for IS NULL, scheduled_for, id
      ''');
      return [for (final r in rows) _toWorkOrder(r)];
    } on SqliteException {
      return const [];
    }
  }

  /// One work order by id, or null when the pack has no WAM context or no such
  /// order. Index-backed by the primary key.
  PackWorkOrder? workOrderById(String id) {
    try {
      final rows = _db.select(
        '''
        SELECT id, reference, work_type, status, priority, title, description,
               asset_class, asset_id, defect_id, assigned_to, scheduled_for,
               due_by, started_at, completion_notes
        FROM wam_work_orders WHERE id = ?
        ''',
        [id],
      );
      if (rows.isEmpty) return null;
      return _toWorkOrder(rows.first);
    } on SqliteException {
      return null;
    }
  }

  /// The open/work-ordered defects the pack carries for one asset (by id;
  /// `wam_defects_asset_idx` makes this index-backed). Empty on a pack with no
  /// WAM context. The class is carried for context but the id alone identifies
  /// the asset -- WAM defect ids are globally unique.
  List<PackDefect> defectsForAsset(String assetId) {
    try {
      final rows = _db.select(
        '''
        SELECT id, asset_class, asset_id, defect_type, severity, status,
               source, title, description, reported_at, notes
        FROM wam_defects WHERE asset_id = ?
        ORDER BY reported_at IS NULL, reported_at DESC, id
        ''',
        [assetId],
      );
      return [for (final r in rows) _toDefect(r)];
    } on SqliteException {
      return const [];
    }
  }

  /// Every defect the pack carries (the whole packed defect backlog, for a
  /// standalone defects list). Empty on a pack with no WAM context.
  List<PackDefect> defects() {
    try {
      final rows = _db.select('''
        SELECT id, asset_class, asset_id, defect_type, severity, status,
               source, title, description, reported_at, notes
        FROM wam_defects
        ORDER BY reported_at IS NULL, reported_at DESC, id
      ''');
      return [for (final r in rows) _toDefect(r)];
    } on SqliteException {
      return const [];
    }
  }

  // ---- Faults read-context (faults:read pack) -------------------------------
  //
  // The `faults_events` table ships ONLY when the pack requester holds faults:read
  // (see internal/fieldpack/builder.go). A pack without it -- or one built for a
  // user without the perm -- has no such table, so both reads below swallow the
  // missing-table SqliteException and degrade to empty, exactly as the WAM reads do.
  // The builder ships only OPEN faults (status NOT IN cleared/closed) that carry a
  // matchable asset_id. Faults never FK into gis; an asset is a bare (asset_class,
  // asset_id) ref the caller resolves against `features` when it needs to show it.

  /// Every OPEN fault the pack carries (the whole packed fault backlog, for a
  /// standalone fault board), newest by detected_at. Empty on a pack with no faults
  /// context.
  List<PackFault> faults() {
    try {
      final rows = _db.select('''
        SELECT id, reference, asset_class, asset_id, fault_type, cause, status,
               title, description, detected_at, longitude, latitude,
               protective_device_class, protective_device_id, incident_id, defect_id
        FROM faults_events
        ORDER BY detected_at IS NULL, detected_at DESC, id
      ''');
      return [for (final r in rows) _toFault(r)];
    } on SqliteException {
      return const [];
    }
  }

  /// The open faults the pack carries for one asset (by id; `faults_events_asset_idx`
  /// makes this index-backed). Empty on a pack with no faults context. The class is
  /// carried for context but the id alone identifies the asset -- fault ids are
  /// globally unique.
  List<PackFault> faultsForAsset(String assetId) {
    try {
      final rows = _db.select(
        '''
        SELECT id, reference, asset_class, asset_id, fault_type, cause, status,
               title, description, detected_at, longitude, latitude,
               protective_device_class, protective_device_id, incident_id, defect_id
        FROM faults_events WHERE asset_id = ?
        ORDER BY detected_at IS NULL, detected_at DESC, id
        ''',
        [assetId],
      );
      return [for (final r in rows) _toFault(r)];
    } on SqliteException {
      return const [];
    }
  }

  /// The active inspection checklists the pack ships. When [assetClass] is given,
  /// only templates that apply to it are returned -- a template scoped to that
  /// class OR an any-asset template (null asset_class). Empty on a pack with no
  /// WAM context. The ordered items are read separately via [templateItems].
  List<PackInspectionTemplate> inspectionTemplates({String? assetClass}) {
    try {
      final rows = assetClass == null
          ? _db.select('''
              SELECT id, code, name, description, asset_class, active
              FROM wam_inspection_templates
              WHERE active = 1
              ORDER BY name IS NULL, name, id
            ''')
          : _db.select(
              '''
              SELECT id, code, name, description, asset_class, active
              FROM wam_inspection_templates
              WHERE active = 1
                AND (asset_class = ? OR asset_class IS NULL)
              ORDER BY name IS NULL, name, id
              ''',
              [assetClass],
            );
      return [
        for (final r in rows)
          PackInspectionTemplate(
            id: r['id'] as String,
            code: r['code'] as String?,
            name: r['name'] as String?,
            description: r['description'] as String?,
            assetClass: r['asset_class'] as String?,
            active: (r['active'] as num?)?.toInt() != 0,
          ),
      ];
    } on SqliteException {
      return const [];
    }
  }

  /// The ordered prompts of one inspection checklist (by template id;
  /// `wam_insp_items_template_idx` makes this index-backed), ascending by
  /// position. Empty on a pack with no WAM context or an unknown template.
  List<PackInspectionTemplateItem> templateItems(String templateId) {
    try {
      final rows = _db.select(
        '''
        SELECT id, template_id, position, prompt, response_type,
               fail_severity, guidance
        FROM wam_inspection_template_items
        WHERE template_id = ?
        ORDER BY position, id
        ''',
        [templateId],
      );
      return [
        for (final r in rows)
          PackInspectionTemplateItem(
            id: r['id'] as String,
            templateId: r['template_id'] as String,
            position: (r['position'] as num?)?.toInt() ?? 0,
            prompt: r['prompt'] as String?,
            responseType: r['response_type'] as String?,
            failSeverity: r['fail_severity'] as String?,
            guidance: r['guidance'] as String?,
          ),
      ];
    } on SqliteException {
      return const [];
    }
  }

  /// Whether [table] exists in the pack (any row count, including zero). Distinct
  /// from [_tableHasRows]: a WAM pack can legitimately carry an empty work board,
  /// so "ships WAM context" is table presence, not row presence.
  bool _tableExists(String table) {
    try {
      _db.select('SELECT 1 FROM $table LIMIT 1');
      return true;
    } on SqliteException {
      return false;
    }
  }

  PackWorkOrder _toWorkOrder(Row r) => PackWorkOrder(
        id: r['id'] as String,
        workType: (r['work_type'] as String?) ?? '',
        status: (r['status'] as String?) ?? '',
        reference: r['reference'] as String?,
        priority: r['priority'] as String?,
        title: r['title'] as String?,
        description: r['description'] as String?,
        assetClass: r['asset_class'] as String?,
        assetId: r['asset_id'] as String?,
        defectId: r['defect_id'] as String?,
        assignedTo: r['assigned_to'] as String?,
        scheduledFor: r['scheduled_for'] as String?,
        dueBy: r['due_by'] as String?,
        startedAt: r['started_at'] as String?,
        completionNotes: r['completion_notes'] as String?,
      );

  PackDefect _toDefect(Row r) => PackDefect(
        id: r['id'] as String,
        assetClass: (r['asset_class'] as String?) ?? '',
        assetId: (r['asset_id'] as String?) ?? '',
        severity: (r['severity'] as String?) ?? '',
        status: (r['status'] as String?) ?? '',
        defectType: r['defect_type'] as String?,
        source: r['source'] as String?,
        title: r['title'] as String?,
        description: r['description'] as String?,
        reportedAt: r['reported_at'] as String?,
        notes: r['notes'] as String?,
      );

  PackFault _toFault(Row r) => PackFault(
        id: r['id'] as String,
        assetClass: (r['asset_class'] as String?) ?? '',
        assetId: (r['asset_id'] as String?) ?? '',
        status: (r['status'] as String?) ?? '',
        reference: r['reference'] as String?,
        faultType: r['fault_type'] as String?,
        cause: r['cause'] as String?,
        title: r['title'] as String?,
        description: r['description'] as String?,
        detectedAt: r['detected_at'] as String?,
        longitude: (r['longitude'] as num?)?.toDouble(),
        latitude: (r['latitude'] as num?)?.toDouble(),
        protectiveDeviceClass: r['protective_device_class'] as String?,
        protectiveDeviceId: r['protective_device_id'] as String?,
        incidentId: r['incident_id'] as String?,
        defectId: r['defect_id'] as String?,
      );

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
