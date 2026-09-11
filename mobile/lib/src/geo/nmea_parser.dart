import 'gnss_sample.dart';

/// A pure, dependency-free NMEA 0183 parser (Phase: gnss-quality, Slice 1). It
/// turns the raw sentence stream from a GNSS receiver -- Android's NMEA listener
/// today, an external Bluetooth receiver in Slice 4 -- into the DOP / satellite /
/// fix-type figures the OS location APIs don't expose. Deliberately transport-
/// agnostic and side-effect-free so it's unit-testable off-device (the internal
/// source is native and needs a real GPS; this class is where the correctness
/// lives, so it carries the tests).
///
/// It parses the sentences that carry quality:
///   - GGA: fix quality, satellites used, HDOP, altitude, position
///   - GSA: fix type (2D/3D), PDOP/HDOP/VDOP
///   - GSV: satellites in view (count only)
///   - RMC: position + timestamp (fallback when GGA is absent)
/// and IGNORES the rest. Talker id is any two letters (GP/GL/GN/GA/GB/GQ...), so
/// we match on the last three chars of the address field (e.g. "GGA"), not the
/// full "$GPGGA".
///
/// Usage: feed complete sentences to [addSentence]; it folds them into a rolling
/// [GnssQualityState]. A GGA (or RMC) with a position marks the state "ready"
/// via [GnssQualityState.hasFix]; the caller reads [current] whenever it wants a
/// snapshot (typically it merges this DOP/sat/fix data onto a geolocator
/// position, which is the authoritative lat/lon on the internal source).
class NmeaParser {
  GnssQualityState _state = const GnssQualityState();

  /// Latest satellites-in-view per constellation talker (GP/GL/GA/GB/GQ...). A
  /// combined receiver emits a separate GSV block per constellation, so the true
  /// "in view" is their sum -- see [_applyGsv]. Kept out of [GnssQualityState]
  /// because it's parser accumulator state, not a reported field.
  final Map<String, int> _inViewByTalker = {};

  /// The latest folded quality snapshot.
  GnssQualityState get current => _state;

  /// Reset accumulated state (e.g. when switching receivers).
  void reset() {
    _state = const GnssQualityState();
    _inViewByTalker.clear();
  }

  /// Parse one NMEA sentence and fold it into [current]. Returns the new state.
  /// Malformed / unsupported / checksum-failing sentences are ignored (returns
  /// the unchanged state) -- a noisy serial stream must never throw here.
  GnssQualityState addSentence(String raw) {
    final fields = _validFields(raw);
    if (fields == null || fields.isEmpty) return _state;

    // fields[0] is the address, e.g. "GPGGA" / "GNGSA". Match on its type suffix;
    // the leading chars are the constellation talker id (GP/GL/GA/GB/GQ/GN).
    final address = fields[0];
    if (address.length < 3) return _state;
    final type = address.substring(address.length - 3);
    final talker = address.substring(0, address.length - 3);

    switch (type) {
      case 'GGA':
        _state = _applyGga(_state, fields);
        break;
      case 'GSA':
        _state = _applyGsa(_state, fields);
        break;
      case 'GSV':
        _state = _applyGsv(_state, fields, talker);
        break;
      case 'RMC':
        _state = _applyRmc(_state, fields);
        break;
      default:
        break;
    }
    return _state;
  }

  // ---- sentence handlers ----------------------------------------------------

  // GGA: $--GGA,time,lat,N/S,lon,E/W,fixQuality,satsUsed,hdop,alt,M,...
  GnssQualityState _applyGga(GnssQualityState s, List<String> f) {
    final lat = _coord(_at(f, 2), _at(f, 3));
    final lon = _coord(_at(f, 4), _at(f, 5));
    final quality = _int(_at(f, 6));
    final satsUsed = _int(_at(f, 7));
    final hdop = _double(_at(f, 8));
    final alt = _double(_at(f, 9));
    return s.copyWith(
      latitude: lat,
      longitude: lon,
      altitude: alt,
      hdop: hdop,
      satsUsed: satsUsed,
      fixType: quality == null ? null : _fixFromGgaQuality(quality),
      hasPosition: (lat != null && lon != null) ? true : null,
    );
  }

  // GSA: $--GSA,mode,fixType,sv1..sv12,pdop,hdop,vdop[,systemId]*cs
  // fixType: 1 = no fix, 2 = 2D, 3 = 3D. The last three numeric fields are the
  // DOPs; when a systemId trailer is present they sit one earlier, so index from
  // the checksum-stripped end rather than a fixed position.
  GnssQualityState _applyGsa(GnssQualityState s, List<String> f) {
    final mode = _int(_at(f, 2));
    // DOP triple is the last 3 fields, or fields before a trailing systemId.
    // We take pdop/hdop/vdop as f[15],f[16],f[17] when present (12 SV slots).
    final pdop = _double(_at(f, 15));
    final hdop = _double(_at(f, 16));
    final vdop = _double(_at(f, 17));
    return s.copyWith(
      pdop: pdop,
      hdop: hdop,
      vdop: vdop,
      // Only set fix type from GSA mode when GGA hasn't already given a richer
      // quality (a GGA fix-quality of dgps/rtk outranks GSA's plain 2D/3D).
      fixType: s.fixType == GnssFixType.unknown ? _fixFromGsaMode(mode) : null,
    );
  }

  // GSV: $--GSV,totalMsgs,msgNum,satsInView,... field 3 is the count in view for
  // THIS constellation. A combined receiver emits a separate GSV block per talker
  // (GPGSV, GLGSV, GAGSV, GBGSV...), each repeating its own count across that
  // talker's messages, so we key the latest count by talker and sum -- giving the
  // true total in view across all constellations rather than a single block's
  // count. Repeated messages within one talker's cycle carry the same number, so
  // overwriting per talker never double-counts.
  GnssQualityState _applyGsv(GnssQualityState s, List<String> f, String talker) {
    final inView = _int(_at(f, 3));
    if (inView == null) return s;
    _inViewByTalker[talker] = inView;
    final total = _inViewByTalker.values.fold<int>(0, (a, b) => a + b);
    return s.copyWith(
      satsInView: total,
      // A snapshot copy so downstream (the readout's per-constellation breakdown)
      // never sees the live accumulator mutate under it.
      satsInViewByTalker: Map.unmodifiable(Map.of(_inViewByTalker)),
    );
  }

  // RMC: $--RMC,time,status,lat,N/S,lon,E/W,... Position fallback only; used
  // when a receiver emits RMC but not GGA so we still get lat/lon.
  GnssQualityState _applyRmc(GnssQualityState s, List<String> f) {
    final status = _at(f, 2);
    if (status != 'A') return s; // 'V' = void / no fix
    final lat = _coord(_at(f, 3), _at(f, 4));
    final lon = _coord(_at(f, 5), _at(f, 6));
    if (lat == null || lon == null) return s;
    // Don't clobber a GGA-supplied position; only fill if we have none yet.
    if (s.hasPosition) return s;
    return s.copyWith(latitude: lat, longitude: lon, hasPosition: true);
  }

  // ---- field / checksum helpers ---------------------------------------------

  /// Validate a sentence's `*CS` checksum and split its comma fields. Returns
  /// null when the sentence is malformed or the checksum fails, so the caller
  /// simply skips it. Accepts a leading '$' or '!' and tolerates trailing CRLF.
  static List<String>? _validFields(String raw) {
    var line = raw.trim();
    if (line.isEmpty) return null;
    if (line.startsWith('\$') || line.startsWith('!')) {
      line = line.substring(1);
    }
    // Split payload from checksum (everything after the last '*').
    final star = line.lastIndexOf('*');
    String payload;
    if (star >= 0) {
      payload = line.substring(0, star);
      final given = line.substring(star + 1).trim();
      if (given.length >= 2) {
        final want = _checksum(payload);
        final got = int.tryParse(given.substring(0, 2), radix: 16);
        if (got == null || got != want) return null; // bad checksum -> drop
      }
      // A '*' with no valid 2-hex suffix: treat as checksum-less, keep payload.
    } else {
      payload = line;
    }
    if (payload.isEmpty) return null;
    return payload.split(',');
  }

  /// XOR of every char between '$' and '*', the NMEA 0183 checksum.
  static int _checksum(String payload) {
    var cs = 0;
    for (final code in payload.codeUnits) {
      cs ^= code;
    }
    return cs & 0xFF;
  }

  static String? _at(List<String> f, int i) =>
      (i >= 0 && i < f.length && f[i].isNotEmpty) ? f[i] : null;

  static int? _int(String? s) => s == null ? null : int.tryParse(s);

  static double? _double(String? s) => s == null ? null : double.tryParse(s);

  /// Convert an NMEA ddmm.mmmm + hemisphere pair to signed decimal degrees.
  static double? _coord(String? value, String? hemisphere) {
    if (value == null || hemisphere == null) return null;
    final dm = double.tryParse(value);
    if (dm == null) return null;
    // Degrees are all but the last two integer digits of the ddmm part.
    final degrees = (dm / 100).floor();
    final minutes = dm - degrees * 100;
    var dec = degrees + minutes / 60.0;
    final h = hemisphere.toUpperCase();
    if (h == 'S' || h == 'W') dec = -dec;
    return dec;
  }

  static GnssFixType _fixFromGgaQuality(int q) {
    switch (q) {
      case 0:
        return GnssFixType.none;
      case 1:
        return GnssFixType.gps;
      case 2:
        return GnssFixType.dgps;
      case 4:
        return GnssFixType.rtkFixed;
      case 5:
        return GnssFixType.rtkFloat;
      default:
        return GnssFixType.gps; // 3=PPS,6=estimated,7=manual,8=sim -> treat as a fix
    }
  }

  static GnssFixType _fixFromGsaMode(int? mode) {
    switch (mode) {
      case 1:
        return GnssFixType.none;
      case 2:
      case 3:
        return GnssFixType.gps;
      default:
        return GnssFixType.unknown;
    }
  }
}

/// The rolling quality snapshot the parser folds sentences into. Every quality
/// field is nullable (a source may not report it, or not yet). [hasPosition] is
/// true once a GGA/RMC gave us lat/lon. The internal source usually ignores this
/// class's lat/lon (geolocator is authoritative there) and reads only the DOP /
/// sat / fix fields; an external NMEA-only receiver uses the position too.
class GnssQualityState {
  const GnssQualityState({
    this.latitude,
    this.longitude,
    this.altitude,
    this.hdop,
    this.pdop,
    this.vdop,
    this.satsUsed,
    this.satsInView,
    this.satsInViewByTalker = const {},
    this.fixType = GnssFixType.unknown,
    this.hasPosition = false,
  });

  final double? latitude;
  final double? longitude;
  final double? altitude;
  final double? hdop;
  final double? pdop;
  final double? vdop;
  final int? satsUsed;
  final int? satsInView;

  /// Satellites in view per constellation talker id (GP/GL/GA/GB/GQ...), for the
  /// readout's per-constellation breakdown. Empty until GSV arrives.
  final Map<String, int> satsInViewByTalker;

  final GnssFixType fixType;
  final bool hasPosition;

  bool get hasFix => hasPosition && fixType != GnssFixType.none;
  bool get hasDop => hdop != null || pdop != null || vdop != null;

  GnssQualityState copyWith({
    double? latitude,
    double? longitude,
    double? altitude,
    double? hdop,
    double? pdop,
    double? vdop,
    int? satsUsed,
    int? satsInView,
    Map<String, int>? satsInViewByTalker,
    GnssFixType? fixType,
    bool? hasPosition,
  }) =>
      GnssQualityState(
        latitude: latitude ?? this.latitude,
        longitude: longitude ?? this.longitude,
        altitude: altitude ?? this.altitude,
        hdop: hdop ?? this.hdop,
        pdop: pdop ?? this.pdop,
        vdop: vdop ?? this.vdop,
        satsUsed: satsUsed ?? this.satsUsed,
        satsInView: satsInView ?? this.satsInView,
        satsInViewByTalker: satsInViewByTalker ?? this.satsInViewByTalker,
        fixType: fixType ?? this.fixType,
        hasPosition: hasPosition ?? this.hasPosition,
      );
}
