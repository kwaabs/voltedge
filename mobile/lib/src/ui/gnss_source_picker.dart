import 'package:flutter/material.dart';
import 'package:flutter/services.dart' show PlatformException;
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../geo/bluetooth_gnss.dart';
import '../providers.dart';

/// Opens the GNSS source picker (Phase: gnss-quality, Slice 4) as a modal bottom
/// sheet. Lets the crew pick the phone's internal receiver or a bonded external
/// Bluetooth GNSS receiver; the choice is persisted and rebuilds the live source.
Future<void> showGnssSourcePicker(BuildContext context) => showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      showDragHandle: true,
      builder: (_) => const _GnssSourceSheet(),
    );

class _GnssSourceSheet extends ConsumerStatefulWidget {
  const _GnssSourceSheet();

  @override
  ConsumerState<_GnssSourceSheet> createState() => _GnssSourceSheetState();
}

/// What the sheet can currently do -- computed from platform / adapter / permission
/// so the body renders the right one-liner instead of an empty list.
enum _SheetState { loading, unsupported, off, needsPermission, ready }

class _GnssSourceSheetState extends ConsumerState<_GnssSourceSheet> {
  _SheetState _state = _SheetState.loading;
  List<BtDevice> _devices = const [];
  String? _error;
  bool _busy = false; // a connect/select in progress

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  Future<void> _refresh() async {
    setState(() {
      _state = _SheetState.loading;
      _error = null;
    });
    final bt = ref.read(bluetoothGnssProvider);
    if (!bt.isPlatformSupported) {
      setState(() => _state = _SheetState.unsupported);
      return;
    }
    try {
      if (!await bt.isSupported() || !await bt.isEnabled()) {
        setState(() => _state = _SheetState.off);
        return;
      }
      if (!await bt.hasPermission()) {
        setState(() => _state = _SheetState.needsPermission);
        return;
      }
      final devices = await bt.bondedDevices();
      if (!mounted) return;
      setState(() {
        _devices = devices;
        _state = _SheetState.ready;
      });
    } on PlatformException catch (e) {
      if (!mounted) return;
      setState(() {
        _error = e.message ?? 'Bluetooth error';
        _state = _SheetState.ready;
      });
    }
  }

  Future<void> _grantPermission() async {
    final bt = ref.read(bluetoothGnssProvider);
    final granted = await bt.requestPermission();
    if (!mounted) return;
    if (granted) {
      _refresh();
    } else {
      setState(() => _error = 'Bluetooth permission is needed to use an external receiver.');
    }
  }

  Future<void> _selectInternal() async {
    if (_busy) return;
    setState(() => _busy = true);
    await ref.read(settingsRepositoryProvider).setExternalGnss(address: null);
    ref.read(gnssSourceSelectionProvider.notifier).state =
        const GnssSourceSelection(external: false);
    if (mounted) Navigator.of(context).pop();
  }

  Future<void> _selectExternal(BtDevice d) async {
    if (_busy) return;
    setState(() => _busy = true);
    await ref
        .read(settingsRepositoryProvider)
        .setExternalGnss(address: d.address, name: d.name);
    ref.read(gnssSourceSelectionProvider.notifier).state =
        GnssSourceSelection(external: true, address: d.address, name: d.name);
    if (mounted) Navigator.of(context).pop();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final selection = ref.watch(gnssSourceSelectionProvider);
    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.satellite_alt_outlined,
                    size: 20, color: theme.colorScheme.primary),
                const SizedBox(width: 8),
                Text('GPS source',
                    style: theme.textTheme.titleMedium
                        ?.copyWith(fontWeight: FontWeight.w700)),
                const Spacer(),
                if (_state == _SheetState.ready)
                  IconButton(
                    tooltip: 'Refresh',
                    icon: const Icon(Icons.refresh, size: 20),
                    onPressed: _busy ? null : _refresh,
                  ),
              ],
            ),
            const SizedBox(height: 4),
            // The phone's own chip is always available.
            _sourceTile(
              context,
              icon: Icons.smartphone,
              title: 'Phone GPS',
              subtitle: 'Built-in receiver',
              selected: !selection.useExternal,
              onTap: _selectInternal,
            ),
            const Divider(height: 20),
            _externalSection(context, selection),
            if (_error != null) ...[
              const SizedBox(height: 8),
              Text(_error!,
                  style: theme.textTheme.bodySmall
                      ?.copyWith(color: theme.colorScheme.error)),
            ],
          ],
        ),
      ),
    );
  }

  Widget _externalSection(BuildContext context, GnssSourceSelection selection) {
    final theme = Theme.of(context);
    Widget note(String text, {Widget? action}) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(text, style: theme.textTheme.bodySmall),
              if (action != null) ...[const SizedBox(height: 8), action],
            ],
          ),
        );

    switch (_state) {
      case _SheetState.loading:
        return const Padding(
          padding: EdgeInsets.symmetric(vertical: 20),
          child: Center(
            child: SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
          ),
        );
      case _SheetState.unsupported:
        return note('External receivers aren\'t supported on iOS. '
            'Use the phone\'s built-in GPS.');
      case _SheetState.off:
        return note('Bluetooth is off. Turn it on in system settings, '
            'pair your receiver, then reopen this.');
      case _SheetState.needsPermission:
        return note(
          'Allow Bluetooth to connect to an external receiver.',
          action: FilledButton.tonal(
            onPressed: _grantPermission,
            child: const Text('Allow Bluetooth'),
          ),
        );
      case _SheetState.ready:
        if (_devices.isEmpty) {
          return note('No paired Bluetooth devices. Pair your GNSS receiver in '
              'system settings first, then refresh.');
        }
        return Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.only(bottom: 4),
              child: Text('Paired Bluetooth receivers',
                  style: theme.textTheme.labelMedium
                      ?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
            ),
            for (final d in _devices)
              _sourceTile(
                context,
                icon: Icons.bluetooth,
                title: d.name,
                subtitle: d.address,
                selected: selection.useExternal && selection.address == d.address,
                onTap: () => _selectExternal(d),
              ),
          ],
        );
    }
  }

  Widget _sourceTile(
    BuildContext context, {
    required IconData icon,
    required String title,
    required String subtitle,
    required bool selected,
    required VoidCallback onTap,
  }) {
    final theme = Theme.of(context);
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: Icon(icon, color: theme.colorScheme.primary),
      title: Text(title, overflow: TextOverflow.ellipsis),
      subtitle: Text(subtitle, overflow: TextOverflow.ellipsis),
      trailing: selected
          ? Icon(Icons.check_circle, color: theme.colorScheme.primary)
          : const Icon(Icons.radio_button_unchecked, color: Colors.transparent),
      onTap: _busy ? null : onTap,
    );
  }
}
