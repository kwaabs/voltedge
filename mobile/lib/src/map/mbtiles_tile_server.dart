import 'dart:io';
import 'dart:typed_data';

import 'package:sqlite3/sqlite3.dart';

/// Serves the pack's `tiles.mbtiles` to MapLibre Native over a loopback HTTP
/// socket, so the map renders fully offline with no network and no import step.
///
/// Why a local server instead of maplibre_gl's own offline path: maplibre_gl's
/// offline story is "download a region into an ambient cache", but we already
/// HAVE the tiles (the pack shipped them). MapLibre Native happily fetches a
/// normal `{z}/{x}/{y}` XYZ source over HTTP, so the cheapest, most portable
/// bridge is to read the MBTiles SQLite directly and hand tiles back on
/// 127.0.0.1. This also keeps the render path independent of the map plugin.
///
/// MBTiles quirks handled here:
///   * tile_row is TMS (y flipped): xyz_y -> tms_row = (2^z - 1) - y.
///   * tippecanoe writes gzip-compressed PBF; we forward Content-Encoding: gzip
///     when the bytes start with the gzip magic so MapLibre inflates them.
///
/// The server binds an ephemeral port on the IPv4 loopback and runs on the app's
/// event loop, so every tile read is serialized -- safe for the single
/// read-only sqlite3 handle.
class MbtilesTileServer {
  MbtilesTileServer(this.mbtilesPath);

  final String mbtilesPath;

  Database? _db;
  HttpServer? _server;
  int _minZoom = 0;
  int _maxZoom = 22;

  int get minZoom => _minZoom;
  int get maxZoom => _maxZoom;

  /// The base URL MapLibre points at, e.g. `http://127.0.0.1:54123`. The tile
  /// template appended by the style builder is `/{z}/{x}/{y}.pbf`.
  String get baseUrl {
    final s = _server;
    if (s == null) throw StateError('tile server not started');
    return 'http://127.0.0.1:${s.port}';
  }

  /// The XYZ tile template MapLibre's vector source consumes.
  String get tileTemplate => '$baseUrl/{z}/{x}/{y}.pbf';

  Future<void> start() async {
    final db = sqlite3.open(mbtilesPath, mode: OpenMode.readOnly);
    _db = db;
    _readZoomRange(db);

    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    _server = server;
    server.listen(_handle, onError: (_) {});
  }

  Future<void> stop() async {
    await _server?.close(force: true);
    _server = null;
    _db?.dispose();
    _db = null;
  }

  void _readZoomRange(Database db) {
    for (final row in db.select('SELECT name, value FROM metadata')) {
      final name = row['name'] as String?;
      final value = row['value'] as String?;
      if (value == null) continue;
      if (name == 'minzoom') _minZoom = int.tryParse(value) ?? _minZoom;
      if (name == 'maxzoom') _maxZoom = int.tryParse(value) ?? _maxZoom;
    }
  }

  Future<void> _handle(HttpRequest req) async {
    final res = req.response;
    try {
      final tile = _parsePath(req.uri.pathSegments);
      if (tile == null) {
        res.statusCode = HttpStatus.notFound;
        await res.close();
        return;
      }
      final bytes = _readTile(tile.z, tile.x, tile.y);
      if (bytes == null) {
        // No tile at this address -- an empty area. MapLibre treats 404/204 as
        // "nothing to draw here".
        res.statusCode = HttpStatus.noContent;
        await res.close();
        return;
      }
      res.statusCode = HttpStatus.ok;
      res.headers.set(HttpHeaders.contentTypeHeader, 'application/x-protobuf');
      res.headers.set(HttpHeaders.cacheControlHeader, 'no-cache');
      if (bytes.length >= 2 && bytes[0] == 0x1f && bytes[1] == 0x8b) {
        res.headers.set(HttpHeaders.contentEncodingHeader, 'gzip');
      }
      res.add(bytes);
      await res.close();
    } catch (_) {
      try {
        res.statusCode = HttpStatus.internalServerError;
        await res.close();
      } catch (_) {}
    }
  }

  /// `/{z}/{x}/{y}.pbf` -> (z, x, y). Tolerates a trailing extension or none.
  _TileXYZ? _parsePath(List<String> segs) {
    if (segs.length != 3) return null;
    final z = int.tryParse(segs[0]);
    final x = int.tryParse(segs[1]);
    var yStr = segs[2];
    final dot = yStr.indexOf('.');
    if (dot >= 0) yStr = yStr.substring(0, dot);
    final y = int.tryParse(yStr);
    if (z == null || x == null || y == null) return null;
    return _TileXYZ(z, x, y);
  }

  Uint8List? _readTile(int z, int x, int y) {
    final db = _db;
    if (db == null) return null;
    // XYZ -> TMS row flip.
    final tmsRow = ((1 << z) - 1) - y;
    final rows = db.select(
      'SELECT tile_data FROM tiles '
      'WHERE zoom_level = ? AND tile_column = ? AND tile_row = ? LIMIT 1',
      [z, x, tmsRow],
    );
    if (rows.isEmpty) return null;
    final data = rows.first['tile_data'];
    return data is Uint8List ? data : null;
  }
}

class _TileXYZ {
  const _TileXYZ(this.z, this.x, this.y);
  final int z;
  final int x;
  final int y;
}
