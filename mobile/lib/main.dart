import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'src/config/settings_repository.dart';
import 'src/providers.dart';
import 'src/ui/splash_screen.dart';
import 'src/ui/theme.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Load the persisted base URL + token once, then hand the repository to the
  // provider graph. Everything downstream reads it synchronously.
  final settings = SettingsRepository();
  await settings.load();

  runApp(
    ProviderScope(
      overrides: [settingsRepositoryProvider.overrideWithValue(settings)],
      child: const VoltEdgeFieldApp(),
    ),
  );
}

class VoltEdgeFieldApp extends StatelessWidget {
  const VoltEdgeFieldApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'VoltEdge Field',
      debugShowCheckedModeBanner: false,
      theme: AppTheme.light(),
      darkTheme: AppTheme.dark(),
      themeMode: ThemeMode.system,
      home: const SplashScreen(),
    );
  }
}
