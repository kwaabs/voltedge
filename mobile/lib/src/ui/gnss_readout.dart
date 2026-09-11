import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../geo/gnss_sample.dart';
import '../providers.dart';
import 'gnss_source_picker.dart';

/// Live GNSS quality readout (Phase: gnss-quality, Slice 2). A compact card shown
/// over the map while live tracking is on. It watches [gnssStreamProvider], which
/// starts the receiver the moment this widget mounts and releases it when the
/// parent unmounts it (the "off" toggle) -- so the GPS only runs while the crew is
/// actually looking at quality. Colour-codes the fix so survey-grade (RTK / low
/// DOP) reads apart from a rough autonomous fix at a glance.
///
/// DOP / satellite fields are null on a source without NMEA (an iPhone's internal
/// receiver), so the card falls back to the OS accuracy estimate and says so,
/// rather than showing zeros. See [[phase-gnss-quality]].
class GnssReadout extends ConsumerWidget {
  const GnssReadout({super.key, required this.onDisable});

  /// Turn live tracking OFF. The parent unmounts this widget in response, which
  /// disposes [gnssStreamProvider] and powers the receiver down.
  final VoidCallback onDisable;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(gnssStreamProvider);
    return ConstrainedBox(
      constraints: const BoxConstraints(maxWidth: 230),
      child: Material(
        elevation: 4,
        borderRadius: BorderRadius.circular(12),
        color: Theme.of(context).colorScheme.surface,
        child: Padding(
          padding: const EdgeInsets.fromLTRB(12, 8, 6, 10),
          child: async.when(
            loading: () => _shell(context, _acquiring(context)),
            error: (e, _) => _shell(context, _unavailable(context)),
            data: (s) => _shell(context, _body(context, s)),
          ),
        ),
      ),
    );
  }

  /// The header (title + power-off button) shared by every state, above [child].
  Widget _shell(BuildContext context, Widget child) {
    final theme = Theme.of(context);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Icon(Icons.satellite_alt_outlined,
                size: 16, color: theme.colorScheme.primary),
            const SizedBox(width: 6),
            Text('GPS quality',
                style: theme.textTheme.labelLarge
                    ?.copyWith(fontWeight: FontWeight.w600)),
            const Spacer(),
            // Pick the receiver (internal chip vs external Bluetooth, Slice 4).
            InkResponse(
              onTap: () => showGnssSourcePicker(context),
              radius: 18,
              child: Icon(Icons.settings_input_antenna,
                  size: 18, color: theme.colorScheme.onSurfaceVariant),
            ),
            const SizedBox(width: 8),
            InkResponse(
              onTap: onDisable,
              radius: 18,
              child: Icon(Icons.power_settings_new,
                  size: 18, color: theme.colorScheme.onSurfaceVariant),
            ),
          ],
        ),
        const SizedBox(height: 6),
        child,
      ],
    );
  }

  Widget _acquiring(BuildContext context) => Row(
        children: [
          const SizedBox(
            width: 14,
            height: 14,
            child: CircularProgressIndicator(strokeWidth: 2),
          ),
          const SizedBox(width: 10),
          Text('Acquiring…',
              style: Theme.of(context).textTheme.bodyMedium),
        ],
      );

  Widget _unavailable(BuildContext context) => Row(
        children: [
          const Icon(Icons.location_disabled, size: 16, color: _red),
          const SizedBox(width: 8),
          Expanded(
            child: Text('GPS unavailable — check location permission.',
                style: Theme.of(context).textTheme.bodySmall),
          ),
        ],
      );

  Widget _body(BuildContext context, GnssSample s) {
    final theme = Theme.of(context);
    final tier = _tierOf(s);
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // Fix badge + the headline quality figure (HDOP if we have it, else the
        // OS accuracy estimate).
        Row(
          children: [
            _dot(tier.color),
            const SizedBox(width: 6),
            Text(_fixLabel(s.fixType),
                style: theme.textTheme.bodyMedium
                    ?.copyWith(fontWeight: FontWeight.w600)),
            const Spacer(),
            Text(tier.label,
                style: theme.textTheme.labelSmall
                    ?.copyWith(color: tier.color, fontWeight: FontWeight.w600)),
          ],
        ),
        const SizedBox(height: 8),
        // DOP triple when the source reports it (Android internal / external).
        if (s.hasDop)
          Wrap(
            spacing: 6,
            runSpacing: 6,
            children: [
              if (s.pdop != null) _chip(context, 'PDOP', _fmt(s.pdop)),
              if (s.hdop != null) _chip(context, 'HDOP', _fmt(s.hdop)),
              if (s.vdop != null) _chip(context, 'VDOP', _fmt(s.vdop)),
            ],
          )
        else
          Text('DOP not reported by this receiver',
              style: theme.textTheme.bodySmall
                  ?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
        const SizedBox(height: 8),
        // Accuracy / satellites / altitude, each shown only when known.
        _kv(context, Icons.my_location, 'Accuracy',
            s.hAccuracy != null ? '±${_fmt1(s.hAccuracy)} m' : '—'),
        if (s.satsUsed != null || s.satsInView != null)
          _kv(context, Icons.satellite_alt, 'Satellites',
              _sats(s.satsUsed, s.satsInView)),
        // Per-constellation in-view breakdown (GPS / GLONASS / Galileo / ...),
        // shown when the receiver reports GSV per talker.
        if (s.satsByConstellation.isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 4, left: 22),
            child: Wrap(
              spacing: 6,
              runSpacing: 4,
              children: [
                for (final e in _constellationEntries(s.satsByConstellation))
                  _constChip(context, e.key, e.value),
              ],
            ),
          ),
        if (s.altitude != null)
          _kv(context, Icons.height, 'Altitude', '${_fmt0(s.altitude)} m'),
        const SizedBox(height: 4),
        Text(
          s.source == GnssSourceKind.external
              ? 'External receiver'
              : 'Internal receiver',
          style: theme.textTheme.labelSmall
              ?.copyWith(color: theme.colorScheme.onSurfaceVariant),
        ),
      ],
    );
  }

  // ---- small pieces ---------------------------------------------------------

  Widget _dot(Color c) => Container(
        width: 10,
        height: 10,
        decoration: BoxDecoration(color: c, shape: BoxShape.circle),
      );

  Widget _chip(BuildContext context, String label, String value) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Text('$label $value',
          style: theme.textTheme.labelMedium
              ?.copyWith(fontFeatures: const [FontFeature.tabularFigures()])),
    );
  }

  /// A compact per-constellation in-view chip, e.g. "GPS 11". Slightly smaller
  /// than the DOP chips so the breakdown reads as a secondary detail.
  Widget _constChip(BuildContext context, String talker, int count) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text('${_constellationLabel(talker)} $count',
          style: theme.textTheme.labelSmall
              ?.copyWith(fontFeatures: const [FontFeature.tabularFigures()])),
    );
  }

  Widget _kv(BuildContext context, IconData icon, String label, String value) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        children: [
          Icon(icon, size: 14, color: theme.colorScheme.onSurfaceVariant),
          const SizedBox(width: 8),
          Text(label, style: theme.textTheme.bodySmall),
          const Spacer(),
          Text(value,
              style: theme.textTheme.bodySmall?.copyWith(
                fontWeight: FontWeight.w600,
                fontFeatures: const [FontFeature.tabularFigures()],
              )),
        ],
      ),
    );
  }

  // ---- formatting / tiers ---------------------------------------------------

  static String _fmt(double? v) => v == null ? '—' : v.toStringAsFixed(1);
  static String _fmt1(double? v) => v == null ? '—' : v.toStringAsFixed(1);
  static String _fmt0(double? v) => v == null ? '—' : v.toStringAsFixed(0);

  static String _sats(int? used, int? inView) {
    if (used != null && inView != null) return '$used / $inView';
    if (used != null) return '$used used';
    if (inView != null) return '$inView in view';
    return '—';
  }

  /// NMEA talker id -> a short constellation label for the readout. Covers the
  /// GNSS systems a combined receiver reports; an unknown talker falls through to
  /// the raw two-letter id rather than being dropped. BeiDou is 'GB' in NMEA 4.10+
  /// but older receivers use 'BD', so both map to BDS.
  static String _constellationLabel(String talker) => switch (talker) {
        'GP' => 'GPS',
        'GL' => 'GLO',
        'GA' => 'GAL',
        'GB' || 'BD' => 'BDS',
        'GQ' => 'QZSS',
        'GI' => 'NavIC',
        'GN' => 'GNSS',
        _ => talker,
      };

  /// Preferred display order of the constellations; anything unlisted sorts last,
  /// then alphabetically, so the breakdown reads in a stable, familiar sequence.
  static const List<String> _constellationOrder = [
    'GP', 'GL', 'GA', 'GB', 'BD', 'GQ', 'GI',
  ];

  /// The non-empty per-constellation counts, ordered for display.
  static List<MapEntry<String, int>> _constellationEntries(
      Map<String, int> byTalker) {
    final entries =
        byTalker.entries.where((e) => e.value > 0).toList(growable: false);
    int rank(String k) {
      final i = _constellationOrder.indexOf(k);
      return i < 0 ? _constellationOrder.length : i;
    }

    entries.sort((a, b) {
      final byRank = rank(a.key).compareTo(rank(b.key));
      return byRank != 0 ? byRank : a.key.compareTo(b.key);
    });
    return entries;
  }

  static String _fixLabel(GnssFixType f) => switch (f) {
        GnssFixType.rtkFixed => 'RTK fixed',
        GnssFixType.rtkFloat => 'RTK float',
        GnssFixType.dgps => 'DGPS',
        GnssFixType.gps => 'GPS fix',
        GnssFixType.none => 'No fix',
        // No NMEA fix-type (iOS internal), but geolocator gave us a position.
        GnssFixType.unknown => 'GPS fix',
      };

  /// The overall quality tier, from HDOP when available (the receiver's own
  /// geometry figure), else the OS accuracy estimate. A "no fix" fix-type always
  /// wins as red regardless of the numbers.
  static _Tier _tierOf(GnssSample s) {
    if (s.fixType == GnssFixType.none) return _Tier.bad;
    if (s.fixType == GnssFixType.rtkFixed) return _Tier.excellent;
    final h = s.hdop;
    if (h != null) {
      if (h <= 2) return _Tier.excellent;
      if (h <= 5) return _Tier.good;
      if (h <= 10) return _Tier.moderate;
      return _Tier.bad;
    }
    final a = s.hAccuracy;
    if (a != null) {
      if (a <= 3) return _Tier.excellent;
      if (a <= 8) return _Tier.good;
      if (a <= 20) return _Tier.moderate;
      return _Tier.bad;
    }
    return _Tier.unknown;
  }
}

// iOS-system-matched signal colours (kept local so the readout matches the map's
// semantic palette without importing chrome theme).
const _green = Color(0xFF34C759);
const _amber = Color(0xFFFF9F0A);
const _red = Color(0xFFFF3B30);
const _grey = Color(0xFF8E8E93);

enum _Tier {
  excellent(_green, 'Excellent'),
  good(_green, 'Good'),
  moderate(_amber, 'Moderate'),
  bad(_red, 'Poor'),
  unknown(_grey, '—');

  const _Tier(this.color, this.label);
  final Color color;
  final String label;
}
