import 'dart:convert';

import '../pack/pack_database.dart';

/// The MapLibre GL style that draws a pack's vector tiles offline.
///
/// One vector source ("pack") pointing at the in-app tile server, plus one draw
/// layer per feature class. tippecanoe wrote each class as its own tile layer
/// named by the class code (see internal/fieldpack/builder.go: `-L code:file`),
/// so a layer's `source-layer` is the class code and its geometry type decides
/// whether it's a fill / line / circle.
///
/// Z-order: polygons (fill + outline) at the bottom, then lines, then points on
/// top -- so a point never hides under a polygon it sits on. Feature ids ride in
/// the tile properties (`id`), which is what a tap resolves back to a SQLite row.
class PackStyle {
  /// The layer ids a tap should hit-test against (every class draw layer, incl.
  /// polygon fills + outlines). queryRenderedFeatures is scoped to these so the
  /// tap ignores the highlight overlay and background.
  final List<String> queryableLayerIds;

  /// The style document, ready to hand to MaplibreMap(styleString:).
  final String json;

  const PackStyle._(this.queryableLayerIds, this.json);

  static PackStyle build({
    required List<ClassMeta> classes,
    required String tileTemplate,
    required int minZoom,
    required int maxZoom,
  }) {
    final sources = <String, dynamic>{
      'pack': {
        'type': 'vector',
        'tiles': [tileTemplate],
        'minzoom': minZoom,
        'maxzoom': maxZoom,
      },
    };

    final queryable = <String>[];
    final fillLayers = <Map<String, dynamic>>[];
    final lineLayers = <Map<String, dynamic>>[];
    final circleLayers = <Map<String, dynamic>>[];

    for (final c in classes) {
      final color = _colorForCode(c.code);
      switch (c.geometryType) {
        case 'polygon':
          final fillId = 'pack-${c.code}';
          final outlineId = 'pack-${c.code}-outline';
          fillLayers.add({
            'id': fillId,
            'type': 'fill',
            'source': 'pack',
            'source-layer': c.code,
            'paint': {
              'fill-color': color,
              'fill-opacity': 0.25,
            },
          });
          fillLayers.add({
            'id': outlineId,
            'type': 'line',
            'source': 'pack',
            'source-layer': c.code,
            'paint': {
              'line-color': color,
              'line-width': 1.2,
            },
          });
          queryable.add(fillId);
          queryable.add(outlineId);
          break;
        case 'line':
          final id = 'pack-${c.code}';
          lineLayers.add({
            'id': id,
            'type': 'line',
            'source': 'pack',
            'source-layer': c.code,
            'layout': {
              'line-cap': 'round',
              'line-join': 'round',
            },
            'paint': {
              'line-color': color,
              'line-width': 2.0,
            },
          });
          queryable.add(id);
          break;
        case 'point':
        default:
          final id = 'pack-${c.code}';
          circleLayers.add({
            'id': id,
            'type': 'circle',
            'source': 'pack',
            'source-layer': c.code,
            'paint': {
              'circle-radius': 5.0,
              'circle-color': color,
              'circle-stroke-color': '#ffffff',
              'circle-stroke-width': 1.5,
            },
          });
          queryable.add(id);
          break;
      }
    }

    final layers = <Map<String, dynamic>>[
      {
        'id': 'background',
        'type': 'background',
        'paint': {'background-color': '#eef1f4'},
      },
      ...fillLayers,
      ...lineLayers,
      ...circleLayers,
    ];

    final doc = <String, dynamic>{
      'version': 8,
      'name': 'VoltEdge Field pack',
      // No sprite/glyphs: the pack has no labels or icons yet, so the style is
      // fully self-contained and needs zero network.
      'sources': sources,
      'layers': layers,
    };

    return PackStyle._(queryable, jsonEncode(doc));
  }
}

/// A deterministic, readable color per class code, so classes stay visually
/// distinct across sessions without a symbology table (that's a later slice).
String _colorForCode(String code) {
  const palette = [
    '#1f77b4', '#ff7f0e', '#2ca02c', '#d62728', '#9467bd',
    '#8c564b', '#e377c2', '#7f7f7f', '#bcbd22', '#17becf',
    '#3366cc', '#dc3912', '#109618', '#990099', '#0099c6',
  ];
  var h = 0;
  for (final unit in code.codeUnits) {
    h = (h * 31 + unit) & 0x7fffffff;
  }
  return palette[h % palette.length];
}
