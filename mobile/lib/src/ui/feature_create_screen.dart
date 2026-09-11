import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../pack/pack_database.dart';
import 'date_form_field.dart';
import 'format.dart';

/// The offline feature-CREATE form (Phase 21 create slice), a **Scaffold-less
/// inline view** rendered in the same panel slot as the read-only detail and the
/// edit form -- the user never leaves the map. It's a sibling of FeatureEditView
/// rather than a mode of it: create picks a subtype FIRST (which drives which
/// fields are required), offers a feature name, and shows `visible_on_create`
/// fields with no "locked after publish" section (a brand-new feature is never
/// published). The controls, decoration, coercion, and coded-dropdown behaviour
/// mirror the editor so the two forms read as one.
///
/// On Save it hands back the chosen subtype, name, and the collected attribute
/// map; the caller (PackMapPage) turns that into a local create + outbox op via
/// EditStore.createFeature. Server validation still has the final say on sync.
class FeatureCreateView extends StatefulWidget {
  const FeatureCreateView({
    super.key,
    required this.featureClass,
    required this.fields,
    required this.subtypes,
    required this.onCreate,
    required this.onCancel,
    this.domainValues = const {},
    this.initialSubtype,
    this.initialName,
    this.initialAttrs = const {},
    this.saveLabel = 'Create',
    this.padding,
  });

  final String featureClass;
  final List<FieldDef> fields;

  /// The class's subtypes. When non-empty the form shows a required subtype
  /// picker at the top; when empty it's skipped (subtype stays null).
  final List<SubtypeDef> subtypes;

  /// Coded-domain pick lists, keyed by domain id (code stored, label shown).
  final Map<String, List<DomainValue>> domainValues;

  /// Prefill (used when re-opening an un-synced create to edit it in place).
  final int? initialSubtype;
  final String? initialName;
  final Map<String, dynamic> initialAttrs;

  final String saveLabel;

  /// Called with the collected values on Save. Awaited so the parent can persist
  /// before leaving create mode.
  final Future<void> Function({
    int? subtype,
    String? name,
    required Map<String, dynamic> attributes,
  }) onCreate;

  final VoidCallback onCancel;
  final EdgeInsetsGeometry? padding;

  @override
  State<FeatureCreateView> createState() => _FeatureCreateViewState();
}

class _FeatureCreateViewState extends State<FeatureCreateView> {
  final _formKey = GlobalKey<FormState>();

  final Map<String, TextEditingController> _text = {};
  final Map<String, bool> _bools = {};
  final Map<String, String?> _codes = {};
  late final TextEditingController _name;

  /// The chosen subtype code (null until picked, or if the class has none). Drives
  /// which fields are required, so changing it re-validates and repaints the `*`s.
  int? _subtype;

  late final List<FieldDef> _createFields;
  bool _saving = false;

  bool _isCoded(FieldDef f) {
    final id = f.domainId;
    return id != null && (widget.domainValues[id]?.isNotEmpty ?? false);
  }

  /// Whether [f] is required for the currently-picked subtype. With no subtype
  /// picked (or a class that has none), nothing is required -- same rule the web
  /// create form and the offline editor use.
  bool _isRequired(FieldDef f) {
    final st = _subtype;
    return st != null && f.requiredForSubtypes.contains(st);
  }

  @override
  void initState() {
    super.initState();
    _subtype = widget.initialSubtype;
    _name = TextEditingController(text: widget.initialName ?? '');

    // Fields offered on create: visible_on_create and not computed. (Publish
    // locking doesn't apply -- a new feature is unpublished.)
    _createFields = [
      for (final f in widget.fields)
        if (f.visibleOnCreate && !f.isComputed) f,
    ];

    for (final f in _createFields) {
      final v = widget.initialAttrs[f.name];
      if (f.dataType == 'boolean') {
        _bools[f.name] = v == true;
      } else if (_isCoded(f)) {
        _codes[f.name] = v == null ? null : '$v';
      } else if (f.dataType == 'date') {
        _text[f.name] = TextEditingController(text: normalizeIsoDate(v));
      } else {
        _text[f.name] = TextEditingController(text: v == null ? '' : '$v');
      }
    }
  }

  @override
  void dispose() {
    _name.dispose();
    for (final c in _text.values) {
      c.dispose();
    }
    super.dispose();
  }

  List<DomainValue> _itemsFor(FieldDef f) {
    final base = widget.domainValues[f.domainId] ?? const <DomainValue>[];
    final current = _codes[f.name];
    if (current == null || current.isEmpty) return base;
    if (base.any((d) => d.code == current)) return base;
    return [DomainValue(code: current, label: '$current (current)'), ...base];
  }

  /// The coerced value for a field, typed to match the server field type so the
  /// create validates on sync. Empty text / unselected dropdown -> null for a
  /// nullable field, else an empty string.
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

  Future<void> _save() async {
    // A class with subtypes must have one chosen before we can even judge
    // requiredness, so guard it explicitly (the picker's own validator also flags
    // it, but this keeps the message clear).
    if (widget.subtypes.isNotEmpty && _subtype == null) {
      _formKey.currentState?.validate();
      return;
    }
    final form = _formKey.currentState;
    if (form == null || !form.validate()) return;
    setState(() => _saving = true);

    final attrs = <String, dynamic>{};
    for (final f in _createFields) {
      attrs[f.name] = _valueFor(f);
    }
    final name = _name.text.trim();

    try {
      await widget.onCreate(
        subtype: _subtype,
        name: name.isEmpty ? null : name,
        attributes: attrs,
      );
    } catch (e) {
      if (!mounted) return;
      setState(() => _saving = false);
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not create feature: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Column(
      children: [
        Expanded(
          child: Form(
            key: _formKey,
            child: ListView(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12)
                  .add(widget.padding ?? EdgeInsets.zero),
              children: [
                _sectionLabel(context, 'Identity'),
                if (widget.subtypes.isNotEmpty) _subtypeControl(),
                _nameControl(),
                if (_createFields.isNotEmpty) ...[
                  const SizedBox(height: 20),
                  _sectionLabel(context, 'Attributes'),
                  for (final f in _createFields) _createControl(f),
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

  /// Decoration with a humanized label and a red `*` when [required].
  InputDecoration _decoration(String label, {bool required = false}) {
    final theme = Theme.of(context);
    return InputDecoration(
      label: Text.rich(
        TextSpan(
          text: label,
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

  Widget _subtypeControl() {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 5),
      child: DropdownButtonFormField<int>(
        value: _subtype,
        isExpanded: true,
        decoration: _decoration('Subtype', required: true),
        hint: const Text('Select…'),
        items: [
          for (final s in widget.subtypes)
            DropdownMenuItem<int>(
              value: s.code,
              child: Text(s.name, overflow: TextOverflow.ellipsis),
            ),
        ],
        // Repaint required markers/validators against the new subtype.
        onChanged: (v) => setState(() => _subtype = v),
        validator: (v) => v == null ? 'Required' : null,
      ),
    );
  }

  Widget _nameControl() {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 5),
      child: TextFormField(
        controller: _name,
        decoration: _decoration('Name'),
        textCapitalization: TextCapitalization.sentences,
      ),
    );
  }

  Widget _createControl(FieldDef f) {
    final label = fieldLabel(f.name);
    final required = _isRequired(f);
    if (f.dataType == 'boolean') {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: SwitchListTile(
          contentPadding: const EdgeInsets.symmetric(horizontal: 4),
          shape:
              RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
          tileColor: Theme.of(context).colorScheme.surfaceContainerHighest,
          title: Text(label),
          value: _bools[f.name] ?? false,
          onChanged: (v) => setState(() => _bools[f.name] = v),
        ),
      );
    }
    if (_isCoded(f)) {
      final items = _itemsFor(f);
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 5),
        child: DropdownButtonFormField<String>(
          value: _codes[f.name],
          isExpanded: true,
          decoration: _decoration(label, required: required),
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
            if ((v == null || v.isEmpty) && required) return 'Required';
            return null;
          },
        ),
      );
    }

    if (f.dataType == 'date') {
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 5),
        child: DateFormField(
          controller: _text[f.name]!,
          decoration: _decoration(label, required: required),
          required: required,
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
        decoration: _decoration(label, required: required),
        validator: (value) {
          final raw = (value ?? '').trim();
          if (raw.isEmpty) {
            if (required) return 'Required';
            return null;
          }
          if (isInt && int.tryParse(raw) == null) return 'Whole number';
          if (isNum && num.tryParse(raw) == null) return 'Number';
          return null;
        },
      ),
    );
  }

  Widget _actionBar(BuildContext context) {
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
                  onPressed: _saving ? null : _save,
                  child: _saving
                      ? const SizedBox(
                          width: 18,
                          height: 18,
                          child: CircularProgressIndicator(strokeWidth: 2))
                      : Text(widget.saveLabel),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
