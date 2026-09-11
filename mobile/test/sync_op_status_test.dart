// Unit tests for the SyncOpStatus apply-outcome lifecycle (Phase 23 Slices 2 & 4c).
// These pin down the terminal/silent contract the status pull relies on: which
// outcomes stop polling, which surface to the user, and -- critically -- that
// 'superseded' is terminal but silent (a newer edit to the same feature won).

import 'package:flutter_test/flutter_test.dart';

import 'package:voltedge_field/src/api/models.dart';

SyncOpStatus _status(String status) => SyncOpStatus(opId: 'op-1', status: status);

void main() {
  group('SyncOpStatus lifecycle', () {
    test('in-flight statuses are neither terminal nor surfaced', () {
      for (final s in ['received', 'applying', 'pending_review']) {
        final st = _status(s);
        expect(st.isTerminal, isFalse, reason: '$s should keep polling');
        expect(st.isApplied, isFalse);
        expect(st.isRejected, isFalse);
        expect(st.isSuperseded, isFalse);
      }
    });

    test('applied is a terminal success', () {
      final st = _status('applied');
      expect(st.isApplied, isTrue);
      expect(st.isTerminal, isTrue);
      expect(st.isRejected, isFalse);
      expect(st.isSuperseded, isFalse);
    });

    test('rejected and conflict are terminal failures that surface', () {
      for (final s in ['rejected', 'conflict']) {
        final st = _status(s);
        expect(st.isRejected, isTrue, reason: '$s needs attention');
        expect(st.isTerminal, isTrue);
        expect(st.isApplied, isFalse);
        expect(st.isSuperseded, isFalse);
      }
    });

    test('superseded is terminal but silent (Slice 4c)', () {
      final st = _status('superseded');
      expect(st.isSuperseded, isTrue);
      expect(st.isTerminal, isTrue, reason: 'stop polling a superseded op');
      // Must NOT look like a failure the user has to act on, nor a success.
      expect(st.isRejected, isFalse, reason: 'superseded is not needs-attention');
      expect(st.isApplied, isFalse);
    });

    test('an unknown/future status is treated as non-terminal (keeps polling)', () {
      // Defensive: a status the client doesn't know yet stays in flight rather
      // than being silently dropped -- the reverse of superseded's explicit drop.
      final st = _status('something_new');
      expect(st.isTerminal, isFalse);
    });
  });
}
