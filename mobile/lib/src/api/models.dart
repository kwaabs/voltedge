/// Wire models for the cmd/fieldsync pack API. Field names mirror the Go
/// packView / manifestView JSON exactly (internal/fieldpack/routes.go).

/// FieldLoginResult is the answer to POST /auth/field-login: the minted bearer
/// [token] the app stores in its keystore, plus the [email] / [displayName] the
/// UI shows as "signed in as". Mirrors the Go handler's JSON (fieldpack/login.go).
class FieldLoginResult {
  const FieldLoginResult({
    required this.token,
    required this.email,
    required this.displayName,
  });

  final String token;
  final String email;
  final String displayName;
}

/// PackManifest is the download envelope on a succeeded pack: two presigned
/// artifact URLs + the scope facts the device renders from.
class PackManifest {
  const PackManifest({
    required this.schemaVersion,
    required this.tilesUrl,
    required this.dataUrl,
    required this.featureCount,
    required this.bbox,
    required this.snapshotAt,
    required this.includeConnectivity,
    required this.includeRelationships,
  });

  final int schemaVersion;
  final String tilesUrl;
  final String dataUrl;
  final int? featureCount;
  final List<double> bbox;
  final DateTime? snapshotAt;
  final bool includeConnectivity;
  final bool includeRelationships;

  factory PackManifest.fromJson(Map<String, dynamic> j) => PackManifest(
        schemaVersion: (j['schema_version'] as num?)?.toInt() ?? 0,
        tilesUrl: j['tiles_url'] as String? ?? '',
        dataUrl: j['data_url'] as String? ?? '',
        featureCount: (j['feature_count'] as num?)?.toInt(),
        bbox: ((j['bbox'] as List?) ?? const [])
            .map((e) => (e as num).toDouble())
            .toList(),
        snapshotAt: parseServerTime(j['snapshot_at'] as String?),
        includeConnectivity: j['include_connectivity'] as bool? ?? false,
        includeRelationships: j['include_relationships'] as bool? ?? false,
      );
}

/// PackSummary is one build over the wire (GET /packs list rows and the
/// GET /packs/{id} poll response). Manifest is present only when succeeded.
class PackSummary {
  const PackSummary({
    required this.id,
    required this.status,
    required this.featureCount,
    required this.tilesBytes,
    required this.dataBytes,
    required this.error,
    required this.snapshotAt,
    required this.createdAt,
    required this.finishedAt,
    required this.manifest,
  });

  final String id;
  final String status; // queued | running | succeeded | failed | cancelled
  final int? featureCount;
  final int? tilesBytes;
  final int? dataBytes;
  final String? error;
  final DateTime? snapshotAt;
  final DateTime createdAt;
  final DateTime? finishedAt;
  final PackManifest? manifest;

  bool get isSucceeded => status == 'succeeded';
  bool get isTerminal =>
      status == 'succeeded' || status == 'failed' || status == 'cancelled';
  bool get isCancellable => status == 'queued' || status == 'running';

  factory PackSummary.fromJson(Map<String, dynamic> j) => PackSummary(
        id: j['id'] as String,
        status: j['status'] as String? ?? 'unknown',
        featureCount: (j['feature_count'] as num?)?.toInt(),
        tilesBytes: (j['tiles_bytes'] as num?)?.toInt(),
        dataBytes: (j['data_bytes'] as num?)?.toInt(),
        error: j['error'] as String?,
        snapshotAt: parseServerTime(j['snapshot_at'] as String?),
        createdAt: parseServerTime(j['created_at'] as String?) ??
            DateTime.fromMillisecondsSinceEpoch(0),
        finishedAt: parseServerTime(j['finished_at'] as String?),
        manifest: j['manifest'] == null
            ? null
            : PackManifest.fromJson(j['manifest'] as Map<String, dynamic>),
      );
}

/// CellVersion summarizes the latest succeeded build of a shared cell (the Go
/// deviceCellVersionView, internal/fieldpack/cellfetch.go) -- enough for the crew to
/// judge size/freshness before picking it up. [packId] is the ops.field_packs row the
/// pickup downloads; the manifest (presigned URLs) is minted only on pickup, not here.
class CellVersion {
  const CellVersion({
    required this.packId,
    required this.featureCount,
    required this.tilesBytes,
    required this.dataBytes,
    required this.snapshotAt,
    required this.builtAt,
  });

  final String packId;
  final int? featureCount;
  final int? tilesBytes;
  final int? dataBytes;
  final DateTime? snapshotAt;
  final DateTime? builtAt;

  factory CellVersion.fromJson(Map<String, dynamic> j) => CellVersion(
        packId: j['pack_id'] as String? ?? '',
        featureCount: (j['feature_count'] as num?)?.toInt(),
        tilesBytes: (j['tiles_bytes'] as num?)?.toInt(),
        dataBytes: (j['data_bytes'] as num?)?.toInt(),
        snapshotAt: parseServerTime(j['snapshot_at'] as String?),
        builtAt: parseServerTime(j['built_at'] as String?),
      );
}

/// CellSummary is one browsable shared cell from GET /cells (the Go deviceCellView,
/// internal/fieldpack/cellfetch.go): an admin pre-built coverage tile the crew may
/// pull. The server only returns cells this crew's access COVERS, so every row here
/// is pickable. [available] is true (and [version] populated) only when the cell has a
/// succeeded build to download; an active-but-never-built cell lists with
/// available=false so the crew sees it is coming but can't yet pull it.
class CellSummary {
  const CellSummary({
    required this.id,
    required this.name,
    required this.bbox,
    required this.featureClasses,
    required this.includeConnectivity,
    required this.includeRelationships,
    required this.includeFaults,
    required this.includeTags,
    required this.available,
    required this.version,
  });

  final String id;
  final String name;
  final List<double> bbox;
  final List<String> featureClasses;
  final bool includeConnectivity;
  final bool includeRelationships;
  final bool includeFaults;
  final bool includeTags;
  final bool available;
  final CellVersion? version;

  factory CellSummary.fromJson(Map<String, dynamic> j) => CellSummary(
        id: j['id'] as String? ?? '',
        name: j['name'] as String? ?? '',
        bbox: ((j['bbox'] as List?) ?? const [])
            .map((e) => (e as num).toDouble())
            .toList(),
        featureClasses: ((j['feature_classes'] as List?) ?? const [])
            .map((e) => e as String)
            .toList(),
        includeConnectivity: j['include_connectivity'] as bool? ?? false,
        includeRelationships: j['include_relationships'] as bool? ?? false,
        includeFaults: j['include_faults'] as bool? ?? false,
        includeTags: j['include_tags'] as bool? ?? false,
        available: j['available'] as bool? ?? false,
        version: j['version'] == null
            ? null
            : CellVersion.fromJson(j['version'] as Map<String, dynamic>),
      );
}

/// CellPickup is the answer to POST /cells/{id}/pickup: the base version's [packId],
/// the [cellId] it belongs to, and the download [manifest] (two freshly-minted
/// presigned URLs). The device downloads the base artifacts through the exact
/// PackStore.download path a personal pack uses -- a shared base IS a pack SQLite.
class CellPickup {
  const CellPickup({
    required this.packId,
    required this.cellId,
    required this.manifest,
  });

  final String packId;
  final String cellId;
  final PackManifest manifest;

  factory CellPickup.fromJson(Map<String, dynamic> j) => CellPickup(
        packId: j['pack_id'] as String? ?? '',
        cellId: j['cell_id'] as String? ?? '',
        manifest:
            PackManifest.fromJson((j['manifest'] as Map<String, dynamic>?) ?? const {}),
      );
}

/// OverlayWorkOrder is one of the crew member's own open work orders from the
/// per-user overlay (GET /cells/{id}/overlay, the Go overlayWorkOrder in
/// internal/fieldpack/overlay.go). A shared base ships NO work board (it is the
/// published, non-personal layer), so the crew's work board arrives here instead and
/// the device merges it in by id -- the same base+override shape WamStore already uses
/// for a personal pack's packed work orders. Times stay as ISO strings (what the WAM
/// store and its payloads expect). Round-trippable so the overlay persists as a
/// sidecar beside the base.
class OverlayWorkOrder {
  const OverlayWorkOrder({
    required this.id,
    required this.workType,
    required this.status,
    this.reference,
    this.priority,
    this.title,
    this.description,
    this.assetClass,
    this.assetId,
    this.defectId,
    this.assignedTo,
    this.scheduledFor,
    this.dueBy,
    this.startedAt,
    this.completionNotes,
  });

  final String id;
  final String workType;
  final String status;
  final String? reference;
  final String? priority;
  final String? title;
  final String? description;
  final String? assetClass;
  final String? assetId;
  final String? defectId;
  final String? assignedTo;
  final String? scheduledFor;
  final String? dueBy;
  final String? startedAt;
  final String? completionNotes;

  factory OverlayWorkOrder.fromJson(Map<String, dynamic> j) => OverlayWorkOrder(
        id: j['id'] as String? ?? '',
        workType: j['work_type'] as String? ?? '',
        status: j['status'] as String? ?? '',
        reference: j['reference'] as String?,
        priority: j['priority'] as String?,
        title: j['title'] as String?,
        description: j['description'] as String?,
        assetClass: j['asset_class'] as String?,
        assetId: j['asset_id'] as String?,
        defectId: j['defect_id'] as String?,
        assignedTo: j['assigned_to'] as String?,
        scheduledFor: j['scheduled_for'] as String?,
        dueBy: j['due_by'] as String?,
        startedAt: j['started_at'] as String?,
        completionNotes: j['completion_notes'] as String?,
      );

  Map<String, dynamic> toJson() => <String, dynamic>{
        'id': id,
        'work_type': workType,
        'status': status,
        if (reference != null) 'reference': reference,
        if (priority != null) 'priority': priority,
        if (title != null) 'title': title,
        if (description != null) 'description': description,
        if (assetClass != null) 'asset_class': assetClass,
        if (assetId != null) 'asset_id': assetId,
        if (defectId != null) 'defect_id': defectId,
        if (assignedTo != null) 'assigned_to': assignedTo,
        if (scheduledFor != null) 'scheduled_for': scheduledFor,
        if (dueBy != null) 'due_by': dueBy,
        if (startedAt != null) 'started_at': startedAt,
        if (completionNotes != null) 'completion_notes': completionNotes,
      };
}

/// OverlayDraft is one of the crew member's OWN private drafts over a cell (the Go
/// overlayDraft in internal/fieldpack/overlay.go) -- an unpublished feature they
/// authored that the shared base (published-only) deliberately excluded. [wkbBase64]
/// is the OGC WKB edit-truth geometry (Go emits []byte as base64); [geojson] rides
/// alongside for display without a device-side WKB decode. Carried + persisted now;
/// composed into the map read path in a later slice (S3c). Round-trippable.
class OverlayDraft {
  const OverlayDraft({
    required this.id,
    required this.featureClass,
    required this.wkbBase64,
    required this.geojson,
    required this.attributes,
    this.name,
    this.status,
    this.subtype,
    this.territoryId,
  });

  final String id;
  final String featureClass;
  final String wkbBase64;
  final String geojson;
  final Map<String, dynamic> attributes;
  final String? name;
  final String? status;
  final int? subtype;
  final String? territoryId;

  factory OverlayDraft.fromJson(Map<String, dynamic> j) => OverlayDraft(
        id: j['id'] as String? ?? '',
        featureClass: j['feature_class'] as String? ?? '',
        wkbBase64: j['wkb'] as String? ?? '',
        geojson: j['geojson'] as String? ?? '',
        attributes:
            (j['attributes'] as Map?)?.cast<String, dynamic>() ?? const {},
        name: j['name'] as String?,
        status: j['status'] as String?,
        subtype: (j['subtype'] as num?)?.toInt(),
        territoryId: j['territory_id'] as String?,
      );

  Map<String, dynamic> toJson() => <String, dynamic>{
        'id': id,
        'feature_class': featureClass,
        'wkb': wkbBase64,
        'geojson': geojson,
        'attributes': attributes,
        if (name != null) 'name': name,
        if (status != null) 'status': status,
        if (subtype != null) 'subtype': subtype,
        if (territoryId != null) 'territory_id': territoryId,
      };
}

/// CellOverlay is the per-user overlay for a cell (GET /cells/{id}/overlay): the
/// crew member's work board + their own private drafts over the cell, read under one
/// server snapshot. It is the thin PERSONAL layer the device composes over the shared
/// base + its own edits. Served as live JSON (not a built artifact) and cached as an
/// `overlay.json` sidecar beside the base pack, refreshed on pickup and each sync.
class CellOverlay {
  const CellOverlay({
    required this.cellId,
    required this.snapshotAt,
    required this.workOrders,
    required this.drafts,
  });

  final String cellId;
  final DateTime? snapshotAt;
  final List<OverlayWorkOrder> workOrders;
  final List<OverlayDraft> drafts;

  factory CellOverlay.fromJson(Map<String, dynamic> j) => CellOverlay(
        cellId: j['cell_id'] as String? ?? '',
        snapshotAt: parseServerTime(j['snapshot_at'] as String?),
        workOrders: ((j['work_orders'] as List?) ?? const [])
            .cast<Map<String, dynamic>>()
            .map(OverlayWorkOrder.fromJson)
            .toList(),
        drafts: ((j['drafts'] as List?) ?? const [])
            .cast<Map<String, dynamic>>()
            .map(OverlayDraft.fromJson)
            .toList(),
      );

  Map<String, dynamic> toJson() => <String, dynamic>{
        'cell_id': cellId,
        if (snapshotAt != null) 'snapshot_at': snapshotAt!.toUtc().toIso8601String(),
        'work_orders': [for (final w in workOrders) w.toJson()],
        'drafts': [for (final d in drafts) d.toJson()],
      };
}

/// SyncOpResult is one outbox op's CAPTURE outcome from POST /sync (mirrors the Go
/// syncOpResult in internal/fieldpack/sync.go). Since Phase 23 the endpoint receives
/// ops into a durable inbox and a worker applies them moments later, so a successful
/// result is now `received` (newly captured) or `duplicate` (already captured -- a
/// safe re-push), not `applied`. [isAccepted] tells the device the op is durably
/// server-side so it can mark that exact op synced and stop pushing it; a non-accepted
/// result carries [error] for the UI to surface. `applied` is still accepted so this
/// stays correct against an old (pre-Phase-23) inline-apply server too. The eventual
/// apply outcome (applied/rejected/conflict) arrives via the status pull (Slice 2).
class SyncOpResult {
  const SyncOpResult({
    required this.opId,
    required this.status,
    this.error,
    this.feature,
  });

  final String opId;
  final String status; // "received" | "duplicate" | "applied" | "error"
  final String? error;
  final Map<String, dynamic>? feature;

  /// True when the op is durably captured server-side (or already applied) -- the
  /// signal to mark it synced. Covers the Phase 23 `received`/`duplicate` acks and
  /// the legacy `applied` ack.
  bool get isAccepted =>
      status == 'received' || status == 'duplicate' || status == 'applied';

  factory SyncOpResult.fromJson(Map<String, dynamic> j) => SyncOpResult(
        opId: j['op_id'] as String? ?? '',
        status: j['status'] as String? ?? 'error',
        error: j['error'] as String?,
        feature: j['feature'] as Map<String, dynamic>?,
      );
}

/// SyncOpStatus is one op's APPLY outcome from the status pull (GET /sync/status
/// or the POST /sync `statuses` piggy-back; mirrors the Go syncOpStatus in
/// internal/fieldpack/sync.go). Unlike [SyncOpResult] -- a capture ack -- this is
/// the eventual result the async apply worker recorded (Phase 23 Slice 2): the
/// device polls the ops it captured earlier to learn whether they landed. [status]
/// is the inbox lifecycle value -- `applied` (in production), `rejected`/`conflict`
/// (quarantined; [error] says why the edit did NOT land), `superseded` (a newer edit
/// to the same feature replaced this one -- silently dropped), or a still-in-flight
/// `received`/`applying`/`pending_review`. [feature] is the server row on applied.
class SyncOpStatus {
  const SyncOpStatus({
    required this.opId,
    required this.status,
    this.error,
    this.feature,
  });

  final String opId;
  final String status;
  final String? error;
  final Map<String, dynamic>? feature;

  /// A terminal failure the user must see: the op was captured but the worker
  /// couldn't apply it (poison op) or it conflicts with server state.
  bool get isRejected => status == 'rejected' || status == 'conflict';

  /// A terminal success -- the edit is in production.
  bool get isApplied => status == 'applied';

  /// A SILENT terminal outcome (Phase 23 Slice 4c): a LATER edit to the same
  /// feature reached the server first as the surviving intent, so this older op
  /// was dropped without ever applying. Deliberately NOT surfaced to the user --
  /// the newer edit is the one that applies or shows up as a conflict, and the
  /// device's overlay already reflects that newer intent (edits to one feature
  /// share a single overlay). It just needs to stop being polled.
  bool get isSuperseded => status == 'superseded';

  /// True once the op has reached any terminal inbox state -- applied, quarantined
  /// (rejected/conflict), or superseded -- i.e. there is nothing left to poll. The
  /// device stops tracking it as in-flight (see EditDatabase.inFlightOpIds).
  bool get isTerminal => isApplied || isRejected || isSuperseded;

  factory SyncOpStatus.fromJson(Map<String, dynamic> j) => SyncOpStatus(
        opId: j['op_id'] as String? ?? '',
        status: j['status'] as String? ?? '',
        error: j['error'] as String?,
        feature: j['feature'] as Map<String, dynamic>?,
      );
}

/// SyncResponse is the whole POST /sync body: per-op capture [results] plus the
/// [statuses] the server piggy-backed for any op_ids the device asked to poll.
class SyncResponse {
  const SyncResponse({required this.results, required this.statuses});

  final List<SyncOpResult> results;
  final List<SyncOpStatus> statuses;
}

/// FeatureClassSummary is one packable class from GET /feature-classes (mirrors
/// the Go featureClassView in internal/fieldpack/classes.go). The "new pack" form
/// lists these as checkboxes so the user picks classes instead of typing codes;
/// [code] is what goes back verbatim in the POST /packs feature_classes array.
class FeatureClassSummary {
  const FeatureClassSummary({
    required this.code,
    required this.name,
    required this.geometryType,
    required this.networkRole,
    this.network,
    this.category,
  });

  final String code;
  final String name;
  final String geometryType; // point | line | polygon
  final String networkRole;
  final String? network; // "MV" | "LV" | null
  final String? category; // admin display grouping, null => "Other"

  factory FeatureClassSummary.fromJson(Map<String, dynamic> j) =>
      FeatureClassSummary(
        code: j['code'] as String? ?? '',
        name: j['name'] as String? ?? '',
        geometryType: j['geometry_type'] as String? ?? '',
        networkRole: j['network_role'] as String? ?? '',
        network: j['network'] as String?,
        category: j['category'] as String?,
      );
}

/// GpsCapture is one GNSS survey-quality snapshot stamped onto a GPS-sourced
/// placement/move (Phase gnss-quality Slice 3), read back from GET
/// /features/{code}/{id}/gps-captures on the cmd/fieldsync surface for the
/// detail screen (Slice 3b). Every DOP/sat/accuracy figure is nullable: an
/// iPhone's internal receiver reports only accuracy (no NMEA => no DOP), so those
/// come through null. [source] is "internal" | "external"; [fixType] is the mobile
/// GnssFixType `.name` (camelCase: none/gps/dgps/rtkFloat/rtkFixed/unknown) or null.
/// [capturedByName]/[capturedByEmail] are resolved server-side (LEFT JOIN
/// iam.users), null for an unknown/since-deleted user.
class GpsCapture {
  const GpsCapture({
    required this.id,
    required this.source,
    required this.capturedAt,
    this.hdop,
    this.pdop,
    this.vdop,
    this.hAccuracyM,
    this.vAccuracyM,
    this.altitudeM,
    this.satsUsed,
    this.satsInView,
    this.fixType,
    this.capturedByName,
    this.capturedByEmail,
  });

  final int id;
  final String source;
  final DateTime? capturedAt;
  final double? hdop;
  final double? pdop;
  final double? vdop;
  final double? hAccuracyM;
  final double? vAccuracyM;
  final double? altitudeM;
  final int? satsUsed;
  final int? satsInView;
  final String? fixType;
  final String? capturedByName;
  final String? capturedByEmail;

  /// True when at least one DOP figure came through -- an iPhone-internal
  /// capture has none, so the UI shows "DOP not reported" instead of blanks.
  bool get hasDop => hdop != null || pdop != null || vdop != null;

  /// Best available "who": display name, else email, else a device fallback.
  String get capturedByLabel =>
      capturedByName?.isNotEmpty == true
          ? capturedByName!
          : (capturedByEmail?.isNotEmpty == true
              ? capturedByEmail!
              : 'Field device');

  factory GpsCapture.fromJson(Map<String, dynamic> j) => GpsCapture(
        id: (j['id'] as num?)?.toInt() ?? 0,
        source: j['source'] as String? ?? 'internal',
        capturedAt: parseServerTime(j['captured_at'] as String?),
        hdop: (j['hdop'] as num?)?.toDouble(),
        pdop: (j['pdop'] as num?)?.toDouble(),
        vdop: (j['vdop'] as num?)?.toDouble(),
        hAccuracyM: (j['h_accuracy_m'] as num?)?.toDouble(),
        vAccuracyM: (j['v_accuracy_m'] as num?)?.toDouble(),
        altitudeM: (j['altitude_m'] as num?)?.toDouble(),
        satsUsed: (j['sats_used'] as num?)?.toInt(),
        satsInView: (j['sats_in_view'] as num?)?.toInt(),
        fixType: j['fix_type'] as String?,
        capturedByName: j['captured_by_name'] as String?,
        capturedByEmail: j['captured_by_email'] as String?,
      );
}

/// The online resolution of a scanned asset tag (GET /tags?code=), mirroring the
/// server's assettag.Resolution {tag, feature, tags}. Used only as the fallback
/// when the offline pack index has no such code (asset-tagging module S5): the
/// server applies the same scope/private-draft read gate a feature GET does, so a
/// code the caller may not see comes back as a FieldsyncException(404). We keep
/// just what the scan flow needs -- the scanned tag's feature ref, its primary
/// code, and a display name pulled best-effort from the feature envelope.
class TagResolution {
  const TagResolution({
    required this.tagCode,
    required this.featureClass,
    required this.featureId,
    this.primaryCode,
    this.featureName,
  });

  final String tagCode;
  final String featureClass;
  final String featureId;

  /// The feature's primary (code-of-record) tag, if the tags list carried one.
  final String? primaryCode;

  /// A best-effort display name for the resolved feature, or null.
  final String? featureName;

  factory TagResolution.fromJson(Map<String, dynamic> j) {
    final tag = (j['tag'] as Map?)?.cast<String, dynamic>() ?? const {};
    final feature = (j['feature'] as Map?)?.cast<String, dynamic>();
    final tags = (j['tags'] as List? ?? const []).cast<Map>();
    String? primary;
    for (final t in tags) {
      if (t['is_primary'] == true) {
        primary = t['tag_code'] as String?;
        break;
      }
    }
    // The feature envelope shape follows GET /features/{code}/{id}: a top-level
    // `name`, else a `name` inside the attribute bag. Either may be absent.
    String? name = feature?['name'] as String?;
    if ((name == null || name.isEmpty) && feature != null) {
      final attrs = (feature['attributes'] as Map?)?.cast<String, dynamic>();
      final a = attrs?['name'];
      if (a is String && a.isNotEmpty) name = a;
    }
    return TagResolution(
      tagCode: (tag['tag_code'] as String?) ?? '',
      featureClass: (tag['feature_class_code'] as String?) ?? '',
      featureId: (tag['feature_id'] as String?) ?? '',
      primaryCode: primary,
      featureName: (name != null && name.isNotEmpty) ? name : null,
    );
  }
}

/// parseServerTime tolerantly parses a Go time.Time RFC3339 string. Go emits up
/// to nanosecond precision; Dart's DateTime.parse only accepts up to microsecond
/// (6 fractional digits), so trim any extra digits before parsing.
DateTime? parseServerTime(String? s) {
  if (s == null || s.isEmpty) return null;
  final trimmed = s.replaceFirstMapped(
    RegExp(r'\.(\d{1,6})\d*'),
    (m) => '.${m.group(1)}',
  );
  return DateTime.tryParse(trimmed);
}
