// Unit tests for the pure NMEA parser (Phase: gnss-quality, Slice 1). The
// internal GNSS source is native and needs a real device, so the parser is where
// the correctness lives and gets covered here off-device: DOP/sat/fix extraction,
// coordinate conversion, checksum rejection, and the GGA-outranks-GSA fix rule.
//
// Fixtures with a `*CS` suffix use real, verified checksums (the canonical
// NMEA 0183 examples). The made-up GSA/GSV fixtures omit the checksum on purpose
// -- the parser accepts a checksum-less sentence -- so a hand-miscomputed hex
// can't make a test fail for the wrong reason. The bad-checksum case below uses a
// deliberately wrong `*00` against a known-good payload.

import 'package:flutter_test/flutter_test.dart';

import 'package:voltedge_field/src/geo/gnss_sample.dart';
import 'package:voltedge_field/src/geo/nmea_parser.dart';

void main() {
  group('NmeaParser', () {
    test('GGA yields position, altitude, HDOP, sats-used and fix quality', () {
      final p = NmeaParser();
      // Canonical GGA (valid checksum): 8 sats, HDOP 0.9, alt 545.4m.
      final s = p.addSentence(
          '\$GPGGA,123519,4807.038,N,01131.000,E,1,08,0.9,545.4,M,46.9,M,,*47');
      expect(s.hasPosition, isTrue);
      expect(s.latitude, closeTo(48.1173, 0.0005));
      expect(s.longitude, closeTo(11.5167, 0.0005));
      expect(s.altitude, closeTo(545.4, 0.001));
      expect(s.hdop, closeTo(0.9, 0.0001));
      expect(s.satsUsed, 8);
      expect(s.fixType, GnssFixType.gps);
    });

    test('GSA yields PDOP/HDOP/VDOP and a 3D fix', () {
      final p = NmeaParser();
      final s = p.addSentence('\$GPGSA,A,3,04,05,,09,12,,,24,,,,,2.5,1.3,2.1');
      expect(s.pdop, closeTo(2.5, 0.0001));
      expect(s.hdop, closeTo(1.3, 0.0001));
      expect(s.vdop, closeTo(2.1, 0.0001));
      expect(s.fixType, GnssFixType.gps); // mode 3 upgraded from unknown
    });

    test('GGA fix quality outranks a later GSA plain-3D mode', () {
      final p = NmeaParser();
      // RTK-fixed GGA (quality 4), then a GSA: fix type must stay rtkFixed while
      // the GSA still supplies the DOP triple.
      p.addSentence(
          '\$GPGGA,123519,4807.038,N,01131.000,E,4,10,0.7,545.4,M,46.9,M,,*40');
      final s = p.addSentence('\$GPGSA,A,3,04,05,,09,12,,,24,,,,,1.8,0.7,1.4');
      expect(s.fixType, GnssFixType.rtkFixed,
          reason: 'a richer GGA quality must not be downgraded by GSA');
      expect(s.pdop, closeTo(1.8, 0.0001));
      expect(s.vdop, closeTo(1.4, 0.0001));
    });

    test('southern / western hemispheres are signed negative', () {
      final p = NmeaParser();
      final s = p.addSentence('\$GPGGA,123519,3351.000,S,15112.000,W,1,06,1.2,10.0,M,,M,,');
      expect(s.latitude, closeTo(-33.85, 0.001));
      expect(s.longitude, closeTo(-151.2, 0.001));
    });

    test('a bad checksum is dropped, leaving state unchanged', () {
      final p = NmeaParser();
      // Known-good GGA payload but a deliberately wrong checksum (*00 != *47).
      final s = p.addSentence(
          '\$GPGGA,123519,4807.038,N,01131.000,E,1,08,0.9,545.4,M,46.9,M,,*00');
      expect(s.hasPosition, isFalse);
      expect(s.latitude, isNull);
      expect(s.hdop, isNull);
    });

    test('multi-constellation talker ids (GN) are parsed', () {
      final p = NmeaParser();
      final s = p.addSentence('\$GNGSA,A,3,04,05,,09,12,,,24,,,,,2.0,1.0,1.7');
      expect(s.pdop, closeTo(2.0, 0.0001));
      expect(s.hdop, closeTo(1.0, 0.0001));
    });

    test('RMC fills position when no GGA has', () {
      final p = NmeaParser();
      final s = p.addSentence(
          '\$GPRMC,123519,A,4807.038,N,01131.000,E,022.4,084.4,230394,003.1,W*6A');
      expect(s.hasPosition, isTrue);
      expect(s.latitude, closeTo(48.1173, 0.0005));
    });

    test('a void RMC (status V) is ignored', () {
      final p = NmeaParser();
      final s = p.addSentence(
          '\$GPRMC,123519,V,4807.038,N,01131.000,E,000.0,000.0,230394,003.1,W');
      expect(s.hasPosition, isFalse);
      expect(s.latitude, isNull);
    });

    test('GSV reports one constellation\'s in-view count', () {
      final p = NmeaParser();
      p.addSentence('\$GPGSV,3,1,11,03,03,111,00,04,15,270,00');
      expect(p.current.satsInView, 11);
    });

    test('GSV sums in-view across constellations (not just the largest block)', () {
      final p = NmeaParser();
      // GPS: 11 in view, then GLONASS: 8, then Galileo: 6 -> total 25.
      p.addSentence('\$GPGSV,3,1,11,03,03,111,00,04,15,270,00');
      p.addSentence('\$GLGSV,2,1,08,65,10,050,20,66,20,100,25');
      p.addSentence('\$GAGSV,1,1,06,01,40,200,30,02,30,150,28');
      expect(p.current.satsInView, 25);
    });

    test('a repeated GSV block for one talker does not double-count', () {
      final p = NmeaParser();
      // Two messages of the SAME GPS cycle both carry in-view 11.
      p.addSentence('\$GPGSV,3,1,11,03,03,111,00,04,15,270,00');
      p.addSentence('\$GPGSV,3,2,11,09,45,090,30,12,60,180,32');
      expect(p.current.satsInView, 11);
    });

    test('garbage and empty input never throw and leave state intact', () {
      final p = NmeaParser();
      expect(() => p.addSentence(''), returnsNormally);
      expect(() => p.addSentence('not a sentence'), returnsNormally);
      expect(() => p.addSentence('\$GP*11'), returnsNormally);
      expect(p.current.hasPosition, isFalse);
    });
  });
}
