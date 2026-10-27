import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/fieldsync_client.dart';
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

  @override
  void initState() {
    super.initState();
    final settings = ref.read(settingsRepositoryProvider);
    _baseUrl = TextEditingController(text: settings.baseUrl);
    _email = TextEditingController(text: settings.signedInEmail ?? '');
    _password = TextEditingController();
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
      await settings.signIn(
        token: res.token,
        email: res.email.isNotEmpty ? res.email : email,
        displayName: res.displayName,
        permissions: res.permissions,
      );
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
          TextField(
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
          ),
          const SizedBox(height: 24),
          if (signedIn) _signedInCard(settings) else _signInForm(),
        ],
      ),
    );
  }

  Widget _signInForm() {
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
        const SizedBox(height: 24),
        FilledButton.icon(
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
