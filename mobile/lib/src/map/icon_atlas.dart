import 'dart:math' as math;
import 'dart:typed_data';
import 'dart:ui' as ui;

import 'package:flutter_svg/flutter_svg.dart';

/// Draws the built-in symbology point shapes as small raster images, so the map
/// can render a point feature as its real silhouette (a square transformer, a
/// hollow open switch) instead of the generic circle the first symbology slice
/// fell back to.
///
/// MapLibre GL renders tiles on the GPU, so -- unlike OpenLayers on the web --
/// it can't run a per-feature Dart callback to draw an icon. Instead each
/// distinct (shape, color) pair is rasterized once here and registered with the
/// map via `controller.addImage(name, bytes)`; a symbol layer then selects the
/// right image per feature with a data-driven `icon-image` expression (see
/// pack_style.dart). Color is baked into the pixels (a plain, non-SDF image),
/// which is why one image exists per shape+color rather than one per shape
/// tinted at draw time -- SDF tinting would need an SDF the shapes don't have.
///
/// The shape set mirrors the web's hardcoded fallback registry
/// (web/src/lib/symbology-icons.ts): circle / square / triangle /
/// triangle-outline / diamond, drawn from the same 24-unit viewBox coordinates
/// so the field tool and the web map read identically. "triangle-outline" is
/// the one hollow shape (stroke, no fill) -- an open switch's genuinely empty
/// silhouette. An unknown `icon_ref` (a typo, or a DB-authored SVG icon the
/// pack can't ship yet) falls back to a circle rather than vanishing.

/// The shapes this atlas can draw. Anything outside this set normalizes to
/// `circle`, matching the web fallback's default-shape behavior.
const kIconShapes = <String>{
  'circle',
  'square',
  'triangle',
  'triangle-outline',
  'diamond',
};

/// The physical pixel size each icon is drawn at. Generous so icons stay crisp
/// when a symbol layer scales them down on a hi-dpi screen; pairs with
/// pack_style.dart's icon-size base (kIconBaseScale) which brings this back to a
/// sensible on-screen size.
const int kIconDrawSize = 64;

/// A single icon image the style references: its registration [name] (the key
/// used in both `addImage` and the `icon-image` expression), the [iconRef] +
/// [color] it draws, and -- when the pack shipped one for this ref (schema v5+)
/// -- the DB-authored [svg] markup to rasterize. When [svg] is null the ref
/// falls back to a built-in Canvas shape (normalizeShape clamps an unknown ref
/// to a circle), so a point always draws.
class IconSpec {
  const IconSpec(this.name, this.iconRef, this.color, {this.svg});

  final String name;
  final String iconRef;
  final String color;
  final String? svg;
}

/// The stable image name for an (icon_ref, color) pair. Must be identical on
/// both sides of the wire: pack_style.dart writes it into the `icon-image`
/// expression, PackMapView.registerIcons passes it to `addImage`. Keyed on the
/// RAW ref (not a normalized shape) so two distinct custom icons never collide
/// on the same name.
String iconName(String iconRef, String color) =>
    'ic:$iconRef:${color.toLowerCase()}';

/// Rasterizes one icon spec to PNG bytes for `addImage`: the DB-authored SVG
/// (with its `{{COLOR}}` placeholder filled) when the pack shipped one, else the
/// built-in Canvas shape for the ref. The single entry point PackMapView uses,
/// so the SVG-vs-shape choice lives here, not in the view.
Future<Uint8List> rasterizeIconSpec(IconSpec spec) => spec.svg != null
    ? rasterizeSvg(spec.svg!, spec.color)
    : rasterizeIcon(spec.iconRef, spec.color);

/// Rasterizes DB-authored SVG markup, in one color, to PNG bytes. The web tints
/// these by substituting a rule's resolved color into each `{{COLOR}}`
/// placeholder (iconDataUri); we do the same, then render the vector off-screen
/// (vg.loadPicture -> Picture) scaled to fit the icon box, preserving aspect and
/// centering. A full-color icon with no placeholder is a harmless no-op
/// substitution, exactly like the web.
Future<Uint8List> rasterizeSvg(
  String svg,
  String color, {
  int size = kIconDrawSize,
}) async {
  final markup = svg.replaceAll('{{COLOR}}', color);
  final info = await vg.loadPicture(SvgStringLoader(markup), null);
  final recorder = ui.PictureRecorder();
  final canvas = ui.Canvas(
    recorder,
    ui.Rect.fromLTWH(0, 0, size.toDouble(), size.toDouble()),
  );
  final src = info.size;
  final maxDim = math.max(src.width, src.height);
  final scale = maxDim > 0 ? size / maxDim : 1.0;
  // Center the scaled picture in the square icon box.
  canvas.translate(
    (size - src.width * scale) / 2,
    (size - src.height * scale) / 2,
  );
  canvas.scale(scale);
  canvas.drawPicture(info.picture);
  info.picture.dispose();
  final picture = recorder.endRecording();
  final image = await picture.toImage(size, size);
  final data = await image.toByteData(format: ui.ImageByteFormat.png);
  image.dispose();
  return data!.buffer.asUint8List();
}

/// Clamps an `icon_ref` to a shape this atlas can draw (unknown -> circle).
String normalizeShape(String? shape) =>
    (shape != null && kIconShapes.contains(shape)) ? shape : 'circle';

/// Rasterizes one shape in one color to PNG bytes, ready for
/// `controller.addImage`. A thin white halo rings every shape so it stays
/// legible over a dark basemap (the same reason the old circle layer carried a
/// white stroke). The hollow "triangle-outline" is stroked, not filled, with
/// the halo drawn as a wider white stroke beneath the colored one.
Future<Uint8List> rasterizeIcon(
  String shape,
  String color, {
  int size = kIconDrawSize,
}) async {
  final s = normalizeShape(shape);
  final recorder = ui.PictureRecorder();
  final canvas = ui.Canvas(
    recorder,
    ui.Rect.fromLTWH(0, 0, size.toDouble(), size.toDouble()),
  );
  // The web icons are authored in a 24-unit viewBox; scale those coordinates up
  // to the draw size so the silhouettes match exactly.
  final f = size / 24.0;
  final path = _shapePath(s, f);
  final fillColor = _parseHexColor(color);
  const white = ui.Color(0xFFFFFFFF);

  if (s == 'triangle-outline') {
    final core = 2.5 * f; // matches the web outline's stroke-width
    canvas.drawPath(
      path,
      ui.Paint()
        ..style = ui.PaintingStyle.stroke
        ..color = white
        ..strokeWidth = core + 2.0 * f
        ..strokeJoin = ui.StrokeJoin.round,
    );
    canvas.drawPath(
      path,
      ui.Paint()
        ..style = ui.PaintingStyle.stroke
        ..color = fillColor
        ..strokeWidth = core
        ..strokeJoin = ui.StrokeJoin.round,
    );
  } else {
    canvas.drawPath(
      path,
      ui.Paint()
        ..style = ui.PaintingStyle.stroke
        ..color = white
        ..strokeWidth = 2.5 * f
        ..strokeJoin = ui.StrokeJoin.round,
    );
    canvas.drawPath(path, ui.Paint()..color = fillColor);
  }

  final picture = recorder.endRecording();
  final image = await picture.toImage(size, size);
  final data = await image.toByteData(format: ui.ImageByteFormat.png);
  image.dispose();
  return data!.buffer.asUint8List();
}

/// The shape's outline as a Path, in draw-size pixels ([f] = size / 24). Points
/// are the web fallback icons' exact viewBox coordinates.
ui.Path _shapePath(String shape, double f) {
  final path = ui.Path();
  switch (shape) {
    case 'square':
      path.addRRect(ui.RRect.fromRectAndRadius(
        ui.Rect.fromLTWH(4 * f, 4 * f, 16 * f, 16 * f),
        ui.Radius.circular(2 * f),
      ));
      break;
    case 'triangle':
    case 'triangle-outline':
      path.moveTo(12 * f, 3 * f);
      path.lineTo(21 * f, 20 * f);
      path.lineTo(3 * f, 20 * f);
      path.close();
      break;
    case 'diamond':
      path.moveTo(12 * f, 2 * f);
      path.lineTo(22 * f, 12 * f);
      path.lineTo(12 * f, 22 * f);
      path.lineTo(2 * f, 12 * f);
      path.close();
      break;
    case 'circle':
    default:
      path.addOval(ui.Rect.fromCircle(
        center: ui.Offset(12 * f, 12 * f),
        radius: 9 * f,
      ));
  }
  return path;
}

/// Parses a `#rrggbb` / `#rgb` (optionally `#aarrggbb`) hex string to a Color,
/// defaulting to opaque black on anything malformed so a bad value shows a
/// visible shape rather than nothing.
ui.Color _parseHexColor(String hex) {
  var h = hex.replaceFirst('#', '').trim();
  if (h.length == 3) {
    h = h.split('').map((c) => '$c$c').join();
  }
  if (h.length == 6) {
    h = 'ff$h';
  }
  final value = int.tryParse(h, radix: 16);
  return ui.Color(value ?? 0xff000000);
}
