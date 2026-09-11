import 'dart:io' show Platform;

import 'package:flutter/services.dart';

/// One bonded Bluetooth device the user can pick as an external GNSS receiver.
/// [name] falls back to the address when the OS doesn't report a friendly name.
class BtDevice {
  const BtDevice({required this.name, required this.address});

  final String name;
  final String address;
}

/// BluetoothGnss is the thin Dart wrapper over the native `voltedge/field/bt`
/// MethodChannel (Phase: gnss-quality, Slice 4). It lists OS-bonded devices and
/// opens/closes the RFCOMM (SPP) socket; the sentence stream itself arrives on the
/// separate `voltedge/field/bt_nmea` EventChannel that [ExternalGnssSource]
/// listens to. Pairing is done in the OS Bluetooth settings -- we only connect to
/// devices already bonded there, so there's no scan/discovery path here.
///
/// Android only: iOS Classic-SPP needs the MFi External Accessory framework, a
/// wall for third-party survey receivers, so every call short-circuits to a
/// "not supported" answer on iOS (and defensively swallows MissingPluginException
/// anywhere the channel isn't registered).
class BluetoothGnss {
  static const MethodChannel _channel = MethodChannel('voltedge/field/bt');

  /// True only where the native control channel exists (Android). Everything else
  /// keys off this so the UI can say "not supported on iOS" without throwing.
  bool get isPlatformSupported => Platform.isAndroid;

  /// Whether this device has Bluetooth hardware at all.
  Future<bool> isSupported() =>
      _boolCall('isSupported', ifUnsupported: false);

  /// Whether Bluetooth is currently turned on.
  Future<bool> isEnabled() => _boolCall('isEnabled', ifUnsupported: false);

  /// Whether the BLUETOOTH_CONNECT runtime permission is granted (always true on
  /// API < 31, where the legacy install-time permission covers it).
  Future<bool> hasPermission() =>
      _boolCall('hasPermission', ifUnsupported: false);

  /// Prompt for the BLUETOOTH_CONNECT permission; resolves to the granted state.
  Future<bool> requestPermission() =>
      _boolCall('requestPermission', ifUnsupported: false);

  /// The OS-bonded devices, as the picker's list. Empty when unsupported, off, or
  /// permission-denied (the picker surfaces those states separately).
  Future<List<BtDevice>> bondedDevices() async {
    if (!isPlatformSupported) return const [];
    try {
      final raw = await _channel
              .invokeListMethod<Map<dynamic, dynamic>>('bondedDevices') ??
          const [];
      return [
        for (final m in raw)
          BtDevice(
            name: _nonEmpty(m['name']) ?? (m['address'] as String? ?? ''),
            address: m['address'] as String? ?? '',
          ),
      ].where((d) => d.address.isNotEmpty).toList();
    } on PlatformException {
      rethrow; // a real error (e.g. permission) the picker wants to show
    } on MissingPluginException {
      return const [];
    }
  }

  /// Open the RFCOMM socket to [address]; the native read thread then starts
  /// pushing sentences on the bt_nmea EventChannel. Throws on failure so the
  /// caller can surface why the receiver didn't connect.
  Future<void> connect(String address) async {
    if (!isPlatformSupported) {
      throw const BluetoothGnssException('External receivers are not supported on iOS');
    }
    await _channel.invokeMethod<void>('connect', {'address': address});
  }

  /// Close the socket. Best-effort; a not-connected disconnect is a no-op.
  Future<void> disconnect() async {
    if (!isPlatformSupported) return;
    try {
      await _channel.invokeMethod<void>('disconnect');
    } on MissingPluginException {
      // channel not registered -- nothing to close
    }
  }

  Future<bool> _boolCall(String method, {required bool ifUnsupported}) async {
    if (!isPlatformSupported) return ifUnsupported;
    try {
      return (await _channel.invokeMethod<bool>(method)) ?? ifUnsupported;
    } on MissingPluginException {
      return ifUnsupported;
    }
  }

  static String? _nonEmpty(Object? v) {
    if (v is String && v.trim().isNotEmpty) return v;
    return null;
  }
}

/// A Bluetooth GNSS failure with a human message, for the picker to show.
class BluetoothGnssException implements Exception {
  const BluetoothGnssException(this.message);
  final String message;
  @override
  String toString() => message;
}
