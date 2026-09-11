import 'dart:math';

/// A cryptographically-seeded generator reused across calls. `Random.secure()`
/// pulls from the platform CSPRNG, so collisions across devices are astronomically
/// unlikely -- which is the whole point of assigning a feature's id offline
/// (Phase 21 create slice): the id minted here must survive a later POST /sync
/// as the row's real primary key without a server round-trip.
final Random _rng = Random.secure();

/// Generate a RFC-4122 version-4 UUID (random). Hand-rolled to avoid pulling in
/// the `uuid` package for a single call site; the layout (version nibble `4`,
/// variant bits `10`) matches what Postgres `uuid` columns accept verbatim.
String newUuidV4() {
  final bytes = List<int>.generate(16, (_) => _rng.nextInt(256));
  bytes[6] = (bytes[6] & 0x0f) | 0x40; // version 4
  bytes[8] = (bytes[8] & 0x3f) | 0x80; // variant 10xx
  final hex = [for (final b in bytes) b.toRadixString(16).padLeft(2, '0')];
  return '${hex.sublist(0, 4).join()}-${hex.sublist(4, 6).join()}-'
      '${hex.sublist(6, 8).join()}-${hex.sublist(8, 10).join()}-'
      '${hex.sublist(10, 16).join()}';
}
