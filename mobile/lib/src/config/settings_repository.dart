import 'dart:convert';
import 'dart:io';

import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';

/// SettingsRepository holds the two things the app needs to reach the
/// cmd/fieldsync engine: the base URL and the iam.api_tokens bearer token.
///
/// Storage split (Phase 21):
///   - The **bearer token** is a credential, so it lives in the OS keystore via
///     `flutter_secure_storage` (Android Keystore-backed EncryptedSharedPreferences /
///     iOS Keychain). It was briefly demoted to the JSON file while the toolchain
///     sat on AGP 9 / `android.builtInKotlin=true` (the plugin applies the classic
///     `kotlin-android` plugin unconditionally, which conflicts with built-in
///     Kotlin). We are back on AGP 8 with `builtInKotlin=false`, where that
///     unconditional apply is correct, so the keystore is reinstated here.
///   - The **non-secret preferences** (base URL, snapping, basemap, network-edges)
///     stay in a small JSON file (`ve_settings.json`) in the app-private documents
///     directory -- they don't warrant the keystore, and keeping them in plain
///     JSON avoids a keystore round-trip per pref read.
///
/// A one-time migration moves a token written by the old JSON-only build into the
/// keystore on first load and strips it from the JSON, so an existing install
/// isn't logged out by the upgrade.
///
/// The repository is loaded once at startup (see main.dart) into in-memory fields
/// so the rest of the app reads them synchronously; writes update memory and
/// persist.
class SettingsRepository {
  /// [file] and [secureStorage] are injectable for tests; in the app the file is
  /// resolved lazily to `<app documents>/ve_settings.json` and the keystore uses
  /// its platform-default backing. Keystore reads/writes are best-effort (wrapped
  /// so a missing plugin in a unit-test harness degrades to "no token" rather than
  /// throwing) -- construction touches neither store.
  SettingsRepository({File? file, FlutterSecureStorage? secureStorage})
      : _file = file,
        _secure = secureStorage ??
            const FlutterSecureStorage(
              aOptions: AndroidOptions(encryptedSharedPreferences: true),
            );

  File? _file;
  final FlutterSecureStorage _secure;

  static const _fileName = 've_settings.json';
  static const _baseUrlKey = 'baseUrl';
  static const _tokenKey = 'token'; // legacy JSON key (migration source only)
  static const _snappingKey = 'snappingEnabled';
  static const _basemapKey = 'basemapId';
  static const _networkEdgesKey = 'showNetworkEdges';
  static const _extGnssEnabledKey = 'externalGnssEnabled';
  static const _extGnssAddressKey = 'externalGnssAddress';
  static const _extGnssNameKey = 'externalGnssName';
  static const _signedInEmailKey = 'signedInEmail';
  static const _signedInNameKey = 'signedInName';
  static const _permissionsKey = 'permissions';
  static const _fieldTrackingKey = 'field_tracking_enabled';
  static const _fieldTrackingDisclosureKey = 'field_tracking_disclosure_enabled';
  static const _accessExpiresKey = 'access_expires_at';

  /// The keystore entry the bearer (access) token lives under.
  static const _secureTokenKey = 've_api_token';

  /// The keystore entry the long-lived refresh token lives under (migration 0191).
  /// A credential, so keystore, not the JSON file -- same as the access token.
  static const _secureRefreshKey = 've_refresh_token';

  /// Default basemap = "none" (pack-only). Offline-first: an online basemap is
  /// opt-in. Mirrors basemaps.dart's kDefaultBasemapId (kept as a literal here so
  /// config doesn't depend on the map layer).
  static const _defaultBasemapId = 'none';

  /// The Android emulator reaches the host machine at 10.0.2.2. On a real
  /// device this MUST be the machine's LAN address (e.g. http://192.168.1.20:3706)
  /// -- and RUSTFS_PUBLIC_ENDPOINT on the server must point at a host the device
  /// can reach too, since presigned download URLs are signed for that host.
  static const defaultBaseUrl = 'http://10.0.2.2:3706';

  String baseUrl = defaultBaseUrl;

  /// The access bearer presented on every call. Short-lived under the migration-0191
  /// session model (paired with [refreshToken] + [accessExpiresAt]); a device signed
  /// in before that shipped holds a legacy non-expiring token with no refresh/expiry.
  String? token;

  /// The long-lived refresh token (keystore), swapped for a fresh access token near
  /// [accessExpiresAt]. Null on a legacy session (nothing to refresh).
  String? refreshToken;

  /// When [token] lapses (UTC). Null on a legacy non-expiring session.
  DateTime? accessExpiresAt;

  /// Whether edit snapping is enabled. When on, a placed point, dragged vertex,
  /// or inserted midpoint snaps to the nearest existing feature vertex within a
  /// small screen-space tolerance (PackMapView._snap). The map settings page
  /// toggles + persists it; the map re-reads it when that page closes.
  bool snappingEnabled = true;

  /// The selected basemap id (see basemaps.dart). Default "none" = pack-only.
  String basemapId = _defaultBasemapId;

  /// Whether the pack's connectivity/relationship graph is drawn as map edges
  /// around the selected feature (Slice 16's on-selection network overlay). Off
  /// hides those synthetic green/purple links so the map stays uncluttered; a
  /// feature's neighbours are still listed in its detail panel, and your own
  /// offline-established (not-yet-synced) links keep drawing regardless. The map
  /// settings page toggles + persists it; the map re-reads it when that page
  /// closes.
  bool showNetworkEdges = true;

  /// Whether an external Bluetooth GNSS receiver is the selected position source
  /// (Phase gnss-quality Slice 4). When false the app uses the phone's internal
  /// chip. [externalGnssAddress] is the bonded device to connect to; the pretty
  /// [externalGnssName] is remembered only so the picker/readout can show it
  /// without a Bluetooth round-trip. Android only -- always false on iOS.
  bool externalGnssEnabled = false;
  String? externalGnssAddress;
  String? externalGnssName;

  /// Who the stored token belongs to, remembered from the field-login response
  /// only so the UI can show "signed in as ..." without a round-trip. Not a
  /// credential (the token is), so it rides the plain JSON prefs, not the
  /// keystore; cleared on sign-out.
  String? signedInEmail;
  String? signedInName;

  /// The signed-in user's effective permission set (from field-login, migration
  /// 0164). The UI shows/hides modules off this (hasPermission); persisted so the
  /// gating survives an offline restart. The server still enforces every call.
  List<String> permissions = const [];

  /// Whether the signed-in user holds [perm] (e.g. 'features:write', 'wam:read').
  /// Used to gate field-app modules in the UI. An EMPTY set means a legacy session
  /// that signed in before migration 0164 shipped permissions -- we don't know them
  /// yet, so fall back to allowing (the server still enforces every call); the next
  /// sign-in populates them and real UI gating takes effect.
  bool hasPermission(String perm) =>
      permissions.isEmpty || permissions.contains(perm);

  /// Org-wide field-tracking switch from the field-login response (default ON).
  /// When off, the app records no breadcrumbs and stamps no located edits, and the
  /// in-app disclosure reflects "not recording". Persisted so it survives an
  /// offline restart; the server also enforces it as a backstop.
  bool fieldTrackingEnabled = true;

  /// Whether the field app DISCLOSES tracking to the crew (shows the in-app "you
  /// are being recorded" notice). From the field-login response; default ON.
  /// Independent of [fieldTrackingEnabled] — an org can record without disclosing.
  bool fieldTrackingDisclosureEnabled = true;

  bool get isConfigured => token != null && token!.isNotEmpty;

  Future<File> _resolveFile() async {
    final existing = _file;
    if (existing != null) return existing;
    final dir = await getApplicationDocumentsDirectory();
    return _file = File(p.join(dir.path, _fileName));
  }

  Future<void> load() async {
    // Preferences come from the JSON file; the token comes from the keystore.
    final file = await _resolveFile();
    String? legacyToken; // a token left in the JSON by the pre-keystore build
    if (!await file.exists()) {
      baseUrl = defaultBaseUrl;
      snappingEnabled = true;
      basemapId = _defaultBasemapId;
      showNetworkEdges = true;
    } else {
      try {
        final map =
            jsonDecode(await file.readAsString()) as Map<String, dynamic>;
        final storedBase = map[_baseUrlKey] as String?;
        baseUrl = (storedBase == null || storedBase.isEmpty)
            ? defaultBaseUrl
            : storedBase;
        final storedSnap = map[_snappingKey];
        snappingEnabled = storedSnap is bool ? storedSnap : true;
        final storedBasemap = map[_basemapKey];
        basemapId = storedBasemap is String && storedBasemap.isNotEmpty
            ? storedBasemap
            : _defaultBasemapId;
        final storedNetwork = map[_networkEdgesKey];
        showNetworkEdges = storedNetwork is bool ? storedNetwork : true;
        final storedExtEnabled = map[_extGnssEnabledKey];
        externalGnssEnabled = storedExtEnabled is bool ? storedExtEnabled : false;
        final storedExtAddr = map[_extGnssAddressKey] as String?;
        externalGnssAddress =
            (storedExtAddr == null || storedExtAddr.isEmpty) ? null : storedExtAddr;
        final storedExtName = map[_extGnssNameKey] as String?;
        externalGnssName =
            (storedExtName == null || storedExtName.isEmpty) ? null : storedExtName;
        final storedEmail = map[_signedInEmailKey] as String?;
        signedInEmail =
            (storedEmail == null || storedEmail.isEmpty) ? null : storedEmail;
        final storedName = map[_signedInNameKey] as String?;
        signedInName =
            (storedName == null || storedName.isEmpty) ? null : storedName;
        final storedPerms = map[_permissionsKey] as List?;
        permissions = storedPerms == null
            ? const []
            : storedPerms.map((p) => p.toString()).toList();
        final storedTracking = map[_fieldTrackingKey];
        fieldTrackingEnabled = storedTracking is bool ? storedTracking : true;
        final storedDisclosure = map[_fieldTrackingDisclosureKey];
        fieldTrackingDisclosureEnabled =
            storedDisclosure is bool ? storedDisclosure : true;
        final storedExpiry = map[_accessExpiresKey] as String?;
        accessExpiresAt = (storedExpiry == null || storedExpiry.isEmpty)
            ? null
            : DateTime.tryParse(storedExpiry)?.toUtc();
        final storedToken = map[_tokenKey] as String?;
        legacyToken =
            (storedToken == null || storedToken.isEmpty) ? null : storedToken;
      } catch (_) {
        // Corrupt/unreadable settings -- fall back to defaults rather than crash.
        baseUrl = defaultBaseUrl;
        snappingEnabled = true;
        basemapId = _defaultBasemapId;
        showNetworkEdges = true;
      }
    }

    token = await _readToken();
    refreshToken = await _readRefreshToken();
    // Migrate a token written by the old JSON-only build into the keystore, then
    // strip it from the file so the credential no longer sits in plain JSON.
    if (token == null && legacyToken != null) {
      token = legacyToken;
      await _writeToken(legacyToken);
      await _persist();
    }
  }

  Future<void> setBaseUrl(String value) async {
    final trimmed = value.trim().replaceAll(RegExp(r'/+$'), '');
    baseUrl = trimmed.isEmpty ? defaultBaseUrl : trimmed;
    await _persist();
  }

  Future<void> setToken(String? value) async {
    final trimmed = value?.trim();
    token = (trimmed == null || trimmed.isEmpty) ? null : trimmed;
    if (token == null) {
      await _deleteToken();
    } else {
      await _writeToken(token!);
    }
  }

  /// Record a successful field login: store the minted [token] in the keystore
  /// and remember who it belongs to for the "signed in as" label. The token is
  /// the credential; the identity is just for display.
  Future<void> signIn({
    required String token,
    String? refreshToken,
    DateTime? accessExpiresAt,
    String? email,
    String? displayName,
    List<String> permissions = const [],
    bool fieldTrackingEnabled = true,
    bool fieldTrackingDisclosureEnabled = true,
  }) async {
    await setToken(token);
    await _setRefreshToken(refreshToken);
    this.accessExpiresAt = accessExpiresAt;
    final e = email?.trim();
    final n = displayName?.trim();
    signedInEmail = (e == null || e.isEmpty) ? null : e;
    signedInName = (n == null || n.isEmpty) ? null : n;
    this.permissions = permissions;
    this.fieldTrackingEnabled = fieldTrackingEnabled;
    this.fieldTrackingDisclosureEnabled = fieldTrackingDisclosureEnabled;
    await _persist();
  }

  /// Apply a refreshed session (POST /auth/field-refresh): swap in the new access
  /// token + expiry, keeping the identity/permissions. The refresh token is unchanged
  /// in v1 (non-rotating) but stored again defensively in case the server ever rotates.
  Future<void> applyRefreshedSession({
    required String token,
    String? refreshToken,
    DateTime? accessExpiresAt,
  }) async {
    await setToken(token);
    if (refreshToken != null && refreshToken.isNotEmpty) {
      await _setRefreshToken(refreshToken);
    }
    this.accessExpiresAt = accessExpiresAt;
    await _persist();
  }

  /// Refresh the org-wide tracking policy from an ONGOING server response (the sync
  /// heartbeat), not just login. The field token is non-expiring, so login is a
  /// one-time event — a policy change an admin makes later would never reach an
  /// already-signed-in device if it only arrived at login. Sync carries it, and this
  /// applies it. Persists (and returns true) only when something actually changed, so
  /// the caller can react — e.g. surface the disclosure notice when it newly turns on.
  Future<bool> applyFieldTrackingPolicy({
    required bool enabled,
    required bool disclosureEnabled,
  }) async {
    if (fieldTrackingEnabled == enabled &&
        fieldTrackingDisclosureEnabled == disclosureEnabled) {
      return false;
    }
    fieldTrackingEnabled = enabled;
    fieldTrackingDisclosureEnabled = disclosureEnabled;
    await _persist();
    return true;
  }

  /// Sign out: drop the token from the keystore and forget the identity. The
  /// base URL and other prefs are kept, so signing back in only needs
  /// credentials. Server-side the token stays valid until an admin revokes it
  /// (there is no per-token id on the device to self-revoke) -- a lost device is
  /// handled by revoking centrally.
  Future<void> signOut() async {
    await setToken(null);
    await _setRefreshToken(null);
    accessExpiresAt = null;
    signedInEmail = null;
    signedInName = null;
    permissions = const [];
    await _persist();
  }

  Future<void> setSnappingEnabled(bool value) async {
    snappingEnabled = value;
    await _persist();
  }

  Future<void> setBasemapId(String value) async {
    basemapId = value.isEmpty ? _defaultBasemapId : value;
    await _persist();
  }

  Future<void> setShowNetworkEdges(bool value) async {
    showNetworkEdges = value;
    await _persist();
  }

  /// Select the external Bluetooth GNSS receiver at [address] (with a display
  /// [name]) as the position source, or clear the selection back to the phone's
  /// internal chip when [address] is null. Persists so the choice survives a
  /// restart; the provider layer rebuilds the live source off this.
  Future<void> setExternalGnss({String? address, String? name}) async {
    final addr = address?.trim();
    if (addr == null || addr.isEmpty) {
      externalGnssEnabled = false;
      externalGnssAddress = null;
      externalGnssName = null;
    } else {
      externalGnssEnabled = true;
      externalGnssAddress = addr;
      final n = name?.trim();
      externalGnssName = (n == null || n.isEmpty) ? null : n;
    }
    await _persist();
  }

  /// Persist the non-secret preferences to the JSON file. The token is
  /// deliberately NOT written here -- it lives in the keystore.
  Future<void> _persist() async {
    final file = await _resolveFile();
    final map = <String, dynamic>{
      _baseUrlKey: baseUrl,
      _snappingKey: snappingEnabled,
      _basemapKey: basemapId,
      _networkEdgesKey: showNetworkEdges,
      _extGnssEnabledKey: externalGnssEnabled,
      if (externalGnssAddress != null) _extGnssAddressKey: externalGnssAddress,
      if (externalGnssName != null) _extGnssNameKey: externalGnssName,
      if (signedInEmail != null) _signedInEmailKey: signedInEmail,
      if (signedInName != null) _signedInNameKey: signedInName,
      if (permissions.isNotEmpty) _permissionsKey: permissions,
      _fieldTrackingKey: fieldTrackingEnabled,
      _fieldTrackingDisclosureKey: fieldTrackingDisclosureEnabled,
      if (accessExpiresAt != null)
        _accessExpiresKey: accessExpiresAt!.toUtc().toIso8601String(),
    };
    await file.writeAsString(jsonEncode(map), flush: true);
  }

  /// Set (or clear, when null/empty) the refresh token in the keystore and mirror it
  /// into [refreshToken]. A credential, so keystore only -- never the JSON file.
  Future<void> _setRefreshToken(String? value) async {
    final trimmed = value?.trim();
    refreshToken = (trimmed == null || trimmed.isEmpty) ? null : trimmed;
    if (refreshToken == null) {
      await _deleteRefreshToken();
    } else {
      await _writeRefreshToken(refreshToken!);
    }
  }

  Future<String?> _readRefreshToken() async {
    try {
      final t = await _secure.read(key: _secureRefreshKey);
      return (t == null || t.isEmpty) ? null : t;
    } catch (_) {
      return null;
    }
  }

  Future<void> _writeRefreshToken(String value) async {
    try {
      await _secure.write(key: _secureRefreshKey, value: value);
    } catch (_) {}
  }

  Future<void> _deleteRefreshToken() async {
    try {
      await _secure.delete(key: _secureRefreshKey);
    } catch (_) {}
  }

  // Keystore access, best-effort: a MissingPluginException (e.g. a unit-test
  // harness with no platform channels) or a keystore error degrades to "no token"
  // / a dropped write rather than crashing the app or a test.
  Future<String?> _readToken() async {
    try {
      final t = await _secure.read(key: _secureTokenKey);
      return (t == null || t.isEmpty) ? null : t;
    } catch (_) {
      return null;
    }
  }

  Future<void> _writeToken(String value) async {
    try {
      await _secure.write(key: _secureTokenKey, value: value);
    } catch (_) {}
  }

  Future<void> _deleteToken() async {
    try {
      await _secure.delete(key: _secureTokenKey);
    } catch (_) {}
  }
}
