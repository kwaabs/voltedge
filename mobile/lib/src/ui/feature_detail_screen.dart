import 'package:flutter/material.dart';

import '../pack/pack_database.dart';
import 'format.dart';
import 'gps_captures_section.dart';

/// One feature's decoded contents: the WKB geometry summary plus every
/// attribute. Extracted as a Scaffold-less view so the map (Slice 2b) can show
/// the very same record in a side panel (tablet) or bottom sheet (phone) without
/// a full page push.
class FeatureDetailView extends StatelessWidget {
  const FeatureDetailView({
    super.key,
    required this.feature,
    this.connections = const [],
    this.relationships = const [],
    this.onNavigate,
    this.attributesOverride,
    this.pending = false,
    this.deleted = false,
    this.padding,
    this.attachments,
  });

  final FeatureRecord feature;

  /// The Photos section (Phase 22 Slice 5), built by the map page from the pack's
  /// packed thumbnails + the local capture outbox. Null on the read-only browse
  /// page (no edit store there), which simply omits it.
  final Widget? attachments;

  /// When set, the attribute values to display instead of the feature's own
  /// (the pack values with any local edit delta merged over them, from
  /// EditStore.mergedAttributes). Null = show the packed values as-is.
  final Map<String, dynamic>? attributesOverride;

  /// True when the feature has an unsynced local edit -- surfaced as a chip so
  /// the user knows the values shown are their own pending changes.
  final bool pending;

  /// True when the feature is marked deleted offline (Phase 21 Slice 15) -- shown
  /// as a banner so the user knows this is a tombstone awaiting sync, and the
  /// attributes below are the last-known (soon to be soft-deleted) values.
  final bool deleted;

  /// Connectivity neighbours of [feature] (from PackDatabase.connectionsFor).
  final List<NeighborLink> connections;

  /// Relationship neighbours of [feature] (from PackDatabase.relationshipsFor).
  final List<NeighborLink> relationships;

  /// Tapping a neighbour row asks the parent to select that feature id, so the
  /// map re-highlights and the panel walks to it hop by hop. Null = non-tappable.
  final void Function(String featureId)? onNavigate;

  final EdgeInsetsGeometry? padding;

  @override
  Widget build(BuildContext context) {
    // The pack has no "geometry" field type (server field types are text/numeric/
    // integer/boolean/date/timestamp/uuid/jsonb), so a geometry showing up in the
    // attribute list is a text/jsonb field holding WKT/WKB/GeoJSON. Drop those by
    // value shape -- the decoded Geometry section above already covers geometry.
    final source = attributesOverride ?? feature.attributes;
    final attrs = source.entries
        .where((e) => !_looksLikeGeometry(e.value))
        .toList()
      ..sort((a, b) => a.key.compareTo(b.key));

    return ListView(
      padding: padding,
      children: [
        if (deleted) _deletedBanner(context) else if (pending) _pendingBanner(context),
        _section(context, 'Identity'),
        _kv(context, 'ID', feature.id),
        _kv(context, 'Class', feature.featureClass),
        if (feature.status != null) _kv(context, 'Status', feature.status!),
        if (feature.subtype != null) _kv(context, 'Subtype', '${feature.subtype}'),
        if (feature.territoryId != null)
          _kv(context, 'Territory', feature.territoryId!),
        _section(context, 'Attributes (${attrs.length})'),
        if (attrs.isEmpty)
          const Padding(
            padding: EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Text('No attributes.'),
          )
        else
          for (final e in attrs) _kv(context, fieldLabel(e.key), '${e.value}'),
        if (attachments != null) attachments!,
        // GNSS survey quality stamped on GPS placements (Phase gnss-quality Slice
        // 3b). Server-only rows, so this is a lazily-fetched, self-contained
        // online section -- collapsed by default, degrades cleanly offline.
        GpsCapturesSection(
          featureClass: feature.featureClass,
          featureId: feature.id,
        ),
        if (connections.isNotEmpty) ...[
          _section(context, 'Connections (${connections.length})'),
          for (final n in connections) _neighborRow(context, n),
        ],
        if (relationships.isNotEmpty) ...[
          _section(context, 'Relationships (${relationships.length})'),
          for (final n in relationships) _neighborRow(context, n),
        ],
        const SizedBox(height: 24),
      ],
    );
  }

  Widget _pendingBanner(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      margin: const EdgeInsets.fromLTRB(16, 12, 16, 0),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: scheme.tertiaryContainer,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        children: [
          Icon(Icons.sync_problem, size: 18, color: scheme.onTertiaryContainer),
          const SizedBox(width: 8),
          Expanded(
            child: Text('Edited offline — not yet synced',
                style: TextStyle(color: scheme.onTertiaryContainer)),
          ),
        ],
      ),
    );
  }

  Widget _deletedBanner(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      margin: const EdgeInsets.fromLTRB(16, 12, 16, 0),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: scheme.errorContainer,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Row(
        children: [
          Icon(Icons.delete_forever, size: 18, color: scheme.onErrorContainer),
          const SizedBox(width: 8),
          Expanded(
            child: Text('Marked deleted offline — soft-deleted on the server when '
                'you sync. Undo above to keep it.',
                style: TextStyle(color: scheme.onErrorContainer)),
          ),
        ],
      ),
    );
  }

  Widget _neighborRow(BuildContext context, NeighborLink n) {
    final title = n.name?.isNotEmpty == true ? n.name! : shortId(n.featureId);
    final subtitleParts = [
      n.featureClass,
      if (n.detail != null) n.detail!,
    ].where((s) => s.isNotEmpty).join(' · ');
    // Only tappable when the neighbour is actually in this pack -- a partly-scoped
    // pack can carry an edge whose far end wasn't packed (no row to navigate to).
    final resolvable = n.featureId.isNotEmpty && onNavigate != null;
    return ListTile(
      dense: true,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16),
      title: Text(title, overflow: TextOverflow.ellipsis),
      subtitle: subtitleParts.isEmpty ? null : Text(subtitleParts),
      trailing: resolvable
          ? const Icon(Icons.chevron_right, size: 20)
          : null,
      onTap: resolvable ? () => onNavigate!(n.featureId) : null,
    );
  }

  /// True when an attribute value is really a serialized geometry (WKT, WKB hex,
  /// or GeoJSON) rather than a normal field, so it can be hidden from Attributes.
  static bool _looksLikeGeometry(dynamic v) {
    if (v is Map) return v['type'] != null && v['coordinates'] != null;
    if (v is! String) return false;
    final s = v.trim();
    if (s.isEmpty) return false;
    if (_wktRe.hasMatch(s)) return true;
    if (s.contains('"coordinates"')) return true; // GeoJSON as a string
    // WKB hex: a long run of hex nibbles and no dashes (so a uuid never matches).
    if (!s.contains('-') && _wkbHexRe.hasMatch(s)) return true;
    return false;
  }

  static final RegExp _wktRe = RegExp(
    r'^\s*(SRID=\d+;)?\s*(POINT|LINESTRING|POLYGON|MULTIPOINT|'
    r'MULTILINESTRING|MULTIPOLYGON|GEOMETRYCOLLECTION)\s*[ZM]*\s*\(',
    caseSensitive: false,
  );
  static final RegExp _wkbHexRe = RegExp(r'^[0-9A-Fa-f]{18,}$');

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
