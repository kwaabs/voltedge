import 'dart:convert';
import 'dart:io';
import 'dart:math' as math;
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:maplibre_gl/maplibre_gl.dart' show LatLng;
import 'package:path/path.dart' as p;

import '../api/fieldsync_client.dart';
import '../api/models.dart';
import '../attach/attachment_capture.dart';
import '../edit/edit_database.dart';
import '../edit/edit_store.dart';
import '../geo/field_track_recorder.dart';
import '../geo/gnss_sample.dart';
import '../geo/location_service.dart';
import '../geo/wkb.dart';
import '../map/pack_map_view.dart';
import '../pack/network_rules.dart';
import '../pack/pack_database.dart';
import '../pack/pack_store.dart';
import '../providers.dart';
import 'feature_attachments_view.dart';
import 'feature_create_screen.dart';
import 'feature_detail_screen.dart';
import 'feature_edit_screen.dart';
import 'format.dart';
import 'gnss_readout.dart';
import 'map_settings_screen.dart';
import 'scan_tag_screen.dart';

/// The map view of a downloaded pack: renders tiles.mbtiles offline and puts the
/// tapped feature's SQLite record on screen. Responsive by width -- a master /
/// detail split on a tablet, a dismissable bottom sheet on a phone.
///
/// It owns the [PackDatabase] and the selection, then hands both down: the map
/// reports taps, this page decides what's selected, and the same
/// [FeatureDetailView] renders in whichever slot the layout picked. It also owns
/// the offline CREATE flow (create slice): pick a class, place a point on the
/// map, fill the create form, and save to the local outbox via [EditStore].
class PackMapPage extends ConsumerStatefulWidget {
  const PackMapPage({super.key, required this.pack});
  final LocalPack pack;

  @override
  ConsumerState<PackMapPage> createState() => _PackMapPageState();
}

class _PackMapPageState extends ConsumerState<PackMapPage> {
  // Below this width we're a phone (bottom sheet); at or above, a tablet split.
  static const _tabletBreakpoint = 720.0;

  /// Max ops pushed per POST /sync request (Phase 21 Slice 11 scale hardening).
  /// A worker's outbox is chunked into batches of this size so a single request
  /// never holds a server DB connection (or a flaky cellular upload) open over an
  /// unbounded op count. Each batch's acks are persisted before the next goes
  /// out, so a mid-way failure leaves durable progress and the rest queued.
  static const _syncBatchSize = 200;

  PackDatabase? _db;
  EditStore? _edit;
  Object? _error;
  List<double>? _bbox;

  /// The crew member's OWN private drafts over this cell (shared-field-packs S3d),
  /// keyed by feature id, loaded once from the per-user overlay sidecar. A shared base
  /// is published-only, so these unpublished features aren't in the pack -- they are
  /// rendered + selectable like an offline create (folded into PackMapView's `created`
  /// overlay), but EDIT like a packed feature (an update op, never a create: the draft
  /// already exists server-side). Empty for a personal pack. _isDraft gates the actions
  /// that aren't wired for a draft yet (geometry move / connect / relate -> a later slice).
  final Map<String, FeatureRecord> _draftRecords = {};
  FeatureRecord? _selected;
  List<NeighborLink> _connections = const [];
  List<NeighborLink> _relationships = const [];

  // ---- Connect flow (Phase 21 Slice 13b) ------------------------------------
  /// The connection SOURCE feature, armed when the user taps Connect on the panel.
  /// Non-null puts the map in connect mode: the next feature tap is the target.
  FeatureRecord? _connectFrom;

  /// The source endpoint's named terminal CODE (null = no specific terminal),
  /// resolved from a picker when the source class carries terminals.
  String? _connectFromTerminal;

  /// True while a source is armed and the map awaits the target tap.
  bool get _connecting => _connectFrom != null;

  // ---- Relate flow (Phase 21 Slice 14) --------------------------------------
  /// The relationship ORIGIN feature, armed when the user taps Relate on the
  /// panel. Non-null puts the map in relate mode: the next feature tap is the
  /// destination. Unlike connect there is no terminal/kind picker -- the pack
  /// doesn't ship relationship_classes, so the establish is optimistic and the
  /// server auto-resolves the single class for the (origin, destination) pair
  /// (a kind picker is a deferred 14b once relationship_classes ships).
  FeatureRecord? _relateFrom;

  /// True while an origin is armed and the map awaits the destination tap.
  bool get _relating => _relateFrom != null;

  /// When true, the panel shows the inline edit form for [_selected] instead of
  /// the read-only detail -- same slot, so the user never leaves the map.
  bool _editing = false;

  /// True while a POST /sync push is in flight -- disables the sync button and
  /// swaps it for a spinner.
  bool _syncing = false;

  // ---- Create flow (create slice) -------------------------------------------
  // The class being created; non-null means we're in the create flow. While
  // [_creatingForm] is false the map is in "place a point" mode; once a point is
  // confirmed the create form takes the panel slot.
  ClassMeta? _createClass;
  List<SubtypeDef> _createSubtypes = const [];
  LatLng? _draftPoint;

  /// The GNSS survey-quality snapshot (GnssSample.toCaptureJson()) captured the
  /// moment the current draft POINT was dropped at the device's position, via
  /// "place at my location" -- so it records the fix quality where the crew was
  /// standing, not where they later save. Null unless the point came from GPS: a
  /// map tap clears it (a tapped point has no fix behind it), and only a live GNSS
  /// stream (the GPS-quality readout on) can supply one. Consumed by _saveCreate
  /// and cleared when the create flow ends (Phase gnss-quality Slice 3).
  Map<String, dynamic>? _draftGpsCapture;

  /// The traced vertices of a LINE being created (empty for a point class, or
  /// before the first tap). Grows tap by tap while placing; the connector + the
  /// per-vertex markers are drawn by the map. Committed as a LineString on Place.
  final List<LatLng> _draftVertices = [];
  bool _creatingForm = false;

  // ---- GPS track-walking (walk a line/polygon into existence) ---------------
  // While tracking, the live GNSS stream auto-drops vertices into
  // [_draftVertices] as the crew walks, hands-free: a vertex every
  // [_trackMinMetres] moved, or on the [_trackMaxInterval] time cap. "Finish" is
  // the ordinary Place button -- the accumulated vertices commit as the same
  // LineString / Polygon a manual trace would. Only valid during a traced create.

  /// Drop a track vertex once the crew has moved this far from the last one.
  static const _trackMinMetres = 5.0;

  /// ...or when this long has passed since the last vertex (the time cap), so a
  /// slow walk still records -- but only if they've moved at least
  /// [_trackMinMove], so a dead-stationary receiver's jitter can't stack
  /// near-duplicate (zero-length-segment) vertices.
  static const _trackMaxInterval = Duration(seconds: 10);
  static const _trackMinMove = 1.0;

  /// Reject a fix whose timestamp is older than the session start by more than
  /// this. The internal source primes each session with the OS last-known fix --
  /// an old, unchanging position that's identical across sessions -- so without
  /// this guard every tracked feature would begin at that same stale point
  /// instead of a fresh fix unique to where this walk actually starts. A live fix
  /// is stamped ~now, so the window only needs to be wide enough for GPS/clock
  /// skew, not for a minutes-old cached fix.
  static const _trackFreshWindow = Duration(seconds: 10);

  /// True while the GPS is auto-dropping vertices as the crew walks.
  bool _tracking = false;

  /// Keeps the autoDispose [gnssStreamProvider] alive for the whole tracking
  /// session (a plain read would let the receiver power down between rebuilds)
  /// and feeds every fix to [_onTrackSample]. Closed when tracking stops.
  ProviderSubscription<AsyncValue<GnssSample>>? _trackSub;

  /// The last vertex the tracker dropped, and when -- the reference the
  /// distance-primary + time-cap capture rule measures against.
  LatLng? _lastTrackPoint;
  DateTime? _lastTrackAt;

  /// Feeds the field breadcrumb-audit recorder (migration 0178) the live GNSS
  /// stream for THIS open-pack session: it records the worker's movement trail and
  /// stamps every edit with where they were. Distinct from the track-walk tracker
  /// above (which builds feature geometry); this runs the whole session. The
  /// recorder itself is retained by this subscription's closure and by
  /// EditStore.actorLocation. Foreground-only — closed in dispose, so nothing
  /// records in the background.
  ProviderSubscription<AsyncValue<GnssSample>>? _recorderSub;

  /// When the current tracking session began -- the reference for the
  /// [_trackFreshWindow] guard that rejects a stale primed/cached first fix.
  DateTime? _trackStartedAt;

  /// Set when re-opening an existing un-synced create to edit it in place (its
  /// client UUID); null when minting a brand-new feature.
  String? _editingCreateId;

  /// When non-null, the current create is an "attach street light" to this pole:
  /// the new STREET_LIGHT is placed at the pole's own point and, on save, a
  /// spatial_support relationship (pole -> lamp) is queued. Offline we can't run
  /// the server's auto-wire, so the lamp lands un-wired -- the web wires it.
  String? _attachToPoleId;
  String? _attachToPoleClass;

  // ---- Geometry (vertex) editor (Slice 10) ----------------------------------
  // Non-null [_editGeomId] means the geometry editor is open for that packed
  // feature: the map draws its vertices as draggable handles (plus midpoints +
  // connector for a line), and a bottom bar drives save/cancel/delete/undo.
  String? _editGeomId;
  String? _editGeomClass;
  bool _editGeomIsLine = false;
  bool _editGeomIsPolygon = false;

  /// Whether the geometry under edit is traced (a line or polygon ring) rather
  /// than a single point: it gets midpoint handles and a min-vertex delete rule.
  bool get _editGeomTracing => _editGeomIsLine || _editGeomIsPolygon;

  /// The live vertices being edited (lon/lat). One for a point, two+ for a line.
  final List<LatLng> _editVertices = [];

  /// The tapped/selected vertex index (deletable), or null when none is picked.
  int? _selectedVertex;

  /// Undo stack of vertex snapshots -- one entry pushed before each mutation
  /// (drag, insert, delete), popped by Undo.
  final List<List<LatLng>> _geomUndo = [];

  bool get _editingGeom => _editGeomId != null;

  // ---- Attachments (Phase 22 Slice 5) ---------------------------------------
  final AttachmentCapture _capture = AttachmentCapture();

  // ---- Location (Slice 5b) --------------------------------------------------
  final LocationService _location = LocationService();

  /// The last GPS fix, handed to the map to recenter on. Mirrors [_draftPoint]:
  /// the map animates whenever this changes.
  LatLng? _gpsCenter;

  /// True while a GPS fix is in flight -- disables the locate controls + shows a
  /// spinner.
  bool _locating = false;

  /// True once foreground location permission is granted, which turns on the
  /// map's live location puck (the pulsing blue dot). We probe permission once
  /// when the page opens, and flip it on after any successful fix too.
  bool _showUserLocation = false;

  /// Whether the live GNSS quality readout is on (Phase: gnss-quality, Slice 2).
  /// Off by default so the continuous position stream (and its battery cost) only
  /// runs when the crew opens the readout; toggling it unmounts [GnssReadout],
  /// which disposes gnssStreamProvider and powers the receiver down.
  bool _gnssTracking = false;

  /// The selected basemap id (see basemaps.dart), read from settings and handed
  /// to the map. Re-read when the settings page closes so a change applies live.
  late String _basemapId;

  /// Whether edit snapping is on (the map-settings toggle), read from settings
  /// and handed to the map. Re-read when the settings page closes, exactly like
  /// [_basemapId], so flipping the toggle takes effect without reopening the map.
  late bool _snapEnabled;

  /// Whether the selected feature's pack connectivity/relationship graph is drawn
  /// as map edges (the map-settings "Network links" toggle). Read from settings
  /// and re-read when the settings page closes, like [_snapEnabled]. Off suppresses
  /// only the on-selection pack edges; your own unsynced established links still
  /// draw so unsaved work stays visible.
  late bool _showNetworkEdges;

  /// True while the next map tap should place the new feature's geometry (a point
  /// for a point class, or the next vertex for a line class).
  bool get _drawing => _createClass != null && !_creatingForm;

  /// Whether the feature being created is a line (multi-vertex) rather than a
  /// point. Drives the draw banner, placement, and the committed geometry.
  bool get _createIsLine => _createClass?.geometryType == 'line';

  /// Whether the feature being created is a polygon. Like a line it's traced
  /// vertex by vertex, but it needs at least three corners and commits as a
  /// closed-ring Polygon.
  bool get _createIsPolygon => _createClass?.geometryType == 'polygon';

  /// Whether the create is traced vertex by vertex (a line or a polygon) rather
  /// than a single dropped point. Both share the same tap-to-add-vertex flow;
  /// only the minimum count and the committed GeoJSON shape differ.
  bool get _createTracing => _createIsLine || _createIsPolygon;

  /// Minimum vertices before a traced create can be placed: 2 for a line, 3 for
  /// a polygon ring (the closing vertex is added at commit, not traced).
  int get _createMinVertices => _createIsPolygon ? 3 : 2;

  @override
  void initState() {
    super.initState();
    final settings = ref.read(settingsRepositoryProvider);
    _basemapId = settings.basemapId;
    _snapEnabled = settings.snappingEnabled;
    _showNetworkEdges = settings.showNetworkEdges;
    _open();
    _initUserLocation();
  }

  /// Ask for location permission once at open and, if granted, turn on the live
  /// blue-dot puck. Best-effort: a denial just leaves the dot off (the user can
  /// still tap my-location, which re-requests and flips it on on success).
  Future<void> _initUserLocation() async {
    final granted = await _location.ensurePermission();
    if (granted && mounted) setState(() => _showUserLocation = true);
  }

  Future<void> _open() async {
    try {
      final db = PackDatabase.open(widget.pack.dataPath);
      final manifest = db.manifest();
      // The writable edit DB lives beside the read-only pack, keyed to the same
      // pack dir. base_version = the pack snapshot the edits are made against.
      final edit = EditStore.open(
        p.join(widget.pack.dir, 'edits.sqlite'),
        baseVersion: manifest['snapshot_at'],
      )..addListener(_onEditChanged);
      // Load the per-user overlay's drafts (shared-field-packs S3d) into display
      // records once. A missing/corrupt sidecar (a personal pack, or a failed overlay
      // fetch) just leaves the map with no drafts.
      _draftRecords.clear();
      for (final d in PackStore().loadOverlay(widget.pack.dir)?.drafts ??
          const <OverlayDraft>[]) {
        _draftRecords[d.id] = _recordFromDraft(d);
      }
      // Keep the access token fresh (migration 0191: short-lived access + refresh),
      // then refresh the org-wide tracking policy from the server BEFORE deciding
      // whether to record (below) and whether to disclose (further down). A policy an
      // admin changed after this device signed in would otherwise never arrive.
      // Best-effort + online-only: on error/offline we keep the cached flags (the
      // sync heartbeat also refreshes both).
      try {
        await ref.read(fieldsyncClientProvider).ensureFreshAccess();
        await ref.read(fieldsyncClientProvider).refreshTrackingPolicy();
      } catch (_) {}
      if (!mounted) return;
      // Field breadcrumb audit trail (migration 0178): start recording this
      // session's movement trail and stamp every edit with the worker's position.
      // The recorder mints the session id; EditStore.actorLocation reads its latest
      // fix so each outbox op carries where the edit was made.
      //
      // Gated on the org-wide field-tracking switch (settings.fieldTrackingEnabled):
      // when an organisation has turned tracking OFF, record nothing — no recorder,
      // no breadcrumbs, and edit.actorLocation stays null so no op is location-
      // stamped. The server enforces the same, so this is the privacy-respecting
      // (and battery-saving) device half; the disclosure banner reflects the state.
      if (ref.read(settingsRepositoryProvider).fieldTrackingEnabled) {
        final recorder = FieldTrackRecorder(
          sessionId: ref.read(fieldSessionIdProvider),
          onBreadcrumb: edit.recordTrackPoint,
        );
        edit.actorLocation = recorder.actorLocation;
        _startRecorder(recorder);
      }
      setState(() {
        _db = db;
        _edit = edit;
        _bbox = _bboxFromManifest(manifest);
      });
      // Actively disclose on pack open: when the org discloses AND we're actually
      // recording, pop the "you are being recorded" notice once, so a crew is TOLD
      // rather than left to notice the icon. Post-frame so the map is on screen
      // first; `mounted`-guarded so a fast close can't show it on a dead context.
      final settings = ref.read(settingsRepositoryProvider);
      if (settings.fieldTrackingDisclosureEnabled && settings.fieldTrackingEnabled) {
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) _showTrackingDisclosure();
        });
      }
      // Reconcile any ops left in flight from a prior session (captured but their
      // apply outcome unknown) so a returning user sees applied/rejected without a
      // manual sync. Fire-and-forget; offline just leaves them in flight.
      if (edit.inFlightOpIds().isNotEmpty) _pollStatuses();
    } catch (e) {
      setState(() => _error = e);
    }
  }

  /// An edit landed (or was cleared): rebuild so the dirty/created overlays and
  /// the panel's merged values / pending chip refresh.
  void _onEditChanged() {
    if (mounted) setState(() {});
  }

  /// Push all pending changes: feature ops first (so a create lands before its
  /// photos upload against it), then the captured attachments, then reconcile any
  /// in-flight ops' apply outcomes (a push with nothing to send still polls).
  Future<void> _sync() async {
    await _pushOps(_edit?.pendingOps() ?? const []);
    await _syncAttachments(_edit?.pendingAttachments() ?? const []);
    await _uploadBreadcrumbs();
    await _pollStatuses();
  }

  /// Reconcile in-flight ops (captured server-side but not yet known applied) via
  /// the standalone status pull (Phase 23 Slice 2). Used when there's nothing to
  /// push -- a push already piggy-backs the poll. Quietly no-ops offline; the ops
  /// stay in flight to reconcile on the next attempt.
  Future<void> _pollStatuses() async {
    final edit = _edit;
    if (edit == null) return;
    final inFlight = edit.inFlightOpIds();
    if (inFlight.isEmpty) return;
    try {
      final statuses =
          await ref.read(fieldsyncClientProvider).syncStatus(inFlight);
      await edit.recordApplyStatuses(statuses);
    } catch (_) {
      // Offline / transient -- leave the ops in flight to reconcile next time.
    }
  }

  /// Push a specific set of ops to the server (POST /sync) and mark synced
  /// exactly the ones it acked -- a partial success leaves the failed ops queued
  /// to retry, and the idempotent create means a re-push of an already-applied op
  /// is harmless. The overlays/creates themselves stay (the pack predates them),
  /// so the map keeps showing the features; they just lose their "pending"
  /// tint/chip. Called with the full outbox (Sync all) or a single row's op.
  Future<void> _pushOps(List<OutboxOp> ops) async {
    final edit = _edit;
    if (edit == null || _syncing || ops.isEmpty) return;

    setState(() => _syncing = true);
    var acceptedTotal = 0;
    var failedTotal = 0;
    String? firstError;
    // Reconcile earlier in-flight ops on the same round-trip: piggy-back their ids
    // on the first push batch so the server returns their current apply status.
    final inFlight = edit.inFlightOpIds();
    final statuses = <SyncOpStatus>[];
    try {
      final client = ref.read(fieldsyncClientProvider);
      // Push in bounded batches (Slice 11): each batch is a single POST /sync, and
      // its acks are marked synced BEFORE the next batch goes out, so a mid-way
      // network failure leaves everything already pushed durably synced and only
      // the remainder queued. The idempotent server (ON CONFLICT DO NOTHING) makes
      // a later retry of the queued rest a safe no-op for anything that did land.
      //
      // Since Phase 23 the server RECEIVES ops into a durable inbox and acks
      // `received`/`duplicate` (a worker applies them moments later), so an op is
      // marked synced once it's durably captured -- not once it's applied. The
      // eventual apply outcome surfaces via the status pull (Slice 2): poll the
      // pre-existing in-flight set on the first batch only (nothing pushed here is
      // applied yet).
      for (var i = 0; i < ops.length; i += _syncBatchSize) {
        final batch =
            ops.sublist(i, math.min(i + _syncBatchSize, ops.length));
        final resp =
            await client.sync(batch, poll: i == 0 ? inFlight : const []);
        statuses.addAll(resp.statuses);
        final accepted = [
          for (final r in resp.results) if (r.isAccepted) r.opId
        ];
        await edit.markSynced(accepted);
        acceptedTotal += accepted.length;
        failedTotal += resp.results.length - accepted.length;
        firstError ??= resp.results
            .where((r) => !r.isAccepted)
            .map((r) => r.error)
            .firstWhere((e) => e != null && e.isNotEmpty, orElse: () => null);
      }
      await edit.recordApplyStatuses(statuses);
      if (!mounted) return;
      final rejected = statuses.where((s) => s.isRejected).length;
      final attn = rejected > 0
          ? ' · $rejected need${rejected == 1 ? 's' : ''} attention'
          : '';
      final msg = failedTotal == 0
          ? 'Synced $acceptedTotal change${acceptedTotal == 1 ? '' : 's'}.$attn'
          : 'Synced $acceptedTotal, $failedTotal failed'
              '${firstError != null ? ': $firstError' : ''}.$attn';
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(msg)));
    } on FieldsyncException catch (e) {
      if (mounted) {
        // Surface what already landed before the failure -- those batches are
        // synced and won't re-push; the rest stays queued for the next attempt.
        final prefix = acceptedTotal > 0 ? 'Synced $acceptedTotal, then ' : '';
        ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('${prefix}sync failed: ${e.message}')));
      }
    } catch (e) {
      if (mounted) {
        final prefix = acceptedTotal > 0 ? 'Synced $acceptedTotal, then ' : '';
        ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('${prefix}sync failed: $e')));
      }
    } finally {
      if (mounted) setState(() => _syncing = false);
    }
  }

  // ---- Attachments (Phase 22 Slice 5) ---------------------------------------

  /// Upload captured images through the bearer /attachments surface (register ->
  /// presigned PUT -> commit each), marking synced exactly the ones that landed.
  /// A failure leaves that photo queued to retry; the idempotent client-minted id
  /// means a re-upload of an already-committed one is harmless.
  Future<void> _syncAttachments(List<LocalAttachment> atts) async {
    final edit = _edit;
    if (edit == null || _syncing || atts.isEmpty) return;

    setState(() => _syncing = true);
    final uploaded = <String>[];
    Object? firstError;
    try {
      final client = ref.read(fieldsyncClientProvider);
      for (final a in atts) {
        try {
          await client.uploadAttachment(a);
          uploaded.add(a.id);
        } catch (e) {
          firstError ??= e;
        }
      }
      await edit.markAttachmentsSynced(uploaded);
      if (!mounted) return;
      final failed = atts.length - uploaded.length;
      final msg = failed == 0
          ? 'Uploaded ${uploaded.length} photo${uploaded.length == 1 ? '' : 's'}.'
          : 'Uploaded ${uploaded.length}, $failed failed'
              '${firstError != null ? ': $firstError' : ''}.';
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
    } finally {
      if (mounted) setState(() => _syncing = false);
    }
  }

  /// Capture a photo (camera/gallery) for [f] and queue it in the local outbox.
  /// Works fully offline -- the bytes sit in the pack dir until the next sync.
  Future<void> _capturePhoto(FeatureRecord f, CaptureSource source) async {
    final edit = _edit;
    if (edit == null) return;
    try {
      final captured = await _capture.capture(
        source: source,
        destDir: p.join(widget.pack.dir, 'attachments'),
      );
      if (captured == null || !mounted) return;
      await edit.addLocalAttachment(
        featureClass: f.featureClass,
        featureId: f.id,
        filename: captured.filename,
        contentType: captured.contentType,
        byteSize: captured.byteSize,
        localPath: captured.localPath,
      );
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Could not add photo: $e')));
      }
    }
  }

  /// Open a captured image full-screen straight from its on-device file (works
  /// offline -- it's the local copy, not the server original).
  void _openLocalAttachment(LocalAttachment a) {
    Navigator.of(context).push(MaterialPageRoute<void>(
      builder: (_) => AttachmentViewerPage(
        image: Image.file(File(a.localPath)),
        title: a.filename,
        subtitle: formatBytes(a.byteSize),
      ),
    ));
  }

  /// Open a packed image's FULL-resolution original via a presigned URL. Online
  /// only -- the pack carries just the thumbnail; a fetch failure (offline) is
  /// surfaced as a snackbar rather than a broken viewer.
  Future<void> _openPackAttachment(PackAttachment a) async {
    try {
      final url = await ref
          .read(fieldsyncClientProvider)
          .attachmentUrl(a.featureClass, a.featureId, a.id);
      if (url.isEmpty || !mounted) return;
      Navigator.of(context).push(MaterialPageRoute<void>(
        builder: (_) => AttachmentViewerPage(
          image: Image.network(url),
          title: a.filename?.isNotEmpty == true ? a.filename! : shortId(a.id),
          subtitle: formatBytes(a.byteSize),
        ),
      ));
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(
            content: Text('Could not open photo (are you online?): $e')));
      }
    }
  }

  /// The un-synced outbox as display rows: one per pending feature, resolving a
  /// best-effort name (the create's own name, or the packed row's for an edit)
  /// and, for an edit, how many fields changed.
  List<_PendingItem> _pendingItems() {
    final edit = _edit, db = _db;
    if (edit == null || db == null) return const [];
    final items = <_PendingItem>[];
    for (final op in edit.pendingOps()) {
      if (op.kind == 'establish_connection') {
        // A queued edge: title the source, subtitle the target it links to. The
        // connection row (id == op.opId) carries the resolved endpoints.
        final conn = _connectionForOp(op.opId);
        items.add(_PendingItem(
          op: op,
          created: false,
          connection: true,
          name: _displayName(op.featureId),
          connectionTo: conn == null ? null : _displayName(conn.toId),
        ));
      } else if (op.kind == 'establish_relationship') {
        // A queued relationship: title the origin, subtitle the destination. The
        // relationship row (id == op.opId) carries the resolved endpoints.
        final rel = _relationshipForOp(op.opId);
        items.add(_PendingItem(
          op: op,
          created: false,
          relationship: true,
          name: _displayName(op.featureId),
          relationshipTo: rel == null ? null : _displayName(rel.destinationId),
        ));
      } else if (op.kind == 'create') {
        items.add(_PendingItem(
          op: op,
          created: true,
          name: edit.createFor(op.featureId)?.name,
        ));
      } else if (op.kind == 'delete') {
        items.add(_PendingItem(
          op: op,
          created: false,
          deleted: true,
          name: db.featureById(op.featureId)?.name,
        ));
      } else if (EditDatabase.wamOpKinds.contains(op.kind) ||
          EditDatabase.faultOpKinds.contains(op.kind)) {
        // A WAM capture (defect/inspection/work-order) OR a fault capture
        // (raise/status/book-defect) riding the shared outbox -- no map feature to
        // name, just a friendly label. Managed on the Work / Faults screens.
        items.add(_PendingItem(op: op, created: false, wam: true, name: _wamOpTitle(op.kind)));
      } else {
        items.add(_PendingItem(
          op: op,
          created: false,
          name: db.featureById(op.featureId)?.name,
          changedCount: edit.overlayFor(op.featureId)?.attrs.length ?? 0,
        ));
      }
    }
    return items;
  }

  /// The offline connection behind a queued establish_connection op (its id is the
  /// op id), or null if it's already been reconciled away.
  LocalConnection? _connectionForOp(String opId) {
    for (final c in _edit?.connections ?? const <LocalConnection>[]) {
      if (c.id == opId) return c;
    }
    return null;
  }

  /// The offline relationship behind a queued establish_relationship op (its id is
  /// the op id), or null if it's already been reconciled away. The relationship
  /// sibling of [_connectionForOp].
  LocalRelationship? _relationshipForOp(String opId) {
    for (final r in _edit?.relationships ?? const <LocalRelationship>[]) {
      if (r.id == opId) return r;
    }
    return null;
  }

  /// A best-effort display name for a feature id: an offline create's own name,
  /// else the packed row's, else a short id.
  String _displayName(String id) {
    final created = _edit?.createFor(id)?.name;
    if (created != null && created.isNotEmpty) return created;
    final packed = _db?.featureById(id)?.name;
    if (packed != null && packed.isNotEmpty) return packed;
    return shortId(id);
  }

  /// Select a pending feature on the map -- resolve a packed row for an edit, or
  /// the synthetic create record for a new feature.
  void _selectPending(String featureId) {
    final rec = _db?.featureById(featureId) ?? _createdRecordFor(featureId);
    if (rec != null) _select(rec);
  }

  /// The pending-changes sheet: browse what's queued, jump to a feature, discard
  /// an un-synced create, or push the lot. Uses a StatefulBuilder so discarding a
  /// row updates the list in place; syncing closes the sheet and runs [_sync].
  Future<void> _showPending() async {
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      isScrollControlled: true,
      builder: (sheetContext) {
        return StatefulBuilder(
          builder: (sheetContext, setSheet) {
            final theme = Theme.of(sheetContext);
            final items = _pendingItems();
            final List<LocalAttachment> pendingAtts =
                _edit?.pendingAttachments() ?? const [];
            // Quarantined ops (captured but rejected by the apply worker), shown in
            // their own section above the pending list (Phase 23 Slice 2).
            final List<OutboxOp> attention =
                _edit?.attentionOps() ?? const <OutboxOp>[];
            final total = items.length + pendingAtts.length;
            return SafeArea(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Padding(
                    padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
                    child: Text(
                      total == 0
                          ? 'No pending changes'
                          : '$total pending change'
                              '${total == 1 ? '' : 's'}',
                      style: theme.textTheme.titleSmall,
                    ),
                  ),
                  Flexible(
                    child: ListView(
                      shrinkWrap: true,
                      children: [
                        if (attention.isNotEmpty)
                          Padding(
                            padding: const EdgeInsets.fromLTRB(16, 4, 16, 4),
                            child: Text(
                              '${attention.length} change'
                              '${attention.length == 1 ? '' : 's'} need'
                              '${attention.length == 1 ? 's' : ''} attention',
                              style: theme.textTheme.labelLarge?.copyWith(
                                  color: const Color(0xFFFF453A)),
                            ),
                          ),
                        for (final op in attention)
                          ListTile(
                            dense: true,
                            leading: const Icon(Icons.error_outline,
                                color: Color(0xFFFF453A)),
                            title: Text(_displayName(op.featureId)),
                            subtitle: Text(
                              '${op.featureClass} · ${op.applyStatus ?? 'rejected'}'
                              '${op.applyError != null && op.applyError!.isNotEmpty ? ': ${op.applyError}' : ''}',
                              maxLines: 3,
                              overflow: TextOverflow.ellipsis,
                            ),
                            trailing: IconButton(
                              icon: const Icon(Icons.check_circle_outline),
                              tooltip: 'Acknowledge',
                              onPressed: _syncing
                                  ? null
                                  : () async {
                                      await _edit!.dismissAttention(op.opId);
                                      setSheet(() {});
                                    },
                            ),
                            onTap: () {
                              Navigator.of(sheetContext).pop();
                              _selectPending(op.featureId);
                            },
                          ),
                        if (attention.isNotEmpty && total > 0)
                          const Divider(height: 1),
                        for (final a in pendingAtts)
                          ListTile(
                            dense: true,
                            leading: ClipRRect(
                              borderRadius: BorderRadius.circular(4),
                              child: Image.file(
                                File(a.localPath),
                                width: 40,
                                height: 40,
                                fit: BoxFit.cover,
                                errorBuilder: (context, error, stack) =>
                                    const Icon(Icons.image_outlined),
                              ),
                            ),
                            title: Text(a.filename,
                                overflow: TextOverflow.ellipsis),
                            subtitle: Text('${a.featureClass} · photo'),
                            trailing: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                IconButton(
                                  icon: const Icon(Icons.delete_outline),
                                  tooltip: 'Discard',
                                  onPressed: _syncing
                                      ? null
                                      : () async {
                                          await _edit!
                                              .discardLocalAttachment(a.id);
                                          setSheet(() {});
                                        },
                                ),
                                IconButton(
                                  icon: const Icon(Icons.upload_outlined),
                                  tooltip: 'Upload this photo',
                                  onPressed: _syncing
                                      ? null
                                      : () async {
                                          await _syncAttachments([a]);
                                          setSheet(() {});
                                        },
                                ),
                              ],
                            ),
                            onTap: () {
                              Navigator.of(sheetContext).pop();
                              _selectPending(a.featureId);
                            },
                          ),
                        for (final it in items)
                          ListTile(
                            dense: true,
                            leading: Icon(
                              it.wam
                                  ? Icons.assignment_outlined
                                  : it.deleted
                                      ? Icons.delete_forever_outlined
                                      : it.connection
                                          ? Icons.hub_outlined
                                          : it.relationship
                                              ? Icons.account_tree_outlined
                                              : it.created
                                                  ? Icons.add_location_alt_outlined
                                                  : Icons.edit_outlined,
                              color: it.wam
                                  ? Theme.of(sheetContext).colorScheme.primary
                                  : it.deleted
                                      ? const Color(0xFFFF453A)
                                      : it.connection
                                          ? const Color(0xFF30D158)
                                          : it.relationship
                                              ? const Color(0xFFBF5AF2)
                                              : it.created
                                                  ? const Color(0xFF34C759)
                                                  : Colors.amber.shade800,
                            ),
                            title: Text(it.name?.isNotEmpty == true
                                ? it.name!
                                : shortId(it.op.featureId)),
                            subtitle: Text(it.wam
                                ? '${it.op.featureClass.isNotEmpty ? '${it.op.featureClass} · ' : ''}queued for sync'
                                : it.deleted
                                    ? '${it.op.featureClass} · delete'
                                    : it.connection
                                        ? '${it.op.featureClass} · connect to '
                                            '${it.connectionTo ?? 'feature'}'
                                        : it.relationship
                                            ? '${it.op.featureClass} · relate to '
                                                '${it.relationshipTo ?? 'feature'}'
                                            : it.created
                                                ? '${it.op.featureClass} · new feature'
                                                : '${it.op.featureClass} · '
                                                    '${it.changedCount} field'
                                                    '${it.changedCount == 1 ? '' : 's'} changed'),
                            trailing: Row(
                              mainAxisSize: MainAxisSize.min,
                              children: [
                                if (it.connection)
                                  IconButton(
                                    icon: const Icon(Icons.delete_outline),
                                    tooltip: 'Discard',
                                    onPressed: _syncing
                                        ? null
                                        : () async {
                                            await _edit!
                                                .discardConnection(it.op.opId);
                                            setSheet(() {});
                                          },
                                  )
                                else if (it.relationship)
                                  IconButton(
                                    icon: const Icon(Icons.delete_outline),
                                    tooltip: 'Discard',
                                    onPressed: _syncing
                                        ? null
                                        : () async {
                                            await _edit!
                                                .discardRelationship(it.op.opId);
                                            setSheet(() {});
                                          },
                                  )
                                else if (it.created)
                                  IconButton(
                                    icon: const Icon(Icons.delete_outline),
                                    tooltip: 'Discard',
                                    onPressed: _syncing
                                        ? null
                                        : () async {
                                            await _edit!
                                                .discardCreate(it.op.featureId);
                                            setSheet(() {});
                                          },
                                  )
                                else if (it.deleted)
                                  IconButton(
                                    icon: const Icon(
                                        Icons.restore_from_trash_outlined),
                                    tooltip: 'Undo delete',
                                    onPressed: _syncing
                                        ? null
                                        : () async {
                                            await _edit!
                                                .undoDelete(it.op.featureId);
                                            setSheet(() {});
                                          },
                                  ),
                                IconButton(
                                  icon: const Icon(Icons.upload_outlined),
                                  tooltip: 'Push this change',
                                  onPressed: _syncing
                                      ? null
                                      : () async {
                                          await _pushOps([it.op]);
                                          setSheet(() {});
                                        },
                                ),
                              ],
                            ),
                            onTap: it.wam
                                ? null
                                : () {
                                    Navigator.of(sheetContext).pop();
                                    _selectPending(it.op.featureId);
                                  },
                          ),
                      ],
                    ),
                  ),
                  if (total > 0)
                    Padding(
                      padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
                      child: FilledButton.icon(
                        onPressed: _syncing
                            ? null
                            : () {
                                Navigator.of(sheetContext).pop();
                                _sync();
                              },
                        icon: const Icon(Icons.sync),
                        label: Text('Sync $total change'
                            '${total == 1 ? '' : 's'}'),
                      ),
                    ),
                ],
              ),
            );
          },
        );
      },
    );
  }

  /// Push the map settings/about page: what basemap is in use (the pack's own
  /// offline tiles), the snapping toggle, and the loaded / new / modified tallies.
  Future<void> _openSettings() async {
    final db = _db, edit = _edit;
    if (db == null || edit == null) return;
    await Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => MapSettingsScreen(
          pack: widget.pack,
          classCounts: db.classCounts(),
          edit: edit,
        ),
      ),
    );
    // The settings page may have changed the basemap or the snapping toggle;
    // re-read both and apply live (the map hot-swaps its style without re-framing;
    // snapping just flows into the next placed/dragged coordinate).
    if (!mounted) return;
    final settings = ref.read(settingsRepositoryProvider);
    final id = settings.basemapId;
    final snap = settings.snappingEnabled;
    final network = settings.showNetworkEdges;
    if (id != _basemapId ||
        snap != _snapEnabled ||
        network != _showNetworkEdges) {
      setState(() {
        _basemapId = id;
        _snapEnabled = snap;
        _showNetworkEdges = network;
      });
    }
  }

  /// The dirty features to hand the map overlay -- every feature with a pending
  /// attribute edit (dirtyIds) OR a geometry move (movedIds, which may already be
  /// synced but still render locally until a fresh pack). A moved feature carries
  /// its new geometry via override so the overlay draws the moved shape. The
  /// feature under ACTIVE geometry edit is excluded -- it's drawn as handles.
  List<FeatureRecord> _dirtyFeatures() {
    final edit = _edit, db = _db;
    if (edit == null || db == null) return const [];
    final ids = {...edit.dirtyIds, ...edit.movedIds};
    final out = <FeatureRecord>[];
    for (final id in ids) {
      if (id == _editGeomId) continue;
      final r = db.featureById(id);
      if (r != null) out.add(_withOverride(r));
    }
    return out;
  }

  /// The offline-created features as (synthetic) records for the green overlay.
  List<FeatureRecord> _createdFeatures() {
    final edit = _edit;
    if (edit == null) return const [];
    return [for (final c in edit.creates) _recordFromCreate(c)];
  }

  /// Build a display record from a local create. Its geometry is GeoJSON, so it
  /// rides in geometryOverride (there are no packed WKB bytes for a create).
  FeatureRecord _recordFromCreate(LocalCreate c) => FeatureRecord(
        id: c.featureId,
        featureClass: c.featureClass,
        name: c.name,
        status: null,
        subtype: c.subtype,
        territoryId: null,
        geomBytes: null,
        attributes: c.attrs,
        geometryOverride: WkbGeometry.fromGeoJson(c.geometry),
      );

  /// Whether a feature id is one of the crew's overlay drafts (shared-field-packs
  /// S3d) -- not in the base pack, but editable (attributes) as a real server feature.
  bool _isDraft(String id) => _draftRecords.containsKey(id);

  /// Build a display record from an overlay draft. The draft ships OGC WKB (the same
  /// encoding a packed feature uses), so it decodes through geomBytes -- no GeoJSON
  /// override. A missing/corrupt base64 degrades to no geometry (it still lists).
  FeatureRecord _recordFromDraft(OverlayDraft d) {
    Uint8List? bytes;
    if (d.wkbBase64.isNotEmpty) {
      try {
        bytes = base64Decode(d.wkbBase64);
      } catch (_) {
        bytes = null;
      }
    }
    return FeatureRecord(
      id: d.id,
      featureClass: d.featureClass,
      name: d.name,
      status: d.status,
      subtype: d.subtype,
      territoryId: d.territoryId,
      geomBytes: bytes,
      attributes: d.attributes,
    );
  }

  /// The crew's drafts as display records for the overlay (shared-field-packs S3d),
  /// folded into PackMapView's `created` layer so each renders + is tappable. The
  /// record carries the draft's ORIGINAL attributes -- the same contract a packed
  /// feature's record follows (the map hit-test returns this very record as the
  /// selection, and saveAttrEdit computes its delta against these, while the panel +
  /// edit form show the merged values via mergedAttributes). A deleted draft is
  /// dropped; the one under active geometry edit is skipped (drawn as handles).
  List<FeatureRecord> _draftFeatures() {
    final edit = _edit;
    final out = <FeatureRecord>[];
    for (final entry in _draftRecords.entries) {
      final id = entry.key;
      if (id == _editGeomId) continue;
      if (edit != null && edit.isDeleted(id)) continue;
      out.add(entry.value);
    }
    return out;
  }

  /// An order-independent key for an edge between two feature ids, so a local
  /// (unsynced) edge and its authoritative pack twin on the same pair dedupe to
  /// one drawn line regardless of which end each stored as "from".
  static String _edgeKey(String a, String b) =>
      a.compareTo(b) <= 0 ? '$a|$b' : '$b|$a';

  /// The connection edges to draw: every offline-established (unsynced) connection
  /// ALWAYS, plus -- when a feature is selected and the "Network links" setting is
  /// on ([_showNetworkEdges]) -- that feature's authoritative connectivity from the
  /// pack graph, so the network around the selection is visible on the map (not
  /// just listed in the panel). Both feed the same green
  /// overlay (provenance unified); an id-pair [seen] set stops a local edge and
  /// its pack twin from double-drawing. Each endpoint resolves to a representative
  /// point (packed OR offline-created); an unresolvable one is dropped. Suppressed
  /// while editing geometry, so edges don't clutter the vertex handles.
  List<ConnectionEdge> _connectionEdges() {
    final edit = _edit;
    if (edit == null) return const [];
    final out = <ConnectionEdge>[];
    final seen = <String>{};
    void add(String aId, String bId) {
      if (!seen.add(_edgeKey(aId, bId))) return;
      final a = _featurePoint(aId);
      final b = _featurePoint(bId);
      if (a != null && b != null) out.add(ConnectionEdge(from: a, to: b));
    }

    for (final c in edit.connections) {
      add(c.fromId, c.toId);
    }
    final sel = _selected;
    final db = _db;
    if (_showNetworkEdges && sel != null && db != null && !_editingGeom) {
      for (final link in db.connectionsFor(sel.id)) {
        add(sel.id, link.featureId);
      }
    }
    return out;
  }

  /// The relationship edges to draw, the sibling of [_connectionEdges]: every
  /// offline-established (unsynced) relationship ALWAYS, plus -- when the "Network
  /// links" setting is on -- the selected feature's authoritative relationships
  /// from the pack graph, into the same
  /// dashed-purple overlay with the same id-pair dedupe and geometry-edit
  /// suppression. An unresolvable endpoint is dropped.
  List<RelationshipEdge> _relationshipEdges() {
    final edit = _edit;
    if (edit == null) return const [];
    final out = <RelationshipEdge>[];
    final seen = <String>{};
    void add(String aId, String bId) {
      if (!seen.add(_edgeKey(aId, bId))) return;
      final a = _featurePoint(aId);
      final b = _featurePoint(bId);
      if (a != null && b != null) out.add(RelationshipEdge(from: a, to: b));
    }

    for (final r in edit.relationships) {
      add(r.originId, r.destinationId);
    }
    final sel = _selected;
    final db = _db;
    if (_showNetworkEdges && sel != null && db != null && !_editingGeom) {
      for (final link in db.relationshipsFor(sel.id)) {
        add(sel.id, link.featureId);
      }
    }
    return out;
  }

  /// A representative lon/lat for a feature (packed or offline-created): the mean of
  /// its geometry's positions -- exact for a point, the rough centre for a line.
  /// Null when the feature or its geometry can't be resolved.
  LatLng? _featurePoint(String id) {
    final rec = _db?.featureById(id) ?? _createdRecordFor(id);
    final g = rec?.geometry();
    if (g == null) return null;
    final ps = g.positions();
    if (ps.isEmpty) return null;
    double sx = 0, sy = 0;
    for (final p in ps) {
      sx += p[0];
      sy += p[1];
    }
    return LatLng(sy / ps.length, sx / ps.length);
  }

  /// Enter inline edit mode for the selected feature. A packed feature swaps to
  /// the attribute editor; an offline-created one re-opens the create form
  /// pre-filled, so editing it before sync mutates the create in place.
  void _startEdit() {
    final sel = _selected;
    if (_db == null || _edit == null || sel == null) return;
    // Only an UN-synced create re-opens the create form (mutate it in place,
    // LWW). Once synced the feature exists server-side, so editing it must go
    // through the attribute editor -> an update_attrs op keyed by the same id.
    if (_edit!.isCreated(sel.id) && _edit!.isPending(sel.id)) {
      final c = _edit!.createFor(sel.id);
      if (c == null) return;
      setState(() {
        _createClass = _classMetaFor(sel.featureClass);
        _createSubtypes = _db!.subtypesFor(sel.featureClass);
        // Re-seed the draft geometry so a Save rebuilds the same shape (the
        // create form itself doesn't edit geometry -- only attributes/subtype).
        // A polygon re-seeds its distinct corners into the same trace list a
        // line uses; the closing vertex is re-added when it commits.
        _draftPoint = _pointOf(c.geometry);
        _draftVertices
          ..clear()
          ..addAll(c.geometry['type'] == 'Polygon'
              ? _ringOf(c.geometry)
              : _lineOf(c.geometry));
        _creatingForm = true;
        _editingCreateId = sel.id;
        // Re-opening an existing create seeds the point from its stored geometry,
        // not a fresh GPS fix -- so no quality is captured for a re-save.
        _draftGpsCapture = null;
        _selected = null; // the form takes the panel slot
        _editing = false;
        // Re-editing an existing create is not an attach -- the relationship (if
        // any) was queued when it was first created; don't re-queue it on save.
        _attachToPoleId = null;
        _attachToPoleClass = null;
      });
      return;
    }
    setState(() => _editing = true);
  }

  /// Persist the form's values as a local overlay + outbox op (EditStore) and
  /// return to the read-only panel. The store listener refreshes the map/panel.
  Future<void> _saveEdit(FeatureRecord f, Map<String, dynamic> edited) async {
    await _edit!.saveAttrEdit(
      featureId: f.id,
      featureClass: f.featureClass,
      packAttrs: f.attributes,
      editedAttrs: edited,
    );
    if (mounted) setState(() => _editing = false);
  }

  /// The inline edit form for [f], rendered in the panel slot. Coded fields carry
  /// their domain's pick list (pack schema v2+) so they render as dropdowns.
  Widget _editForm(FeatureRecord f, EdgeInsetsGeometry padding) {
    final fields = _db!.fieldsFor(f.featureClass);
    final domains = _domainsFor(fields);
    return FeatureEditView(
      feature: f,
      fields: fields,
      domainValues: domains,
      currentValues: _edit!.mergedAttributes(f.id, f.attributes),
      onSave: (edited) => _saveEdit(f, edited),
      onCancel: () => setState(() => _editing = false),
      padding: padding,
    );
  }

  /// The coded-domain pick lists needed by a class's fields, keyed by domain id.
  Map<String, List<DomainValue>> _domainsFor(List<FieldDef> fields) {
    final domains = <String, List<DomainValue>>{};
    for (final fd in fields) {
      final id = fd.domainId;
      if (id == null || domains.containsKey(id)) continue;
      final values = _db!.domainValuesFor(id);
      if (values.isNotEmpty) domains[id] = values;
    }
    return domains;
  }

  // ---- Create flow ----------------------------------------------------------

  /// Start creating: pick a class, then drop into "place a point" mode.
  Future<void> _startCreate() async {
    final db = _db;
    if (db == null) return;
    final cls = await _pickCreateClass(db);
    if (cls == null || !mounted) return;
    setState(() {
      _createClass = cls;
      _createSubtypes = db.subtypesFor(cls.code);
      _draftPoint = null;
      _draftGpsCapture = null;
      _draftVertices.clear();
      _creatingForm = false;
      _editingCreateId = null;
      _selected = null;
      _editing = false;
      _connections = const [];
      _relationships = const [];
      _connectFrom = null;
      _connectFromTerminal = null;
      _relateFrom = null;
      _attachToPoleId = null;
      _attachToPoleClass = null;
    });
  }

  /// Attach a pole-mounted street light to [pole]. Street lights are geometry-
  /// optional on the server (it auto-wires them to the pole on create), but that
  /// compositing only runs server-side, so offline we place the lamp at the
  /// pole's own point and queue a spatial_support relationship (pole -> lamp).
  /// The lamp lands un-wired; the web runs the electrical attach. We reuse the
  /// ordinary create form (class STREET_LIGHT) pre-seeded with the pole's point,
  /// so the crew just fills attributes and saves -- no drawing step.
  void _startAttachStreetLight(FeatureRecord pole) {
    final db = _db;
    if (db == null) return;
    final meta = _classMetaFor('STREET_LIGHT');
    if (meta == null) {
      _snack("Street lights aren't in this field pack.");
      return;
    }
    final at = _featurePoint(pole.id);
    if (at == null) {
      _snack('This pole has no location to mount a light on.');
      return;
    }
    setState(() {
      _createClass = meta;
      _createSubtypes = db.subtypesFor(meta.code);
      _draftPoint = at;
      _draftGpsCapture = null;
      _draftVertices.clear();
      _creatingForm = true; // straight to the form; the geometry is the pole's
      _editingCreateId = null;
      _attachToPoleId = pole.id;
      _attachToPoleClass = pole.featureClass;
      _selected = null;
      _editing = false;
      _connections = const [];
      _relationships = const [];
      _connectFrom = null;
      _connectFromTerminal = null;
      _relateFrom = null;
    });
  }

  /// Class picker for a new feature. Point, line, and polygon classes are all
  /// selectable (drop a point, trace a line, or trace a polygon ring); anything
  /// with an unexpected geometry type is listed disabled so it's clear it's not
  /// yet handled rather than missing.
  Future<ClassMeta?> _pickCreateClass(PackDatabase db) {
    const drawableTypes = {'point', 'line', 'polygon'};
    final metas = db.classMetas();
    final drawable = [
      for (final m in metas)
        if (drawableTypes.contains(m.geometryType)) m,
    ];
    final others = [
      for (final m in metas)
        if (!drawableTypes.contains(m.geometryType)) m,
    ];
    return showModalBottomSheet<ClassMeta>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
              child: Text('Create a feature',
                  style: Theme.of(context).textTheme.titleSmall),
            ),
            if (drawable.isEmpty)
              const Padding(
                padding: EdgeInsets.fromLTRB(16, 8, 16, 16),
                child: Text('This pack has no classes to create yet.'),
              ),
            Flexible(
              child: ListView(
                shrinkWrap: true,
                children: [
                  for (final m in drawable)
                    ListTile(
                      dense: true,
                      leading: Icon(switch (m.geometryType) {
                        'line' => Icons.timeline,
                        'polygon' => Icons.pentagon_outlined,
                        _ => Icons.place_outlined,
                      }),
                      title: Text(m.name?.isNotEmpty == true ? m.name! : m.code),
                      subtitle: Text(m.code),
                      onTap: () => Navigator.of(context).pop(m),
                    ),
                  for (final m in others)
                    ListTile(
                      dense: true,
                      enabled: false,
                      leading: const Icon(Icons.help_outline),
                      title: Text(m.name?.isNotEmpty == true ? m.name! : m.code),
                      subtitle: Text('${m.geometryType} — not supported'),
                    ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  /// The map reported a tap while placing. For a point class it sets (or moves)
  /// the single draft point; for a line class it appends another vertex to the
  /// trace.
  void _onPointPlaced(LatLng latLng) {
    setState(() {
      if (_createTracing) {
        _draftVertices.add(latLng);
      } else {
        _draftPoint = latLng;
        // A tapped point isn't a GPS placement -- drop any quality captured from a
        // prior "place at my location" so it can't attach to this point.
        _draftGpsCapture = null;
      }
    });
  }

  /// Remove the last-placed traced vertex (the draw banner's Undo). No-op once
  /// the trace is empty.
  void _undoVertex() {
    if (_draftVertices.isEmpty) return;
    setState(() => _draftVertices.removeLast());
  }

  /// Start walking the line/polygon: the live GPS auto-drops a vertex into
  /// [_draftVertices] as the crew moves, so a feeder run or a site boundary can
  /// be captured hands-free. Only valid mid-trace (a line/polygon create); a
  /// no-op otherwise. Resumable -- vertices already traced are kept, and the
  /// distance rule continues from the last one.
  void _startTracking() {
    if (_tracking || !_createTracing) return;
    _lastTrackPoint = _draftVertices.isNotEmpty ? _draftVertices.last : null;
    _lastTrackAt = null;
    _trackStartedAt = DateTime.now();
    // listenManual keeps the autoDispose GNSS stream alive for the session and
    // powers the receiver on even with the GPS-quality card closed, so tracking
    // is truly hands-free. It uses whichever source is selected (phone chip or an
    // external RTK receiver), so a corrections-fed receiver walks in RTK-grade.
    // NO fireImmediately: that would synchronously replay the provider's cached
    // sample (stale if the stream was already alive) as vertex 1 -- so each new
    // feature would start at the previous walk's point. We wait for the next live
    // fix instead, and the freshness guard in _onTrackSample rejects the stale
    // last-known prime too.
    _trackSub = ref.listenManual<AsyncValue<GnssSample>>(
      gnssStreamProvider,
      (prev, next) {
        final sample = next.valueOrNull;
        if (sample != null) _onTrackSample(sample);
      },
    );
    // Show the live puck so the crew sees themselves against the growing trail.
    setState(() {
      _tracking = true;
      _showUserLocation = true;
    });
  }

  /// Stop auto-dropping vertices and release the GNSS stream. The vertices
  /// captured so far stay in the draft, so the crew can resume, tap-add, undo, or
  /// Place. [_teardownTracking] does the release without setState for callers
  /// (cancel/save/finish) that already rebuild.
  void _stopTracking() {
    _teardownTracking();
    if (_tracking) setState(() => _tracking = false);
  }

  void _teardownTracking() {
    _trackSub?.close();
    _trackSub = null;
    _lastTrackPoint = null;
    _lastTrackAt = null;
    _trackStartedAt = null;
  }

  // ---- Field breadcrumb audit trail (migration 0178) ------------------------

  /// Subscribe the breadcrumb recorder to the live GNSS stream for the whole
  /// open-pack session (foreground-only — the subscription is closed in dispose,
  /// so nothing records in the background). listenManual (not a widget watch) keeps
  /// the autoDispose stream alive independent of rebuilds; no fireImmediately, so
  /// the first breadcrumb is a fresh live fix, not a stale cached one. Separate from
  /// the track-walk subscription: this one runs the whole time, that one only while
  /// walking a geometry.
  void _startRecorder(FieldTrackRecorder recorder) {
    _recorderSub?.close();
    _recorderSub = ref.listenManual<AsyncValue<GnssSample>>(
      gnssStreamProvider,
      (prev, next) {
        final sample = next.valueOrNull;
        if (sample != null) recorder.onSample(sample);
      },
    );
  }

  /// Upload the session's pending breadcrumbs to POST /field-tracks/breadcrumbs, in
  /// bounded batches, marking each acked batch uploaded before the next. Best-effort:
  /// offline / a transient failure just leaves them queued for the next sync (the
  /// trail is loss-tolerant and idempotent by point id). Called from [_sync] after
  /// the edit ops, so the located-action waypoints and the trail land together.
  Future<void> _uploadBreadcrumbs() async {
    final edit = _edit;
    if (edit == null) return;
    try {
      final client = ref.read(fieldsyncClientProvider);
      while (true) {
        final batch = edit.pendingTrackPoints(limit: _syncBatchSize);
        if (batch.isEmpty) break;
        await client.uploadBreadcrumbs(batch);
        edit.markTrackPointsUploaded([for (final p in batch) p.id]);
      }
    } catch (_) {
      // Offline / transient -- leave the breadcrumbs queued to retry next sync.
    }
  }

  /// A live fix arrived while tracking: append it as a vertex when the crew has
  /// moved at least [_trackMinMetres] from the last one, or the [_trackMaxInterval]
  /// time cap elapsed after a small ([_trackMinMove]) move. The very first fix of
  /// a session drops immediately. Distance is a cheap equirectangular metres
  /// approximation -- more than accurate enough at a walking-step scale.
  void _onTrackSample(GnssSample s) {
    // Reject a stale fix (the internal source's last-known prime, or a cached
    // sample from a prior session): only fixes from at/after this session's start
    // become vertices, so each feature's first point is genuinely its own. The
    // year-2000 floor ignores devices that report a bogus/zero fix timestamp --
    // there we can't tell stale from fresh, so we fall back to accepting (the
    // dropped fireImmediately already stops the worst carry-over).
    final startedAt = _trackStartedAt;
    if (startedAt != null &&
        s.timestamp.isAfter(DateTime(2000)) &&
        s.timestamp.isBefore(startedAt.subtract(_trackFreshWindow))) {
      return;
    }
    final here = LatLng(s.latitude, s.longitude);
    final now = DateTime.now();
    final last = _lastTrackPoint;
    if (last != null) {
      final moved = _metresBetween(last, here);
      final elapsed =
          _lastTrackAt == null ? Duration.zero : now.difference(_lastTrackAt!);
      final farEnough = moved >= _trackMinMetres;
      final timeCap = elapsed >= _trackMaxInterval && moved >= _trackMinMove;
      if (!farEnough && !timeCap) return;
    }
    _lastTrackPoint = here;
    _lastTrackAt = now;
    setState(() => _draftVertices.add(here));
  }

  /// Approximate distance in metres between two lon/lat points (equirectangular
  /// projection). Sub-metre-accurate at the few-metre scale the capture rule
  /// gates on, and far cheaper per fix than a full haversine.
  static double _metresBetween(LatLng a, LatLng b) {
    const earthRadius = 6371000.0;
    final lat1 = a.latitude * math.pi / 180;
    final lat2 = b.latitude * math.pi / 180;
    final dLat = lat2 - lat1;
    final dLon = (b.longitude - a.longitude) * math.pi / 180;
    final x = dLon * math.cos((lat1 + lat2) / 2);
    return earthRadius * math.sqrt(dLat * dLat + x * x);
  }

  /// The map's my-location control: get a GPS fix and recenter on it. Returns a
  /// fix (via a snackbar on failure) but doesn't place anything.
  Future<void> _locateMe() async {
    final fix = await _fetchFix();
    if (fix != null && mounted) setState(() => _gpsCenter = fix);
  }

  /// Create-flow "use my location": drop the draft point at the GPS fix and
  /// recenter there, so the user can place a feature where they're standing
  /// without hunting for it on the map.
  Future<void> _placeAtMyLocation() async {
    final fix = await _fetchFix();
    if (fix != null && mounted) {
      // Stamp the live GNSS quality onto a POINT dropped here (Phase gnss-quality
      // Slice 3). Only a running GNSS stream (the GPS-quality readout on) has a
      // sample; with it off this is null and the placement simply carries no
      // quality record -- the same graceful degradation as an iPhone with no DOP.
      // A traced vertex isn't stamped: a line's many fixes have no single quality.
      final sample = _createTracing
          ? null
          : ref.read(gnssStreamProvider).valueOrNull?.toCaptureJson();
      setState(() {
        if (_createTracing) {
          _draftVertices.add(fix);
        } else {
          _draftPoint = fix;
          _draftGpsCapture = sample;
        }
        _gpsCenter = fix;
      });
    }
  }

  /// Shared GPS fetch: flips the locating flag, resolves a fix, and surfaces any
  /// failure reason as a snackbar. Returns null when no fix was obtained.
  Future<LatLng?> _fetchFix() async {
    if (_locating) return null;
    setState(() => _locating = true);
    try {
      final result = await _location.current();
      if (!result.ok && mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text(result.error ?? 'Could not get a location.')),
        );
      }
      // A fix means permission is granted -- make sure the live puck is on.
      if (result.ok && !_showUserLocation && mounted) {
        setState(() => _showUserLocation = true);
      }
      return result.position;
    } finally {
      if (mounted) setState(() => _locating = false);
    }
  }

  /// Confirm the placed geometry and open the create form. A line needs at least
  /// two vertices, a polygon at least three; a point needs a placed draft point.
  void _confirmPlacement() {
    if (_createTracing) {
      if (_draftVertices.length < _createMinVertices) return;
    } else if (_draftPoint == null) {
      return;
    }
    // Finishing a walked trace: release the GPS before the form takes the panel.
    _teardownTracking();
    setState(() {
      _tracking = false;
      _creatingForm = true;
    });
  }

  /// Leave the create flow entirely (from any stage) without saving.
  void _cancelCreate() {
    _teardownTracking();
    setState(() {
      _createClass = null;
      _createSubtypes = const [];
      _draftPoint = null;
      _draftGpsCapture = null;
      _draftVertices.clear();
      _creatingForm = false;
      _tracking = false;
      _editingCreateId = null;
      _attachToPoleId = null;
      _attachToPoleClass = null;
    });
  }

  /// Persist the create (new or in-place edit) to the local outbox and select it.
  Future<void> _saveCreate({
    int? subtype,
    String? name,
    required Map<String, dynamic> attributes,
  }) async {
    final cls = _createClass;
    if (cls == null) return;
    final Map<String, dynamic> geometry;
    if (_createTracing) {
      if (_draftVertices.length < _createMinVertices) return;
      geometry = _createIsPolygon
          ? {
              'type': 'Polygon',
              'coordinates': [_closedRing(_draftVertices)],
            }
          : {
              'type': 'LineString',
              'coordinates': [
                for (final v in _draftVertices) [v.longitude, v.latitude],
              ],
            };
    } else {
      final pt = _draftPoint;
      if (pt == null) return;
      geometry = {
        'type': 'Point',
        'coordinates': [pt.longitude, pt.latitude],
      };
    }
    final id = await _edit!.createFeature(
      featureClass: cls.code,
      subtype: subtype,
      geometry: geometry,
      attributes: attributes,
      name: name,
      featureId: _editingCreateId,
      // Only a point dropped via "place at my location" carries a capture; it's
      // null for a map-tap point or any traced line/polygon.
      gpsCapture: _createTracing ? null : _draftGpsCapture,
    );
    // Attach flow: queue the pole -> lamp relationship so the carry is recorded
    // offline. Kind comes from the pack's relationship_classes (a single
    // spatial_support class for POLE -> STREET_LIGHT); null lets the server
    // resolve it if the pack predates relationship_classes.
    final poleId = _attachToPoleId;
    final poleClass = _attachToPoleClass;
    if (poleId != null && poleClass != null) {
      final classes = _db?.relationshipClassesFor(poleClass, cls.code) ??
          const <RelationshipClassDef>[];
      await _edit!.createRelationship(
        originClass: poleClass,
        originId: poleId,
        destinationClass: cls.code,
        destinationId: id,
        kind: classes.length == 1 ? classes.first.kind : null,
      );
    }
    if (!mounted) return;
    final wasAttach = poleId != null;
    final rec = _createdRecordFor(id);
    _teardownTracking();
    setState(() {
      _createClass = null;
      _createSubtypes = const [];
      _draftPoint = null;
      _draftGpsCapture = null;
      _draftVertices.clear();
      _creatingForm = false;
      _tracking = false;
      _editingCreateId = null;
      _attachToPoleId = null;
      _attachToPoleClass = null;
    });
    if (rec != null) _select(rec);
    // Be honest about the limit: the pole->lamp carry is recorded, but the LV
    // auto-wire is a server-side composite that only runs through the web attach,
    // not generic sync -- so the lamp syncs un-wired and must be wired on the web.
    if (wasAttach) {
      _snack('Street light added to the pole. It still needs wiring to the LV '
          'on the web after you sync.');
    }
  }

  FeatureRecord? _createdRecordFor(String id) {
    final c = _edit?.createFor(id);
    return c == null ? null : _recordFromCreate(c);
  }

  /// Discard the selected offline create (nothing to fall back to server-side).
  Future<void> _discardSelectedCreate() async {
    final sel = _selected;
    if (sel == null || _edit == null) return;
    await _edit!.discardCreate(sel.id);
    _clearSelection();
  }

  /// Delete a PACKED feature offline (Phase 21 Slice 15): confirm, queue a 'delete'
  /// op (soft-deletes on the server at sync), and clear the selection so the map
  /// drops it -- the tombstone then hides it from the base layers. Undo stays
  /// available from the pending-changes sheet (or by re-selecting) until it syncs.
  Future<void> _deleteSelectedFeature(FeatureRecord f) async {
    if (_edit == null) return;
    final name = f.name?.isNotEmpty == true ? f.name! : shortId(f.id);
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Delete feature?'),
        content: Text('$name will be marked deleted and soft-deleted on the '
            'server when you sync. You can undo before then.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    await _edit!.deleteFeature(featureId: f.id, featureClass: f.featureClass);
    _clearSelection();
  }

  /// Undo the selected feature's offline delete (only offered on an un-synced
  /// tombstone). The feature reappears from the base pack; selection is kept so the
  /// panel refreshes in place.
  Future<void> _undoSelectedDelete() async {
    final sel = _selected;
    if (sel == null || _edit == null) return;
    await _edit!.undoDelete(sel.id);
    if (mounted) setState(() {});
  }

  ClassMeta? _classMetaFor(String code) {
    for (final m in _db!.classMetas()) {
      if (m.code == code) return m;
    }
    return null;
  }

  /// Extract a LatLng from a GeoJSON Point geometry (lon,lat order), else null.
  LatLng? _pointOf(Map<String, dynamic> geom) {
    if (geom['type'] != 'Point') return null;
    final c = geom['coordinates'];
    if (c is List && c.length >= 2 && c[0] is num && c[1] is num) {
      return LatLng((c[1] as num).toDouble(), (c[0] as num).toDouble());
    }
    return null;
  }

  /// Extract the vertices of a GeoJSON LineString geometry (lon,lat order), else
  /// an empty list.
  List<LatLng> _lineOf(Map<String, dynamic> geom) {
    if (geom['type'] != 'LineString') return const [];
    final coords = geom['coordinates'];
    if (coords is! List) return const [];
    final out = <LatLng>[];
    for (final c in coords) {
      if (c is List && c.length >= 2 && c[0] is num && c[1] is num) {
        out.add(LatLng((c[1] as num).toDouble(), (c[0] as num).toDouble()));
      }
    }
    return out;
  }

  /// Extract the DISTINCT corners of a GeoJSON Polygon's outer ring (lon,lat
  /// order), dropping the closing vertex (a ring repeats its first point as its
  /// last) so the editor works with N handles for N corners. Inner rings (holes)
  /// aren't editable on device and are ignored. Else an empty list.
  List<LatLng> _ringOf(Map<String, dynamic> geom) {
    if (geom['type'] != 'Polygon') return const [];
    final rings = geom['coordinates'];
    if (rings is! List || rings.isEmpty || rings.first is! List) return const [];
    return _cornersOf(rings.first as List);
  }

  /// Convert a raw ring (a List of [lon,lat] pairs, first==last) to its distinct
  /// corners as LatLng, dropping the trailing closing duplicate if present.
  List<LatLng> _cornersOf(List ring) {
    final out = <LatLng>[];
    for (final c in ring) {
      if (c is List && c.length >= 2 && c[0] is num && c[1] is num) {
        out.add(LatLng((c[1] as num).toDouble(), (c[0] as num).toDouble()));
      }
    }
    if (out.length >= 2 &&
        out.first.latitude == out.last.latitude &&
        out.first.longitude == out.last.longitude) {
      out.removeLast();
    }
    return out;
  }

  /// Build a closed GeoJSON ring ([lon,lat] pairs) from distinct corners: the
  /// vertices in order, then the first vertex repeated to close it.
  List<List<double>> _closedRing(List<LatLng> verts) {
    return [
      for (final v in verts) [v.longitude, v.latitude],
      [verts.first.longitude, verts.first.latitude],
    ];
  }

  // ---- Geometry (vertex) editor (Slice 10) ----------------------------------

  /// Whether [f] can have its geometry edited: a PACKED feature (a create edits
  /// its geometry through the create form) with a Point, LineString, or Polygon
  /// geometry.
  bool _canEditGeometry(FeatureRecord f) {
    if (_edit?.isCreated(f.id) ?? false) return false;
    final g = f.geometry();
    return g != null &&
        (g.type == 'Point' ||
            g.type == 'LineString' ||
            g.type == 'Polygon');
  }

  /// Enter the geometry editor for the selected feature: seed the vertices from
  /// the current local move (if any) else the packed geometry, and hand them to
  /// the map as draggable handles. The detail panel gives way to the geom-edit
  /// bar; [_selected] is kept so save/cancel can restore the panel.
  void _startGeomEdit() {
    final f = _selected;
    if (f == null || _edit == null || !_canEditGeometry(f)) return;
    final moved = _edit!.geomEditFor(f.id);
    final List<LatLng> verts;
    final bool isLine;
    final bool isPolygon;
    if (moved != null) {
      final t = moved['type'];
      isLine = t == 'LineString';
      isPolygon = t == 'Polygon';
      if (isPolygon) {
        verts = _ringOf(moved);
      } else if (isLine) {
        verts = _lineOf(moved);
      } else {
        final p = _pointOf(moved);
        verts = p == null ? const <LatLng>[] : [p];
      }
    } else {
      final g = f.geometry()!;
      isLine = g.type == 'LineString';
      isPolygon = g.type == 'Polygon';
      verts = _verticesOf(g);
    }
    if (verts.isEmpty) return;
    setState(() {
      _editGeomId = f.id;
      _editGeomClass = f.featureClass;
      _editGeomIsLine = isLine;
      _editGeomIsPolygon = isPolygon;
      _editVertices
        ..clear()
        ..addAll(verts);
      _selectedVertex = null;
      _geomUndo.clear();
      _editing = false;
    });
  }

  /// Vertices of a decoded geometry as LatLng (lon/lat order in WKB/GeoJSON).
  List<LatLng> _verticesOf(WkbGeometry g) {
    final c = g.coordinates;
    if (g.type == 'Point') {
      if (c is List && c.length >= 2 && c[0] is num && c[1] is num) {
        return [LatLng((c[1] as num).toDouble(), (c[0] as num).toDouble())];
      }
      return const [];
    }
    if (g.type == 'LineString' && c is List) {
      final out = <LatLng>[];
      for (final pt in c) {
        if (pt is List && pt.length >= 2 && pt[0] is num && pt[1] is num) {
          out.add(LatLng((pt[1] as num).toDouble(), (pt[0] as num).toDouble()));
        }
      }
      return out;
    }
    // Polygon: the outer ring's distinct corners (the closing duplicate is
    // dropped so each handle is one editable corner).
    if (g.type == 'Polygon' &&
        c is List &&
        c.isNotEmpty &&
        c.first is List) {
      return _cornersOf(c.first as List);
    }
    return const [];
  }

  /// Snapshot the current vertices for Undo, before a mutation.
  void _pushGeomUndo() => _geomUndo.add(List<LatLng>.of(_editVertices));

  /// A vertex handle finished dragging: commit its new position.
  void _onVertexDragged(int index, LatLng pos) {
    if (index < 0 || index >= _editVertices.length) return;
    setState(() {
      _pushGeomUndo();
      _editVertices[index] = pos;
    });
  }

  /// A vertex was tapped: select it (or deselect if it was already selected).
  void _onVertexTapped(int index) {
    setState(() => _selectedVertex = _selectedVertex == index ? null : index);
  }

  /// A midpoint on segment [segIndex] was tapped: insert a new vertex there and
  /// select it (so it can be dragged or deleted immediately). A polygon has one
  /// extra segment closing the ring (its last vertex back to the first, at
  /// segIndex == length-1); inserting at length appends the corner between them.
  void _onMidpointInserted(int segIndex, LatLng pos) {
    final maxSeg =
        _editGeomIsPolygon ? _editVertices.length - 1 : _editVertices.length - 2;
    if (segIndex < 0 || segIndex > maxSeg) return;
    setState(() {
      _pushGeomUndo();
      _editVertices.insert(segIndex + 1, pos);
      _selectedVertex = segIndex + 1;
    });
  }

  /// Delete the selected vertex. A line must keep at least two vertices and a
  /// polygon ring at least three; a point can't lose its only vertex, so delete
  /// is a no-op there.
  void _deleteSelectedVertex() {
    final i = _selectedVertex;
    final min = _editGeomIsPolygon ? 3 : 2;
    if (i == null || !_editGeomTracing || _editVertices.length <= min) return;
    setState(() {
      _pushGeomUndo();
      _editVertices.removeAt(i);
      _selectedVertex = null;
    });
  }

  /// Undo the last vertex mutation (drag/insert/delete).
  void _undoGeom() {
    if (_geomUndo.isEmpty) return;
    setState(() {
      _editVertices
        ..clear()
        ..addAll(_geomUndo.removeLast());
      _selectedVertex = null;
    });
  }

  /// Leave the geometry editor without saving, restoring the detail panel.
  void _cancelGeomEdit() {
    setState(() {
      _editGeomId = null;
      _editGeomClass = null;
      _editGeomIsPolygon = false;
      _editVertices.clear();
      _selectedVertex = null;
      _geomUndo.clear();
    });
  }

  /// Persist the moved geometry as a local overlay + outbox op and return to the
  /// detail panel showing the feature in its new position.
  Future<void> _saveGeomEdit() async {
    final id = _editGeomId, cls = _editGeomClass, edit = _edit;
    if (id == null || cls == null || edit == null) return;
    if (_editGeomIsPolygon && _editVertices.length < 3) return;
    if (_editGeomIsLine && _editVertices.length < 2) return;
    if (!_editGeomTracing && _editVertices.isEmpty) return;
    final Map<String, dynamic> geometry;
    if (_editGeomIsPolygon) {
      geometry = {
        'type': 'Polygon',
        'coordinates': [_closedRing(_editVertices)],
      };
    } else if (_editGeomIsLine) {
      geometry = {
        'type': 'LineString',
        'coordinates': [
          for (final v in _editVertices) [v.longitude, v.latitude],
        ],
      };
    } else {
      geometry = {
        'type': 'Point',
        'coordinates': [
          _editVertices.first.longitude,
          _editVertices.first.latitude,
        ],
      };
    }
    await edit.saveGeometryEdit(
      featureId: id,
      featureClass: cls,
      geometry: geometry,
    );
    if (!mounted) return;
    setState(() {
      _editGeomId = null;
      _editGeomClass = null;
      _editGeomIsPolygon = false;
      _editVertices.clear();
      _selectedVertex = null;
      _geomUndo.clear();
    });
    // Re-select the feature so the panel reappears, now with the moved geometry
    // (its overlay override) reflected in the highlight.
    final rec = _db?.featureById(id);
    if (rec != null) _select(_withOverride(rec));
  }

  /// A copy of [f] carrying its local geometry move (if any) as geometryOverride,
  /// so the map highlight/overlay draws the new shape instead of the packed one.
  FeatureRecord _withOverride(FeatureRecord f) {
    final moved = _edit?.geomEditFor(f.id);
    if (moved == null) return f;
    return FeatureRecord(
      id: f.id,
      featureClass: f.featureClass,
      name: f.name,
      status: f.status,
      subtype: f.subtype,
      territoryId: f.territoryId,
      geomBytes: f.geomBytes,
      attributes: f.attributes,
      geometryOverride: WkbGeometry.fromGeoJson(moved),
    );
  }

  /// The ids hidden from the base pack layers: every moved feature (its original
  /// is baked into the tiles), every feature deleted offline (a tombstone -- hidden
  /// and never redrawn), plus the one being actively edited (drawn as handles).
  /// Deduped.
  List<String> _hiddenIds() {
    final edit = _edit;
    if (edit == null) return const [];
    final ids = {...edit.movedIds, ...edit.deletedIds};
    if (_editGeomId != null) ids.add(_editGeomId!);
    return ids.toList(growable: false);
  }

  /// The create form, rendered in the panel slot. Pre-filled when editing an
  /// existing create in place.
  Widget _createForm(EdgeInsetsGeometry padding) {
    final cls = _createClass!;
    final fields = _db!.fieldsFor(cls.code);
    final domains = _domainsFor(fields);
    final existing =
        _editingCreateId != null ? _edit!.createFor(_editingCreateId!) : null;
    return FeatureCreateView(
      featureClass: cls.code,
      fields: fields,
      subtypes: _createSubtypes,
      domainValues: domains,
      initialSubtype: existing?.subtype,
      initialName: existing?.name,
      initialAttrs: existing?.attrs ?? const {},
      saveLabel: _attachToPoleId != null
          ? 'Attach'
          : _editingCreateId != null
              ? 'Save'
              : 'Create',
      onCreate: _saveCreate,
      onCancel: _cancelCreate,
      padding: padding,
    );
  }

  /// The manifest stores bbox as a JSON array string ("[minX,minY,maxX,maxY]").
  List<double>? _bboxFromManifest(Map<String, String> manifest) {
    final raw = manifest['bbox'];
    if (raw == null || raw.isEmpty) return null;
    try {
      final decoded = jsonDecode(raw);
      if (decoded is List && decoded.length == 4) {
        return [for (final v in decoded) (v as num).toDouble()];
      }
    } catch (_) {}
    return null;
  }

  @override
  void dispose() {
    _trackSub?.close();
    // End the field session: stop recording breadcrumbs (foreground-only).
    _recorderSub?.close();
    _recorderSub = null;
    _edit?.removeListener(_onEditChanged);
    _edit?.dispose();
    _db?.close();
    super.dispose();
  }

  /// Select one feature and load its connectivity/relationship neighbours (both
  /// index-backed by-id lookups in the pack -- cheap enough to run on every pick).
  /// A created feature has no packed neighbours yet, so those come back empty.
  void _select(FeatureRecord f) {
    // A moved feature carries its new geometry via override, so the highlight and
    // panel reflect the local move, not the packed original.
    final sel = _withOverride(f);
    setState(() {
      _selected = sel;
      _editing = false; // picking a different feature leaves any open edit
      _connections = _db!.connectionsFor(f.id);
      _relationships = _db!.relationshipsFor(f.id);
    });
  }

  /// The map reports every feature under the tap. One hit selects directly; a
  /// stack of 2+ opens a compact chooser so a buried line/point is reachable.
  Future<void> _onFeaturesTapped(List<FeatureRecord> features) async {
    if (features.isEmpty || _creatingForm || _editingGeom) return;
    if (features.length == 1) {
      _select(features.first);
      return;
    }
    final chosen = await _pickPeer(features);
    if (chosen != null) _select(chosen);
  }

  /// Follow a neighbour link: look the target up in the pack and select it, so
  /// the panel and map walk connectivity/relationships hop by hop.
  void _navigate(String featureId) {
    final r = _db!.featureById(featureId);
    if (r != null) _select(r); // _select clears edit mode
  }

  Future<FeatureRecord?> _pickPeer(List<FeatureRecord> features) {
    return showModalBottomSheet<FeatureRecord>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
              child: Text('${features.length} features here',
                  style: Theme.of(context).textTheme.titleSmall),
            ),
            Flexible(
              child: ListView(
                shrinkWrap: true,
                children: [
                  for (final f in features)
                    ListTile(
                      dense: true,
                      title: Text(f.name?.isNotEmpty == true
                          ? f.name!
                          : shortId(f.id)),
                      subtitle: Text(f.featureClass),
                      onTap: () => Navigator.of(context).pop(f),
                    ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _clearSelection() => setState(() {
        _selected = null;
        _editing = false;
        _connections = const [];
        _relationships = const [];
      });

  // ---- Connect flow (Phase 21 Slice 13b) ------------------------------------

  /// Arm connect mode from the selected feature: if its class has named terminals,
  /// ask which one the wire leaves from, then wait for a target tap on the map. The
  /// source stays selected (its panel/highlight persists) while the banner guides
  /// the target pick.
  Future<void> _startConnect(FeatureRecord from) async {
    final terminals =
        _db?.terminalsFor(from.featureClass, subtypeCode: from.subtype) ??
            const <TerminalDef>[];
    String? terminal;
    if (terminals.isNotEmpty) {
      final pick = await _pickTerminal(from, terminals);
      if (pick == null || !mounted) return; // dismissed -> abort
      terminal = pick.code;
    }
    setState(() {
      _connectFrom = from;
      _connectFromTerminal = terminal;
      _editing = false;
    });
  }

  /// Leave connect mode without establishing anything.
  void _cancelConnect() => setState(() {
        _connectFrom = null;
        _connectFromTerminal = null;
      });

  /// The map reported the features under a tap while in connect mode. Pick the
  /// target (disambiguating a stack), refuse a self-link, optionally resolve its
  /// terminal, enforce the pack's network_rules (a rejected edge is blocked when
  /// the pack ships rules -- Slice 14c), then queue the establish_connection op
  /// and drop back to the panel. The server re-validates on sync regardless.
  Future<void> _onConnectTarget(List<FeatureRecord> features) async {
    final from = _connectFrom;
    final edit = _edit;
    if (from == null || edit == null || features.isEmpty) return;
    final candidates = [for (final f in features) if (f.id != from.id) f];
    if (candidates.isEmpty) {
      _snack('Pick a different feature to connect to.');
      return;
    }
    final target =
        candidates.length == 1 ? candidates.first : await _pickPeer(candidates);
    if (target == null || !mounted) return;

    String? toTerminal;
    final terminals =
        _db?.terminalsFor(target.featureClass, subtypeCode: target.subtype) ??
            const <TerminalDef>[];
    if (terminals.isNotEmpty) {
      final pick = await _pickTerminal(target, terminals);
      if (pick == null || !mounted) return;
      toTerminal = pick.code;
    }

    // Enforce the pack's network_rules offline (Phase 21 Slice 14c). When the
    // pack ships rules at all, an edge they reject is BLOCKED here -- the same
    // zero-trust verdict the web/REST surface gives -- instead of being queued
    // for the server to bounce on sync. A blocked pick keeps connect mode armed
    // so the user can choose a valid target. When the pack ships no rules
    // (pre-v7), packHasRules is false and the check returns `unknown`, so the
    // edge is created optimistically and the server stays the sole authority.
    final rules = _db?.networkRulesFor(from.featureClass, target.featureClass) ??
        const <NetworkRuleDef>[];
    final check = checkConnectivity(
      fromClass: from.featureClass,
      fromSubtype: from.subtype,
      fromTerminal: _connectFromTerminal,
      toClass: target.featureClass,
      toSubtype: target.subtype,
      toTerminal: toTerminal,
      rules: rules,
      packHasRules: _db?.packsNetworkRules ?? false,
    );
    if (check.isBlocked) {
      await _showConnectivityBlocked(check);
      return;
    }

    await edit.createConnection(
      fromClass: from.featureClass,
      fromId: from.id,
      fromTerminal: _connectFromTerminal,
      toClass: target.featureClass,
      toId: target.id,
      toTerminal: toTerminal,
    );
    if (!mounted) return;
    setState(() {
      _connectFrom = null;
      _connectFromTerminal = null;
    });
    _snack('Connection queued.');
  }

  /// A terminal picker for one endpoint: the class's named terminals plus a "No
  /// specific terminal" option. Returns null when dismissed (the caller aborts); a
  /// [_TermPick] carrying a null code is the no-terminal choice.
  Future<_TermPick?> _pickTerminal(
      FeatureRecord f, List<TerminalDef> terminals) {
    final label = f.name?.isNotEmpty == true ? f.name! : shortId(f.id);
    return showModalBottomSheet<_TermPick>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
              child: Text('Terminal on $label',
                  style: Theme.of(context).textTheme.titleSmall),
            ),
            Flexible(
              child: ListView(
                shrinkWrap: true,
                children: [
                  for (final t in terminals)
                    ListTile(
                      dense: true,
                      leading: const Icon(Icons.electrical_services_outlined),
                      title: Text(t.label),
                      onTap: () => Navigator.of(context).pop(_TermPick(t.code)),
                    ),
                  ListTile(
                    dense: true,
                    leading: const Icon(Icons.do_not_disturb_on_outlined),
                    title: const Text('No specific terminal'),
                    onTap: () =>
                        Navigator.of(context).pop(const _TermPick(null)),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  /// A blocking dialog shown when the pack's network_rules refuse a proposed
  /// connection (Slice 14c) -- the edge is NOT queued. Connect mode stays armed so
  /// the user can pick a valid target. Reached only when the pack ships rules (a
  /// pre-v7 pack that carries none never gets here); the server re-checks on sync.
  Future<void> _showConnectivityBlocked(ConnectivityCheck check) {
    return showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
        icon: const Icon(Icons.block),
        title: const Text('Connection not allowed'),
        content: Text(check.message),
        actions: [
          FilledButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('OK'),
          ),
        ],
      ),
    );
  }

  // ---- Relate flow (Phase 21 Slice 14) --------------------------------------

  /// Arm relate mode from the selected feature: wait for a destination tap on the
  /// map. The relationship sibling of [_startConnect]. The kind is resolved at the
  /// destination tap (see [_onRelateTarget]) -- a pack with relationship_classes
  /// (schema v7+) offers a KIND picker when the pair allows more than one; the
  /// origin stays selected while the banner guides the destination pick.
  void _startRelate(FeatureRecord from) {
    setState(() {
      _relateFrom = from;
      _editing = false;
    });
  }

  /// Leave relate mode without establishing anything.
  void _cancelRelate() => setState(() => _relateFrom = null);

  /// The map reported the features under a tap while in relate mode. Pick the
  /// destination (disambiguating a stack), refuse a self-link, resolve the
  /// relationship kind, then queue the establish_relationship op and drop back to
  /// the panel. Kind resolution (Slice 14b): the pack's relationship_classes for
  /// the directional (origin, destination) pair decide it -- one class needs no
  /// prompt (its kind rides along), several offer a KIND picker. When the pack
  /// ships classes but none permits the pair the relationship is BLOCKED (Slice
  /// 14c); when the pack ships none (pre-v7) kind stays null for the server to
  /// resolve on sync. The relationship sibling of [_onConnectTarget].
  Future<void> _onRelateTarget(List<FeatureRecord> features) async {
    final from = _relateFrom;
    final edit = _edit;
    if (from == null || edit == null || features.isEmpty) return;
    final candidates = [for (final f in features) if (f.id != from.id) f];
    if (candidates.isEmpty) {
      _snack('Pick a different feature to relate to.');
      return;
    }
    final target =
        candidates.length == 1 ? candidates.first : await _pickPeer(candidates);
    if (target == null || !mounted) return;

    final classes = _db?.relationshipClassesFor(
            from.featureClass, target.featureClass) ??
        const <RelationshipClassDef>[];
    // Enforce the pack's relationship_classes offline (Slice 14c). When the pack
    // ships classes at all but none permits this directional (origin, destination)
    // pair, the relationship is BLOCKED here rather than queued for the server to
    // reject. When the pack ships none (pre-v7) the pair can't be judged, so kind
    // stays null and the server resolves/rejects it on sync.
    if (classes.isEmpty && (_db?.packsRelationshipClasses ?? false)) {
      await _showRelationshipBlocked(target);
      return;
    }
    String? kind;
    if (classes.length == 1) {
      kind = classes.first.kind;
    } else if (classes.length > 1) {
      final pick = await _pickRelationshipKind(target, classes);
      if (pick == null || !mounted) return; // dismissed -> abort
      kind = pick.kind;
    }

    await edit.createRelationship(
      originClass: from.featureClass,
      originId: from.id,
      destinationClass: target.featureClass,
      destinationId: target.id,
      kind: kind,
    );
    if (!mounted) return;
    setState(() => _relateFrom = null);
    _snack('Relationship queued.');
  }

  /// A blocking dialog shown when the pack's relationship_classes permit no
  /// relationship between the origin and [target] classes (Slice 14c) -- the
  /// relationship is NOT queued. Relate mode stays armed so the user can pick a
  /// valid destination. Reached only when the pack ships classes at all; the
  /// server re-checks on sync regardless.
  Future<void> _showRelationshipBlocked(FeatureRecord target) {
    final label =
        target.name?.isNotEmpty == true ? target.name! : shortId(target.id);
    return showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
        icon: const Icon(Icons.block),
        title: const Text('Relationship not allowed'),
        content: Text('No relationship class permits linking these to $label, '
            'so it can\'t be created here.'),
        actions: [
          FilledButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('OK'),
          ),
        ],
      ),
    );
  }

  /// A kind picker for a relationship whose (origin, destination) pair supports
  /// more than one relationship_class (Slice 14b) -- mirrors [_pickTerminal].
  /// Returns null when dismissed (the caller aborts). Each row is one candidate
  /// class; there's no "no kind" escape hatch because a multi-class pair is
  /// ambiguous without one (the server would reject a kind-less op).
  Future<RelationshipClassDef?> _pickRelationshipKind(
      FeatureRecord target, List<RelationshipClassDef> classes) {
    final label =
        target.name?.isNotEmpty == true ? target.name! : shortId(target.id);
    return showModalBottomSheet<RelationshipClassDef>(
      context: context,
      showDragHandle: true,
      builder: (context) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
              child: Text('Relationship to $label',
                  style: Theme.of(context).textTheme.titleSmall),
            ),
            Flexible(
              child: ListView(
                shrinkWrap: true,
                children: [
                  for (final c in classes)
                    ListTile(
                      dense: true,
                      leading: const Icon(Icons.account_tree_outlined),
                      title: Text(c.label),
                      subtitle: c.cardinality.isNotEmpty
                          ? Text(c.cardinality.replaceAll('_', ' '))
                          : null,
                      onTap: () => Navigator.of(context).pop(c),
                    ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  void _snack(String msg) {
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
    }
  }

  // ---- Asset-tag scan (asset-tagging module S5) -----------------------------

  /// Open the camera scanner, then resolve whatever code it returns. The scanner
  /// normalizes a QR deep link or a Code128 to the bare code (see
  /// [extractTagCode]); a dismissed scan returns null and we do nothing.
  Future<void> _scanTag() async {
    if (_db == null) return;
    final code = await Navigator.of(context).push<String>(
      MaterialPageRoute<String>(builder: (_) => const ScanTagScreen()),
    );
    if (code == null || code.isEmpty || !mounted) return;
    await _resolveScannedTag(code);
  }

  /// Resolve a scanned tag [code] OFFLINE FIRST against the pack's `asset_tags`
  /// index -- the whole reason S5 ships that index -- then fall back to the online
  /// GET /tags?code= resolver when the pack has no such code (the asset sits
  /// outside the download, or an older pack predates the index). On a hit whose
  /// feature is in the pack we select it on the map; an out-of-pack hit can't be
  /// mapped here, so we name it instead. A genuine miss or an offline failure is
  /// surfaced as a message rather than silently doing nothing.
  Future<void> _resolveScannedTag(String code) async {
    final db = _db;
    if (db == null) return;

    // Offline: the pack carries every resolvable code (active + superseded) on its
    // packed features, already scope/draft-filtered when it was built.
    final packed = db.resolveTag(code);
    if (packed != null) {
      final rec = db.featureById(packed.featureId);
      if (rec != null) {
        _select(rec);
        _snack('Resolved $code'
            '${packed.status == 'superseded' ? ' — superseded label' : ''}.');
        return;
      }
    }

    // Fall back to the server (online-only). A code the caller may not see, an
    // unknown code, or a retired label answers 404 there.
    TagResolution res;
    try {
      res = await ref.read(fieldsyncClientProvider).resolveTag(code);
    } on FieldsyncException catch (e) {
      _snack(e.statusCode == 404
          ? 'No asset found for tag $code.'
          : 'Could not resolve $code: ${e.message}');
      return;
    } catch (_) {
      _snack('Could not resolve $code (are you online?).');
      return;
    }
    if (!mounted) return;

    // The server resolved it. If that feature is in this pack, select it; else it
    // lies outside the download, so we can't put it on this map -- name it.
    final rec = db.featureById(res.featureId);
    if (rec != null) {
      _select(rec);
      _snack('Resolved $code.');
      return;
    }
    final name = res.featureName?.isNotEmpty == true
        ? res.featureName!
        : shortId(res.featureId);
    await showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Tag $code'),
        content: Text(
          'Resolved to $name'
          '${res.featureClass.isNotEmpty ? ' (${res.featureClass})' : ''}, '
          "but this asset is outside the downloaded pack, so it can't be shown "
          'on this map. Build a pack covering it to work with it offline.',
        ),
        actions: [
          FilledButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('OK'),
          ),
        ],
      ),
    );
  }

  /// In-app disclosure of location recording (the field breadcrumb audit trail):
  /// the crew taps the location icon to see whether they're being recorded and what
  /// that means. Required so crews are told their location is logged — some
  /// jurisdictions mandate disclosing staff-location tracking. Reflects the org-wide
  /// switch: "on" explains the trail + edit stamps; "off" states nothing is recorded.
  void _showTrackingDisclosure() {
    final on = ref.read(settingsRepositoryProvider).fieldTrackingEnabled;
    showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        icon: Icon(on ? Icons.my_location : Icons.location_disabled),
        title: Text(on ? 'Location recording is on' : 'Location recording is off'),
        content: Text(on
            ? 'While this app is open, VoltEdge records your movement trail and the spot where you make each edit, so your supervisor can review how the field work was done. It stops when you close the app and never runs in the background.'
            : 'Your organisation has turned field tracking off, so nothing about your location is recorded — no movement trail, and no location saved on your edits.'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('OK'),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    // The badge folds feature edits + captured-but-unuploaded photos into one
    // "unsynced" count, since Sync pushes both.
    final pending =
        (_edit?.pendingCount ?? 0) + (_edit?.pendingAttachmentCount ?? 0);
    // Quarantined ops (captured but rejected by the apply worker) that the user
    // hasn't acknowledged -- surfaced separately from `pending` since they're not
    // re-pushable work but do need attention (Phase 23 Slice 2).
    final attention = _edit?.attentionCount ?? 0;
    return Scaffold(
      appBar: AppBar(
        title: Text('Map · ${shortId(widget.pack.id)}'),
        actions: [
          if (_edit != null && attention > 0 && !_syncing)
            Padding(
              padding: const EdgeInsets.only(right: 4),
              child: Badge(
                backgroundColor: const Color(0xFFFF453A),
                label: Text('$attention'),
                child: IconButton(
                  icon: const Icon(Icons.error_outline,
                      color: Color(0xFFFF453A)),
                  tooltip: '$attention change'
                      '${attention == 1 ? '' : 's'} need attention',
                  onPressed: _showPending,
                ),
              ),
            ),
          if (_edit != null && (pending > 0 || _syncing))
            Padding(
              padding: const EdgeInsets.only(right: 4),
              child: _syncing
                  ? const Center(
                      child: Padding(
                        padding: EdgeInsets.symmetric(horizontal: 16),
                        child: SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        ),
                      ),
                    )
                  : Badge(
                      label: Text('$pending'),
                      child: IconButton(
                        icon: const Icon(Icons.sync),
                        tooltip: 'Pending changes ($pending)',
                        onPressed: _showPending,
                      ),
                    ),
            ),
          // Disclosure is shown only when we are ACTUALLY recording (tracking on)
          // AND the org has disclosure on. If tracking is off there's nothing to
          // disclose; if disclosure is off the org has chosen not to tell crews —
          // either way the icon is hidden.
          if (_db != null &&
              ref.read(settingsRepositoryProvider).fieldTrackingEnabled &&
              ref.read(settingsRepositoryProvider).fieldTrackingDisclosureEnabled)
            IconButton(
              icon: const Icon(Icons.my_location),
              tooltip: 'Location recording',
              onPressed: _showTrackingDisclosure,
            ),
          if (_db != null)
            IconButton(
              icon: const Icon(Icons.qr_code_scanner),
              tooltip: 'Scan asset tag',
              onPressed: _scanTag,
            ),
          if (_db != null && _edit != null)
            IconButton(
              icon: const Icon(Icons.settings_outlined),
              tooltip: 'Map settings',
              onPressed: _openSettings,
            ),
        ],
      ),
      body: _error != null
          ? Center(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Text('Could not open pack:\n$_error',
                    textAlign: TextAlign.center,
                    style:
                        TextStyle(color: Theme.of(context).colorScheme.error)),
              ),
            )
          : _db == null
              ? const Center(child: CircularProgressIndicator())
              : LayoutBuilder(
                  builder: (context, constraints) {
                    final wide = constraints.maxWidth >= _tabletBreakpoint;
                    return wide ? _tabletLayout() : _phoneLayout();
                  },
                ),
    );
  }

  Widget _map() => PackMapView(
        db: _db!,
        mbtilesPath: widget.pack.tilesPath,
        bbox: _bbox,
        // No selection highlight while editing geometry -- the handles are it.
        selected: _editingGeom ? null : _selected,
        dirty: _dirtyFeatures(),
        // Offline creates AND the crew's overlay drafts (shared-field-packs S3d) share
        // the one queryable overlay layer -- a draft isn't in the base pack, so like a
        // create it's drawn here and made tappable; the map resolves a tapped id from
        // this set when the pack has no such row.
        created: [..._createdFeatures(), ..._draftFeatures()],
        movedIds: _edit?.movedIds.toList(growable: false) ?? const [],
        hiddenIds: _hiddenIds(),
        drawing: _drawing,
        draftPoint: _draftPoint,
        // A fresh copy each build: the map diffs old vs new in didUpdateWidget,
        // which the same mutated instance would defeat.
        draftLine: List<LatLng>.of(_draftVertices),
        draftIsPolygon: _createIsPolygon,
        onPointPlaced: _onPointPlaced,
        editing: _editingGeom,
        editVertices: List<LatLng>.of(_editVertices),
        editIsLine: _editGeomIsLine,
        editIsPolygon: _editGeomIsPolygon,
        selectedVertex: _selectedVertex,
        onVertexDragged: _onVertexDragged,
        onVertexTapped: _onVertexTapped,
        onMidpointTapped: _onMidpointInserted,
        snapEnabled: _snapEnabled,
        onFeaturesTapped: _onFeaturesTapped,
        // Connect flow (Slice 13b): the green established-edge overlay, plus
        // connect-mode routing so a tap picks the target instead of selecting.
        connections: _connectionEdges(),
        connecting: _connecting,
        onConnectTarget: _onConnectTarget,
        // Relate flow (Slice 14): the purple established-relationship overlay, plus
        // relate-mode routing so a tap picks the destination instead of selecting.
        relationships: _relationshipEdges(),
        relating: _relating,
        onRelateTarget: _onRelateTarget,
        onLocate: _locateMe,
        centerOn: _gpsCenter,
        locating: _locating,
        showUserLocation: _showUserLocation,
        basemapId: _basemapId,
      );

  /// Whether the Create FAB should show. It lives INSIDE the map region (not as a
  /// Scaffold FAB) so on a tablet it floats over the map, never the side panel,
  /// and on a phone the bottom sheet draws over it instead of it over the sheet.
  bool get _showCreateFab =>
      _db != null &&
      _error == null &&
      // Creating a feature is a write -- a read-only tier (field-surveyor, no
      // features:write) doesn't get the Create button (migration 0164 ships the
      // user's permissions on login; the server enforces the write on sync too).
      ref.read(settingsRepositoryProvider).hasPermission('features:write') &&
      _createClass == null &&
      !_editing &&
      !_editingGeom &&
      !_connecting &&
      !_relating;

  /// The map plus, while placing a point, an instruction/confirm banner on top,
  /// and the Create FAB anchored to the map's own bottom-right.
  Widget _mapArea() => Stack(
        children: [
          Positioned.fill(child: _map()),
          // Live GNSS quality (Slice 2), anchored top-left. Hidden while a top
          // banner (draw/connect/relate) owns that strip so the two never overlap.
          if (!_drawing && !_connecting && !_relating)
            Positioned(
              top: 12 + MediaQuery.of(context).padding.top,
              left: 12,
              child: _gnssTracking
                  ? GnssReadout(
                      onDisable: () => setState(() => _gnssTracking = false),
                    )
                  : _gnssChip(),
            ),
          if (_showCreateFab)
            Positioned(
              right: 16,
              bottom: 16 + MediaQuery.of(context).padding.bottom,
              child: FloatingActionButton.extended(
                heroTag: 'createFab',
                onPressed: _startCreate,
                icon: const Icon(Icons.add_location_alt_outlined),
                label: const Text('Create'),
              ),
            ),
          if (_drawing)
            Positioned(
              top: 0,
              left: 0,
              right: 0,
              child: SafeArea(bottom: false, child: _drawBanner()),
            ),
          if (_connecting)
            Positioned(
              top: 0,
              left: 0,
              right: 0,
              child: SafeArea(bottom: false, child: _connectBanner()),
            ),
          if (_relating)
            Positioned(
              top: 0,
              left: 0,
              right: 0,
              child: SafeArea(bottom: false, child: _relateBanner()),
            ),
          if (_editingGeom)
            Positioned(
              left: 0,
              right: 0,
              bottom: 0,
              child: SafeArea(top: false, child: _geomEditBar()),
            ),
        ],
      );

  /// The collapsed GNSS control: a small pill that turns the live quality readout
  /// on. Kept tiny so it doesn't cover the map; expands into [GnssReadout] once on.
  Widget _gnssChip() {
    final theme = Theme.of(context);
    return Material(
      elevation: 3,
      color: theme.colorScheme.surface,
      borderRadius: BorderRadius.circular(20),
      child: InkWell(
        borderRadius: BorderRadius.circular(20),
        onTap: () => setState(() => _gnssTracking = true),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(Icons.satellite_alt_outlined,
                  size: 18, color: theme.colorScheme.primary),
              const SizedBox(width: 6),
              Text('GPS quality', style: theme.textTheme.labelLarge),
            ],
          ),
        ),
      ),
    );
  }

  /// The bottom action bar for the geometry editor: instructions plus Undo,
  /// Delete-vertex (line, when a vertex is selected), Cancel, and Save.
  Widget _geomEditBar() {
    final theme = Theme.of(context);
    final selected = _selectedVertex != null;
    final minVertices = _editGeomIsPolygon ? 3 : 2;
    final canDelete =
        selected && _editGeomTracing && _editVertices.length > minVertices;
    final String hint;
    if (_editGeomTracing) {
      hint = selected
          ? 'Drag to move, tap a white midpoint to add, or delete this point.'
          : 'Drag a point to move it. Tap a white midpoint to add a point.';
    } else {
      hint = 'Drag the point to move it.';
    }
    return Padding(
      padding: const EdgeInsets.all(12),
      child: Material(
        elevation: 6,
        borderRadius: BorderRadius.circular(12),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 8, 8, 8),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  const Icon(Icons.edit_location_alt_outlined, size: 20),
                  const SizedBox(width: 10),
                  Expanded(
                    child: Text(hint, style: theme.textTheme.bodyMedium),
                  ),
                ],
              ),
              const SizedBox(height: 4),
              Row(
                children: [
                  IconButton(
                    icon: const Icon(Icons.undo),
                    tooltip: 'Undo',
                    onPressed: _geomUndo.isEmpty ? null : _undoGeom,
                  ),
                  if (_editGeomTracing)
                    IconButton(
                      icon: const Icon(Icons.delete_outline),
                      tooltip: canDelete
                          ? 'Delete selected point'
                          : 'Select a point to delete (min $minVertices)',
                      onPressed: canDelete ? _deleteSelectedVertex : null,
                    ),
                  const Spacer(),
                  TextButton(
                    onPressed: _cancelGeomEdit,
                    child: const Text('Cancel'),
                  ),
                  const SizedBox(width: 4),
                  FilledButton(
                    onPressed: _saveGeomEdit,
                    child: const Text('Save'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  /// The connect-mode banner: names the armed source (and its terminal, if any)
  /// and prompts for the target tap, with a Cancel to bail out.
  Widget _connectBanner() {
    final from = _connectFrom!;
    final name = from.name?.isNotEmpty == true ? from.name! : shortId(from.id);
    final term = _connectFromTerminal;
    final suffix = term != null ? ' ($term)' : '';
    return Padding(
      padding: const EdgeInsets.all(12),
      child: Material(
        elevation: 4,
        borderRadius: BorderRadius.circular(12),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 10, 10, 10),
          child: Row(
            children: [
              const Icon(Icons.hub_outlined, size: 20, color: Color(0xFF30D158)),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  'Tap the feature to connect to $name$suffix.',
                  style: Theme.of(context).textTheme.bodyMedium,
                ),
              ),
              const SizedBox(width: 4),
              TextButton(
                onPressed: _cancelConnect,
                child: const Text('Cancel'),
              ),
            ],
          ),
        ),
      ),
    );
  }

  /// The relate-mode banner: names the origin and prompts for the destination tap,
  /// with a Cancel to bail out. The relationship sibling of [_connectBanner], in
  /// purple; no terminal suffix because a relationship has no terminals.
  Widget _relateBanner() {
    final from = _relateFrom!;
    final name = from.name?.isNotEmpty == true ? from.name! : shortId(from.id);
    return Padding(
      padding: const EdgeInsets.all(12),
      child: Material(
        elevation: 4,
        borderRadius: BorderRadius.circular(12),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 10, 10, 10),
          child: Row(
            children: [
              const Icon(Icons.account_tree_outlined,
                  size: 20, color: Color(0xFFBF5AF2)),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  'Tap the feature to relate to $name.',
                  style: Theme.of(context).textTheme.bodyMedium,
                ),
              ),
              const SizedBox(width: 4),
              TextButton(
                onPressed: _cancelRelate,
                child: const Text('Cancel'),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _drawBanner() {
    final cls = _createClass!;
    final name = cls.name?.isNotEmpty == true ? cls.name! : cls.code;
    final tracing = _createTracing;
    // A traced shape is placeable at its minimum (2 for a line, 3 for a
    // polygon); a point once it's dropped.
    final vertices = _draftVertices.length;
    final min = _createMinVertices;
    final canPlace = tracing ? vertices >= min : _draftPoint != null;

    final String message;
    if (tracing && _tracking) {
      // Hands-free GPS walk in progress.
      final walk = _createIsPolygon ? 'boundary' : 'line';
      message = 'Recording GPS track — walk the $walk. '
          '$vertices point${vertices == 1 ? '' : 's'} so far'
          '${canPlace ? '; tap Place when done.' : '.'}';
    } else if (tracing) {
      final needMore = min - vertices;
      final shape = _createIsPolygon ? 'shape' : 'line';
      message = vertices == 0
          ? 'Tap the map, use your location, or record a GPS track to trace the '
              'new $name.'
          : 'Tap to add points ($vertices), then Place. '
              '${needMore > 0 ? 'Add $needMore more to finish the $shape.' : ''}'
              '${needMore <= 0 && _createIsPolygon ? 'The ring closes automatically.' : ''}';
    } else {
      message = canPlace
          ? 'Tap to move, or place the new $name here.'
          : 'Tap the map, or use your location, to place the new $name.';
    }

    return Padding(
      padding: const EdgeInsets.all(12),
      child: Material(
        elevation: 4,
        borderRadius: BorderRadius.circular(12),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(14, 10, 10, 10),
          child: Row(
            children: [
              const Icon(Icons.touch_app_outlined, size: 20),
              const SizedBox(width: 10),
              Expanded(
                child: Text(
                  message,
                  style: Theme.of(context).textTheme.bodyMedium,
                ),
              ),
              const SizedBox(width: 4),
              // Traced shapes only: walk the geometry by GPS, hands-free. Toggles
              // auto-vertex capture on/off; Place still commits the result.
              if (tracing)
                IconButton(
                  icon: Icon(
                    _tracking
                        ? Icons.stop_circle
                        : Icons.fiber_manual_record,
                    color: _tracking
                        ? Theme.of(context).colorScheme.error
                        : null,
                  ),
                  tooltip:
                      _tracking ? 'Stop GPS tracking' : 'Record a GPS track',
                  onPressed: _tracking ? _stopTracking : _startTracking,
                ),
              // Traced shapes only: undo the last vertex placed.
              if (tracing)
                IconButton(
                  icon: const Icon(Icons.undo),
                  tooltip: 'Undo last point',
                  onPressed: vertices == 0 ? null : _undoVertex,
                ),
              IconButton(
                icon: _locating
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Icon(Icons.my_location),
                tooltip: 'Use my location',
                // While a GPS track is recording, the manual drop is redundant
                // (fixes are already flowing), so it's disabled to avoid confusion.
                onPressed:
                    (_locating || _tracking) ? null : _placeAtMyLocation,
              ),
              TextButton(
                onPressed: _cancelCreate,
                child: const Text('Cancel'),
              ),
              const SizedBox(width: 4),
              FilledButton(
                onPressed: canPlace ? _confirmPlacement : null,
                child: const Text('Place'),
              ),
            ],
          ),
        ),
      ),
    );
  }

  /// The detail panel for the selected feature, with any local edit merged in
  /// and the Photos section (packed thumbnails + captured-offline images).
  Widget _detail(FeatureRecord f, EdgeInsetsGeometry padding) =>
      FeatureDetailView(
        feature: f,
        connections: _connections,
        relationships: _relationships,
        onNavigate: _navigate,
        attributesOverride: _edit?.mergedAttributes(f.id, f.attributes),
        pending: _edit?.isPending(f.id) ?? false,
        deleted: _edit?.isDeleted(f.id) ?? false,
        padding: padding,
        attachments: FeatureAttachmentsView(
          packAttachments: _db?.attachmentsFor(f.id) ?? const [],
          localAttachments: _edit?.localAttachmentsFor(f.id) ?? const [],
          onCapture: (source) => _capturePhoto(f, source),
          onOpenLocal: _openLocalAttachment,
          onOpenPack: _openPackAttachment,
          onDiscardLocal: (a) => _edit?.discardLocalAttachment(a.id),
        ),
      );

  /// Whether the side panel / bottom sheet should be shown at all. Hidden while
  /// editing geometry -- the geom-edit bar over the map owns the interaction then.
  bool get _panelVisible =>
      !_editingGeom && (_selected != null || _creatingForm);

  /// The panel body for the current mode (create form, edit form, or detail).
  Widget _panelBody(EdgeInsetsGeometry editPadding, EdgeInsetsGeometry
      detailPadding) {
    if (_creatingForm) return _createForm(editPadding);
    if (_editing) return _editForm(_selected!, editPadding);
    return _detail(_selected!, detailPadding);
  }

  Widget _tabletLayout() {
    return Row(
      children: [
        Expanded(child: _mapArea()),
        if (_panelVisible)
          SizedBox(
            width: 360,
            child: Material(
              elevation: 2,
              child: Column(
                children: [
                  _panelHeader(),
                  Expanded(
                    child: _panelBody(const EdgeInsets.only(bottom: 12),
                        const EdgeInsets.only(bottom: 24)),
                  ),
                ],
              ),
            ),
          ),
      ],
    );
  }

  Widget _phoneLayout() {
    // The create/edit forms need more room than a read-only glance.
    final tall = _editing || _creatingForm;
    return Stack(
      children: [
        Positioned.fill(child: _mapArea()),
        if (_panelVisible)
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            child: SafeArea(
              top: false,
              // While a form is open, lift the sheet above the keyboard so the
              // fields and the action bar stay visible.
              child: Padding(
                padding: EdgeInsets.only(
                  bottom: tall ? MediaQuery.of(context).viewInsets.bottom : 0,
                ),
                child: Material(
                  elevation: 8,
                  borderRadius:
                      const BorderRadius.vertical(top: Radius.circular(16)),
                  clipBehavior: Clip.antiAlias,
                  child: ConstrainedBox(
                    constraints: BoxConstraints(
                      maxHeight: MediaQuery.of(context).size.height *
                          (tall ? 0.7 : 0.45),
                    ),
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        _panelHeader(),
                        Flexible(
                          child: _panelBody(const EdgeInsets.only(bottom: 12),
                              const EdgeInsets.only(bottom: 16)),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ),
      ],
    );
  }

  Widget _panelHeader() {
    final theme = Theme.of(context);
    // Create mode: a synthetic header naming the class being created.
    if (_creatingForm) {
      final cls = _createClass!;
      final name = cls.name?.isNotEmpty == true ? cls.name! : cls.code;
      return Container(
        padding: const EdgeInsets.fromLTRB(16, 12, 8, 12),
        color: theme.colorScheme.surfaceContainerHighest,
        child: Row(
          children: [
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                      _attachToPoleId != null
                          ? 'Attach $name'
                          : _editingCreateId != null
                              ? 'Edit $name'
                              : 'New $name',
                      style: theme.textTheme.titleMedium,
                      overflow: TextOverflow.ellipsis),
                  Text(cls.code,
                      style: theme.textTheme.bodySmall
                          ?.copyWith(color: theme.colorScheme.outline)),
                ],
              ),
            ),
            IconButton(
              icon: const Icon(Icons.close),
              tooltip: 'Discard',
              onPressed: _cancelCreate,
            ),
          ],
        ),
      );
    }

    final f = _selected!;
    final title = f.name?.isNotEmpty == true ? f.name! : shortId(f.id);
    // Discard only makes sense for an UN-synced create -- once synced the feature
    // lives on the server, so a local "discard" would be a lie (it wouldn't
    // delete it there). A PACKED feature (a server row) is deleted instead (Slice
    // 15): a 'delete' op soft-deletes it on sync, and until then it's a tombstone.
    final canDiscard =
        (_edit?.isCreated(f.id) ?? false) && (_edit?.isPending(f.id) ?? false);
    final isDeleted = _edit?.isDeleted(f.id) ?? false;
    // Only a packed feature (present in this pack = a real server row) can be
    // deleted; an offline create is discarded, not deleted (no server row yet).
    final canDelete = !isDeleted &&
        !(_edit?.isCreated(f.id) ?? false) &&
        (_db?.featureById(f.id) != null);
    // Connectivity is electrical: only an `edge` or a `junction` can hold a
    // terminal connection. A `container` (diagram host) and `none` (e.g. a POLE,
    // fully outside the network model) can't, so the Connect action is hidden
    // for them -- offering it would only produce a connection the server rules
    // reject. An unknown role (older pack missing the column) keeps the button
    // rather than silently dropping connectivity. Relationships stay available
    // for every feature (a pole may still *relate* to the gear it carries).
    final role = _db?.networkRoleFor(f.featureClass);
    final canConnect = role != 'none' && role != 'container';
    // A read-only tier (field-surveyor, no features:write) sees no write
    // affordances -- edit / delete / connect / relate / move / undo are all hidden,
    // leaving view + close. The server also rejects the write on sync regardless.
    final canWrite = ref.read(settingsRepositoryProvider).hasPermission('features:write');
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 12, 8, 12),
      color: theme.colorScheme.surfaceContainerHighest,
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          // Actions get their own right-aligned row so the title + class below
          // can use the panel's full width, instead of being squeezed into a
          // sliver beside the icons (which forced the class to wrap letter by
          // letter and the title to hard-ellipsize).
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              // The edit form carries its own Cancel/Save bar, so the header's Edit
              // and Discard buttons are only shown in read-only mode -- and not while
              // connecting, when the map's target-tap owns the interaction.
              if (!_editing && !_connecting && !_relating && canWrite) ...[
                // A tombstoned feature offers only Undo -- editing/connecting a
                // feature that's about to be soft-deleted makes no sense.
                if (isDeleted)
                  IconButton(
                    icon: const Icon(Icons.restore_from_trash_outlined),
                    tooltip: 'Undo delete',
                    onPressed: _undoSelectedDelete,
                  )
                else ...[
                  if (canDiscard)
                    IconButton(
                      icon: const Icon(Icons.delete_outline),
                      tooltip: 'Discard unsynced feature',
                      onPressed: _discardSelectedCreate,
                    ),
                  // Geometry move / connect / relate aren't wired for an overlay draft
                  // yet (shared-field-packs S3d offers draft attribute editing only --
                  // these ride a later slice), so they're hidden for a draft. A packed
                  // or created feature keeps them.
                  if (_canEditGeometry(f) && !_isDraft(f.id))
                    IconButton(
                      icon: const Icon(Icons.edit_location_alt_outlined),
                      tooltip: 'Edit geometry',
                      onPressed: _startGeomEdit,
                    ),
                  if (canConnect && !_isDraft(f.id))
                    IconButton(
                      icon: const Icon(Icons.hub_outlined),
                      tooltip: 'Connect to another feature',
                      onPressed: () => _startConnect(f),
                    ),
                  // A pole can carry a street light: a one-tap "attach" that
                  // creates the lamp at the pole and records the carry offline
                  // (the web runs the electrical auto-wire on sync).
                  if (f.featureClass == 'POLE' && !_isDraft(f.id))
                    IconButton(
                      icon: const Icon(Icons.lightbulb_outline),
                      tooltip: 'Attach street light',
                      onPressed: () => _startAttachStreetLight(f),
                    ),
                  if (!_isDraft(f.id))
                    IconButton(
                      icon: const Icon(Icons.account_tree_outlined),
                      tooltip: 'Relate to another feature',
                      onPressed: () => _startRelate(f),
                    ),
                  IconButton(
                    icon: const Icon(Icons.edit_outlined),
                    tooltip: 'Edit attributes',
                    onPressed: _startEdit,
                  ),
                  if (canDelete)
                    IconButton(
                      icon: const Icon(Icons.delete_forever_outlined),
                      tooltip: 'Delete feature',
                      onPressed: () => _deleteSelectedFeature(f),
                    ),
                ],
              ],
              IconButton(
                icon: const Icon(Icons.close),
                tooltip: _editing ? 'Close' : 'Clear selection',
                onPressed: _clearSelection,
              ),
            ],
          ),
          // Title + class on their own full-width row below the actions.
          Text(title,
              style: theme.textTheme.titleMedium,
              overflow: TextOverflow.ellipsis),
          Text(f.featureClass,
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.colorScheme.outline)),
        ],
      ),
    );
  }
}

/// One row in the pending-changes sheet: a queued outbox op with a resolved
/// display name and, for an edit, its changed-field count.
/// A friendly title for a WAM capture op shown in the pending sheet.
String _wamOpTitle(String kind) {
  switch (kind) {
    case 'wam_defect_create':
      return 'Defect captured';
    case 'wam_defect_status':
      return 'Defect status change';
    case 'wam_workorder_update':
      return 'Work order update';
    case 'wam_inspection_record':
      return 'Inspection recorded';
    case 'fault_create':
      return 'Fault raised';
    case 'fault_status':
      return 'Fault status change';
    case 'fault_book_defect':
      return 'Defect booked from fault';
    default:
      return 'Work update';
  }
}

class _PendingItem {
  _PendingItem({
    required this.op,
    required this.created,
    this.connection = false,
    this.connectionTo,
    this.relationship = false,
    this.relationshipTo,
    this.deleted = false,
    this.wam = false,
    this.name,
    this.changedCount = 0,
  });

  final OutboxOp op;
  final bool created;

  /// True for a queued WAM capture (a defect/inspection/work-order op,
  /// [[offline-wam-field-pack-and-sync]]). It shares the outbox with feature
  /// edits, so it shows here too -- but it targets no map feature, so [name]
  /// carries a friendly label and the row doesn't try to select a feature on tap.
  final bool wam;

  /// True for a queued 'delete' op (Slice 15) -- a packed feature marked deleted
  /// offline, soft-deleted on the server at sync.
  final bool deleted;

  /// True for a queued establish_connection op; [connectionTo] is the target's
  /// display name (null if the connection was reconciled away).
  final bool connection;
  final String? connectionTo;

  /// True for a queued establish_relationship op (Slice 14); [relationshipTo] is
  /// the destination's display name (null if the relationship was reconciled away).
  final bool relationship;
  final String? relationshipTo;
  final String? name;
  final int changedCount;
}

/// The result of the terminal picker (Phase 21 Slice 13b): a non-null return means
/// the user chose, [code] being the named terminal CODE or null for "no specific
/// terminal". A null future result means the sheet was dismissed -> abort.
class _TermPick {
  const _TermPick(this.code);
  final String? code;
}
