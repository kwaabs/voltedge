import 'dart:convert';
import 'dart:io';
import 'dart:math' as math;

import 'package:flutter_web_auth_2/flutter_web_auth_2.dart';
import 'package:http/http.dart' as http;

import '../config/settings_repository.dart';
import '../edit/edit_database.dart';
import 'app_release.dart';
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
    return _parseSession(jsonDecode(res.body) as Map<String, dynamic>);
  }

  /// Parse a field-session envelope (login / SSO exchange / refresh all share it):
  /// the access + refresh tokens, the access expiry, identity, permissions, and the
  /// org-wide tracking policy. Mirrors fieldpack writeFieldSession.
  FieldLoginResult _parseSession(Map<String, dynamic> body) {
    final expRaw = body['access_expires_at'] as String?;
    return FieldLoginResult(
      token: body['token'] as String? ?? '',
      refreshToken: body['refresh_token'] as String?,
      accessExpiresAt:
          (expRaw == null || expRaw.isEmpty) ? null : DateTime.tryParse(expRaw)?.toUtc(),
      email: body['email'] as String? ?? '',
      displayName: body['display_name'] as String? ?? '',
      permissions: (body['permissions'] as List? ?? const [])
          .map((p) => p.toString())
          .toList(),
      fieldTrackingEnabled: body['field_tracking_enabled'] as bool? ?? true,
      fieldTrackingDisclosureEnabled:
          body['field_tracking_disclosure_enabled'] as bool? ?? true,
    );
  }

  /// GET /auth/sso-config -- where to START the SSO flow. SSO lives on cmd/server
  /// (/auth/oidc/...), which may differ from this fieldsync origin; the server tells
  /// us the base. Falls back to our own base URL (single-gateway deployments).
  Future<String> ssoConfig() async {
    final res = await _http.get(_uri('/auth/sso-config'));
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final base = body['login_base'] as String?;
    return (base == null || base.isEmpty) ? _settings.baseUrl : base;
  }

  /// GET `loginBase/auth/providers` -- the SSO buttons to offer (public). Returns an
  /// empty list if the web origin has no providers configured (local-only login).
  Future<List<SsoProvider>> fetchSsoProviders(String loginBase) async {
    final res = await _http.get(Uri.parse('$loginBase/auth/providers'));
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    return (body['providers'] as List? ?? const [])
        .cast<Map<String, dynamic>>()
        .map((p) => SsoProvider(
              key: p['key'] as String? ?? '',
              displayName: p['display_name'] as String? ?? '',
            ))
        .where((p) => p.key.isNotEmpty)
        .toList();
  }

  /// The SSO providers to offer on the login screen: resolve the SSO origin, then
  /// list its providers. Empty when SSO isn't configured (local-only login).
  Future<List<SsoProvider>> ssoProviders() async {
    final loginBase = await ssoConfig();
    return fetchSsoProviders(loginBase);
  }

  /// Sign in with SSO: open the server-mediated OIDC flow in the SYSTEM browser
  /// (Azure discourages embedded webviews), wait for the voltedge:// hand-off, and
  /// redeem its one-time code for a field session. The IdP only ever sees the
  /// server's registered callback; voltedge:// is our own app scheme, not registered
  /// with the IdP.
  Future<FieldLoginResult> signInWithSSO(String providerKey,
      {String? deviceName}) async {
    final loginBase = await ssoConfig();
    final url = Uri.parse('$loginBase/auth/oidc/$providerKey/login').replace(
      queryParameters: {
        'field': '1',
        if (deviceName != null && deviceName.trim().isNotEmpty)
          'device_name': deviceName.trim(),
      },
    ).toString();
    final callback = await FlutterWebAuth2.authenticate(
      url: url,
      callbackUrlScheme: 'voltedge',
    );
    final uri = Uri.parse(callback);
    final error = uri.queryParameters['error'];
    if (error != null && error.isNotEmpty) {
      throw FieldsyncException(401, _ssoErrorMessage(error));
    }
    final code = uri.queryParameters['code'];
    if (code == null || code.isEmpty) {
      throw FieldsyncException(401, 'SSO sign-in did not complete.');
    }
    return exchangeSSO(code, deviceName: deviceName);
  }

  String _ssoErrorMessage(String code) {
    switch (code) {
      case 'no_field_access':
        return 'This account does not have field-app access.';
      case 'no_account':
        return 'No VoltEdge account is linked to this sign-in.';
      default:
        return 'SSO sign-in failed. Please try again.';
    }
  }

  /// POST /auth/field-sso/exchange -- redeem the one-time SSO code for a session.
  Future<FieldLoginResult> exchangeSSO(String code, {String? deviceName}) async {
    final res = await _http.post(
      _uri('/auth/field-sso/exchange'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({
        'code': code,
        if (deviceName != null && deviceName.trim().isNotEmpty)
          'device_name': deviceName.trim(),
      }),
    );
    _ensureOk(res);
    return _parseSession(jsonDecode(res.body) as Map<String, dynamic>);
  }

  /// POST /auth/field-refresh -- swap the stored refresh token for a fresh access
  /// token and persist it (same refresh token back -- non-rotating). No-op when the
  /// device holds a legacy non-expiring token (no refresh token).
  Future<void> refreshSession() async {
    final refresh = _settings.refreshToken;
    if (refresh == null || refresh.isEmpty) return;
    final res = await _http.post(
      _uri('/auth/field-refresh'),
      headers: {'Content-Type': 'application/json'},
      body: jsonEncode({'refresh_token': refresh}),
    );
    _ensureOk(res);
    final session = _parseSession(jsonDecode(res.body) as Map<String, dynamic>);
    await _settings.applyRefreshedSession(
      token: session.token,
      refreshToken: session.refreshToken,
      accessExpiresAt: session.accessExpiresAt,
    );
    // The refresh response also carries the current tracking policy.
    await _settings.applyFieldTrackingPolicy(
      enabled: session.fieldTrackingEnabled,
      disclosureEnabled: session.fieldTrackingDisclosureEnabled,
    );
  }

  /// Refresh the access token if it's within a minute of expiry (or past it) and a
  /// refresh token is held. Best-effort: on failure the stale token is left and the
  /// next call 401s, prompting a re-login. Call before recurring authed work.
  Future<void> ensureFreshAccess() async {
    final exp = _settings.accessExpiresAt;
    final refresh = _settings.refreshToken;
    if (refresh == null || refresh.isEmpty || exp == null) return; // legacy token
    if (DateTime.now().toUtc().isBefore(exp.subtract(const Duration(minutes: 1)))) {
      return; // still fresh
    }
    try {
      await refreshSession();
    } catch (_) {
      // leave it; the authed call will 401 and the UI prompts re-login.
    }
  }

  /// POST /auth/field-logout -- revoke the refresh token on sign-out (best-effort).
  Future<void> revokeRefreshToken(String refreshToken) async {
    if (refreshToken.isEmpty) return;
    try {
      await _http.post(
        _uri('/auth/field-logout'),
        headers: {'Content-Type': 'application/json'},
        body: jsonEncode({'refresh_token': refreshToken}),
      );
    } catch (_) {
      // best-effort; the access token lapses on its own regardless.
    }
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

  /// GET /cells -- the shared, admin pre-built coverage cells this crew's access
  /// covers, each with its latest build (shared-field-packs S2). Online-only: you
  /// browse and pick up a cell while connected, then work it offline. The server hides
  /// any cell the crew can't fully cover (territory + the base's extra read-perms), so
  /// every row returned is pickable.
  Future<List<CellSummary>> listCells() async {
    final res = await _http.get(_uri('/cells'), headers: _headers());
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    final rows =
        (body['cells'] as List? ?? const []).cast<Map<String, dynamic>>();
    return rows.map(CellSummary.fromJson).toList();
  }

  /// POST /cells/{id}/pickup -- pick up a shared cell: the server re-checks coverage,
  /// records the pickup in the download ledger, and returns the base version's pack id
  /// + a fresh download manifest. [deviceId] (optional) distinguishes one crew member's
  /// two tablets in the supervisor's usage view. A cell the crew can't cover is 403; a
  /// cell with no build yet is 409 -- both surface as a FieldsyncException the UI shows.
  Future<CellPickup> pickupCell(String cellId, {String? deviceId}) async {
    final res = await _http.post(
      _uri('/cells/$cellId/pickup'),
      headers: _headers(json: true),
      body: jsonEncode({
        if (deviceId != null && deviceId.trim().isNotEmpty)
          'device_id': deviceId.trim(),
      }),
    );
    _ensureOk(res);
    return CellPickup.fromJson(jsonDecode(res.body) as Map<String, dynamic>);
  }

  /// GET /cells/{id}/overlay -- the crew member's PERSONAL overlay over a cell: their
  /// own work board + their own private drafts (shared-field-packs S2b). Online-only;
  /// fetched at pickup and each sync, then cached as an overlay.json sidecar beside the
  /// base. Gated exactly like pickup -- the overlay is only served for a cell the crew
  /// may pull -- and its contents are the caller's OWN data (no cross-user leak).
  Future<CellOverlay> fetchOverlay(String cellId) async {
    final res = await _http.get(_uri('/cells/$cellId/overlay'), headers: _headers());
    _ensureOk(res);
    return CellOverlay.fromJson(jsonDecode(res.body) as Map<String, dynamic>);
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
    await ensureFreshAccess();
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
    // Refresh the org-wide tracking policy the server rode on the response, so a
    // provisioned device picks up an admin's change without re-logging in.
    await _applyTrackingPolicy(body);
    return SyncResponse(results: results, statuses: statuses);
  }

  /// GET /tracking-policy — pull the org-wide tracking policy on demand and apply it
  /// (the device calls this on pack open, BEFORE deciding whether to record and
  /// whether to disclose). The field token is non-expiring, so login is one-time;
  /// this is how a device learns a policy an admin changed since it signed in. The
  /// endpoint returns `{enabled,disclosure_enabled}` directly, so it's wrapped to
  /// match the sync response shape _applyTrackingPolicy reads. Online-only; a throw
  /// is the caller's cue to keep the cached flags (offline).
  Future<void> refreshTrackingPolicy() async {
    final res = await _http.get(_uri('/tracking-policy'), headers: _headers());
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    await _applyTrackingPolicy({'policy': body});
  }

  /// Apply the org-wide tracking policy if the server attached it (sync + status
  /// responses carry `policy:{enabled,disclosure_enabled}`). Updates the shared
  /// SettingsRepository — the same instance the UI reads — so the recorder gate and
  /// the in-app disclosure reflect the current policy on the next rebuild / pack
  /// open, with no re-login. Missing keys fail open (default on), matching the server.
  Future<void> _applyTrackingPolicy(Map<String, dynamic> body) async {
    final policy = body['policy'];
    if (policy is! Map<String, dynamic>) return;
    await _settings.applyFieldTrackingPolicy(
      enabled: policy['enabled'] as bool? ?? true,
      disclosureEnabled: policy['disclosure_enabled'] as bool? ?? true,
    );
  }

  /// POST /field-tracks/breadcrumbs -- upload a batch of movement-trail points
  /// (field breadcrumb audit trail, migration 0178). A light, loss-tolerant push of
  /// the caller's OWN trail (idempotent by device-minted point id), separate from
  /// the durable /sync edit pipeline. Returns the number the server accepted.
  Future<int> uploadBreadcrumbs(List<LocalTrackPoint> points,
      {String? deviceId}) async {
    if (points.isEmpty) return 0;
    final res = await _http.post(
      _uri('/field-tracks/breadcrumbs'),
      headers: _headers(json: true),
      body: jsonEncode({
        if (deviceId != null && deviceId.isNotEmpty) 'device_id': deviceId,
        'points': [for (final p in points) p.toJson()],
      }),
    );
    _ensureOk(res);
    final body = jsonDecode(res.body) as Map<String, dynamic>;
    return (body['accepted'] as num?)?.toInt() ?? 0;
  }

  /// GET /sync/status -- the standalone status pull (Phase 23 Slice 2): reconcile
  /// in-flight ops (captured but not yet known applied) when there's nothing new to
  /// push. Chunks into <=500-id requests (the server's per-poll cap) and
  /// concatenates. An op the server never captured simply isn't in the response, so
  /// the caller leaves it queued/in-flight to reconcile later.
  Future<List<SyncOpStatus>> syncStatus(List<String> opIds) async {
    await ensureFreshAccess();
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
      // A status-only reconcile (nothing to push) still refreshes the policy.
      await _applyTrackingPolicy(body);
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

  /// GET /tags?code= -- resolve a scanned asset-tag code online (asset-tagging
  /// module S5), the fallback when the offline pack index has no such code (the
  /// asset sits outside the downloaded pack, or the pack predates the tag index).
  /// Uses the query form (not /tags/{code}) so a code with URL-reserved characters
  /// resolves cleanly, matching the web hook. The server applies the same
  /// scope/private-draft read gate a feature GET does, so an unknown/retired/
  /// out-of-scope code answers 404 -> FieldsyncException(404); the caller shows a
  /// friendly "not found" rather than treating it as a crash.
  Future<TagResolution> resolveTag(String code) async {
    final uri = Uri.parse('${_settings.baseUrl}/tags')
        .replace(queryParameters: {'code': code});
    final res = await _http.get(uri, headers: _headers());
    _ensureOk(res);
    return TagResolution.fromJson(jsonDecode(res.body) as Map<String, dynamic>);
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

  /// GET /app/latest -- the published field-app release manifest, served PUBLICLY
  /// (no bearer) by the self-distribution endpoints the fieldsync process also
  /// mounts. Returns null when nothing has been published yet (404), so a fresh
  /// deployment doesn't surface an error; other failures still throw so a flaky
  /// network degrades to "couldn't check" rather than a false "up to date".
  Future<AppRelease?> fetchLatestRelease() async {
    final res = await _http.get(_uri('/app/latest'), headers: _headers());
    if (res.statusCode == 404) return null;
    _ensureOk(res);
    return AppRelease.fromJson(jsonDecode(res.body) as Map<String, dynamic>);
  }

  /// The absolute URL the device opens to download the current APK (the
  /// /app/download redirect to a presigned RustFS object). Built from the same
  /// base URL every other call uses.
  String appDownloadUrl() => '${_settings.baseUrl}/app/download';

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
