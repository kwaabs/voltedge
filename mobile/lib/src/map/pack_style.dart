import 'dart:convert';

import '../pack/pack_database.dart';
import 'basemaps.dart';
import 'icon_atlas.dart';

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
  /// polygon fills + outlines, and every per-rule sub-layer). queryRenderedFeatures
  /// is scoped to these so the tap ignores the highlight overlay and background.
  final List<String> queryableLayerIds;

  /// The base MapLibre filter each draw layer carries, by layer id -- non-empty
  /// only for the per-rule sub-layers, whose filter selects the features whose
  /// most-specific rule is that layer's (see [_buckets]). PackMapView must COMPOSE
  /// this with its hide-moved-features filter rather than overwrite it, or a
  /// dash/zoom sub-layer would start matching every feature. Layers absent here
  /// (points, single-rule classes) carry no base filter.
  final Map<String, List<dynamic>> layerFilters;

  /// The style document, ready to hand to MaplibreMap(styleString:).
  final String json;

  /// The point-icon images this style's symbol layers reference by name. The
  /// style JSON only names them (in `icon-image` expressions); PackMapView must
  /// rasterize and register each via `controller.addImage` on style load, since
  /// MapLibre can't draw an icon it hasn't been handed the pixels for.
  final List<IconSpec> icons;

  const PackStyle._(
    this.queryableLayerIds,
    this.layerFilters,
    this.json,
    this.icons,
  );

  static PackStyle build({
    required List<ClassMeta> classes,
    required String tileTemplate,
    required int minZoom,
    required int maxZoom,
    Basemap? basemap,
    Map<String, List<SymbologyRule>> symbology = const {},
    Map<String, String> iconSvg = const {},
  }) {
    final sources = <String, dynamic>{
      'pack': {
        'type': 'vector',
        'tiles': [tileTemplate],
        'minzoom': minZoom,
        'maxzoom': maxZoom,
      },
    };
    // An optional raster basemap drawn UNDER the pack data (an online provider
    // like OSM / Carto). Absent for the offline "none" basemap, keeping the
    // style fully self-contained + network-free.
    final hasBasemap = basemap != null && !basemap.isNone;
    if (hasBasemap) {
      sources['basemap'] = <String, dynamic>{
        'type': 'raster',
        'tiles': [basemap.tileUrl],
        'tileSize': 256,
        'maxzoom': basemap.maxZoom,
        if (basemap.attribution.isNotEmpty) 'attribution': basemap.attribution,
      };
    }

    final queryable = <String>[];
    // Each per-rule sub-layer's own filter, by id, so PackMapView composes rather
    // than clobbers it when hiding moved/edited features.
    final layerFilters = <String, List<dynamic>>{};
    final fillLayers = <Map<String, dynamic>>[];
    final lineLayers = <Map<String, dynamic>>[];
    final pointLayers = <Map<String, dynamic>>[];
    // Distinct (shape, color) icons referenced by the point symbol layers,
    // deduped by name so the same square-in-blue isn't rasterized per class.
    final icons = <String, IconSpec>{};

    for (final c in classes) {
      // The class's default color when no rule sets one -- still per-class so
      // classes stay visually distinct even where symbology_rules are unseeded.
      final color = _colorForCode(c.code);
      // Rules most-specific-first, so a MapLibre `case` picks the first matching
      // branch = the most specific rule, mirroring resolveSymbology's ordering.
      final rules = _sortedRules(symbology[c.code] ?? const []);
      switch (c.geometryType) {
        case 'polygon':
          // One (fill + outline) pair per rule bucket, so a rule's dash + zoom
          // (neither of which MapLibre can data-drive within one layer) apply as
          // that sub-layer's constant paint + layer-level minzoom/maxzoom. The
          // default bucket keeps the base `pack-<code>` id.
          for (final b in _buckets(rules)) {
            final s = b.style;
            final fillId = 'pack-${c.code}${b.idSuffix}';
            final outlineId = '$fillId-outline';
            final fillColor = s?.fillColor ?? s?.color ?? color;
            final strokeColor = s?.strokeColor ?? s?.color ?? color;
            final width = s?.width ?? 1.2;
            final dash = _dashArray(s?.dash, width);
            fillLayers.add(_withZoom(b.rule, {
              'id': fillId,
              'type': 'fill',
              'source': 'pack',
              'source-layer': c.code,
              if (b.filter != null) 'filter': b.filter,
              'paint': {'fill-color': fillColor, 'fill-opacity': 0.25},
            }));
            fillLayers.add(_withZoom(b.rule, {
              'id': outlineId,
              'type': 'line',
              'source': 'pack',
              'source-layer': c.code,
              if (b.filter != null) 'filter': b.filter,
              'layout': {'line-cap': 'round', 'line-join': 'round'},
              'paint': {
                'line-color': strokeColor,
                'line-width': width,
                if (dash != null) 'line-dasharray': dash,
              },
            }));
            if (b.filter != null) {
              layerFilters[fillId] = b.filter!;
              layerFilters[outlineId] = b.filter!;
            }
            queryable.add(fillId);
            queryable.add(outlineId);
          }
          break;
        case 'line':
          // One line layer per rule bucket (see the polygon case). Each bucket is
          // a constant paint so its dash can be set (line-dasharray can't be a
          // data-driven `case`) and its zoom bounds ride on the layer.
          for (final b in _buckets(rules)) {
            final s = b.style;
            final id = 'pack-${c.code}${b.idSuffix}';
            final width = s?.width ?? 2.0;
            final dash = _dashArray(s?.dash, width);
            lineLayers.add(_withZoom(b.rule, {
              'id': id,
              'type': 'line',
              'source': 'pack',
              'source-layer': c.code,
              if (b.filter != null) 'filter': b.filter,
              'layout': {'line-cap': 'round', 'line-join': 'round'},
              'paint': {
                'line-color': s?.color ?? color,
                'line-width': width,
                if (dash != null) 'line-dasharray': dash,
              },
            }));
            if (b.filter != null) layerFilters[id] = b.filter!;
            queryable.add(id);
          }
          break;
        case 'point':
        default:
          final id = 'pack-${c.code}';
          // A symbol layer draws each point as its resolved shape (style_def
          // .icon_ref rasterized in icon_atlas.dart), not a generic circle. The
          // image is chosen per feature by a data-driven `icon-image` expression
          // over the same rule predicates as the colors; the referenced images
          // are collected in [icons] for PackMapView to register. A point with
          // no icon rule resolves to a circle in the class fallback color, so
          // every point still draws (and pre-symbology packs look as before).
          // Unlike dash, icon image + size CAN be data-driven, so a point keeps
          // ONE layer. Zoom can't ride inside that `case`, so it's honored only at
          // the class level -- the class-default rule's min/max_zoom becomes the
          // whole layer's bounds; per-subtype point zoom is a documented
          // limitation (see docs/phase-21-field-tool.md).
          pointLayers.add(_withZoom(_defaultRule(rules), {
            'id': id,
            'type': 'symbol',
            'source': 'pack',
            'source-layer': c.code,
            'layout': {
              'icon-image': _iconImageValue(rules, color, icons, iconSvg),
              // Base size brings the generously-sized raster (kIconDrawSize px)
              // down to a sensible on-screen size; a rule's scale multiplies it.
              'icon-size': _paintValue(
                rules,
                (s) => s.scale != null ? kIconBaseScale * s.scale! : null,
                kIconBaseScale,
              ),
              // Field data is dense; never drop an icon to de-clutter, and keep
              // it tappable -- placement must not hide a feature the user needs.
              'icon-allow-overlap': true,
              'icon-ignore-placement': true,
            },
          }));
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
      // The basemap raster sits just above the background and below all pack
      // data, so features always draw on top of the map.
      if (hasBasemap)
        {
          'id': 'basemap',
          'type': 'raster',
          'source': 'basemap',
        },
      ...fillLayers,
      ...lineLayers,
      ...pointLayers,
    ];

    final doc = <String, dynamic>{
      'version': 8,
      'name': 'VoltEdge Field pack',
      // No sprite/glyphs: the pack has no labels or icons yet. With no basemap
      // selected the style is fully self-contained + network-free; a basemap
      // adds the one raster source above.
      'sources': sources,
      'layers': layers,
    };

    return PackStyle._(
      queryable,
      layerFilters,
      jsonEncode(doc),
      icons.values.toList(),
    );
  }
}

/// One rendering bucket for a class: the features whose most-specific matching
/// rule is [rule] (or the class default / fallback when [rule] is null). [filter]
/// is the MapLibre expression selecting exactly those features -- null for a
/// class with a single default (no filter needed). [idSuffix] disambiguates the
/// bucket's layer id ('' for the default, '-r<i>' for a predicated rule).
class _Bucket {
  const _Bucket(this.idSuffix, this.filter, this.rule);
  final String idSuffix;
  final List<dynamic>? filter;
  final SymbologyRule? rule;
  StyleDef? get style => rule?.style;
}

/// Split a class's rules (most-specific-first) into mutually-exclusive rendering
/// buckets, one per predicated rule plus a default. A predicated rule's bucket
/// matches its predicate AND NOT any more-specific rule's predicate -- so a
/// feature is drawn by exactly the bucket of its most-specific matching rule,
/// mirroring web resolveSymbology's whole-rule-wins pick. The default bucket
/// (the class-level rule with no predicate, or the class fallback when none)
/// matches whatever no predicated rule claimed. This decomposition is what lets
/// each bucket be its own layer with a CONSTANT paint, so line-dasharray (which
/// MapLibre can't data-drive) and layer-level minzoom/maxzoom can vary per rule.
///
/// Limitation: a zoom-gated rule's layer simply hides its features at that zoom
/// (its bucket is the only layer that draws them), rather than falling through to
/// a less-specific rule the way the web does -- MapLibre can't gate a filter on
/// zoom, so the fall-through is unreachable. Documented in phase-21-field-tool.md.
List<_Bucket> _buckets(List<SymbologyRule> rules) {
  final buckets = <_Bucket>[];
  final earlier = <dynamic>[]; // predicates of more-specific rules seen so far
  SymbologyRule? defaultRule;
  for (var i = 0; i < rules.length; i++) {
    final pred = _predicate(rules[i]);
    if (pred == null) {
      defaultRule = rules[i]; // the class-level default (no match axes)
      continue;
    }
    final filter = earlier.isEmpty
        ? (pred as List<dynamic>)
        : <dynamic>['all', pred, for (final p in earlier) ['!', p]];
    buckets.add(_Bucket('-r$i', filter, rules[i]));
    earlier.add(pred);
  }
  // The default bucket keeps the base `pack-<code>` id and matches every feature
  // no predicated rule did; with no predicated rules it carries no filter at all.
  final defFilter = earlier.isEmpty
      ? null
      : <dynamic>['all', for (final p in earlier) ['!', p]];
  buckets.add(_Bucket('', defFilter, defaultRule));
  return buckets;
}

/// The class-level default rule (no match axes) among [rules], or null. Used to
/// hang a point layer's class-wide zoom bounds, since a point keeps one layer.
SymbologyRule? _defaultRule(List<SymbologyRule> rules) {
  for (final r in rules) {
    if (_predicate(r) == null) return r;
  }
  return null;
}

/// Add layer-level `minzoom`/`maxzoom` to [layer] from [rule]'s zoom bounds, if
/// any. MapLibre draws a layer for minzoom <= z < maxzoom, but the pack's bounds
/// (and the web) are INCLUSIVE on both ends, so max_zoom becomes maxzoom + 1 to
/// keep the feature visible AT its max zoom. Returns [layer] unchanged when the
/// rule sets no bounds.
Map<String, dynamic> _withZoom(SymbologyRule? rule, Map<String, dynamic> layer) {
  if (rule == null) return layer;
  if (rule.minZoom != null) layer['minzoom'] = rule.minZoom;
  if (rule.maxZoom != null) layer['maxzoom'] = rule.maxZoom! + 1;
  return layer;
}

/// A `line-dasharray` for a named dash pattern, expressed in units of line width
/// (MapLibre's dasharray unit) so a dash looks consistent as the width varies.
/// Mirrors web/src/lib/line-dash.ts's four patterns; "solid"/unknown/null return
/// null so the caller omits the property (a plain solid line). Dotted uses a
/// zero-length dash, which the layers' round line-cap renders as a round dot.
List<double>? _dashArray(String? dash, double width) {
  final w = width <= 0 ? 1.0 : width;
  switch (dash) {
    case 'dashed':
      return [8 / w, 6 / w];
    case 'dotted':
      return [0, 8 / w]; // 0-length dash + round cap = a dot every gap
    case 'dashdot':
      return [10 / w, 6 / w, 0, 6 / w];
    default:
      return null; // 'solid', null, or an unknown pattern -> a solid line
  }
}

/// The on-screen icon size the point symbol layers use as their base, before a
/// rule's own scale multiplies it. Paired with icon_atlas.dart's kIconDrawSize:
/// the icons are rasterized large for crispness, and this shrinks them back to a
/// touch-friendly ~26px on screen.
const double kIconBaseScale = 0.4;

/// Compiles a class's rules into an `icon-image` value: which registered image
/// (an icon_ref+color name from iconName) each feature draws. Mirrors
/// _paintValue's shape -- a constant when the class has a single default, else a
/// `case` whose branches are the icon-bearing rules most-specific-first. Every
/// branch (and the default) resolves to a real icon+color: a rule's icon_ref in
/// its color, or a plain circle in the class [fallbackColor] where a rule sets no
/// color. Each referenced image is recorded in [icons] -- carrying the pack's
/// DB-authored SVG for the ref when [iconSvg] has one, else null so the device
/// draws the built-in shape -- so PackMapView registers exactly the set the
/// expression names.
dynamic _iconImageValue(
  List<SymbologyRule> rules,
  String fallbackColor,
  Map<String, IconSpec> icons,
  Map<String, String> iconSvg,
) {
  String register(String? iconRef, String? color) {
    // A rule with no icon_ref draws a circle (the web's default shape).
    final ref = iconRef ?? 'circle';
    final c = color ?? fallbackColor;
    final name = iconName(ref, c);
    icons.putIfAbsent(name, () => IconSpec(name, ref, c, svg: iconSvg[ref]));
    return name;
  }

  final branches = <dynamic>[];
  var defaultName = register(null, null); // class fallback: circle in class color
  for (final r in rules) {
    final name = register(r.style.iconRef, r.style.color);
    final pred = _predicate(r);
    if (pred == null) {
      defaultName = name; // the class-level default rule
      continue;
    }
    branches.add(pred);
    branches.add(name);
  }
  if (branches.isEmpty) return defaultName;
  return ['case', ...branches, defaultName];
}

/// A class's symbology rules ordered most-specific-first (subtype+domain >
/// subtype > domain > default), ties broken by original order -- so a MapLibre
/// `case` built by iterating this list picks the first matching branch = the
/// most specific rule, exactly as web/src/lib/symbology.ts's resolveSymbology
/// scores it. A plain List.sort isn't guaranteed stable in Dart, so the original
/// index is the explicit tiebreaker.
List<SymbologyRule> _sortedRules(List<SymbologyRule> rules) {
  final indexed = [
    for (var i = 0; i < rules.length; i++) MapEntry(i, rules[i]),
  ];
  indexed.sort((a, b) {
    final s = b.value.specificity.compareTo(a.value.specificity);
    return s != 0 ? s : a.key.compareTo(b.key);
  });
  return [for (final e in indexed) e.value];
}

/// Compiles one paint property (e.g. line-color) from a class's rules into a
/// MapLibre value: a constant when no rule varies it, otherwise a `case`
/// expression whose branches are the rules that DO set it (most-specific-first,
/// so the first match wins). [pick] pulls the property out of a rule's StyleDef
/// (null = that rule doesn't set this property, so it's skipped and the feature
/// falls through to a less-specific rule or the [fallback]). A rule with no match
/// axes (the class-level default) sets the fallback value rather than a branch,
/// since it applies to every feature of the class.
///
/// Now used only for the point symbol layer (icon image + size), which CAN be
/// data-driven and so stays one layer; line/polygon color/width/dash instead ride
/// per-rule sub-layers (see [_buckets]). Point zoom bounds aren't honored per-rule
/// here -- MapLibre can't gate a data-driven branch on `zoom` -- only the class
/// default rule's bounds apply, at the layer level (a documented limitation; see
/// docs/phase-21-field-tool.md).
dynamic _paintValue(
  List<SymbologyRule> rules,
  Object? Function(StyleDef) pick,
  Object? fallback,
) {
  final branches = <dynamic>[];
  Object? defaultValue = fallback;
  for (final r in rules) {
    final v = pick(r.style);
    if (v == null) continue;
    final pred = _predicate(r);
    if (pred == null) {
      defaultValue = v; // the class-level default rule
      continue;
    }
    branches.add(pred);
    branches.add(v);
  }
  if (branches.isEmpty) return defaultValue;
  return ['case', ...branches, defaultValue];
}

/// A MapLibre boolean expression that's true for a feature this rule matches, or
/// null for a rule with no match axes (the class-level default). subtype matches
/// the `subtype` tile prop; a domain rule matches its field's value (coerced to a
/// string, since domain codes are strings but a field's raw value may be numeric).
dynamic _predicate(SymbologyRule r) {
  final preds = <dynamic>[];
  if (r.subtypeCode != null) {
    preds.add(['==', ['get', 'subtype'], r.subtypeCode]);
  }
  if (r.domainField != null && r.domainValueCode != null) {
    preds.add([
      '==',
      ['to-string', ['get', r.domainField]],
      r.domainValueCode,
    ]);
  }
  if (preds.isEmpty) return null;
  if (preds.length == 1) return preds.first;
  return ['all', ...preds];
}

/// A deterministic, readable color per class code -- the fallback a class uses
/// where symbology_rules don't set a color (unseeded class, or a rule that only
/// sets width/dash), so classes stay visually distinct regardless.
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
