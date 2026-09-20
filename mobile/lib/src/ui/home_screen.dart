import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/fieldsync_client.dart';
import '../api/models.dart';
import '../pack/pack_store.dart';
import '../providers.dart';
import 'format.dart';
import 'new_pack_screen.dart';
import 'pack_detail_screen.dart';
import 'pack_map_page.dart';
import 'settings_screen.dart';

/// The app's front door: what's on the device, and what's available to build /
/// download from the server.
class HomeScreen extends ConsumerWidget {
  const HomeScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final settings = ref.watch(settingsRepositoryProvider);
    final downloaded = ref.watch(downloadedPacksProvider);
    final remote = ref.watch(remotePacksProvider);

    return Scaffold(
      appBar: AppBar(
        title: const Text('VoltEdge Field'),
        actions: [
          IconButton(
            tooltip: 'Connection',
            icon: const Icon(Icons.settings),
            onPressed: () => Navigator.of(context).push(
              MaterialPageRoute(builder: (_) => const SettingsScreen()),
            ),
          ),
        ],
      ),
      floatingActionButton: settings.isConfigured
          ? FloatingActionButton.extended(
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute(builder: (_) => const NewPackScreen()),
              ),
              icon: const Icon(Icons.add),
              label: const Text('New pack'),
            )
          : null,
      body: RefreshIndicator(
        onRefresh: () async {
          ref.invalidate(downloadedPacksProvider);
          ref.invalidate(remotePacksProvider);
          await Future.wait([
            ref.read(downloadedPacksProvider.future),
            if (settings.isConfigured) ref.read(remotePacksProvider.future),
          ]);
        },
        child: ListView(
          padding: const EdgeInsets.only(bottom: 96),
          children: [
            if (!settings.isConfigured) const _NotConfiguredBanner(),
            const _SectionHeader('On this device'),
            downloaded.when(
              loading: () => const _Loading(),
              error: (e, _) => _ErrorRow('$e'),
              data: (packs) => packs.isEmpty
                  ? const _EmptyRow('No packs downloaded yet.')
                  : Column(
                      children: [
                        for (final pack in packs) _DownloadedTile(pack: pack),
                      ],
                    ),
            ),
            const _SectionHeader('Available from server'),
            if (!settings.isConfigured)
              const _EmptyRow('Set a base URL and sign in first.')
            else
              remote.when(
                loading: () => const _Loading(),
                error: (e, _) => _ErrorRow('$e'),
                data: (packs) => packs.isEmpty
                    ? const _EmptyRow('No builds yet. Tap “New pack”.')
                    : Column(
                        children: [
                          for (final pack in packs) _RemoteTile(pack: pack),
                        ],
                      ),
              ),
          ],
        ),
      ),
    );
  }
}

class _DownloadedTile extends ConsumerWidget {
  const _DownloadedTile({required this.pack});
  final LocalPack pack;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Card(
      child: ListTile(
      leading: const CircleAvatar(
        backgroundColor: Color(0x2234C759),
        foregroundColor: Color(0xFF34C759),
        child: Icon(Icons.offline_pin),
      ),
      title: Text('Pack ${shortId(pack.id)}',
          style: const TextStyle(fontWeight: FontWeight.w600)),
      subtitle: Text(
        '${pack.featureCount ?? '?'} features · '
        'downloaded ${shortTime(pack.downloadedAt)}',
      ),
      trailing: PopupMenuButton<String>(
        onSelected: (v) async {
          if (v == 'map') {
            Navigator.of(context).push(
              MaterialPageRoute(builder: (_) => PackMapPage(pack: pack)),
            );
          } else if (v == 'delete') {
            await ref.read(packStoreProvider).delete(pack.id);
            ref.invalidate(downloadedPacksProvider);
          }
        },
        itemBuilder: (_) => const [
          PopupMenuItem(value: 'map', child: Text('Open map')),
          PopupMenuItem(value: 'delete', child: Text('Delete')),
        ],
      ),
      onTap: () => Navigator.of(context).push(
        MaterialPageRoute(builder: (_) => PackDetailScreen(pack: pack)),
      ),
      ),
    );
  }
}

class _RemoteTile extends ConsumerStatefulWidget {
  const _RemoteTile({required this.pack});
  final PackSummary pack;

  @override
  ConsumerState<_RemoteTile> createState() => _RemoteTileState();
}

class _RemoteTileState extends ConsumerState<_RemoteTile> {
  double? _progress; // non-null while downloading

  Future<void> _download() async {
    setState(() => _progress = 0);
    try {
      // Re-fetch to mint fresh presigned URLs right before downloading.
      final fresh = await ref.read(fieldsyncClientProvider).getPack(widget.pack.id);
      final manifest = fresh.manifest;
      if (manifest == null) {
        throw FieldsyncException(0, 'pack has no manifest (not ready)');
      }
      final local = await ref.read(packStoreProvider).download(
            widget.pack.id,
            manifest,
            onProgress: (f) => setState(() => _progress = f),
          );
      ref.invalidate(downloadedPacksProvider);
      if (!mounted) return;
      Navigator.of(context).push(
        MaterialPageRoute(builder: (_) => PackDetailScreen(pack: local)),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Download failed: $e')));
    } finally {
      if (mounted) setState(() => _progress = null);
    }
  }

  Future<void> _cancel() async {
    try {
      await ref.read(fieldsyncClientProvider).cancelPack(widget.pack.id);
      ref.invalidate(remotePacksProvider);
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Cancel failed: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    final pack = widget.pack;
    return Card(
      child: ListTile(
        leading: _statusIcon(pack.status),
        title: Text('Pack ${shortId(pack.id)}',
            style: const TextStyle(fontWeight: FontWeight.w600)),
        subtitle: Text(_subtitle(pack)),
        trailing: _trailing(pack),
      ),
    );
  }

  Widget _statusIcon(String status) {
    switch (status) {
      case 'succeeded':
        return const Icon(Icons.cloud_done, color: Colors.blue);
      case 'failed':
        return const Icon(Icons.error, color: Colors.red);
      case 'cancelled':
        return const Icon(Icons.cancel, color: Colors.grey);
      default:
        return const SizedBox(
          width: 24,
          height: 24,
          child: Center(
            child: SizedBox(
                width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2)),
          ),
        );
    }
  }

  String _subtitle(PackSummary pack) {
    if (pack.status == 'failed' && pack.error != null) return pack.error!;
    final parts = <String>[pack.status];
    if (pack.featureCount != null) parts.add('${pack.featureCount} features');
    if (pack.dataBytes != null) {
      parts.add('${humanBytes(pack.tilesBytes)} + ${humanBytes(pack.dataBytes)}');
    }
    return parts.join(' · ');
  }

  Widget? _trailing(PackSummary pack) {
    if (_progress != null) {
      return SizedBox(
        width: 40,
        height: 40,
        child: CircularProgressIndicator(
            value: _progress == 0 ? null : _progress),
      );
    }
    if (pack.isSucceeded) {
      return IconButton(
        icon: const Icon(Icons.download),
        tooltip: 'Download',
        onPressed: _download,
      );
    }
    if (pack.isCancellable) {
      return IconButton(
        icon: const Icon(Icons.stop_circle_outlined),
        tooltip: 'Cancel',
        onPressed: _cancel,
      );
    }
    return null;
  }
}

class _NotConfiguredBanner extends StatelessWidget {
  const _NotConfiguredBanner();
  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      margin: const EdgeInsets.fromLTRB(16, 12, 16, 0),
      decoration: BoxDecoration(
        color: scheme.secondaryContainer,
        borderRadius: BorderRadius.circular(16),
      ),
      padding: const EdgeInsets.all(16),
      child: Row(
        children: [
          Icon(Icons.info_outline, color: scheme.onSecondaryContainer),
          const SizedBox(width: 12),
          Expanded(
            child: Text(
              'Set the fieldsync base URL and sign in to fetch packs.',
              style: Theme.of(context).textTheme.bodyMedium,
            ),
          ),
          TextButton(
            onPressed: () => Navigator.of(context).push(
              MaterialPageRoute(builder: (_) => const SettingsScreen()),
            ),
            child: const Text('Sign in'),
          ),
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader(this.text);
  final String text;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 8),
      child: Text(text,
          style: Theme.of(context).textTheme.titleSmall?.copyWith(
              color: Theme.of(context).colorScheme.primary,
              fontWeight: FontWeight.w700)),
    );
  }
}

class _Loading extends StatelessWidget {
  const _Loading();
  @override
  Widget build(BuildContext context) => const Padding(
        padding: EdgeInsets.all(24),
        child: Center(child: CircularProgressIndicator()),
      );
}

class _EmptyRow extends StatelessWidget {
  const _EmptyRow(this.text);
  final String text;
  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: Text(text, style: Theme.of(context).textTheme.bodyMedium),
      );
}

class _ErrorRow extends StatelessWidget {
  const _ErrorRow(this.text);
  final String text;
  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: Text(text,
            style: TextStyle(color: Theme.of(context).colorScheme.error)),
      );
}
