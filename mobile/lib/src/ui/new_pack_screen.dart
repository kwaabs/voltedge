import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/fieldsync_client.dart';
import '../providers.dart';

/// A minimal build-request form for Slice 2a: a bbox + a comma-separated set of
/// class codes. (Slice 2b's map gives a draw-a-box UI; this text form is enough
/// to exercise the enqueue path now.) Class codes are UPPERCASE and
/// case-sensitive on the server (e.g. POLE, MV_CONDUCTOR, DIST_XFMR).
class NewPackScreen extends ConsumerStatefulWidget {
  const NewPackScreen({super.key});

  @override
  ConsumerState<NewPackScreen> createState() => _NewPackScreenState();
}

class _NewPackScreenState extends ConsumerState<NewPackScreen> {
  final _minX = TextEditingController(text: '-180');
  final _minY = TextEditingController(text: '-90');
  final _maxX = TextEditingController(text: '180');
  final _maxY = TextEditingController(text: '90');
  final _classes = TextEditingController(text: 'POLE, MV_CONDUCTOR, DIST_XFMR');
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _minX.dispose();
    _minY.dispose();
    _maxX.dispose();
    _maxY.dispose();
    _classes.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    final bbox = [_minX, _minY, _maxX, _maxY]
        .map((c) => double.tryParse(c.text.trim()))
        .toList();
    if (bbox.any((v) => v == null)) {
      setState(() => _error = 'All four bbox values must be numbers.');
      return;
    }
    final classes = _classes.text
        .split(',')
        .map((s) => s.trim())
        .where((s) => s.isNotEmpty)
        .toList();
    if (classes.isEmpty) {
      setState(() => _error = 'Enter at least one feature class code.');
      return;
    }

    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await ref.read(fieldsyncClientProvider).createPack(
            bbox: bbox.cast<double>(),
            featureClasses: classes,
          );
      ref.invalidate(remotePacksProvider);
      if (!mounted) return;
      Navigator.of(context).pop();
    } on FieldsyncException catch (e) {
      setState(() => _error = e.message);
    } catch (e) {
      setState(() => _error = '$e');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('New pack')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          const Text('Bounding box (EPSG:4326)',
              style: TextStyle(fontWeight: FontWeight.w600)),
          const SizedBox(height: 8),
          Row(children: [
            Expanded(child: _num(_minX, 'min X (lon)')),
            const SizedBox(width: 12),
            Expanded(child: _num(_minY, 'min Y (lat)')),
          ]),
          const SizedBox(height: 12),
          Row(children: [
            Expanded(child: _num(_maxX, 'max X (lon)')),
            const SizedBox(width: 12),
            Expanded(child: _num(_maxY, 'max Y (lat)')),
          ]),
          const SizedBox(height: 20),
          TextField(
            controller: _classes,
            autocorrect: false,
            decoration: const InputDecoration(
              labelText: 'Feature classes (comma-separated, UPPERCASE)',
              helperText: 'e.g. POLE, MV_CONDUCTOR, DIST_XFMR',
              border: OutlineInputBorder(),
            ),
          ),
          if (_error != null) ...[
            const SizedBox(height: 16),
            Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
          ],
          const SizedBox(height: 24),
          FilledButton.icon(
            onPressed: _busy ? null : _submit,
            icon: _busy
                ? const SizedBox(
                    width: 18,
                    height: 18,
                    child: CircularProgressIndicator(strokeWidth: 2))
                : const Icon(Icons.cloud_upload),
            label: Text(_busy ? 'Requesting…' : 'Request build'),
          ),
        ],
      ),
    );
  }

  Widget _num(TextEditingController c, String label) => TextField(
        controller: c,
        keyboardType: const TextInputType.numberWithOptions(
            signed: true, decimal: true),
        decoration: InputDecoration(
          labelText: label,
          border: const OutlineInputBorder(),
          isDense: true,
        ),
      );
}
