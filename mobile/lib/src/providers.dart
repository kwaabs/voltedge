import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'api/fieldsync_client.dart';
import 'api/models.dart';
import 'config/settings_repository.dart';
import 'pack/pack_store.dart';

/// The settings repository is created and loaded in main(), then injected here
/// via a ProviderScope override -- so reads across the app are synchronous.
final settingsRepositoryProvider = Provider<SettingsRepository>(
  (ref) => throw UnimplementedError(
    'settingsRepositoryProvider must be overridden in main()',
  ),
);

final fieldsyncClientProvider = Provider<FieldsyncClient>(
  (ref) => FieldsyncClient(ref.watch(settingsRepositoryProvider)),
);

final packStoreProvider = Provider<PackStore>((ref) => PackStore());

/// The caller's remote builds (GET /packs). autoDispose so it refetches each
/// time the home screen is shown; invalidate it to force a refresh.
final remotePacksProvider = FutureProvider.autoDispose<List<PackSummary>>(
  (ref) => ref.watch(fieldsyncClientProvider).listPacks(),
);

/// Packs already downloaded to the device.
final downloadedPacksProvider = FutureProvider.autoDispose<List<LocalPack>>(
  (ref) => ref.watch(packStoreProvider).listDownloaded(),
);
