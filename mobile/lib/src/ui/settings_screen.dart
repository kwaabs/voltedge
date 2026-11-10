import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/fieldsync_client.dart';
import '../api/models.dart';
import '../config/settings_repository.dart';
import '../providers.dart';

/// Where the device is pointed, and who it's signed in as. The worker enters the
/// cmd/fieldsync base URL and their email + password; the app exchanges those for
/// a bearer token (POST /auth/field-login) and stores it in the OS keystore.
/// Nothing about the token is ever shown or typed -- username/password is only
/// the front of the door, a token is what walks through it (see fieldpack/login.go).
class SettingsScreen extends ConsumerStatefulWidget {
  const SettingsScreen({super.key});

  @override
  ConsumerState<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends ConsumerState<SettingsScreen> {
  late final TextEditingController _baseUrl;
  late final TextEditingController _email;
  late final TextEditingController _password;
  bool _obscure = true;
  bool _busy = false;

  /// Reveal the email/password form on the branded (SSO-first) login. Starts hidden
  /// behind a "Sign in with email" link when SSO is available; the form is shown
  /// outright when no SSO provider is configured (there's nothing else to use).
  bool _showEmailForm = false;

  /// Reveal the "Advanced" section (the fieldsync base URL) on the login view.
  bool _showAdvanced = false;

  /// The SSO providers to offer, fetched once. Empty / failed = local-only login
  /// (no SSO configured, or the server unreachable) -> no SSO buttons shown.
  Future<List<SsoProvider>>? _ssoProvidersFuture;

  @override
  void initState() {
    super.initState();
    final settings = ref.read(settingsRepositoryProvider);
    _baseUrl = TextEditingController(text: settings.baseUrl);
    _email = TextEditingController(text: settings.signedInEmail ?? '');
    _password = TextEditingController();
    _ssoProvidersFuture = ref.read(fieldsyncClientProvider).ssoProviders();
  }

  @override
  void dispose() {
    _baseUrl.dispose();
    _email.dispose();
    _password.dispose();
    super.dispose();
  }

  void _snack(String msg) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
  }

  /// Save the base URL, exchange email + password for a token, and store it.
  Future<void> _signIn() async {
    final email = _email.text.trim();
    final password = _password.text;
    if (email.isEmpty || password.isEmpty) {
      _snack('Enter your email and password.');
      return;
    }
    setState(() => _busy = true);
    final settings = ref.read(settingsRepositoryProvider);
    try {
      // Persist the base URL FIRST so the client (which reads it live) posts the
      // login to the right host.
      await settings.setBaseUrl(_baseUrl.text);
      final res = await ref.read(fieldsyncClientProvider).login(email, password);
      await _storeSession(settings, res, fallbackEmail: email);
      ref.invalidate(remotePacksProvider);
      if (!mounted) return;
      _snack('Signed in.');
      Navigator.of(context).pop();
    } on FieldsyncException catch (e) {
      _snack(e.message);
    } catch (e) {
      _snack('Could not sign in: $e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  /// Persist a successful session (password OR SSO). Both yield a FieldLoginResult
  /// carrying the access + refresh tokens and the access expiry.
  Future<void> _storeSession(SettingsRepository settings, FieldLoginResult res,
      {String? fallbackEmail}) async {
    await settings.signIn(
      token: res.token,
      refreshToken: res.refreshToken,
      accessExpiresAt: res.accessExpiresAt,
      email: res.email.isNotEmpty ? res.email : (fallbackEmail ?? ''),
      displayName: res.displayName,
      permissions: res.permissions,
      fieldTrackingEnabled: res.fieldTrackingEnabled,
      fieldTrackingDisclosureEnabled: res.fieldTrackingDisclosureEnabled,
    );
  }

  /// Sign in with SSO: run the system-browser OIDC flow and store the session.
  Future<void> _signInWithSSO(SsoProvider provider) async {
    setState(() => _busy = true);
    final settings = ref.read(settingsRepositoryProvider);
    try {
      await settings.setBaseUrl(_baseUrl.text);
      final res =
          await ref.read(fieldsyncClientProvider).signInWithSSO(provider.key);
      await _storeSession(settings, res);
      ref.invalidate(remotePacksProvider);
      if (!mounted) return;
      _snack('Signed in.');
      Navigator.of(context).pop();
    } on FieldsyncException catch (e) {
      _snack(e.message);
    } catch (e) {
      _snack('Could not sign in: $e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _signOut() async {
    setState(() => _busy = true);
    final settings = ref.read(settingsRepositoryProvider);
    // Revoke the refresh token server-side first (best-effort) so a lost device's
    // token can't be refreshed; the short-lived access token lapses on its own.
    final refresh = settings.refreshToken;
    if (refresh != null && refresh.isNotEmpty) {
      await ref.read(fieldsyncClientProvider).revokeRefreshToken(refresh);
    }
    await settings.signOut();
    ref.invalidate(remotePacksProvider);
    if (!mounted) return;
    _password.clear();
    setState(() => _busy = false);
    _snack('Signed out.');
  }

  /// Save just the base URL (available while signed in, in case the server moves).
  Future<void> _saveBaseUrl() async {
    final settings = ref.read(settingsRepositoryProvider);
    await settings.setBaseUrl(_baseUrl.text);
    ref.invalidate(remotePacksProvider);
    if (!mounted) return;
    _snack('Saved.');
  }

  @override
  Widget build(BuildContext context) {
    final settings = ref.watch(settingsRepositoryProvider);
    final signedIn = settings.isConfigured;
    return Scaffold(
      appBar: AppBar(title: const Text('Connection')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          // Signed in: keep the plain settings view (base URL + "signed in as").
          // Signed out: the branded, SSO-first login.
          if (signedIn) ...[
            _baseUrlField(),
            const SizedBox(height: 24),
            _signedInCard(settings),
          ] else
            _loginView(),
        ],
      ),
    );
  }

  /// The branded, SSO-first login: bolt + app name, SSO buttons as the primary
  /// action, email/password behind a "Sign in with email" link, and the fieldsync
  /// base URL tucked under "Advanced". When the org has no SSO provider configured
  /// (or the server is unreachable), the email form is shown outright -- there's
  /// nothing else to sign in with.
  Widget _loginView() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const SizedBox(height: 28),
        _brandHeader(),
        const SizedBox(height: 32),
        FutureBuilder<List<SsoProvider>>(
          future: _ssoProvidersFuture,
          builder: (context, snap) {
            if (snap.connectionState == ConnectionState.waiting) {
              return const Padding(
                padding: EdgeInsets.symmetric(vertical: 24),
                child: Center(child: CircularProgressIndicator()),
              );
            }
            final providers = snap.data ?? const <SsoProvider>[];
            final hasSso = providers.isNotEmpty;
            // Discovery FAILED (sso-config / providers unreachable) is different from
            // "no providers configured": the former is a server/deploy problem worth
            // surfacing with a retry, the latter is just a local-only org. On failure
            // we still fall through to the email form so sign-in isn't blocked.
            final ssoFailed = snap.hasError;
            final showEmail = !hasSso || _showEmailForm;
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                for (final p in providers) ...[
                  FilledButton.icon(
                    style: FilledButton.styleFrom(
                      minimumSize: const Size.fromHeight(48),
                    ),
                    onPressed: _busy ? null : () => _signInWithSSO(p),
                    icon: const Icon(Icons.shield_outlined),
                    label: Text('Sign in with ${p.displayName}'),
                  ),
                  const SizedBox(height: 10),
                ],
                if (hasSso && !showEmail)
                  TextButton(
                    onPressed:
                        _busy ? null : () => setState(() => _showEmailForm = true),
                    child: const Text('Sign in with email'),
                  ),
                if (ssoFailed) _ssoUnavailableHint(snap.error),
                if (showEmail) ...[
                  if (hasSso) ...[
                    const SizedBox(height: 12),
                    Row(
                      children: const [
                        Expanded(child: Divider()),
                        Padding(
                          padding: EdgeInsets.symmetric(horizontal: 12),
                          child: Text('or with email'),
                        ),
                        Expanded(child: Divider()),
                      ],
                    ),
                    const SizedBox(height: 12),
                  ],
                  _emailPasswordFields(),
                ],
              ],
            );
          },
        ),
        const SizedBox(height: 24),
        _advancedSection(),
      ],
    );
  }

  /// Re-run SSO provider discovery (after a failed fetch — e.g. the backend wasn't
  /// deployed yet, or the server was briefly unreachable).
  void _retrySso() {
    setState(() {
      _ssoProvidersFuture = ref.read(fieldsyncClientProvider).ssoProviders();
    });
  }

  /// Shown when SSO provider discovery FAILED (not merely "no SSO configured"): a
  /// muted notice + Retry, so the screen isn't silently SSO-less while an admin is
  /// bringing the server up. The email form still shows below, so sign-in isn't
  /// blocked. The raw reason is included small, to help debug a deploy.
  Widget _ssoUnavailableHint(Object? error) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      margin: const EdgeInsets.only(bottom: 16),
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: scheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(12),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.shield_outlined, size: 18, color: scheme.outline),
              const SizedBox(width: 8),
              Expanded(
                child: Text(
                  'Single sign-on is unavailable right now. Sign in with email '
                  'below, or retry.',
                  style: TextStyle(color: scheme.outline),
                ),
              ),
            ],
          ),
          if (error != null)
            Padding(
              padding: const EdgeInsets.only(top: 4, left: 26),
              child: Text(
                '$error',
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: TextStyle(fontSize: 11, color: scheme.outline),
              ),
            ),
          Align(
            alignment: Alignment.centerRight,
            child: TextButton.icon(
              onPressed: _busy ? null : _retrySso,
              icon: const Icon(Icons.refresh, size: 18),
              label: const Text('Retry'),
            ),
          ),
        ],
      ),
    );
  }

  /// Bolt mark + app name — the same brand as the launcher icon / splash.
  Widget _brandHeader() {
    final theme = Theme.of(context);
    return Column(
      children: [
        Container(
          width: 72,
          height: 72,
          decoration: BoxDecoration(
            color: const Color(0xFF00A878),
            borderRadius: BorderRadius.circular(18),
          ),
          child: const Icon(Icons.bolt, color: Colors.white, size: 44),
        ),
        const SizedBox(height: 16),
        Text('Voltedge field',
            style: theme.textTheme.headlineSmall
                ?.copyWith(fontWeight: FontWeight.w700)),
        const SizedBox(height: 4),
        Text(
          'Sign in to download and sync your field packs',
          textAlign: TextAlign.center,
          style: TextStyle(color: theme.colorScheme.outline),
        ),
      ],
    );
  }

  /// The email + password fields and their Sign in button.
  Widget _emailPasswordFields() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TextField(
          controller: _email,
          enabled: !_busy,
          keyboardType: TextInputType.emailAddress,
          autocorrect: false,
          enableSuggestions: false,
          textInputAction: TextInputAction.next,
          decoration: const InputDecoration(
            labelText: 'Email',
            border: OutlineInputBorder(),
          ),
        ),
        const SizedBox(height: 16),
        TextField(
          controller: _password,
          enabled: !_busy,
          obscureText: _obscure,
          autocorrect: false,
          enableSuggestions: false,
          onSubmitted: (_) => _busy ? null : _signIn(),
          decoration: InputDecoration(
            labelText: 'Password',
            border: const OutlineInputBorder(),
            suffixIcon: IconButton(
              icon: Icon(_obscure ? Icons.visibility : Icons.visibility_off),
              onPressed: () => setState(() => _obscure = !_obscure),
            ),
          ),
        ),
        const SizedBox(height: 20),
        FilledButton.icon(
          style: FilledButton.styleFrom(minimumSize: const Size.fromHeight(48)),
          onPressed: _busy ? null : _signIn,
          icon: _busy
              ? const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2))
              : const Icon(Icons.login),
          label: Text(_busy ? 'Signing in…' : 'Sign in'),
        ),
      ],
    );
  }

  /// The fieldsync base URL field. At the top of the signed-in view, and under the
  /// "Advanced" disclosure on the login view (most crews never touch it).
  Widget _baseUrlField() {
    return TextField(
      controller: _baseUrl,
      enabled: !_busy,
      keyboardType: TextInputType.url,
      autocorrect: false,
      decoration: const InputDecoration(
        labelText: 'fieldsync base URL',
        helperText:
            'Android emulator: http://10.0.2.2:3706 · real device: http://<LAN-ip>:3706',
        border: OutlineInputBorder(),
      ),
    );
  }

  /// A collapsed "Advanced" disclosure holding the base URL on the login view.
  Widget _advancedSection() {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Align(
          alignment: Alignment.center,
          child: TextButton.icon(
            onPressed: () => setState(() => _showAdvanced = !_showAdvanced),
            icon: Icon(_showAdvanced ? Icons.expand_less : Icons.expand_more),
            label: const Text('Advanced'),
          ),
        ),
        if (_showAdvanced) ...[
          const SizedBox(height: 8),
          _baseUrlField(),
        ],
      ],
    );
  }

  Widget _signedInCard(SettingsRepository settings) {
    final name = settings.signedInName;
    final email = settings.signedInEmail;
    final who = (name != null && name.isNotEmpty)
        ? name
        : (email ?? 'this device');
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Card(
          child: ListTile(
            leading: const Icon(Icons.verified_user_outlined),
            title: Text('Signed in as $who'),
            subtitle: (email != null && email.isNotEmpty && email != who)
                ? Text(email)
                : null,
          ),
        ),
        const SizedBox(height: 16),
        OutlinedButton.icon(
          onPressed: _busy ? null : _saveBaseUrl,
          icon: const Icon(Icons.save_outlined),
          label: const Text('Save base URL'),
        ),
        const SizedBox(height: 8),
        TextButton.icon(
          onPressed: _busy ? null : _signOut,
          icon: const Icon(Icons.logout),
          label: const Text('Sign out'),
        ),
      ],
    );
  }
}
