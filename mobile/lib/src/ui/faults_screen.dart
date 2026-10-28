import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:path/path.dart' as p;

import '../api/fieldsync_client.dart';
import '../api/models.dart';
import '../faults/faults_models.dart';
import '../faults/faults_store.dart';
import '../geo/field_track_recorder.dart';
import '../geo/gnss_sample.dart';
import '../geo/location_service.dart';
import '../pack/pack_database.dart';
import '../pack/pack_store.dart';
import '../providers.dart';
import 'format.dart';

/// The offline Faults surface (Faults module mobile, [[faults-module]] /
/// [[offline-wam-field-pack-and-sync]]): the fault-response crew's field face for
/// the OPEN faults a pack ships on its assets. It owns a screen-scoped [FaultsStore]
/// (its own read-only pack + writable edits.sqlite handles, WAL-safe beside the map
/// and WAM screens) and drives the same POST /sync machinery the map's edit layer
/// uses -- one flush pushes feature edits, WAM captures AND fault captures together.
///
/// One surface: a merged fault board (open faults on packed assets + locally-raised)
/// with a "Raise fault" capture form, per-fault status changes, and "Book defect".
/// Every capture rides the shared outbox, so the AppBar's sync button flushes the
/// lot. This is the faithful twin of [WamScreen].
class FaultsScreen extends ConsumerStatefulWidget {
  const FaultsScreen({super.key, required this.pack});
  final LocalPack pack;

  @override
  ConsumerState<FaultsScreen> createState() => _FaultsScreenState();
}

class _FaultsScreenState extends ConsumerState<FaultsScreen> {
  /// Max ops pushed per POST /sync request -- mirrors PackMapPage._syncBatchSize so
  /// a large outbox never holds one request open over an unbounded op count.
  static const _syncBatchSize = 200;

  FaultsStore? _store;
  Object? _error;
  bool _syncing = false;

  /// Feeds the shared field-track recorder the live GNSS stream so fault captures
  /// made on this screen are stamped with where the worker was (field breadcrumb
  /// audit trail, migration 0178). Foreground-only — closed in dispose.
  ProviderSubscription<AsyncValue<GnssSample>>? _recorderSub;

  @override
  void initState() {
    super.initState();
    _open();
  }

  void _open() {
    try {
      // The writable edit DB lives beside the read-only pack, keyed to the same dir
      // -- the store falls back to the pack manifest's snapshot_at for the base
      // version, so we needn't pre-read the manifest here.
      final store = FaultsStore.open(
        widget.pack.dataPath,
        p.join(widget.pack.dir, 'edits.sqlite'),
      )..addListener(_onChanged);
      // Stamp fault captures with the worker's position: a recorder on the shared
      // field session feeds store.actorLocation (no breadcrumb sink here — the trail
      // is recorded on the map screen; this only supplies the edit stamp).
      final recorder = FieldTrackRecorder(sessionId: ref.read(fieldSessionIdProvider));
      store.actorLocation = recorder.actorLocation;
      _recorderSub = ref.listenManual<AsyncValue<GnssSample>>(
        gnssStreamProvider,
        (prev, next) {
          final sample = next.valueOrNull;
          if (sample != null) recorder.onSample(sample);
        },
      );
      setState(() => _store = store);
      // Reconcile any ops left in flight from a prior session so a returning crew
      // sees applied/rejected without a manual sync. Fire-and-forget.
      if (store.inFlightOpIds().isNotEmpty) _pollStatuses();
    } catch (e) {
      setState(() => _error = e);
    }
  }

  void _onChanged() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _recorderSub?.close();
    _store?.removeListener(_onChanged);
    _store?.dispose();
    super.dispose();
  }

  // ---- Sync (the shared outbox) ---------------------------------------------

  /// Push the whole outbox (feature edits, WAM AND fault captures) in bounded
  /// batches, marking synced exactly what the server acked, then reconcile in-flight
  /// ops' apply outcomes. A push with nothing to send still polls.
  Future<void> _sync() async {
    final store = _store;
    if (store == null || _syncing) return;
    final ops = store.pendingOps();
    if (ops.isEmpty) {
      await _pollStatuses();
      return;
    }
    setState(() => _syncing = true);
    final inFlight = store.inFlightOpIds();
    final statuses = <SyncOpStatus>[];
    var accepted = 0;
    var failed = 0;
    String? firstError;
    try {
      final client = ref.read(fieldsyncClientProvider);
      for (var i = 0; i < ops.length; i += _syncBatchSize) {
        final batch = ops.sublist(i, math.min(i + _syncBatchSize, ops.length));
        final resp = await client.sync(batch, poll: i == 0 ? inFlight : const []);
        statuses.addAll(resp.statuses);
        final ok = [for (final r in resp.results) if (r.isAccepted) r.opId];
        await store.markSynced(ok);
        accepted += ok.length;
        failed += resp.results.length - ok.length;
        firstError ??= resp.results
            .where((r) => !r.isAccepted)
            .map((r) => r.error)
            .firstWhere((e) => e != null && e.isNotEmpty, orElse: () => null);
      }
      await store.recordApplyStatuses(statuses);
      if (!mounted) return;
      final rejected = statuses.where((s) => s.isRejected).length;
      final attn = rejected > 0
          ? ' · $rejected need${rejected == 1 ? 's' : ''} attention'
          : '';
      final msg = failed == 0
          ? 'Synced $accepted change${accepted == 1 ? '' : 's'}.$attn'
          : 'Synced $accepted, $failed failed'
              '${firstError != null ? ': $firstError' : ''}.$attn';
      _snack(msg);
    } on FieldsyncException catch (e) {
      final prefix = accepted > 0 ? 'Synced $accepted, then ' : '';
      _snack('${prefix}sync failed: ${e.message}');
    } catch (e) {
      final prefix = accepted > 0 ? 'Synced $accepted, then ' : '';
      _snack('${prefix}sync failed: $e');
    } finally {
      if (mounted) setState(() => _syncing = false);
    }
  }

  /// Reconcile in-flight ops (captured server-side but not yet known applied) via the
  /// standalone status pull. Quietly no-ops offline.
  Future<void> _pollStatuses() async {
    final store = _store;
    if (store == null) return;
    final inFlight = store.inFlightOpIds();
    if (inFlight.isEmpty) return;
    try {
      final statuses =
          await ref.read(fieldsyncClientProvider).syncStatus(inFlight);
      await store.recordApplyStatuses(statuses);
    } catch (_) {
      // Offline / transient -- leave in flight to reconcile next time.
    }
  }

  void _snack(String msg) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
  }

  /// The "needs attention" sheet: ops the apply worker rejected, each dismissable.
  Future<void> _showAttention() async {
    final store = _store;
    if (store == null) return;
    await showModalBottomSheet<void>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => StatefulBuilder(
        builder: (sheetContext, setSheet) {
          final attention = store.attentionOps();
          return SafeArea(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
                  child: Text(
                    attention.isEmpty
                        ? 'Nothing needs attention'
                        : '${attention.length} rejected',
                    style: Theme.of(sheetContext).textTheme.titleMedium,
                  ),
                ),
                for (final op in attention)
                  ListTile(
                    leading: const Icon(Icons.error_outline,
                        color: Color(0xFFFF453A)),
                    title: Text(_opKindLabel(op.kind)),
                    subtitle: Text(op.applyError?.isNotEmpty == true
                        ? op.applyError!
                        : 'Rejected by the server'),
                    trailing: TextButton(
                      onPressed: () async {
                        await store.dismissAttention(op.opId);
                        setSheet(() {});
                      },
                      child: const Text('Dismiss'),
                    ),
                  ),
                const SizedBox(height: 8),
              ],
            ),
          );
        },
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final store = _store;
    final pending = store?.pendingOps().length ?? 0;
    final attention = store?.attentionCount ?? 0;
    return Scaffold(
      appBar: AppBar(
        title: Text('Faults · ${shortId(widget.pack.id)}'),
        actions: [
          if (store != null && attention > 0 && !_syncing)
            Padding(
              padding: const EdgeInsets.only(right: 4),
              child: Badge(
                backgroundColor: const Color(0xFFFF453A),
                label: Text('$attention'),
                child: IconButton(
                  icon:
                      const Icon(Icons.error_outline, color: Color(0xFFFF453A)),
                  tooltip: '$attention change'
                      '${attention == 1 ? '' : 's'} need attention',
                  onPressed: _showAttention,
                ),
              ),
            ),
          if (store != null && (pending > 0 || _syncing))
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
                        tooltip: 'Sync $pending pending',
                        onPressed: _sync,
                      ),
                    ),
            ),
        ],
      ),
      body: _body(store),
    );
  }

  Widget _body(FaultsStore? store) {
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text('Could not open faults:\n$_error',
              textAlign: TextAlign.center,
              style: TextStyle(color: Theme.of(context).colorScheme.error)),
        ),
      );
    }
    if (store == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (!store.hasFaults) {
      return const _EmptyState(
        icon: Icons.bolt_outlined,
        title: 'No faults in this pack',
        message:
            'This pack was built without faults data. Rebuild it while signed in '
            'with faults access to bring open faults offline.',
      );
    }
    return _FaultBoardTab(store: store, onSaved: _offerSync);
  }

  /// Right after a fault is saved to the outbox, suggest sending it now -- a
  /// non-blocking nudge so a crew with a connection can push immediately, while
  /// "sync later" stays the default (ignore or dismiss it and the fault rides the
  /// next batch). This is the only push prompt: capture itself never auto-syncs.
  void _offerSync() {
    if (!mounted || _syncing) return;
    ScaffoldMessenger.of(context)
      ..clearSnackBars()
      ..showSnackBar(SnackBar(
        content: const Text('Fault raised offline.'),
        duration: const Duration(seconds: 6),
        action: SnackBarAction(label: 'Sync now', onPressed: _sync),
      ));
  }
}

// ---- Fault board tab --------------------------------------------------------

class _FaultBoardTab extends StatelessWidget {
  const _FaultBoardTab({required this.store, required this.onSaved});
  final FaultsStore store;

  /// Called after a capture lands in the outbox, so the parent screen (which owns
  /// the shared-outbox sync) can offer an immediate "Sync now" push.
  final VoidCallback onSaved;

  @override
  Widget build(BuildContext context) {
    final faults = store.faults();
    return Scaffold(
      body: faults.isEmpty
          ? const _EmptyState(
              icon: Icons.electrical_services_outlined,
              title: 'No open faults',
              message: 'Open faults on packed assets appear here. Raise a new one '
                  'with the button below.')
          : ListView.builder(
              padding: const EdgeInsets.symmetric(vertical: 8),
              itemCount: faults.length,
              itemBuilder: (context, i) =>
                  _FaultCard(store: store, fault: faults[i]),
            ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: () => _captureFlow(context),
        icon: const Icon(Icons.add),
        label: const Text('Fault'),
      ),
    );
  }

  /// Pick an asset from the pack (or choose "no asset"), then open the capture form.
  /// An orphan pick (empty asset) opens the form in location/incident-anchored mode.
  Future<void> _captureFlow(BuildContext context) async {
    final asset = await Navigator.of(context).push<_AssetRef>(
      MaterialPageRoute(
          builder: (_) => _AssetPickerScreen(store: store, allowOrphan: true)),
    );
    if (asset == null || !context.mounted) return;
    final orphan = asset.assetClass.isEmpty || asset.assetId.isEmpty;
    final saved = await Navigator.of(context).push<bool>(MaterialPageRoute(
      builder: (_) => FaultCaptureScreen(
        store: store,
        assetClass: orphan ? null : asset.assetClass,
        assetId: orphan ? null : asset.assetId,
        assetName: orphan ? null : asset.name,
      ),
    ));
    // A fault landed in the outbox -- let the screen offer an immediate push.
    if (saved == true) onSaved();
  }
}

class _FaultCard extends StatelessWidget {
  const _FaultCard({required this.store, required this.fault});
  final FaultsStore store;
  final FaultView fault;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final orphan = fault.assetId.isEmpty;
    final assetName = orphan
        ? (fault.longitude != null && fault.latitude != null
            ? 'No asset — pinned location'
            : (fault.incidentId?.isNotEmpty == true
                ? 'Incident-linked'
                : 'No asset'))
        : (store.assetById(fault.assetId)?.name ?? shortId(fault.assetId));
    final terminal = fault.status == 'cleared' || fault.status == 'closed';
    return Card(
      margin: const EdgeInsets.fromLTRB(12, 6, 12, 6),
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Expanded(
                  child: Text(
                    fault.title?.isNotEmpty == true
                        ? fault.title!
                        : (fault.faultType?.isNotEmpty == true
                            ? _faultTypeLabel(fault.faultType!)
                            : 'Fault'),
                    style: theme.textTheme.titleMedium
                        ?.copyWith(fontWeight: FontWeight.w600),
                  ),
                ),
                if (fault.pending)
                  const Padding(
                    padding: EdgeInsets.only(left: 6),
                    child: _PendingDot(),
                  ),
              ],
            ),
            const SizedBox(height: 6),
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                _Pill(
                    text: fault.status.isEmpty
                        ? 'Detected'
                        : fieldLabel(fault.status),
                    color: _faultStatusColor(fault.status)),
                if (fault.faultType?.isNotEmpty == true)
                  _Pill(
                      text: _faultTypeLabel(fault.faultType!),
                      color: theme.colorScheme.outline,
                      outline: true),
                if (fault.cause?.isNotEmpty == true)
                  _Pill(
                      text: fieldLabel(fault.cause!),
                      color: theme.colorScheme.outline,
                      outline: true),
                if (fault.isLocal)
                  _Pill(
                      text: 'Raised offline',
                      color: theme.colorScheme.outline,
                      outline: true),
              ],
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Icon(
                    orphan
                        ? Icons.wrong_location_outlined
                        : Icons.category_outlined,
                    size: 15,
                    color: theme.colorScheme.outline),
                const SizedBox(width: 6),
                Expanded(
                    child: Text(assetName,
                        style: TextStyle(color: theme.colorScheme.outline))),
              ],
            ),
            if (fault.reference?.isNotEmpty == true)
              _line(context, Icons.tag, fault.reference!),
            if (fault.description?.isNotEmpty == true) ...[
              const SizedBox(height: 6),
              Text(fault.description!),
            ],
            const SizedBox(height: 4),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                if (fault.canBookDefect && !fault.booked)
                  TextButton.icon(
                    onPressed: () => _bookDefect(context),
                    icon: const Icon(Icons.report_outlined, size: 18),
                    label: const Text('Book defect'),
                  ),
                if (!terminal)
                  TextButton(
                    onPressed: () => _statusMenu(context),
                    child: const Text('Update status'),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Widget _line(BuildContext context, IconData icon, String text) {
    final c = Theme.of(context).colorScheme.outline;
    return Padding(
      padding: const EdgeInsets.only(top: 3),
      child: Row(
        children: [
          Icon(icon, size: 15, color: c),
          const SizedBox(width: 6),
          Expanded(child: Text(text, style: TextStyle(color: c))),
        ],
      ),
    );
  }

  Future<void> _statusMenu(BuildContext context) async {
    final choice = await showModalBottomSheet<String>(
      context: context,
      showDragHandle: true,
      builder: (sheetContext) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final s in const ['investigating', 'cleared', 'closed'])
              ListTile(
                leading:
                    Icon(Icons.circle, size: 14, color: _faultStatusColor(s)),
                title: Text('Mark ${fieldLabel(s).toLowerCase()}'),
                onTap: () => Navigator.of(sheetContext).pop(s),
              ),
          ],
        ),
      ),
    );
    if (choice == null) return;
    await store.setFaultStatus(
      faultId: fault.id,
      status: choice,
      assetClass: fault.assetClass,
    );
  }

  Future<void> _bookDefect(BuildContext context) async {
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => FaultBookDefectScreen(store: store, fault: fault),
    ));
  }
}

// ---- Fault capture form -----------------------------------------------------

/// The offline fault-capture form (Faults module mobile). Fills a new
/// [FaultsStore.captureFault] -- a fault type, a short title, an optional
/// cause/description/notes/reference -- and queues a `fault_create` op. Opened either
/// against a known ASSET (prefilled from a picked feature) or, when [assetClass]/
/// [assetId] are null, in ORPHAN mode: the crew anchors the fault by capturing the
/// current GPS location OR an incident reference instead of naming an asset.
class FaultCaptureScreen extends StatefulWidget {
  const FaultCaptureScreen({
    super.key,
    required this.store,
    this.assetClass,
    this.assetId,
    this.assetName,
  });

  final FaultsStore store;
  final String? assetClass;
  final String? assetId;
  final String? assetName;

  /// True when no asset was given: the fault must be anchored by a GPS fix or an
  /// incident reference.
  bool get isOrphan =>
      (assetClass == null || assetClass!.isEmpty) ||
      (assetId == null || assetId!.isEmpty);

  @override
  State<FaultCaptureScreen> createState() => _FaultCaptureScreenState();
}

class _FaultCaptureScreenState extends State<FaultCaptureScreen> {
  final _formKey = GlobalKey<FormState>();
  final _title = TextEditingController();
  final _reference = TextEditingController();
  final _cause = TextEditingController();
  final _description = TextEditingController();
  final _notes = TextEditingController();
  final _incident = TextEditingController();
  final _location = LocationService();
  String _faultType = 'unknown';
  bool _saving = false;

  // Orphan-mode anchor: the captured GPS fix (lon/lat) and its progress/error.
  double? _lng;
  double? _lat;
  bool _locating = false;
  String? _locError;

  // The fault_type domain the server accepts (see the frozen contract).
  static const _faultTypes = [
    'SLG',
    'LL',
    'LLG',
    'LLL',
    'LLLG',
    'earth',
    'unknown',
  ];

  @override
  void initState() {
    super.initState();
    // For an orphan fault the location IS an anchor -- start a fix immediately so
    // it's ready by the time the form is filled (an incident ref is an alternative).
    if (widget.isOrphan) _captureLocation();
  }

  @override
  void dispose() {
    _title.dispose();
    _reference.dispose();
    _cause.dispose();
    _description.dispose();
    _notes.dispose();
    _incident.dispose();
    super.dispose();
  }

  Future<void> _captureLocation() async {
    setState(() {
      _locating = true;
      _locError = null;
    });
    final result = await _location.current();
    if (!mounted) return;
    setState(() {
      _locating = false;
      if (result.ok) {
        _lng = result.position!.longitude;
        _lat = result.position!.latitude;
        _locError = null;
      } else {
        _locError = result.error;
      }
    });
  }

  Future<void> _save() async {
    final form = _formKey.currentState;
    if (form == null || !form.validate()) return;
    final incident = _incident.text.trim();
    // An orphan fault cannot be saved without an anchor: a location OR an incident.
    if (widget.isOrphan &&
        (_lng == null || _lat == null) &&
        incident.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
          content: Text(
              'Capture a location or enter an incident before saving an orphan fault.')));
      return;
    }
    setState(() => _saving = true);
    try {
      await widget.store.captureFault(
        assetClass: widget.isOrphan ? null : widget.assetClass,
        assetId: widget.isOrphan ? null : widget.assetId,
        title: _title.text.trim(),
        reference: _reference.text.trim().isEmpty ? null : _reference.text.trim(),
        faultType: _faultType,
        cause: _cause.text.trim().isEmpty ? null : _cause.text.trim(),
        description:
            _description.text.trim().isEmpty ? null : _description.text.trim(),
        notes: _notes.text.trim().isEmpty ? null : _notes.text.trim(),
        longitude: _lng,
        latitude: _lat,
        incidentId: incident.isEmpty ? null : incident,
      );
      if (!mounted) return;
      // Pop with `true` so the board knows a fault landed in the outbox and can
      // offer an immediate push (the "Sync now" nudge). Capture still never
      // auto-pushes -- the suggestion is the board's, and declining it just
      // leaves the fault queued for a later batch sync.
      Navigator.of(context).pop(true);
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Could not raise fault: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Raise fault')),
      body: Form(
        key: _formKey,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            if (widget.isOrphan)
              _OrphanLocationCard(
                lng: _lng,
                lat: _lat,
                locating: _locating,
                error: _locError,
                onCapture: _captureLocation,
              )
            else
              _AssetHeader(
                assetClass: widget.assetClass!,
                name: widget.assetName ?? shortId(widget.assetId!),
              ),
            const SizedBox(height: 16),
            DropdownButtonFormField<String>(
              value: _faultType,
              isExpanded: true,
              decoration: _dec('Fault type'),
              items: [
                for (final t in _faultTypes)
                  DropdownMenuItem(value: t, child: Text(_faultTypeLabel(t))),
              ],
              onChanged: (v) => setState(() => _faultType = v ?? 'unknown'),
            ),
            const SizedBox(height: 12),
            TextFormField(
              controller: _title,
              decoration: _dec('Title', required: true),
              textCapitalization: TextCapitalization.sentences,
              validator: (v) =>
                  (v == null || v.trim().isEmpty) ? 'Required' : null,
            ),
            const SizedBox(height: 12),
            TextFormField(
              controller: _cause,
              decoration: _dec('Cause (e.g. tree, animal, equipment)'),
              textCapitalization: TextCapitalization.sentences,
            ),
            const SizedBox(height: 12),
            TextFormField(
              controller: _reference,
              decoration: _dec('Reference'),
            ),
            const SizedBox(height: 12),
            TextFormField(
              controller: _description,
              decoration: _dec('Description'),
              minLines: 2,
              maxLines: 5,
              textCapitalization: TextCapitalization.sentences,
            ),
            const SizedBox(height: 12),
            TextFormField(
              controller: _notes,
              decoration: _dec('Notes'),
              minLines: 1,
              maxLines: 4,
              textCapitalization: TextCapitalization.sentences,
            ),
            // An incident reference is an alternative anchor for an orphan fault (no
            // asset, no GPS fix) and useful context otherwise.
            if (widget.isOrphan) ...[
              const SizedBox(height: 12),
              TextFormField(
                controller: _incident,
                decoration: _dec('Incident id (alternative anchor)'),
              ),
            ],
            const SizedBox(height: 20),
            FilledButton(
              onPressed: _saving ? null : _save,
              child: _saving
                  ? const SizedBox(
                      width: 18,
                      height: 18,
                      child: CircularProgressIndicator(strokeWidth: 2))
                  : const Text('Save fault'),
            ),
          ],
        ),
      ),
    );
  }
}

// ---- Book defect from fault -------------------------------------------------

/// The offline "book a WAM defect from a fault" form. Fills
/// [FaultsStore.bookDefectFromFault] -- an optional title/type/severity/description
/// -- and queues a `fault_book_defect` op. The server books the defect against the
/// fault's asset/location and links it back, so this is only offered for a fault
/// that carries an asset or a location.
class FaultBookDefectScreen extends StatefulWidget {
  const FaultBookDefectScreen({
    super.key,
    required this.store,
    required this.fault,
  });

  final FaultsStore store;
  final FaultView fault;

  @override
  State<FaultBookDefectScreen> createState() => _FaultBookDefectScreenState();
}

class _FaultBookDefectScreenState extends State<FaultBookDefectScreen> {
  final _title = TextEditingController();
  final _type = TextEditingController();
  final _description = TextEditingController();
  String _severity = 'medium';
  bool _saving = false;

  @override
  void initState() {
    super.initState();
    // Seed the defect title from the fault's title -- the crew can override it.
    _title.text = widget.fault.title ?? '';
  }

  @override
  void dispose() {
    _title.dispose();
    _type.dispose();
    _description.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    setState(() => _saving = true);
    try {
      await widget.store.bookDefectFromFault(
        faultId: widget.fault.id,
        assetClass: widget.fault.assetClass,
        title: _title.text.trim().isEmpty ? null : _title.text.trim(),
        defectType: _type.text.trim().isEmpty ? null : _type.text.trim(),
        severity: _severity,
        description:
            _description.text.trim().isEmpty ? null : _description.text.trim(),
      );
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Defect booked from fault.')));
      Navigator.of(context).pop();
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Could not book defect: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Book defect')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          DropdownButtonFormField<String>(
            value: _severity,
            isExpanded: true,
            decoration: _dec('Severity'),
            items: [
              for (final s in const ['low', 'medium', 'high', 'critical'])
                DropdownMenuItem(value: s, child: Text(fieldLabel(s))),
            ],
            onChanged: (v) => setState(() => _severity = v ?? 'medium'),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _title,
            decoration: _dec('Title'),
            textCapitalization: TextCapitalization.sentences,
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _type,
            decoration: _dec('Type (e.g. corrosion, damage)'),
            textCapitalization: TextCapitalization.sentences,
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _description,
            decoration: _dec('Description'),
            minLines: 2,
            maxLines: 5,
            textCapitalization: TextCapitalization.sentences,
          ),
          const SizedBox(height: 20),
          FilledButton(
            onPressed: _saving ? null : _save,
            child: _saving
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2))
                : const Text('Book defect'),
          ),
        ],
      ),
    );
  }
}

// ---- Asset picker -----------------------------------------------------------

/// A picked asset reference: the bare (class, id) a fault keys off, plus a name.
class _AssetRef {
  const _AssetRef(this.assetClass, this.assetId, this.name);
  final String assetClass;
  final String assetId;
  final String? name;
}

/// Two-step asset picker over the packed features: pick a class, then a feature.
/// When [allowOrphan] is true the list is topped with a "no asset" option that pops
/// an empty [_AssetRef] -- the caller opens location/incident-anchored capture for it.
class _AssetPickerScreen extends StatelessWidget {
  const _AssetPickerScreen({required this.store, this.allowOrphan = false});
  final FaultsStore store;
  final bool allowOrphan;

  @override
  Widget build(BuildContext context) {
    final classes = store.assetClasses();
    return Scaffold(
      appBar: AppBar(title: const Text('Pick an asset')),
      body: ListView(
        children: [
          if (allowOrphan) ...[
            ListTile(
              leading: Icon(Icons.wrong_location_outlined,
                  color: Theme.of(context).colorScheme.primary),
              title: const Text('No asset — report by location'),
              subtitle: const Text(
                  'For an un-inventoried fault; pins your GPS position or an incident'),
              onTap: () =>
                  Navigator.of(context).pop(const _AssetRef('', '', null)),
            ),
            const Divider(height: 1),
          ],
          for (final c in classes)
            ListTile(
              leading: const Icon(Icons.category_outlined),
              title: Text(c.name?.isNotEmpty == true ? c.name! : c.code),
              subtitle: Text(c.code),
              trailing: Text('${c.count}'),
              onTap: () async {
                final ref = await Navigator.of(context).push<_AssetRef>(
                  MaterialPageRoute(
                    builder: (_) => _AssetFeatureList(store: store, code: c.code),
                  ),
                );
                if (ref != null && context.mounted) {
                  Navigator.of(context).pop(ref);
                }
              },
            ),
        ],
      ),
    );
  }
}

class _AssetFeatureList extends StatefulWidget {
  const _AssetFeatureList({required this.store, required this.code});
  final FaultsStore store;
  final String code;

  @override
  State<_AssetFeatureList> createState() => _AssetFeatureListState();
}

class _AssetFeatureListState extends State<_AssetFeatureList> {
  static const _pageSize = 200;
  final _rows = <FeatureRecord>[];
  bool _loading = false;
  bool _done = false;

  @override
  void initState() {
    super.initState();
    _loadMore();
  }

  void _loadMore() {
    if (_loading || _done) return;
    setState(() => _loading = true);
    final page = widget.store
        .assetsOfClass(widget.code, limit: _pageSize, offset: _rows.length);
    setState(() {
      _rows.addAll(page);
      _loading = false;
      if (page.length < _pageSize) _done = true;
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(widget.code)),
      body: NotificationListener<ScrollNotification>(
        onNotification: (n) {
          if (n.metrics.pixels >= n.metrics.maxScrollExtent - 400) _loadMore();
          return false;
        },
        child: ListView.builder(
          itemCount: _rows.length + (_done ? 0 : 1),
          itemBuilder: (context, i) {
            if (i >= _rows.length) {
              return const Padding(
                padding: EdgeInsets.all(16),
                child: Center(child: CircularProgressIndicator()),
              );
            }
            final f = _rows[i];
            return ListTile(
              dense: true,
              title: Text(f.name?.isNotEmpty == true ? f.name! : shortId(f.id)),
              subtitle:
                  Text(f.status != null ? fieldLabel(f.status!) : shortId(f.id)),
              trailing: const Icon(Icons.chevron_right),
              onTap: () => Navigator.of(context)
                  .pop(_AssetRef(f.featureClass, f.id, f.name)),
            );
          },
        ),
      ),
    );
  }
}

// ---- Shared bits ------------------------------------------------------------

class _AssetHeader extends StatelessWidget {
  const _AssetHeader({required this.assetClass, required this.name});
  final String assetClass;
  final String name;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(12),
      ),
      child: Row(
        children: [
          Icon(Icons.category_outlined, color: theme.colorScheme.outline),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(name, style: const TextStyle(fontWeight: FontWeight.w600)),
                Text(fieldLabel(assetClass),
                    style: theme.textTheme.bodySmall
                        ?.copyWith(color: theme.colorScheme.outline)),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// The orphan-fault anchor card: an orphan fault has no asset, so its location is a
/// primary way to tie it to the world. Shows the captured GPS fix (or the progress /
/// failure of getting one) with a re-capture button.
class _OrphanLocationCard extends StatelessWidget {
  const _OrphanLocationCard({
    required this.lng,
    required this.lat,
    required this.locating,
    required this.error,
    required this.onCapture,
  });

  final double? lng;
  final double? lat;
  final bool locating;
  final String? error;
  final VoidCallback onCapture;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final hasFix = lng != null && lat != null;
    final Color accent = error != null
        ? theme.colorScheme.error
        : (hasFix ? theme.colorScheme.primary : theme.colorScheme.outline);
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: accent.withValues(alpha: 0.5)),
      ),
      child: Row(
        children: [
          Icon(hasFix ? Icons.my_location : Icons.location_searching,
              color: accent),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                const Text('No asset — pinned location',
                    style: TextStyle(fontWeight: FontWeight.w600)),
                const SizedBox(height: 2),
                Text(
                  locating
                      ? 'Getting a GPS fix…'
                      : (error != null
                          ? error!
                          : (hasFix
                              ? '${lat!.toStringAsFixed(6)}, ${lng!.toStringAsFixed(6)}'
                              : 'No location yet')),
                  style: theme.textTheme.bodySmall?.copyWith(color: accent),
                ),
              ],
            ),
          ),
          if (locating)
            const Padding(
              padding: EdgeInsets.only(left: 8),
              child: SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(strokeWidth: 2)),
            )
          else
            IconButton(
              tooltip: hasFix ? 'Re-capture location' : 'Capture location',
              icon: const Icon(Icons.gps_fixed),
              onPressed: onCapture,
            ),
        ],
      ),
    );
  }
}

/// A small status/type pill.
class _Pill extends StatelessWidget {
  const _Pill({required this.text, required this.color, this.outline = false});
  final String text;
  final Color color;
  final bool outline;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
      decoration: BoxDecoration(
        color: outline ? Colors.transparent : color.withValues(alpha: 0.16),
        border: outline ? Border.all(color: color.withValues(alpha: 0.6)) : null,
        borderRadius: BorderRadius.circular(20),
      ),
      child: Text(
        text,
        style: TextStyle(
          color: color,
          fontSize: 12,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

/// The amber "not synced" dot.
class _PendingDot extends StatelessWidget {
  const _PendingDot();
  @override
  Widget build(BuildContext context) => const Icon(Icons.cloud_upload_outlined,
      size: 18, color: Color(0xFFFF9F0A));
}

class _EmptyState extends StatelessWidget {
  const _EmptyState(
      {required this.icon, required this.title, required this.message});
  final IconData icon;
  final String title;
  final String message;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(icon, size: 56, color: theme.colorScheme.outline),
            const SizedBox(height: 16),
            Text(title, style: theme.textTheme.titleMedium),
            const SizedBox(height: 8),
            Text(message,
                textAlign: TextAlign.center,
                style: TextStyle(color: theme.colorScheme.outline)),
          ],
        ),
      ),
    );
  }
}

InputDecoration _dec(String label, {bool required = false}) => InputDecoration(
      label: required
          ? Text.rich(TextSpan(text: label, children: const [
              TextSpan(text: ' *', style: TextStyle(color: Color(0xFFFF453A))),
            ]))
          : Text(label),
      filled: true,
      isDense: true,
      contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 14),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(10),
        borderSide: BorderSide.none,
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(10),
        borderSide: BorderSide.none,
      ),
    );

/// A friendly label for a fault outbox op kind (attention sheet).
String _opKindLabel(String kind) {
  switch (kind) {
    case 'fault_create':
      return 'Fault raised';
    case 'fault_status':
      return 'Fault status change';
    case 'fault_book_defect':
      return 'Defect booked from fault';
    case 'create':
      return 'New feature';
    case 'update_attrs':
      return 'Feature edit';
    case 'delete':
      return 'Feature delete';
    default:
      return fieldLabel(kind);
  }
}

/// A readable label for a fault_type code (the short-circuit codes aren't
/// title-case-friendly, so name them; everything else falls back to [fieldLabel]).
String _faultTypeLabel(String t) {
  switch (t) {
    case 'SLG':
      return 'SLG (single line–ground)';
    case 'LL':
      return 'LL (line–line)';
    case 'LLG':
      return 'LLG (line–line–ground)';
    case 'LLL':
      return 'LLL (three-phase)';
    case 'LLLG':
      return 'LLLG (three-phase–ground)';
    default:
      return fieldLabel(t);
  }
}

// ---- Colour maps ------------------------------------------------------------

Color _faultStatusColor(String s) {
  switch (s) {
    case 'investigating':
      return const Color(0xFF0A84FF);
    case 'cleared':
      return const Color(0xFF34C759);
    case 'closed':
      return const Color(0xFF8E8E93);
    case 'detected':
    default:
      return const Color(0xFFFF453A);
  }
}
