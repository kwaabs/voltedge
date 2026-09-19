/// Wire + on-device models for the offline Faults module (mobile mirror of the
/// Faults server module, [[faults-module]] / [[offline-wam-field-pack-and-sync]]).
/// A "fault" is an electrical fault EVENT on the network -- an earth fault, a phase
/// short, a protection operation -- NOT a GIS feature. This is the faithful twin of
/// the offline WAM models (see wam/wam_models.dart): two families live here.
///
///   * The PACK-READ row ([PackFault]) mirrors the read-only `faults_events` device
///     table the field pack ships (built by internal/fieldpack/builder.go). It is
///     read-only truth: the OPEN faults on the pack's assets (the builder ships only
///     faults with a matchable asset_id and a non-terminal status).
///   * The LOCAL-CAPTURE rows ([LocalFault], [FaultStatusOverride]) mirror the
///     writable `edits.sqlite` fault tables the offline capture flow appends to. Each
///     capture also queues a `fault_*` outbox op replayed verbatim through POST /sync
///     -- the SAME pipeline feature edits and WAM ops ride (no faults-specific sync).
///     The payload each op carries is built to match the Go payload structs in
///     internal/fieldpack/sync.go exactly (asset_class/fault_type/... JSON keys), so
///     a mismatched key never silently drops a field on the server.
///
/// Record-CREATING captures (a new fault) carry a device-minted UUID v4 as BOTH the
/// outbox op id AND the server row's primary key (via op.FeatureID), so a re-push
/// after a crash is idempotent -- the server no-ops an already-created fault. Status
/// captures are deterministic last-write-wins writes on an existing fault id, each a
/// distinct outbox op deduped to the latest-per-fault. Booking a WAM defect from a
/// fault is a fire-and-forget op with no local mirror row (the store tracks booked
/// fault ids in memory so the UI hides the button).

library;

// ---- Pack-read row (the faults_events device table) -------------------------

/// One OPEN fault on a packed asset, from the pack's `faults_events` table. Keyed
/// off a bare (asset_class, asset_id) ref to a GIS feature -- faults never FK into
/// gis, so the device resolves the asset by id when it needs to show it on the map.
/// [defectId] is set once a WAM defect has been booked against the fault (the server
/// links it back), so the UI hides "Book defect" for an already-booked fault.
class PackFault {
  const PackFault({
    required this.id,
    required this.assetClass,
    required this.assetId,
    required this.status,
    this.reference,
    this.faultType,
    this.cause,
    this.title,
    this.description,
    this.detectedAt,
    this.longitude,
    this.latitude,
    this.protectiveDeviceClass,
    this.protectiveDeviceId,
    this.incidentId,
    this.defectId,
  });

  final String id;
  final String assetClass;
  final String assetId;
  final String status;
  final String? reference;
  final String? faultType;
  final String? cause;
  final String? title;
  final String? description;
  final String? detectedAt;
  final double? longitude;
  final double? latitude;
  final String? protectiveDeviceClass;
  final String? protectiveDeviceId;
  final String? incidentId;
  final String? defectId;
}

// ---- Local-capture rows (the writable edits.sqlite fault_* tables) ----------

/// A fault RAISED offline. Unlike a [PackFault] -- server truth shipped read-only --
/// this is a whole new fault event that exists only on-device until POST /sync
/// replays its `fault_create` op. [id] is a client-minted UUID v4 that doubles as
/// the outbox op id AND becomes the server row's primary key, so a re-push is
/// idempotent (the server no-ops an already-created fault). A fault is anchored to
/// an ASSET (both assetClass and assetId), a LOCATION (both longitude and latitude),
/// or an INCIDENT (incidentId) -- at least one anchor is required.
class LocalFault {
  const LocalFault({
    required this.id,
    required this.assetClass,
    required this.assetId,
    required this.status,
    required this.createdAt,
    this.reference,
    this.faultType,
    this.cause,
    this.title,
    this.description,
    this.detectedAt,
    this.longitude,
    this.latitude,
    this.protectiveDeviceClass,
    this.protectiveDeviceId,
    this.faultCurrentA,
    this.clearingTimeMs,
    this.incidentId,
  });

  final String id;
  final String assetClass;
  final String assetId;
  final String status;
  final DateTime createdAt;
  final String? reference;
  final String? faultType;
  final String? cause;
  final String? title;
  final String? description;
  final String? detectedAt;
  final double? longitude;
  final double? latitude;
  final String? protectiveDeviceClass;
  final String? protectiveDeviceId;
  final double? faultCurrentA;
  final double? clearingTimeMs;
  final String? incidentId;
}

/// A fault STATUS change captured offline. A deterministic last-write-wins write on
/// an existing fault id (packed or locally-raised), so it needs no UUID of its own
/// -- its outbox op is keyed by a fresh op id and targets the fault via op.FeatureID.
/// Mirrored here (keyed by fault id, latest wins) so the UI reflects the new status
/// optimistically before sync. Valid statuses: detected/investigating/cleared/closed.
class FaultStatusOverride {
  const FaultStatusOverride({
    required this.faultId,
    required this.status,
    required this.updatedAt,
  });

  final String faultId;
  final String status;
  final DateTime updatedAt;
}

// ---- Merged read view (what the store hands the UI) -------------------------

/// A fault as the crew SEES it: either a packed fault with an optional offline
/// status override, or a whole fault raised offline ([isLocal]). Unifies the two
/// sources so a board renders them the same, with [pending] marking a capture (or
/// status change) still queued for POST /sync and [booked] hiding "Book defect" once
/// a defect has been booked against the fault (packed defect_id or an offline book).
/// Built by the store via [FaultView.fromPacked] / [FaultView.fromLocal].
class FaultView {
  const FaultView({
    required this.id,
    required this.assetClass,
    required this.assetId,
    required this.status,
    required this.isLocal,
    required this.pending,
    required this.booked,
    this.reference,
    this.faultType,
    this.cause,
    this.title,
    this.description,
    this.detectedAt,
    this.longitude,
    this.latitude,
    this.protectiveDeviceClass,
    this.protectiveDeviceId,
    this.incidentId,
  });

  final String id;
  final String assetClass;
  final String assetId;
  final String status;

  /// True for a fault raised offline (no packed/server row yet); false for a packed
  /// fault (its status may still carry a local override).
  final bool isLocal;

  /// True while a capture or status change for this fault is queued for sync.
  final bool pending;

  /// True once a WAM defect has been booked against this fault (packed defect_id, or
  /// an offline book queued this session) -- the UI hides the "Book defect" action.
  final bool booked;

  final String? reference;
  final String? faultType;
  final String? cause;
  final String? title;
  final String? description;
  final String? detectedAt;
  final double? longitude;
  final double? latitude;
  final String? protectiveDeviceClass;
  final String? protectiveDeviceId;
  final String? incidentId;

  /// True when the fault carries an asset or a location -- the only faults a WAM
  /// defect can be booked against (an incident-only fault has nowhere to hang it).
  bool get canBookDefect =>
      assetId.isNotEmpty || (longitude != null && latitude != null);

  /// A packed fault with an optional offline status [override] folded on top.
  factory FaultView.fromPacked(
    PackFault f, {
    FaultStatusOverride? override,
    required bool pending,
    required bool booked,
  }) =>
      FaultView(
        id: f.id,
        assetClass: f.assetClass,
        assetId: f.assetId,
        status: override?.status ?? f.status,
        isLocal: false,
        pending: pending,
        booked: booked || f.defectId != null,
        reference: f.reference,
        faultType: f.faultType,
        cause: f.cause,
        title: f.title,
        description: f.description,
        detectedAt: f.detectedAt,
        longitude: f.longitude,
        latitude: f.latitude,
        protectiveDeviceClass: f.protectiveDeviceClass,
        protectiveDeviceId: f.protectiveDeviceId,
        incidentId: f.incidentId,
      );

  /// A fault raised offline, with an optional later status [override] folded on.
  factory FaultView.fromLocal(
    LocalFault f, {
    FaultStatusOverride? override,
    required bool pending,
    required bool booked,
  }) =>
      FaultView(
        id: f.id,
        assetClass: f.assetClass,
        assetId: f.assetId,
        status: override?.status ?? f.status,
        isLocal: true,
        pending: pending,
        booked: booked,
        reference: f.reference,
        faultType: f.faultType,
        cause: f.cause,
        title: f.title,
        description: f.description,
        detectedAt: f.detectedAt,
        longitude: f.longitude,
        latitude: f.latitude,
        protectiveDeviceClass: f.protectiveDeviceClass,
        protectiveDeviceId: f.protectiveDeviceId,
        incidentId: f.incidentId,
      );
}
