import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'api/fieldsync_client.dart';
import 'api/models.dart';
import 'config/settings_repository.dart';
import 'geo/bluetooth_gnss.dart';
import 'geo/gnss_sample.dart';
import 'geo/gnss_source.dart';
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
///
/// While any build is still in flight (queued/running -- not [PackSummary.isTerminal])
/// this self-schedules a re-fetch every few seconds, so a finished build flips to
/// "succeeded" (and shows its Download button) on its own -- without this the tile
/// spins forever and the user never learns the pack is ready. Polling stops the
/// moment every pack is terminal, and the timer is cancelled when the provider is
/// disposed (the home screen is gone). The re-fetch is a refresh, so the UI keeps
/// showing the current list rather than flashing a spinner (`when`'s default
/// skipLoadingOnRefresh).
final remotePacksProvider = FutureProvider.autoDispose<List<PackSummary>>(
  (ref) async {
    final packs = await ref.watch(fieldsyncClientProvider).listPacks();
    if (packs.any((p) => !p.isTerminal)) {
      final timer =
          Timer(const Duration(seconds: 4), () => ref.invalidateSelf());
      ref.onDispose(timer.cancel);
    }
    return packs;
  },
);

/// Packs already downloaded to the device.
final downloadedPacksProvider = FutureProvider.autoDispose<List<LocalPack>>(
  (ref) => ref.watch(packStoreProvider).listDownloaded(),
);

/// The packable feature-class catalog (GET /feature-classes) -- backs the class
/// picker on the "new pack" form. autoDispose so it refetches each time the form
/// opens (the catalog rarely changes, but staleness here is cheap to avoid).
final featureClassesProvider =
    FutureProvider.autoDispose<List<FeatureClassSummary>>(
  (ref) => ref.watch(fieldsyncClientProvider).listFeatureClasses(),
);

/// The Bluetooth control channel (Slice 4) -- lists bonded devices, opens/closes
/// the RFCOMM socket. Stateless wrapper over a MethodChannel, so a plain Provider.
final bluetoothGnssProvider = Provider<BluetoothGnss>((ref) => BluetoothGnss());

/// Which receiver feeds the live source (Slice 4): the phone's internal chip, or a
/// bonded external Bluetooth receiver at [address]. Seeded from persisted settings
/// so a chosen receiver survives a restart; the source picker updates this (and
/// persists), which rebuilds [gnssSourceProvider] onto the new transport.
class GnssSourceSelection {
  const GnssSourceSelection({required this.external, this.address, this.name});

  final bool external;
  final String? address;
  final String? name;

  /// Only treat the selection as external when we actually have a device to reach.
  bool get useExternal => external && address != null && address!.isNotEmpty;
}

final gnssSourceSelectionProvider = StateProvider<GnssSourceSelection>((ref) {
  final s = ref.watch(settingsRepositoryProvider);
  return GnssSourceSelection(
    external: s.externalGnssEnabled,
    address: s.externalGnssAddress,
    name: s.externalGnssName,
  );
});

/// The live GNSS source (Phase: gnss-quality, Slice 1; external in Slice 4). Kept
/// alive for the app so the source object is stable; the sensor itself only runs
/// while something listens to [gnssStreamProvider]. Watches
/// [gnssSourceSelectionProvider], so switching to/from an external receiver
/// rebuilds this onto the right transport (the old source is disposed first).
final gnssSourceProvider = Provider<GnssSource>((ref) {
  final selection = ref.watch(gnssSourceSelectionProvider);
  if (selection.useExternal) {
    final external = ExternalGnssSource(address: selection.address!);
    ref.onDispose(external.dispose);
    return external;
  }
  final source = InternalGnssSource();
  // dispose() (closing the underlying StreamController) is the concrete source's,
  // not on the GnssSource interface, so hold the concrete type for teardown.
  ref.onDispose(source.dispose);
  return source;
});

/// The live fix stream, colour-coded readout (Slice 2) and stamp-on-capture
/// (Slice 3) both read this. autoDispose so the GPS is released the moment no
/// screen is watching it -- start()/stop() are driven off listen/dispose here.
final gnssStreamProvider = StreamProvider.autoDispose<GnssSample>((ref) {
  final source = ref.watch(gnssSourceProvider);
  source.start();
  ref.onDispose(source.stop);
  return source.stream;
});
