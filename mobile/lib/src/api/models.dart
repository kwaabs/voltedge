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
