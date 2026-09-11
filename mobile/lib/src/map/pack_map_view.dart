import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:maplibre_gl/maplibre_gl.dart';

import '../geo/wkb.dart';
import '../pack/pack_database.dart';
import 'basemaps.dart';
import 'icon_atlas.dart';
import 'mbtiles_tile_server.dart';
import 'pack_style.dart';

/// One connection to draw as a local edge overlay (Phase 21 Slice 13b): a straight
/// green link between the two endpoints' coordinates. Purely cosmetic -- the real
/// topology lives server-side once synced -- so it's just the two resolved points.
class ConnectionEdge {
  const ConnectionEdge({required this.from, required this.to});
  final LatLng from;
  final LatLng to;
}

/// One relationship to draw as a local edge overlay (Phase 21 Slice 14): a straight
/// dashed purple link between the two endpoints' coordinates. The relationship
/// sibling of [ConnectionEdge] -- purely cosmetic, distinct colour/dash so a
/// relationship reads differently from a (green) connection on the map.
class RelationshipEdge {
  const RelationshipEdge({required this.from, required this.to});
  final LatLng from;
  final LatLng to;
}

/// Renders one downloaded pack on a MapLibre Native map, fully offline, and
/// resolves a tap back to the feature's SQLite row.
///
/// Data flow of a tap:
///   screen point -> queryRenderedFeaturesInRect (a tolerance box) over the pack
///     draw layers -> each hit's `id` property -> PackDatabase.featureById(id)
///     -> onFeaturesTapped(records): one hit selects, a stack opens a peer picker.
///
/// Selection is owned by the parent: [selected] drives the highlight overlay, so
/// the map, the detail panel, and the bottom sheet never disagree about what's
/// picked. The widget only reports taps; it doesn't decide selection itself.
class PackMapView extends StatefulWidget {
  const PackMapView({
    super.key,
    required this.db,
    required this.mbtilesPath,
    required this.onFeaturesTapped,
    this.bbox,
    this.selected,
    this.dirty = const [],
    this.created = const [],
    this.movedIds = const [],
    this.hiddenIds = const [],
    this.connections = const [],
    this.connecting = false,
    this.onConnectTarget,
    this.relationships = const [],
    this.relating = false,
    this.onRelateTarget,
    this.drawing = false,
    this.draftPoint,
    this.draftLine = const [],
    this.draftIsPolygon = false,
    this.onPointPlaced,
    this.editing = false,
    this.editVertices = const [],
    this.editIsLine = false,
    this.editIsPolygon = false,
    this.selectedVertex,
    this.onVertexDragged,
    this.onVertexTapped,
    this.onMidpointTapped,
    this.snapEnabled = true,
    this.onLocate,
    this.centerOn,
    this.locating = false,
    this.showUserLocation = false,
    this.basemapId = kDefaultBasemapId,
  });

  final PackDatabase db;
  final String mbtilesPath;

  /// Features CREATED offline (create slice). They aren't baked into the pack's
  /// vector tiles, so the map paints them from these records as a GREEN overlay
  /// and makes that overlay queryable, so a created feature is tappable just like
  /// a packed one. Each carries a GeoJSON geometryOverride.
  final List<FeatureRecord> created;

  /// Connections established offline (Phase 21 Slice 13b), drawn as green links
  /// between endpoint coordinates. A cosmetic overlay of the local topology edits
  /// that haven't been baked into a fresh pack yet.
  final List<ConnectionEdge> connections;

  /// When true the map is in "pick a connection target" mode: the next tap on a
  /// feature reports the hit records via [onConnectTarget] instead of selecting.
  /// Mirrors [drawing] for the connect flow.
  final bool connecting;

  /// Called with the features under a tap while [connecting] (nearest first, so
  /// the parent can disambiguate a stacked tap just like a normal select). Null
  /// outside connect mode.
  final void Function(List<FeatureRecord> features)? onConnectTarget;

  /// Relationships established offline (Phase 21 Slice 14), drawn as dashed purple
  /// links between endpoint coordinates. The relationship sibling of [connections].
  final List<RelationshipEdge> relationships;

  /// When true the map is in "pick a relationship target" mode: the next tap on a
  /// feature reports the hit records via [onRelateTarget] instead of selecting.
  /// Mirrors [connecting] for the relate flow.
  final bool relating;

  /// Called with the features under a tap while [relating] (nearest first). Null
  /// outside relate mode.
  final void Function(List<FeatureRecord> features)? onRelateTarget;

  /// When true the map is in "place a point" mode: the next tap reports its
  /// lon/lat via [onPointPlaced] instead of selecting a feature.
  final bool drawing;

  /// A point placed but not yet committed (the create flow's draft), painted as a
  /// distinct marker so the user sees where the new feature will land.
  final LatLng? draftPoint;

  /// The vertices of a LINE or POLYGON being traced but not yet committed (the
  /// create flow's draft). Painted as a blue polyline (or a filled ring when
  /// [draftIsPolygon]) with a marker at each vertex, so the user sees the shape
  /// take form tap by tap. Empty when not tracing. Mirrors [draftPoint] for the
  /// point case.
  final List<LatLng> draftLine;

  /// Whether the traced draft is a polygon rather than a line: its vertices are
  /// drawn as a closed, filled ring (first vertex re-joined to last) instead of
  /// an open polyline. Only meaningful when [draftLine] is non-empty.
  final bool draftIsPolygon;

  /// Called with the tapped lon/lat while [drawing]. Null outside create mode.
  final void Function(LatLng latLng)? onPointPlaced;

  /// Features with an unsynced local edit (Slice 3a). Painted with an amber
  /// "pending" overlay so it's obvious at a glance which features carry edits
  /// that haven't been pushed. An attribute-only edit reuses the feature's packed
  /// geometry (base tile still visible, amber ring on top); a MOVED feature (its
  /// id also in [movedIds]) carries a geometryOverride and is drawn FILLED amber,
  /// because its original is hidden from the base layers (see [hiddenIds]).
  final List<FeatureRecord> dirty;

  /// Ids of packed features whose GEOMETRY was moved (Slice 10 vertex editor).
  /// These render from [dirty] with a filled amber marker (points) and drive the
  /// filled-vs-hollow distinction; their originals are hidden via [hiddenIds].
  final List<String> movedIds;

  /// Ids to hide from the base pack layers via setFilter -- moved features (whose
  /// original geometry is baked into the vector tiles) plus the feature currently
  /// being edited (drawn as draggable handles instead). Empty shows everything.
  final List<String> hiddenIds;

  /// True while the geometry (vertex) editor is active. The map suppresses normal
  /// tap-to-select, draws the edited geometry as a blue connector + draggable
  /// vertex handles, and reports vertex/midpoint interactions via the callbacks.
  final bool editing;

  /// The vertices being edited (lon/lat), in order. One entry for a point, two+
  /// for a line. Each becomes a draggable handle; consecutive pairs get a
  /// tappable midpoint (line only) for inserting a vertex.
  final List<LatLng> editVertices;

  /// Whether the feature under edit is a line (draws the connector + midpoints).
  final bool editIsLine;

  /// Whether the feature under edit is a polygon: like a line it draws a
  /// connector + midpoints, but the connector closes the ring (last vertex back
  /// to first) with a fill, and there's a midpoint on that closing segment too.
  final bool editIsPolygon;

  /// The index of the currently-selected vertex (tapped), highlighted red and
  /// deletable; null when none is selected.
  final int? selectedVertex;

  /// A vertex handle finished dragging: its index and new position. The parent
  /// updates [editVertices] in response (which redraws the connector/handles).
  final void Function(int index, LatLng position)? onVertexDragged;

  /// A vertex handle was tapped: its index. The parent selects it (for delete).
  final void Function(int index)? onVertexTapped;

  /// A midpoint on segment [segIndex] was tapped, at [position]: insert a new
  /// vertex there. Traced shapes only; for a polygon, segIndex == vertexCount-1
  /// is the ring-closing segment (last vertex back to first).
  final void Function(int segIndex, LatLng position)? onMidpointTapped;

  /// When true (the map-settings toggle), a placed/dragged/inserted coordinate
  /// snaps to the nearest existing feature vertex within a small screen-space
  /// tolerance before it's reported. Off reports the raw coordinate. The feature
  /// under edit is already hidden from the query (see [hiddenIds]), so a vertex
  /// never snaps to its own geometry.
  final bool snapEnabled;

  /// [minX, minY, maxX, maxY] in lon/lat, used to frame the map on first load.
  final List<double>? bbox;

  /// The currently-picked feature (owned by the parent), highlighted on the map.
  final FeatureRecord? selected;

  /// All distinct features under the tap (nearest first). The parent selects the
  /// single hit directly, or shows a peer picker when the tap stacks 2+ features.
  final void Function(List<FeatureRecord> features) onFeaturesTapped;

  /// Invoked by the my-location control. Null hides the control (e.g. a build
  /// without the GPS slice wired). The parent owns geolocation + permissions;
  /// when it has a fix it updates [centerOn], which the view animates to -- so
  /// the button here just asks, and camera control stays in the view.
  final Future<void> Function()? onLocate;

  /// A target to recenter on when it changes (a GPS fix, typically). Mirrors the
  /// [draftPoint] pattern: the parent sets it, the view animates there. Null on
  /// first build and whenever there's nothing to recenter on.
  final LatLng? centerOn;

  /// True while a GPS fix is being acquired -- swaps the locate control for a
  /// spinner so a slow fix reads as "working," not "stuck."
  final bool locating;

  /// When true, MapLibre draws its native location puck (the pulsing blue dot +
  /// accuracy ring) that tracks the device position live. The parent flips this
  /// on only once foreground location permission is granted, since the puck's
  /// location engine needs it; otherwise the map just omits the dot.
  final bool showUserLocation;

  /// The selected basemap id (see basemaps.dart). Draws a raster provider under
  /// the pack data; [kDefaultBasemapId] ("none") is pack-only/offline. When the
  /// parent changes this, the view hot-swaps the map style without re-framing.
  final String basemapId;

  @override
  State<PackMapView> createState() => _PackMapViewState();
}

class _PackMapViewState extends State<PackMapView> {
  static const _selectionSource = 'selection';
  static const _accent = '#ff2d55';
  static const _editsSource = 'edits';
  static const _pending = '#ff9f0a'; // amber: unsynced local edit
  static const _createsSource = 'creates';
  static const _createNew = '#34c759'; // green: feature created offline
  static const _connSource = 'connections';
  static const _connColor = '#30d158'; // green: a connection established offline
  static const _connLine = 'connections-line';
  // Marching-ants dash frames (units of line width) for the connectivity line.
  // Cycling through these on a timer shifts the dash/gap boundaries so the line
  // appears to flow -- the documented MapLibre single-layer line animation. The
  // flow reads as "current through the connection," and the dashing + opacity
  // keep the synthetic edge from looking like a real (solid) conductor.
  static const _connDashSeq = <List<double>>[
    [0, 4, 3],
    [0.5, 4, 2.5],
    [1, 4, 2],
    [1.5, 4, 1.5],
    [2, 4, 1],
    [2.5, 4, 0.5],
    [3, 4, 0],
    [0, 0.5, 3, 3.5],
    [0, 1, 3, 3],
    [0, 1.5, 3, 2.5],
    [0, 2, 3, 2],
    [0, 2.5, 3, 1.5],
    [0, 3, 3, 1],
    [0, 3.5, 3, 0.5],
  ];
  static const _relSource = 'relationships';
  static const _relColor = '#bf5af2'; // purple: a relationship established offline
  static const _draftSource = 'draft';
  static const _draftColor = '#0a84ff'; // blue: point being placed (draft)
  static const _editGeomSource = 'editgeom';
  static const _editColor = '#0a84ff'; // blue: geometry being edited (connector)
  static const _vertexColor = '#0a84ff'; // blue: a draggable vertex handle
  static const _vertexSelected = '#ff2d55'; // red: the selected/deletable vertex
  static const _midpointColor = '#ffffff'; // white: an insert-a-vertex midpoint
  // How close (screen px) a placed/dragged coordinate must fall to an existing
  // feature vertex to snap onto it. Finger-friendly, matching the tap tolerance.
  static const _snapPixels = 20.0;
  // The queryable creates layers, so a tap can hit a created feature too.
  static const _createsCircle = 'creates-circle';
  static const _createsLine = 'creates-line';
  static const _createsFill = 'creates-fill';
  // The queryable edits layers, so a tap can hit a MOVED feature (hidden from the
  // base layers) too.
  static const _editsFill = 'edits-fill';
  static const _editsLine = 'edits-line';
  static const _editsCircle = 'edits-circle';
  static const _editsMovedCircle = 'edits-moved-circle';

  MapLibreMapController? _controller;
  PackStyle? _style;
  Object? _error;
  bool _styleReady = false;

  /// The draggable vertex + tappable midpoint handles currently on the map (the
  /// geometry editor). Rebuilt whenever the edited vertices/selection change.
  final List<Circle> _handles = [];

  /// Reentrancy guard for [_syncHandles]: rebuilding handles is a sequence of
  /// async add/clear calls, and a vertex drag can fire another rebuild mid-flight.
  /// _handlesBusy means one is running; _handlesDirty means "run again after."
  bool _handlesBusy = false;
  bool _handlesDirty = false;

  /// The marching-ants animation for the connectivity line: a periodic timer
  /// advances [_connDashPhase] through [_connDashSeq], re-setting the layer's
  /// dash each tick so it flows. [_connDashBusy] skips a tick while the previous
  /// paint update is still in flight (each is a platform round-trip).
  Timer? _connDashTimer;
  int _connDashPhase = 0;
  bool _connDashBusy = false;

  /// True once the camera has been framed to the pack extent. A basemap swap
  /// reloads the style (firing onStyleLoaded again), and we must NOT re-frame on
  /// that reload -- the user's current pan/zoom should survive changing basemaps.
  bool _framedOnce = false;

  /// The map's current compass bearing in degrees (0 = north up). Tracked so the
  /// custom compass needle rotates with the map and taps can reset it to north.
  /// The native compass is disabled (it hides itself at north, which read as
  /// "the north button is missing") in favour of this always-visible one.
  double _bearing = 0;

  /// The box the map actually frames to: the real feature extent (computed from
  /// the pack's geometries) if available, else the requested scope bbox. This is
  /// what stops a wide-bbox pack from opening blank and zoomed-out past the tiles.
  List<double>? _frameBbox;

  /// The map widget's pixel size (from the LayoutBuilder in build). Needed to
  /// compute a fit zoom that respects the tile zoom range instead of letting a
  /// bounds-fit underflow minZoom.
  Size? _viewSize;

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
      // Prefer the real feature extent; fall back to the requested scope bbox.
      _frameBbox = widget.db.featureExtent() ?? widget.bbox;
      final style = PackStyle.build(
        classes: widget.db.classMetas(),
        tileTemplate: _tiles.tileTemplate,
        minZoom: _tiles.minZoom,
        maxZoom: _tiles.maxZoom,
        basemap: basemapById(widget.basemapId),
        symbology: widget.db.symbologyByClass(),
        iconSvg: widget.db.iconSvgByRef(),
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
    // The dirty set changing (a feature gained/lost a pending edit) OR a moved
    // feature's geometry changing redraws the overlay. movedIds is compared too
    // because a move flips a feature between hollow-ring and filled rendering.
    if (!_sameIds(old.dirty, widget.dirty) ||
        !_sameStrings(old.movedIds, widget.movedIds)) {
      _applyDirty();
    }
    if (!_sameStrings(old.hiddenIds, widget.hiddenIds)) {
      _applyHidden();
    }
    if (!_sameIds(old.created, widget.created)) {
      _applyCreates();
    }
    if (!_sameEdges(old.connections, widget.connections)) {
      _applyConnections();
    }
    if (!_sameRelEdges(old.relationships, widget.relationships)) {
      _applyRelationships();
    }
    if (old.draftPoint != widget.draftPoint ||
        old.draftIsPolygon != widget.draftIsPolygon ||
        !_sameLatLngs(old.draftLine, widget.draftLine)) {
      _applyDraft();
    }
    // The geometry editor: redraw the blue connector and rebuild the vertex/
    // midpoint handles whenever the edit state, the vertices, or the selected
    // vertex changes (a drag commits a vertex, a tap changes the selection).
    if (old.editing != widget.editing ||
        old.editIsLine != widget.editIsLine ||
        old.editIsPolygon != widget.editIsPolygon ||
        old.selectedVertex != widget.selectedVertex ||
        !_sameLatLngs(old.editVertices, widget.editVertices)) {
      _applyEditGeom();
      _syncHandles();
    }
    if (old.centerOn != widget.centerOn && widget.centerOn != null) {
      _recenterOn(widget.centerOn!);
    }
    if (old.basemapId != widget.basemapId) {
      _applyBasemap();
    }
  }

  /// Hot-swap the map style when the basemap changes: rebuild the style with the
  /// new provider and hand it to the controller, which reloads and re-fires
  /// onStyleLoaded (re-adding the overlays). The camera is left where it is --
  /// _framedOnce keeps the reload from re-framing to the pack extent. The pack's
  /// queryable layer ids don't depend on the basemap, so [_style] (which backs
  /// the widget's stable styleString + hit-testing) stays as built at boot.
  Future<void> _applyBasemap() async {
    final controller = _controller;
    if (controller == null) return;
    final style = PackStyle.build(
      classes: widget.db.classMetas(),
      tileTemplate: _tiles.tileTemplate,
      minZoom: _tiles.minZoom,
      maxZoom: _tiles.maxZoom,
      basemap: basemapById(widget.basemapId),
      symbology: widget.db.symbologyByClass(),
      iconSvg: widget.db.iconSvgByRef(),
    );
    _styleReady = false;
    try {
      await controller.setStyle(style.json);
    } catch (_) {}
  }

  /// Animate to a target the parent handed us (a GPS fix), keeping the current
  /// zoom unless we're zoomed out past a useful street level.
  Future<void> _recenterOn(LatLng target) async {
    final controller = _controller;
    if (controller == null) return;
    final currentZoom = controller.cameraPosition?.zoom ?? 0;
    final zoom = currentZoom < 15 ? 16.0 : currentZoom;
    try {
      await controller.animateCamera(CameraUpdate.newLatLngZoom(target, zoom));
    } catch (_) {}
  }

  static bool _sameIds(List<FeatureRecord> a, List<FeatureRecord> b) {
    if (a.length != b.length) return false;
    final ids = {for (final f in a) f.id};
    return b.every((f) => ids.contains(f.id));
  }

  static bool _sameEdges(List<ConnectionEdge> a, List<ConnectionEdge> b) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i].from != b[i].from || a[i].to != b[i].to) return false;
    }
    return true;
  }

  static bool _sameRelEdges(List<RelationshipEdge> a, List<RelationshipEdge> b) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i].from != b[i].from || a[i].to != b[i].to) return false;
    }
    return true;
  }

  static bool _sameLatLngs(List<LatLng> a, List<LatLng> b) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i] != b[i]) return false;
    }
    return true;
  }

  static bool _sameStrings(List<String> a, List<String> b) {
    if (a.length != b.length) return false;
    final set = a.toSet();
    return b.every(set.contains);
  }

  @override
  void dispose() {
    _connDashTimer?.cancel();
    _controller?.removeListener(_onCameraChanged);
    _controller?.onFeatureDrag.remove(_onFeatureDrag);
    _controller?.onCircleTapped.remove(_onCircleTapped);
    _tiles.stop();
    super.dispose();
  }

  /// The controller notifies on camera moves (trackCameraPosition is on). Repaint
  /// the compass only when the bearing actually changed, not on every pan/zoom
  /// tick, so the needle stays in sync without churning the whole overlay.
  void _onCameraChanged() {
    final bearing = _controller?.cameraPosition?.bearing ?? 0;
    if ((bearing - _bearing).abs() < 0.5) return;
    if (mounted) setState(() => _bearing = bearing);
  }

  CameraPosition get _initialCamera {
    final b = _frameBbox ?? widget.bbox;
    if (b != null && b.length == 4) {
      // Fall back to a plausible landscape size if the LayoutBuilder hasn't run
      // yet; _fitToBounds re-frames with the real size once the style loads.
      final size = _viewSize ?? const Size(1080, 720);
      return CameraPosition(
        target: LatLng((b[1] + b[3]) / 2, (b[0] + b[2]) / 2),
        zoom: _fitZoom(b, size),
      );
    }
    return const CameraPosition(target: LatLng(0, 0), zoom: 2);
  }

  /// The zoom that fits bbox [b] = [minLon,minLat,maxLon,maxLat] into a widget of
  /// [size] px, in MapLibre's 512px-per-tile scale, then clamped to the pack's
  /// tile zoom range. The clamp is the whole point: tippecanoe writes tiles only
  /// from minZoom up, so a fit that lands below it shows nothing but the
  /// background. A degenerate (single-point) extent has zero span on an axis, so
  /// that axis imposes no upper bound and we let the other axis (or maxZoom) win.
  double _fitZoom(List<double> b, Size size) {
    const tile = 512.0;
    const pad = 64.0; // margin in px, so features aren't flush to the edge
    final w = math.max(1.0, size.width - pad);
    final h = math.max(1.0, size.height - pad);
    final fracX = (_mercX(b[2]) - _mercX(b[0])).abs();
    final fracY = (_mercY(b[1]) - _mercY(b[3])).abs();
    final zx = fracX <= 0 ? 22.0 : _log2(w / (tile * fracX));
    final zy = fracY <= 0 ? 22.0 : _log2(h / (tile * fracY));
    final z = math.min(zx, zy);
    return z.clamp(_tiles.minZoom.toDouble(), _tiles.maxZoom.toDouble());
  }

  // Web Mercator normalized coordinates in [0,1]. _mercY grows southward.
  static double _mercX(double lon) => (lon + 180.0) / 360.0;
  static double _mercY(double lat) {
    final s = (math.sin(lat * math.pi / 180.0)).clamp(-0.9999, 0.9999);
    return 0.5 - math.log((1 + s) / (1 - s)) / (4 * math.pi);
  }

  static double _log2(double x) => math.log(x) / math.ln2;

  /// Platform channel to the host, mirrored in MainActivity.kt.
  static const _hostChannel = MethodChannel('voltedge/field/maplibre');

  void _onMapCreated(MapLibreMapController controller) {
    _controller = controller;
    controller.addListener(_onCameraChanged);
    // The geometry editor uses draggable Circle annotations as vertex handles and
    // non-draggable ones as midpoints. Register the drag + tap callbacks once here
    // (removed in dispose): a vertex drag commits on DragEventType.end, a circle
    // tap either selects a vertex or inserts one at a midpoint.
    controller.onFeatureDrag.add(_onFeatureDrag);
    controller.onCircleTapped.add(_onCircleTapped);
    // MapLibre Native suppresses HTTP requests (including our loopback tile
    // server) whenever the OS reports no connectivity. This pack is offline by
    // design, so force the SDK "connected" -- otherwise tiles never load with
    // the network off. Runs after the SDK is initialized (map created).
    _forceMapLibreConnected();
  }

  /// Rotate the map back to north-up.
  Future<void> _resetNorth() async {
    try {
      await _controller?.animateCamera(CameraUpdate.bearingTo(0));
    } catch (_) {}
  }

  Future<void> _forceMapLibreConnected() async {
    try {
      await _hostChannel.invokeMethod('setConnected');
    } catch (_) {
      // Best-effort: on iOS / if the host method is absent this is a no-op; the
      // online path is unaffected.
    }
  }

  Future<void> _onStyleLoaded() async {
    final controller = _controller;
    if (controller == null) return;

    // Register the point-icon images the style's symbol layers name, FIRST:
    // MapLibre draws nothing for an `icon-image` whose bytes it hasn't been
    // given, and a style reload (e.g. a basemap swap) clears registered images,
    // so this must re-run on every style load, before anything relies on them.
    await _registerIcons();

    // The "created offline" overlay, added FIRST (bottom of the overlay stack)
    // so edits and the selection highlight draw on top of it. Green, and -- unlike
    // the amber edits ring -- FILLED, because a created feature isn't in the vector
    // tiles at all, so this overlay is the ONLY thing that draws it.
    await controller.addGeoJsonSource(_createsSource, _emptyCollection());
    await controller.addFillLayer(
      _createsSource,
      _createsFill,
      FillLayerProperties(fillColor: _createNew, fillOpacity: 0.25),
      filter: [
        'match',
        ['geometry-type'],
        ['Polygon', 'MultiPolygon'],
        true,
        false,
      ],
    );
    await controller.addLineLayer(
      _createsSource,
      _createsLine,
      LineLayerProperties(
        lineColor: _createNew,
        lineWidth: 4.0,
        lineOpacity: 0.95,
        lineCap: 'round',
        lineJoin: 'round',
      ),
      filter: [
        'match',
        ['geometry-type'],
        ['LineString', 'MultiLineString', 'Polygon', 'MultiPolygon'],
        true,
        false,
      ],
    );
    await controller.addCircleLayer(
      _createsSource,
      _createsCircle,
      CircleLayerProperties(
        circleRadius: 7.0,
        circleColor: _createNew,
        circleOpacity: 0.9,
        circleStrokeColor: '#ffffff',
        circleStrokeWidth: 2.0,
      ),
      filter: [
        'match',
        ['geometry-type'],
        ['Point', 'MultiPoint'],
        true,
        false,
      ],
    );

    // The "connection established offline" overlay (Slice 13b): green links drawn
    // between endpoint coordinates, added above the creates overlay so a link to a
    // created feature draws over it, and below edits/selection. A line per edge,
    // plus a small dot at each endpoint so a link is visible even when it lands on
    // an existing marker.
    await controller.addGeoJsonSource(_connSource, _emptyCollection());
    await controller.addLineLayer(
      _connSource,
      _connLine,
      _connLineProps(_connDashSeq.first),
      filter: [
        'match',
        ['geometry-type'],
        ['LineString'],
        true,
        false,
      ],
    );
    await controller.addCircleLayer(
      _connSource,
      'connections-circle',
      CircleLayerProperties(
        circleRadius: 4.0,
        circleColor: _connColor,
        circleOpacity: 0.95,
        circleStrokeColor: '#ffffff',
        circleStrokeWidth: 1.5,
      ),
      filter: [
        'match',
        ['geometry-type'],
        ['Point'],
        true,
        false,
      ],
    );

    // The "relationship established offline" overlay (Slice 14): the relationship
    // sibling of the connections overlay, in purple and DASHED so a relationship
    // reads distinctly from a (solid green) connection. Added just above the
    // connections overlay. A dashed line per edge, plus a dot at each endpoint.
    await controller.addGeoJsonSource(_relSource, _emptyCollection());
    await controller.addLineLayer(
      _relSource,
      'relationships-line',
      LineLayerProperties(
        lineColor: _relColor,
        lineWidth: 3.0,
        lineOpacity: 0.95,
        lineCap: 'round',
        lineJoin: 'round',
        lineDasharray: [2.0, 2.0],
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
      _relSource,
      'relationships-circle',
      CircleLayerProperties(
        circleRadius: 4.0,
        circleColor: _relColor,
        circleOpacity: 0.95,
        circleStrokeColor: '#ffffff',
        circleStrokeWidth: 1.5,
      ),
      filter: [
        'match',
        ['geometry-type'],
        ['Point'],
        true,
        false,
      ],
    );

    // The "pending edit" overlay, added so the red selection highlight draws on
    // top of it. Same one-source-per-geometry-kind trick as selection, in amber,
    // styled as an outline/ring so it reads as a marker, not a fill.
    await controller.addGeoJsonSource(_editsSource, _emptyCollection());
    await controller.addFillLayer(
      _editsSource,
      'edits-fill',
      FillLayerProperties(fillColor: _pending, fillOpacity: 0.12),
      filter: [
        'match',
        ['geometry-type'],
        ['Polygon', 'MultiPolygon'],
        true,
        false,
      ],
    );
    await controller.addLineLayer(
      _editsSource,
      'edits-line',
      LineLayerProperties(
        lineColor: _pending,
        lineWidth: 4.0,
        lineOpacity: 0.9,
        lineCap: 'round',
        lineJoin: 'round',
      ),
      filter: [
        'match',
        ['geometry-type'],
        ['LineString', 'MultiLineString', 'Polygon', 'MultiPolygon'],
        true,
        false,
      ],
    );
    await controller.addCircleLayer(
      _editsSource,
      _editsCircle,
      CircleLayerProperties(
        circleRadius: 11.0,
        circleColor: _pending,
        circleOpacity: 0.0, // hollow ring, so the base symbol stays visible
        circleStrokeColor: _pending,
        circleStrokeWidth: 3.0,
      ),
      filter: [
        'match',
        ['geometry-type'],
        ['Point', 'MultiPoint'],
        true,
        false,
      ],
    );
    // A MOVED point's original is hidden from the base layers, so the hollow ring
    // above would leave nothing to see. This FILLED amber dot (only for features
    // flagged moved) is the whole marker in that case. Drawn on top of the ring.
    await controller.addCircleLayer(
      _editsSource,
      _editsMovedCircle,
      CircleLayerProperties(
        circleRadius: 7.0,
        circleColor: _pending,
        circleOpacity: 0.9,
        circleStrokeColor: '#ffffff',
        circleStrokeWidth: 2.0,
      ),
      filter: [
        'all',
        [
          'match',
          ['geometry-type'],
          ['Point', 'MultiPoint'],
          true,
          false,
        ],
        ['==', ['get', 'moved'], true],
      ],
    );

    // The highlight overlay: one empty GeoJSON source + a draw layer per
    // geometry kind. Only the layer matching the picked feature's type paints,
    // so one source covers points, lines, and polygons.
    await controller.addGeoJsonSource(_selectionSource, _emptyCollection());
    // Each highlight layer is filtered to its own geometry type. Without this a
    // circle layer paints a dot at EVERY vertex of a selected line/polygon --
    // we only want the circle for actual points, the line for lines, the fill
    // for polygons.
    await controller.addFillLayer(
      _selectionSource,
      'selection-fill',
      FillLayerProperties(fillColor: _accent, fillOpacity: 0.18),
      filter: [
        'match',
        ['geometry-type'],
        ['Polygon', 'MultiPolygon'],
        true,
        false,
      ],
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
      // Lines proper, plus polygon outlines -- but never a line drawn through a
      // point's (nonexistent) path.
      filter: [
        'match',
        ['geometry-type'],
        ['LineString', 'MultiLineString', 'Polygon', 'MultiPolygon'],
        true,
        false,
      ],
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
      filter: [
        'match',
        ['geometry-type'],
        ['Point', 'MultiPoint'],
        true,
        false,
      ],
    );

    // The draft overlay, added LAST so it sits on top of everything: the point
    // being placed (point create) or the line being traced (line create), before
    // the feature is committed. The line connector draws first, then a marker at
    // each vertex on top. One source per geometry kind, filtered like the others.
    await controller.addGeoJsonSource(_draftSource, _emptyCollection());
    // A polygon draft is drawn as a translucent fill under its outline; a line
    // draft carries no Polygon geometry, so this layer stays empty for it.
    await controller.addFillLayer(
      _draftSource,
      'draft-fill',
      FillLayerProperties(fillColor: _draftColor, fillOpacity: 0.2),
      filter: [
        'match',
        ['geometry-type'],
        ['Polygon'],
        true,
        false,
      ],
    );
    await controller.addLineLayer(
      _draftSource,
      'draft-line',
      LineLayerProperties(
        lineColor: _draftColor,
        lineWidth: 4.0,
        lineOpacity: 0.9,
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
      _draftSource,
      'draft-circle',
      CircleLayerProperties(
        circleRadius: 8.0,
        circleColor: _draftColor,
        circleOpacity: 0.9,
        circleStrokeColor: '#ffffff',
        circleStrokeWidth: 2.5,
      ),
      // Vertices only -- otherwise the circle layer would also dot along the
      // line's own path.
      filter: [
        'match',
        ['geometry-type'],
        ['Point'],
        true,
        false,
      ],
    );

    // The geometry-editor connector: a blue line joining the vertices being
    // edited (lines only). Added above the draft so it sits over the base data;
    // the draggable vertex handles are Circle annotations, which MapLibre always
    // draws on top of every layer. One source, a single line layer.
    await controller.addGeoJsonSource(_editGeomSource, _emptyCollection());
    // A polygon under edit fills its ring; a point/line carries no Polygon on
    // this source, so the fill layer stays empty for them.
    await controller.addFillLayer(
      _editGeomSource,
      'editgeom-fill',
      FillLayerProperties(fillColor: _editColor, fillOpacity: 0.15),
      filter: [
        'match',
        ['geometry-type'],
        ['Polygon'],
        true,
        false,
      ],
    );
    await controller.addLineLayer(
      _editGeomSource,
      'editgeom-line',
      LineLayerProperties(
        lineColor: _editColor,
        lineWidth: 3.0,
        lineOpacity: 0.9,
        lineCap: 'round',
        lineJoin: 'round',
      ),
      // Only the connector LineString draws here; a polygon's fill is the layer
      // above, and its outline is emitted as a closed-ring LineString too.
      filter: [
        'match',
        ['geometry-type'],
        ['LineString'],
        true,
        false,
      ],
    );

    _styleReady = true;
    // Frame the pack extent on the FIRST style load only; a basemap swap reloads
    // the style but must keep the user's current camera.
    if (!_framedOnce) {
      await _fitToBounds();
      _framedOnce = true;
    }
    await _applyHidden();
    await _applyHighlight();
    await _applyDirty();
    await _applyCreates();
    await _applyConnections();
    await _applyRelationships();
    await _applyDraft();
    await _applyEditGeom();
    // A style (re)load clears every annotation, so rebuild the handles if an edit
    // is in progress (e.g. across a basemap swap).
    await _syncHandles();
    // (Re)start the connectivity line's marching-ants animation -- a reload
    // re-added the layer at dash frame 0, so restart the timer against it.
    _startConnDashAnimation();
  }

  /// Rasterize and register every point-icon image the style references, so the
  /// symbol layers' data-driven `icon-image` expressions can resolve. Names come
  /// straight off the style (PackStyle.icons), so exactly the set the
  /// expressions name is registered -- no more, no less. Best-effort per image:
  /// a single failed icon shouldn't blank the map.
  Future<void> _registerIcons() async {
    final controller = _controller;
    final style = _style;
    if (controller == null || style == null) return;
    for (final spec in style.icons) {
      try {
        final bytes = await rasterizeIconSpec(spec);
        await controller.addImage(spec.name, bytes);
      } catch (_) {}
    }
  }

  /// Repaint the pending-edit overlay from the current dirty set. Each feature's
  /// geometry comes from `f.geometry()` -- for a MOVED feature that's its
  /// geometryOverride (the new shape), for an attribute-only edit the packed
  /// geometry. The `moved` property drives the filled-vs-hollow point marker.
  Future<void> _applyDirty() async {
    final controller = _controller;
    if (controller == null || !_styleReady) return;
    final moved = widget.movedIds.toSet();
    final feats = <Map<String, dynamic>>[];
    for (final f in widget.dirty) {
      final geom = f.geometry();
      if (geom == null) continue;
      feats.add({
        'type': 'Feature',
        'properties': {'id': f.id, 'moved': moved.contains(f.id)},
        'geometry': _geoJsonGeometry(geom),
      });
    }
    try {
      await controller.setGeoJsonSource(
        _editsSource,
        {'type': 'FeatureCollection', 'features': feats},
      );
    } catch (_) {}
  }

  /// Repaint the green "created offline" overlay from the current create set.
  Future<void> _applyCreates() async {
    final controller = _controller;
    if (controller == null || !_styleReady) return;
    final feats = <Map<String, dynamic>>[];
    for (final f in widget.created) {
      final geom = f.geometry();
      if (geom == null) continue;
      feats.add({
        'type': 'Feature',
        'properties': {'id': f.id},
        'geometry': _geoJsonGeometry(geom),
      });
    }
    try {
      await controller.setGeoJsonSource(
        _createsSource,
        {'type': 'FeatureCollection', 'features': feats},
      );
    } catch (_) {}
  }

  /// The connectivity line's paint for a given dash frame. Rebuilt in full every
  /// animation tick because [MapLibreMapController.setLayerProperties] resets any
  /// property left null -- so colour/width/opacity are re-sent alongside the
  /// shifting [dash]. A **zoom-interpolated width with a ~2.5px floor** keeps a
  /// minimum thickness at every zoom (never a hairline), and **0.75 opacity** +
  /// the dashing keep this synthetic edge reading as an annotation, not a solid
  /// conductor.
  LineLayerProperties _connLineProps(List<double> dash) => LineLayerProperties(
        lineColor: _connColor,
        lineWidth: [
          'interpolate',
          ['linear'],
          ['zoom'],
          10.0,
          2.5,
          16.0,
          5.0,
        ],
        lineOpacity: 0.75,
        lineCap: 'round',
        lineJoin: 'round',
        lineDasharray: dash,
      );

  /// Start (or restart) the marching-ants timer for the connectivity line. Cheap
  /// when idle: a tick with no connections drawn does nothing, so the timer can
  /// run for the map's lifetime without churning the platform channel. Restarted
  /// on every style (re)load, since a reload re-adds the layer from frame 0.
  void _startConnDashAnimation() {
    _connDashTimer?.cancel();
    _connDashTimer = Timer.periodic(
      const Duration(milliseconds: 80),
      (_) => _tickConnDash(),
    );
  }

  /// Advance the connectivity line's dash one frame. Skips entirely when nothing
  /// is drawn (no selection / no edges) or when the previous paint update is
  /// still in flight, so the animation never queues up round-trips.
  Future<void> _tickConnDash() async {
    final controller = _controller;
    if (controller == null || !_styleReady || _connDashBusy) return;
    if (widget.connections.isEmpty) return;
    _connDashBusy = true;
    _connDashPhase = (_connDashPhase + 1) % _connDashSeq.length;
    try {
      await controller.setLayerProperties(
        _connLine,
        _connLineProps(_connDashSeq[_connDashPhase]),
      );
    } catch (_) {
      // Best-effort: a dropped paint update just skips a frame.
    } finally {
      _connDashBusy = false;
    }
  }

  /// Repaint the connections overlay: one green LineString per edge joining its
  /// endpoints, plus a Point at each end so a link lands visibly on top of the
  /// existing feature markers. Empty when no connections have been established.
  Future<void> _applyConnections() async {
    final controller = _controller;
    if (controller == null || !_styleReady) return;
    final feats = <Map<String, dynamic>>[];
    for (final e in widget.connections) {
      feats.add({
        'type': 'Feature',
        'properties': const <String, dynamic>{},
        'geometry': {
          'type': 'LineString',
          'coordinates': [
            [e.from.longitude, e.from.latitude],
            [e.to.longitude, e.to.latitude],
          ],
        },
      });
      for (final p in [e.from, e.to]) {
        feats.add({
          'type': 'Feature',
          'properties': const <String, dynamic>{},
          'geometry': {
            'type': 'Point',
            'coordinates': [p.longitude, p.latitude],
          },
        });
      }
    }
    try {
      await controller.setGeoJsonSource(
        _connSource,
        {'type': 'FeatureCollection', 'features': feats},
      );
    } catch (_) {}
  }

  /// Repaint the relationships overlay: one purple LineString per edge joining its
  /// endpoints, plus a Point at each end. The relationship sibling of
  /// [_applyConnections]; the dashing that distinguishes it lives in the layer's
  /// paint, so the geometry built here is identical in shape.
  Future<void> _applyRelationships() async {
    final controller = _controller;
    if (controller == null || !_styleReady) return;
    final feats = <Map<String, dynamic>>[];
    for (final e in widget.relationships) {
      feats.add({
        'type': 'Feature',
        'properties': const <String, dynamic>{},
        'geometry': {
          'type': 'LineString',
          'coordinates': [
            [e.from.longitude, e.from.latitude],
            [e.to.longitude, e.to.latitude],
          ],
        },
      });
      for (final p in [e.from, e.to]) {
        feats.add({
          'type': 'Feature',
          'properties': const <String, dynamic>{},
          'geometry': {
            'type': 'Point',
            'coordinates': [p.longitude, p.latitude],
          },
        });
      }
    }
    try {
      await controller.setGeoJsonSource(
        _relSource,
        {'type': 'FeatureCollection', 'features': feats},
      );
    } catch (_) {}
  }

  /// Repaint the draft overlay: a single marker for a point being placed, or the
  /// traced polyline (or filled closed ring, for a polygon) plus a marker per
  /// vertex for a shape being drawn. Empty when neither is active.
  Future<void> _applyDraft() async {
    final controller = _controller;
    if (controller == null || !_styleReady) return;
    final feats = <Map<String, dynamic>>[];
    final line = widget.draftLine;
    if (line.isNotEmpty) {
      final coords = [
        for (final v in line) [v.longitude, v.latitude],
      ];
      // A polygon of 3+ corners draws as a filled closed ring plus a closed
      // outline; below 3 (or a line) it draws as an open connector once there
      // are 2 points to join.
      if (widget.draftIsPolygon && line.length >= 3) {
        final ring = [...coords, coords.first];
        feats.add({
          'type': 'Feature',
          'properties': const <String, dynamic>{},
          'geometry': {'type': 'Polygon', 'coordinates': [ring]},
        });
        feats.add({
          'type': 'Feature',
          'properties': const <String, dynamic>{},
          'geometry': {'type': 'LineString', 'coordinates': ring},
        });
      } else if (line.length >= 2) {
        feats.add({
          'type': 'Feature',
          'properties': const <String, dynamic>{},
          'geometry': {'type': 'LineString', 'coordinates': coords},
        });
      }
      for (final v in line) {
        feats.add({
          'type': 'Feature',
          'properties': const <String, dynamic>{},
          'geometry': {
            'type': 'Point',
            'coordinates': [v.longitude, v.latitude],
          },
        });
      }
    }
    final p = widget.draftPoint;
    if (p != null) {
      feats.add({
        'type': 'Feature',
        'properties': const <String, dynamic>{},
        'geometry': {
          'type': 'Point',
          'coordinates': [p.longitude, p.latitude],
        },
      });
    }
    try {
      await controller.setGeoJsonSource(
        _draftSource,
        {'type': 'FeatureCollection', 'features': feats},
      );
    } catch (_) {}
  }

  /// Hide the [hiddenIds] features from every base pack layer via setFilter, so a
  /// moved feature's original (baked into the vector tiles) doesn't show through
  /// under its overlay, and the feature being edited disappears behind its handles.
  ///
  /// A per-rule symbology sub-layer (dash/zoom buckets) carries its own base
  /// filter selecting the features it draws, so the hide filter is COMPOSED with
  /// it (`['all', base, hide]`) rather than overwriting it -- overwrite would make
  /// the bucket start drawing every feature. Layers with no base filter (points,
  /// single-rule classes) just take the hide filter. An empty id list yields
  /// `!in(id, [])` = `!false` = true, so composing leaves the base filter intact
  /// (or shows everything where there's no base filter).
  Future<void> _applyHidden() async {
    final controller = _controller;
    final style = _style;
    if (controller == null || style == null || !_styleReady) return;
    final ids = <dynamic>[for (final id in widget.hiddenIds) id];
    final hide = <dynamic>[
      '!',
      [
        'in',
        ['get', 'id'],
        ['literal', ids],
      ],
    ];
    for (final layerId in style.queryableLayerIds) {
      final base = style.layerFilters[layerId];
      final filter =
          base == null ? hide : <dynamic>['all', base, hide];
      try {
        await controller.setFilter(layerId, filter);
      } catch (_) {}
    }
  }

  /// Repaint the geometry-editor connector: the blue line joining the edited
  /// vertices (lines only; a point has no connector). Empty when not editing.
  Future<void> _applyEditGeom() async {
    final controller = _controller;
    if (controller == null || !_styleReady) return;
    final feats = <Map<String, dynamic>>[];
    final verts = widget.editVertices;
    if (widget.editing && widget.editIsPolygon && verts.length >= 3) {
      // A polygon: a filled closed ring plus a closed outline (first re-joined
      // to last). The fill draws on editgeom-fill, the outline on editgeom-line.
      final coords = [
        for (final v in verts) [v.longitude, v.latitude],
      ];
      final ring = [...coords, coords.first];
      feats.add({
        'type': 'Feature',
        'properties': const <String, dynamic>{},
        'geometry': {'type': 'Polygon', 'coordinates': [ring]},
      });
      feats.add({
        'type': 'Feature',
        'properties': const <String, dynamic>{},
        'geometry': {'type': 'LineString', 'coordinates': ring},
      });
    } else if (widget.editing && widget.editIsLine && verts.length >= 2) {
      feats.add({
        'type': 'Feature',
        'properties': const <String, dynamic>{},
        'geometry': {
          'type': 'LineString',
          'coordinates': [
            for (final v in verts) [v.longitude, v.latitude],
          ],
        },
      });
    }
    try {
      await controller.setGeoJsonSource(
        _editGeomSource,
        {'type': 'FeatureCollection', 'features': feats},
      );
    } catch (_) {}
  }

  /// Rebuild the vertex + midpoint handles for the geometry editor. Vertices are
  /// DRAGGABLE circles (blue, red when selected); midpoints are non-draggable
  /// white circles between consecutive vertices (traced shapes only, plus the
  /// ring-closing segment for a polygon) that insert a vertex when tapped. Each
  /// circle carries `data: {'kind','i'}` so the drag/tap
  /// callbacks know what it is. Reentrancy-guarded because a drag can trigger a
  /// rebuild while one is mid-flight (each add/clear is an async round-trip).
  Future<void> _syncHandles() async {
    final controller = _controller;
    if (controller == null || !_styleReady) return;
    if (_handlesBusy) {
      _handlesDirty = true;
      return;
    }
    _handlesBusy = true;
    try {
      await controller.clearCircles();
      _handles.clear();
      if (!widget.editing) return;
      final verts = widget.editVertices;
      // Midpoints first (traced shapes only), so vertex handles draw on top. A
      // polygon adds one extra midpoint on the segment closing the ring (its
      // last vertex back to its first), so every edge -- including the closer --
      // can gain a corner.
      final tracing = widget.editIsLine || widget.editIsPolygon;
      if (tracing) {
        final segs = widget.editIsPolygon ? verts.length : verts.length - 1;
        for (var i = 0; i < segs; i++) {
          final next = verts[(i + 1) % verts.length];
          final mid = LatLng(
            (verts[i].latitude + next.latitude) / 2,
            (verts[i].longitude + next.longitude) / 2,
          );
          final c = await controller.addCircle(
            CircleOptions(
              geometry: mid,
              circleRadius: 5.0,
              circleColor: _midpointColor,
              circleOpacity: 0.9,
              circleStrokeColor: _editColor,
              circleStrokeWidth: 2.0,
              draggable: false,
            ),
            {'kind': 'm', 'i': i},
          );
          _handles.add(c);
        }
      }
      for (var i = 0; i < verts.length; i++) {
        final selected = widget.selectedVertex == i;
        final c = await controller.addCircle(
          CircleOptions(
            geometry: verts[i],
            circleRadius: 8.0,
            circleColor: selected ? _vertexSelected : _vertexColor,
            circleOpacity: 0.95,
            circleStrokeColor: '#ffffff',
            circleStrokeWidth: 2.5,
            draggable: true,
          ),
          {'kind': 'v', 'i': i},
        );
        _handles.add(c);
      }
    } catch (_) {
      // Best-effort: a failed handle rebuild shouldn't crash the editor.
    } finally {
      _handlesBusy = false;
      if (_handlesDirty) {
        _handlesDirty = false;
        // Coalesce the rebuild requested mid-flight into one more pass.
        await _syncHandles();
      }
    }
  }

  /// A vertex handle finished dragging: commit its new position on END only (not
  /// every intermediate drag frame), reading the vertex index off the circle's
  /// data. Midpoints aren't draggable, so only 'v' handles reach here.
  void _onFeatureDrag(
    math.Point<double> point,
    LatLng origin,
    LatLng current,
    LatLng delta,
    String id,
    Annotation? annotation,
    DragEventType eventType,
  ) {
    if (eventType != DragEventType.end) return;
    if (annotation is! Circle) return;
    final data = annotation.data;
    if (data == null || data['kind'] != 'v') return;
    final i = data['i'];
    if (i is! int) return;
    // Snap the dropped position onto a nearby feature vertex (no-op when off).
    // The dragged feature is hidden from the query layers, so it can't snap to
    // itself. Async, so fire-and-forget the report once the snap resolves.
    _snap(current).then((snapped) => widget.onVertexDragged?.call(i, snapped));
  }

  /// A handle circle was tapped: a vertex 'v' selects it (for delete), a midpoint
  /// 'm' inserts a new vertex at that segment.
  void _onCircleTapped(Circle circle) {
    final data = circle.data;
    if (data == null) return;
    final i = data['i'];
    if (i is! int) return;
    if (data['kind'] == 'v') {
      widget.onVertexTapped?.call(i);
    } else if (data['kind'] == 'm') {
      final geom = circle.options.geometry;
      // Snap the inserted vertex onto a nearby feature vertex (no-op when off).
      if (geom != null) {
        _snap(geom).then((snapped) => widget.onMidpointTapped?.call(i, snapped));
      }
    }
  }

  /// Created features by id, for resolving a tap that lands on the green overlay
  /// (those ids aren't in the pack's features table).
  Map<String, FeatureRecord> get _createdById =>
      {for (final f in widget.created) f.id: f};

  Future<void> _fitToBounds() async {
    final controller = _controller;
    final b = _frameBbox ?? widget.bbox;
    if (controller == null || b == null || b.length != 4) return;
    // Read the map's real size from its render box (available now -- this runs
    // after layout, in onStyleLoaded), falling back to a plausible size only if
    // it hasn't been laid out yet. No LayoutBuilder wrapper, which would rebuild
    // the platform view.
    final size = context.size ?? _viewSize ?? const Size(1080, 720);
    _viewSize = size;
    // Move to the extent centre at a zoom we compute + clamp to the tile range,
    // NOT newLatLngBounds -- a bounds-fit can zoom out past minZoom (where no
    // tiles exist), which is what left the map blank on wide-extent packs.
    final target = LatLng((b[1] + b[3]) / 2, (b[0] + b[2]) / 2);
    try {
      await controller.moveCamera(
        CameraUpdate.newLatLngZoom(target, _fitZoom(b, size)),
      );
    } catch (_) {
      // A camera move can fail before the map has a size; harmless, the initial
      // camera already framed the same target and zoom.
    }
  }

  /// Snap [target] to the nearest existing feature vertex within [_snapPixels]
  /// (screen space), or return it unchanged when snapping is off or nothing is
  /// close. This is the geometry-editing aid the settings toggle gates: a placed
  /// point, a dragged vertex, or an inserted midpoint lands exactly on a
  /// neighbour's vertex/endpoint instead of a hair off, so connections line up.
  ///
  /// It reuses the tap machinery -- project the target to screen, query the draw
  /// layers in a small box around it (base pack + green creates + amber edits, so
  /// created/moved features are snap targets too), then pick the closest of every
  /// candidate feature's vertices in screen space. The feature under edit is
  /// hidden from those layers ([hiddenIds]), so it's never its own snap target.
  /// Distance is measured in pixels so the tolerance is zoom-independent.
  Future<LatLng> _snap(LatLng target) async {
    if (!widget.snapEnabled) return target;
    final controller = _controller;
    final style = _style;
    if (controller == null || style == null || !_styleReady) return target;
    math.Point<num> screen;
    try {
      screen = await controller.toScreenLocation(target);
    } catch (_) {
      return target;
    }
    final sx = screen.x.toDouble(), sy = screen.y.toDouble();
    final rect = Rect.fromLTRB(
      sx - _snapPixels,
      sy - _snapPixels,
      sx + _snapPixels,
      sy + _snapPixels,
    );
    List<dynamic> hits;
    try {
      hits = await controller.queryRenderedFeaturesInRect(
        rect,
        [
          ...style.queryableLayerIds,
          _createsCircle,
          _createsLine,
          _createsFill,
          _editsFill,
          _editsLine,
          _editsCircle,
          _editsMovedCircle,
        ],
        null,
      );
    } catch (_) {
      return target;
    }
    final createdById = _createdById;
    final seen = <String>{};
    LatLng? best;
    var bestDist = _snapPixels; // only snap within tolerance
    for (final id in _featureIds(hits)) {
      if (!seen.add(id)) continue;
      final geom = (widget.db.featureById(id) ?? createdById[id])?.geometry();
      if (geom == null) continue;
      for (final pos in geom.positions()) {
        // positions() yields [lon, lat]; LatLng is (lat, lon).
        final cand = LatLng(pos[1], pos[0]);
        math.Point<num> cs;
        try {
          cs = await controller.toScreenLocation(cand);
        } catch (_) {
          continue;
        }
        final dx = cs.x.toDouble() - sx, dy = cs.y.toDouble() - sy;
        final d = math.sqrt(dx * dx + dy * dy);
        if (d < bestDist) {
          bestDist = d;
          best = cand;
        }
      }
    }
    return best ?? target;
  }

  Future<void> _onMapClick(math.Point<double> point, LatLng latLng) async {
    // While the geometry editor is open, a map tap does nothing -- interaction is
    // through the vertex/midpoint handles (draggable circles), not selection.
    if (widget.editing) return;
    // In create mode a tap PLACES the new feature's point instead of selecting;
    // the parent owns the draft and re-opens the form. Snap it onto a nearby
    // feature vertex first (a no-op when snapping is off), so a new pole lands
    // exactly on the conductor it sits under.
    if (widget.drawing) {
      widget.onPointPlaced?.call(await _snap(latLng));
      return;
    }
    final controller = _controller;
    final style = _style;
    if (controller == null || style == null) return;
    // Query a padded box around the tap, not the exact pixel: a 2px line or a
    // small circle is almost impossible to hit dead-on, so we give the tap a
    // tolerance radius and take the nearest hit.
    const tol = 16.0;
    final rect = Rect.fromLTRB(
      point.x - tol,
      point.y - tol,
      point.x + tol,
      point.y + tol,
    );
    List<dynamic> hits;
    try {
      hits = await controller.queryRenderedFeaturesInRect(
        rect,
        // Include the green creates layers and the amber edits layers, so an
        // offline-created feature OR a moved feature (hidden from the base layers)
        // stays tappable even though it's absent from / suppressed in the pack's
        // vector tiles.
        [
          ...style.queryableLayerIds,
          _createsCircle,
          _createsLine,
          _createsFill,
          _editsFill,
          _editsLine,
          _editsCircle,
          _editsMovedCircle,
        ],
        null,
      );
    } catch (_) {
      return;
    }
    // Resolve every distinct hit to its record, nearest first, de-duped by id
    // (one feature can span several tiles/layers and come back more than once).
    // A packed feature resolves from the pack; a created one from the create set.
    final createdById = _createdById;
    final records = <FeatureRecord>[];
    final seen = <String>{};
    for (final id in _featureIds(hits)) {
      if (!seen.add(id)) continue;
      final record = widget.db.featureById(id) ?? createdById[id];
      if (record != null) records.add(record);
    }
    if (records.isEmpty) return;
    // In connect mode a tap picks the connection TARGET (nearest first, so a
    // stacked tap can still be disambiguated) instead of selecting; in relate mode
    // it picks the relationship target the same way. Otherwise it selects.
    if (widget.connecting) {
      widget.onConnectTarget?.call(records);
    } else if (widget.relating) {
      widget.onRelateTarget?.call(records);
    } else {
      widget.onFeaturesTapped(records);
    }
  }

  /// Feature ids from the query hits, in hit order (queryRenderedFeaturesInRect
  /// returns nearest first).
  Iterable<String> _featureIds(List<dynamic> hits) sync* {
    for (final h in hits) {
      final props = (h is Map) ? h['properties'] : null;
      if (props is Map && props['id'] != null) {
        yield props['id'].toString();
      }
    }
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
    return Stack(
      children: [
        Positioned.fill(
          child: MapLibreMap(
            styleString: style.json,
            initialCameraPosition: _initialCamera,
            onMapCreated: _onMapCreated,
            onStyleLoadedCallback: _onStyleLoaded,
            onMapClick: _onMapClick,
            // On so the custom compass can track the bearing.
            trackCameraPosition: true,
            // The live blue-dot puck, once the parent has location permission.
            // NORMAL render mode = the standard dot+accuracy ring; tracking mode
            // stays NONE so the dot never hijacks the camera -- panning is free
            // and the my-location button is what recenters.
            myLocationEnabled: widget.showUserLocation,
            myLocationRenderMode: MyLocationRenderMode.normal,
            myLocationTrackingMode: MyLocationTrackingMode.none,
            // Off in favour of our always-visible compass -- the native one hides
            // itself at north, which read as "the north button is missing."
            compassEnabled: false,
          ),
        ),
        // Map controls sit TOP-right of the map body (clear of the app bar above,
        // the Create FAB bottom-right, and the panel/sheet). Hidden while placing
        // a point, when the draw banner owns the top.
        if (!widget.drawing)
          Positioned(
            top: 8,
            right: 8,
            child: SafeArea(
              child: _MapControls(
                bearing: _bearing,
                locating: widget.locating,
                onCompass: _resetNorth,
                onRecenter: _fitToBounds,
                onLocate: widget.onLocate,
              ),
            ),
          ),
      ],
    );
  }
}

/// The map's own control cluster: a compass that resets north and always shows
/// (unlike the native one), a recenter-to-data button, and -- when the parent
/// wires GPS -- a my-location button. A vertical stack of small circular
/// buttons, deliberately top-right so nothing fights the Create FAB or the
/// selection panel.
class _MapControls extends StatelessWidget {
  const _MapControls({
    required this.bearing,
    required this.locating,
    required this.onCompass,
    required this.onRecenter,
    this.onLocate,
  });

  final double bearing;
  final bool locating;
  final VoidCallback onCompass;
  final VoidCallback onRecenter;
  final Future<void> Function()? onLocate;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        _ControlButton(
          tooltip: 'Face north',
          onPressed: onCompass,
          child: Transform.rotate(
            angle: -bearing * math.pi / 180.0,
            child: const Icon(Icons.navigation, color: Color(0xFFFF2D55)),
          ),
        ),
        const SizedBox(height: 8),
        _ControlButton(
          tooltip: 'Recenter on data',
          onPressed: onRecenter,
          child: const Icon(Icons.center_focus_strong),
        ),
        if (onLocate != null) ...[
          const SizedBox(height: 8),
          _ControlButton(
            tooltip: 'My location',
            onPressed: locating ? null : () => onLocate!(),
            child: locating
                ? const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.my_location),
          ),
        ],
      ],
    );
  }
}

/// A small circular map-control button -- a filled surface tile with an icon, so
/// the cluster reads as controls floating over the map, not chrome.
class _ControlButton extends StatelessWidget {
  const _ControlButton({
    required this.tooltip,
    required this.onPressed,
    required this.child,
  });

  final String tooltip;
  final VoidCallback? onPressed;
  final Widget child;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Tooltip(
      message: tooltip,
      child: Material(
        color: scheme.surface,
        elevation: 3,
        shape: const CircleBorder(),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onPressed,
          child: SizedBox(
            width: 44,
            height: 44,
            child: Center(child: child),
          ),
        ),
      ),
    );
  }
}
