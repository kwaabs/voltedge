import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../edit/edit_store.dart';
import '../map/basemaps.dart';
import '../pack/pack_database.dart';
import '../pack/pack_store.dart';
import '../providers.dart';
import 'format.dart';
import 'gnss_source_picker.dart';

/// The map's settings/about page (Phase 21 Slice 5c), pushed from the map app
/// bar. It reports what the map is currently showing -- the per-class feature
/// inventory and the local-edit tallies (new / modified / deleted) -- and carries
/// the map's controls: the basemap choice, a "Network links" toggle (draw a
/// selected feature's connectivity/relationship graph as map edges), and a
/// snapping toggle (snap edits onto nearby vertices). Each is persisted and the
/// map re-reads them when this page closes.
class MapSettingsScreen extends ConsumerStatefulWidget {
  const MapSettingsScreen({
    super.key,
    required this.pack,
    required this.classCounts,
    required this.edit,
  });

  final LocalPack pack;
  final List<ClassCount> classCounts;
  final EditStore edit;

  @override
  ConsumerState<MapSettingsScreen> createState() => _MapSettingsScreenState();
}

class _MapSettingsScreenState extends ConsumerState<MapSettingsScreen> {
  late bool _snapping;
  late String _basemapId;
  late bool _showNetwork;

  @override
  void initState() {
    super.initState();
    final settings = ref.read(settingsRepositoryProvider);
    _snapping = settings.snappingEnabled;
    _basemapId = settings.basemapId;
    _showNetwork = settings.showNetworkEdges;
  }

  Future<void> _setSnapping(bool value) async {
    setState(() => _snapping = value);
    await ref.read(settingsRepositoryProvider).setSnappingEnabled(value);
  }

  Future<void> _setShowNetwork(bool value) async {
    setState(() => _showNetwork = value);
    await ref.read(settingsRepositoryProvider).setShowNetworkEdges(value);
  }

  Future<void> _setBasemap(String id) async {
    setState(() => _basemapId = id);
    await ref.read(settingsRepositoryProvider).setBasemapId(id);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final totalLoaded =
        widget.classCounts.fold<int>(0, (sum, c) => sum + c.count);
    final snapshot = widget.pack.meta['snapshot_at'] as String?;
    // Live so the row reflects a switch made from the GPS-quality card's picker
    // while this page is open (both drive the same gnssSourceSelectionProvider).
    final gnssSel = ref.watch(gnssSourceSelectionProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('Map settings')),
      body: ListView(
        children: [
          _sectionLabel(theme, 'Basemap'),
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 4),
            child: Text(
              'Draw a map provider under the pack data. Online providers need a '
              'connection; your pack always renders offline on top.',
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.colorScheme.outline),
            ),
          ),
          for (final b in kBasemaps)
            RadioListTile<String>(
              value: b.id,
              groupValue: _basemapId,
              onChanged: (v) {
                if (v != null) _setBasemap(v);
              },
              secondary: Icon(
                b.isNone ? Icons.layers_clear_outlined : Icons.public,
              ),
              title: Text(b.label),
              subtitle: Text(
                b.isNone
                    ? 'Pack tiles only — fully offline'
                    : b.attribution,
              ),
            ),
          ListTile(
            leading: const Icon(Icons.inventory_2_outlined),
            title: Text('Pack ${shortId(widget.pack.id)}'),
            subtitle: Text(snapshot != null
                ? 'Snapshot ${_formatTime(snapshot)}'
                : 'Snapshot time unknown'),
          ),
          const Divider(),

          _sectionLabel(theme, 'Map overlays'),
          SwitchListTile(
            secondary: const Icon(Icons.hub_outlined),
            title: const Text('Network links'),
            subtitle: const Text(
                'Draw a selected feature\'s connections and relationships as '
                'links to its neighbours. Off keeps the map uncluttered — the '
                'links are still listed in the feature\'s detail panel.'),
            value: _showNetwork,
            onChanged: _setShowNetwork,
          ),
          const Divider(),

          _sectionLabel(theme, 'Editing'),
          SwitchListTile(
            secondary: const Icon(Icons.align_horizontal_left),
            title: const Text('Snapping'),
            subtitle: const Text(
                'Snap a placed point, dragged vertex, or inserted midpoint onto '
                'the nearest existing feature vertex.'),
            value: _snapping,
            onChanged: _setSnapping,
          ),
          const Divider(),

          _sectionLabel(theme, 'GPS receiver'),
          ListTile(
            leading: const Icon(Icons.settings_input_antenna),
            title: const Text('GPS source'),
            subtitle: Text(
              gnssSel.useExternal
                  ? (gnssSel.name?.isNotEmpty == true
                      ? gnssSel.name!
                      : gnssSel.address ?? 'External Bluetooth receiver')
                  : 'Phone GPS (built-in)',
            ),
            trailing: const Icon(Icons.chevron_right),
            // Same picker the GPS-quality card opens. Pair receivers in the OS
            // Bluetooth settings first; this lists only bonded devices.
            onTap: () => showGnssSourcePicker(context),
          ),
          const Divider(),

          _sectionLabel(theme, 'Features on the map'),
          // The local-edit tallies stay live via the store's ChangeNotifier, in
          // case an edit lands while this page is open.
          ListenableBuilder(
            listenable: widget.edit,
            builder: (context, _) => Column(
              children: [
                _statTile(
                  icon: Icons.map_outlined,
                  color: theme.colorScheme.primary,
                  label: 'Loaded from pack',
                  value: totalLoaded,
                ),
                _statTile(
                  icon: Icons.add_location_alt_outlined,
                  color: const Color(0xFF34C759),
                  label: 'New (created offline)',
                  value: widget.edit.createdCount,
                ),
                _statTile(
                  icon: Icons.edit_outlined,
                  color: Colors.amber.shade800,
                  label: 'Modified',
                  value: widget.edit.modifiedCount,
                ),
                _statTile(
                  icon: Icons.delete_outline,
                  color: const Color(0xFFFF453A),
                  label: 'Deleted',
                  value: widget.edit.deletedCount,
                ),
                // Only shown when non-zero: ops the server captured but the apply
                // worker rejected (Phase 23 Slice 2). These need the user to
                // re-do the edit; acknowledge them from the pending sheet.
                if (widget.edit.attentionCount > 0)
                  _statTile(
                    icon: Icons.error_outline,
                    color: const Color(0xFFFF453A),
                    label: 'Rejected — needs attention',
                    value: widget.edit.attentionCount,
                  ),
              ],
            ),
          ),
          const Divider(),

          _sectionLabel(theme, 'Loaded by class'),
          if (widget.classCounts.isEmpty)
            const ListTile(title: Text('This pack has no features.'))
          else
            for (final c in widget.classCounts)
              ListTile(
                dense: true,
                title: Text(c.name?.isNotEmpty == true ? c.name! : c.code),
                subtitle: Text(c.code),
                trailing: Text(c.count.toLocaleString()),
              ),
          const SizedBox(height: 24),
        ],
      ),
    );
  }

  Widget _sectionLabel(ThemeData theme, String text) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
        child: Text(
          text.toUpperCase(),
          style: theme.textTheme.labelSmall?.copyWith(
            color: theme.colorScheme.primary,
            letterSpacing: 0.8,
            fontWeight: FontWeight.w600,
          ),
        ),
      );

  Widget _statTile({
    required IconData icon,
    required Color color,
    required String label,
    required int value,
    String? trailingNote,
  }) =>
      ListTile(
        dense: true,
        leading: Icon(icon, color: color),
        title: Text(label),
        trailing: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (trailingNote != null)
              Padding(
                padding: const EdgeInsets.only(right: 8),
                child: Text(
                  trailingNote,
                  style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: Theme.of(context).colorScheme.outline,
                      ),
                ),
              ),
            Text(value.toLocaleString(),
                style: Theme.of(context).textTheme.titleMedium),
          ],
        ),
      );

  String _formatTime(String iso) {
    final dt = DateTime.tryParse(iso);
    if (dt == null) return iso;
    return dt.toLocal().toString().split('.').first;
  }
}

extension _IntFormat on int {
  /// A thousands-separated string (1234 -> "1,234"), no intl dependency.
  String toLocaleString() {
    final s = toString();
    final buf = StringBuffer();
    for (var i = 0; i < s.length; i++) {
      if (i > 0 && (s.length - i) % 3 == 0) buf.write(',');
      buf.write(s[i]);
    }
    return buf.toString();
  }
}
