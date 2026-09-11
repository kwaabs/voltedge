/// Small display helpers shared across screens.

String humanBytes(int? bytes) {
  if (bytes == null) return '–';
  const units = ['B', 'KB', 'MB', 'GB'];
  var value = bytes.toDouble();
  var unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  final str = unit == 0 ? value.toStringAsFixed(0) : value.toStringAsFixed(1);
  return '$str ${units[unit]}';
}

String shortTime(DateTime? t) {
  if (t == null) return '';
  final local = t.toLocal();
  String two(int n) => n.toString().padLeft(2, '0');
  return '${local.year}-${two(local.month)}-${two(local.day)} '
      '${two(local.hour)}:${two(local.minute)}';
}

String shortId(String id) => id.length <= 8 ? id : id.substring(0, 8);

/// Humanize a raw column name for display -- `voltage_class` -> "Voltage class".
/// Mirrors the web app's formatFieldLabel (web/.../field-utils.ts) so the same
/// feature reads the same on both surfaces. The pack carries no separate label
/// column; a field's `name` is the only human-facing string.
String fieldLabel(String name) {
  if (name.isEmpty) return name;
  final spaced = name.replaceAll('_', ' ');
  return spaced[0].toUpperCase() + spaced.substring(1);
}

/// Parse a stored `date` value into a DateTime, tolerating either a bare
/// `yyyy-MM-dd` or a full timestamp string. Returns null on empty/unparseable.
DateTime? parseIsoDate(String? s) {
  final raw = s?.trim() ?? '';
  if (raw.isEmpty) return null;
  return DateTime.tryParse(raw);
}

/// Format a DateTime as the `yyyy-MM-dd` string a server `date` field expects.
String formatIsoDate(DateTime d) {
  String two(int n) => n.toString().padLeft(2, '0');
  return '${d.year.toString().padLeft(4, '0')}-${two(d.month)}-${two(d.day)}';
}

/// Normalize a stored `date` value to `yyyy-MM-dd` for display/prefill. Keeps the
/// original text if it doesn't parse, so an odd value is shown rather than lost.
String normalizeIsoDate(dynamic v) {
  final s = v == null ? '' : '$v';
  final d = parseIsoDate(s);
  return d != null ? formatIsoDate(d) : s;
}
