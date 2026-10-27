/// The published field-app release manifest (GET /app/latest, served by the
/// appdist package). It is the device-facing half of the self-distribution ("our
/// own play store") flow: the app fetches this on launch and, when it names a
/// build newer than the one installed, nudges the user to download the new APK.
class AppRelease {
  const AppRelease({
    required this.version,
    required this.versionCode,
    required this.sizeBytes,
    this.notes,
  });

  /// The release version NAME, e.g. "1.2.0" -- compared semver-style against the
  /// installed PackageInfo.version when a version code isn't available on both.
  final String version;

  /// The monotonic version CODE (0 when the publisher didn't set one). When both
  /// the manifest and the installed build carry a positive code it's the
  /// authoritative "is this newer" signal; the version name is the fallback.
  final int versionCode;

  final int sizeBytes;
  final String? notes;

  factory AppRelease.fromJson(Map<String, dynamic> json) => AppRelease(
        version: (json['version'] as String? ?? '').trim(),
        versionCode: (json['version_code'] as num?)?.toInt() ?? 0,
        sizeBytes: (json['size_bytes'] as num?)?.toInt() ?? 0,
        notes: (json['notes'] as String?)?.trim().isEmpty ?? true
            ? null
            : (json['notes'] as String).trim(),
      );

  /// Whether this published release is newer than the build currently installed
  /// ([installedVersion] = PackageInfo.version, [installedCode] = its buildNumber).
  /// Prefers the integer version-code comparison when both sides have a positive
  /// code (monotonic + unambiguous); otherwise falls back to a dotted-number
  /// (semver-ish) comparison of the version names. An unparseable/empty published
  /// version is never treated as newer (no false nudge).
  bool isNewerThan(String installedVersion, int installedCode) {
    if (versionCode > 0 && installedCode > 0) {
      return versionCode > installedCode;
    }
    return _compareDotted(version, installedVersion) > 0;
  }
}

/// A resolved, actionable update: the published release plus the absolute URL the
/// device opens to download the APK (the fieldsync /app/download redirect). Only
/// built when the published build is actually newer than the installed one.
class AppUpdate {
  const AppUpdate({required this.release, required this.downloadUrl});
  final AppRelease release;
  final String downloadUrl;
}

/// Compare two dotted numeric version strings ("1.2.0" vs "1.10"). Returns >0 when
/// [a] is greater, <0 when less, 0 when equal. Missing trailing components count
/// as 0 (so "1.2" == "1.2.0"); any non-numeric component is treated as 0 so a
/// malformed string degrades to "not newer" rather than throwing.
int _compareDotted(String a, String b) {
  final pa = a.split('.');
  final pb = b.split('.');
  final n = pa.length > pb.length ? pa.length : pb.length;
  for (var i = 0; i < n; i++) {
    final va = i < pa.length ? (int.tryParse(pa[i].trim()) ?? 0) : 0;
    final vb = i < pb.length ? (int.tryParse(pb[i].trim()) ?? 0) : 0;
    if (va != vb) return va > vb ? 1 : -1;
  }
  return 0;
}
