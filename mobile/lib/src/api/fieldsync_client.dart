import 'dart:convert';

import 'package:http/http.dart' as http;

import '../config/settings_repository.dart';
import 'models.dart';

/// FieldsyncException carries a server-supplied error message (the {"error": ...}
/// envelope the Go handlers write) plus the HTTP status, so the UI can show a
/// human message instead of a raw exception.
class FieldsyncException implements Exception {
  FieldsyncException(this.statusCode, this.message);
  final int statusCode;
  final String message;
  @override
  String toString() => 'fieldsync $statusCode: $message';
}

/// FieldsyncClient talks to the cmd/fieldsync pack API. It reads the base URL +
/// bearer token from SettingsRepository on every call, so a settings change
/// takes effect without rebuilding the client.
class FieldsyncClient {
  FieldsyncClient(this._settings, {http.Client? httpClient})
      : _http = httpClient ?? http.Client();

  final SettingsRepository _settings;
  final http.Client _http;

  Uri _uri(String path) => Uri.parse('${_settings.baseUrl}$path');

  Map<String, String> _headers({bool json = false}) => {
        'Authorization': 'Bearer ${_settings.token ?? ''}',
        if (json) 'Content-Type': 'application/json',
      };

  /// GET /packs -- the caller's recent builds, newest first.
  Future<List<PackSummary>> listPacks() async {
    final res = await _http.get(_uri('/packs'), headers: _headers());
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final rows = (body['packs'] as List? ?? const [])
        .cast<Map<String, dynamic>>();
    return rows.map(PackSummary.fromJson).toList();
  }

  /// GET /packs/{id} -- poll one build; on success the body carries the manifest.
  Future<PackSummary> getPack(String id) async {
    final res = await _http.get(_uri('/packs/$id'), headers: _headers());
    _ensureOk(res);
    return PackSummary.fromJson(jsonDecode(res.body) as Map<String, dynamic>);
  }

  /// POST /packs -- enqueue a build for a bbox + class set. Returns the new
  /// pack id (the server answers 202 Accepted).
  Future<String> createPack({
    required List<double> bbox,
    required List<String> featureClasses,
    List<String>? territories,
  }) async {
    final res = await _http.post(
      _uri('/packs'),
      headers: _headers(json: true),
      body: jsonEncode({
        'bbox': bbox,
        'feature_classes': featureClasses,
        if (territories != null && territories.isNotEmpty)
          'territories': territories,
      }),
    );
    _ensureOk(res, expected: 202);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    return body['pack_id'] as String;
  }

  /// POST /packs/{id}/cancel -- cancel the caller's own queued/running build.
  Future<PackSummary> cancelPack(String id) async {
    final res = await _http.post(_uri('/packs/$id/cancel'), headers: _headers());
    _ensureOk(res);
    return PackSummary.fromJson(jsonDecode(res.body) as Map<String, dynamic>);
  }

  void _ensureOk(http.Response res, {int expected = 200}) {
    if (res.statusCode == expected) return;
    // 202 and 200 are both "fine" for reads that the server may answer either way.
    if (expected == 200 && res.statusCode == 202) return;
    String message = 'HTTP ${res.statusCode}';
    try {
      final body = jsonDecode(res.body) as Map<String, dynamic>;
      if (body['error'] is String) message = body['error'] as String;
    } catch (_) {
      if (res.body.isNotEmpty) message = res.body;
    }
    throw FieldsyncException(res.statusCode, message);
  }
}
