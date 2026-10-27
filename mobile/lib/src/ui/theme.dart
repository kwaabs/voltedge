import 'package:flutter/material.dart';

/// VoltEdge Field's visual identity, centralized so every screen reads as
/// "designed" instead of flat white. One brand seed is expanded into a full
/// Material 3 scheme for both brightnesses (so light + dark stay recognisably
/// the same product), then component themes add the colour + depth the plain
/// `colorSchemeSeed` default was missing: a branded app bar, tonal card/list
/// surfaces, rounded inputs, and coloured FAB/chips.
///
/// The map's own overlay colours (selection red, create green, connection green,
/// relationship purple, pending amber, draft blue) live in `pack_map_view.dart`
/// and stay as-is — they're semantic signals, not chrome, and deliberately match
/// the iOS system palette. [accentFor] mirrors that palette for non-map UI (e.g.
/// the pending sheet, status chips) so the two never drift.
class AppTheme {
  const AppTheme._();

  /// The primary seed and app-bar fill: an energetic green-teal that reads
  /// "electric/energy" and expands into the whole Material 3 scheme. Distinct
  /// enough from the map's semantic greens (create/connection are Apple green
  /// #34C759/#30D158) that a themed surface never reads as a map signal. Change
  /// this one value to reskin the entire app.
  static const seed = Color(0xFF00A878);

  // The map overlay palette, re-exported so non-map surfaces can match it.
  static const created = Color(0xFF34C759); // green: created offline
  static const connection = Color(0xFF30D158); // green: connection edge
  static const relationship = Color(0xFFBF5AF2); // purple: relationship edge
  static const pending = Color(0xFFFF9F0A); // amber: unsynced edit
  static const selection = Color(0xFFFF2D55); // red: selection / delete

  /// The light theme.
  static ThemeData light() => _build(Brightness.light);

  /// The dark theme (shares the seed, so it's the same product after dark).
  static ThemeData dark() => _build(Brightness.dark);

  static ThemeData _build(Brightness brightness) {
    final scheme = ColorScheme.fromSeed(
      seedColor: seed,
      brightness: brightness,
    );
    // Noto Sans is BUNDLED (assets/fonts, declared in pubspec) rather than fetched
    // at runtime -- the field tool is offline-first, so a runtime font download
    // (e.g. google_fonts' default) could leave a crew with no typeface. Setting
    // fontFamily on the base theme flows it through the whole textTheme.
    final base = ThemeData(
      colorScheme: scheme,
      useMaterial3: true,
      fontFamily: 'Noto Sans',
    );
    final onBar = scheme.onPrimary;

    return base.copyWith(
      // A hair of tint over pure white/black so sections aren't stark.
      scaffoldBackgroundColor: scheme.surface,

      // A solid branded bar is the single most visible "this app has a theme"
      // cue — far from the flat white default. Elevates a touch on scroll so a
      // long list reads as sliding under the bar.
      appBarTheme: AppBarTheme(
        backgroundColor: scheme.primary,
        foregroundColor: onBar,
        elevation: 0,
        scrolledUnderElevation: 3,
        centerTitle: false,
        titleTextStyle: base.textTheme.titleLarge?.copyWith(
          color: onBar,
          fontWeight: FontWeight.w600,
        ),
        iconTheme: IconThemeData(color: onBar),
        actionsIconTheme: IconThemeData(color: onBar),
      ),

      // Content rides on soft, rounded tonal cards instead of bare white rows.
      cardTheme: CardThemeData(
        elevation: 0,
        color: scheme.surfaceContainerLow,
        surfaceTintColor: Colors.transparent,
        clipBehavior: Clip.antiAlias,
        margin: const EdgeInsets.symmetric(horizontal: 16, vertical: 6),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(16),
        ),
      ),

      listTileTheme: ListTileThemeData(
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
        ),
        iconColor: scheme.primary,
      ),

      // A tonal FAB reads softer + more themed than the default primary block.
      floatingActionButtonTheme: FloatingActionButtonThemeData(
        backgroundColor: scheme.primaryContainer,
        foregroundColor: scheme.onPrimaryContainer,
        elevation: 2,
        highlightElevation: 4,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(16),
        ),
      ),

      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(12),
          ),
        ),
      ),

      chipTheme: ChipThemeData(
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(8),
        ),
        side: BorderSide.none,
        backgroundColor: scheme.secondaryContainer,
        labelStyle: TextStyle(color: scheme.onSecondaryContainer),
      ),

      inputDecorationTheme: InputDecorationTheme(
        filled: true,
        fillColor: scheme.surfaceContainerHighest,
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide.none,
        ),
        enabledBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide.none,
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide(color: scheme.primary, width: 2),
        ),
      ),

      dividerTheme: DividerThemeData(
        color: scheme.outlineVariant,
        space: 1,
        thickness: 1,
      ),

      snackBarTheme: SnackBarThemeData(
        behavior: SnackBarBehavior.floating,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
        ),
      ),

      bottomSheetTheme: BottomSheetThemeData(
        backgroundColor: scheme.surfaceContainerLow,
        surfaceTintColor: Colors.transparent,
        shape: const RoundedRectangleBorder(
          borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
        ),
      ),
    );
  }
}
