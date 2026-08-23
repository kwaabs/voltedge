import 'dart:convert';

import 'package:flutter/material.dart';

import '../map/pack_map_view.dart';
import '../pack/pack_database.dart';
import '../pack/pack_store.dart';
import 'feature_detail_screen.dart';
import 'format.dart';

/// The map view of a downloaded pack: renders tiles.mbtiles offline and puts the
/// tapped feature's SQLite record on screen. Responsive by width -- a master /
/// detail split on a tablet, a dismissable bottom sheet on a phone.
///
/// It owns the [PackDatabase] and the selection, then hands both down: the map
/// reports taps, this page decides what's selected, and the same
/// [FeatureDetailView] renders in whichever slot the layout picked.
class PackMapPage extends StatefulWidget {
  const PackMapPage({super.key, required this.pack});
  final LocalPack pack;

  @override
  State<PackMapPage> createState() => _PackMapPageState();
}

class _PackMapPageState extends State<PackMapPage> {
  // Below this width we're a phone (bottom sheet); at or above, a tablet split.
  static const _tabletBreakpoint = 720.0;

  PackDatabase? _db;
  Object? _error;
  List<double>? _bbox;
  FeatureRecord? _selected;

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
        _bbox = _bboxFromManifest(db.manifest());
      });
    } catch (e) {
      setState(() => _error = e);
    }
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
    _db?.close();
    super.dispose();
  }

  void _onFeatureTap(FeatureRecord f) => setState(() => _selected = f);
  void _clearSelection() => setState(() => _selected = null);

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text('Map · ${shortId(widget.pack.id)}')),
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
        selected: _selected,
        onFeatureTap: _onFeatureTap,
      );

  Widget _tabletLayout() {
    return Row(
      children: [
        Expanded(child: _map()),
        if (_selected != null)
          SizedBox(
            width: 360,
            child: Material(
              elevation: 2,
              child: Column(
                children: [
                  _panelHeader(_selected!),
                  Expanded(
                    child: FeatureDetailView(
                      feature: _selected!,
                      padding: const EdgeInsets.only(bottom: 24),
                    ),
                  ),
                ],
              ),
            ),
          ),
      ],
    );
  }

  Widget _phoneLayout() {
    return Stack(
      children: [
        Positioned.fill(child: _map()),
        if (_selected != null)
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            child: SafeArea(
              top: false,
              child: Material(
                elevation: 8,
                borderRadius:
                    const BorderRadius.vertical(top: Radius.circular(16)),
                clipBehavior: Clip.antiAlias,
                child: ConstrainedBox(
                  constraints: BoxConstraints(
                    maxHeight: MediaQuery.of(context).size.height * 0.45,
                  ),
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      _panelHeader(_selected!),
                      Flexible(
                        child: FeatureDetailView(
                          feature: _selected!,
                          padding: const EdgeInsets.only(bottom: 16),
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
      ],
    );
  }

  Widget _panelHeader(FeatureRecord f) {
    final title = f.name?.isNotEmpty == true ? f.name! : shortId(f.id);
    return Container(
      padding: const EdgeInsets.fromLTRB(16, 12, 8, 12),
      color: Theme.of(context).colorScheme.surfaceContainerHighest,
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title,
                    style: Theme.of(context).textTheme.titleMedium,
                    overflow: TextOverflow.ellipsis),
                Text(f.featureClass,
                    style: Theme.of(context).textTheme.bodySmall?.copyWith(
                        color: Theme.of(context).colorScheme.outline)),
              ],
            ),
          ),
          IconButton(
            icon: const Icon(Icons.close),
            tooltip: 'Clear selection',
            onPressed: _clearSelection,
          ),
        ],
      ),
    );
  }
}
