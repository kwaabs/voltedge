import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;
import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';

import '../api/models.dart';
import '../edit/edit_database.dart';
import 'pack_database.dart';

/// A pack that has been downloaded to the device.
class LocalPack {
  const LocalPack({
    required this.id,
    required this.dir,
    required this.dataPath,
    required this.tilesPath,
    required this.meta,
  });

  final String id;
  final String dir;
  final String dataPath; // data.sqlite (edit-truth SQLite pack)
  final String tilesPath; // tiles.mbtiles (display tileset -- used in Slice 2b)

  /// The small local sidecar we write next to the artifacts (bbox, snapshot,
  /// counts) so the pack list renders without opening the SQLite file.
  final Map<String, dynamic> meta;

  DateTime? get downloadedAt => parseServerTime(meta['downloaded_at'] as String?);
  int? get featureCount => (meta['feature_count'] as num?)?.toInt();
}

/// PackStore downloads a pack's two artifacts into the app documents directory
/// and lists what has already been downloaded. Each pack lives under
/// <docs>/packs/<pack_id>/ with data.sqlite, tiles.mbtiles, and pack.json.
class PackStore {
  PackStore({http.Client? httpClient})
      : _http = httpClient ?? http.Client();

  final http.Client _http;

  Future<Directory> _packsRoot() async {
    final docs = await getApplicationDocumentsDirectory();
    final root = Directory(p.join(docs.path, 'packs'));
    await root.create(recursive: true);
    return root;
  }

  /// Download both artifacts for a succeeded pack via its presigned manifest
  /// URLs. onProgress reports 0..1 across the two files (best-effort; falls back
  /// to indeterminate when the server sends no Content-Length).
  Future<LocalPack> download(
    String packId,
    PackManifest manifest, {
    void Function(double fraction)? onProgress,
  }) async {
    final root = await _packsRoot();
    final dir = Directory(p.join(root.path, packId));
    await dir.create(recursive: true);

    final dataPath = p.join(dir.path, 'data.sqlite');
    final tilesPath = p.join(dir.path, 'tiles.mbtiles');

    // Data pack first -- it's what Slice 2a browses; the tileset is for Slice 2b.
    await _downloadTo(manifest.dataUrl, dataPath,
        onProgress: onProgress == null ? null : (f) => onProgress(f * 0.5));
    await _downloadTo(manifest.tilesUrl, tilesPath,
        onProgress: onProgress == null ? null : (f) => onProgress(0.5 + f * 0.5));

    final meta = <String, dynamic>{
      'pack_id': packId,
      'schema_version': manifest.schemaVersion,
      'feature_count': manifest.featureCount,
      'bbox': manifest.bbox,
      'snapshot_at': manifest.snapshotAt?.toUtc().toIso8601String(),
      'downloaded_at': DateTime.now().toUtc().toIso8601String(),
      'include_connectivity': manifest.includeConnectivity,
      'include_relationships': manifest.includeRelationships,
    };
    await File(p.join(dir.path, 'pack.json'))
        .writeAsString(jsonEncode(meta), flush: true);

    final pack = LocalPack(
      id: packId,
      dir: dir.path,
      dataPath: dataPath,
      tilesPath: tilesPath,
      meta: meta,
    );

    // A fresh snapshot makes older packs' already-synced local edits redundant.
    // Prune them so the lingering green/amber "local truth" copy doesn't outlive
    // the pack it was made against. Best-effort: never fail a good download over
    // housekeeping.
    try {
      reconcileSyncedEdits(pack);
    } catch (_) {}

    return pack;
  }

  /// After [freshPack] downloads, sweep every OTHER downloaded pack and drop the
  /// local overlays/creates whose feature the fresh snapshot now contains -- the
  /// stale green (created) / amber (edited) copy that otherwise lingers until the
  /// old pack is deleted (docs/phase-21-field-tool.md, Slice 4). It's cross-pack
  /// by necessity: each pack has its own edits.sqlite, and a re-download is a NEW
  /// pack id with a fresh empty one, so the edit to clean up always sits in the
  /// PRIOR pack's dir. Membership-checked (not blind), so an unrelated-region pack
  /// keeps a synced edit the fresh pack doesn't cover, and PENDING (unpushed) work
  /// is never eligible (see EditDatabase.syncedFeatureIds).
  void reconcileSyncedEdits(LocalPack freshPack) {
    final packsRoot = Directory(p.dirname(freshPack.dir));
    if (!packsRoot.existsSync()) return;
    final fresh = PackDatabase.open(freshPack.dataPath);
    try {
      for (final entry in packsRoot.listSync()) {
        if (entry is! Directory) continue;
        if (p.basename(entry.path) == freshPack.id) continue;
        final editsPath = p.join(entry.path, 'edits.sqlite');
        if (!File(editsPath).existsSync()) continue;
        final edits = EditDatabase.open(editsPath);
        try {
          final synced = edits.syncedFeatureIds();
          if (synced.isNotEmpty) {
            final present = fresh.existingFeatureIds(synced);
            edits.dropSynced(present);
          }
          _reconcileSyncedAttachments(fresh, edits);
          _reconcileSyncedConnections(fresh, edits);
          _reconcileSyncedRelationships(fresh, edits);
        } finally {
          edits.close();
        }
      }
    } finally {
      fresh.close();
    }
  }

  /// Drop an older pack's synced captured photos (local_attachments) that the
  /// fresh pack now carries as packed thumbnails (Phase 22 Slice 6). The upload
  /// kept the local file rendering as on-device truth until a fresh snapshot baked
  /// the server thumbnail in; once it has, the local copy is pure redundancy, so
  /// the row is dropped AND its on-device file deleted (unlike a feature overlay,
  /// which has no file). Membership-checked against the pack's `attachments` table
  /// so a photo the fresh pack doesn't cover (e.g. an unrelated-region pack) is
  /// kept; pending (un-uploaded) captures are never eligible (syncedAttachments).
  void _reconcileSyncedAttachments(PackDatabase fresh, EditDatabase edits) {
    final synced = edits.syncedAttachments();
    if (synced.isEmpty) return;
    final present =
        fresh.existingAttachmentIds([for (final a in synced) a.id]);
    if (present.isEmpty) return;
    final drop = [for (final a in synced) if (present.contains(a.id)) a];
    for (final a in drop) {
      try {
        final f = File(a.localPath);
        if (f.existsSync()) f.deleteSync();
      } catch (_) {
        // Best-effort: a stray file left behind is harmless housekeeping debt.
      }
    }
    edits.dropSyncedAttachments([for (final a in drop) a.id]);
  }

  /// Drop an older pack's synced offline connections (Phase 21 Slice 13b) that the
  /// fresh pack now carries in its connectivity graph. Symmetric to the feature and
  /// attachment reconcile: membership-checked (the edge must be present in the fresh
  /// pack, either direction) so a connection the new snapshot predates -- synced but
  /// not yet baked in -- is kept rendering its local edge until a later pack covers
  /// it; pending (un-pushed) connections are never eligible (syncedConnectionEdges).
  void _reconcileSyncedConnections(PackDatabase fresh, EditDatabase edits) {
    final synced = edits.syncedConnectionEdges();
    if (synced.isEmpty) return;
    final drop = [
      for (final c in synced)
        if (fresh.hasConnection(c.fromId, c.toId)) c.id,
    ];
    if (drop.isNotEmpty) edits.dropConnections(drop);
  }

  /// Drop an older pack's synced offline relationships (Phase 21 Slice 14) that the
  /// fresh pack now carries in its relationships table. The relationship sibling of
  /// [_reconcileSyncedConnections]: membership-checked (the edge must be present in
  /// the fresh pack, either direction) so a relationship the new snapshot predates --
  /// synced but not yet baked in -- keeps rendering its local edge until a later pack
  /// covers it; pending (un-pushed) relationships are never eligible
  /// (syncedRelationshipEdges).
  void _reconcileSyncedRelationships(PackDatabase fresh, EditDatabase edits) {
    final synced = edits.syncedRelationshipEdges();
    if (synced.isEmpty) return;
    final drop = [
      for (final r in synced)
        if (fresh.hasRelationship(r.originId, r.destinationId)) r.id,
    ];
    if (drop.isNotEmpty) edits.dropRelationships(drop);
  }

  /// Every pack already on disk, newest download first.
  Future<List<LocalPack>> listDownloaded() async {
    final root = await _packsRoot();
    final packs = <LocalPack>[];
    await for (final entry in root.list()) {
      if (entry is! Directory) continue;
      final dataPath = p.join(entry.path, 'data.sqlite');
      if (!File(dataPath).existsSync()) continue;
      Map<String, dynamic> meta = const {};
      final metaFile = File(p.join(entry.path, 'pack.json'));
      if (metaFile.existsSync()) {
        final decoded = jsonDecode(await metaFile.readAsString());
        if (decoded is Map<String, dynamic>) meta = decoded;
      }
      packs.add(LocalPack(
        id: p.basename(entry.path),
        dir: entry.path,
        dataPath: dataPath,
        tilesPath: p.join(entry.path, 'tiles.mbtiles'),
        meta: meta,
      ));
    }
    packs.sort((a, b) {
      final da = a.downloadedAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      final db = b.downloadedAt ?? DateTime.fromMillisecondsSinceEpoch(0);
      return db.compareTo(da);
    });
    return packs;
  }

  Future<void> delete(String packId) async {
    final root = await _packsRoot();
    final dir = Directory(p.join(root.path, packId));
    if (dir.existsSync()) await dir.delete(recursive: true);
  }

  /// Stream a URL to a file. Downloads to a .part then renames, so an
  /// interrupted download never leaves a half file that looks complete.
  Future<void> _downloadTo(
    String url,
    String destPath, {
    void Function(double fraction)? onProgress,
  }) async {
    final req = http.Request('GET', Uri.parse(url));
    final res = await _http.send(req);
    if (res.statusCode != 200) {
      throw HttpException(
          'download failed (HTTP ${res.statusCode})', uri: Uri.parse(url));
    }
    final total = res.contentLength ?? 0;
    var received = 0;

    final partPath = '$destPath.part';
    final sink = File(partPath).openWrite();
    try {
      await for (final chunk in res.stream) {
        sink.add(chunk);
        received += chunk.length;
        if (onProgress != null && total > 0) {
          onProgress(received / total);
        }
      }
      await sink.flush();
    } finally {
      await sink.close();
    }
    await File(partPath).rename(destPath);
    if (onProgress != null) onProgress(1.0);
  }
}
