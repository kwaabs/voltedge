import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../api/fieldsync_client.dart';
import '../api/models.dart';
import '../providers.dart';
import 'bbox_picker_page.dart';

/// The build-request form: a bbox + a set of feature classes. The classes are
/// chosen from a checkbox pick-list populated by GET /feature-classes (the
/// packable catalog) -- no more typing UPPERCASE, case-sensitive codes by hand,
/// which was a stopgap and made a typo into a server 404. The bbox can be typed
/// into the four numeric fields OR drawn on a map ("Draw on map" opens
/// [BboxPickerPage] and drops the result back into those same fields), so the two
/// ways stay in sync.
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

  /// The codes the user has ticked. Keyed by code (what the API wants back).
  final Set<String> _selected = {};
  String _filter = '';
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _minX.dispose();
    _minY.dispose();
    _maxX.dispose();
    _maxY.dispose();
    super.dispose();
  }

  /// Open the map picker, seeded with whatever the fields currently hold (if
  /// they parse to a real box), and write the drawn box back into the fields.
  Future<void> _drawBbox() async {
    final current = [_minX, _minY, _maxX, _maxY]
        .map((c) => double.tryParse(c.text.trim()))
        .toList();
    final initial = current.every((v) => v != null) ? current.cast<double>() : null;
    final result = await Navigator.of(context).push<List<double>>(
      MaterialPageRoute(builder: (_) => BboxPickerPage(initial: initial)),
    );
    if (result == null || result.length != 4) return;
    setState(() {
      _minX.text = _fmt(result[0]);
      _minY.text = _fmt(result[1]);
      _maxX.text = _fmt(result[2]);
      _maxY.text = _fmt(result[3]);
      _error = null;
    });
  }

  static String _fmt(double v) => v.toStringAsFixed(6);

  Future<void> _submit() async {
    final bbox = [_minX, _minY, _maxX, _maxY]
        .map((c) => double.tryParse(c.text.trim()))
        .toList();
    if (bbox.any((v) => v == null)) {
      setState(() => _error = 'All four bbox values must be numbers.');
      return;
    }
    if (_selected.isEmpty) {
      setState(() => _error = 'Select at least one feature class.');
      return;
    }

    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await ref.read(fieldsyncClientProvider).createPack(
            bbox: bbox.cast<double>(),
            featureClasses: _selected.toList(),
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
    final classesAsync = ref.watch(featureClassesProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('New pack')),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          Row(
            children: [
              const Expanded(
                child: Text('Bounding box (EPSG:4326)',
                    style: TextStyle(fontWeight: FontWeight.w600)),
              ),
              TextButton.icon(
                onPressed: _busy ? null : _drawBbox,
                icon: const Icon(Icons.crop, size: 18),
                label: const Text('Draw on map'),
              ),
            ],
          ),
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
          const SizedBox(height: 24),
          Row(
            children: [
              const Expanded(
                child: Text('Feature classes',
                    style: TextStyle(fontWeight: FontWeight.w600)),
              ),
              if (_selected.isNotEmpty)
                Text('${_selected.length} selected',
                    style: Theme.of(context).textTheme.bodySmall),
              if (_selected.isNotEmpty)
                TextButton(
                  onPressed: () => setState(_selected.clear),
                  child: const Text('Clear'),
                ),
            ],
          ),
          const SizedBox(height: 8),
          classesAsync.when(
            loading: () => const Padding(
              padding: EdgeInsets.symmetric(vertical: 24),
              child: Center(child: CircularProgressIndicator()),
            ),
            error: (e, _) => _classesError(context, e),
            data: (classes) => _classPicker(context, classes),
          ),
          if (_error != null) ...[
            const SizedBox(height: 16),
            Text(_error!,
                style: TextStyle(color: Theme.of(context).colorScheme.error)),
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

  Widget _classesError(BuildContext context, Object err) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('Could not load feature classes.\n$err',
              style: TextStyle(color: Theme.of(context).colorScheme.error)),
          const SizedBox(height: 8),
          OutlinedButton.icon(
            onPressed: () => ref.invalidate(featureClassesProvider),
            icon: const Icon(Icons.refresh),
            label: const Text('Retry'),
          ),
        ],
      );

  Widget _classPicker(BuildContext context, List<FeatureClassSummary> all) {
    if (all.isEmpty) {
      return const Text('No packable feature classes are available.');
    }

    final q = _filter.trim().toLowerCase();
    final matches = q.isEmpty
        ? all
        : all
            .where((c) =>
                c.code.toLowerCase().contains(q) ||
                c.name.toLowerCase().contains(q))
            .toList();

    // Group by admin category (null/empty -> "Other"), categories alphabetical
    // with "Other" pinned last; classes keep the server's code order within.
    final groups = <String, List<FeatureClassSummary>>{};
    for (final c in matches) {
      final key = (c.category == null || c.category!.trim().isEmpty)
          ? 'Other'
          : c.category!.trim();
      groups.putIfAbsent(key, () => []).add(c);
    }
    final orderedKeys = groups.keys.toList()
      ..sort((a, b) {
        if (a == 'Other') return 1;
        if (b == 'Other') return -1;
        return a.toLowerCase().compareTo(b.toLowerCase());
      });

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        TextField(
          onChanged: (v) => setState(() => _filter = v),
          decoration: InputDecoration(
            isDense: true,
            prefixIcon: const Icon(Icons.search),
            hintText: 'Filter by name or code',
            border: const OutlineInputBorder(),
            suffixIcon: _filter.isEmpty
                ? null
                : IconButton(
                    icon: const Icon(Icons.clear),
                    onPressed: () => setState(() => _filter = ''),
                  ),
          ),
        ),
        const SizedBox(height: 8),
        DecoratedBox(
          decoration: BoxDecoration(
            border: Border.all(color: Theme.of(context).dividerColor),
            borderRadius: BorderRadius.circular(8),
          ),
          child: Column(
            children: [
              if (matches.isEmpty)
                const Padding(
                  padding: EdgeInsets.all(16),
                  child: Text('No classes match your filter.'),
                ),
              for (final key in orderedKeys) ...[
                _groupHeader(context, key, groups[key]!),
                for (final c in groups[key]!) _classTile(c),
              ],
            ],
          ),
        ),
      ],
    );
  }

  Widget _groupHeader(
      BuildContext context, String label, List<FeatureClassSummary> members) {
    final codes = members.map((m) => m.code).toList();
    final allOn = codes.every(_selected.contains);
    return Container(
      color: Theme.of(context).colorScheme.surfaceContainerHighest,
      padding: const EdgeInsets.fromLTRB(12, 6, 4, 6),
      child: Row(
        children: [
          Expanded(
            child: Text(label,
                style: const TextStyle(
                    fontWeight: FontWeight.w600, fontSize: 13)),
          ),
          TextButton(
            onPressed: () => setState(() {
              if (allOn) {
                _selected.removeAll(codes);
              } else {
                _selected.addAll(codes);
              }
            }),
            child: Text(allOn ? 'None' : 'All'),
          ),
        ],
      ),
    );
  }

  Widget _classTile(FeatureClassSummary c) {
    final subtitle = [
      c.code,
      if (c.network != null) c.network!,
      c.geometryType,
    ].join(' · ');
    return CheckboxListTile(
      dense: true,
      controlAffinity: ListTileControlAffinity.leading,
      value: _selected.contains(c.code),
      onChanged: (on) => setState(() {
        if (on ?? false) {
          _selected.add(c.code);
        } else {
          _selected.remove(c.code);
        }
      }),
      title: Text(c.name),
      subtitle: Text(subtitle),
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
