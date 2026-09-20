import 'dart:convert';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:maplibre_gl/maplibre_gl.dart';

import '../map/basemaps.dart';

/// A full-screen map where the worker DRAWS a pack's bounding box instead of
/// typing four numbers. Tap two opposite corners and a rectangle is rubber-banded
/// between them; "Use this box" pops the resulting [minLon, minLat, maxLon, maxLat]
/// back to the New pack form, which drops it into its numeric fields (so the two
/// ways of setting a bbox stay in sync -- typing still works, this just fills them
/// by hand-on-map instead).
///
/// This is an ONLINE map (an OSM raster basemap): choosing where to build a pack
/// happens against the server, so a live basemap is available and appropriate --
/// unlike [PackMapView], which is the offline, pack-only viewer and needs a
/// downloaded pack's tiles + SQLite. Nothing here touches a pack; it only returns
/// coordinates.
class BboxPickerPage extends StatefulWidget {
  const BboxPickerPage({super.key, this.initial});

  /// The bbox the form currently holds ([minLon, minLat, maxLon, maxLat]), used
  /// to frame the map and pre-draw the box on open. Null (or the whole-world
  /// default) just opens zoomed out with nothing drawn.
  final List<double>? initial;

  @override
  State<BboxPickerPage> createState() => _BboxPickerPageState();
}

class _BboxPickerPageState extends State<BboxPickerPage> {
  static const _boxSource = 'bbox';
  static const _boxFill = 'bbox-fill';
  static const _boxLine = 'bbox-line';
  static const _boxCircle = 'bbox-corners';
  static const _color = '#0a84ff'; // blue, matching the create-draft overlay

  MapLibreMapController? _controller;
  bool _styleReady = false;

  /// The two opposite corners the user has tapped. Both null = nothing drawn;
  /// only [_a] = one corner placed (waiting for the second); both = a box.
  LatLng? _a;
  LatLng? _b;

  bool get _hasBox => _a != null && _b != null;

  /// A whole-world (or absent) initial bbox isn't worth framing or drawing.
  bool _isRealBox(List<double>? b) =>
      b != null &&
      b.length == 4 &&
      !(b[0] <= -180 && b[1] <= -90 && b[2] >= 180 && b[3] >= 90) &&
      b[0] < b[2] &&
      b[1] < b[3];

  /// The OSM raster style, built inline so no pack/style machinery is needed.
  String get _styleJson {
    final osm = basemapById('osm');
    return jsonEncode({
      'version': 8,
      'sources': {
        'basemap': {
          'type': 'raster',
          'tiles': [osm.tileUrl],
          'tileSize': 256,
          'maxzoom': osm.maxZoom,
          'attribution': osm.attribution,
        },
      },
      'layers': [
        {'id': 'basemap', 'type': 'raster', 'source': 'basemap'},
      ],
    });
  }

  CameraPosition get _initialCamera {
    final b = widget.initial;
    if (_isRealBox(b)) {
      return CameraPosition(
        target: LatLng((b![1] + b[3]) / 2, (b[0] + b[2]) / 2),
        zoom: 10,
      );
    }
    return const CameraPosition(target: LatLng(0, 0), zoom: 1);
  }

  void _onMapCreated(MapLibreMapController controller) {
    _controller = controller;
  }

  Future<void> _onStyleLoaded() async {
    final controller = _controller;
    if (controller == null) return;
    await controller.addGeoJsonSource(_boxSource, _empty());
    await controller.addFillLayer(
      _boxSource,
      _boxFill,
      FillLayerProperties(fillColor: _color, fillOpacity: 0.2),
      filter: [
        'match',
        ['geometry-type'],
        ['Polygon'],
        true,
        false,
      ],
    );
    await controller.addLineLayer(
      _boxSource,
      _boxLine,
      LineLayerProperties(
        lineColor: _color,
        lineWidth: 3.0,
        lineOpacity: 0.95,
        lineCap: 'round',
        lineJoin: 'round',
      ),
      filter: [
        'match',
        ['geometry-type'],
        ['LineString'],
        true,
        false,
      ],
    );
    await controller.addCircleLayer(
      _boxSource,
      _boxCircle,
      CircleLayerProperties(
        circleRadius: 7.0,
        circleColor: _color,
        circleOpacity: 0.9,
        circleStrokeColor: '#ffffff',
        circleStrokeWidth: 2.5,
      ),
      filter: [
        'match',
        ['geometry-type'],
        ['Point'],
        true,
        false,
      ],
    );
    _styleReady = true;

    // Pre-load the form's current box, if it's a real one, and frame to it.
    final b = widget.initial;
    if (_isRealBox(b)) {
      _a = LatLng(b![1], b[0]);
      _b = LatLng(b[3], b[2]);
      await _redraw();
      await _frame(_a!, _b!);
    }
  }

  /// A tap places a corner. The cycle is: first tap sets corner A, second sets
  /// corner B (completing the box); a tap once a box exists starts a fresh one.
  void _onMapClick(math.Point<double> point, LatLng latLng) {
    setState(() {
      if (!_hasBox && _a == null) {
        _a = latLng;
      } else if (!_hasBox && _a != null) {
        _b = latLng;
      } else {
        // A completed box exists -- begin again from this corner.
        _a = latLng;
        _b = null;
      }
    });
    _redraw();
  }

  /// Snap the map's current viewport to the box -- a fast way to pick "roughly
  /// what's on screen" without hunting for two exact corners.
  Future<void> _useCurrentView() async {
    final controller = _controller;
    if (controller == null) return;
    try {
      final region = await controller.getVisibleRegion();
      setState(() {
        _a = region.southwest;
        _b = region.northeast;
      });
      await _redraw();
    } catch (_) {
      // Best-effort: if the region read fails, leave the current box as-is.
    }
  }

  void _clear() {
    setState(() {
      _a = null;
      _b = null;
    });
    _redraw();
  }

  List<double>? _bbox() {
    if (!_hasBox) return null;
    final a = _a!, b = _b!;
    final minLon = math.min(a.longitude, b.longitude);
    final minLat = math.min(a.latitude, b.latitude);
    final maxLon = math.max(a.longitude, b.longitude);
    final maxLat = math.max(a.latitude, b.latitude);
    // A degenerate (zero-area) box isn't a usable scope.
    if (minLon == maxLon || minLat == maxLat) return null;
    return [minLon, minLat, maxLon, maxLat];
  }

  Future<void> _redraw() async {
    final controller = _controller;
    if (controller == null || !_styleReady) return;
    final feats = <Map<String, dynamic>>[];
    final box = _bbox();
    if (box != null) {
      final ring = [
        [box[0], box[1]],
        [box[2], box[1]],
        [box[2], box[3]],
        [box[0], box[3]],
        [box[0], box[1]],
      ];
      feats.add({
        'type': 'Feature',
        'properties': const <String, dynamic>{},
        'geometry': {
          'type': 'Polygon',
          'coordinates': [ring],
        },
      });
      feats.add({
        'type': 'Feature',
        'properties': const <String, dynamic>{},
        'geometry': {'type': 'LineString', 'coordinates': ring},
      });
    }
    // Draw a marker at each placed corner (so a single first tap is visible too).
    for (final c in [_a, _b]) {
      if (c == null) continue;
      feats.add({
        'type': 'Feature',
        'properties': const <String, dynamic>{},
        'geometry': {
          'type': 'Point',
          'coordinates': [c.longitude, c.latitude],
        },
      });
    }
    try {
      await controller
          .setGeoJsonSource(_boxSource, {'type': 'FeatureCollection', 'features': feats});
    } catch (_) {}
  }

  Future<void> _frame(LatLng a, LatLng b) async {
    final controller = _controller;
    if (controller == null) return;
    final bounds = LatLngBounds(
      southwest: LatLng(math.min(a.latitude, b.latitude), math.min(a.longitude, b.longitude)),
      northeast: LatLng(math.max(a.latitude, b.latitude), math.max(a.longitude, b.longitude)),
    );
    try {
      await controller.moveCamera(
        CameraUpdate.newLatLngBounds(bounds, left: 48, top: 96, right: 48, bottom: 120),
      );
    } catch (_) {}
  }

  Map<String, dynamic> _empty() =>
      {'type': 'FeatureCollection', 'features': const <dynamic>[]};

  @override
  Widget build(BuildContext context) {
    final box = _bbox();
    return Scaffold(
      appBar: AppBar(
        title: const Text('Draw bounding box'),
        actions: [
          if (_a != null)
            TextButton(
              onPressed: _clear,
              child: const Text('Clear'),
            ),
        ],
      ),
      body: Stack(
        children: [
          Positioned.fill(
            child: MapLibreMap(
              styleString: _styleJson,
              initialCameraPosition: _initialCamera,
              onMapCreated: _onMapCreated,
              onStyleLoadedCallback: _onStyleLoaded,
              onMapClick: _onMapClick,
            ),
          ),
          Positioned(
            top: 8,
            left: 8,
            right: 8,
            child: SafeArea(
              child: _Hint(
                text: box != null
                    ? 'Box set. Adjust by tapping a new pair of corners, or use it below.'
                    : _a != null
                        ? 'Tap the opposite corner to finish the box.'
                        : 'Tap two opposite corners to draw the box.',
              ),
            ),
          ),
          Positioned(
            left: 8,
            right: 8,
            bottom: 8,
            child: SafeArea(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (box != null)
                    Card(
                      margin: const EdgeInsets.only(bottom: 8),
                      child: Padding(
                        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                        child: Text(
                          'min ${_fmt(box[0])}, ${_fmt(box[1])}  ·  '
                          'max ${_fmt(box[2])}, ${_fmt(box[3])}',
                          style: Theme.of(context).textTheme.bodySmall,
                        ),
                      ),
                    ),
                  Row(
                    children: [
                      Expanded(
                        child: OutlinedButton.icon(
                          onPressed: _useCurrentView,
                          icon: const Icon(Icons.crop_free),
                          label: const Text('Use current view'),
                        ),
                      ),
                      const SizedBox(width: 12),
                      Expanded(
                        child: FilledButton.icon(
                          onPressed:
                              box == null ? null : () => Navigator.of(context).pop(box),
                          icon: const Icon(Icons.check),
                          label: const Text('Use this box'),
                        ),
                      ),
                    ],
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }

  static String _fmt(double v) => v.toStringAsFixed(5);
}

/// A small floating banner over the map top, explaining the current tap step.
class _Hint extends StatelessWidget {
  const _Hint({required this.text});
  final String text;

  @override
  Widget build(BuildContext context) {
    return Material(
      elevation: 3,
      borderRadius: BorderRadius.circular(10),
      color: Theme.of(context).colorScheme.surface,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        child: Row(
          children: [
            const Icon(Icons.touch_app, size: 18),
            const SizedBox(width: 8),
            Expanded(
              child: Text(text, style: Theme.of(context).textTheme.bodySmall),
            ),
          ],
        ),
      ),
    );
  }
}
