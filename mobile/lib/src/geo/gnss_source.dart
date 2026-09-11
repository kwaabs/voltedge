import 'dart:async';
import 'dart:io' show Platform;

import 'package:flutter/services.dart' show EventChannel;
import 'package:geolocator/geolocator.dart';

import 'bluetooth_gnss.dart';
import 'gnss_sample.dart';
import 'nmea_parser.dart';

/// A live stream of position fixes enriched with receiver quality (Phase:
/// gnss-quality, Slice 1). One abstraction, several transports: the phone's own
/// chip today ([InternalGnssSource]), an external Bluetooth survey receiver in
/// Slice 4. Callers subscribe to [stream]; [start]/[stop] gate the underlying
/// hardware so the readout can be turned off to save battery.
abstract class GnssSource {
  GnssSourceKind get kind;

  /// The live fixes. Broadcast, so the readout UI and a capture-in-progress can
  /// both listen. Nothing flows until [start] is called.
  Stream<GnssSample> get stream;

  /// Begin producing fixes (acquires the sensor / channel). Idempotent.
  Future<void> start();

  /// Stop producing fixes and release the sensor. Idempotent; [start] can be
  /// called again afterwards.
  Future<void> stop();
}

/// The internal (on-device) GNSS source. It fuses two facts the OS splits apart:
///   - geolocator's position stream — authoritative lat/lon/accuracy/altitude,
///     available on both platforms;
///   - raw NMEA (Android only, via the `voltedge/field/nmea` EventChannel over
///     `OnNmeaMessageListener`) parsed by [NmeaParser] into DOP / sats / fix type.
/// Every geolocator position is emitted as a [GnssSample], enriched with whatever
/// the NMEA parser has folded so far. On iOS there is no NMEA — CoreLocation never
/// exposes it — so the channel is simply not subscribed and the DOP/sat fields
/// stay null (the readout then shows accuracy only). See [[phase-gnss-quality]].
class InternalGnssSource implements GnssSource {
  InternalGnssSource({NmeaParser? parser}) : _parser = parser ?? NmeaParser();

  /// Must match the EventChannel name registered in the Android MainActivity.
  static const EventChannel _nmeaChannel = EventChannel('voltedge/field/nmea');

  final NmeaParser _parser;
  final StreamController<GnssSample> _controller =
      StreamController<GnssSample>.broadcast();

  StreamSubscription<Position>? _posSub;
  StreamSubscription<dynamic>? _nmeaSub;
  bool _started = false;

  @override
  GnssSourceKind get kind => GnssSourceKind.internal;

  @override
  Stream<GnssSample> get stream => _controller.stream;

  @override
  Future<void> start() async {
    if (_started) return;
    _started = true;
    _parser.reset();

    // Ensure foreground location permission + services BEFORE opening the stream.
    // getPositionStream on a denied/undetermined permission (or with location
    // services off) can sit silently without ever emitting -- which left the
    // readout stuck spinning on "Acquiring…". Surface the failure as a stream
    // error so the UI shows "unavailable" instead, and request the permission
    // once if the user hasn't decided yet.
    try {
      if (!await Geolocator.isLocationServiceEnabled()) {
        _emitError('Location is turned off. Enable it in system settings.');
        return;
      }
      var permission = await Geolocator.checkPermission();
      if (permission == LocationPermission.denied) {
        permission = await Geolocator.requestPermission();
      }
      if (permission == LocationPermission.denied ||
          permission == LocationPermission.deniedForever) {
        _emitError('Location permission denied.');
        return;
      }
    } catch (e) {
      _emitError('$e');
      return;
    }

    // A stop() during the awaits above (rapid off toggle) must not leave a live
    // subscription behind.
    if (!_started) return;

    // Android streams raw NMEA -> real DOP/sats/fix. iOS never does, so we don't
    // subscribe there; a broken/absent channel must never tear down the source,
    // hence the swallowed onError.
    if (Platform.isAndroid) {
      _nmeaSub = _nmeaChannel.receiveBroadcastStream().listen(
        (event) {
          if (event is String) _parser.addSentence(event);
        },
        onError: (_) {},
      );
    }

    _posSub = Geolocator.getPositionStream(
      locationSettings: _positionSettings(),
    ).listen(
      _onPosition,
      onError: _controller.addError,
    );

    // Prime with the last known fix so the readout shows a position immediately
    // instead of spinning while the first live update arrives (which can take a
    // while for a cold GPS, especially indoors). Best-effort: null when the
    // device has no cached fix, in which case we just wait for the live stream.
    try {
      final last = await Geolocator.getLastKnownPosition();
      if (_started && last != null) _onPosition(last);
    } catch (_) {}
  }

  void _emitError(String message) {
    if (!_controller.isClosed) _controller.addError(StateError(message));
  }

  /// The position-stream settings. On Android we force the raw LocationManager
  /// (GPS_PROVIDER) instead of geolocator's default Google-Play fused provider:
  /// DOP / satellites / fix-type come ONLY from the OS `OnNmeaMessageListener`,
  /// which fires solely while the LocationManager GPS provider is the one
  /// producing fixes. The fused provider powers the GPS but doesn't drive that
  /// callback on most devices, so without this the NMEA channel stays silent and
  /// the readout shows "DOP not reported" (accuracy only). The trade-off -- the
  /// LocationManager path is a touch less battery-smooth than fused -- is fine
  /// while the quality readout is deliberately open. Other platforms keep the
  /// default (iOS has no NMEA regardless). See [[phase-gnss-quality]].
  static LocationSettings _positionSettings() {
    if (Platform.isAndroid) {
      return AndroidSettings(
        accuracy: LocationAccuracy.high,
        distanceFilter: 0,
        forceLocationManager: true,
      );
    }
    return const LocationSettings(
      accuracy: LocationAccuracy.high,
      distanceFilter: 0,
    );
  }

  void _onPosition(Position pos) {
    if (_controller.isClosed) return;
    final q = _parser.current;
    // geolocator reports accuracy in metres; a non-positive value means "unknown"
    // on some backends, so fold that back to null.
    final hAcc = pos.accuracy > 0 ? pos.accuracy : null;
    final vAcc = pos.altitudeAccuracy > 0 ? pos.altitudeAccuracy : null;
    _controller.add(
      GnssSample(
        latitude: pos.latitude,
        longitude: pos.longitude,
        timestamp: pos.timestamp,
        source: GnssSourceKind.internal,
        altitude: pos.altitude,
        hAccuracy: hAcc,
        vAccuracy: vAcc,
        hdop: q.hdop,
        pdop: q.pdop,
        vdop: q.vdop,
        satsUsed: q.satsUsed,
        satsInView: q.satsInView,
        satsByConstellation: q.satsInViewByTalker,
        fixType: q.fixType,
      ),
    );
  }

  @override
  Future<void> stop() async {
    await _posSub?.cancel();
    await _nmeaSub?.cancel();
    _posSub = null;
    _nmeaSub = null;
    _started = false;
  }

  /// Permanently release the source (also closes [stream]).
  Future<void> dispose() async {
    await stop();
    await _controller.close();
  }
}

/// An external Bluetooth GNSS receiver as a [GnssSource] (Phase: gnss-quality,
/// Slice 4). Unlike [InternalGnssSource] -- where geolocator is the authoritative
/// position and NMEA only enriches it -- a survey receiver's NMEA IS the position:
/// GGA/RMC carry lat/lon/alt, GSA/GGA the DOPs, so every field comes from the same
/// [NmeaParser] the internal Android source already feeds. The transport is the
/// native `voltedge/field/bt_nmea` EventChannel, fed by a bonded RFCOMM socket the
/// [BluetoothGnss] control channel opens.
///
/// It emits one [GnssSample] per position sentence -- a GGA (the standard 1 Hz
/// position+quality line), or an RMC only when the receiver sends no GGA -- so the
/// enrichment-only GSA/GSV lines don't double up the stream. hAccuracy is null:
/// plain NMEA has no metres-accuracy field (that's GST, which we don't parse), and
/// the readout tiers off HDOP for an external receiver anyway. Android only.
class ExternalGnssSource implements GnssSource {
  ExternalGnssSource({
    required this.address,
    NmeaParser? parser,
    BluetoothGnss? bluetooth,
  })  : _parser = parser ?? NmeaParser(),
        _bt = bluetooth ?? BluetoothGnss();

  /// The bonded device address to connect to (from [BluetoothGnss.bondedDevices]).
  final String address;

  /// Must match the EventChannel name registered in the Android MainActivity.
  static const EventChannel _btNmeaChannel = EventChannel('voltedge/field/bt_nmea');

  final NmeaParser _parser;
  final BluetoothGnss _bt;
  final StreamController<GnssSample> _controller =
      StreamController<GnssSample>.broadcast();

  StreamSubscription<dynamic>? _nmeaSub;
  bool _started = false;

  /// Once a GGA has been seen we stop treating RMC as a position trigger, so a
  /// receiver that sends both doesn't emit twice per cycle.
  bool _sawGga = false;

  @override
  GnssSourceKind get kind => GnssSourceKind.external;

  @override
  Stream<GnssSample> get stream => _controller.stream;

  @override
  Future<void> start() async {
    if (_started) return;
    _started = true;
    _parser.reset();
    _sawGga = false;

    // Subscribe BEFORE connecting so the native read thread always has a live sink
    // (the bridge's ordering contract). A channel error tears down cleanly into a
    // stream error the readout shows as "unavailable".
    _nmeaSub = _btNmeaChannel.receiveBroadcastStream().listen(
      _onLine,
      onError: (Object e, StackTrace _) {
        if (!_controller.isClosed) _controller.addError(e);
      },
    );

    try {
      await _bt.connect(address);
    } catch (e) {
      if (!_controller.isClosed) _controller.addError(e);
    }
  }

  void _onLine(dynamic event) {
    if (_controller.isClosed || event is! String) return;
    final line = event;
    _parser.addSentence(line);

    final isGga = _isType(line, 'GGA');
    final isRmc = _isType(line, 'RMC');
    if (isGga) _sawGga = true;
    // Emit only on a position-bearing sentence, so DOP/sat enrichment lines don't
    // re-emit the same fix.
    if (!(isGga || (isRmc && !_sawGga))) return;

    final q = _parser.current;
    if (!q.hasPosition || q.latitude == null || q.longitude == null) return;
    _controller.add(
      GnssSample(
        latitude: q.latitude!,
        longitude: q.longitude!,
        // NMEA time-of-day carries no date; the device clock is the honest stamp
        // for captured_at and matches how the internal source timestamps a fix.
        timestamp: DateTime.now(),
        source: GnssSourceKind.external,
        altitude: q.altitude,
        hAccuracy: null,
        vAccuracy: null,
        hdop: q.hdop,
        pdop: q.pdop,
        vdop: q.vdop,
        satsUsed: q.satsUsed,
        satsInView: q.satsInView,
        satsByConstellation: q.satsInViewByTalker,
        fixType: q.fixType,
      ),
    );
  }

  /// True when [raw]'s NMEA address (between the leading `$`/`!` and the first
  /// comma) ends with [type] (e.g. "GGA"), matching any talker id (GP/GN/GL...).
  static bool _isType(String raw, String type) {
    final start = (raw.startsWith('\$') || raw.startsWith('!')) ? 1 : 0;
    final comma = raw.indexOf(',');
    if (comma <= start) return false;
    final address = raw.substring(start, comma);
    return address.length >= 3 &&
        address.substring(address.length - 3) == type;
  }

  @override
  Future<void> stop() async {
    await _nmeaSub?.cancel();
    _nmeaSub = null;
    try {
      await _bt.disconnect();
    } catch (_) {}
    _started = false;
  }

  /// Permanently release the source (also closes [stream]).
  Future<void> dispose() async {
    await stop();
    await _controller.close();
  }
}
