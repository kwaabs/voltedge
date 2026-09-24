import 'package:flutter/material.dart';
import 'package:mobile_scanner/mobile_scanner.dart';

/// Pull the bare asset-tag code out of a scanned barcode value. The QR on a label
/// encodes the deep link `<origin>/t/<code>` (so any phone camera opens the web
/// resolver), while the Code128 encodes the raw code for scanner guns. This
/// normalizes both to the code alone: a value carrying `/t/` is treated as the
/// deep link and everything after the LAST `/t/` (minus any query/fragment, and
/// percent-decoded) is the code; anything else is used verbatim (trimmed).
///
/// Returns null for an empty/whitespace value so the caller can ignore a stray
/// non-tag barcode rather than resolving on garbage.
String? extractTagCode(String? raw) {
  if (raw == null) return null;
  var v = raw.trim();
  if (v.isEmpty) return null;
  final marker = v.lastIndexOf('/t/');
  if (marker >= 0) {
    v = v.substring(marker + 3);
    // Drop a trailing query string or fragment (a QR may carry either).
    for (final sep in const ['?', '#', '/']) {
      final i = v.indexOf(sep);
      if (i >= 0) v = v.substring(0, i);
    }
    try {
      v = Uri.decodeComponent(v);
    } catch (_) {
      // Leave it as-is if it isn't valid percent-encoding.
    }
    v = v.trim();
  }
  return v.isEmpty ? null : v;
}

/// A full-screen camera scanner for asset tags (asset-tagging module S5). It reads
/// QR and Code128 symbologies, and on the first hit pops with the extracted tag
/// code (see [extractTagCode]). A manual-entry fallback covers a damaged/unreadable
/// label, and a torch toggle helps in a dark cabinet. The caller
/// ([PackMapPage._scanTag]) resolves the returned code against the pack, then the
/// server.
class ScanTagScreen extends StatefulWidget {
  const ScanTagScreen({super.key});

  @override
  State<ScanTagScreen> createState() => _ScanTagScreenState();
}

class _ScanTagScreenState extends State<ScanTagScreen> {
  final MobileScannerController _controller = MobileScannerController(
    formats: const [BarcodeFormat.qrCode, BarcodeFormat.code128],
  );

  /// Guards against the scanner firing onDetect several times (once per frame the
  /// code is in view) before the route finishes popping.
  bool _handled = false;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  void _onDetect(BarcodeCapture capture) {
    if (_handled) return;
    for (final b in capture.barcodes) {
      final code = extractTagCode(b.rawValue);
      if (code != null) {
        _handled = true;
        Navigator.of(context).pop(code);
        return;
      }
    }
  }

  Future<void> _enterManually() async {
    final controller = TextEditingController();
    final code = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Enter tag code'),
        content: TextField(
          controller: controller,
          autofocus: true,
          textCapitalization: TextCapitalization.characters,
          decoration: const InputDecoration(
            hintText: 'e.g. VE-7F3K2Q',
            labelText: 'Asset tag code',
          ),
          onSubmitted: (v) => Navigator.of(ctx).pop(v.trim()),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () =>
                Navigator.of(ctx).pop(controller.text.trim()),
            child: const Text('Resolve'),
          ),
        ],
      ),
    );
    if (!mounted) return;
    final trimmed = code?.trim();
    if (trimmed != null && trimmed.isNotEmpty) {
      _handled = true;
      Navigator.of(context).pop(trimmed);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Scan asset tag'),
        actions: [
          IconButton(
            tooltip: 'Torch',
            icon: const Icon(Icons.flashlight_on_outlined),
            onPressed: () => _controller.toggleTorch(),
          ),
          IconButton(
            tooltip: 'Switch camera',
            icon: const Icon(Icons.cameraswitch_outlined),
            onPressed: () => _controller.switchCamera(),
          ),
        ],
      ),
      body: Stack(
        fit: StackFit.expand,
        children: [
          MobileScanner(controller: _controller, onDetect: _onDetect),
          // A simple reticle so the crew knows where to aim.
          IgnorePointer(
            child: Center(
              child: Container(
                width: 240,
                height: 240,
                decoration: BoxDecoration(
                  border: Border.all(color: Colors.white70, width: 2),
                  borderRadius: BorderRadius.circular(12),
                ),
              ),
            ),
          ),
          Positioned(
            left: 0,
            right: 0,
            bottom: 0,
            child: SafeArea(
              child: Padding(
                padding: const EdgeInsets.all(16),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Text(
                      'Point the camera at the QR or barcode on the tag.',
                      textAlign: TextAlign.center,
                      style: TextStyle(color: Colors.white),
                    ),
                    const SizedBox(height: 12),
                    OutlinedButton.icon(
                      style: OutlinedButton.styleFrom(
                        foregroundColor: Colors.white,
                        side: const BorderSide(color: Colors.white70),
                      ),
                      icon: const Icon(Icons.keyboard_outlined),
                      label: const Text('Enter code manually'),
                      onPressed: _enterManually,
                    ),
                  ],
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}
