import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:maplibre_gl/maplibre_gl.dart';

import '../geo/wkb.dart';
import '../pack/pack_database.dart';
import 'mbtiles_tile_server.dart';
import 'pack_style.dart';

/// Renders one downloaded pack on a MapLibre Native map, fully offline, and
/// resolves a tap back to the feature's SQLite row.
///
/// Data flow of a tap:
///   screen point -> queryRenderedFeatures over the pack draw layers
///     -> tile feature's `id` property -> PackDatabase.featureById(id)
///     -> onFeatureTap(record) (the parent shows it + sets [selected]).
///
/// Selection is owned by the parent: [selected] drives the highlight overlay, so
/// the map, the detail panel, and the bottom sheet never disagree about what's
/// picked. The widget only reports taps; it doesn't decide selection itself.
class PackMapView extends StatefulWidget {
  const PackMapView({
    super.key,
    required this.db,
    required this.mbtilesPath,
    required this.onFeatureTap,
    this.bbox,
    this.selected,
  });

  final PackDatabase db;
  final String mbtilesPath;

  /// [minX, minY, maxX, maxY] in lon/lat, used to frame the map on first load.
  final List<double>? bbox;

  /// The currently-picked feature (owned by the parent), highlighted on the map.
  final FeatureRecord? selected;

  final void Function(FeatureRecord feature) onFeatureTap;

  @override
  State<PackMapView> createState() => _PackMapViewState();
}

class _PackMapViewState extends State<PackMapView> {
  static const _selectionSource = 'selection';
  static const _accent = '#ff2d55';

  MapLibreMapController? _controller;
  PackStyle? _style;
  Object? _error;
  bool _styleReady = false;

  // `widget` is in scope for a late field initializer, so the server can read
  // the pack's tiles path straight off the widget.
  late final MbtilesTileServer _tiles = MbtilesTileServer(widget.mbtilesPath);

  @override
  void initState() {
    super.initState();
    _boot();
  }

  Future<void> _boot() async {
    try {
      await _tiles.start();
      final style = PackStyle.build(
        classes: widget.db.classMetas(),
        tileTemplate: _tiles.tileTemplate,
        minZoom: _tiles.minZoom,
        maxZoom: _tiles.maxZoom,
      );
      if (!mounted) return;
      setState(() => _style = style);
    } catch (e) {
      if (!mounted) return;
      setState(() => _error = e);
    }
  }

  @override
  void didUpdateWidget(covariant PackMapView old) {
    super.didUpdateWidget(old);
    if (old.selected?.id != widget.selected?.id) {
      _applyHighlight();
    }
  }

  @override
  void dispose() {
    _tiles.stop();
    super.dispose();
  }

  CameraPosition get _initialCamera {
    final b = widget.bbox;
    if (b != null && b.length == 4) {
      return CameraPosition(
        target: LatLng((b[1] + b[3]) / 2, (b[0] + b[2]) / 2),
        zoom: 12,
      );
    }
    return const CameraPosition(target: LatLng(0, 0), zoom: 2);
  }

  void _onMapCreated(MapLibreMapController controller) {
    _controller = controller;
  }

  Future<void> _onStyleLoaded() async {
    final controller = _controller;
    if (controller == null) return;

    // The highlight overlay: one empty GeoJSON source + a draw layer per
    // geometry kind. Only the layer matching the picked feature's type paints,
    // so one source covers points, lines, and polygons.
    await controller.addGeoJsonSource(_selectionSource, _emptyCollection());
    await controller.addFillLayer(
      _selectionSource,
      'selection-fill',
      FillLayerProperties(fillColor: _accent, fillOpacity: 0.18),
    );
    await controller.addLineLayer(
      _selectionSource,
      'selection-line',
      LineLayerProperties(
        lineColor: _accent,
        lineWidth: 5.0,
        lineOpacity: 0.8,
        lineCap: 'round',
        lineJoin: 'round',
      ),
    );
    await controller.addCircleLayer(
      _selectionSource,
      'selection-circle',
      CircleLayerProperties(
        circleRadius: 9.0,
        circleColor: _accent,
        circleOpacity: 0.35,
        circleStrokeColor: _accent,
        circleStrokeWidth: 2.0,
      ),
    );

    _styleReady = true;
    await _fitToBounds();
    await _applyHighlight();
  }

  Future<void> _fitToBounds() async {
    final controller = _controller;
    final b = widget.bbox;
    if (controller == null || b == null || b.length != 4) return;
    // Pad a hair so a degenerate (single-point) bbox still yields a valid box.
    const pad = 0.0005;
    final sw = LatLng(math.min(b[1], b[3]) - pad, math.min(b[0], b[2]) - pad);
    final ne = LatLng(math.max(b[1], b[3]) + pad, math.max(b[0], b[2]) + pad);
    try {
      await controller.moveCamera(
        CameraUpdate.newLatLngBounds(
          LatLngBounds(southwest: sw, northeast: ne),
          left: 32,
          right: 32,
          top: 32,
          bottom: 32,
        ),
      );
    } catch (_) {
      // A bounds fit can fail before the map has a size; harmless, the initial
      // camera already framed the bbox center.
    }
  }

  Future<void> _onMapClick(math.Point<double> point, LatLng latLng) async {
    final controller = _controller;
    final style = _style;
    if (controller == null || style == null) return;
    List<dynamic> hits;
    try {
      hits = await controller.queryRenderedFeatures(
        point,
        style.queryableLayerIds,
        const [],
      );
    } catch (_) {
      return;
    }
    final id = _firstFeatureId(hits);
    if (id == null) return;
    final record = widget.db.featureById(id);
    if (record == null) return;
    widget.onFeatureTap(record);
  }

  String? _firstFeatureId(List<dynamic> hits) {
    for (final h in hits) {
      final props = (h is Map) ? h['properties'] : null;
      if (props is Map && props['id'] != null) {
        return props['id'].toString();
      }
    }
    return null;
  }

  Future<void> _applyHighlight() async {
    final controller = _controller;
    if (controller == null || !_styleReady) return;
    final selected = widget.selected;
    final geom = selected?.geometry();
    final data = (geom == null)
        ? _emptyCollection()
        : {
            'type': 'FeatureCollection',
            'features': [
              {
                'type': 'Feature',
                'properties': const <String, dynamic>{},
                'geometry': _geoJsonGeometry(geom),
              }
            ],
          };
    try {
      await controller.setGeoJsonSource(_selectionSource, data);
    } catch (_) {}
  }

  Map<String, dynamic> _emptyCollection() => {
        'type': 'FeatureCollection',
        'features': const <dynamic>[],
      };

  /// WkbGeometry already carries GeoJSON-style names + nested coordinates, so a
  /// plain geometry is a direct wrap. GeometryCollection is the one case whose
  /// coordinates are sub-geometries rather than numbers.
  Map<String, dynamic> _geoJsonGeometry(WkbGeometry g) {
    if (g.type == 'GeometryCollection') {
      final members = (g.coordinates as List)
          .whereType<WkbGeometry>()
          .map(_geoJsonGeometry)
          .toList();
      return {'type': 'GeometryCollection', 'geometries': members};
    }
    return {'type': g.type, 'coordinates': g.coordinates};
  }

  @override
  Widget build(BuildContext context) {
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            'Could not open the map tiles:\n$_error',
            textAlign: TextAlign.center,
            style: TextStyle(color: Theme.of(context).colorScheme.error),
          ),
        ),
      );
    }
    final style = _style;
    if (style == null) {
      return const Center(child: CircularProgressIndicator());
    }
    return MapLibreMap(
      styleString: style.json,
      initialCameraPosition: _initialCamera,
      onMapCreated: _onMapCreated,
      onStyleLoadedCallback: _onStyleLoaded,
      onMapClick: _onMapClick,
      trackCameraPosition: false,
      // No basemap/telemetry: the pack is the whole world here.
      myLocationEnabled: false,
      compassEnabled: true,
    );
  }
}
