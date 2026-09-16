import 'dart:io';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:path/path.dart' as p;

import '../api/fieldsync_client.dart';
import '../api/models.dart';
import '../attach/attachment_capture.dart';
import '../edit/edit_database.dart';
import '../geo/location_service.dart';
import '../pack/pack_database.dart';
import '../pack/pack_store.dart';
import '../providers.dart';
import '../wam/wam_models.dart';
import '../wam/wam_store.dart';
import 'format.dart';

/// The offline Work & Asset Management surface (Phase 59 mobile,
/// [[offline-wam-field-pack-and-sync]]): the crew's field face for the work board,
/// the defect backlog, and the inspection checklists a pack ships. It owns a
/// screen-scoped [WamStore] (its own read-only pack + writable edits.sqlite
/// handles, WAL-safe beside the map screen) and drives the same POST /sync
/// machinery the map's edit layer uses -- one flush pushes feature edits AND WAM
/// captures together.
///
/// Three tabs, one per surface: **Work** (my assigned orders, start/complete),
/// **Defects** (the backlog + capture a new one), **Inspections** (run a checklist
/// / recorded runs). Every capture rides the shared outbox, so the AppBar's sync
/// button flushes the lot.
class WamScreen extends ConsumerStatefulWidget {
  const WamScreen({super.key, required this.pack});
  final LocalPack pack;

  @override
  ConsumerState<WamScreen> createState() => _WamScreenState();
}

class _WamScreenState extends ConsumerState<WamScreen>
    with SingleTickerProviderStateMixin {
  /// Max ops pushed per POST /sync request -- mirrors PackMapPage._syncBatchSize
  /// so a large outbox never holds one request open over an unbounded op count.
  static const _syncBatchSize = 200;

  WamStore? _store;
  Object? _error;
  bool _syncing = false;
  late final TabController _tabs;

  @override
  void initState() {
    super.initState();
    _tabs = TabController(length: 3, vsync: this);
    _open();
  }

  void _open() {
    try {
      // The writable edit DB lives beside the read-only pack, keyed to the same
      // dir -- the store falls back to the pack manifest's snapshot_at for the
      // base version, so we needn't pre-read the manifest here.
      final store = WamStore.open(
        widget.pack.dataPath,
        p.join(widget.pack.dir, 'edits.sqlite'),
      )..addListener(_onChanged);
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
    _tabs.dispose();
    _store?.removeListener(_onChanged);
    _store?.dispose();
    super.dispose();
  }

  // ---- Sync (the shared outbox) ---------------------------------------------

  /// Push the whole outbox (feature edits AND WAM captures) in bounded batches,
  /// marking synced exactly what the server acked, then reconcile in-flight ops'
  /// apply outcomes. A push with nothing to send still polls.
  Future<void> _sync() async {
    final store = _store;
    if (store == null || _syncing) return;
    final ops = store.pendingOps();
    final photos = store.pendingAttachments();
    if (ops.isEmpty) {
      // Nothing to push -- but a defect's asset photos may still be queued (they
      // ride a separate pipeline from the outbox). Upload them, else just poll.
      if (photos.isNotEmpty) {
        await _syncAttachments(photos);
      } else {
        await _pollStatuses();
      }
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
      // Push any queued defect photos AFTER the outbox ops -- the defect's server
      // row lands first, then its asset photos register/upload/commit.
      var photosDone = 0;
      var photosFailed = 0;
      if (photos.isNotEmpty) {
        final r = await _uploadAttachments(store, photos, client);
        photosDone = r.$1;
        photosFailed = r.$2;
        firstError ??= r.$3;
      }
      if (!mounted) return;
      final rejected = statuses.where((s) => s.isRejected).length;
      final attn = rejected > 0
          ? ' · $rejected need${rejected == 1 ? 's' : ''} attention'
          : '';
      final photoNote = photosDone > 0 || photosFailed > 0
          ? ' · $photosDone photo${photosDone == 1 ? '' : 's'}'
              '${photosFailed > 0 ? ' ($photosFailed failed)' : ''}'
          : '';
      final msg = failed == 0
          ? 'Synced $accepted change${accepted == 1 ? '' : 's'}.$photoNote$attn'
          : 'Synced $accepted, $failed failed'
              '${firstError != null ? ': $firstError' : ''}.$photoNote$attn';
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

  /// Reconcile in-flight ops (captured server-side but not yet known applied) via
  /// the standalone status pull. Quietly no-ops offline.
  Future<void> _pollStatuses() async {
    final store = _store;
    if (store == null) return;
    final inFlight = store.inFlightOpIds();
    if (inFlight.isEmpty) return;
    try {
      final statuses = await ref.read(fieldsyncClientProvider).syncStatus(inFlight);
      await store.recordApplyStatuses(statuses);
    } catch (_) {
      // Offline / transient -- leave in flight to reconcile next time.
    }
  }

  /// Standalone photo push (used when the outbox is empty but photos are queued).
  Future<void> _syncAttachments(List<LocalAttachment> photos) async {
    final store = _store;
    if (store == null || _syncing || photos.isEmpty) return;
    setState(() => _syncing = true);
    try {
      final client = ref.read(fieldsyncClientProvider);
      final r = await _uploadAttachments(store, photos, client);
      if (!mounted) return;
      final msg = r.$2 == 0
          ? 'Uploaded ${r.$1} photo${r.$1 == 1 ? '' : 's'}.'
          : 'Uploaded ${r.$1}, ${r.$2} failed'
              '${r.$3 != null ? ': ${r.$3}' : ''}.';
      _snack(msg);
    } finally {
      if (mounted) setState(() => _syncing = false);
    }
  }

  /// Register -> PUT -> commit each queued photo, marking the ones that land synced.
  /// Returns (uploaded, failed, firstError). Caller owns the _syncing flag/snack.
  Future<(int, int, String?)> _uploadAttachments(
    WamStore store,
    List<LocalAttachment> photos,
    FieldsyncClient client,
  ) async {
    final uploaded = <String>[];
    String? firstError;
    for (final a in photos) {
      try {
        await client.uploadAttachment(a);
        uploaded.add(a.id);
      } catch (e) {
        firstError ??= '$e';
      }
    }
    await store.markAttachmentsSynced(uploaded);
    return (uploaded.length, photos.length - uploaded.length, firstError);
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
    final pending = (store?.pendingOps().length ?? 0) +
        (store?.pendingAttachmentCount ?? 0);
    final attention = store?.attentionCount ?? 0;
    return Scaffold(
      appBar: AppBar(
        title: Text('Work · ${shortId(widget.pack.id)}'),
        bottom: TabBar(
          controller: _tabs,
          tabs: const [
            Tab(text: 'Work', icon: Icon(Icons.assignment_outlined)),
            Tab(text: 'Defects', icon: Icon(Icons.report_outlined)),
            Tab(text: 'Inspections', icon: Icon(Icons.checklist_outlined)),
          ],
        ),
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

  Widget _body(WamStore? store) {
    if (_error != null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text('Could not open WAM:\n$_error',
              textAlign: TextAlign.center,
              style: TextStyle(color: Theme.of(context).colorScheme.error)),
        ),
      );
    }
    if (store == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (!store.hasWam) {
      return const _EmptyState(
        icon: Icons.work_off_outlined,
        title: 'No work in this pack',
        message:
            'This pack was built without Work & Asset Management data. Rebuild it '
            'while signed in with WAM access to bring your work board offline.',
      );
    }
    return TabBarView(
      controller: _tabs,
      children: [
        _WorkBoardTab(store: store),
        _DefectsTab(store: store),
        _InspectionsTab(store: store),
      ],
    );
  }
}

// ---- Work board tab ---------------------------------------------------------

class _WorkBoardTab extends StatelessWidget {
  const _WorkBoardTab({required this.store});
  final WamStore store;

  @override
  Widget build(BuildContext context) {
    final orders = store.workOrders()..sort(_byUrgency);
    if (orders.isEmpty) {
      return const _EmptyState(
        icon: Icons.assignment_turned_in_outlined,
        title: 'No assigned work',
        message: 'Work orders assigned to you ship with the pack and appear here.',
      );
    }
    return ListView.builder(
      padding: const EdgeInsets.symmetric(vertical: 8),
      itemCount: orders.length,
      itemBuilder: (context, i) => _WorkOrderCard(store: store, wo: orders[i]),
    );
  }

  /// In-progress first, then by priority (urgent→low), then earliest due.
  static int _byUrgency(WamWorkOrderView a, WamWorkOrderView b) {
    int rankStatus(String s) => s == 'in_progress' ? 0 : (s == 'completed' ? 2 : 1);
    final s = rankStatus(a.status).compareTo(rankStatus(b.status));
    if (s != 0) return s;
    final pr = _priorityRank(b.priority ?? 'medium')
        .compareTo(_priorityRank(a.priority ?? 'medium'));
    if (pr != 0) return pr;
    final ad = DateTime.tryParse(a.dueBy ?? '');
    final bd = DateTime.tryParse(b.dueBy ?? '');
    if (ad == null && bd == null) return 0;
    if (ad == null) return 1;
    if (bd == null) return -1;
    return ad.compareTo(bd);
  }
}

class _WorkOrderCard extends StatelessWidget {
  const _WorkOrderCard({required this.store, required this.wo});
  final WamStore store;
  final WamWorkOrderView wo;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final assetName = wo.assetId == null
        ? null
        : (store.assetById(wo.assetId!)?.name ?? shortId(wo.assetId!));
    final due = DateTime.tryParse(wo.dueBy ?? '');
    return Card(
      margin: const EdgeInsets.fromLTRB(12, 6, 12, 6),
      child: InkWell(
        borderRadius: BorderRadius.circular(16),
        onTap: () => Navigator.of(context).push(MaterialPageRoute(
          builder: (_) => _WorkOrderDetailScreen(store: store, workOrderId: wo.id),
        )),
        child: Padding(
          padding: const EdgeInsets.all(14),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Expanded(
                    child: Text(
                      wo.title?.isNotEmpty == true
                          ? wo.title!
                          : fieldLabel(wo.workType),
                      style: theme.textTheme.titleMedium
                          ?.copyWith(fontWeight: FontWeight.w600),
                    ),
                  ),
                  if (wo.pending)
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
                  _Pill(text: fieldLabel(wo.status), color: _woStatusColor(wo.status)),
                  _Pill(
                      text: fieldLabel(wo.priority ?? 'medium'),
                      color: _priorityColor(wo.priority ?? 'medium'),
                      outline: true),
                  _Pill(text: fieldLabel(wo.workType), color: theme.colorScheme.outline, outline: true),
                ],
              ),
              const SizedBox(height: 8),
              if (assetName != null)
                _line(context, Icons.category_outlined, assetName),
              if (wo.reference?.isNotEmpty == true)
                _line(context, Icons.tag, wo.reference!),
              if (due != null)
                _line(context, Icons.event_outlined, 'Due ${shortTime(due)}'),
            ],
          ),
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
}

/// A work order opened for action: start it, complete it (with notes), run an
/// inspection against its asset, or raise a defect on its asset.
class _WorkOrderDetailScreen extends StatelessWidget {
  const _WorkOrderDetailScreen({required this.store, required this.workOrderId});
  final WamStore store;
  final String workOrderId;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Work order')),
      // Rebuild on capture (start/complete/inspection) -- this is a pushed route,
      // separate from the tab the WamScreen rebuilds, so it listens itself.
      body: ListenableBuilder(
        listenable: store,
        builder: (context, _) {
          final wo = store.workOrderById(workOrderId);
          return wo == null
              ? const _EmptyState(
                  icon: Icons.help_outline,
                  title: 'Work order not found',
                  message: 'It may have been removed from the pack.')
              : _WorkOrderDetailBody(store: store, wo: wo);
        },
      ),
    );
  }
}

class _WorkOrderDetailBody extends StatelessWidget {
  const _WorkOrderDetailBody({required this.store, required this.wo});
  final WamStore store;
  final WamWorkOrderView wo;

  bool get _terminal => wo.status == 'completed' || wo.status == 'cancelled';

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final assetName = wo.assetId == null
        ? null
        : (store.assetById(wo.assetId!)?.name ?? shortId(wo.assetId!));
    final scheduled = DateTime.tryParse(wo.scheduledFor ?? '');
    final due = DateTime.tryParse(wo.dueBy ?? '');
    final started = DateTime.tryParse(wo.startedAt ?? '');
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Text(
          wo.title?.isNotEmpty == true ? wo.title! : fieldLabel(wo.workType),
          style: theme.textTheme.titleLarge?.copyWith(fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 10),
        Wrap(
          spacing: 6,
          runSpacing: 6,
          children: [
            _Pill(text: fieldLabel(wo.status), color: _woStatusColor(wo.status)),
            _Pill(
                text: fieldLabel(wo.priority ?? 'medium'),
                color: _priorityColor(wo.priority ?? 'medium'),
                outline: true),
            _Pill(text: fieldLabel(wo.workType), color: theme.colorScheme.outline, outline: true),
            if (wo.pending) const _Pill(text: 'Not synced', color: Color(0xFFFF9F0A), outline: true),
          ],
        ),
        const SizedBox(height: 16),
        if (wo.description?.isNotEmpty == true) ...[
          Text(wo.description!),
          const SizedBox(height: 16),
        ],
        Card(
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (assetName != null) _kv(context, 'Asset', assetName),
                if (wo.reference?.isNotEmpty == true) _kv(context, 'Reference', wo.reference!),
                if (wo.assignedTo?.isNotEmpty == true) _kv(context, 'Assigned', wo.assignedTo!),
                if (scheduled != null) _kv(context, 'Scheduled', shortTime(scheduled)),
                if (due != null) _kv(context, 'Due', shortTime(due)),
                if (started != null) _kv(context, 'Started', shortTime(started)),
                if (wo.completionNotes?.isNotEmpty == true)
                  _kv(context, 'Completion', wo.completionNotes!),
              ],
            ),
          ),
        ),
        const SizedBox(height: 20),
        // Actions ------------------------------------------------------------
        if (!_terminal) ...[
          if (wo.status != 'in_progress')
            FilledButton.icon(
              onPressed: () => _start(context),
              icon: const Icon(Icons.play_arrow),
              label: const Text('Start work'),
            ),
          if (wo.status != 'in_progress') const SizedBox(height: 8),
          FilledButton.icon(
            onPressed: () => _complete(context),
            icon: const Icon(Icons.check),
            label: const Text('Complete'),
          ),
          const SizedBox(height: 8),
          if (wo.assetId != null && wo.assetClass != null)
            OutlinedButton.icon(
              onPressed: () => _runInspection(context),
              icon: const Icon(Icons.checklist),
              label: const Text('Run inspection'),
            ),
          if (wo.assetId != null && wo.assetClass != null) const SizedBox(height: 8),
          if (wo.assetId != null && wo.assetClass != null)
            OutlinedButton.icon(
              onPressed: () => _captureDefect(context),
              icon: const Icon(Icons.report_outlined),
              label: const Text('Raise defect'),
            ),
        ] else
          Text('This work order is ${fieldLabel(wo.status).toLowerCase()}.',
              style: TextStyle(color: theme.colorScheme.outline)),
      ],
    );
  }

  Future<void> _start(BuildContext context) async {
    await store.startWorkOrder(wo.id, assetClass: wo.assetClass ?? '');
    if (context.mounted) {
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Work started.')));
    }
  }

  Future<void> _complete(BuildContext context) async {
    final notes = await _promptText(context,
        title: 'Complete work order', label: 'Completion notes (optional)');
    if (notes == null) return; // cancelled
    await store.completeWorkOrder(wo.id,
        assetClass: wo.assetClass ?? '',
        completionNotes: notes.isEmpty ? null : notes);
    if (context.mounted) {
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Work completed.')));
      Navigator.of(context).pop();
    }
  }

  Future<void> _runInspection(BuildContext context) async {
    final template = await _pickTemplate(context, store, wo.assetClass);
    if (template == null || !context.mounted) return;
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => WamInspectionRunnerScreen(
        store: store,
        template: template,
        assetClass: wo.assetClass!,
        assetId: wo.assetId!,
        workOrderId: wo.id,
      ),
    ));
  }

  Future<void> _captureDefect(BuildContext context) async {
    // Most work orders name an asset; an asset-less order (allowed since 0101)
    // raises an orphan defect instead (the form captures a location).
    final hasAsset = (wo.assetClass?.isNotEmpty ?? false) &&
        (wo.assetId?.isNotEmpty ?? false);
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => WamDefectCaptureScreen(
        store: store,
        assetClass: hasAsset ? wo.assetClass : null,
        assetId: hasAsset ? wo.assetId : null,
        assetName: hasAsset ? store.assetById(wo.assetId!)?.name : null,
      ),
    ));
  }
}

// ---- Defects tab ------------------------------------------------------------

class _DefectsTab extends StatelessWidget {
  const _DefectsTab({required this.store});
  final WamStore store;

  @override
  Widget build(BuildContext context) {
    final defects = store.defects();
    return Scaffold(
      body: defects.isEmpty
          ? const _EmptyState(
              icon: Icons.verified_outlined,
              title: 'No defects',
              message: 'Open defects on packed assets appear here. Capture a new '
                  'one with the button below.')
          : ListView.builder(
              padding: const EdgeInsets.symmetric(vertical: 8),
              itemCount: defects.length,
              itemBuilder: (context, i) =>
                  _DefectCard(store: store, defect: defects[i]),
            ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: () => _captureFlow(context),
        icon: const Icon(Icons.add),
        label: const Text('Defect'),
      ),
    );
  }

  /// Pick an asset from the pack (or choose "no asset"), then open the capture
  /// form. An orphan pick (empty asset) opens the form in location-anchored mode.
  Future<void> _captureFlow(BuildContext context) async {
    final asset = await Navigator.of(context).push<_AssetRef>(
      MaterialPageRoute(
          builder: (_) => _AssetPickerScreen(store: store, allowOrphan: true)),
    );
    if (asset == null || !context.mounted) return;
    final orphan = asset.assetClass.isEmpty || asset.assetId.isEmpty;
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => WamDefectCaptureScreen(
        store: store,
        assetClass: orphan ? null : asset.assetClass,
        assetId: orphan ? null : asset.assetId,
        assetName: orphan ? null : asset.name,
      ),
    ));
  }
}

class _DefectCard extends StatelessWidget {
  const _DefectCard({required this.store, required this.defect});
  final WamStore store;
  final WamDefectView defect;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final orphan = defect.assetId.isEmpty;
    final assetName = orphan
        ? 'No asset — pinned location'
        : (store.assetById(defect.assetId)?.name ?? shortId(defect.assetId));
    final resolved = defect.status == 'resolved' || defect.status == 'dismissed';
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
                    defect.title?.isNotEmpty == true
                        ? defect.title!
                        : (defect.defectType?.isNotEmpty == true
                            ? fieldLabel(defect.defectType!)
                            : 'Defect'),
                    style: theme.textTheme.titleMedium
                        ?.copyWith(fontWeight: FontWeight.w600),
                  ),
                ),
                if (defect.pending) const Padding(
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
                _Pill(text: fieldLabel(defect.severity), color: _severityColor(defect.severity)),
                _Pill(text: fieldLabel(defect.status), color: _defectStatusColor(defect.status), outline: true),
                if (defect.isLocal)
                  _Pill(text: 'Captured offline', color: theme.colorScheme.outline, outline: true),
              ],
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Icon(orphan ? Icons.wrong_location_outlined : Icons.category_outlined,
                    size: 15, color: theme.colorScheme.outline),
                const SizedBox(width: 6),
                Expanded(child: Text(assetName, style: TextStyle(color: theme.colorScheme.outline))),
              ],
            ),
            if (defect.description?.isNotEmpty == true) ...[
              const SizedBox(height: 6),
              Text(defect.description!),
            ],
            if (!resolved) ...[
              const SizedBox(height: 4),
              Align(
                alignment: Alignment.centerRight,
                child: TextButton(
                  onPressed: () => _statusMenu(context),
                  child: const Text('Update status'),
                ),
              ),
            ],
          ],
        ),
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
            for (final s in const ['work_ordered', 'resolved', 'dismissed'])
              ListTile(
                leading: Icon(Icons.circle, size: 14, color: _defectStatusColor(s)),
                title: Text('Mark ${fieldLabel(s).toLowerCase()}'),
                onTap: () => Navigator.of(sheetContext).pop(s),
              ),
          ],
        ),
      ),
    );
    if (choice == null) return;
    await store.setDefectStatus(
      defectId: defect.id,
      status: choice,
      assetClass: defect.assetClass,
    );
  }
}

// ---- Inspections tab --------------------------------------------------------

class _InspectionsTab extends StatelessWidget {
  const _InspectionsTab({required this.store});
  final WamStore store;

  @override
  Widget build(BuildContext context) {
    final templates = store.inspectionTemplates();
    final recorded = store.recordedInspections();
    if (templates.isEmpty && recorded.isEmpty) {
      return const _EmptyState(
        icon: Icons.fact_check_outlined,
        title: 'No checklists',
        message: 'Active inspection checklists ship with the pack and appear here.',
      );
    }
    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 8),
      children: [
        if (templates.isNotEmpty)
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 8, 16, 4),
            child: Text('Checklists', style: TextStyle(fontWeight: FontWeight.w700)),
          ),
        for (final t in templates)
          ListTile(
            leading: const Icon(Icons.checklist_outlined),
            title: Text(t.label),
            subtitle: Text([
              if (t.assetClass?.isNotEmpty == true) fieldLabel(t.assetClass!) else 'Any asset',
              '${store.templateItems(t.id).length} items',
            ].join(' · ')),
            trailing: const Icon(Icons.chevron_right),
            onTap: () => _run(context, t),
          ),
        if (recorded.isNotEmpty) ...[
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 16, 16, 4),
            child: Text('Recorded', style: TextStyle(fontWeight: FontWeight.w700)),
          ),
          for (final insp in recorded)
            ListTile(
              leading: Icon(Icons.assignment_turned_in_outlined,
                  color: _overallColor(insp.overallResult ?? 'pass')),
              title: Text(store.assetById(insp.assetId)?.name ?? shortId(insp.assetId)),
              subtitle: Text([
                fieldLabel(insp.overallResult ?? 'pass'),
                shortTime(DateTime.tryParse(insp.inspectedAt ?? '') ?? insp.createdAt),
              ].join(' · ')),
              trailing: store.isPending(insp.id)
                  ? const _PendingDot()
                  : const Icon(Icons.cloud_done_outlined),
            ),
        ],
      ],
    );
  }

  Future<void> _run(BuildContext context, PackInspectionTemplate t) async {
    // A checklist scoped to a class picks an asset of that class; an any-asset one
    // opens the full picker.
    final asset = await Navigator.of(context).push<_AssetRef>(
      MaterialPageRoute(
        builder: (_) => _AssetPickerScreen(store: store, onlyClass: t.assetClass),
      ),
    );
    if (asset == null || !context.mounted) return;
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => WamInspectionRunnerScreen(
        store: store,
        template: t,
        assetClass: asset.assetClass,
        assetId: asset.assetId,
      ),
    ));
  }
}

// ---- Defect capture form ----------------------------------------------------

/// The offline defect-capture form (Phase 59 mobile). Fills a new
/// [WamStore.captureDefect] -- a severity, a short title, an optional
/// type/description/notes -- and queues a `wam_defect_create` op. Opened either
/// against a known ASSET (prefilled from a work order or a picked feature) or, when
/// [assetClass]/[assetId] are null, in ORPHAN mode: the field worker found an
/// un-inventoried problem (an unmapped pole, a stray bit of kit) and anchors it by
/// capturing the current GPS location instead of naming an asset.
class WamDefectCaptureScreen extends StatefulWidget {
  const WamDefectCaptureScreen({
    super.key,
    required this.store,
    this.assetClass,
    this.assetId,
    this.assetName,
  });

  final WamStore store;
  final String? assetClass;
  final String? assetId;
  final String? assetName;

  /// True when no asset was given: the defect must be anchored by a GPS fix.
  bool get isOrphan =>
      (assetClass == null || assetClass!.isEmpty) ||
      (assetId == null || assetId!.isEmpty);

  @override
  State<WamDefectCaptureScreen> createState() => _WamDefectCaptureScreenState();
}

class _WamDefectCaptureScreenState extends State<WamDefectCaptureScreen> {
  final _formKey = GlobalKey<FormState>();
  final _title = TextEditingController();
  final _type = TextEditingController();
  final _description = TextEditingController();
  final _notes = TextEditingController();
  final _location = LocationService();
  final _capture = AttachmentCapture();
  String _severity = 'medium';
  bool _saving = false;

  // Orphan-mode anchor: the captured GPS fix (lon/lat) and its progress/error.
  double? _lng;
  double? _lat;
  bool _locating = false;
  String? _locError;

  // Photos captured in THIS form session (asset-linked defects only). They attach
  // to the ASSET feature, not the defect row -- so on cancel we discard just these,
  // leaving any photos taken against the asset elsewhere untouched. [_saved] flips
  // true on a successful save so dispose keeps them.
  final List<String> _sessionPhotoIds = [];
  bool _saved = false;

  @override
  void initState() {
    super.initState();
    // For an orphan defect the location IS the anchor -- start a fix immediately so
    // it's ready by the time the form is filled.
    if (widget.isOrphan) _captureLocation();
  }

  @override
  void dispose() {
    // Discard photos captured in an abandoned draft (an orphan defect never has
    // any). Fire-and-forget: the store outlives this screen.
    if (!_saved) {
      for (final id in _sessionPhotoIds) {
        widget.store.discardAssetPhoto(id);
      }
    }
    _title.dispose();
    _type.dispose();
    _description.dispose();
    _notes.dispose();
    super.dispose();
  }

  /// Capture a photo for the defect's ASSET (camera or gallery), copy it into the
  /// pack's attachments dir, and queue it on the asset feature. Asset-linked
  /// defects only -- an orphan defect has no asset to hang a photo on.
  Future<void> _capturePhoto(CaptureSource source) async {
    if (widget.isOrphan) return;
    try {
      final captured = await _capture.capture(
        source: source,
        destDir: widget.store.attachmentsDir,
      );
      if (captured == null || !mounted) return;
      final id = await widget.store.addAssetPhoto(
        featureClass: widget.assetClass!,
        featureId: widget.assetId!,
        filename: captured.filename,
        contentType: captured.contentType,
        byteSize: captured.byteSize,
        localPath: captured.localPath,
      );
      _sessionPhotoIds.add(id);
      if (mounted) setState(() {});
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Could not add photo: $e')));
      }
    }
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
    // An orphan defect cannot be saved without its anchor.
    if (widget.isOrphan && (_lng == null || _lat == null)) {
      ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
          content: Text('Capture a location before saving an orphan defect.')));
      return;
    }
    setState(() => _saving = true);
    try {
      await widget.store.captureDefect(
        assetClass: widget.isOrphan ? null : widget.assetClass,
        assetId: widget.isOrphan ? null : widget.assetId,
        severity: _severity,
        title: _title.text.trim(),
        source: 'ad_hoc',
        longitude: _lng,
        latitude: _lat,
        defectType: _type.text.trim().isEmpty ? null : _type.text.trim(),
        description:
            _description.text.trim().isEmpty ? null : _description.text.trim(),
        notes: _notes.text.trim().isEmpty ? null : _notes.text.trim(),
      );
      _saved = true; // keep the captured photos -- they're part of this defect now
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('Defect captured.')));
      Navigator.of(context).pop();
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Could not capture defect: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Capture defect')),
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
            TextFormField(
              controller: _title,
              decoration: _dec('Title', required: true),
              textCapitalization: TextCapitalization.sentences,
              validator: (v) =>
                  (v == null || v.trim().isEmpty) ? 'Required' : null,
            ),
            const SizedBox(height: 12),
            TextFormField(
              controller: _type,
              decoration: _dec('Type (e.g. corrosion, leak)'),
              textCapitalization: TextCapitalization.sentences,
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
            // Photos attach to the asset feature, so an orphan defect (no asset)
            // can't carry any -- offer the section only for asset-linked defects.
            if (!widget.isOrphan) ...[
              const SizedBox(height: 16),
              _DefectPhotos(
                photos: widget.store.assetPhotos(widget.assetId!),
                sessionIds: _sessionPhotoIds,
                onCamera: () => _capturePhoto(CaptureSource.camera),
                onGallery: () => _capturePhoto(CaptureSource.gallery),
                onRemove: (id) async {
                  await widget.store.discardAssetPhoto(id);
                  _sessionPhotoIds.remove(id);
                  if (mounted) setState(() {});
                },
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
                  : const Text('Save defect'),
            ),
          ],
        ),
      ),
    );
  }
}

// ---- Inspection checklist runner --------------------------------------------

/// The offline inspection-checklist runner (Phase 59 mobile): walks the template's
/// ordered prompts, collecting a pass/fail, a number, or a note per item, then
/// records the inspection via [WamStore.recordInspection] (a `wam_inspection_record`
/// op). A failed pass/fail item carrying a fail severity auto-raises a defect on
/// sync. When launched from a work order it can complete that order in the same op.
class WamInspectionRunnerScreen extends StatefulWidget {
  const WamInspectionRunnerScreen({
    super.key,
    required this.store,
    required this.template,
    required this.assetClass,
    required this.assetId,
    this.workOrderId,
  });

  final WamStore store;
  final PackInspectionTemplate template;
  final String assetClass;
  final String assetId;
  final String? workOrderId;

  @override
  State<WamInspectionRunnerScreen> createState() =>
      _WamInspectionRunnerScreenState();
}

class _WamInspectionRunnerScreenState extends State<WamInspectionRunnerScreen> {
  late final List<PackInspectionTemplateItem> _items;
  final Map<String, bool?> _passed = {}; // pass_fail item id -> pass
  final Map<String, TextEditingController> _values = {}; // numeric/rating/text
  final Map<String, TextEditingController> _notes = {};
  final _overallNotes = TextEditingController();
  bool _completeWorkOrder = true;
  bool _saving = false;

  @override
  void initState() {
    super.initState();
    _items = widget.store.templateItems(widget.template.id);
    for (final it in _items) {
      if (it.responseType == 'numeric' ||
          it.responseType == 'rating' ||
          it.responseType == 'text') {
        _values[it.id] = TextEditingController();
      }
      _notes[it.id] = TextEditingController();
    }
  }

  @override
  void dispose() {
    for (final c in _values.values) {
      c.dispose();
    }
    for (final c in _notes.values) {
      c.dispose();
    }
    _overallNotes.dispose();
    super.dispose();
  }

  /// Every pass/fail item must be answered before the checklist can be recorded.
  bool get _complete => _items
      .where((it) => (it.responseType ?? 'pass_fail') == 'pass_fail')
      .every((it) => _passed[it.id] != null);

  Future<void> _save() async {
    setState(() => _saving = true);
    var anyFail = false;
    final responses = <WamInspectionItemResponse>[];
    for (final it in _items) {
      final rt = it.responseType ?? 'pass_fail';
      final note = _notes[it.id]!.text.trim();
      bool? passed;
      String? valueText;
      double? valueNumeric;
      if (rt == 'pass_fail') {
        passed = _passed[it.id];
        if (passed == false) anyFail = true;
      } else if (rt == 'text') {
        valueText = _values[it.id]!.text.trim().isEmpty
            ? null
            : _values[it.id]!.text.trim();
      } else {
        // numeric / rating
        valueNumeric = double.tryParse(_values[it.id]!.text.trim());
      }
      responses.add(WamInspectionItemResponse(
        templateItemId: it.id,
        position: it.position,
        prompt: it.prompt ?? '',
        responseType: rt,
        passed: passed,
        valueText: valueText,
        valueNumeric: valueNumeric,
        // A failed pass/fail item carries the item's fail severity so the server
        // raises a defect at the right severity.
        failSeverity: (passed == false) ? it.failSeverity : null,
        notes: note.isEmpty ? null : note,
      ));
    }
    try {
      await widget.store.recordInspection(
        assetClass: widget.assetClass,
        assetId: widget.assetId,
        items: responses,
        templateId: widget.template.id,
        workOrderId: widget.workOrderId,
        overallResult: anyFail ? 'fail' : 'pass',
        notes: _overallNotes.text.trim().isEmpty
            ? null
            : _overallNotes.text.trim(),
        completeWorkOrder: widget.workOrderId != null && _completeWorkOrder,
      );
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
        content: Text(anyFail
            ? 'Inspection recorded — a defect will be raised on sync.'
            : 'Inspection recorded.'),
      ));
      Navigator.of(context).pop();
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Could not record inspection: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    final assetName =
        widget.store.assetById(widget.assetId)?.name ?? shortId(widget.assetId);
    return Scaffold(
      appBar: AppBar(title: Text(widget.template.label)),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          _AssetHeader(assetClass: widget.assetClass, name: assetName),
          if (widget.template.description?.isNotEmpty == true) ...[
            const SizedBox(height: 12),
            Text(widget.template.description!,
                style: TextStyle(color: Theme.of(context).colorScheme.outline)),
          ],
          const SizedBox(height: 8),
          for (final it in _items) _itemControl(it),
          const SizedBox(height: 12),
          TextField(
            controller: _overallNotes,
            decoration: _dec('Overall notes'),
            minLines: 2,
            maxLines: 4,
            textCapitalization: TextCapitalization.sentences,
          ),
          if (widget.workOrderId != null) ...[
            const SizedBox(height: 8),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('Complete the work order'),
              value: _completeWorkOrder,
              onChanged: (v) => setState(() => _completeWorkOrder = v),
            ),
          ],
          const SizedBox(height: 12),
          FilledButton(
            onPressed: (_saving || !_complete) ? null : _save,
            child: _saving
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2))
                : Text(_complete
                    ? 'Record inspection'
                    : 'Answer all checks to record'),
          ),
        ],
      ),
    );
  }

  Widget _itemControl(PackInspectionTemplateItem it) {
    final theme = Theme.of(context);
    final rt = it.responseType ?? 'pass_fail';
    return Card(
      margin: const EdgeInsets.symmetric(vertical: 6),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(it.prompt?.isNotEmpty == true ? it.prompt! : 'Item ${it.position}',
                style: const TextStyle(fontWeight: FontWeight.w600)),
            if (it.guidance?.isNotEmpty == true) ...[
              const SizedBox(height: 4),
              Text(it.guidance!,
                  style: theme.textTheme.bodySmall
                      ?.copyWith(color: theme.colorScheme.outline)),
            ],
            const SizedBox(height: 10),
            if (rt == 'pass_fail')
              _passFailControl(it)
            else if (rt == 'text')
              TextField(
                controller: _values[it.id],
                decoration: _dec('Answer'),
                textCapitalization: TextCapitalization.sentences,
              )
            else
              TextField(
                controller: _values[it.id],
                decoration: _dec(rt == 'rating' ? 'Rating' : 'Value'),
                keyboardType:
                    const TextInputType.numberWithOptions(decimal: true),
                inputFormatters: [
                  FilteringTextInputFormatter.allow(RegExp(r'[0-9.eE+-]')),
                ],
              ),
            const SizedBox(height: 8),
            TextField(
              controller: _notes[it.id],
              decoration: _dec('Note (optional)'),
              textCapitalization: TextCapitalization.sentences,
            ),
          ],
        ),
      ),
    );
  }

  Widget _passFailControl(PackInspectionTemplateItem it) {
    final v = _passed[it.id];
    return Row(
      children: [
        Expanded(
          child: ChoiceChip(
            label: const Text('Pass'),
            selected: v == true,
            selectedColor: const Color(0xFF34C759),
            onSelected: (_) => setState(() => _passed[it.id] = true),
          ),
        ),
        const SizedBox(width: 8),
        Expanded(
          child: ChoiceChip(
            label: Text(it.failSeverity?.isNotEmpty == true
                ? 'Fail (${fieldLabel(it.failSeverity!)})'
                : 'Fail'),
            selected: v == false,
            selectedColor: const Color(0xFFFF453A),
            onSelected: (_) => setState(() => _passed[it.id] = false),
          ),
        ),
      ],
    );
  }
}

// ---- Asset picker -----------------------------------------------------------

/// A picked asset reference: the bare (class, id) a defect/inspection keys off,
/// plus a display name.
class _AssetRef {
  const _AssetRef(this.assetClass, this.assetId, this.name);
  final String assetClass;
  final String assetId;
  final String? name;
}

/// Two-step asset picker over the packed features: pick a class, then a feature.
/// When [onlyClass] is set (a class-scoped checklist) the class step is skipped.
/// When [allowOrphan] is true (defect capture) the list is topped with a "no asset"
/// option that pops an empty [_AssetRef] -- the caller opens location-anchored
/// (orphan) capture for it.
class _AssetPickerScreen extends StatelessWidget {
  const _AssetPickerScreen(
      {required this.store, this.onlyClass, this.allowOrphan = false});
  final WamStore store;
  final String? onlyClass;
  final bool allowOrphan;

  @override
  Widget build(BuildContext context) {
    if (onlyClass != null && onlyClass!.isNotEmpty) {
      return _AssetFeatureList(store: store, code: onlyClass!);
    }
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
                  'For an un-inventoried problem; pins your GPS position'),
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
  final WamStore store;
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
    final page =
        widget.store.assetsOfClass(widget.code, limit: _pageSize, offset: _rows.length);
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
              subtitle: Text(f.status != null ? fieldLabel(f.status!) : shortId(f.id)),
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

/// The orphan-defect anchor card: an orphan defect has no asset, so its location
/// is the only thing that ties it to the world. Shows the captured GPS fix (or the
/// progress / failure of getting one) with a re-capture button.
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

/// The defect photo strip (asset-linked defects only). Photos hang on the asset
/// feature and ride the same attachment upload pipeline as the map's photos, so a
/// photo taken here shows on the asset everywhere. A camera/gallery button adds
/// one; a photo captured in this draft can be removed before saving.
class _DefectPhotos extends StatelessWidget {
  const _DefectPhotos({
    required this.photos,
    required this.sessionIds,
    required this.onCamera,
    required this.onGallery,
    required this.onRemove,
  });

  final List<LocalAttachment> photos;
  final List<String> sessionIds;
  final VoidCallback onCamera;
  final VoidCallback onGallery;
  final Future<void> Function(String id) onRemove;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Icon(Icons.photo_camera_outlined,
                size: 18, color: theme.colorScheme.outline),
            const SizedBox(width: 8),
            Text('Photos', style: theme.textTheme.titleSmall),
            const Spacer(),
            IconButton(
              tooltip: 'Take a photo',
              icon: const Icon(Icons.photo_camera),
              onPressed: onCamera,
            ),
            IconButton(
              tooltip: 'Choose from gallery',
              icon: const Icon(Icons.photo_library_outlined),
              onPressed: onGallery,
            ),
          ],
        ),
        if (photos.isEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 2, bottom: 4),
            child: Text(
              'Attach a photo of the defect on this asset.',
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.colorScheme.outline),
            ),
          )
        else
          SizedBox(
            height: 96,
            child: ListView.separated(
              scrollDirection: Axis.horizontal,
              itemCount: photos.length,
              separatorBuilder: (_, __) => const SizedBox(width: 8),
              itemBuilder: (context, i) {
                final a = photos[i];
                final removable = sessionIds.contains(a.id);
                return _PhotoThumb(
                  attachment: a,
                  onRemove: removable ? () => onRemove(a.id) : null,
                );
              },
            ),
          ),
      ],
    );
  }
}

class _PhotoThumb extends StatelessWidget {
  const _PhotoThumb({required this.attachment, this.onRemove});
  final LocalAttachment attachment;
  final VoidCallback? onRemove;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Stack(
      children: [
        ClipRRect(
          borderRadius: BorderRadius.circular(10),
          child: Image.file(
            File(attachment.localPath),
            width: 96,
            height: 96,
            fit: BoxFit.cover,
            errorBuilder: (_, __, ___) => Container(
              width: 96,
              height: 96,
              color: theme.colorScheme.surfaceContainerHighest,
              child: Icon(Icons.broken_image_outlined,
                  color: theme.colorScheme.outline),
            ),
          ),
        ),
        // An amber cloud until the photo uploads; a green check once synced.
        Positioned(
          left: 4,
          bottom: 4,
          child: Container(
            padding: const EdgeInsets.all(2),
            decoration: BoxDecoration(
              color: Colors.black.withValues(alpha: 0.45),
              borderRadius: BorderRadius.circular(6),
            ),
            child: Icon(
              attachment.isPending
                  ? Icons.cloud_upload_outlined
                  : Icons.cloud_done_outlined,
              size: 14,
              color: attachment.isPending
                  ? const Color(0xFFFF9F0A)
                  : const Color(0xFF34C759),
            ),
          ),
        ),
        if (onRemove != null)
          Positioned(
            top: 2,
            right: 2,
            child: GestureDetector(
              onTap: onRemove,
              child: Container(
                padding: const EdgeInsets.all(2),
                decoration: BoxDecoration(
                  color: Colors.black.withValues(alpha: 0.55),
                  shape: BoxShape.circle,
                ),
                child: const Icon(Icons.close, size: 16, color: Colors.white),
              ),
            ),
          ),
      ],
    );
  }
}

/// A small status/severity pill.
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

Widget _kv(BuildContext context, String k, String v) => Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 110,
            child: Text(k,
                style: TextStyle(color: Theme.of(context).colorScheme.outline)),
          ),
          Expanded(child: Text(v)),
        ],
      ),
    );

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

/// A friendly label for a WAM outbox op kind (attention sheet).
String _opKindLabel(String kind) {
  switch (kind) {
    case 'wam_defect_create':
      return 'Defect capture';
    case 'wam_defect_status':
      return 'Defect status change';
    case 'wam_workorder_update':
      return 'Work order update';
    case 'wam_inspection_record':
      return 'Inspection';
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

// ---- Colour maps ------------------------------------------------------------

Color _severityColor(String s) {
  switch (s) {
    case 'critical':
      return const Color(0xFFFF453A);
    case 'high':
      return const Color(0xFFFF9F0A);
    case 'medium':
      return const Color(0xFFFFD60A);
    case 'low':
    default:
      return const Color(0xFF34C759);
  }
}

Color _woStatusColor(String s) {
  switch (s) {
    case 'in_progress':
      return const Color(0xFF0A84FF);
    case 'completed':
      return const Color(0xFF34C759);
    case 'cancelled':
      return const Color(0xFF8E8E93);
    default: // draft / scheduled / assigned
      return const Color(0xFFFF9F0A);
  }
}

Color _defectStatusColor(String s) {
  switch (s) {
    case 'resolved':
      return const Color(0xFF34C759);
    case 'dismissed':
      return const Color(0xFF8E8E93);
    case 'work_ordered':
      return const Color(0xFF0A84FF);
    case 'open':
    default:
      return const Color(0xFFFF9F0A);
  }
}

Color _priorityColor(String s) {
  switch (s) {
    case 'urgent':
      return const Color(0xFFFF453A);
    case 'high':
      return const Color(0xFFFF9F0A);
    case 'low':
      return const Color(0xFF8E8E93);
    case 'medium':
    default:
      return const Color(0xFF0A84FF);
  }
}

Color _overallColor(String s) {
  switch (s) {
    case 'fail':
      return const Color(0xFFFF453A);
    case 'conditional':
      return const Color(0xFFFF9F0A);
    case 'pass':
    default:
      return const Color(0xFF34C759);
  }
}

/// Rank for sorting priorities (higher = more urgent).
int _priorityRank(String s) {
  switch (s) {
    case 'urgent':
      return 3;
    case 'high':
      return 2;
    case 'medium':
      return 1;
    default:
      return 0;
  }
}

/// A tiny helper: prompt the crew for a line of text (completion notes), returning
/// the text, or null if cancelled.
Future<String?> _promptText(BuildContext context,
    {required String title, required String label}) async {
  final controller = TextEditingController();
  final result = await showDialog<String>(
    context: context,
    builder: (dialogContext) => AlertDialog(
      title: Text(title),
      content: TextField(
        controller: controller,
        autofocus: true,
        minLines: 1,
        maxLines: 4,
        decoration: _dec(label),
        textCapitalization: TextCapitalization.sentences,
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(dialogContext).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: () =>
              Navigator.of(dialogContext).pop(controller.text.trim()),
          child: const Text('Done'),
        ),
      ],
    ),
  );
  controller.dispose();
  return result;
}

/// Pick an active inspection checklist that applies to [assetClass] (a class-scoped
/// one or an any-asset one). Returns null if cancelled or none apply.
Future<PackInspectionTemplate?> _pickTemplate(
    BuildContext context, WamStore store, String? assetClass) async {
  final templates = store.inspectionTemplates(assetClass: assetClass);
  if (templates.isEmpty) {
    ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('No checklist applies to this asset.')));
    return null;
  }
  if (templates.length == 1) return templates.first;
  return showModalBottomSheet<PackInspectionTemplate>(
    context: context,
    showDragHandle: true,
    builder: (sheetContext) => SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          for (final t in templates)
            ListTile(
              leading: const Icon(Icons.checklist_outlined),
              title: Text(t.label),
              subtitle: t.description?.isNotEmpty == true
                  ? Text(t.description!)
                  : null,
              onTap: () => Navigator.of(sheetContext).pop(t),
            ),
        ],
      ),
    ),
  );
}
