import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';

import '../api/models.dart';
import '../pack/pack_database.dart';
import '../pack/pack_store.dart';
import 'feature_detail_screen.dart';
import 'format.dart';

/// "My drafts": the crew member's OWN private drafts over a shared cell, from the
/// per-user overlay sidecar (shared-field-packs S3c). A shared base is published-only,
/// so the crew's in-progress (unpublished) features never ride the base pack -- they
/// arrive in the overlay instead, and this screen lets the crew SEE them offline,
/// opening each into the standard read-only detail view.
///
/// Read-only by design for this slice: a draft is a real server feature, so EDITING
/// one offline (an update op on the map) is a later slice -- here the crew inspects
/// their drafts' geometry + attributes alongside the base they picked up.
class DraftsScreen extends StatelessWidget {
  const DraftsScreen({super.key, required this.pack});

  final LocalPack pack;

  @override
  Widget build(BuildContext context) {
    final overlay = PackStore().loadOverlay(pack.dir);
    final drafts = overlay?.drafts ?? const <OverlayDraft>[];
    return Scaffold(
      appBar: AppBar(title: const Text('My drafts')),
      body: drafts.isEmpty
          ? const Center(
              child: Padding(
                padding: EdgeInsets.all(24),
                child: Text(
                  'No drafts of yours in this area.\n'
                  'Your unpublished features over this cell show up here.',
                  textAlign: TextAlign.center,
                ),
              ),
            )
          : ListView.builder(
              itemCount: drafts.length,
              itemBuilder: (context, i) {
                final d = drafts[i];
                final rec = _recordFromDraft(d);
                final geom = rec.geometry();
                return ListTile(
                  leading: const Icon(Icons.edit_note_outlined),
                  title: Text(
                      d.name?.isNotEmpty == true ? d.name! : shortId(d.id)),
                  subtitle: Text(
                    '${d.featureClass}'
                    '${geom != null ? ' · ${geom.type}' : ' · no geometry'}'
                    '${d.status != null ? ' · ${d.status}' : ''}',
                  ),
                  trailing: const Icon(Icons.chevron_right),
                  onTap: () => Navigator.of(context).push(
                    MaterialPageRoute(
                      builder: (_) => FeatureDetailScreen(feature: rec),
                    ),
                  ),
                );
              },
            ),
    );
  }

  /// Build a display record from an overlay draft. The draft ships OGC WKB (the same
  /// encoding a packed feature's geometry uses), so it decodes through [geomBytes] --
  /// no GeoJSON override needed. A missing/corrupt base64 degrades to no geometry.
  static FeatureRecord _recordFromDraft(OverlayDraft d) {
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
}
