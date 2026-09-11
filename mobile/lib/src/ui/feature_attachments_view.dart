import 'dart:io';

import 'package:flutter/material.dart';

import '../attach/attachment_capture.dart';
import '../edit/edit_database.dart';
import '../pack/pack_database.dart';

/// The Photos section of a feature's detail panel (Phase 22 Slice 5). Shows a
/// thumbnail grid combining:
///
///   * captured-offline images (from the local_attachments outbox) -- rendered
///     from their on-device file, with a "not synced" tint until uploaded;
///   * packed images (pack schema v6 `attachments` table) -- rendered from the
///     offline thumbnail bytes the pack carries.
///
/// The field tool is images-only. Tapping a thumbnail opens the full-resolution
/// original (a local capture straight from disk; a packed image fetched via a
/// presigned URL, so full-res viewing is online-only). Capture (camera/gallery)
/// and discard-before-upload are surfaced when the callbacks are provided.
class FeatureAttachmentsView extends StatelessWidget {
  const FeatureAttachmentsView({
    super.key,
    this.packAttachments = const [],
    this.localAttachments = const [],
    this.onCapture,
    this.onOpenLocal,
    this.onOpenPack,
    this.onDiscardLocal,
  });

  final List<PackAttachment> packAttachments;
  final List<LocalAttachment> localAttachments;

  /// Capture a new image from the given source. Null hides the Add control (e.g.
  /// the read-only browse page).
  final Future<void> Function(CaptureSource source)? onCapture;

  final void Function(LocalAttachment a)? onOpenLocal;
  final void Function(PackAttachment a)? onOpenPack;
  final void Function(LocalAttachment a)? onDiscardLocal;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    // A local capture and its packed copy (after a fresh pack) share an id --
    // prefer the local one (renders from disk, no network) and hide the dupe.
    final localIds = {for (final a in localAttachments) a.id};
    final packs =
        packAttachments.where((a) => !localIds.contains(a.id)).toList();
    final total = localAttachments.length + packs.length;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 20, 8, 6),
          child: Row(
            children: [
              Expanded(
                child: Text('Photos ($total)',
                    style: theme.textTheme.titleSmall?.copyWith(
                        color: theme.colorScheme.primary,
                        fontWeight: FontWeight.w700)),
              ),
              if (onCapture != null)
                TextButton.icon(
                  onPressed: () => _pickSource(context),
                  icon: const Icon(Icons.add_a_photo_outlined, size: 18),
                  label: const Text('Add'),
                ),
            ],
          ),
        ),
        if (total == 0)
          const Padding(
            padding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Text('No photos.'),
          )
        else
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                for (final a in localAttachments) _localTile(context, a),
                for (final a in packs) _packTile(context, a),
              ],
            ),
          ),
      ],
    );
  }

  Widget _localTile(BuildContext context, LocalAttachment a) {
    final theme = Theme.of(context);
    return _tile(
      context,
      onTap: onOpenLocal == null ? null : () => onOpenLocal!(a),
      onDiscard:
          (onDiscardLocal != null && a.isPending) ? () => onDiscardLocal!(a) : null,
      badge: a.isPending
          ? _Badge(
              color: Colors.amber.shade800,
              icon: Icons.cloud_upload_outlined,
              tooltip: 'Not uploaded',
            )
          : _Badge(
              color: const Color(0xFF34C759),
              icon: Icons.cloud_done_outlined,
              tooltip: 'Uploaded',
            ),
      child: Image.file(
        File(a.localPath),
        width: 96,
        height: 96,
        fit: BoxFit.cover,
        errorBuilder: (context, error, stack) => _placeholder(theme),
      ),
    );
  }

  Widget _packTile(BuildContext context, PackAttachment a) {
    final theme = Theme.of(context);
    final thumb = a.thumb;
    return _tile(
      context,
      onTap: onOpenPack == null ? null : () => onOpenPack!(a),
      child: thumb != null && thumb.isNotEmpty
          ? Image.memory(
              thumb,
              width: 96,
              height: 96,
              fit: BoxFit.cover,
              errorBuilder: (context, error, stack) => _placeholder(theme),
            )
          : _placeholder(theme),
    );
  }

  Widget _tile(
    BuildContext context, {
    required Widget child,
    VoidCallback? onTap,
    VoidCallback? onDiscard,
    Widget? badge,
  }) {
    return SizedBox(
      width: 96,
      height: 96,
      child: ClipRRect(
        borderRadius: BorderRadius.circular(8),
        child: Stack(
          fit: StackFit.expand,
          children: [
            Material(
              color: Theme.of(context).colorScheme.surfaceContainerHighest,
              child: InkWell(onTap: onTap, child: child),
            ),
            if (badge != null) Positioned(top: 2, left: 2, child: badge),
            if (onDiscard != null)
              Positioned(
                top: -6,
                right: -6,
                child: IconButton(
                  iconSize: 18,
                  visualDensity: VisualDensity.compact,
                  icon: const Icon(Icons.cancel, color: Colors.white),
                  style: IconButton.styleFrom(
                    backgroundColor: Colors.black54,
                    padding: const EdgeInsets.all(2),
                  ),
                  tooltip: 'Discard',
                  onPressed: onDiscard,
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget _placeholder(ThemeData theme) => Container(
        width: 96,
        height: 96,
        color: theme.colorScheme.surfaceContainerHighest,
        child: Icon(Icons.image_outlined,
            color: theme.colorScheme.outline, size: 32),
      );

  Future<void> _pickSource(BuildContext context) async {
    final source = await showModalBottomSheet<CaptureSource>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.photo_camera_outlined),
              title: const Text('Take a photo'),
              onTap: () => Navigator.of(context).pop(CaptureSource.camera),
            ),
            ListTile(
              leading: const Icon(Icons.photo_library_outlined),
              title: const Text('Choose from gallery'),
              onTap: () => Navigator.of(context).pop(CaptureSource.gallery),
            ),
          ],
        ),
      ),
    );
    if (source != null) await onCapture!(source);
  }
}

class _Badge extends StatelessWidget {
  const _Badge({required this.color, required this.icon, required this.tooltip});
  final Color color;
  final IconData icon;
  final String tooltip;

  @override
  Widget build(BuildContext context) {
    return Tooltip(
      message: tooltip,
      child: Container(
        padding: const EdgeInsets.all(2),
        decoration: BoxDecoration(
          color: color,
          borderRadius: BorderRadius.circular(4),
        ),
        child: Icon(icon, size: 14, color: Colors.white),
      ),
    );
  }
}

/// A full-screen viewer for one image, pushed when a thumbnail is tapped. [image]
/// is either an Image.file (local capture) or an Image.network (packed full-res
/// via presigned URL). Kept dependency-free (no photo_view / lightbox package):
/// an InteractiveViewer gives pinch-zoom, which is enough for a field photo.
class AttachmentViewerPage extends StatelessWidget {
  const AttachmentViewerPage({
    super.key,
    required this.image,
    this.title,
    this.subtitle,
  });

  final Widget image;
  final String? title;
  final String? subtitle;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        foregroundColor: Colors.white,
        title: title == null
            ? null
            : Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(title!,
                      style: const TextStyle(fontSize: 16),
                      overflow: TextOverflow.ellipsis),
                  if (subtitle != null)
                    Text(subtitle!,
                        style: const TextStyle(
                            fontSize: 12, color: Colors.white70)),
                ],
              ),
      ),
      body: Center(
        child: InteractiveViewer(
          maxScale: 5,
          child: image,
        ),
      ),
    );
  }
}

/// Human byte-size label (shared with the pending/detail rows).
String formatBytes(int bytes) {
  if (bytes < 1024) return '$bytes B';
  const units = ['KB', 'MB', 'GB'];
  double value = bytes / 1024;
  var i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return '${value.toStringAsFixed(value >= 10 ? 0 : 1)} ${units[i]}';
}
