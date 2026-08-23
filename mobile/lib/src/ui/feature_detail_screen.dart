import 'package:flutter/material.dart';

import '../pack/pack_database.dart';
import 'format.dart';

/// One feature's decoded contents: the WKB geometry summary plus every
/// attribute. Extracted as a Scaffold-less view so the map (Slice 2b) can show
/// the very same record in a side panel (tablet) or bottom sheet (phone) without
/// a full page push.
class FeatureDetailView extends StatelessWidget {
  const FeatureDetailView({super.key, required this.feature, this.padding});

  final FeatureRecord feature;
  final EdgeInsetsGeometry? padding;

  @override
  Widget build(BuildContext context) {
    final geom = feature.geometry();
    final positions = geom?.positions() ?? const [];
    final attrs = feature.attributes.entries.toList()
      ..sort((a, b) => a.key.compareTo(b.key));

    return ListView(
      padding: padding,
      children: [
        _section(context, 'Identity'),
        _kv(context, 'ID', feature.id),
        _kv(context, 'Class', feature.featureClass),
        if (feature.status != null) _kv(context, 'Status', feature.status!),
        if (feature.subtype != null) _kv(context, 'Subtype', '${feature.subtype}'),
        if (feature.territoryId != null)
          _kv(context, 'Territory', feature.territoryId!),
        _section(context, 'Geometry'),
        if (geom == null)
          const Padding(
            padding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Text('No geometry / could not decode WKB.'),
          )
        else ...[
          _kv(context, 'Type', geom.type),
          _kv(context, 'Vertices', '${positions.length}'),
          if (positions.isNotEmpty)
            _kv(context, 'First vertex',
                '${positions.first[0].toStringAsFixed(6)}, '
                '${positions.first[1].toStringAsFixed(6)}'),
        ],
        _section(context, 'Attributes (${attrs.length})'),
        if (attrs.isEmpty)
          const Padding(
            padding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Text('No attributes.'),
          )
        else
          for (final e in attrs) _kv(context, e.key, '${e.value}'),
        const SizedBox(height: 24),
      ],
    );
  }

  static Widget _section(BuildContext context, String title) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 20, 16, 6),
        child: Text(title,
            style: Theme.of(context).textTheme.titleSmall?.copyWith(
                color: Theme.of(context).colorScheme.primary,
                fontWeight: FontWeight.w700)),
      );

  static Widget _kv(BuildContext context, String k, String v) => Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 130,
              child: Text(k,
                  style:
                      TextStyle(color: Theme.of(context).colorScheme.outline)),
            ),
            Expanded(child: SelectableText(v)),
          ],
        ),
      );
}

/// The full-page version (pushed from the browse list). The map surfaces the
/// same [FeatureDetailView] inline instead of pushing this.
class FeatureDetailScreen extends StatelessWidget {
  const FeatureDetailScreen({super.key, required this.feature});
  final FeatureRecord feature;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: Text(
          feature.name?.isNotEmpty == true ? feature.name! : shortId(feature.id),
        ),
      ),
      body: FeatureDetailView(feature: feature),
    );
  }
}
