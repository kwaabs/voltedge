// GNSS quality model (Phase: gnss-quality, Slice 1). A single position fix
// enriched with the receiver-quality metrics field crews care about: DOP,
// satellite counts and fix type, alongside the metres-accuracy the OS gives us.
//
// DOP and satellite counts come only from raw NMEA (GSA/GGA/GSV), which is
// available from Android's NMEA listener and from an external receiver, but
// NEVER from iOS CoreLocation. So hdop/pdop/vdop/satsUsed/satsInView are all
// NULLABLE: on an iPhone's internal receiver they stay null and only
// hAccuracy/vAccuracy (from geolocator) are populated. The UI must treat a
// null DOP as "not reported by this source", not as zero.

/// Where a sample came from -- the phone's own GNSS chip, or an attached
/// external survey receiver. Surfaced to the user so they know which receiver a
/// reading (and any stamped capture) is from.
enum GnssSourceKind { internal, external }

/// The GNSS fix quality, as reported in NMEA (GGA fix-quality field / GSA fix
/// type). Ordered worst-to-best so callers can compare. [unknown] is for a
/// source that doesn't report it (e.g. iOS internal, which has no NMEA).
enum GnssFixType {
  none, // no fix
  gps, // autonomous 2D/3D GPS fix
  dgps, // differential GPS
  rtkFloat, // real-time kinematic, float ambiguities
  rtkFixed, // real-time kinematic, fixed ambiguities (survey grade)
  unknown, // source didn't report a fix type
}

class GnssSample {
  const GnssSample({
    required this.latitude,
    required this.longitude,
    required this.timestamp,
    required this.source,
    this.altitude,
    this.hAccuracy,
    this.vAccuracy,
    this.hdop,
    this.pdop,
    this.vdop,
    this.satsUsed,
    this.satsInView,
    this.satsByConstellation = const {},
    this.fixType = GnssFixType.unknown,
  });

  final double latitude;
  final double longitude;

  /// Ellipsoidal/MSL altitude in metres, when known.
  final double? altitude;

  /// Estimated horizontal / vertical accuracy in metres (geolocator's
  /// `accuracy` / `altitudeAccuracy`). Always available on a real fix,
  /// independent of DOP; this is the only quality figure iOS internal gives us.
  final double? hAccuracy;
  final double? vAccuracy;

  /// Dilution of precision -- horizontal, positional (3D), vertical. Null unless
  /// the source emits NMEA GSA/GGA (Android internal or external receiver).
  final double? hdop;
  final double? pdop;
  final double? vdop;

  /// Satellites used in the fix / visible in view. Null on a source without NMEA.
  final int? satsUsed;
  final int? satsInView;

  /// Satellites in view broken down per constellation talker id (GP/GL/GA/GB/GQ),
  /// for the live readout's per-constellation breakdown. Empty on a source without
  /// NMEA (iOS internal). LIVE-ONLY: deliberately not in [toCaptureJson] -- the
  /// server gis.feature_gps_captures table stores only the total, not the split.
  final Map<String, int> satsByConstellation;

  final GnssFixType fixType;

  final GnssSourceKind source;

  /// The receiver/OS timestamp of the fix.
  final DateTime timestamp;

  /// True when the source reported any DOP figure -- i.e. we have real
  /// receiver-geometry quality, not just the OS accuracy estimate.
  bool get hasDop => hdop != null || pdop != null || vdop != null;

  GnssSample copyWith({
    double? latitude,
    double? longitude,
    double? altitude,
    double? hAccuracy,
    double? vAccuracy,
    double? hdop,
    double? pdop,
    double? vdop,
    int? satsUsed,
    int? satsInView,
    Map<String, int>? satsByConstellation,
    GnssFixType? fixType,
    GnssSourceKind? source,
    DateTime? timestamp,
  }) =>
      GnssSample(
        latitude: latitude ?? this.latitude,
        longitude: longitude ?? this.longitude,
        altitude: altitude ?? this.altitude,
        hAccuracy: hAccuracy ?? this.hAccuracy,
        vAccuracy: vAccuracy ?? this.vAccuracy,
        hdop: hdop ?? this.hdop,
        pdop: pdop ?? this.pdop,
        vdop: vdop ?? this.vdop,
        satsUsed: satsUsed ?? this.satsUsed,
        satsInView: satsInView ?? this.satsInView,
        satsByConstellation: satsByConstellation ?? this.satsByConstellation,
        fixType: fixType ?? this.fixType,
        source: source ?? this.source,
        timestamp: timestamp ?? this.timestamp,
      );

  /// The wire shape stamped onto a GPS-sourced capture and sent with the edit
  /// outbox (Slice 3). Keys mirror the server gis.feature_gps_captures columns.
  /// Only ever emitted for a real fix, so lat/lon/timestamp are always present.
  Map<String, dynamic> toCaptureJson() => {
        'hdop': hdop,
        'pdop': pdop,
        'vdop': vdop,
        'h_accuracy_m': hAccuracy,
        'v_accuracy_m': vAccuracy,
        'altitude_m': altitude,
        'sats_used': satsUsed,
        'sats_in_view': satsInView,
        'fix_type': fixType.name,
        'source': source.name,
        'captured_at': timestamp.toUtc().toIso8601String(),
      };
}
