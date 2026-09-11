import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/models.dart';
import '../providers.dart';
import 'format.dart';

/// The feature-detail read surface for GNSS survey quality (Phase gnss-quality
/// Slice 3b) -- the mobile mirror of the web "GPS" tab. Each row is one capture
/// the field tool stamped when a feature was placed/moved at the device's
/// position (Slice 3); the office already sees these, and now the crew can too.
///
/// The capture rows live server-side only (they never ride the offline pack), so
/// this fetches on demand when online. It's a collapsed ExpansionTile that only
/// hits the network on first expand -- no request on every detail open, and a
/// clean "you may be offline" fallback when the pull throws. Empty is the normal
/// answer for any feature never placed by GPS (every map-drawn/REST-created one),
/// so it says so plainly rather than looking broken.
class GpsCapturesSection extends ConsumerStatefulWidget {
  const GpsCapturesSection({
    super.key,
    required this.featureClass,
    required this.featureId,
  });

  final String featureClass;
  final String featureId;

  @override
  ConsumerState<GpsCapturesSection> createState() => _GpsCapturesSectionState();
}

class _GpsCapturesSectionState extends ConsumerState<GpsCapturesSection> {
  // Null until the tile is first expanded, so nothing is fetched for a feature
  // whose GPS section the user never opens.
  Future<List<GpsCapture>>? _future;

  void _load() {
    setState(() {
      _future = ref
          .read(fieldsyncClientProvider)
          .listGpsCaptures(widget.featureClass, widget.featureId);
    });
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(8, 8, 8, 0),
      // ExpansionTile draws its own top/bottom divider hairlines; drop them so
      // the section sits flush with the _section() headers above and below it.
      child: Theme(
        data: theme.copyWith(dividerColor: Colors.transparent),
        child: ExpansionTile(
          tilePadding: const EdgeInsets.symmetric(horizontal: 8),
          childrenPadding: const EdgeInsets.only(bottom: 4),
          leading: Icon(Icons.satellite_alt_outlined,
              color: theme.colorScheme.primary, size: 20),
          title: Text('GPS survey quality',
              style: theme.textTheme.titleSmall?.copyWith(
                  color: theme.colorScheme.primary,
                  fontWeight: FontWeight.w700)),
          subtitle: Text('Recorded when placed or moved by GPS',
              style: theme.textTheme.bodySmall),
          onExpansionChanged: (open) {
            if (open && _future == null) _load();
          },
          children: [_content(context)],
        ),
      ),
    );
  }

  Widget _content(BuildContext context) {
    final future = _future;
    if (future == null) return const SizedBox.shrink();
    return FutureBuilder<List<GpsCapture>>(
      future: future,
      builder: (context, snap) {
        if (snap.connectionState == ConnectionState.waiting) {
          return const Padding(
            padding: EdgeInsets.symmetric(vertical: 16),
            child: Center(
              child: SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
            ),
          );
        }
        if (snap.hasError) return _error(context);
        final caps = snap.data ?? const <GpsCapture>[];
        if (caps.isEmpty) return _empty(context);
        return Column(
          children: [for (final c in caps) _CaptureCard(capture: c)],
        );
      },
    );
  }

  Widget _empty(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
        child: Text('No GPS captures recorded for this feature.',
            style: Theme.of(context).textTheme.bodySmall?.copyWith(
                color: Theme.of(context).colorScheme.onSurfaceVariant)),
      );

  Widget _error(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 8),
      child: Row(
        children: [
          Icon(Icons.cloud_off, size: 16, color: theme.colorScheme.onSurfaceVariant),
          const SizedBox(width: 8),
          Expanded(
            child: Text("Couldn't load — you may be offline.",
                style: theme.textTheme.bodySmall),
          ),
          TextButton(onPressed: _load, child: const Text('Retry')),
        ],
      ),
    );
  }
}

/// One capture as a compact card: fix badge + quality tier, the DOP triple (or a
/// "not reported" note for an iPhone-internal capture), then accuracy / satellites
/// / altitude, and a footer with who captured it and when. Mirrors the live
/// [GnssReadout] card's layout so a placed capture reads the same as the live one.
class _CaptureCard extends StatelessWidget {
  const _CaptureCard({required this.capture});

  final GpsCapture capture;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = capture;
    final tier = _tierOf(c);
    return Container(
      margin: const EdgeInsets.fromLTRB(8, 4, 8, 4),
      padding: const EdgeInsets.fromLTRB(12, 10, 12, 10),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(10),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Container(
                width: 10,
                height: 10,
                decoration: BoxDecoration(color: tier.color, shape: BoxShape.circle),
              ),
              const SizedBox(width: 6),
              Text(_fixLabel(c.fixType),
                  style: theme.textTheme.bodyMedium
                      ?.copyWith(fontWeight: FontWeight.w600)),
              const Spacer(),
              Text(tier.label,
                  style: theme.textTheme.labelSmall?.copyWith(
                      color: tier.color, fontWeight: FontWeight.w600)),
            ],
          ),
          const SizedBox(height: 8),
          if (c.hasDop)
            Wrap(
              spacing: 6,
              runSpacing: 6,
              children: [
                if (c.pdop != null) _chip(context, 'PDOP', _fmt(c.pdop)),
                if (c.hdop != null) _chip(context, 'HDOP', _fmt(c.hdop)),
                if (c.vdop != null) _chip(context, 'VDOP', _fmt(c.vdop)),
              ],
            )
          else
            Text('DOP not reported by this receiver',
                style: theme.textTheme.bodySmall
                    ?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
          const SizedBox(height: 8),
          _kv(context, Icons.my_location, 'Accuracy',
              c.hAccuracyM != null ? '±${_fmt1(c.hAccuracyM)} m' : '—'),
          if (c.satsUsed != null || c.satsInView != null)
            _kv(context, Icons.satellite_alt, 'Satellites',
                _sats(c.satsUsed, c.satsInView)),
          if (c.altitudeM != null)
            _kv(context, Icons.height, 'Altitude', '${_fmt0(c.altitudeM)} m'),
          const SizedBox(height: 6),
          Row(
            children: [
              Expanded(
                child: Text(
                  '${c.source == 'external' ? 'External' : 'Internal'} · ${c.capturedByLabel}',
                  style: theme.textTheme.labelSmall
                      ?.copyWith(color: theme.colorScheme.onSurfaceVariant),
                  overflow: TextOverflow.ellipsis,
                ),
              ),
              const SizedBox(width: 8),
              Text(shortTime(c.capturedAt),
                  style: theme.textTheme.labelSmall
                      ?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
            ],
          ),
        ],
      ),
    );
  }

  Widget _chip(BuildContext context, String label, String value) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: theme.colorScheme.surface,
        borderRadius: BorderRadius.circular(8),
      ),
      child: Text('$label $value',
          style: theme.textTheme.labelMedium
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

  // ---- formatting / tiers (mirrors gnss_readout.dart, over the stored strings) --

  static String _fmt(double? v) => v == null ? '—' : v.toStringAsFixed(1);
  static String _fmt1(double? v) => v == null ? '—' : v.toStringAsFixed(1);
  static String _fmt0(double? v) => v == null ? '—' : v.toStringAsFixed(0);

  static String _sats(int? used, int? inView) {
    if (used != null && inView != null) return '$used / $inView';
    if (used != null) return '$used used';
    if (inView != null) return '$inView in view';
    return '—';
  }

  /// fix_type is the mobile GnssFixType.name written at capture (camelCase enum
  /// identifier -- see gnss_sample.dart's toCaptureJson). Unknown/absent reads as
  /// a plain GPS fix, the same fallback the live readout uses for an iOS position.
  static String _fixLabel(String? f) => switch (f) {
        'rtkFixed' => 'RTK fixed',
        'rtkFloat' => 'RTK float',
        'dgps' => 'DGPS',
        'gps' => 'GPS fix',
        'none' => 'No fix',
        _ => 'GPS fix',
      };

  /// Quality tier from HDOP when the receiver reported it, else the OS accuracy
  /// estimate; a "no fix" always reads red. Same thresholds as gnss_readout's
  /// _tierOf so a stored capture tiers identically to the live reading.
  static _Tier _tierOf(GpsCapture c) {
    if (c.fixType == 'none') return _Tier.bad;
    if (c.fixType == 'rtkFixed') return _Tier.excellent;
    final h = c.hdop;
    if (h != null) {
      if (h <= 2) return _Tier.excellent;
      if (h <= 5) return _Tier.good;
      if (h <= 10) return _Tier.moderate;
      return _Tier.bad;
    }
    final a = c.hAccuracyM;
    if (a != null) {
      if (a <= 3) return _Tier.excellent;
      if (a <= 8) return _Tier.good;
      if (a <= 20) return _Tier.moderate;
      return _Tier.bad;
    }
    return _Tier.unknown;
  }
}

// Signal colours kept local, matching gnss_readout.dart's semantic palette.
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
