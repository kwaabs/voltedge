import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../pack/pack_database.dart';
import 'date_form_field.dart';
import 'format.dart';

/// The offline attribute editor (Phase 21 Slice 3a), as a **Scaffold-less inline
/// view** so it renders in the very same slot the read-only detail panel does --
/// the tablet side panel or the phone bottom sheet -- rather than pushing a
/// separate page. A type-aware form over one feature's editable fields with its
/// own Cancel / Save bar; on save it hands the collected values back to the
/// caller, which turns them into a delta overlay + outbox op (see EditStore).
///
/// Editability mirrors the server's own rules read from the packed field defs:
/// only `visible_on_update`, non-computed fields are editable, and a field that
/// is not `editable_after_publish` is locked once the feature is published. A
/// locked field is still shown (read-only) so the user sees why it can't change.
///
/// A coded field renders as a dropdown when the pack (schema v2+) ships that
/// domain's values ([domainValues], keyed by domain id); otherwise it falls back
/// to a plain input. Server validation still enforces the domain on sync either
/// way.
class FeatureEditView extends StatefulWidget {
  const FeatureEditView({
    super.key,
    required this.feature,
    required this.fields,
    required this.currentValues,
    required this.onSave,
    required this.onCancel,
    this.domainValues = const {},
    this.padding,
  });

  final FeatureRecord feature;
  final List<FieldDef> fields;

  /// Coded-domain pick lists, keyed by domain id. A field whose `domainId` has a
  /// non-empty list here renders as a dropdown (code stored, label shown).
  final Map<String, List<DomainValue>> domainValues;

  /// The merged (pack + any existing overlay) values to prefill from.
  final Map<String, dynamic> currentValues;

  /// Called with the full set of editable-field values on Save. Returning a
  /// future lets us await the write before the parent leaves edit mode.
  final Future<void> Function(Map<String, dynamic> editedAttrs) onSave;

  /// Leave edit mode without saving.
  final VoidCallback onCancel;

  final EdgeInsetsGeometry? padding;

  @override
  State<FeatureEditView> createState() => _FeatureEditViewState();
}

class _FeatureEditViewState extends State<FeatureEditView> {
  final _formKey = GlobalKey<FormState>();

  // Text-backed controls (text/numeric/integer/date/timestamp/uuid/jsonb).
  final Map<String, TextEditingController> _text = {};
  // Boolean controls.
  final Map<String, bool> _bools = {};
  // Coded (dropdown) controls -> the selected value CODE (or null).
  final Map<String, String?> _codes = {};

  late final List<FieldDef> _editable;
  late final List<FieldDef> _locked;
  bool _saving = false;

  bool get _published => widget.feature.status == 'published';

  /// A field is a dropdown when it has a coded domain AND the pack shipped that
  /// domain's values.
  bool _isCoded(FieldDef f) {
    final id = f.domainId;
    return id != null && (widget.domainValues[id]?.isNotEmpty ?? false);
  }

  /// Whether [f] is required for THIS feature's subtype (pack v3+ ships the
  /// subtype codes per field, from the server's subtype_field_rules). Mirrors the
  /// web app's isFieldRequired: a class with no subtype, or a field with no rule
  /// for this subtype, is optional. Drives both the red `*` and the Save
  /// validator; an older pack ships no codes, so nothing is marked required.
  bool _isRequired(FieldDef f) {
    final st = widget.feature.subtype;
    return st != null && f.requiredForSubtypes.contains(st);
  }

  @override
  void initState() {
    super.initState();
    // A field is offered for editing only when the server would accept an update
    // to it: visible on update, not computed, and (if the feature is published)
    // still editable after publish.
    final editable = <FieldDef>[];
    final locked = <FieldDef>[];
    for (final f in widget.fields) {
      if (!f.visibleOnUpdate || f.isComputed) continue;
      if (_published && !f.editableAfterPublish) {
        locked.add(f);
      } else {
        editable.add(f);
      }
    }
    _editable = editable;
    _locked = locked;

    for (final f in _editable) {
      final v = widget.currentValues[f.name];
      if (f.dataType == 'boolean') {
        _bools[f.name] = v == true;
      } else if (_isCoded(f)) {
        _codes[f.name] = v == null ? null : '$v';
      } else if (f.dataType == 'date') {
        // Normalize to yyyy-MM-dd so the picker and display agree.
        _text[f.name] = TextEditingController(text: normalizeIsoDate(v));
      } else {
        _text[f.name] = TextEditingController(text: _asText(v));
      }
    }
  }

  @override
  void dispose() {
    for (final c in _text.values) {
      c.dispose();
    }
    super.dispose();
  }

  static String _asText(dynamic v) {
    if (v == null) return '';
    return '$v';
  }

  /// The dropdown items for a coded field: the packed values plus, if the current
  /// value isn't among them (a stale/deprecated code), a synthetic item so the
  /// existing value is preserved rather than silently dropped.
  List<DomainValue> _itemsFor(FieldDef f) {
    final base = widget.domainValues[f.domainId] ?? const <DomainValue>[];
    final current = _codes[f.name];
    if (current == null || current.isEmpty) return base;
    if (base.any((d) => d.code == current)) return base;
    return [DomainValue(code: current, label: '$current (current)'), ...base];
  }

  Future<void> _save() async {
    final form = _formKey.currentState;
    if (form == null || !form.validate()) return;
    setState(() => _saving = true);

    final edited = <String, dynamic>{};
    for (final f in _editable) {
      edited[f.name] = _valueFor(f);
    }

    try {
      await widget.onSave(edited);
      // The parent leaves edit mode on success (it owns _editing).
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not save edit: $e')),
      );
    }
  }

  /// The coerced value for a field, typed to match the server field type so the
  /// sync PATCH validates (an integer field must send an int, not "5"). An empty
  /// text box / unselected dropdown becomes null for a nullable field, else an
  /// empty string.
  dynamic _valueFor(FieldDef f) {
    if (f.dataType == 'boolean') return _bools[f.name] ?? false;
    final raw =
        (_isCoded(f) ? _codes[f.name] ?? '' : _text[f.name]!.text).trim();
    if (raw.isEmpty) return f.nullable ? null : '';
    switch (f.dataType) {
      case 'integer':
        return int.tryParse(raw) ?? raw;
      case 'numeric':
        return num.tryParse(raw) ?? raw;
      default:
        return raw;
    }
  }

  @override
  Widget build(BuildContext context) {
    if (_editable.isEmpty && _locked.isEmpty) {
      return Column(
        children: [
          const Expanded(
            child: Center(child: Text('This feature has no editable fields.')),
          ),
          _actionBar(context, saveEnabled: false),
        ],
      );
    }
    final theme = Theme.of(context);
    return Column(
      children: [
        Expanded(
          child: Form(
            key: _formKey,
            child: ListView(
              // Horizontal padding is always applied so fields (and, crucially,
              // each dropdown's popup -- which inherits the button's width and
              // position) stay inset from the panel edges. Any padding the parent
              // passes for the slot is merged on top rather than replacing it.
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12)
                  .add(widget.padding ?? EdgeInsets.zero),
              children: [
                if (_editable.isNotEmpty) ...[
                  _sectionLabel(context, 'Attributes'),
                  for (final f in _editable) _editControl(f),
                ],
                if (_locked.isNotEmpty) ...[
                  const SizedBox(height: 20),
                  _sectionLabel(context, 'Locked after publish',
                      icon: Icons.lock_outline),
                  for (final f in _locked) _lockedControl(f),
                ],
                const SizedBox(height: 8),
              ],
            ),
          ),
        ),
        Divider(height: 1, color: theme.dividerColor),
        _actionBar(context),
      ],
    );
  }

  Widget _sectionLabel(BuildContext context, String text, {IconData? icon}) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(bottom: 10, top: 2),
      child: Row(
        children: [
          if (icon != null) ...[
            Icon(icon, size: 15, color: theme.colorScheme.outline),
            const SizedBox(width: 6),
          ],
          Text(
            text.toUpperCase(),
            style: theme.textTheme.labelSmall?.copyWith(
              color: theme.colorScheme.outline,
              letterSpacing: 0.8,
              fontWeight: FontWeight.w600,
            ),
          ),
        ],
      ),
    );
  }

  /// A consistent, filled decoration for every control -- the theme-driven
  /// equivalent of a shared utility style, so fields read as one set. The label
  /// shows the humanized field name with a red `*` when the field is required
  /// for this feature's subtype (see _isRequired) -- the same thing the validator
  /// blocks Save on. A locked field never shows the asterisk.
  InputDecoration _decoration(FieldDef f, {bool locked = false}) {
    final theme = Theme.of(context);
    final required = !locked && _isRequired(f);
    return InputDecoration(
      label: Text.rich(
        TextSpan(
          text: fieldLabel(f.name),
          children: required
              ? [
                  TextSpan(
                    text: ' *',
                    style: TextStyle(color: theme.colorScheme.error),
                  ),
                ]
              : null,
        ),
      ),
      filled: true,
      isDense: true,
      contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 14),
      border: OutlineInputBorder(
        borderRadius: BorderRadius.circular(10),
        borderSide: BorderSide.none,
      ),
      enabledBorder: OutlineInputBorder(
        borderRadius: BorderRadius.circular(10),
        borderSide: BorderSide.none,
      ),
    );
  }

  Widget _actionBar(BuildContext context, {bool saveEnabled = true}) {
    return Material(
      elevation: 6,
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
        child: SafeArea(
          top: false,
          child: Row(
            children: [
              Expanded(
                child: OutlinedButton(
                  onPressed: _saving ? null : widget.onCancel,
                  child: const Text('Cancel'),
                ),
              ),
              const SizedBox(width: 8),
              Expanded(
                child: FilledButton(
                  onPressed: (_saving || !saveEnabled) ? null : _save,
                  child: _saving
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2))
                      : const Text('Save'),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _editControl(FieldDef f) {
    if (f.dataType == 'boolean') {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: SwitchListTile(
          contentPadding: const EdgeInsets.symmetric(horizontal: 4),
          shape: RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(10)),
          tileColor: Theme.of(context).colorScheme.surfaceContainerHighest,
          title: Text(fieldLabel(f.name)),
          value: _bools[f.name] ?? false,
          onChanged: (v) => setState(() => _bools[f.name] = v),
        ),
      );
    }
    if (_isCoded(f)) return _codedControl(f);
    if (f.dataType == 'date') {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 5),
        child: DateFormField(
          controller: _text[f.name]!,
          decoration: _decoration(f),
          required: _isRequired(f),
          nullable: f.nullable,
        ),
      );
    }

    final isInt = f.dataType == 'integer';
    final isNum = f.dataType == 'numeric';
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 5),
      child: TextFormField(
        controller: _text[f.name],
        keyboardType: isInt
            ? TextInputType.number
            : isNum
                ? const TextInputType.numberWithOptions(decimal: true)
                : (f.dataType == 'jsonb'
                    ? TextInputType.multiline
                    : TextInputType.text),
        minLines: f.dataType == 'jsonb' ? 2 : 1,
        maxLines: f.dataType == 'jsonb' ? 6 : 1,
        inputFormatters: isInt
            ? [FilteringTextInputFormatter.allow(RegExp(r'[0-9-]'))]
            : isNum
                ? [FilteringTextInputFormatter.allow(RegExp(r'[0-9.eE+-]'))]
                : null,
        decoration: _decoration(f),
        validator: (value) {
          final raw = (value ?? '').trim();
          if (raw.isEmpty) {
            if (_isRequired(f)) return 'Required';
            return null;
          }
          if (isInt && int.tryParse(raw) == null) return 'Whole number';
          if (isNum && num.tryParse(raw) == null) return 'Number';
          return null;
        },
      ),
    );
  }

  Widget _codedControl(FieldDef f) {
    final items = _itemsFor(f);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 5),
      child: DropdownButtonFormField<String>(
        value: _codes[f.name],
        isExpanded: true,
        decoration: _decoration(f),
        hint: const Text('Select…'),
        items: [
          if (f.nullable)
            const DropdownMenuItem<String>(
              value: null,
              child: Text('— none —'),
            ),
          for (final dv in items)
            DropdownMenuItem<String>(
              value: dv.code,
              child: Text(dv.label, overflow: TextOverflow.ellipsis),
            ),
        ],
        onChanged: (v) => setState(() => _codes[f.name] = v),
        validator: (v) {
          if ((v == null || v.isEmpty) && _isRequired(f)) return 'Required';
          return null;
        },
      ),
    );
  }

  Widget _lockedControl(FieldDef f) {
    final v = widget.currentValues[f.name];
    // Prefer the domain LABEL over the raw code when we have the pick list.
    String display = _asText(v);
    if (_isCoded(f) && v != null) {
      final match = widget.domainValues[f.domainId]!
          .where((d) => d.code == '$v');
      if (match.isNotEmpty) display = match.first.label;
    }
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 5),
      child: TextFormField(
        enabled: false,
        initialValue: display,
        decoration: _decoration(f, locked: true).copyWith(
          suffixIcon: const Icon(Icons.lock_outline, size: 18),
        ),
      ),
    );
  }

}
