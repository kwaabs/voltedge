import 'dart:math' as math;

import '../edit/edit_database.dart';
import '../edit/edit_store.dart';
import '../util/uuid.dart';
import 'gnss_sample.dart';

/// FieldTrackRecorder records where a field worker physically was during a
/// session (migration 0178, field breadcrumb audit trail), so a supervisor can map
/// how the work was done. It does two jobs off the same live GNSS stream:
///
///   1. BREADCRUMBS — drops a movement-trail point into the edit DB on a
///      distance-primary + time-cap rule (a point every [_minMetres] of movement,
///      or every [_maxInterval] after at least a small [_minMove] step). Foreground
///      / session only: it runs while the pack map is open, fed by the page's GNSS
///      subscription — no background-location permission.
///   2. actor_location — keeps the latest fix so [actorLocation] can stamp every
///      mutating edit with where the worker was when they made it (EditStore wires
///      this into each outbox op's payload).
///
/// The session id is minted per recorder instance (one open-pack span). The recorder
/// is fed samples via [onSample] rather than subscribing itself, so its lifecycle
/// and the GNSS stream stay owned by the screen.
class FieldTrackRecorder {
  FieldTrackRecorder(this._edit, {String? deviceId, String? sessionId})
      : _deviceId = deviceId,
        sessionId = sessionId ?? newUuidV4();

  final EditStore _edit;
  final String? _deviceId;

  /// The device-minted field session this recorder's breadcrumbs and waypoints
  /// share — the supervisor map groups a worker's trail by it.
  final String sessionId;

  // Capture rule. Coarser than the track-walk vertex rule: a movement trail wants
  // "where did they go", not a surveyable line, so ~20 m between points keeps the
  // trail legible and the upload light.
  static const double _minMetres = 20.0;
  static const Duration _maxInterval = Duration(seconds: 30);
  static const double _minMove = 5.0;

  GnssSample? _latest;
  double? _lastLat;
  double? _lastLon;
  DateTime? _lastAt;

  /// Feed one live fix. Updates the latest-fix cache (for [actorLocation]) and, when
  /// the capture rule fires, persists a breadcrumb. Idempotent storage (device-minted
  /// id) means a double-feed is harmless.
  void onSample(GnssSample s) {
    _latest = s;
    final now = s.timestamp;
    if (_lastLat == null) {
      // First fix of the session: drop it so the trail starts where they started.
      _drop(s);
      return;
    }
    final moved = _metres(_lastLat!, _lastLon!, s.latitude, s.longitude);
    final elapsed = _lastAt == null ? _maxInterval : now.difference(_lastAt!);
    if (moved >= _minMetres || (elapsed >= _maxInterval && moved >= _minMove)) {
      _drop(s);
    }
  }

  void _drop(GnssSample s) {
    _edit.recordTrackPoint(LocalTrackPoint(
      id: newUuidV4(),
      sessionId: sessionId,
      recordedAt: s.timestamp.toUtc(),
      longitude: s.longitude,
      latitude: s.latitude,
      accuracyM: s.hAccuracy,
      speedMps: null,
      source: s.source.name,
    ));
    _lastLat = s.latitude;
    _lastLon = s.longitude;
    _lastAt = s.timestamp;
  }

  /// The worker's live position as the server's `actor_location` wire shape, or
  /// null when there's no fix yet. EditStore folds this into every mutating op so
  /// the edit is a located action in the audit trail.
  Map<String, dynamic>? actorLocation() {
    final s = _latest;
    if (s == null) return null;
    return {
      'session_id': sessionId,
      if (_deviceId != null && _deviceId.isNotEmpty) 'device_id': _deviceId,
      'recorded_at': s.timestamp.toUtc().toIso8601String(),
      'longitude': s.longitude,
      'latitude': s.latitude,
      'accuracy_m': s.hAccuracy,
    };
  }

  /// Cheap equirectangular metres between two lat/lons — accurate enough for the
  /// coarse capture rule (mirrors the track-walk vertex recorder's helper).
  static double _metres(double lat1, double lon1, double lat2, double lon2) {
    const r = 6371000.0; // mean earth radius, metres
    final dLat = (lat2 - lat1) * math.pi / 180.0;
    final dLon = (lon2 - lon1) * math.pi / 180.0;
    final meanLat = (lat1 + lat2) / 2.0 * math.pi / 180.0;
    final x = dLon * math.cos(meanLat);
    return r * math.sqrt(x * x + dLat * dLat);
  }
}
