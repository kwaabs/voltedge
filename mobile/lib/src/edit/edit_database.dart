import 'dart:convert';
import 'dart:typed_data';

import 'package:sqlite3/sqlite3.dart';

/// One pending local edit to a feature, kept in the WRITABLE edit DB that lives
/// beside the read-only pack (Phase 21 Slice 3a, docs/phase-21-field-tool.md).
///
/// [attrs] is a DELTA, not the full attribute map: only the keys whose value
/// differs from the pack's (server) truth. That keeps replaying it through
/// crud.UpdateFeature (Slice 3c's POST /sync) a minimal, LWW-friendly PATCH, and
/// makes "reverted back to the packed value" naturally shrink the delta to empty
/// (which drops the overlay). [geomJson] is the moved geometry as a GeoJSON
/// geometry object (Slice 10 vertex editor) -- null for an attribute-only edit,
/// set when the feature's vertices were dragged/added/removed. [geomBytes]/[name]
/// stay reserved. An overlay can carry an attr delta, a geometry move, or both.
class FeatureOverlay {
  const FeatureOverlay({
    required this.featureId,
    required this.featureClass,
    required this.attrs,
    required this.updatedAt,
    this.geomJson,
    this.geomBytes,
    this.name,
  });

  final String featureId;
  final String featureClass;
  final Map<String, dynamic> attrs; // delta vs the packed values
  final DateTime updatedAt;
  final Map<String, dynamic>? geomJson; // moved geometry (GeoJSON), or null
  final Uint8List? geomBytes;
  final String? name;

  bool get hasAttrEdit => attrs.isNotEmpty;
  bool get hasGeomEdit => geomJson != null;
}

/// One queued operation to push to the server when back online. [payload] is
/// already in crud.UpdateFeatureInput shape ({"attributes": {...}}), so Slice 3c
/// posts it verbatim; [opId] is the idempotency key the sync endpoint dedupes on.
class OutboxOp {
  const OutboxOp({
    required this.opId,
    required this.featureId,
    required this.featureClass,
    required this.kind,
    required this.payload,
    required this.createdAt,
    this.baseVersion,
    this.syncedAt,
    this.applyStatus,
    this.applyError,
  });

  final String opId;
  final String featureId;
  final String featureClass;
  final String kind; // 'update_attrs' (3a) | 'create' (create slice); 'move' (3b)
  final Map<String, dynamic> payload;
  final DateTime createdAt;

  /// The pack's snapshot_at this edit was made against -- carried so the server's
  /// LWW resolve (Slice 3c) can reason about staleness. Null for an older pack.
  final String? baseVersion;
  final DateTime? syncedAt; // null until POST /sync acks it (Slice 3c)

  /// The apply worker's outcome, learned via the status pull (Phase 23 Slice 2).
  /// Since Phase 23 POST /sync only CAPTURES an op ([syncedAt] set = "durably
  /// server-side"); a worker applies it moments later and may quarantine it. This
  /// is null until the first status pull, then the inbox lifecycle value:
  /// 'received'/'applying'/'pending_review' (still in flight) -> 'applied'
  /// (landed) | 'rejected' | 'conflict' (quarantined -- the edit did NOT land) |
  /// 'superseded' (a newer edit to the same feature replaced this one -- silently
  /// dropped, Slice 4c; never surfaced, unlike rejected/conflict).
  final String? applyStatus;

  /// The worker's human message on a 'rejected'/'conflict' apply -- what to show
  /// in the "needs attention" list.
  final String? applyError;

  /// True once the op is durably captured server-side (POST /sync acked it).
  bool get isCaptured => syncedAt != null;

  /// True when the apply worker quarantined this op -- it needs the user's
  /// attention because the edit was captured but never applied. NOTE 'superseded'
  /// is deliberately excluded (Slice 4c): a superseded op was replaced by a newer
  /// edit to the same feature, so there is nothing for the user to act on.
  bool get needsAttention =>
      applyStatus == 'rejected' || applyStatus == 'conflict';
}

/// One feature CREATED offline (Phase 21 create slice). Unlike an overlay -- a
/// delta against a packed row -- a create is a whole new feature that exists only
/// on-device until POST /sync lands it. [featureId] is a client-minted UUID v4
/// (see util/uuid.dart) that becomes the row's real primary key on the server, so
/// the create is idempotent and needs no server round-trip to get an id. [geometry]
/// is a GeoJSON geometry object (the exact shape crud.CreateFeatureInput.Geometry
/// wants), and [attrs] is the FULL attribute map (not a delta -- there's no packed
/// truth to diff against).
class LocalCreate {
  const LocalCreate({
    required this.featureId,
    required this.featureClass,
    required this.subtype,
    required this.geometry,
    required this.attrs,
    required this.createdAt,
    this.name,
    this.baseVersion,
  });

  final String featureId;
  final String featureClass;
  final int? subtype;
  final Map<String, dynamic> geometry; // GeoJSON geometry object
  final Map<String, dynamic> attrs; // full attribute map
  final DateTime createdAt;
  final String? name;
  final String? baseVersion;
}

/// One packed feature DELETED offline (Phase 21 Slice 15). A tombstone: unlike an
/// overlay (a delta) or a create (a whole new row), a delete records only that the
/// feature should be soft-deleted on the server. Keyed by the feature's real
/// (server) id, so it's the packed feature's id -- never an offline create, which
/// is discarded outright (discardCreate) since there's no server row to delete.
/// The tombstone hides the feature from the map (fed into hiddenIds, never
/// redrawn) and, like an overlay/create, is KEPT after sync so the row stays
/// hidden until this pack is replaced (fresh-pack auto-pruning is deferred -- see
/// EditDatabase.syncedDeletedFeatureIds). [baseVersion] is the pack snapshot the
/// delete was made against (LWW context for the server), null for an older pack.
class LocalDelete {
  const LocalDelete({
    required this.featureId,
    required this.featureClass,
    required this.createdAt,
    this.baseVersion,
  });

  final String featureId;
  final String featureClass;
  final DateTime createdAt;
  final String? baseVersion;
}

/// One electrical connection established offline (Phase 21 Slice 13). Like a
/// [LocalCreate] it's a whole new fact that exists only on-device until POST /sync
/// replays it through the server's zero-trust establish engine (Slice 12 spine).
/// [id] is a client-minted UUID v4 that doubles as the outbox op id, so a re-push
/// is idempotent (the server no-ops an already-established edge). Endpoints are
/// carried by class code + feature id (+ an optional named terminal per side), the
/// exact shape the establish_connection sync op wants. Unlike an overlay/create --
/// keyed by one feature -- a connection spans TWO features, and one feature can be
/// the source of many, so this is its own table (never deduped by feature id).
class LocalConnection {
  const LocalConnection({
    required this.id,
    required this.fromClass,
    required this.fromId,
    required this.toClass,
    required this.toId,
    required this.createdAt,
    this.fromTerminal,
    this.toTerminal,
    this.baseVersion,
  });

  final String id;
  final String fromClass;
  final String fromId;
  final String? fromTerminal;
  final String toClass;
  final String toId;
  final String? toTerminal;
  final DateTime createdAt;
  final String? baseVersion;

  /// True when [featureId] is either endpoint -- used to fold a pending
  /// connection into a feature's neighbour list in the detail panel.
  bool involves(String featureId) => fromId == featureId || toId == featureId;

  /// The endpoint at the OTHER end from [featureId] as (class, id, terminal),
  /// or null when [featureId] isn't an endpoint. Mirrors PackDatabase's
  /// connectionsFor resolution so the panel renders a local edge like a packed one.
  ({String featureClass, String featureId, String? terminal})? otherEnd(
      String featureId) {
    if (fromId == featureId) {
      return (featureClass: toClass, featureId: toId, terminal: toTerminal);
    }
    if (toId == featureId) {
      return (featureClass: fromClass, featureId: fromId, terminal: fromTerminal);
    }
    return null;
  }
}

/// One relationship ESTABLISHED offline (Phase 21 Slice 14). The relationship
/// sibling of [LocalConnection]: a non-topological association (origin ->
/// destination) POST /sync replays through the server's zero-trust establish
/// engine (Slice 12 spine). [id] is a client-minted UUID v4 that doubles as the
/// outbox op id, so a re-push is idempotent (the server no-ops an already-
/// established relationship). Endpoints are carried by class code + feature id --
/// the exact shape the establish_relationship sync op wants. [kind] is the optional
/// relationship_class kind, only needed to disambiguate when the (origin,
/// destination) class pair has more than one relationship_classes row; null lets
/// the server auto-resolve the single class for the pair. Like a connection -- and
/// unlike an overlay/create keyed by one feature -- a relationship spans TWO
/// features and one feature can be the origin of many, so this is its own table
/// (never deduped by feature id).
class LocalRelationship {
  const LocalRelationship({
    required this.id,
    required this.originClass,
    required this.originId,
    required this.destinationClass,
    required this.destinationId,
    required this.createdAt,
    this.kind,
    this.baseVersion,
  });

  final String id;
  final String originClass;
  final String originId;
  final String destinationClass;
  final String destinationId;
  final String? kind;
  final DateTime createdAt;
  final String? baseVersion;

  /// True when [featureId] is either endpoint -- used to fold a pending
  /// relationship into a feature's neighbour list in the detail panel.
  bool involves(String featureId) =>
      originId == featureId || destinationId == featureId;

  /// The endpoint at the OTHER end from [featureId] as (class, id), or null when
  /// [featureId] isn't an endpoint. Mirrors PackDatabase's relationshipsFor
  /// resolution so the panel renders a local relationship like a packed one.
  ({String featureClass, String featureId})? otherEnd(String featureId) {
    if (originId == featureId) {
      return (featureClass: destinationClass, featureId: destinationId);
    }
    if (destinationId == featureId) {
      return (featureClass: originClass, featureId: originId);
    }
    return null;
  }
}

/// One image attachment captured offline (Phase 22 Slice 5). Unlike a feature
/// overlay/create -- pure metadata replayed through POST /sync -- an attachment
/// carries BYTES: [localPath] is the captured image copied into the pack dir,
/// which stays the on-device truth (and the offline thumbnail source) until it's
/// uploaded. [id] is a client-minted UUID v4 that becomes the server row's PK, so
/// the register -> PUT -> commit upload is idempotent (a re-register with the same
/// id is a no-op server-side). [syncedAt] is null until the three-step upload has
/// fully committed; a pending row is what the sync flow pushes.
class LocalAttachment {
  const LocalAttachment({
    required this.id,
    required this.featureClass,
    required this.featureId,
    required this.filename,
    required this.contentType,
    required this.byteSize,
    required this.localPath,
    required this.createdAt,
    this.syncedAt,
  });

  final String id;
  final String featureClass;
  final String featureId;
  final String filename;
  final String contentType;
  final int byteSize;
  final String localPath;
  final DateTime createdAt;
  final DateTime? syncedAt;

  bool get isPending => syncedAt == null;
}

/// EditDatabase is the on-device persistence for offline edits: a tiny writable
/// SQLite file (`edits.sqlite`) sitting next to the read-only `data.sqlite` pack.
/// It holds two tables the map/panel and the (later) sync engine share:
///
///   * `overlays` -- one row per edited feature, the current local edit state
///     (an attribute delta today, geometry/name later). Drives the dirty set the
///     map paints amber and the merged values the panel shows.
///   * `creates`  -- one row per feature created offline (whole new features, not
///     deltas). Drives the green "new" overlay the map paints and the values the
///     panel shows for a not-yet-synced feature.
///   * `outbox`   -- the queue of operations to replay to the server. One PENDING
///     row per (feature, kind): a re-edit REPLACES the pending op with a fresh one
///     carrying the latest delta, so the outbox never bloats and always reflects
///     "the newest local intent," which is exactly what LWW wants.
///   * `connections` -- one row per connection established offline (Slice 13).
///   * `relationships` -- one row per relationship established offline (Slice 14).
///     NOT deduped by feature (a feature sources many connections); each row's id
///     is its establish_connection outbox op id, so the rendered edge and the
///     pending op share one idempotency key.
///
/// Raw SQL by design (Slice 2a/3a decision) -- a fixed handful of tables doesn't
/// justify Drift's build_runner step. Everything runs on the app's event loop
/// against a single connection, so writes are naturally serialized.
class EditDatabase {
  EditDatabase._(this._db);

  final Database _db;

  factory EditDatabase.open(String path) {
    final db = sqlite3.open(path, mode: OpenMode.readWriteCreate);
    _migrate(db);
    return EditDatabase._(db);
  }

  static void _migrate(Database db) {
    // WAL: this file is written interactively (unlike the throwaway pack build),
    // so we want crash-safe, concurrent-reader durability, not raw speed.
    db.execute('PRAGMA journal_mode = WAL');
    db.execute('''
      CREATE TABLE IF NOT EXISTS overlays (
        feature_id    TEXT PRIMARY KEY,
        feature_class TEXT NOT NULL,
        attrs         TEXT,
        geom          BLOB,
        geom_json     TEXT,
        name          TEXT,
        base_version  TEXT,
        updated_at    TEXT NOT NULL
      )
    ''');
    // geom_json was added in Slice 10 (vertex editor): an older edits.sqlite has
    // the overlays table without it, so add it in-place. SQLite has no
    // ADD COLUMN IF NOT EXISTS, so check the schema first.
    if (!_hasColumn(db, 'overlays', 'geom_json')) {
      db.execute('ALTER TABLE overlays ADD COLUMN geom_json TEXT');
    }
    db.execute('''
      CREATE TABLE IF NOT EXISTS creates (
        feature_id    TEXT PRIMARY KEY,
        feature_class TEXT NOT NULL,
        subtype       INTEGER,
        geom_json     TEXT NOT NULL,
        attrs         TEXT,
        name          TEXT,
        base_version  TEXT,
        created_at    TEXT NOT NULL
      )
    ''');
    // Features soft-DELETED offline (Phase 21 Slice 15). A tombstone table keyed by
    // the packed feature's server id. Kept after sync (like overlays/creates) until
    // a fresh pack no longer carries the row -- see LocalDelete + the delete
    // reconcile. The matching 'delete' outbox op (feature_id == this row's id)
    // carries an empty payload; the server needs only (feature_class, feature_id).
    db.execute('''
      CREATE TABLE IF NOT EXISTS deletes (
        feature_id    TEXT PRIMARY KEY,
        feature_class TEXT NOT NULL,
        base_version  TEXT,
        created_at    TEXT NOT NULL
      )
    ''');
    db.execute('''
      CREATE TABLE IF NOT EXISTS outbox (
        op_id         TEXT PRIMARY KEY,
        feature_id    TEXT NOT NULL,
        feature_class TEXT NOT NULL,
        kind          TEXT NOT NULL,
        payload       TEXT NOT NULL,
        base_version  TEXT,
        created_at    TEXT NOT NULL,
        synced_at     TEXT
      )
    ''');
    db.execute(
        'CREATE INDEX IF NOT EXISTS outbox_feature_idx ON outbox (feature_id)');
    db.execute(
        'CREATE INDEX IF NOT EXISTS outbox_pending_idx ON outbox (synced_at)');
    // Apply-status columns (Phase 23 Slice 2). Since Phase 23 an op's synced_at
    // only means "captured by POST /sync"; the apply worker runs moments later and
    // may reject it (a poison op). These carry the worker's outcome learned via the
    // status pull: apply_status ('received' -> 'applied' | 'rejected' | 'conflict'),
    // apply_error its message, and dismissed_at records the user acknowledging a
    // rejection so it drops off the "needs attention" list. Added in-place -- an
    // older edits.sqlite predates them (SQLite has no ADD COLUMN IF NOT EXISTS).
    if (!_hasColumn(db, 'outbox', 'apply_status')) {
      db.execute('ALTER TABLE outbox ADD COLUMN apply_status TEXT');
    }
    if (!_hasColumn(db, 'outbox', 'apply_error')) {
      db.execute('ALTER TABLE outbox ADD COLUMN apply_error TEXT');
    }
    if (!_hasColumn(db, 'outbox', 'dismissed_at')) {
      db.execute('ALTER TABLE outbox ADD COLUMN dismissed_at TEXT');
    }
    // Image attachments captured offline (Phase 22 Slice 5). Separate from the
    // outbox: an attachment is a three-step BYTE upload (register -> presigned
    // PUT -> commit), not a JSON op replayed through POST /sync, so it has its own
    // queue keyed by the client-minted attachment id, with the captured file's
    // local_path as the on-device truth until uploaded.
    db.execute('''
      CREATE TABLE IF NOT EXISTS local_attachments (
        id            TEXT PRIMARY KEY,
        feature_class TEXT NOT NULL,
        feature_id    TEXT NOT NULL,
        filename      TEXT NOT NULL,
        content_type  TEXT NOT NULL,
        byte_size     INTEGER NOT NULL,
        local_path    TEXT NOT NULL,
        created_at    TEXT NOT NULL,
        synced_at     TEXT
      )
    ''');
    db.execute('CREATE INDEX IF NOT EXISTS local_attachments_feature_idx '
        'ON local_attachments (feature_id)');
    // Connections established offline (Phase 21 Slice 13). Its own table -- NOT the
    // outbox's (feature, kind) dedupe -- because one feature is the source of many
    // connections and each is a distinct fact (see LocalConnection). The row id IS
    // the outbox op_id, so the pending establish_connection op and the row that
    // renders the local edge share one idempotency key. from_/to_ terminals are the
    // optional named ports either endpoint is wired at.
    db.execute('''
      CREATE TABLE IF NOT EXISTS connections (
        id            TEXT PRIMARY KEY,
        from_class    TEXT NOT NULL,
        from_id       TEXT NOT NULL,
        from_terminal TEXT,
        to_class      TEXT NOT NULL,
        to_id         TEXT NOT NULL,
        to_terminal   TEXT,
        base_version  TEXT,
        created_at    TEXT NOT NULL
      )
    ''');
    db.execute(
        'CREATE INDEX IF NOT EXISTS connections_from_idx ON connections (from_id)');
    db.execute(
        'CREATE INDEX IF NOT EXISTS connections_to_idx ON connections (to_id)');
    // Relationships established offline (Phase 21 Slice 14). The relationship
    // sibling of `connections` -- its own table for the same reason (one feature is
    // the origin of many, each a distinct fact; see LocalRelationship). The row id
    // IS the outbox op_id, so the pending establish_relationship op and the row that
    // renders the local relationship share one idempotency key. `kind` is the
    // optional relationship_class kind (null = let the server auto-resolve the pair).
    // NOTE: this is the EDIT db's queue of pending establishes -- distinct from the
    // read-only pack's own `relationships` table of already-committed edges.
    db.execute('''
      CREATE TABLE IF NOT EXISTS relationships (
        id                TEXT PRIMARY KEY,
        origin_class      TEXT NOT NULL,
        origin_id         TEXT NOT NULL,
        destination_class TEXT NOT NULL,
        destination_id    TEXT NOT NULL,
        kind              TEXT,
        base_version      TEXT,
        created_at        TEXT NOT NULL
      )
    ''');
    db.execute('CREATE INDEX IF NOT EXISTS relationships_origin_idx '
        'ON relationships (origin_id)');
    db.execute('CREATE INDEX IF NOT EXISTS relationships_dest_idx '
        'ON relationships (destination_id)');
  }

  /// True if [table] already has a column named [column] -- used to make an
  /// ADD COLUMN migration idempotent (SQLite has no ADD COLUMN IF NOT EXISTS).
  static bool _hasColumn(Database db, String table, String column) {
    final rows = db.select('PRAGMA table_info($table)');
    return rows.any((r) => r['name'] == column);
  }

  /// Every overlay row -- loaded once at open into the store's in-memory map so
  /// the dirty set and merged reads are pure memory after boot.
  List<FeatureOverlay> allOverlays() {
    final rows = _db.select(
        'SELECT feature_id, feature_class, attrs, geom, geom_json, name, updated_at FROM overlays');
    return [for (final r in rows) _toOverlay(r)];
  }

  /// Upsert a feature overlay AND replace the feature's pending update op in
  /// one transaction, so what the UI shows (overlay) and what will be pushed
  /// (outbox) can never drift apart. Replacing -- not appending -- the pending op
  /// keeps exactly one un-synced update per feature (LWW: newest local intent).
  ///
  /// An overlay can carry an attribute [delta], a geometry move ([geomJson], a
  /// GeoJSON geometry object), or both. The caller (EditStore) reads the current
  /// overlay and passes the FULL desired state each time -- an attr-only save
  /// re-passes the existing geomJson, and a geometry save re-passes the existing
  /// delta -- since INSERT OR REPLACE overwrites the whole row. Both flavours ride
  /// the same 'update_attrs' op: crud.UpdateFeatureInput reads geometry AND
  /// attributes from one payload, so the server needs no new op kind.
  void putEdit({
    required String featureId,
    required String featureClass,
    required Map<String, dynamic> delta,
    Map<String, dynamic>? geomJson,
    required String opId,
    required Map<String, dynamic> payload,
    required DateTime updatedAt,
    String? baseVersion,
  }) {
    _inTx(() {
      _db.execute(
        'INSERT OR REPLACE INTO overlays '
        '(feature_id, feature_class, attrs, geom, geom_json, name, base_version, updated_at) '
        'VALUES (?, ?, ?, NULL, ?, NULL, ?, ?)',
        [
          featureId,
          featureClass,
          jsonEncode(delta),
          geomJson == null ? null : jsonEncode(geomJson),
          baseVersion,
          updatedAt.toIso8601String(),
        ],
      );
      // Drop only the still-pending update op for this feature; a synced one is
      // history and stays. A future 'move' op (Slice 3b) is a different kind and
      // is untouched here.
      _db.execute(
        "DELETE FROM outbox WHERE feature_id = ? AND kind = 'update_attrs' "
        'AND synced_at IS NULL',
        [featureId],
      );
      _db.execute(
        'INSERT INTO outbox '
        '(op_id, feature_id, feature_class, kind, payload, base_version, created_at, synced_at) '
        "VALUES (?, ?, ?, 'update_attrs', ?, ?, ?, NULL)",
        [
          opId,
          featureId,
          featureClass,
          jsonEncode(payload),
          baseVersion,
          updatedAt.toIso8601String(),
        ],
      );
    });
  }

  /// Upsert a created feature AND replace its pending create op in one tx. Keyed
  /// by the client UUID, so re-saving the same offline feature (e.g. editing its
  /// attributes before sync) overwrites the row and refreshes the single pending
  /// 'create' op rather than piling up duplicates -- same LWW "newest intent" rule
  /// the update path uses.
  void putCreate({
    required String featureId,
    required String featureClass,
    required int? subtype,
    required Map<String, dynamic> geometry,
    required Map<String, dynamic> attrs,
    required String? name,
    required String opId,
    required Map<String, dynamic> payload,
    required DateTime createdAt,
    String? baseVersion,
  }) {
    _inTx(() {
      _db.execute(
        'INSERT OR REPLACE INTO creates '
        '(feature_id, feature_class, subtype, geom_json, attrs, name, base_version, created_at) '
        'VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
        [
          featureId,
          featureClass,
          subtype,
          jsonEncode(geometry),
          jsonEncode(attrs),
          name,
          baseVersion,
          createdAt.toIso8601String(),
        ],
      );
      _db.execute(
        "DELETE FROM outbox WHERE feature_id = ? AND kind = 'create' "
        'AND synced_at IS NULL',
        [featureId],
      );
      _db.execute(
        'INSERT INTO outbox '
        '(op_id, feature_id, feature_class, kind, payload, base_version, created_at, synced_at) '
        "VALUES (?, ?, ?, 'create', ?, ?, ?, NULL)",
        [
          opId,
          featureId,
          featureClass,
          jsonEncode(payload),
          baseVersion,
          createdAt.toIso8601String(),
        ],
      );
    });
  }

  /// Every created feature -- loaded once at open into the store's in-memory map.
  List<LocalCreate> allCreates() {
    final rows = _db.select(
        'SELECT feature_id, feature_class, subtype, geom_json, attrs, name, '
        'base_version, created_at FROM creates');
    return [for (final r in rows) _toCreate(r)];
  }

  /// Drop a feature's overlay/create and any still-pending ops -- used when an
  /// edit is reverted to exactly the packed values (empty delta) or a created
  /// feature is discarded, so nothing lingers.
  void clearFeature(String featureId) {
    _inTx(() {
      _db.execute('DELETE FROM overlays WHERE feature_id = ?', [featureId]);
      _db.execute('DELETE FROM creates WHERE feature_id = ?', [featureId]);
      _db.execute(
        'DELETE FROM outbox WHERE feature_id = ? AND synced_at IS NULL',
        [featureId],
      );
    });
  }

  // ---- Deletes (Phase 21 Slice 15) ------------------------------------------

  /// Every delete tombstone -- loaded once at open into the store's in-memory map
  /// so the hidden set (fed into the map's hiddenIds) is a pure-memory read.
  List<LocalDelete> allDeletes() {
    final rows = _db.select(
        'SELECT feature_id, feature_class, base_version, created_at FROM deletes');
    return [for (final r in rows) _toDelete(r)];
  }

  /// Record a delete tombstone AND queue its 'delete' outbox op in one tx. A delete
  /// SUPERSEDES any pending attribute edit for the feature, so this also drops the
  /// overlay and its still-pending update_attrs op -- pushing "edit then delete"
  /// would just soft-delete after a pointless PATCH. Replaces (not appends) the
  /// pending delete op so re-deleting is idempotent (one delete op per feature).
  /// The op's payload is empty: the server needs only (feature_class, feature_id).
  void putDelete({
    required String featureId,
    required String featureClass,
    required String opId,
    required DateTime createdAt,
    String? baseVersion,
  }) {
    _inTx(() {
      _db.execute(
        'INSERT OR REPLACE INTO deletes '
        '(feature_id, feature_class, base_version, created_at) VALUES (?, ?, ?, ?)',
        [featureId, featureClass, baseVersion, createdAt.toIso8601String()],
      );
      // A delete wins over an in-flight attribute edit: drop the overlay and its
      // still-pending update op so the outbox carries a delete, not a PATCH.
      _db.execute('DELETE FROM overlays WHERE feature_id = ?', [featureId]);
      _db.execute(
        "DELETE FROM outbox WHERE feature_id = ? AND kind IN ('update_attrs', 'delete') "
        'AND synced_at IS NULL',
        [featureId],
      );
      _db.execute(
        'INSERT INTO outbox '
        '(op_id, feature_id, feature_class, kind, payload, base_version, created_at, synced_at) '
        "VALUES (?, ?, ?, 'delete', '{}', ?, ?, NULL)",
        [
          opId,
          featureId,
          featureClass,
          baseVersion,
          createdAt.toIso8601String(),
        ],
      );
    });
  }

  /// Undo a delete tombstone before it syncs: drop the row AND its still-pending
  /// delete op, so the feature reappears from the base pack. A synced delete is
  /// history and left alone (the UI only offers undo on un-synced ones), matching
  /// discardConnection/discardRelationship.
  void discardDelete(String featureId) {
    _inTx(() {
      _db.execute('DELETE FROM deletes WHERE feature_id = ?', [featureId]);
      _db.execute(
        "DELETE FROM outbox WHERE feature_id = ? AND kind = 'delete' "
        'AND synced_at IS NULL',
        [featureId],
      );
    });
  }

  /// Feature ids whose delete op the server has acked (pending deletes excluded).
  /// The counterpart of [syncedFeatureIds] for tombstones -- but note the reconcile
  /// is INVERSE and asymmetric: a synced EDIT is redundant once the fresh pack
  /// CONTAINS the row, whereas a synced DELETE would be redundant once the pack no
  /// longer carries it -- yet "absent" also describes a pack that simply doesn't
  /// cover the feature's region, and packs exclude soft-deleted rows, so there's no
  /// positive "confirmed deleted" signal (unlike a connection's presence check).
  /// Auto-pruning is therefore deferred: a synced tombstone is left in place and
  /// cleaned up when its pack dir is deleted. This accessor + [dropDeletes] are the
  /// hooks a future scope-aware reconcile would use.
  List<String> syncedDeletedFeatureIds() {
    final rows = _db.select(
      "SELECT d.feature_id FROM deletes d JOIN outbox o ON o.feature_id = d.feature_id "
      "AND o.kind = 'delete' WHERE o.synced_at IS NOT NULL",
    );
    return [for (final r in rows) r['feature_id'] as String];
  }

  /// Drop the given delete tombstones AND their synced 'delete' op history once a
  /// fresh pack is confirmed to no longer carry the rows. Only pass ids from
  /// [syncedDeletedFeatureIds] (never a pending delete): this deletes the synced op
  /// row too, so an accidental call on a pending delete would lose an unpushed op.
  void dropDeletes(Iterable<String> featureIds) {
    final ids = featureIds.toList(growable: false);
    if (ids.isEmpty) return;
    _inTx(() {
      for (final id in ids) {
        _db.execute('DELETE FROM deletes WHERE feature_id = ?', [id]);
        _db.execute(
          "DELETE FROM outbox WHERE feature_id = ? AND kind = 'delete' "
          'AND synced_at IS NOT NULL',
          [id],
        );
      }
    });
  }

  LocalDelete _toDelete(Row r) {
    return LocalDelete(
      featureId: r['feature_id'] as String,
      featureClass: r['feature_class'] as String,
      baseVersion: r['base_version'] as String?,
      createdAt:
          DateTime.tryParse(r['created_at'] as String? ?? '') ?? DateTime.now(),
    );
  }

  /// Un-synced operations, oldest first -- the push order for POST /sync.
  List<OutboxOp> pendingOps() {
    final rows = _db.select(
      'SELECT op_id, feature_id, feature_class, kind, payload, base_version, '
      'created_at, synced_at, apply_status, apply_error FROM outbox '
      'WHERE synced_at IS NULL ORDER BY created_at',
    );
    return [for (final r in rows) _toOp(r)];
  }

  /// Stamp ops the server acked as CAPTURED (durably in the inbox). The op row
  /// stays (as history) and so does its overlay/create -- the feature keeps
  /// rendering its local truth until a fresh pack lands, since the current pack's
  /// snapshot predates the edit. Seeds apply_status='received' (the op is now
  /// in-flight, awaiting the worker's apply outcome) without clobbering a status a
  /// concurrent pull may already have written.
  void markSynced(Iterable<String> opIds, DateTime syncedAt) {
    final ids = opIds.toList(growable: false);
    if (ids.isEmpty) return;
    _inTx(() {
      final ts = syncedAt.toIso8601String();
      for (final id in ids) {
        _db.execute(
          "UPDATE outbox SET synced_at = ?, "
          "apply_status = COALESCE(apply_status, 'received') WHERE op_id = ?",
          [ts, id],
        );
      }
    });
  }

  // ---- Apply-status reconcile (Phase 23 Slice 2) ----------------------------

  /// The non-terminal apply statuses: an op carrying one of these (or a null
  /// status -- captured but not yet polled) is still in flight and worth polling.
  /// Every OTHER value is terminal and dropped from the poll set: the successes/
  /// failures ('applied'/'rejected'/'conflict'), the silent 'superseded' (Slice
  /// 4c -- a newer edit to the same feature won), and any future/unknown status.
  /// Centralized so the poll query and this contract can't drift apart.
  static const List<String> inFlightApplyStatuses = [
    'received',
    'applying',
    'pending_review',
  ];

  /// Op ids captured server-side but not yet known-terminal -- the set to poll via
  /// GET /sync/status. An op is in flight once POST /sync acked it (synced_at set)
  /// while its apply_status is still one of [inFlightApplyStatuses] (or null, just
  /// after capture). Terminal ops -- applied/rejected/conflict and the silently
  /// dropped 'superseded' (Slice 4c) -- fall out of the whitelist, so the device
  /// stops polling them; nothing is left to learn.
  List<String> inFlightOpIds() {
    final placeholders =
        List.filled(inFlightApplyStatuses.length, '?').join(', ');
    final rows = _db.select(
      'SELECT op_id FROM outbox WHERE synced_at IS NOT NULL AND '
      '(apply_status IS NULL OR apply_status IN ($placeholders))',
      inFlightApplyStatuses,
    );
    return [for (final r in rows) r['op_id'] as String];
  }

  /// Record the apply worker's outcome for one op (from a status pull). Only
  /// touches a row we already have; a status for an op_id we don't know (never
  /// ours) updates nothing.
  void recordApplyStatus(String opId, String status, String? error) {
    _db.execute(
      'UPDATE outbox SET apply_status = ?, apply_error = ? WHERE op_id = ?',
      [status, error, opId],
    );
  }

  /// Ops the apply worker quarantined (rejected/conflict) that the user hasn't
  /// acknowledged yet -- the "needs attention" list. Oldest first. 'superseded' is
  /// intentionally NOT here (Slice 4c): a superseded op was silently replaced by a
  /// newer edit to the same feature, so it's terminal but needs no attention.
  List<OutboxOp> attentionOps() {
    final rows = _db.select(
      'SELECT op_id, feature_id, feature_class, kind, payload, base_version, '
      'created_at, synced_at, apply_status, apply_error FROM outbox '
      "WHERE apply_status IN ('rejected', 'conflict') AND dismissed_at IS NULL "
      'ORDER BY created_at',
    );
    return [for (final r in rows) _toOp(r)];
  }

  /// Count of un-acknowledged quarantined ops -- drives the attention badge.
  int attentionCount() {
    final rows = _db.select(
      "SELECT COUNT(*) AS n FROM outbox WHERE apply_status IN "
      "('rejected', 'conflict') AND dismissed_at IS NULL",
    );
    return (rows.first['n'] as int?) ?? 0;
  }

  /// Acknowledge a quarantined op: it drops off the attention list but the row is
  /// kept as history (the server retains the authoritative inbox record). The user
  /// re-does the edit to submit a fresh op with a new op_id.
  void dismissOp(String opId, DateTime dismissedAt) {
    _db.execute('UPDATE outbox SET dismissed_at = ? WHERE op_id = ?',
        [dismissedAt.toIso8601String(), opId]);
  }

  /// Feature ids that still have an un-synced op -- the store's "pending" set,
  /// which drives the amber/green map tint and the panel's "not synced" chip.
  Set<String> pendingFeatureIds() {
    final rows = _db
        .select('SELECT DISTINCT feature_id FROM outbox WHERE synced_at IS NULL');
    return {for (final r in rows) r['feature_id'] as String};
  }

  /// Feature ids whose local edits are FULLY synced -- they have at least one
  /// acked op and nothing still pending. These are the rows a freshly downloaded
  /// pack can make redundant: once the new snapshot contains the feature, its
  /// local overlay/create is just a stale duplicate (the source of the lingering
  /// green/amber tint). A feature with any pending op is deliberately excluded so
  /// unpushed work is never touched. See PackStore.reconcileSyncedEdits.
  List<String> syncedFeatureIds() {
    final rows = _db.select(
      'SELECT DISTINCT feature_id FROM outbox WHERE synced_at IS NOT NULL '
      'AND feature_id NOT IN '
      '(SELECT feature_id FROM outbox WHERE synced_at IS NULL)',
    );
    return [for (final r in rows) r['feature_id'] as String];
  }

  /// Drop the local overlay/create AND the synced outbox history for [featureIds]
  /// -- called once a fresh pack is confirmed to contain the server truth for
  /// them, so the on-device copy is pure redundancy. Only pass ids from
  /// [syncedFeatureIds] (never a feature with pending work): this deletes the
  /// synced op rows too, so an accidental call on a pending feature would lose an
  /// unpushed edit.
  void dropSynced(Iterable<String> featureIds) {
    final ids = featureIds.toList(growable: false);
    if (ids.isEmpty) return;
    _inTx(() {
      for (final id in ids) {
        _db.execute('DELETE FROM overlays WHERE feature_id = ?', [id]);
        _db.execute('DELETE FROM creates WHERE feature_id = ?', [id]);
        _db.execute(
          'DELETE FROM outbox WHERE feature_id = ? AND synced_at IS NOT NULL',
          [id],
        );
      }
    });
  }

  // ---- Image attachments (Phase 22 Slice 5) ---------------------------------

  /// Every captured attachment (synced or not) -- loaded once at open into the
  /// store's in-memory map so the detail panel's grid is a pure-memory read.
  List<LocalAttachment> allLocalAttachments() {
    final rows = _db.select(
      'SELECT id, feature_class, feature_id, filename, content_type, byte_size, '
      'local_path, created_at, synced_at FROM local_attachments ORDER BY created_at',
    );
    return [for (final r in rows) _toAttachment(r)];
  }

  /// Insert a captured attachment. Keyed by the client UUID, so a re-put with the
  /// same id (shouldn't happen -- each capture mints a fresh id) is an upsert.
  void putLocalAttachment(LocalAttachment a) {
    _db.execute(
      'INSERT OR REPLACE INTO local_attachments '
      '(id, feature_class, feature_id, filename, content_type, byte_size, '
      'local_path, created_at, synced_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)',
      [
        a.id,
        a.featureClass,
        a.featureId,
        a.filename,
        a.contentType,
        a.byteSize,
        a.localPath,
        a.createdAt.toIso8601String(),
        a.syncedAt?.toIso8601String(),
      ],
    );
  }

  /// Un-uploaded attachments, oldest first -- the push order for the sync flow.
  List<LocalAttachment> pendingLocalAttachments() {
    final rows = _db.select(
      'SELECT id, feature_class, feature_id, filename, content_type, byte_size, '
      'local_path, created_at, synced_at FROM local_attachments '
      'WHERE synced_at IS NULL ORDER BY created_at',
    );
    return [for (final r in rows) _toAttachment(r)];
  }

  /// Stamp an attachment uploaded (register -> PUT -> commit all landed). The row
  /// and its local file are KEPT so the image keeps rendering from the local copy
  /// until a fresh pack carries the server thumbnail (mirrors markSynced for
  /// features); the fresh-pack reconcile prunes the redundant local copy later.
  void markAttachmentSynced(String id, DateTime syncedAt) {
    _db.execute('UPDATE local_attachments SET synced_at = ? WHERE id = ?',
        [syncedAt.toIso8601String(), id]);
  }

  /// Drop a captured attachment row (the caller deletes the file). Used to discard
  /// an un-uploaded capture, or to clean up attachments of a discarded create.
  void deleteLocalAttachment(String id) {
    _db.execute('DELETE FROM local_attachments WHERE id = ?', [id]);
  }

  /// Every FULLY uploaded attachment (synced_at set) -- the ones a freshly
  /// downloaded pack can make redundant, since the pack now carries the server
  /// thumbnail. Returned whole (not just ids) so the caller has each [localPath]
  /// to delete the on-device file alongside the row. Pending captures are excluded
  /// so an un-uploaded photo is never swept. Mirrors [syncedFeatureIds] for
  /// features; see PackStore.reconcileSyncedEdits.
  List<LocalAttachment> syncedAttachments() {
    final rows = _db.select(
      'SELECT id, feature_class, feature_id, filename, content_type, byte_size, '
      'local_path, created_at, synced_at FROM local_attachments '
      'WHERE synced_at IS NOT NULL ORDER BY created_at',
    );
    return [for (final r in rows) _toAttachment(r)];
  }

  /// Drop the given synced attachment rows once a fresh pack is confirmed to carry
  /// them (the caller deletes each file first). Only pass ids from
  /// [syncedAttachments] -- this never guards on synced_at, so an accidental call
  /// on a pending capture would drop an un-uploaded photo.
  void dropSyncedAttachments(Iterable<String> ids) {
    final list = ids.toList(growable: false);
    if (list.isEmpty) return;
    _inTx(() {
      for (final id in list) {
        _db.execute('DELETE FROM local_attachments WHERE id = ?', [id]);
      }
    });
  }

  // ---- Connections (Phase 21 Slice 13) --------------------------------------

  /// Every established connection (synced or not) -- loaded once at open into the
  /// store's in-memory map so the panel's neighbour list and the map's local-edge
  /// overlay are pure-memory reads.
  List<LocalConnection> allConnections() {
    final rows = _db.select(
      'SELECT id, from_class, from_id, from_terminal, to_class, to_id, '
      'to_terminal, base_version, created_at FROM connections ORDER BY created_at',
    );
    return [for (final r in rows) _toConnection(r)];
  }

  /// Insert a connection AND its pending establish_connection op in one tx, so the
  /// rendered edge (connections row) and what gets pushed (outbox op) can't drift.
  /// Unlike putEdit/putCreate there is NO delete-then-insert: connections are never
  /// deduped by feature -- each is its own fact. [payload] is the establish_connection
  /// wire shape ({"from": {...}, "to": {...}}) POST /sync replays verbatim. The op's
  /// feature_id is the FROM endpoint (an arbitrary but stable anchor for the op).
  void putConnection(
    LocalConnection c, {
    required Map<String, dynamic> payload,
  }) {
    _inTx(() {
      _db.execute(
        'INSERT OR REPLACE INTO connections '
        '(id, from_class, from_id, from_terminal, to_class, to_id, to_terminal, '
        'base_version, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)',
        [
          c.id,
          c.fromClass,
          c.fromId,
          c.fromTerminal,
          c.toClass,
          c.toId,
          c.toTerminal,
          c.baseVersion,
          c.createdAt.toIso8601String(),
        ],
      );
      _db.execute(
        'INSERT INTO outbox '
        '(op_id, feature_id, feature_class, kind, payload, base_version, created_at, synced_at) '
        "VALUES (?, ?, ?, 'establish_connection', ?, ?, ?, NULL)",
        [
          c.id,
          c.fromId,
          c.fromClass,
          jsonEncode(payload),
          c.baseVersion,
          c.createdAt.toIso8601String(),
        ],
      );
    });
  }

  /// Discard a connection: drop its row AND its still-pending op. A synced op is
  /// history and is left alone (the UI only offers discard on un-synced ones), so
  /// this never rewrites what the server already accepted.
  void discardConnection(String id) {
    _inTx(() {
      _db.execute('DELETE FROM connections WHERE id = ?', [id]);
      _db.execute(
        'DELETE FROM outbox WHERE op_id = ? AND synced_at IS NULL',
        [id],
      );
    });
  }

  /// Connection ids whose establish op the server has acked -- the rows a freshly
  /// downloaded pack can make redundant once its connectivity graph carries the
  /// edge. A connection with a still-pending op is excluded so unpushed work is
  /// never touched. See PackStore.reconcileSyncedEdits (wired in Slice 13b).
  List<String> syncedConnectionIds() {
    final rows = _db.select(
      'SELECT c.id FROM connections c JOIN outbox o ON o.op_id = c.id '
      'WHERE o.synced_at IS NOT NULL',
    );
    return [for (final r in rows) r['id'] as String];
  }

  /// Synced connections as (id, fromId, toId) -- the reconcile needs both endpoints
  /// to confirm the fresh pack's connectivity graph carries each edge before it
  /// drops the redundant local row. Pending (un-pushed) connections are excluded.
  List<({String id, String fromId, String toId})> syncedConnectionEdges() {
    final rows = _db.select(
      'SELECT c.id, c.from_id, c.to_id FROM connections c '
      'JOIN outbox o ON o.op_id = c.id WHERE o.synced_at IS NOT NULL',
    );
    return [
      for (final r in rows)
        (
          id: r['id'] as String,
          fromId: r['from_id'] as String,
          toId: r['to_id'] as String,
        ),
    ];
  }

  /// Drop the given synced connection rows AND their synced op history once a fresh
  /// pack is confirmed to carry them. Only pass ids from [syncedConnectionIds].
  void dropConnections(Iterable<String> ids) {
    final list = ids.toList(growable: false);
    if (list.isEmpty) return;
    _inTx(() {
      for (final id in list) {
        _db.execute('DELETE FROM connections WHERE id = ?', [id]);
        _db.execute(
          'DELETE FROM outbox WHERE op_id = ? AND synced_at IS NOT NULL',
          [id],
        );
      }
    });
  }

  LocalConnection _toConnection(Row r) {
    return LocalConnection(
      id: r['id'] as String,
      fromClass: r['from_class'] as String,
      fromId: r['from_id'] as String,
      fromTerminal: r['from_terminal'] as String?,
      toClass: r['to_class'] as String,
      toId: r['to_id'] as String,
      toTerminal: r['to_terminal'] as String?,
      baseVersion: r['base_version'] as String?,
      createdAt:
          DateTime.tryParse(r['created_at'] as String? ?? '') ?? DateTime.now(),
    );
  }

  // ---- Relationships (Phase 21 Slice 14) ------------------------------------

  /// Every relationship established offline -- loaded once at open into the store.
  List<LocalRelationship> allRelationships() {
    final rows = _db.select(
      'SELECT id, origin_class, origin_id, destination_class, destination_id, '
      'kind, base_version, created_at FROM relationships ORDER BY created_at',
    );
    return [for (final r in rows) _toRelationship(r)];
  }

  /// Insert a relationship AND its pending establish_relationship op in one tx, so
  /// the rendered edge (relationships row) and what gets pushed (outbox op) can't
  /// drift. Like putConnection there is NO delete-then-insert -- relationships are
  /// never deduped by feature. [payload] is the establish_relationship wire shape
  /// ({"origin": {...}, "destination": {...}, "kind"?}) POST /sync replays verbatim.
  /// The op's feature_id is the ORIGIN endpoint (an arbitrary but stable anchor).
  void putRelationship(
    LocalRelationship r, {
    required Map<String, dynamic> payload,
  }) {
    _inTx(() {
      _db.execute(
        'INSERT OR REPLACE INTO relationships '
        '(id, origin_class, origin_id, destination_class, destination_id, kind, '
        'base_version, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)',
        [
          r.id,
          r.originClass,
          r.originId,
          r.destinationClass,
          r.destinationId,
          r.kind,
          r.baseVersion,
          r.createdAt.toIso8601String(),
        ],
      );
      _db.execute(
        'INSERT INTO outbox '
        '(op_id, feature_id, feature_class, kind, payload, base_version, created_at, synced_at) '
        "VALUES (?, ?, ?, 'establish_relationship', ?, ?, ?, NULL)",
        [
          r.id,
          r.originId,
          r.originClass,
          jsonEncode(payload),
          r.baseVersion,
          r.createdAt.toIso8601String(),
        ],
      );
    });
  }

  /// Discard a relationship: drop its row AND its still-pending op. A synced op is
  /// history and is left alone, so this never rewrites what the server accepted.
  void discardRelationship(String id) {
    _inTx(() {
      _db.execute('DELETE FROM relationships WHERE id = ?', [id]);
      _db.execute(
        'DELETE FROM outbox WHERE op_id = ? AND synced_at IS NULL',
        [id],
      );
    });
  }

  /// Synced relationships as (id, originId, destinationId) -- the reconcile needs
  /// both endpoints to confirm the fresh pack's relationship graph carries each edge
  /// before dropping the redundant local row. Pending relationships are excluded.
  List<({String id, String originId, String destinationId})>
      syncedRelationshipEdges() {
    final rows = _db.select(
      'SELECT r.id, r.origin_id, r.destination_id FROM relationships r '
      'JOIN outbox o ON o.op_id = r.id WHERE o.synced_at IS NOT NULL',
    );
    return [
      for (final r in rows)
        (
          id: r['id'] as String,
          originId: r['origin_id'] as String,
          destinationId: r['destination_id'] as String,
        ),
    ];
  }

  /// Drop the given synced relationship rows AND their synced op history once a
  /// fresh pack is confirmed to carry them. Only pass ids from the synced set.
  void dropRelationships(Iterable<String> ids) {
    final list = ids.toList(growable: false);
    if (list.isEmpty) return;
    _inTx(() {
      for (final id in list) {
        _db.execute('DELETE FROM relationships WHERE id = ?', [id]);
        _db.execute(
          'DELETE FROM outbox WHERE op_id = ? AND synced_at IS NOT NULL',
          [id],
        );
      }
    });
  }

  LocalRelationship _toRelationship(Row r) {
    return LocalRelationship(
      id: r['id'] as String,
      originClass: r['origin_class'] as String,
      originId: r['origin_id'] as String,
      destinationClass: r['destination_class'] as String,
      destinationId: r['destination_id'] as String,
      kind: r['kind'] as String?,
      baseVersion: r['base_version'] as String?,
      createdAt:
          DateTime.tryParse(r['created_at'] as String? ?? '') ?? DateTime.now(),
    );
  }

  LocalAttachment _toAttachment(Row r) {
    final synced = r['synced_at'] as String?;
    return LocalAttachment(
      id: r['id'] as String,
      featureClass: r['feature_class'] as String,
      featureId: r['feature_id'] as String,
      filename: r['filename'] as String,
      contentType: r['content_type'] as String,
      byteSize: (r['byte_size'] as num?)?.toInt() ?? 0,
      localPath: r['local_path'] as String,
      createdAt:
          DateTime.tryParse(r['created_at'] as String? ?? '') ?? DateTime.now(),
      syncedAt: synced == null ? null : DateTime.tryParse(synced),
    );
  }

  FeatureOverlay _toOverlay(Row r) {
    final attrsText = r['attrs'] as String?;
    Map<String, dynamic> attrs = const {};
    if (attrsText != null && attrsText.isNotEmpty) {
      final decoded = jsonDecode(attrsText);
      if (decoded is Map<String, dynamic>) attrs = decoded;
    }
    final geom = r['geom'];
    final geomJsonText = r['geom_json'] as String?;
    Map<String, dynamic>? geomJson;
    if (geomJsonText != null && geomJsonText.isNotEmpty) {
      final decoded = jsonDecode(geomJsonText);
      if (decoded is Map<String, dynamic>) geomJson = decoded;
    }
    return FeatureOverlay(
      featureId: r['feature_id'] as String,
      featureClass: r['feature_class'] as String,
      attrs: attrs,
      updatedAt:
          DateTime.tryParse(r['updated_at'] as String? ?? '') ?? DateTime.now(),
      geomJson: geomJson,
      geomBytes: geom is Uint8List ? geom : null,
      name: r['name'] as String?,
    );
  }

  LocalCreate _toCreate(Row r) {
    Map<String, dynamic> decodeMap(String? text) {
      if (text == null || text.isEmpty) return const {};
      final decoded = jsonDecode(text);
      return decoded is Map<String, dynamic> ? decoded : const {};
    }

    final subtype = r['subtype'];
    return LocalCreate(
      featureId: r['feature_id'] as String,
      featureClass: r['feature_class'] as String,
      subtype: subtype is int ? subtype : (subtype as num?)?.toInt(),
      geometry: decodeMap(r['geom_json'] as String?),
      attrs: decodeMap(r['attrs'] as String?),
      name: r['name'] as String?,
      baseVersion: r['base_version'] as String?,
      createdAt:
          DateTime.tryParse(r['created_at'] as String? ?? '') ?? DateTime.now(),
    );
  }

  OutboxOp _toOp(Row r) {
    final payloadText = r['payload'] as String?;
    Map<String, dynamic> payload = const {};
    if (payloadText != null && payloadText.isNotEmpty) {
      final decoded = jsonDecode(payloadText);
      if (decoded is Map<String, dynamic>) payload = decoded;
    }
    final syncedAt = r['synced_at'] as String?;
    return OutboxOp(
      opId: r['op_id'] as String,
      featureId: r['feature_id'] as String,
      featureClass: r['feature_class'] as String,
      kind: r['kind'] as String,
      payload: payload,
      baseVersion: r['base_version'] as String?,
      createdAt:
          DateTime.tryParse(r['created_at'] as String? ?? '') ?? DateTime.now(),
      syncedAt: syncedAt == null ? null : DateTime.tryParse(syncedAt),
      applyStatus: r['apply_status'] as String?,
      applyError: r['apply_error'] as String?,
    );
  }

  /// Wrap [body] in a BEGIN/COMMIT, rolling back on any error so a half-written
  /// overlay/outbox pair never survives.
  void _inTx(void Function() body) {
    _db.execute('BEGIN');
    try {
      body();
      _db.execute('COMMIT');
    } catch (_) {
      try {
        _db.execute('ROLLBACK');
      } catch (_) {}
      rethrow;
    }
  }

  void close() => _db.dispose();
}
