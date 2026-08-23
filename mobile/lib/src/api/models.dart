/// Wire models for the cmd/fieldsync pack API. Field names mirror the Go
/// packView / manifestView JSON exactly (internal/fieldpack/routes.go).

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
