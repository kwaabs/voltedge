import 'package:flutter/foundation.dart';

import '../api/models.dart';
import '../edit/edit_database.dart';
import '../pack/pack_database.dart';
import '../util/uuid.dart';
import 'faults_models.dart';

/// FaultsStore is the reactive face of the offline Faults module (mobile mirror of
/// the Faults server module, [[faults-module]] / [[offline-wam-field-pack-and-sync]]).
/// It is the faults sibling of [WamStore]: it owns a read-only [PackDatabase] (the
/// OPEN faults the pack ships on its assets) and a writable [EditDatabase] (the same
/// `edits.sqlite` the map's edit layer and the WAM store use), MERGES the two so a
/// just-raised fault or a just-changed status shows immediately, and is a
/// [ChangeNotifier] so the faults surfaces rebuild the instant a capture lands.
///
/// Captures ride the SHARED outbox: every fault op ([EditDatabase.faultOpKinds]) is
/// pushed, acked, and status-polled by the exact same POST /sync machinery a feature
/// edit (or a WAM capture) uses -- there is no faults-specific sync. So the store
/// also exposes the standard sync passthrough ([pendingOps]/[markSynced]/
/// [inFlightOpIds]/...), letting a dedicated faults screen flush the queue without
/// the map screen. One caveat by design: because the stores share the one outbox, a
/// sync from ANY screen pushes ALL pending work (feature edits AND WAM AND fault
/// captures) -- which is exactly right, one round-trip flushes everything.
///
/// Screen-owned, like WamStore: open it when a faults surface mounts, dispose it
/// when the surface leaves. It opens its own SQLite handles (WAL makes the extra
/// pair safe beside the map/WAM screens').
class FaultsStore extends ChangeNotifier {
  FaultsStore._(this._pack, this._db, this._baseVersion);

  final PackDatabase _pack;
  final EditDatabase _db;

  /// The pack's snapshot_at, carried onto every fault outbox op for parity with
  /// feature edits (the server doesn't conflict-check fault ops, but the base
  /// version keeps the audit trail consistent).
  final String? _baseVersion;

  // In-memory mirrors of the writable fault tables -- so every merged read is pure
  // memory after open (the pack side is index-backed SQLite, read on demand).
  final Map<String, LocalFault> _localFaults = {};
  final Map<String, FaultStatusOverride> _faultStatus = {};

  /// Fault record ids with an un-synced capture op -- drives the "not synced" chip
  /// without touching the gis feature tint.
  final Set<String> _pendingFaults = {};

  /// Fault ids a WAM defect has been booked against THIS session (fire-and-forget
  /// `fault_book_defect` op, no local mirror row) -- the UI hides "Book defect" for
  /// them. A packed fault's own defect_id covers the already-booked-on-server case.
  final Set<String> _booked = {};

  int _attentionCount = 0;
  int _opSeq = 0;

  /// Open the faults store over an already-downloaded pack: [dataPath] is the
  /// read-only `data.sqlite`, [editsPath] the writable `edits.sqlite` beside it, and
  /// [baseVersion] the pack manifest's snapshot_at. Hydrates the in-memory fault
  /// mirrors and the pending set.
  factory FaultsStore.open(
    String dataPath,
    String editsPath, {
    String? baseVersion,
  }) {
    final pack = PackDatabase.open(dataPath);
    final db = EditDatabase.open(editsPath);
    // Fall back to the pack's own snapshot_at so a caller can open the store with
    // just the two paths (the map screen already had the manifest in hand; a faults
    // screen opened straight from the pack list has not).
    final base = baseVersion ?? pack.manifest()['snapshot_at'];
    final store = FaultsStore._(pack, db, base);
    for (final f in db.allLocalFaults()) {
      store._localFaults[f.id] = f;
    }
    for (final o in db.allFaultStatusOverrides()) {
      store._faultStatus[o.faultId] = o;
    }
    store._pendingFaults.addAll(db.pendingFaultRecordIds());
    store._attentionCount = db.attentionCount();
    return store;
  }

  /// Whether this pack even carries faults read-context (a `faults_events` table).
  /// A faults surface hides its entry point when false.
  bool get hasFaults => _pack.packsFaults;

  bool isPending(String recordId) => _pendingFaults.contains(recordId);

  // ---- Asset picking (packed GIS features a fault can be raised against) ------

  /// The packed feature classes with their row counts -- the first step of the
  /// "raise a fault" asset picker (a fault keys off a bare (asset_class, asset_id)
  /// ref to any packed feature).
  List<ClassCount> assetClasses() => _pack.classCounts();

  /// A page of one class's packed features, for the asset picker's second step.
  List<FeatureRecord> assetsOfClass(String code, {int limit = 200, int offset = 0}) =>
      _pack.featuresOfClass(code, limit: limit, offset: offset);

  /// One packed feature by id -- resolves a fault's asset to a human name.
  FeatureRecord? assetById(String id) => _pack.featureById(id);

  // ---- Faults ---------------------------------------------------------------

  /// The whole fault backlog the crew can see: every packed (open) fault (with any
  /// offline status override) PLUS every fault raised offline, merged into one list.
  /// Locals first (newest work on top), then packed.
  List<FaultView> faults() {
    final out = <FaultView>[
      for (final f in _localFaults.values)
        FaultView.fromLocal(
          f,
          override: _faultStatus[f.id],
          pending: _pendingFaults.contains(f.id),
          booked: _booked.contains(f.id),
        ),
    ];
    for (final f in _pack.faults()) {
      out.add(FaultView.fromPacked(
        f,
        override: _faultStatus[f.id],
        pending: _pendingFaults.contains(f.id),
        booked: _booked.contains(f.id),
      ));
    }
    return out;
  }

  /// The faults on one asset (by id): packed faults for the asset plus any raised
  /// offline against it, merged. Locals first.
  List<FaultView> faultsForAsset(String assetId) {
    final out = <FaultView>[
      for (final f in _localFaults.values)
        if (f.assetId == assetId)
          FaultView.fromLocal(
            f,
            override: _faultStatus[f.id],
            pending: _pendingFaults.contains(f.id),
            booked: _booked.contains(f.id),
          ),
    ];
    for (final f in _pack.faultsForAsset(assetId)) {
      out.add(FaultView.fromPacked(
        f,
        override: _faultStatus[f.id],
        pending: _pendingFaults.contains(f.id),
        booked: _booked.contains(f.id),
      ));
    }
    return out;
  }

  // ---- Captures (queue a fault_* outbox op + mirror it) ----------------------

  /// Raise a NEW fault offline. Mints a device UUID (which doubles as the outbox op
  /// id and the server row's PK), queues a `fault_create` op whose payload matches
  /// the server's faultCreatePayload byte-for-byte, and mirrors the fault for
  /// optimistic display. Returns the new fault id. [status] defaults to 'detected';
  /// [detectedAt] defaults to now. Note: `detected_by` is NEVER sent -- the server
  /// stamps the submitting user as the detector.
  ///
  /// A fault is anchored to an ASSET (both [assetClass] and [assetId]), a LOCATION
  /// (both [longitude]/[latitude]), or an INCIDENT ([incidentId]) -- at least one is
  /// required. Omit asset_class/asset_id entirely for an orphan/location-anchored
  /// fault (mirror how WAM captureDefect omits them for orphan defects).
  ///
  /// [id] lets a caller pre-mint the fault id before saving; when null a fresh UUID
  /// is minted.
  Future<String> captureFault({
    String? assetClass,
    String? assetId,
    required String title,
    String? id,
    String? reference,
    String? faultType,
    String? cause,
    String? description,
    String? notes,
    double? longitude,
    double? latitude,
    double? faultCurrentA,
    String? protectiveDeviceClass,
    String? protectiveDeviceId,
    double? clearingTimeMs,
    String? incidentId,
    DateTime? detectedAt,
    String status = 'detected',
  }) async {
    final ac = assetClass?.trim() ?? '';
    final aid = assetId?.trim() ?? '';
    final hasAsset = ac.isNotEmpty && aid.isNotEmpty;
    final hasLoc = longitude != null && latitude != null;
    final hasIncident = incidentId != null && incidentId.isNotEmpty;
    assert(hasAsset || hasLoc || hasIncident,
        'a fault needs an asset (class + id), a location, or an incident');
    final faultId = id ?? newUuidV4();
    final now = DateTime.now().toUtc();
    final detected = (detectedAt ?? now).toUtc();
    final detectedIso = detected.toIso8601String();
    final payload = <String, dynamic>{
      if (reference != null && reference.isNotEmpty) 'reference': reference,
      if (hasAsset) 'asset_class': ac,
      if (hasAsset) 'asset_id': aid,
      'longitude': ?longitude,
      'latitude': ?latitude,
      if (faultType != null && faultType.isNotEmpty) 'fault_type': faultType,
      if (cause != null && cause.isNotEmpty) 'cause': cause,
      'fault_current_a': ?faultCurrentA,
      if (protectiveDeviceClass != null && protectiveDeviceClass.isNotEmpty)
        'protective_device_class': protectiveDeviceClass,
      if (protectiveDeviceId != null && protectiveDeviceId.isNotEmpty)
        'protective_device_id': protectiveDeviceId,
      'clearing_time_ms': ?clearingTimeMs,
      'detected_at': detectedIso,
      if (hasIncident) 'incident_id': incidentId,
      'title': title,
      if (description != null && description.isNotEmpty)
        'description': description,
      if (notes != null && notes.isNotEmpty) 'notes': notes,
    };
    final f = LocalFault(
      id: faultId,
      assetClass: ac,
      assetId: aid,
      status: status,
      createdAt: now,
      reference: reference,
      faultType: faultType,
      cause: cause,
      title: title,
      description: description,
      detectedAt: detectedIso,
      longitude: longitude,
      latitude: latitude,
      protectiveDeviceClass: protectiveDeviceClass,
      protectiveDeviceId: protectiveDeviceId,
      faultCurrentA: faultCurrentA,
      clearingTimeMs: clearingTimeMs,
      incidentId: hasIncident ? incidentId : null,
    );
    _db.putFault(f, payload: payload, baseVersion: _baseVersion);
    _localFaults[faultId] = f;
    _pendingFaults.add(faultId);
    notifyListeners();
    return faultId;
  }

  /// Change a fault's status offline (packed OR locally-raised fault). Queues a
  /// `fault_status` op (fresh op id, deduped to the latest per fault) and mirrors the
  /// new status. [assetClass] is carried as the op's context class. Valid statuses:
  /// detected/investigating/cleared/closed.
  Future<void> setFaultStatus({
    required String faultId,
    required String status,
    required String assetClass,
  }) async {
    final now = DateTime.now().toUtc();
    _db.putFaultStatus(
      faultId: faultId,
      status: status,
      assetClass: assetClass,
      opId: _nextOpId(faultId),
      payload: {'status': status},
      updatedAt: now,
      baseVersion: _baseVersion,
    );
    _faultStatus[faultId] =
        FaultStatusOverride(faultId: faultId, status: status, updatedAt: now);
    _pendingFaults.add(faultId);
    notifyListeners();
  }

  /// Book a WAM defect from a fault offline. Queues a `fault_book_defect` op (fresh
  /// op id, APPENDED -- never deduped) whose payload matches faultBookDefectPayload,
  /// and optimistically records the fault id as booked so the UI hides the button.
  /// Only offer this for a fault that carries an asset or a location (the server
  /// books the defect against the fault's asset/location and links it back). Only the
  /// non-null fields are sent (omitempty parity). [assetClass] is carried as the op's
  /// context class ('' when unknown).
  Future<void> bookDefectFromFault({
    required String faultId,
    String assetClass = '',
    String? title,
    String? defectType,
    String? severity,
    String? description,
  }) async {
    final now = DateTime.now().toUtc();
    final payload = <String, dynamic>{
      if (title != null && title.isNotEmpty) 'title': title,
      if (defectType != null && defectType.isNotEmpty) 'defect_type': defectType,
      if (severity != null && severity.isNotEmpty) 'severity': severity,
      if (description != null && description.isNotEmpty)
        'description': description,
    };
    _db.putFaultBookDefect(
      faultId: faultId,
      assetClass: assetClass,
      opId: _nextOpId(faultId),
      payload: payload,
      createdAt: now,
      baseVersion: _baseVersion,
    );
    _booked.add(faultId);
    _pendingFaults.add(faultId);
    notifyListeners();
  }

  /// Discard a not-yet-synced fault raised offline (and its pending op). A synced
  /// capture is history and left alone.
  Future<void> discardCapture(String recordId) async {
    final wasLocal = _localFaults.remove(recordId) != null;
    if (!wasLocal) return;
    _db.discardFaultCreate(recordId);
    _faultStatus.remove(recordId);
    _pendingFaults
      ..clear()
      ..addAll(_db.pendingFaultRecordIds());
    notifyListeners();
  }

  // ---- Sync passthrough (the shared outbox) ---------------------------------

  /// The full pending push queue (feature edits AND WAM AND fault captures), oldest
  /// first -- one POST /sync flushes everything, so a faults screen can sync alone.
  List<OutboxOp> pendingOps() => _db.pendingOps();

  /// Mark ops synced after POST /sync captured them, then recompute the fault pending
  /// set. Fault mirrors are KEPT (still the only local view until a fresh pack
  /// lands), so this only flips the "not synced" chip off.
  Future<void> markSynced(Iterable<String> opIds) async {
    final ids = opIds.toList(growable: false);
    if (ids.isEmpty) return;
    _db.markSynced(ids, DateTime.now().toUtc());
    _pendingFaults
      ..clear()
      ..addAll(_db.pendingFaultRecordIds());
    notifyListeners();
  }

  /// Op ids captured server-side but not yet known-terminal -- the status poll set.
  List<String> inFlightOpIds() => _db.inFlightOpIds();

  /// Count of quarantined ops (rejected/conflict) awaiting acknowledgement.
  int get attentionCount => _attentionCount;

  /// The quarantined ops to show in the "needs attention" list, oldest first.
  List<OutboxOp> attentionOps() => _db.attentionOps();

  /// Record the apply worker's outcomes (from a status pull) and refresh the
  /// attention tally.
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

  /// Acknowledge a quarantined op -- drops it off the attention list.
  Future<void> dismissAttention(String opId) async {
    _db.dismissOp(opId, DateTime.now().toUtc());
    _attentionCount = _db.attentionCount();
    notifyListeners();
  }

  // op ids need only be unique idempotency keys -- a monotonic seq within this store
  // plus a microsecond timestamp never collides, even for two captures on the same
  // record in one tick. Mirrors WamStore._nextOpId.
  String _nextOpId(String recordId) =>
      '${DateTime.now().toUtc().microsecondsSinceEpoch}-${_opSeq++}-$recordId';

  @override
  void dispose() {
    _db.close();
    _pack.close();
    super.dispose();
  }
}
