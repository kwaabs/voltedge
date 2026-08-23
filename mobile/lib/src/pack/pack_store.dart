import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;
import 'package:path/path.dart' as p;
import 'package:path_provider/path_provider.dart';

import '../api/models.dart';

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

    return LocalPack(
      id: packId,
      dir: dir.path,
      dataPath: dataPath,
      tilesPath: tilesPath,
      meta: meta,
    );
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
