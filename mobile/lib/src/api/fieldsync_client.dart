import 'dart:convert';
import 'dart:io';
import 'dart:math' as math;

import 'package:http/http.dart' as http;

import '../config/settings_repository.dart';
import '../edit/edit_database.dart';
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

  /// POST /auth/field-login -- exchange an email + password for a long-lived
  /// bearer token. This is the ONE unauthenticated call (it is what mints the
  /// credential every other call carries), so it sends no bearer header -- only
  /// the base URL must be set. The returned token is the same shape as one an
  /// admin would mint with `authadmin mint-token`; the caller stores it in the
  /// keystore and the app is "signed in". A bad credential is a generic 401.
  Future<FieldLoginResult> login(String email, String password,
      {String? deviceName}) async {
    final res = await _http.post(
      _uri('/auth/field-login'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({
        'email': email,
        'password': password,
        if (deviceName != null && deviceName.trim().isNotEmpty)
          'device_name': deviceName.trim(),
      }),
    );
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    return FieldLoginResult(
      token: body['token'] as String? ?? '',
      email: body['email'] as String? ?? '',
      displayName: body['display_name'] as String? ?? '',
    );
  }

  /// GET /packs -- the caller's recent builds, newest first.
  Future<List<PackSummary>> listPacks() async {
    final res = await _http.get(_uri('/packs'), headers: _headers());
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final rows = (body['packs'] as List? ?? const [])
        .cast<Map<String, dynamic>>();
    return rows.map(PackSummary.fromJson).toList();
  }

  /// GET /feature-classes -- the packable class catalog, so the "new pack" form
  /// can show a pick-list instead of a free-text code field. Online-only (you
  /// build a pack while connected anyway).
  Future<List<FeatureClassSummary>> listFeatureClasses() async {
    final res = await _http.get(_uri('/feature-classes'), headers: _headers());
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final rows = (body['feature_classes'] as List? ?? const [])
        .cast<Map<String, dynamic>>();
    return rows.map(FeatureClassSummary.fromJson).toList();
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

  /// POST /sync -- push the device's outbox to the server's durable inbox (Phase
  /// 23). Sends the pending ops verbatim (their payloads are already crud input
  /// shape) and returns one capture result per op, keyed by op_id, so the caller
  /// marks synced exactly what the server captured and leaves the rest queued.
  /// [poll] piggy-backs a set of already-captured op_ids the device is awaiting an
  /// apply outcome for -- the server returns their current statuses on the same
  /// round-trip, so a push reconciles earlier in-flight ops for free.
  Future<SyncResponse> sync(List<OutboxOp> ops,
      {List<String> poll = const []}) async {
    final res = await _http.post(
      _uri('/sync'),
      headers: _headers(json: true),
      body: jsonEncode({
        'ops': [for (final o in ops) _opToJson(o)],
        if (poll.isNotEmpty) 'poll': poll,
      }),
    );
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final results = (body['results'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(SyncOpResult.fromJson)
        .toList();
    final statuses = (body['statuses'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map(SyncOpStatus.fromJson)
        .toList();
    return SyncResponse(results: results, statuses: statuses);
  }

  /// GET /sync/status -- the standalone status pull (Phase 23 Slice 2): reconcile
  /// in-flight ops (captured but not yet known applied) when there's nothing new to
  /// push. Chunks into <=500-id requests (the server's per-poll cap) and
  /// concatenates. An op the server never captured simply isn't in the response, so
  /// the caller leaves it queued/in-flight to reconcile later.
  Future<List<SyncOpStatus>> syncStatus(List<String> opIds) async {
    final out = <SyncOpStatus>[];
    for (var i = 0; i < opIds.length; i += 500) {
      final chunk = opIds.sublist(i, math.min(i + 500, opIds.length));
      final uri = Uri.parse('${_settings.baseUrl}/sync/status')
          .replace(queryParameters: {'op_ids': chunk.join(',')});
      final res = await _http.get(uri, headers: _headers());
      _ensureOk(res);
      final body = jsonDecode(res.body) as Map<String, dynamic>;
      out.addAll((body['statuses'] as List? ?? const [])
          .cast<Map<String, dynamic>>()
          .map(SyncOpStatus.fromJson));
    }
    return out;
  }

  /// GET /features/{code}/{id}/gps-captures -- a feature's GNSS survey-quality
  /// captures, newest first (Phase gnss-quality Slice 3b). Server-only rows (they
  /// never ride the offline pack), so this is online-only; the detail screen
  /// fetches lazily and degrades to an "offline" note when it throws. An empty
  /// list is the normal answer for any feature never placed by GPS.
  Future<List<GpsCapture>> listGpsCaptures(
      String featureClass, String featureId) async {
    final res = await _http.get(
      _uri('/features/$featureClass/$featureId/gps-captures'),
      headers: _headers(),
    );
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final rows = (body['gps_captures'] as List? ?? const [])
        .cast<Map<String, dynamic>>();
    return rows.map(GpsCapture.fromJson).toList();
  }

  // ---- Image attachments (Phase 22 Slice 5) ---------------------------------

  /// Upload one captured image through the bearer `/attachments` surface: the same
  /// three-step register -> presigned PUT -> commit dance the web runs. Idempotent
  /// on the client-minted [a.id] -- a re-run after a partial failure re-registers
  /// the same id (server no-op) and re-PUTs/commits, so a flaky upload just
  /// retries. Throws on any step's failure so the caller leaves it queued.
  Future<void> uploadAttachment(LocalAttachment a) async {
    final file = File(a.localPath);
    if (!await file.exists()) {
      throw FieldsyncException(0, 'captured file is missing: ${a.localPath}');
    }
    final bytes = await file.readAsBytes();

    // 1) Register -> presigned PUT URL + the content-type to PUT with.
    final regRes = await _http.post(
      _uri('/attachments/${a.featureClass}/${a.featureId}'),
      headers: _headers(json: true),
      body: jsonEncode({
        'id': a.id,
        'filename': a.filename,
        'content_type': a.contentType,
        'byte_size': a.byteSize,
        'kind': 'image',
      }),
    );
    _ensureOk(regRes, expected: 201);
    final regBody = jsonDecode(regRes.body) as Map<String, dynamic>;
    final uploadUrl = regBody['upload_url'] as String? ?? '';
    final uploadContentType =
        regBody['upload_content_type'] as String? ?? a.contentType;
    if (uploadUrl.isEmpty) {
      throw FieldsyncException(regRes.statusCode, 'no upload_url in register response');
    }

    // 2) PUT the raw bytes straight to RustFS. The presigned URL self-authorizes,
    //    so this carries NO bearer token -- just the agreed Content-Type.
    final putRes = await _http.put(
      Uri.parse(uploadUrl),
      headers: {'Content-Type': uploadContentType},
      body: bytes,
    );
    if (putRes.statusCode < 200 || putRes.statusCode >= 300) {
      throw FieldsyncException(putRes.statusCode, 'upload PUT failed');
    }

    // 3) Commit -> server confirms the object landed and generates the thumbnail.
    final commitRes = await _http.post(
      _uri('/attachments/${a.featureClass}/${a.featureId}/${a.id}/commit'),
      headers: _headers(),
    );
    _ensureOk(commitRes);
  }

  /// A short-lived presigned URL to view an already-uploaded attachment's
  /// full-resolution original (images serve inline). Online-only.
  Future<String> attachmentUrl(
      String featureClass, String featureId, String attachmentId) async {
    final res = await _http.get(
      _uri('/attachments/$featureClass/$featureId/$attachmentId/url'),
      headers: _headers(),
    );
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    return body['url'] as String? ?? '';
  }

  Map<String, dynamic> _opToJson(OutboxOp o) => {
        'op_id': o.opId,
        'feature_id': o.featureId,
        'feature_class': o.featureClass,
        'kind': o.kind,
        'payload': o.payload,
        if (o.baseVersion != null) 'base_version': o.baseVersion,
      };

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
