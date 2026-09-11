import 'dart:io';

import 'package:image_picker/image_picker.dart';
import 'package:path/path.dart' as p;

import '../util/uuid.dart';

/// Where a captured image comes from. A tiny local enum so the UI doesn't have to
/// import image_picker's ImageSource (keeps the plugin dependency contained here).
enum CaptureSource { camera, gallery }

/// One image just captured and copied into the pack dir, ready to be queued in
/// the local_attachments outbox (EditStore.addLocalAttachment).
class CapturedAttachment {
  const CapturedAttachment({
    required this.localPath,
    required this.filename,
    required this.contentType,
    required this.byteSize,
  });

  final String localPath;
  final String filename;
  final String contentType;
  final int byteSize;
}

/// AttachmentCapture wraps image_picker: take a photo (camera) or pick one from
/// the gallery, then copy it OUT of the picker's transient cache and INTO the
/// pack's own `attachments/` dir under a stable UUID name, so the file survives as
/// the on-device truth (and the offline thumbnail source) until it's uploaded.
///
/// Images only -- the field tool never attaches documents (that's web-only). We
/// downscale on capture (maxWidth + quality) so a full-frame phone photo doesn't
/// bloat the on-device queue or the upload; the server still generates its own
/// thumbnail for the pack on commit.
class AttachmentCapture {
  AttachmentCapture({ImagePicker? picker}) : _picker = picker ?? ImagePicker();

  final ImagePicker _picker;

  Future<CapturedAttachment?> capture({
    required CaptureSource source,
    required String destDir,
  }) async {
    final picked = await _picker.pickImage(
      source: source == CaptureSource.camera
          ? ImageSource.camera
          : ImageSource.gallery,
      maxWidth: 2048,
      imageQuality: 85,
    );
    if (picked == null) return null; // user cancelled

    await Directory(destDir).create(recursive: true);
    final ext = _extensionOf(picked.name, picked.path);
    final id = newUuidV4();
    final destPath = p.join(destDir, '$id$ext');
    await File(picked.path).copy(destPath);
    final size = await File(destPath).length();

    final name =
        picked.name.isNotEmpty ? picked.name : 'photo-$id$ext';
    return CapturedAttachment(
      localPath: destPath,
      filename: name,
      contentType: _contentTypeOf(ext),
      byteSize: size,
    );
  }

  /// Prefer the picker-reported name's extension, falling back to the temp path;
  /// default `.jpg` (image_picker re-encodes most captures to JPEG).
  static String _extensionOf(String name, String path) {
    var ext = p.extension(name);
    if (ext.isEmpty) ext = p.extension(path);
    ext = ext.toLowerCase();
    return ext.isEmpty ? '.jpg' : ext;
  }

  static String _contentTypeOf(String ext) {
    switch (ext) {
      case '.png':
        return 'image/png';
      case '.gif':
        return 'image/gif';
      case '.webp':
        return 'image/webp';
      case '.heic':
      case '.heif':
        return 'image/heic';
      case '.jpg':
      case '.jpeg':
      default:
        return 'image/jpeg';
    }
  }
}
