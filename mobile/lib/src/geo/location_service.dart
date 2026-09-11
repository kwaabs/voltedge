import 'package:geolocator/geolocator.dart';
import 'package:maplibre_gl/maplibre_gl.dart' show LatLng;

/// The outcome of a location request. Either a fix or a reason we couldn't get
/// one, so callers can show a specific message ("turn on location", "permission
/// denied") instead of a generic failure.
class LocationResult {
  const LocationResult._(this.position, this.error);

  const LocationResult.ok(LatLng position) : this._(position, null);
  const LocationResult.failed(String error) : this._(null, error);

  /// The GPS fix as lon/lat, or null when [error] is set.
  final LatLng? position;

  /// A human-readable reason the fix failed, or null on success.
  final String? error;

  bool get ok => position != null;
}

/// LocationService wraps geolocator with the permission + service-enabled dance
/// the field tool needs (Phase 21 Slice 5b). It's foreground-only: the app asks
/// for a fix at the moment the user taps "my location" or "use my location," and
/// never tracks in the background. All the failure modes collapse into a
/// [LocationResult] so the UI can surface one clear message.
class LocationService {
  /// Ensure we hold foreground location permission (requesting once if the user
  /// hasn't decided yet), WITHOUT fetching a fix. Used to decide whether to turn
  /// on the map's live location puck. Returns true only when granted while-in-use
  /// or always; false (never throws) for off/denied so the map just omits the dot.
  Future<bool> ensurePermission() async {
    try {
      if (!await Geolocator.isLocationServiceEnabled()) return false;
      var permission = await Geolocator.checkPermission();
      if (permission == LocationPermission.denied) {
        permission = await Geolocator.requestPermission();
      }
      return permission == LocationPermission.whileInUse ||
          permission == LocationPermission.always;
    } catch (_) {
      return false;
    }
  }

  /// Get a single current position. Requests permission if not yet decided,
  /// and reports (rather than throws) the service-off / denied / timeout cases.
  Future<LocationResult> current() async {
    try {
      if (!await Geolocator.isLocationServiceEnabled()) {
        return const LocationResult.failed(
            'Location is turned off. Enable it in system settings.');
      }

      var permission = await Geolocator.checkPermission();
      if (permission == LocationPermission.denied) {
        permission = await Geolocator.requestPermission();
      }
      if (permission == LocationPermission.denied) {
        return const LocationResult.failed('Location permission denied.');
      }
      if (permission == LocationPermission.deniedForever) {
        return const LocationResult.failed(
            'Location permission is permanently denied. Enable it in app settings.');
      }

      final pos = await Geolocator.getCurrentPosition(
        locationSettings: const LocationSettings(
          accuracy: LocationAccuracy.high,
          timeLimit: Duration(seconds: 15),
        ),
      );
      return LocationResult.ok(LatLng(pos.latitude, pos.longitude));
    } catch (e) {
      return LocationResult.failed('Could not get a location fix: $e');
    }
  }
}
