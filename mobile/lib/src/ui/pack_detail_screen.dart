import 'package:flutter/material.dart';

import '../pack/pack_database.dart';
import '../pack/pack_store.dart';
import 'faults_screen.dart';
import 'feature_detail_screen.dart';
import 'format.dart';
import 'pack_map_page.dart';
import 'wam_screen.dart';

/// Opens a downloaded pack's data.sqlite and shows what's inside: the manifest
/// facts, the graph-table row counts, and the per-class feature list. Proves the
/// whole server→device contract end to end without the map (that's Slice 2b).
class PackDetailScreen extends StatefulWidget {
  const PackDetailScreen({super.key, required this.pack});
  final LocalPack pack;

  @override
  State<PackDetailScreen> createState() => _PackDetailScreenState();
}

class _PackDetailScreenState extends State<PackDetailScreen> {
  PackDatabase? _db;
  Object? _error;

  Map<String, String> _manifest = const {};
  List<ClassCount> _classes = const [];
  int _connCount = 0;
  int _relCount = 0;

  @override
  void initState() {
    super.initState();
    _open();
  }

  void _open() {
    try {
      final db = PackDatabase.open(widget.pack.dataPath);
      setState(() {
        _db = db;
        _manifest = db.manifest();
        _classes = db.classCounts();
        _connCount = db.countTable('connectivity');
        _relCount = db.countTable('relationships');
      });
    } catch (e) {
      setState(() => _error = e);
    }
  }

  @override
  void dispose() {
    _db?.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text('Pack ${shortId(widget.pack.id)}'),
        actions: [
          // The offline WAM surface (work board / defects / inspections), shown
          // only when this pack shipped WAM read-context (a wam:read build).
          if (_db?.packsWam == true)
            IconButton(
              icon: const Icon(Icons.assignment_outlined),
              tooltip: 'Work & assets',
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => WamScreen(pack: widget.pack),
                ),
              ),
            ),
          // The offline Faults surface (fault board / raise / status / book defect),
          // shown only when this pack shipped faults read-context (a faults:read
          // build). Orthogonal to WAM -- a pack can carry faults, WAM, both, neither.
          if (_db?.packsFaults == true)
            IconButton(
              icon: const Icon(Icons.bolt_outlined),
              tooltip: 'Faults',
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => FaultsScreen(pack: widget.pack),
                ),
              ),
            ),
          IconButton(
            icon: const Icon(Icons.map_outlined),
            tooltip: 'Open map',
            onPressed: () => Navigator.of(context).push(
              MaterialPageRoute(
                builder: (_) => PackMapPage(pack: widget.pack),
              ),
            ),
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
          : ListView(
              children: [
                _summaryCard(context),
                const Padding(
                  padding: EdgeInsets.fromLTRB(16, 8, 16, 8),
                  child: Text('Feature classes',
                      style: TextStyle(fontWeight: FontWeight.w700)),
                ),
                for (final c in _classes)
                  ListTile(
                    leading: const Icon(Icons.category_outlined),
                    title: Text(c.name?.isNotEmpty == true ? c.name! : c.code),
                    subtitle: Text(c.code),
                    trailing: Text('${c.count}',
                        style: Theme.of(context).textTheme.titleMedium),
                    onTap: _db == null
                        ? null
                        : () => Navigator.of(context).push(
                              MaterialPageRoute(
                                builder: (_) => FeatureListScreen(
                                  db: _db!,
                                  code: c.code,
                                  displayName: c.name,
                                  total: c.count,
                                ),
                              ),
                            ),
                  ),
              ],
            ),
    );
  }

  Widget _summaryCard(BuildContext context) {
    final snapshot = parseIsoOrNull(_manifest['snapshot_at']);
    return Card(
      margin: const EdgeInsets.all(12),
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            _kv(context, 'Features', _manifest['feature_count'] ?? '?'),
            _kv(context, 'Connectivity', '$_connCount edges'),
            _kv(context, 'Relationships', '$_relCount links'),
            _kv(context, 'Schema version', _manifest['schema_version'] ?? '?'),
            if (snapshot != null) _kv(context, 'Snapshot', shortTime(snapshot)),
            _kv(context, 'Bbox', _manifest['bbox'] ?? '?'),
          ],
        ),
      ),
    );
  }

  Widget _kv(BuildContext context, String k, String v) => Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 130,
              child: Text(k,
                  style: TextStyle(
                      color: Theme.of(context).colorScheme.outline)),
            ),
            Expanded(child: Text(v)),
          ],
        ),
      );
}

DateTime? parseIsoOrNull(String? s) =>
    (s == null || s.isEmpty) ? null : DateTime.tryParse(s);

/// A paged list of one class's features, read straight from the pack.
class FeatureListScreen extends StatefulWidget {
  const FeatureListScreen({
    super.key,
    required this.db,
    required this.code,
    required this.displayName,
    required this.total,
  });

  final PackDatabase db;
  final String code;
  final String? displayName;
  final int total;

  @override
  State<FeatureListScreen> createState() => _FeatureListScreenState();
}

class _FeatureListScreenState extends State<FeatureListScreen> {
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
    final page = widget.db
        .featuresOfClass(widget.code, limit: _pageSize, offset: _rows.length);
    setState(() {
      _rows.addAll(page);
      _loading = false;
      if (page.length < _pageSize) _done = true;
    });
  }

  @override
  Widget build(BuildContext context) {
    final title = widget.displayName?.isNotEmpty == true
        ? widget.displayName!
        : widget.code;
    return Scaffold(
      appBar: AppBar(title: Text('$title (${widget.total})')),
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
            final geom = f.geometry();
            return ListTile(
              dense: true,
              title: Text(f.name?.isNotEmpty == true ? f.name! : shortId(f.id)),
              subtitle: Text(
                '${geom?.type ?? 'no geometry'}'
                '${f.status != null ? ' · ${f.status}' : ''}',
              ),
              trailing: const Icon(Icons.chevron_right),
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(
                  builder: (_) => FeatureDetailScreen(feature: f),
                ),
              ),
            );
          },
        ),
      ),
    );
  }
}
