import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:path/path.dart' as p;

import '../api/models.dart';
import '../edit/edit_database.dart';
import '../pack/pack_database.dart';
import '../util/uuid.dart';
import 'wam_models.dart';

/// WamStore is the reactive face of offline Work & Asset Management (Phase 59
/// mobile, [[offline-wam-field-pack-and-sync]]). It is the WAM sibling of
/// [EditStore]: it owns a read-only [PackDatabase] (the crew's shipped work board,
/// open defects, and inspection checklists) and a writable [EditDatabase] (the
/// same `edits.sqlite` the map's edit layer uses), MERGES the two so a just-
/// captured defect or a started work order shows immediately, and is a
/// [ChangeNotifier] so the WAM surfaces rebuild the instant a capture lands.
///
/// Captures ride the SHARED outbox: every WAM op ([EditDatabase.wamOpKinds]) is
/// pushed, acked, and status-polled by the exact same POST /sync machinery a
/// feature edit uses -- there is no WAM-specific sync. So the store also exposes
/// the standard sync passthrough ([pendingOps]/[markSynced]/[inFlightOpIds]/...),
/// letting a dedicated WAM screen flush the queue without the map screen. One
/// caveat by design: because both stores read the one outbox, a sync from EITHER
/// screen pushes ALL pending work (feature edits AND WAM captures) -- which is
/// exactly right, one round-trip flushes everything.
///
/// Screen-owned, like PackMapPage: open it when a WAM surface mounts, dispose it
/// when the surface leaves. It opens its own SQLite handles (WAL makes the second
/// pair safe beside the map screen's).
class WamStore extends ChangeNotifier {
  WamStore._(this._pack, this._db, this._baseVersion, this._attachmentsDir);

  final PackDatabase _pack;
  final EditDatabase _db;

  /// `<pack.dir>/attachments` -- where a captured defect photo's downscaled bytes
  /// are copied (the same dir the map screen's attachments use, so one pack holds
  /// every crew photo whether taken on the map or in a WAM defect form).
  final String _attachmentsDir;

  /// The directory a defect-photo capture writes its bytes into.
  String get attachmentsDir => _attachmentsDir;

  /// The pack's snapshot_at, carried onto every WAM outbox op for parity with
  /// feature edits (the server doesn't conflict-check WAM ops, but the base
  /// version keeps the audit trail consistent).
  final String? _baseVersion;

  // In-memory mirrors of the writable WAM tables -- so every merged read is pure
  // memory after open (the pack side is index-backed SQLite, read on demand).
  final Map<String, LocalWamDefect> _localDefects = {};
  final Map<String, WamDefectStatusOverride> _defectStatus = {};
  final Map<String, WamWorkOrderOverride> _woOverrides = {};
  final Map<String, LocalWamInspection> _localInspections = {};

  // Captured photos keyed by the feature (asset) id they hang on -- the WAM sibling
  // of EditStore._attachments. A defect's photos attach to its ASSET feature (the
  // attachments pipeline is keyed by gis feature class, which a defect row is not),
  // so an asset-linked defect shows every photo taken against that asset.
  final Map<String, List<LocalAttachment>> _attachments = {};

  /// WAM record ids (defect / work-order / inspection) with an un-synced capture
  /// op -- drives the "not synced" chip without touching the gis feature tint.
  final Set<String> _pendingWam = {};

  int _attentionCount = 0;
  int _opSeq = 0;

  /// Open the WAM store over an already-downloaded pack: [dataPath] is the
  /// read-only `data.sqlite`, [editsPath] the writable `edits.sqlite` beside it,
  /// and [baseVersion] the pack manifest's snapshot_at. Hydrates the in-memory
  /// WAM mirrors and the pending set.
  factory WamStore.open(
    String dataPath,
    String editsPath, {
    String? baseVersion,
  }) {
    final pack = PackDatabase.open(dataPath);
    final db = EditDatabase.open(editsPath);
    // Fall back to the pack's own snapshot_at so a caller can open the store with
    // just the two paths (the map screen already had the manifest in hand; a WAM
    // screen opened straight from the pack list has not).
    final base = baseVersion ?? pack.manifest()['snapshot_at'];
    // The pack dir is the edits.sqlite's parent; photos live in its attachments/
    // subdir (created lazily by AttachmentCapture on the first capture).
    final attachDir = p.join(p.dirname(editsPath), 'attachments');
    final store = WamStore._(pack, db, base, attachDir);
    for (final d in db.allLocalWamDefects()) {
      store._localDefects[d.id] = d;
    }
    for (final o in db.allWamDefectStatusOverrides()) {
      store._defectStatus[o.defectId] = o;
    }
    for (final o in db.allWamWorkOrderOverrides()) {
      store._woOverrides[o.workOrderId] = o;
    }
    for (final i in db.allLocalWamInspections()) {
      store._localInspections[i.id] = i;
    }
    for (final a in db.allLocalAttachments()) {
      (store._attachments[a.featureId] ??= <LocalAttachment>[]).add(a);
    }
    store._pendingWam.addAll(db.pendingWamRecordIds());
    store._attentionCount = db.attentionCount();
    return store;
  }

  /// Whether this pack even carries WAM read-context (built for a wam:read user).
  /// A WAM surface hides its entry point when false.
  bool get hasWam => _pack.packsWam;

  bool isPending(String recordId) => _pendingWam.contains(recordId);

  // ---- Asset picking (packed GIS features a defect can be raised against) ----

  /// The packed feature classes with their row counts -- the first step of the
  /// standalone "capture a defect" asset picker (a defect keys off a bare
  /// (asset_class, asset_id) ref to any packed feature).
  List<ClassCount> assetClasses() => _pack.classCounts();

  /// A page of one class's packed features, for the asset picker's second step.
  List<FeatureRecord> assetsOfClass(String code, {int limit = 200, int offset = 0}) =>
      _pack.featuresOfClass(code, limit: limit, offset: offset);

  /// One packed feature by id -- resolves a work order's / defect's asset to a
  /// human name for display.
  FeatureRecord? assetById(String id) => _pack.featureById(id);

  // ---- Work board -----------------------------------------------------------

  /// The crew's work board: every packed work order with its offline override (a
  /// start/reschedule/complete) folded on top and its pending flag resolved.
  List<WamWorkOrderView> workOrders() => [
        for (final wo in _pack.workOrders())
          WamWorkOrderView(
            base: wo,
            override: _woOverrides[wo.id],
            pending: _pendingWam.contains(wo.id),
          ),
      ];

  /// One work order by id (packed truth + offline override), or null when the
  /// pack has no such order.
  WamWorkOrderView? workOrderById(String id) {
    final wo = _pack.workOrderById(id);
    if (wo == null) return null;
    return WamWorkOrderView(
      base: wo,
      override: _woOverrides[id],
      pending: _pendingWam.contains(id),
    );
  }

  // ---- Defects --------------------------------------------------------------

  /// The whole defect backlog the crew can see: every packed defect (with any
  /// offline status override) PLUS every defect captured offline, merged into one
  /// list. Locals first (newest work on top), then packed.
  List<WamDefectView> defects() {
    final out = <WamDefectView>[
      for (final d in _localDefects.values)
        WamDefectView.fromLocal(
          d,
          override: _defectStatus[d.id],
          pending: _pendingWam.contains(d.id),
        ),
    ];
    for (final d in _pack.defects()) {
      out.add(WamDefectView.fromPacked(
        d,
        override: _defectStatus[d.id],
        pending: _pendingWam.contains(d.id),
      ));
    }
    return out;
  }

  /// The defects on one asset (by id): packed defects for the asset plus any
  /// captured offline against it, merged. Locals first.
  List<WamDefectView> defectsForAsset(String assetId) {
    final out = <WamDefectView>[
      for (final d in _localDefects.values)
        if (d.assetId == assetId)
          WamDefectView.fromLocal(
            d,
            override: _defectStatus[d.id],
            pending: _pendingWam.contains(d.id),
          ),
    ];
    for (final d in _pack.defectsForAsset(assetId)) {
      out.add(WamDefectView.fromPacked(
        d,
        override: _defectStatus[d.id],
        pending: _pendingWam.contains(d.id),
      ));
    }
    return out;
  }

  // ---- Inspection checklists ------------------------------------------------

  /// The active inspection checklists the pack ships, optionally narrowed to those
  /// that apply to [assetClass] (a class-scoped template or an any-asset one).
  List<PackInspectionTemplate> inspectionTemplates({String? assetClass}) =>
      _pack.inspectionTemplates(assetClass: assetClass);

  /// The ordered prompts of one checklist.
  List<PackInspectionTemplateItem> templateItems(String templateId) =>
      _pack.templateItems(templateId);

  /// Inspections recorded offline (not yet synced-pruned), newest first.
  List<LocalWamInspection> recordedInspections() {
    final list = _localInspections.values.toList()
      ..sort((a, b) => b.createdAt.compareTo(a.createdAt));
    return list;
  }

  // ---- Captures (queue a wam_* outbox op + mirror it) -----------------------

  /// Capture a NEW defect offline. Mints a device UUID (which doubles as the
  /// outbox op id and the server row's PK), queues a `wam_defect_create` op whose
  /// payload matches the server's wamDefectCreatePayload byte-for-byte, and mirrors
  /// the defect for optimistic display. Returns the new defect id. [status]
  /// defaults to 'open'; [reportedAt] defaults to now.
  ///
  /// A defect is anchored to an ASSET (both [assetClass] and [assetId]) or, when
  /// no catalogued asset applies, is an ORPHAN pinned by [longitude]/[latitude]
  /// (e.g. a field worker finds an un-inventoried broken pole). Exactly one anchor
  /// is required -- asset or location; the empty-string asset is the server's
  /// "no asset" sentinel (its columns are nullable since migration 0101).
  ///
  /// [id] lets a caller pre-mint the defect id before saving (the capture form
  /// does this so photos can be attached to the defect while it's still a draft);
  /// when null a fresh UUID is minted.
  Future<String> captureDefect({
    String? assetClass,
    String? assetId,
    required String severity,
    required String title,
    String? id,
    String? defectType,
    String? source,
    String? description,
    double? longitude,
    double? latitude,
    DateTime? reportedAt,
    String? notes,
    String status = 'open',
  }) async {
    final ac = assetClass?.trim() ?? '';
    final aid = assetId?.trim() ?? '';
    final hasAsset = ac.isNotEmpty && aid.isNotEmpty;
    final hasLoc = longitude != null && latitude != null;
    assert(hasAsset || hasLoc,
        'a defect needs an asset (class + id) or a location');
    final defectId = id ?? newUuidV4();
    final now = DateTime.now().toUtc();
    final reported = (reportedAt ?? now).toUtc();
    final reportedIso = reported.toIso8601String();
    final payload = <String, dynamic>{
      if (hasAsset) 'asset_class': ac,
      if (hasAsset) 'asset_id': aid,
      if (defectType != null && defectType.isNotEmpty) 'defect_type': defectType,
      if (severity.isNotEmpty) 'severity': severity,
      if (source != null && source.isNotEmpty) 'source': source,
      'title': title,
      if (description != null && description.isNotEmpty)
        'description': description,
      'longitude': ?longitude,
      'latitude': ?latitude,
      'reported_at': reportedIso,
      if (notes != null && notes.isNotEmpty) 'notes': notes,
    };
    final d = LocalWamDefect(
      id: defectId,
      assetClass: ac,
      assetId: aid,
      severity: severity,
      status: status,
      createdAt: now,
      defectType: defectType,
      source: source,
      title: title,
      description: description,
      reportedAt: reportedIso,
      notes: notes,
    );
    _db.putWamDefect(d, payload: payload, baseVersion: _baseVersion);
    _localDefects[defectId] = d;
    _pendingWam.add(defectId);
    notifyListeners();
    return defectId;
  }

  /// Change a defect's status offline (packed OR locally-captured defect). Queues
  /// a `wam_defect_status` op (fresh op id, deduped to the latest per defect) and
  /// mirrors the new status. [assetClass] is carried as the op's context class.
  Future<void> setDefectStatus({
    required String defectId,
    required String status,
    required String assetClass,
  }) async {
    final now = DateTime.now().toUtc();
    _db.putWamDefectStatus(
      defectId: defectId,
      status: status,
      assetClass: assetClass,
      opId: _nextOpId(defectId),
      payload: {'status': status},
      updatedAt: now,
      baseVersion: _baseVersion,
    );
    _defectStatus[defectId] =
        WamDefectStatusOverride(defectId: defectId, status: status, updatedAt: now);
    _pendingWam.add(defectId);
    notifyListeners();
  }

  /// Apply a work-order UPDATE offline (start / reschedule / complete). Queues a
  /// `wam_workorder_update` op (fresh op id, APPENDED so an ordered start->complete
  /// both reach the server) whose payload matches wamWorkOrderUpdatePayload, and
  /// merges the change into the optimistic override. Only the non-null fields are
  /// sent (omitempty parity). [assetClass] falls back to '' when unknown.
  Future<void> updateWorkOrder({
    required String workOrderId,
    String assetClass = '',
    String? status,
    String? priority,
    String? completionNotes,
    DateTime? scheduledFor,
    DateTime? dueBy,
  }) async {
    final now = DateTime.now().toUtc();
    final scheduledIso = scheduledFor?.toUtc().toIso8601String();
    final dueIso = dueBy?.toUtc().toIso8601String();
    final payload = <String, dynamic>{
      if (status != null && status.isNotEmpty) 'status': status,
      if (priority != null && priority.isNotEmpty) 'priority': priority,
      if (completionNotes != null && completionNotes.isNotEmpty)
        'completion_notes': completionNotes,
      'scheduled_for': ?scheduledIso,
      'due_by': ?dueIso,
    };
    _db.putWamWorkOrderUpdate(
      workOrderId: workOrderId,
      assetClass: assetClass,
      opId: _nextOpId(workOrderId),
      payload: payload,
      updatedAt: now,
      baseVersion: _baseVersion,
      status: status,
      priority: priority,
      completionNotes: completionNotes,
      scheduledFor: scheduledIso,
      dueBy: dueIso,
    );
    // Merge into the optimistic override (COALESCE upsert in the DB; mirror it in
    // memory so a start then a complete accumulate rather than clobber).
    final prev = _woOverrides[workOrderId];
    _woOverrides[workOrderId] = WamWorkOrderOverride(
      workOrderId: workOrderId,
      updatedAt: now,
      status: status ?? prev?.status,
      priority: priority ?? prev?.priority,
      completionNotes: completionNotes ?? prev?.completionNotes,
      scheduledFor: scheduledIso ?? prev?.scheduledFor,
      dueBy: dueIso ?? prev?.dueBy,
    );
    _pendingWam.add(workOrderId);
    notifyListeners();
  }

  /// Convenience: mark a work order started (status -> in_progress).
  Future<void> startWorkOrder(String workOrderId, {String assetClass = ''}) =>
      updateWorkOrder(
        workOrderId: workOrderId,
        assetClass: assetClass,
        status: 'in_progress',
      );

  /// Convenience: mark a work order completed, with optional completion notes.
  Future<void> completeWorkOrder(
    String workOrderId, {
    String assetClass = '',
    String? completionNotes,
  }) =>
      updateWorkOrder(
        workOrderId: workOrderId,
        assetClass: assetClass,
        status: 'completed',
        completionNotes: completionNotes,
      );

  /// Record an inspection offline. Mints a device UUID (op id + server PK), queues
  /// a `wam_inspection_record` op whose payload matches wamInspectionRecordPayload
  /// (including the per-item [items] array, byte-for-byte the wamInspectionItem
  /// shape), and mirrors the summary for display. When [completeWorkOrder] is true
  /// the server also closes [workOrderId] in the same transaction. A failing item
  /// carrying a fail severity auto-raises a defect server-side on sync. Returns the
  /// inspection id.
  Future<String> recordInspection({
    required String assetClass,
    required String assetId,
    required List<WamInspectionItemResponse> items,
    String? workOrderId,
    String? templateId,
    DateTime? inspectedAt,
    String? notes,
    String? overallResult,
    double? longitude,
    double? latitude,
    bool completeWorkOrder = false,
  }) async {
    final id = newUuidV4();
    final now = DateTime.now().toUtc();
    final inspected = (inspectedAt ?? now).toUtc();
    final inspectedIso = inspected.toIso8601String();
    final payload = <String, dynamic>{
      if (workOrderId != null && workOrderId.isNotEmpty)
        'work_order_id': workOrderId,
      if (templateId != null && templateId.isNotEmpty) 'template_id': templateId,
      'asset_class': assetClass,
      'asset_id': assetId,
      'inspected_at': inspectedIso,
      if (notes != null && notes.isNotEmpty) 'notes': notes,
      'longitude': ?longitude,
      'latitude': ?latitude,
      if (overallResult != null && overallResult.isNotEmpty)
        'overall_result': overallResult,
      if (completeWorkOrder) 'complete_work_order': true,
      'items': [for (final it in items) it.toPayload()],
    };
    final insp = LocalWamInspection(
      id: id,
      assetClass: assetClass,
      assetId: assetId,
      createdAt: now,
      workOrderId: workOrderId,
      templateId: templateId,
      overallResult: overallResult,
      inspectedAt: inspectedIso,
      notes: notes,
    );
    _db.putWamInspection(insp, payload: payload, baseVersion: _baseVersion);
    _localInspections[id] = insp;
    _pendingWam.add(id);
    // Recording an inspection can complete its work order; reflect that optimistically
    // so the board updates without waiting for sync.
    if (completeWorkOrder && workOrderId != null && workOrderId.isNotEmpty) {
      final prev = _woOverrides[workOrderId];
      _woOverrides[workOrderId] = WamWorkOrderOverride(
        workOrderId: workOrderId,
        updatedAt: now,
        status: 'completed',
        priority: prev?.priority,
        completionNotes: prev?.completionNotes,
        scheduledFor: prev?.scheduledFor,
        dueBy: prev?.dueBy,
      );
    }
    notifyListeners();
    return id;
  }

  /// Discard a not-yet-synced defect/inspection captured offline (and its pending
  /// op). A synced capture is history and left alone.
  Future<void> discardCapture(String recordId) async {
    final wasLocal = _localDefects.remove(recordId) != null ||
        _localInspections.remove(recordId) != null;
    if (!wasLocal) return;
    _db.discardWamCreate(recordId);
    _defectStatus.remove(recordId);
    _pendingWam
      ..clear()
      ..addAll(_db.pendingWamRecordIds());
    notifyListeners();
  }

  // ---- Photos (asset-attached, ride the attachment upload pipeline) ---------

  /// The captured photos hanging on one asset feature (by id), oldest first --
  /// both un-uploaded and already-synced (each still renders from its device file).
  /// A defect's capture form reads this for its asset so every photo taken against
  /// the asset is visible.
  List<LocalAttachment> assetPhotos(String assetId) =>
      List.unmodifiable(_attachments[assetId] ?? const <LocalAttachment>[]);

  /// Un-uploaded photos across all assets -- the sync flow's push list, uploaded
  /// AFTER the outbox ops so a defect's server row exists before its asset photos
  /// register (they key off the asset feature, not the defect, so ordering is only
  /// a nicety here, but it mirrors the map screen's push-ops-then-attachments flow).
  List<LocalAttachment> pendingAttachments() => _db.pendingLocalAttachments();

  /// Count of captured photos not yet uploaded -- folds into the WAM sync badge.
  int get pendingAttachmentCount {
    var n = 0;
    for (final list in _attachments.values) {
      for (final a in list) {
        if (a.isPending) n++;
      }
    }
    return n;
  }

  /// Record a freshly captured photo (already copied to [localPath] on device) and
  /// queue it for upload, hung on the asset feature ([featureClass], [featureId]).
  /// Returns the minted attachment id.
  Future<String> addAssetPhoto({
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

  /// Mark the given photo ids uploaded after their register -> PUT -> commit landed,
  /// refreshing the in-memory copies so their "uploading" state clears.
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

  /// Discard a captured photo before upload (deletes its row and device file). A
  /// synced photo is history and normally isn't discarded here.
  Future<void> discardAssetPhoto(String id) async {
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
      // A missing/locked file is not worth surfacing -- the row is already gone.
    }
  }

  // ---- Sync passthrough (the shared outbox) ---------------------------------

  /// The full pending push queue (feature edits AND WAM captures), oldest first --
  /// one POST /sync flushes everything, so a WAM screen can sync standalone.
  List<OutboxOp> pendingOps() => _db.pendingOps();

  /// Mark ops synced after POST /sync captured them, then recompute the WAM pending
  /// set. WAM mirrors are KEPT (still the only local view until a fresh pack lands),
  /// so this only flips the "not synced" chip off.
  Future<void> markSynced(Iterable<String> opIds) async {
    final ids = opIds.toList(growable: false);
    if (ids.isEmpty) return;
    _db.markSynced(ids, DateTime.now().toUtc());
    _pendingWam
      ..clear()
      ..addAll(_db.pendingWamRecordIds());
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

  // op ids need only be unique idempotency keys -- a monotonic seq within this
  // store plus a microsecond timestamp never collides, even for two captures on the
  // same record in one tick. Mirrors EditStore._nextOpId.
  String _nextOpId(String recordId) =>
      '${DateTime.now().toUtc().microsecondsSinceEpoch}-${_opSeq++}-$recordId';

  @override
  void dispose() {
    _db.close();
    _pack.close();
    super.dispose();
  }
}
