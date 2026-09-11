/// The selectable basemaps that draw UNDER the pack's own vector data
/// (Phase 21 Slice 5c basemap). Mirrors the web app's list
/// (web/src/stores/basemap-store.ts + web/src/lib/basemaps.ts) so the field tool
/// and the web map offer the same providers: OpenStreetMap and Carto Voyager,
/// plus a "none" option that draws pack-only.
///
/// All are keyless raster XYZ tiles — no API key, no style JSON. "none" is the
/// default here (not on web) because the field tool is offline-first: a real
/// basemap needs a network, so it's opt-in. When a basemap IS selected and the
/// device is offline its tiles simply don't load; the pack still renders on top.
class Basemap {
  const Basemap({
    required this.id,
    required this.label,
    required this.attribution,
    this.tileUrl,
    this.maxZoom = 22,
  });

  final String id;
  final String label;

  /// One-line attribution, shown in the settings picker (and required by the
  /// providers' usage terms). Empty for [none].
  final String attribution;

  /// The raster XYZ template (`{z}/{x}/{y}`), or null for the no-basemap option.
  final String? tileUrl;

  /// The provider's max native zoom; MapLibre over-zooms (upscales) past it.
  final int maxZoom;

  bool get isNone => tileUrl == null;
}

/// The "no basemap" default — pack tiles only, fully offline.
const kDefaultBasemapId = 'none';

/// The basemap catalogue. ids `osm` / `carto-voyager` match the web app's ids;
/// `none` is the web's `blank` under a clearer name.
const kBasemaps = <Basemap>[
  Basemap(
    id: kDefaultBasemapId,
    label: 'None (pack only)',
    attribution: '',
  ),
  Basemap(
    id: 'osm',
    label: 'OpenStreetMap',
    attribution: '© OpenStreetMap contributors',
    tileUrl: 'https://tile.openstreetmap.org/{z}/{x}/{y}.png',
    maxZoom: 19,
  ),
  Basemap(
    id: 'carto-voyager',
    label: 'Carto Voyager',
    attribution: '© OpenStreetMap contributors, © CARTO',
    tileUrl: 'https://basemaps.cartocdn.com/rastertiles/voyager/{z}/{x}/{y}.png',
    maxZoom: 20,
  ),
];

/// Resolve an id to a basemap, falling back to [none] for an unknown/null id
/// (e.g. a value persisted before a provider was removed).
Basemap basemapById(String? id) {
  for (final b in kBasemaps) {
    if (b.id == id) return b;
  }
  return kBasemaps.first;
}
