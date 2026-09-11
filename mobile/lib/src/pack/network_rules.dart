import 'pack_database.dart';

/// The verdict of an offline connectivity check.
///
/// This is a port of the server's api/internal/connectivity matcher: the field
/// tool checks a proposed connection against the pack's `network_rules` before
/// establishing it and ENFORCES the result -- an edge the packed rules reject is
/// blocked in the field, exactly as the web/REST surface blocks it, instead of
/// being queued for the server to bounce on sync. Enforcement only engages when
/// the pack actually ships rules (v7+): a pre-v7 pack that carries none can't be
/// judged, so [unknown] proceeds and the server stays the sole backstop there.
/// The outcomes mirror what matchRule distinguishes: a valid rule authorizes it,
/// a disabled rule is a different (fixable) rejection than no rule at all.
enum ConnectivityAdvice {
  /// A valid (is_valid = 1) rule matched -- the connection is legal offline.
  allowed,

  /// A rule matched but is disabled (is_valid = 0) -- authored then turned off.
  disabled,

  /// No rule matched this class/subtype/terminal shape at all (zero-trust: an
  /// unmatched pair is invalid).
  noRule,

  /// The pack predates network_rules (pre-v7) or ships none at all -- nothing to
  /// judge by, so the flow proceeds and the server stays the sole authority.
  unknown,
}

/// The outcome of [checkConnectivity]: the advice plus the matched rule (when
/// [advice] is [ConnectivityAdvice.allowed]) and a human message for the warning
/// sheet. [isBlocked] is the single flag the connect flow gates the edit on.
class ConnectivityCheck {
  const ConnectivityCheck(this.advice, {this.rule, required this.message});

  final ConnectivityAdvice advice;
  final NetworkRuleDef? rule;
  final String message;

  /// Whether this connection must be refused. `unknown` (the pack ships no rules
  /// to judge by) and `allowed` proceed; `disabled` and `noRule` are blocked.
  bool get isBlocked =>
      advice == ConnectivityAdvice.disabled ||
      advice == ConnectivityAdvice.noRule;
}

/// checkConnectivity is the field-tool port of api/internal/connectivity's
/// matchRuleEitherDirection + matchRule. It scores every rule the pack ships for
/// the (from, to) class pair -- in EITHER orientation, since the server authorizes
/// a rule authored one way for a request made either way -- and returns the
/// most-specific valid match, or the reason none applied.
///
/// The phase gate (checkPhaseCompatibility, opt-in via phase_rule =
/// "phases_intersect") is deliberately NOT ported here: the field tool doesn't yet
/// resolve an endpoint's live phase offline, and the server re-checks phase on
/// sync regardless. A rule that opts into phasing still matches on its
/// subtype/terminal shape here; the phase constraint is simply left to the sync
/// backstop.
///
/// [rules] is what [PackDatabase.networkRulesFor] returns (both orientations).
/// The subtype args are integer subtype CODES and the terminal args are terminal
/// CODES -- exactly what the pack stores, so no UUID resolution happens on-device.
ConnectivityCheck checkConnectivity({
  required String fromClass,
  int? fromSubtype,
  String? fromTerminal,
  required String toClass,
  int? toSubtype,
  String? toTerminal,
  required List<NetworkRuleDef> rules,
  bool packHasRules = false,
}) {
  if (rules.isEmpty) {
    // No rule touches this class pair. When the pack ships network_rules at all
    // ([packHasRules], v7+) that's a zero-trust rejection -- exactly the server's
    // "absence of a rule means invalid" -- so block it. When the pack ships none
    // there's nothing to judge by; proceed and let the sync backstop decide.
    return packHasRules
        ? const ConnectivityCheck(
            ConnectivityAdvice.noRule,
            message: 'No network rule permits this connection, so it can\'t be '
                'created here.',
          )
        : const ConnectivityCheck(ConnectivityAdvice.unknown, message: '');
  }

  NetworkRuleDef? best;
  var bestScore = -1;
  var disabledMatch = false;

  for (final r in rules) {
    final applies = _ruleApplies(
      r,
      fromClass: fromClass,
      fromSubtype: fromSubtype,
      fromTerminal: fromTerminal,
      toClass: toClass,
      toSubtype: toSubtype,
      toTerminal: toTerminal,
    );
    if (!applies) continue;
    if (!r.isValid) {
      disabledMatch = true;
      continue;
    }
    final score = _specificity(r);
    if (score > bestScore) {
      best = r;
      bestScore = score;
    }
  }

  if (best != null) {
    return ConnectivityCheck(
      ConnectivityAdvice.allowed,
      rule: best,
      message: '',
    );
  }
  if (disabledMatch) {
    return const ConnectivityCheck(
      ConnectivityAdvice.disabled,
      message: 'A network rule exists for this connection but is disabled, so '
          'it can\'t be created here.',
    );
  }
  return const ConnectivityCheck(
    ConnectivityAdvice.noRule,
    message: 'No network rule permits this connection, so it can\'t be created '
        'here (zero trust: absence of a rule means invalid).',
  );
}

/// Whether [r] -- in whichever orientation its class pair matches the request --
/// applies to this connection, using the wildcard-or-exact match on each of the
/// four subtype/terminal axes (a null rule value matches anything). Mirrors Go's
/// ruleApplies, but resolves the rule's orientation first since the pack returns
/// both directions in one list.
bool _ruleApplies(
  NetworkRuleDef r, {
  required String fromClass,
  int? fromSubtype,
  String? fromTerminal,
  required String toClass,
  int? toSubtype,
  String? toTerminal,
}) {
  final forward = r.fromClass == fromClass && r.toClass == toClass;
  final swapped = r.fromClass == toClass && r.toClass == fromClass;
  if (!forward && !swapped) return false;

  // In the forward orientation the rule's from-* axes constrain the request's
  // from-* values; in the swapped orientation they constrain the request's to-*
  // values (and vice versa), matching matchRuleEitherDirection's swapped call.
  final aSub = forward ? fromSubtype : toSubtype;
  final aTerm = forward ? fromTerminal : toTerminal;
  final bSub = forward ? toSubtype : fromSubtype;
  final bTerm = forward ? toTerminal : fromTerminal;

  return _intMatches(r.fromSubtype, aSub) &&
      _intMatches(r.toSubtype, bSub) &&
      _strMatches(r.fromTerminal, aTerm) &&
      _strMatches(r.toTerminal, bTerm);
}

/// A null rule value is a wildcard; a set value requires the actual to be present
/// and equal (Go's idMatches, for integer subtype codes).
bool _intMatches(int? ruleValue, int? actual) {
  if (ruleValue == null) return true;
  return actual != null && actual == ruleValue;
}

/// The string (terminal code) counterpart of [_intMatches]. Treats an empty rule
/// value as a wildcard too, since the pack stores a wildcard as SQL NULL.
bool _strMatches(String? ruleValue, String? actual) {
  if (ruleValue == null || ruleValue.isEmpty) return true;
  return actual != null && actual == ruleValue;
}

/// Counts how many of the rule's four subtype/terminal axes are pinned rather
/// than wildcarded -- more pins = more specific = higher priority. Go's
/// ruleSpecificity.
int _specificity(NetworkRuleDef r) {
  var score = 0;
  if (r.fromSubtype != null) score++;
  if (r.toSubtype != null) score++;
  if (r.fromTerminal != null && r.fromTerminal!.isNotEmpty) score++;
  if (r.toTerminal != null && r.toTerminal!.isNotEmpty) score++;
  return score;
}
