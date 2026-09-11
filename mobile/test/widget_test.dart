// A minimal smoke test: the app boots and renders its home screen. The default
// `flutter create` counter test was removed -- this app has no counter and its
// root widget is VoltEdgeFieldApp, not MyApp.

import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'package:voltedge_field/main.dart';
import 'package:voltedge_field/src/config/settings_repository.dart';
import 'package:voltedge_field/src/providers.dart';

void main() {
  testWidgets('App boots to the home screen', (WidgetTester tester) async {
    // settingsRepositoryProvider must be overridden (main() does this after
    // loading from disk); a fresh in-memory repository is enough for the boot.
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          settingsRepositoryProvider.overrideWithValue(SettingsRepository()),
        ],
        child: const VoltEdgeFieldApp(),
      ),
    );

    // One frame is enough to render the branded app bar; the pack lists resolve
    // asynchronously (and may error without platform channels), which the home
    // screen handles on its own -- so don't pumpAndSettle here.
    expect(find.text('VoltEdge Field'), findsOneWidget);
  });
}
