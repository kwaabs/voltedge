import 'dart:convert';
import 'dart:io';

import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';

/// SettingsRepository holds the two things the app needs to reach the
/// cmd/fieldsync engine: the base URL and the iam.api_tokens bearer token.
///
/// Storage note: these live in a small JSON file (`ve_settings.json`) in the
/// app-private documents directory (`getApplicationDocumentsDirectory`), which is
/// per-app sandboxed on Android/iOS. This is a deliberate step down from the OS
/// keystore (`flutter_secure_storage`): no published version of that plugin
/// (through 11.0.0) added the `if (agpMajor < 9) apply 'kotlin-android'` migration
/// guard, so it can't build under this AGP 9 / `android.builtInKotlin=true`
/// toolchain -- and built-in Kotlin is itself required by maplibre_gl 0.27. When
/// the plugin migrates, reinstate it here: the public API below is unchanged, so
/// only the private read/write/delete need to swap back to the keystore.
///
/// The repository is loaded once at startup (see main.dart) into in-memory fields
/// so the rest of the app reads them synchronously; writes update memory and
/// persist.
class SettingsRepository {
  /// [file] is injectable for tests; in the app it is resolved lazily to
  /// `<app documents>/ve_settings.json` on first access.
  SettingsRepository({File? file}) : _file = file;

  File? _file;

  static const _fileName = 've_settings.json';
  static const _baseUrlKey = 'baseUrl';
  static const _tokenKey = 'token';

  /// The Android emulator reaches the host machine at 10.0.2.2. On a real
  /// device this MUST be the machine's LAN address (e.g. http://192.168.1.20:3706)
  /// -- and RUSTFS_PUBLIC_ENDPOINT on the server must point at a host the device
  /// can reach too, since presigned download URLs are signed for that host.
  static const defaultBaseUrl = 'http://10.0.2.2:3706';

  String baseUrl = defaultBaseUrl;
  String? token;

  bool get isConfigured => token != null && token!.isNotEmpty;

  Future<File> _resolveFile() async {
    final existing = _file;
    if (existing != null) return existing;
    final dir = await getApplicationDocumentsDirectory();
    return _file = File(p.join(dir.path, _fileName));
  }

  Future<void> load() async {
    final file = await _resolveFile();
    if (!await file.exists()) {
      baseUrl = defaultBaseUrl;
      token = null;
      return;
    }
    try {
      final map = jsonDecode(await file.readAsString()) as Map<String, dynamic>;
      final storedBase = map[_baseUrlKey] as String?;
      baseUrl =
          (storedBase == null || storedBase.isEmpty) ? defaultBaseUrl : storedBase;
      final storedToken = map[_tokenKey] as String?;
      token = (storedToken == null || storedToken.isEmpty) ? null : storedToken;
    } catch (_) {
      // Corrupt/unreadable settings -- fall back to defaults rather than crash.
      baseUrl = defaultBaseUrl;
      token = null;
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
    await _persist();
  }

  Future<void> _persist() async {
    final file = await _resolveFile();
    final map = <String, String>{_baseUrlKey: baseUrl};
    final t = token;
    if (t != null) map[_tokenKey] = t;
    await file.writeAsString(jsonEncode(map), flush: true);
  }
}
