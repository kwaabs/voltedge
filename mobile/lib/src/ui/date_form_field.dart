import 'package:flutter/material.dart';

import 'format.dart';

/// A tap-to-pick date field for `date`-typed attributes, shared by the create and
/// edit forms. It's backed by a [TextEditingController] holding an ISO
/// `yyyy-MM-dd` string (what the server's `date` fields expect), so it drops into
/// the same controller map and save path the text controls already use.
///
/// The text is read-only -- the value only changes through the calendar picker --
/// and the picker never offers a future date (`lastDate` is today), per the
/// field-tool rule that a survey/inspection date can't be in the future.
class DateFormField extends StatefulWidget {
  const DateFormField({
    super.key,
    required this.controller,
    required this.decoration,
    this.required = false,
    this.nullable = true,
  });

  /// Holds the selected date as `yyyy-MM-dd` (empty when unset).
  final TextEditingController controller;

  /// The field's base decoration (label/hint/etc.); this widget adds the trailing
  /// calendar / clear affordance on top.
  final InputDecoration decoration;

  /// Marks the field required -- drives the empty-value validator message.
  final bool required;

  /// When true (and a value is set) a clear button is offered to unset the date.
  final bool nullable;

  @override
  State<DateFormField> createState() => _DateFormFieldState();
}

class _DateFormFieldState extends State<DateFormField> {
  @override
  void initState() {
    super.initState();
    // Rebuild when the value changes so the trailing icon (calendar vs clear)
    // stays in sync.
    widget.controller.addListener(_onChanged);
  }

  @override
  void dispose() {
    widget.controller.removeListener(_onChanged);
    super.dispose();
  }

  void _onChanged() {
    if (mounted) setState(() {});
  }

  Future<void> _pick() async {
    final now = DateTime.now();
    final today = DateTime(now.year, now.month, now.day);
    final current = parseIsoDate(widget.controller.text);
    // Open on the current value when it's valid and not in the future; else today.
    final initial =
        (current != null && !current.isAfter(today)) ? current : today;
    final picked = await showDatePicker(
      context: context,
      initialDate: initial,
      firstDate: DateTime(1900),
      lastDate: today, // no future dates
    );
    if (picked != null) {
      widget.controller.text = formatIsoDate(picked);
    }
  }

  @override
  Widget build(BuildContext context) {
    final hasValue = widget.controller.text.trim().isNotEmpty;
    return TextFormField(
      controller: widget.controller,
      readOnly: true,
      onTap: _pick,
      decoration: widget.decoration.copyWith(
        suffixIcon: hasValue && widget.nullable
            ? IconButton(
                icon: const Icon(Icons.clear, size: 18),
                tooltip: 'Clear',
                onPressed: () => widget.controller.clear(),
              )
            : const Icon(Icons.calendar_today_outlined, size: 18),
      ),
      validator: (value) {
        if ((value ?? '').trim().isEmpty && widget.required) return 'Required';
        return null;
      },
    );
  }
}
