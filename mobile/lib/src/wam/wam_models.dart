/// Wire + on-device models for offline Work & Asset Management (Phase 59 mobile,
/// [[offline-wam-field-pack-and-sync]]). Two families live here:
///
///   * The PACK-READ rows (`PackDefect`, `PackWorkOrder`, `PackInspectionTemplate`,
///     `PackInspectionTemplateItem`) mirror the four `wam_*` device tables the
///     field pack ships (pack schema v8, see internal/fieldpack/builder.go). They
///     are read-only truth: the crew's assigned work board, the open defects on
///     packed assets, and the active inspection checklists.
///   * The LOCAL-CAPTURE rows (`LocalWamDefect`, `WamDefectStatusOverride`,
///     `WamWorkOrderOverride`, `LocalWamInspection`) mirror the writable
///     `edits.sqlite` tables the offline capture flow appends to. Each capture also
///     queues a `wam_*` outbox op replayed verbatim through POST /sync -- the SAME
///     pipeline feature edits ride (no WAM-specific sync). The payload each op
///     carries is built to match the Go payload structs in internal/fieldpack/sync.go
///     exactly (asset_class/asset_id/... JSON keys), so a mismatched key never
///     silently drops a field on the server.
///
/// Record-CREATING captures (a new defect, a recorded inspection) carry a
/// device-minted UUID v4 as BOTH the outbox op id AND the server row's primary key
/// (via op.FeatureID), so a re-push after a crash is idempotent -- the server
/// no-ops an already-created row (ON CONFLICT (id) DO NOTHING for a defect; an
/// EXISTS guard for an inspection, which has side effects). Status/update captures
/// are deterministic last-write-wins writes on an existing id, each a distinct
/// outbox op (never deduped) so a start-then-complete sequence replays in order.

library;

// ---- Pack-read rows (the four wam_* device tables) --------------------------

/// One open/work-ordered defect on a packed asset, from the pack's `wam_defects`
/// table (pack schema v8). Keyed off a bare (asset_class, asset_id) ref to a GIS
/// feature -- WAM never FKs into gis, so the device resolves the asset by id when
/// it needs to show it on the map.
class PackDefect {
  const PackDefect({
    required this.id,
    required this.assetClass,
    required this.assetId,
    required this.severity,
    required this.status,
    this.defectType,
    this.source,
    this.title,
    this.description,
    this.reportedAt,
    this.notes,
  });

  final String id;
  final String assetClass;
  final String assetId;
  final String severity;
  final String status;
  final String? defectType;
  final String? source;
  final String? title;
  final String? description;
  final String? reportedAt;
  final String? notes;
}

/// One work order on the pack requester's own field work board, from the pack's
/// `wam_work_orders` table (pack schema v8). The pack ships only the requester's
/// non-terminal (not completed/cancelled) orders, regardless of bbox -- a job is a
/// job wherever its asset sits. A start / completion rides back as a
/// `wam_workorder_update` sync op.
class PackWorkOrder {
  const PackWorkOrder({
    required this.id,
    required this.workType,
    required this.status,
    this.reference,
    this.priority,
    this.title,
    this.description,
    this.assetClass,
    this.assetId,
    this.defectId,
    this.assignedTo,
    this.scheduledFor,
    this.dueBy,
    this.startedAt,
    this.completionNotes,
  });

  final String id;
  final String workType;
  final String status;
  final String? reference;
  final String? priority;
  final String? title;
  final String? description;
  final String? assetClass;
  final String? assetId;
  final String? defectId;
  final String? assignedTo;
  final String? scheduledFor;
  final String? dueBy;
  final String? startedAt;
  final String? completionNotes;
}

/// One active inspection checklist, from the pack's `wam_inspection_templates`
/// table (pack schema v8). [assetClass] narrows which assets it applies to (null
/// = any). Its ordered [PackInspectionTemplateItem]s are read separately.
class PackInspectionTemplate {
  const PackInspectionTemplate({
    required this.id,
    this.code,
    this.name,
    this.description,
    this.assetClass,
    this.active = true,
  });

  final String id;
  final String? code;
  final String? name;
  final String? description;
  final String? assetClass;
  final bool active;

  String get label => (name != null && name!.isNotEmpty)
      ? name!
      : (code != null && code!.isNotEmpty ? code! : id);
}

/// One ordered prompt in an inspection checklist, from the pack's
/// `wam_inspection_template_items` table (pack schema v8). A failing item that
/// carries a [failSeverity] auto-raises a defect on sync (server RecordInspection),
/// so the runner surfaces that severity when the crew marks it failed.
class PackInspectionTemplateItem {
  const PackInspectionTemplateItem({
    required this.id,
    required this.templateId,
    this.position = 0,
    this.prompt,
    this.responseType,
    this.failSeverity,
    this.guidance,
  });

  final String id;
  final String templateId;
  final int position;
  final String? prompt;
  final String? responseType; // pass_fail | text | numeric (server ResponseType)
  final String? failSeverity;
  final String? guidance;
}

// ---- Local-capture rows (the writable edits.sqlite wam_* tables) ------------

/// A defect CAPTURED offline (Phase 59 mobile). Unlike a [PackDefect] -- server
/// truth shipped read-only -- this is a whole new defect that exists only
/// on-device until POST /sync replays its `wam_defect_create` op. [id] is a
/// client-minted UUID v4 that doubles as the outbox op id AND becomes the server
/// row's primary key, so a re-push is idempotent (the server no-ops an already-
/// created defect). Keyed off the bare (assetClass, assetId) asset ref, the same
/// shape the server's wam engine wants.
class LocalWamDefect {
  const LocalWamDefect({
    required this.id,
    required this.assetClass,
    required this.assetId,
    required this.severity,
    required this.status,
    required this.createdAt,
    this.defectType,
    this.source,
    this.title,
    this.description,
    this.reportedAt,
    this.notes,
  });

  final String id;
  final String assetClass;
  final String assetId;
  final String severity;
  final String status;
  final DateTime createdAt;
  final String? defectType;
  final String? source;
  final String? title;
  final String? description;
  final String? reportedAt;
  final String? notes;
}

/// A defect STATUS change captured offline. A deterministic last-write-wins write
/// on an existing defect id (packed or locally-created), so it needs no UUID of
/// its own -- its outbox op is keyed by a fresh op id and targets the defect via
/// op.FeatureID. Mirrored here (keyed by defect id, latest wins) so the UI reflects
/// the new status optimistically before sync.
class WamDefectStatusOverride {
  const WamDefectStatusOverride({
    required this.defectId,
    required this.status,
    required this.updatedAt,
  });

  final String defectId;
  final String status;
  final DateTime updatedAt;
}

/// A work order UPDATE captured offline (start / reschedule / complete). Like a
/// defect status change it's a deterministic write on an existing work order id,
/// keyed here by that id (latest wins) so the work board reflects it optimistically.
/// Each distinct action still queues its OWN `wam_workorder_update` op (never
/// deduped), so a start-then-complete pair replays in order server-side.
class WamWorkOrderOverride {
  const WamWorkOrderOverride({
    required this.workOrderId,
    required this.updatedAt,
    this.status,
    this.priority,
    this.completionNotes,
    this.scheduledFor,
    this.dueBy,
  });

  final String workOrderId;
  final DateTime updatedAt;
  final String? status;
  final String? priority;
  final String? completionNotes;
  final String? scheduledFor;
  final String? dueBy;
}

/// An inspection RECORDED offline (Phase 59 mobile). Like [LocalWamDefect] it's a
/// whole new fact replayed through POST /sync (`wam_inspection_record`); [id] is a
/// client-minted UUID v4 that doubles as the outbox op id and the server row's PK,
/// so a re-push is idempotent (the server's EXISTS guard returns the existing row
/// without re-raising the defects a failing checklist item spawns). The per-item
/// responses ride in the op payload, not this summary row.
class LocalWamInspection {
  const LocalWamInspection({
    required this.id,
    required this.assetClass,
    required this.assetId,
    required this.createdAt,
    this.workOrderId,
    this.templateId,
    this.overallResult,
    this.inspectedAt,
    this.notes,
  });

  final String id;
  final String assetClass;
  final String assetId;
  final DateTime createdAt;
  final String? workOrderId;
  final String? templateId;
  final String? overallResult;
  final String? inspectedAt;
  final String? notes;
}

// ---- Merged read views (what the store hands the UI) ------------------------

/// A work order as the crew SEES it: the packed truth ([base]) with any offline
/// [override] folded on top (a start/reschedule/complete not yet synced). The
/// effective getters resolve override-over-base per field, so the board shows the
/// crew's optimistic edit immediately while [pending] tells it the change is still
/// queued for POST /sync. [override] is null for an untouched order.
class WamWorkOrderView {
  const WamWorkOrderView({
    required this.base,
    required this.pending,
    this.override,
  });

  final PackWorkOrder base;
  final WamWorkOrderOverride? override;

  /// True while this order has an un-synced `wam_workorder_update` op queued.
  final bool pending;

  bool get hasLocalEdit => override != null;

  String get id => base.id;
  String get workType => base.workType;
  String? get reference => base.reference;
  String? get title => base.title;
  String? get description => base.description;
  String? get assetClass => base.assetClass;
  String? get assetId => base.assetId;
  String? get defectId => base.defectId;
  String? get assignedTo => base.assignedTo;
  String? get startedAt => base.startedAt;

  String get status => override?.status ?? base.status;
  String? get priority => override?.priority ?? base.priority;
  String? get completionNotes =>
      override?.completionNotes ?? base.completionNotes;
  String? get scheduledFor => override?.scheduledFor ?? base.scheduledFor;
  String? get dueBy => override?.dueBy ?? base.dueBy;
}

/// A defect as the crew SEES it: either a packed defect with an optional offline
/// status override, or a whole defect captured offline ([isLocal]). Unifies the
/// two sources so a list/asset view renders them the same, with [pending] marking
/// a capture (or status change) still queued for POST /sync. Built by the store
/// via [WamDefectView.fromPacked] / [WamDefectView.fromLocal].
class WamDefectView {
  const WamDefectView({
    required this.id,
    required this.assetClass,
    required this.assetId,
    required this.severity,
    required this.status,
    required this.isLocal,
    required this.pending,
    this.defectType,
    this.source,
    this.title,
    this.description,
    this.reportedAt,
    this.notes,
  });

  final String id;
  final String assetClass;
  final String assetId;
  final String severity;
  final String status;

  /// True for a defect captured offline (no packed/server row yet); false for a
  /// packed defect (its status may still carry a local override).
  final bool isLocal;

  /// True while a capture or status change for this defect is queued for sync.
  final bool pending;

  final String? defectType;
  final String? source;
  final String? title;
  final String? description;
  final String? reportedAt;
  final String? notes;

  /// A packed defect with an optional offline status [override] folded on top.
  factory WamDefectView.fromPacked(
    PackDefect d, {
    WamDefectStatusOverride? override,
    required bool pending,
  }) =>
      WamDefectView(
        id: d.id,
        assetClass: d.assetClass,
        assetId: d.assetId,
        severity: d.severity,
        status: override?.status ?? d.status,
        isLocal: false,
        pending: pending,
        defectType: d.defectType,
        source: d.source,
        title: d.title,
        description: d.description,
        reportedAt: d.reportedAt,
        notes: d.notes,
      );

  /// A defect captured offline, with an optional later status [override] folded on.
  factory WamDefectView.fromLocal(
    LocalWamDefect d, {
    WamDefectStatusOverride? override,
    required bool pending,
  }) =>
      WamDefectView(
        id: d.id,
        assetClass: d.assetClass,
        assetId: d.assetId,
        severity: d.severity,
        status: override?.status ?? d.status,
        isLocal: true,
        pending: pending,
        defectType: d.defectType,
        source: d.source,
        title: d.title,
        description: d.description,
        reportedAt: d.reportedAt,
        notes: d.notes,
      );
}

/// One item response the crew fills in while running a checklist, assembled into
/// the `wam_inspection_record` op's `items` array. Matches the Go
/// wamInspectionItemPayload JSON keys exactly (template_item_id/position/prompt/
/// response_type/passed/value_text/value_numeric/fail_severity/notes). [passed] ==
/// false with a [failSeverity] is what raises a defect server-side on sync.
class WamInspectionItemResponse {
  const WamInspectionItemResponse({
    this.templateItemId,
    this.position = 0,
    required this.prompt,
    this.responseType,
    this.passed,
    this.valueText,
    this.valueNumeric,
    this.failSeverity,
    this.notes,
  });

  final String? templateItemId;
  final int position;
  final String prompt;
  final String? responseType;
  final bool? passed;
  final String? valueText;
  final double? valueNumeric;
  final String? failSeverity;
  final String? notes;

  /// The op-payload item shape (omitempty-matched to the Go struct, so an absent
  /// optional is left out rather than sent as null).
  Map<String, dynamic> toPayload() => <String, dynamic>{
        if (templateItemId != null && templateItemId!.isNotEmpty)
          'template_item_id': templateItemId,
        if (position != 0) 'position': position,
        'prompt': prompt,
        if (responseType != null && responseType!.isNotEmpty)
          'response_type': responseType,
        if (passed != null) 'passed': passed,
        if (valueText != null && valueText!.isNotEmpty) 'value_text': valueText,
        if (valueNumeric != null) 'value_numeric': valueNumeric,
        if (failSeverity != null && failSeverity!.isNotEmpty)
          'fail_severity': failSeverity,
        if (notes != null && notes!.isNotEmpty) 'notes': notes,
      };
}
