import 'dart:io';

import 'package:flutter/foundation.dart';

import '../api/models.dart';
import '../util/uuid.dart';
import 'edit_database.dart';

/// EditStore is the reactive face of the offline-edit layer (Phase 21 Slice 3a).
/// It owns an [EditDatabase], mirrors the overlay rows in memory for zero-cost
/// dirty/merge reads, and is a [ChangeNotifier] so the map (dirty overlay) and
/// the detail panel (merged values + "pending" chip) rebuild the instant an edit
/// lands. The persistence + delta math live in EditDatabase; this is the API the
/// widgets touch.
class EditStore extends ChangeNotifier {
  EditStore._(this._db, this._baseVersion);

  final EditDatabase _db;

  /// The pack's snapshot_at, stamped onto every outbox op so the server's LWW
  /// resolve (Slice 3c) can weigh the edit against the current server state.
  final String? _baseVersion;

  final Map<String, FeatureOverlay> _overlays = {};
  final Map<String, LocalCreate> _creates = {};

  /// Packed features DELETED offline (Phase 21 Slice 15), keyed by the feature's
  /// server id. Mirrors the `deletes` tombstone table so the map's hidden set is a
  /// pure-memory read. Kept after sync (like overlays/creates) until a fresh pack
  /// no longer carries the row (see PackStore.reconcileSyncedDeletes).
  final Map<String, LocalDelete> _deletes = {};

  /// Captured image attachments (Phase 22 Slice 5), grouped by target feature id.
  /// Mirrors the local_attachments rows so the detail panel's grid + the sync
  /// badge are pure-memory reads. Both synced and un-uploaded rows are kept (an
  /// uploaded one still renders from its local file until a fresh pack lands).
  final Map<String, List<LocalAttachment>> _attachments = {};

  /// Connections established offline (Phase 21 Slice 13), keyed by connection id
  /// (== the establish_connection outbox op id). Mirrors the `connections` table so
  /// the map's local-edge overlay and the panel's neighbour list are pure-memory
  /// reads. Kept after sync (like overlays/creates) until a fresh pack carries the
  /// server edge -- the current pack's connectivity graph predates it.
  final Map<String, LocalConnection> _connections = {};

  /// Relationships established offline (Phase 21 Slice 14), keyed by relationship id
  /// (== the establish_relationship outbox op id). The relationship sibling of
  /// [_connections]: mirrors the `relationships` table so the map's local-edge
  /// overlay and the panel's neighbour list are pure-memory reads, kept after sync
  /// until a fresh pack carries the server edge.
  final Map<String, LocalRelationship> _relationships = {};

  /// Feature ids with an un-synced outbox op -- the "pending" set. An overlay or
  /// create leaves this set once POST /sync acks its op (markSynced), but its
  /// row is kept so the feature still renders its local truth until a fresh pack
  /// lands. So "has an overlay/create" (display) and "is pending" (sync state)
  /// are deliberately separate.
  final Set<String> _pending = {};

  /// Count of quarantined ops the user hasn't acknowledged (Phase 23 Slice 2):
  /// edits POST /sync captured but the apply worker rejected. Cached from the DB so
  /// the attention badge is a pure-memory read; refreshed whenever a status pull
  /// records outcomes or the user dismisses one.
  int _attentionCount = 0;
  int _opSeq = 0;

  /// Open the edit DB beside the pack and hydrate the in-memory overlay + create
  /// maps and the pending set.
  factory EditStore.open(String path, {String? baseVersion}) {
    final db = EditDatabase.open(path);
    final store = EditStore._(db, baseVersion);
    for (final o in db.allOverlays()) {
      store._overlays[o.featureId] = o;
    }
    for (final c in db.allCreates()) {
      store._creates[c.featureId] = c;
    }
    for (final a in db.allLocalAttachments()) {
      (store._attachments[a.featureId] ??= <LocalAttachment>[]).add(a);
    }
    for (final d in db.allDeletes()) {
      store._deletes[d.featureId] = d;
    }
    for (final c in db.allConnections()) {
      store._connections[c.id] = c;
    }
    for (final r in db.allRelationships()) {
      store._relationships[r.id] = r;
    }
    store._pending.addAll(db.pendingFeatureIds());
    store._attentionCount = db.attentionCount();
    return store;
  }

  /// Ids of every EDITED packed feature that's still un-synced -- the map paints
  /// these amber. A synced edit keeps its overlay (so the panel still shows the
  /// new value) but drops off the amber set.
  Set<String> get dirtyIds =>
      {for (final id in _overlays.keys) if (_pending.contains(id)) id};

  /// Ids of every packed feature whose GEOMETRY was moved (Slice 10). These
  /// features must be hidden from the base pack layers (whose tiles still hold the
  /// original shape) and redrawn from their overlay geometry -- see PackMapView's
  /// hiddenIds. A moved feature is also in [dirtyIds] until synced.
  Set<String> get movedIds =>
      {for (final e in _overlays.entries) if (e.value.geomJson != null) e.key};

  /// The moved geometry (GeoJSON) for a feature, or null if it wasn't moved.
  Map<String, dynamic>? geomEditFor(String id) => _overlays[id]?.geomJson;

  /// Ids of every feature CREATED offline -- the map paints these green. Kept
  /// even after sync: the current pack's snapshot predates the create, so the
  /// feature would vanish until a fresh pack if we dropped it.
  Set<String> get createdIds => _creates.keys.toSet();

  /// Ids of every packed feature DELETED offline (Phase 21 Slice 15). The map
  /// hides these from the base pack layers (they feed PackMapView's hiddenIds) and
  /// never redraws them -- a delete is a tombstone, unlike a move (hidden AND
  /// redrawn). Kept even after sync until a fresh pack drops the row.
  Set<String> get deletedIds => _deletes.keys.toSet();

  /// Count of features deleted offline (synced or not) -- the map settings
  /// "deleted" tally. Kept until a fresh pack, so this counts the local tombstone
  /// set, not just the pending ones.
  int get deletedCount => _deletes.length;

  bool isDeleted(String id) => _deletes.containsKey(id);

  /// Count of features with un-synced local changes -- drives the sync badge.
  int get pendingCount => _pending.length;

  /// Count of features CREATED offline (synced or not) -- the map settings
  /// "new" tally. A create keeps its row until a fresh pack, so this counts the
  /// local render set, not just the pending ones.
  int get createdCount => _creates.length;

  /// Count of packed features with a local attribute edit -- the settings
  /// "modified" tally.
  int get modifiedCount => _overlays.length;

  bool isDirty(String id) => _overlays.containsKey(id);

  bool isCreated(String id) => _creates.containsKey(id);

  /// Whether a feature still has un-synced local changes to push.
  bool isPending(String id) => _pending.contains(id);

  FeatureOverlay? overlayFor(String id) => _overlays[id];

  LocalCreate? createFor(String id) => _creates[id];

  /// Every offline-created feature (for the map's green overlay + tap hit-test).
  List<LocalCreate> get creates => _creates.values.toList(growable: false);

  /// The values to SHOW for a feature: the packed attributes with the local delta
  /// laid over them. Returns [packAttrs] unchanged when there's no edit.
  Map<String, dynamic> mergedAttributes(
      String id, Map<String, dynamic> packAttrs) {
    final o = _overlays[id];
    if (o == null || o.attrs.isEmpty) return packAttrs;
    return {...packAttrs, ...o.attrs};
  }

  /// Persist an attribute edit as a DELTA against the packed values. [editedAttrs]
  /// is the full set of values the form manages; only keys that actually differ
  /// from [packAttrs] become the delta (an explicit null clears a field). When
  /// every change is reverted the delta is empty and the overlay is dropped.
  Future<void> saveAttrEdit({
    required String featureId,
    required String featureClass,
    required Map<String, dynamic> packAttrs,
    required Map<String, dynamic> editedAttrs,
  }) async {
    final delta = <String, dynamic>{};
    for (final e in editedAttrs.entries) {
      if (!_deepEquals(packAttrs[e.key], e.value)) delta[e.key] = e.value;
    }

    // A geometry move (Slice 10) is stored on the same overlay. Preserve it: an
    // attribute save re-passes the existing geomJson, and the overlay is only
    // dropped when there's NOTHING left (no delta AND no move).
    final geomJson = _overlays[featureId]?.geomJson;

    if (delta.isEmpty && geomJson == null) {
      if (_overlays.remove(featureId) != null) {
        _db.clearFeature(featureId);
        _pending.remove(featureId);
        notifyListeners();
      }
      return;
    }

    final now = DateTime.now().toUtc();
    final opId = _nextOpId(featureId);
    _db.putEdit(
      featureId: featureId,
      featureClass: featureClass,
      delta: delta,
      geomJson: geomJson,
      opId: opId,
      payload: {
        if (delta.isNotEmpty) 'attributes': delta,
        if (geomJson != null) 'geometry': geomJson,
      },
      baseVersion: _baseVersion,
      updatedAt: now,
    );
    _overlays[featureId] = FeatureOverlay(
      featureId: featureId,
      featureClass: featureClass,
      attrs: delta,
      geomJson: geomJson,
      updatedAt: now,
    );
    _pending.add(featureId);
    notifyListeners();
  }

  /// Persist a GEOMETRY move (Slice 10 vertex editor): the feature's vertices were
  /// dragged / added / removed and [geometry] is the new GeoJSON geometry object
  /// (Point or LineString). Rides the SAME overlay + 'update_attrs' op as an
  /// attribute edit -- crud.UpdateFeatureInput reads geometry and attributes from
  /// one payload -- so any pending attribute delta is preserved and pushed with it.
  /// Only packed features reach here (a created feature edits its geometry through
  /// the create form), so there's always a packed truth to move against.
  ///
  /// [gpsCapture] is the GNSS survey-quality snapshot when the vertex was moved to
  /// the device's position (a GPS-sourced move), null for a drag on the map. It
  /// nests under the op payload's `gps_capture` key and is persisted server-side
  /// alongside the update, one row per GPS move (Phase gnss-quality Slice 3).
  Future<void> saveGeometryEdit({
    required String featureId,
    required String featureClass,
    required Map<String, dynamic> geometry,
    Map<String, dynamic>? gpsCapture,
  }) async {
    final delta = _overlays[featureId]?.attrs ?? const <String, dynamic>{};
    final now = DateTime.now().toUtc();
    final opId = _nextOpId(featureId);
    _db.putEdit(
      featureId: featureId,
      featureClass: featureClass,
      delta: delta,
      geomJson: geometry,
      opId: opId,
      payload: {
        'geometry': geometry,
        if (delta.isNotEmpty) 'attributes': delta,
        if (gpsCapture != null) 'gps_capture': gpsCapture,
      },
      baseVersion: _baseVersion,
      updatedAt: now,
    );
    _overlays[featureId] = FeatureOverlay(
      featureId: featureId,
      featureClass: featureClass,
      attrs: delta,
      geomJson: geometry,
      updatedAt: now,
    );
    _pending.add(featureId);
    notifyListeners();
  }

  /// Persist a feature created offline: mint (or reuse) a client UUID, write the
  /// `creates` row, and queue a single 'create' outbox op whose [payload] is in
  /// crud.CreateFeatureInput shape (plus an `id` the sync slice will honour). Pass
  /// [featureId] to re-save an existing offline create in place (editing its
  /// attributes before sync) -- omit it to mint a brand-new feature. Returns the
  /// feature's id so the caller can select it on the map.
  ///
  /// [gpsCapture] is the GNSS survey-quality snapshot (GnssSample.toCaptureJson())
  /// when this feature was placed at the device's position -- the field tool's
  /// "place at my location". It's null for a map-tap placement (no GPS behind it).
  /// When present it nests under the op payload's `gps_capture` key; the server's
  /// applySyncOp persists it to gis.feature_gps_captures inside the create's
  /// transaction, one row per GPS placement (Phase gnss-quality Slice 3).
  Future<String> createFeature({
    required String featureClass,
    required int? subtype,
    required Map<String, dynamic> geometry,
    required Map<String, dynamic> attributes,
    String? name,
    String? featureId,
    Map<String, dynamic>? gpsCapture,
  }) async {
    final id = featureId ?? newUuidV4();
    final now = DateTime.now().toUtc();
    final trimmedName = name?.trim();
    final payload = <String, dynamic>{
      'id': id,
      if (subtype != null) 'subtype': subtype,
      if (trimmedName != null && trimmedName.isNotEmpty) 'name': trimmedName,
      'geometry': geometry,
      'attributes': attributes,
      if (gpsCapture != null) 'gps_capture': gpsCapture,
    };
    _db.putCreate(
      featureId: id,
      featureClass: featureClass,
      subtype: subtype,
      geometry: geometry,
      attrs: attributes,
      name: (trimmedName != null && trimmedName.isNotEmpty) ? trimmedName : null,
      opId: _nextOpId(id),
      payload: payload,
      baseVersion: _baseVersion,
      createdAt: now,
    );
    _creates[id] = LocalCreate(
      featureId: id,
      featureClass: featureClass,
      subtype: subtype,
      geometry: geometry,
      attrs: attributes,
      name: (trimmedName != null && trimmedName.isNotEmpty) ? trimmedName : null,
      baseVersion: _baseVersion,
      createdAt: now,
    );
    _pending.add(id);
    notifyListeners();
    return id;
  }

  /// Discard an offline create (and its pending op) entirely -- there's no server
  /// truth to fall back to, so the feature simply disappears.
  Future<void> discardCreate(String featureId) async {
    if (_creates.remove(featureId) != null) {
      _db.clearFeature(featureId);
      _pending.remove(featureId);
      // Drop any captured attachments queued against this (never-synced) feature
      // -- there's no server feature to upload them to. Delete their files too.
      final atts = _attachments.remove(featureId) ?? const <LocalAttachment>[];
      for (final a in atts) {
        _db.deleteLocalAttachment(a.id);
        await _deleteFile(a.localPath);
      }
      notifyListeners();
    }
  }

  // ---- Deletes (Phase 21 Slice 15) ------------------------------------------

  /// Delete a PACKED feature offline: write its tombstone + a pending 'delete'
  /// outbox op, drop any in-memory overlay (a delete supersedes a pending attr
  /// edit), and mark the feature pending. Only packed features reach here -- an
  /// offline create is discarded instead (discardCreate), since there's no server
  /// row to soft-delete. Optimistic: POST /sync replays the delete through the
  /// server's soft-delete (idempotent), the zero-trust authority.
  Future<void> deleteFeature({
    required String featureId,
    required String featureClass,
  }) async {
    final now = DateTime.now().toUtc();
    _db.putDelete(
      featureId: featureId,
      featureClass: featureClass,
      opId: _nextOpId(featureId),
      baseVersion: _baseVersion,
      createdAt: now,
    );
    // A delete wins over an in-flight attribute edit (putDelete dropped its row +
    // pending op in the DB); mirror that in memory so modifiedCount/dirtyIds fall.
    _overlays.remove(featureId);
    _deletes[featureId] = LocalDelete(
      featureId: featureId,
      featureClass: featureClass,
      baseVersion: _baseVersion,
      createdAt: now,
    );
    _pending.add(featureId);
    notifyListeners();
  }

  /// Undo an offline delete before it syncs: drop the tombstone + its pending op so
  /// the feature reappears from the base pack. Recomputes the pending set from the
  /// DB, since the feature may still have other un-synced work queued elsewhere.
  Future<void> undoDelete(String featureId) async {
    if (_deletes.remove(featureId) == null) return;
    _db.discardDelete(featureId);
    _pending
      ..clear()
      ..addAll(_db.pendingFeatureIds());
    notifyListeners();
  }

  // ---- Connections (Phase 21 Slice 13) --------------------------------------

  /// Every connection established offline (for the map's local-edge overlay).
  List<LocalConnection> get connections =>
      _connections.values.toList(growable: false);

  /// Count of connections established offline -- the map settings tally.
  int get connectionCount => _connections.length;

  /// Connections with either endpoint at [featureId] -- folded into the detail
  /// panel's neighbour list beside the packed connections.
  List<LocalConnection> connectionsInvolving(String featureId) =>
      [for (final c in _connections.values) if (c.involves(featureId)) c];

  /// Establish a connection offline: mint a client UUID (which doubles as the
  /// outbox op id), write the `connections` row + its pending establish_connection
  /// op, and mirror it in memory. [fromTerminal]/[toTerminal] are the optional
  /// named terminal CODES either endpoint is wired at (the server resolves them via
  /// Loaded.ResolveTerminal). Optimistic: no offline rule check -- POST /sync's
  /// zero-trust engine (Slice 12) is the authority and rejects an illegal edge
  /// per-op on replay. Returns the connection id.
  Future<String> createConnection({
    required String fromClass,
    required String fromId,
    String? fromTerminal,
    required String toClass,
    required String toId,
    String? toTerminal,
  }) async {
    final id = newUuidV4();
    final now = DateTime.now().toUtc();
    final payload = <String, dynamic>{
      'from': {
        'feature_class': fromClass,
        'feature_id': fromId,
        'terminal': ?fromTerminal,
      },
      'to': {
        'feature_class': toClass,
        'feature_id': toId,
        'terminal': ?toTerminal,
      },
    };
    final conn = LocalConnection(
      id: id,
      fromClass: fromClass,
      fromId: fromId,
      fromTerminal: fromTerminal,
      toClass: toClass,
      toId: toId,
      toTerminal: toTerminal,
      baseVersion: _baseVersion,
      createdAt: now,
    );
    _db.putConnection(conn, payload: payload);
    _connections[id] = conn;
    _pending.add(fromId);
    notifyListeners();
    return id;
  }

  /// Discard a connection established offline (and its still-pending op). Recomputes
  /// the pending set from the DB afterwards, since the FROM feature may still have
  /// other un-synced work (an attr edit, another connection).
  Future<void> discardConnection(String id) async {
    if (_connections.remove(id) == null) return;
    _db.discardConnection(id);
    _pending
      ..clear()
      ..addAll(_db.pendingFeatureIds());
    notifyListeners();
  }

  // ---- Relationships (Phase 21 Slice 14) ------------------------------------

  /// Every relationship established offline (for the map's local-edge overlay).
  List<LocalRelationship> get relationships =>
      _relationships.values.toList(growable: false);

  /// Count of relationships established offline -- the map settings tally.
  int get relationshipCount => _relationships.length;

  /// Relationships with either endpoint at [featureId] -- folded into the detail
  /// panel's neighbour list beside the packed relationships.
  List<LocalRelationship> relationshipsInvolving(String featureId) =>
      [for (final r in _relationships.values) if (r.involves(featureId)) r];

  /// Establish a relationship offline: mint a client UUID (which doubles as the
  /// outbox op id), write the `relationships` row + its pending
  /// establish_relationship op, and mirror it in memory. [kind] is the optional
  /// relationship_class kind (null lets the server auto-resolve the single class for
  /// the origin/destination pair). Optimistic: no offline rule check -- POST /sync's
  /// zero-trust engine (Slice 12) is the authority and rejects an illegal or
  /// ambiguous relationship per-op on replay. Returns the relationship id.
  Future<String> createRelationship({
    required String originClass,
    required String originId,
    required String destinationClass,
    required String destinationId,
    String? kind,
  }) async {
    final id = newUuidV4();
    final now = DateTime.now().toUtc();
    final payload = <String, dynamic>{
      'origin': {
        'feature_class': originClass,
        'feature_id': originId,
      },
      'destination': {
        'feature_class': destinationClass,
        'feature_id': destinationId,
      },
      'kind': ?kind,
    };
    final rel = LocalRelationship(
      id: id,
      originClass: originClass,
      originId: originId,
      destinationClass: destinationClass,
      destinationId: destinationId,
      kind: kind,
      baseVersion: _baseVersion,
      createdAt: now,
    );
    _db.putRelationship(rel, payload: payload);
    _relationships[id] = rel;
    _pending.add(originId);
    notifyListeners();
    return id;
  }

  /// Discard a relationship established offline (and its still-pending op).
  /// Recomputes the pending set from the DB afterwards, since the ORIGIN feature may
  /// still have other un-synced work.
  Future<void> discardRelationship(String id) async {
    if (_relationships.remove(id) == null) return;
    _db.discardRelationship(id);
    _pending
      ..clear()
      ..addAll(_db.pendingFeatureIds());
    notifyListeners();
  }

  // ---- Image attachments (Phase 22 Slice 5) ---------------------------------

  /// The captured attachments for a feature, oldest first (both un-uploaded and
  /// already-synced -- each still renders from its on-device file).
  List<LocalAttachment> localAttachmentsFor(String id) =>
      List.unmodifiable(_attachments[id] ?? const <LocalAttachment>[]);

  /// Un-uploaded attachments across all features -- the sync flow's push list.
  List<LocalAttachment> pendingAttachments() => _db.pendingLocalAttachments();

  /// Count of captured attachments not yet uploaded -- folds into the sync badge.
  int get pendingAttachmentCount {
    var n = 0;
    for (final list in _attachments.values) {
      for (final a in list) {
        if (a.isPending) n++;
      }
    }
    return n;
  }

  /// Record a freshly captured image (already copied to [localPath] on device)
  /// and queue it for upload. Returns the minted attachment id.
  Future<String> addLocalAttachment({
    required String featureClass,
    required String featureId,
    required String filename,
    required String contentType,
    required int byteSize,
    required String localPath,
  }) async {
    final a = LocalAttachment(
      id: newUuidV4(),
      featureClass: featureClass,
      featureId: featureId,
      filename: filename,
      contentType: contentType,
      byteSize: byteSize,
      localPath: localPath,
      createdAt: DateTime.now().toUtc(),
    );
    _db.putLocalAttachment(a);
    (_attachments[featureId] ??= <LocalAttachment>[]).add(a);
    notifyListeners();
    return a.id;
  }

  /// Mark the given attachment ids uploaded after their register -> PUT -> commit
  /// landed, refreshing the in-memory copies so their "uploading" state clears.
  Future<void> markAttachmentsSynced(Iterable<String> ids) async {
    final list = ids.toList(growable: false);
    if (list.isEmpty) return;
    final now = DateTime.now().toUtc();
    for (final id in list) {
      _db.markAttachmentSynced(id, now);
    }
    _reloadAttachments();
    notifyListeners();
  }

  /// Discard a captured attachment (deletes its row and local file). Used before
  /// upload; a synced attachment is history and normally isn't discarded here.
  Future<void> discardLocalAttachment(String id) async {
    _db.deleteLocalAttachment(id);
    for (final list in _attachments.values) {
      final idx = list.indexWhere((a) => a.id == id);
      if (idx >= 0) {
        await _deleteFile(list[idx].localPath);
        list.removeAt(idx);
        break;
      }
    }
    notifyListeners();
  }

  void _reloadAttachments() {
    _attachments.clear();
    for (final a in _db.allLocalAttachments()) {
      (_attachments[a.featureId] ??= <LocalAttachment>[]).add(a);
    }
  }

  Future<void> _deleteFile(String path) async {
    try {
      final f = File(path);
      if (await f.exists()) await f.delete();
    } catch (_) {
      // Best-effort: a leftover file is harmless (the pack dir is reaped on
      // pack delete); never fail a discard over it.
    }
  }

  /// Pending push queue (POST /sync), oldest first.
  List<OutboxOp> pendingOps() => _db.pendingOps();

  /// Mark the given ops synced after POST /sync captured them, then recompute the
  /// pending set from what's left un-synced. Overlays/creates are intentionally
  /// kept (they're still the only local render of a feature the pack predates),
  /// so this only flips the pending state -- amber -> plain, chip off, badge down.
  /// The op is now IN FLIGHT (captured, awaiting the worker's apply); its outcome
  /// arrives later via [recordApplyStatuses].
  Future<void> markSynced(Iterable<String> opIds) async {
    final ids = opIds.toList(growable: false);
    if (ids.isEmpty) return;
    _db.markSynced(ids, DateTime.now().toUtc());
    _pending
      ..clear()
      ..addAll(_db.pendingFeatureIds());
    notifyListeners();
  }

  // ---- Apply-status reconcile (Phase 23 Slice 2) ----------------------------

  /// Count of quarantined ops awaiting the user's acknowledgement -- drives the
  /// "needs attention" badge. Distinct from [pendingCount] (un-pushed work): an op
  /// here was captured server-side but the apply worker rejected it.
  int get attentionCount => _attentionCount;

  /// Op ids captured server-side but not yet known-terminal -- the poll set the
  /// UI passes to the status pull (POST /sync `poll` or GET /sync/status).
  List<String> inFlightOpIds() => _db.inFlightOpIds();

  /// The quarantined ops to show in the "needs attention" list, oldest first.
  List<OutboxOp> attentionOps() => _db.attentionOps();

  /// Record the apply worker's outcomes (from a status pull) and refresh the
  /// attention tally so the badge/list update. A no-op ids-empty call is cheap.
  Future<void> recordApplyStatuses(Iterable<SyncOpStatus> statuses) async {
    var changed = false;
    for (final s in statuses) {
      if (s.opId.isEmpty) continue;
      _db.recordApplyStatus(s.opId, s.status, s.error);
      changed = true;
    }
    if (changed) {
      _attentionCount = _db.attentionCount();
      notifyListeners();
    }
  }

  /// Acknowledge a quarantined op -- it leaves the attention list (kept as history
  /// on device; the server retains the authoritative inbox record). The user re-does
  /// the edit to submit a fresh op with a new op_id.
  Future<void> dismissAttention(String opId) async {
    _db.dismissOp(opId, DateTime.now().toUtc());
    _attentionCount = _db.attentionCount();
    notifyListeners();
  }

  // op ids don't need to be UUIDs -- just unique idempotency keys. A monotonic
  // seq within this store instance plus a microsecond timestamp guarantees that
  // even two edits to the same feature in the same tick never collide.
  String _nextOpId(String featureId) =>
      '${DateTime.now().toUtc().microsecondsSinceEpoch}-${_opSeq++}-$featureId';

  @override
  void dispose() {
    _db.close();
    super.dispose();
  }
}

/// Structural equality over decoded-JSON scalars/lists/maps, so a delta only
/// captures REAL changes. `==` already handles num/String/bool/null (and Dart's
/// `5 == 5.0` is true), leaving only nested List/Map to walk by hand.
bool _deepEquals(dynamic a, dynamic b) {
  if (identical(a, b)) return true;
  if (a is Map && b is Map) {
    if (a.length != b.length) return false;
    for (final k in a.keys) {
      if (!b.containsKey(k) || !_deepEquals(a[k], b[k])) return false;
    }
    return true;
  }
  if (a is List && b is List) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (!_deepEquals(a[i], b[i])) return false;
    }
    return true;
  }
  return a == b;
}
