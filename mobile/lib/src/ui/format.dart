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
