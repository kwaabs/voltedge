import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2 } from "lucide-react"

import { type BusPick, CouplerBusPicker } from "@/components/map/coupler-bus-picker"
import { FieldInput } from "@/components/map/feature-field-input"
import { formatFieldLabel, inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import type { CreateCouplerResult } from "@/hooks/use-couplers"
import { type FieldSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import type { CreateDuctResult } from "@/hooks/use-ducts"
import type { CreatePanelResult } from "@/hooks/use-panels"
import { ApiError, apiFetch } from "@/lib/api"
import { useAssistantStore } from "@/stores/assistant-store"
import { useDrawStore } from "@/stores/draw-store"
import { useMapRefreshStore } from "@/stores/map-refresh-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useErrorToast, useToastStore } from "@/stores/toast-store"

/**
 * Renders in place of feature-panel.tsx's tabs while a draft exists
 * (see draw-store.ts) -- a not-yet-saved feature has no id, so
 * Terminals/Relationships don't apply yet. Two distinct draft origins,
 * same form either way: the map's own draw flow (`draft.geometry` set,
 * point/line/polygon depending on the feature class) and a diagram's
 * digitizing tool (`draft.diagramPlacement` set instead, no geometry at
 * all -- diagram-canvas.tsx only ever starts one of these for a
 * `geometry_type = 'none'` class). Deliberately one shared form rather
 * than a second bespoke one for the diagram case: the actual attribute
 * inputs, subtype picker, and save/cancel affordances have no reason to
 * differ, and a user filling either one should see the exact same
 * panel they already know from the feature-panel.tsx attribute view.
 */
export function FeatureCreateForm() {
  const draft = useDrawStore((s) => s.draft)
  const draftToken = useDrawStore((s) => s.draftToken)
  const clearDraft = useDrawStore((s) => s.clearDraft)
  const bumpMapRefresh = useMapRefreshStore((s) => s.bump)
  const select = useSelectionStore((s) => s.select)
  const queryClient = useQueryClient()
  const { data: schema, isPending, isError, error } = useFeatureClassSchema(draft?.featureClassCode)
  useErrorToast(isError, error, "Could not load the form")

  const [name, setName] = useState("")
  const [subtypeCode, setSubtypeCode] = useState<number | "">("")
  const [attributes, setAttributes] = useState<Record<string, unknown>>({})
  const [saving, setSaving] = useState(false)
  // The two buses a coupler draft bridges (Phase Bus-coupler Slice 2). Only ever
  // set for a draft.coupler; picked in the form (not on the map) via the two
  // CouplerBusPicker inputs, and sent as bus_a/bus_b on Save.
  const [busA, setBusA] = useState<BusPick | null>(null)
  const [busB, setBusB] = useState<BusPick | null>(null)

  // Fresh form state for every new draft -- a leftover subtype pick or
  // attribute value from a just-canceled or just-saved draft has no
  // business surviving into the next one. Keyed on draftToken (bumped once
  // per new draft), NOT on the draft object: a Duplicate draft dragged on
  // the map re-writes draft.geometry every move, and re-running this then
  // would wipe what's been typed. A seeded draft (Duplicate) pre-fills from
  // its `seed`; a blank draw starts empty. Read live from the store so this
  // needs no dependency on `draft` (whose identity churns on every drag).
  useEffect(() => {
    const seed = useDrawStore.getState().draft?.seed
    setName(seed?.name ?? "")
    setSubtypeCode(seed?.subtype ?? "")
    // "Born PLANNED" (Phase 38): a new feature's operational_state defaults to
    // PLANNED so the lifecycle picker lands there instead of the coded field's
    // blank "—". Under the seed spread so a Duplicate draft's own value still
    // wins; harmless on a class without the field -- handleSave's visible-field
    // filter drops it. Special-cased by name, the same convention circuit_id
    // and circuit_ref follow (feature-field-input.tsx).
    setAttributes({ operational_state: "PLANNED", ...(seed?.attributes ?? {}) })
    // A coupler draft picks its two buses in the form -- reset them per new draft
    // alongside the rest of the form state.
    setBusA(null)
    setBusB(null)
  }, [draftToken])

  if (!draft) return null

  if (isPending) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 text-sm">
        <Loader2 className="size-4 animate-spin" />
        Loading form...
      </div>
    )
  }
  if (isError || !schema) return null

  const visibleFields = schema.fields.filter((f) => f.visible_on_create)
  const isFieldRequired = (field: FieldSchema) =>
    subtypeCode !== "" && (field.required_for_subtypes ?? []).includes(subtypeCode)
  const needsSubtype = schema.subtypes.length > 0
  const missingSubtype = needsSubtype && subtypeCode === ""
  const missingRequiredField = visibleFields.some((f) => {
    if (!isFieldRequired(f)) return false
    const v = attributes[f.name]
    return v === undefined || v === ""
  })
  // A coupler needs both buses picked (and distinct -- CouplerBusPicker already
  // excludes the other pick, but guard anyway) before it can be wired.
  const missingCouplerBuses = !!draft.coupler && (!busA || !busB || busA.featureId === busB.featureId)
  const canSave = !missingSubtype && !missingRequiredField && !missingCouplerBuses && !saving

  async function handleSave() {
    if (!draft || !schema) return
    setSaving(true)
    try {
      // Only send fields the class actually allows at create time. For a
      // blank draw this is exactly what's in `attributes` already (the form
      // only ever wrote visible fields), so it's a no-op; for a seeded
      // Duplicate draft it drops any computed/invisible attributes copied
      // off the source that the create validator (api/internal/crud) would
      // reject outright.
      const createAttributes: Record<string, unknown> = {}
      for (const field of visibleFields) {
        if (field.name in attributes) createAttributes[field.name] = attributes[field.name]
      }

      // A split draft doesn't create a plain feature -- it cuts an existing
      // line in one transaction (api/internal/crud/split.go): the original
      // keeps the near half, and this new_feature becomes the far half, whose
      // geometry PostGIS computes from split_point (so no geometry is sent).
      // Everything else -- the visible-only attribute filter, the subtype and
      // name -- is exactly the create form's, since the far half is a create.
      if (draft.split) {
        const { sourceFeatureId, splitPoint } = draft.split
        const newFeature: Record<string, unknown> = { attributes: createAttributes }
        if (needsSubtype) newFeature.subtype = subtypeCode
        if (name.trim()) newFeature.name = name.trim()
        const created = await apiFetch<{ id: string }>(
          `/features/${draft.featureClassCode}/${sourceFeatureId}/split`,
          { method: "POST", body: JSON.stringify({ split_point: splitPoint, new_feature: newFeature }) },
        )
        bumpMapRefresh()
        // The original was shortened server-side -- drop its cached row so the
        // panel/map re-read the near half rather than the pre-split geometry.
        queryClient.invalidateQueries({ queryKey: ["feature", draft.featureClassCode, sourceFeatureId] })
        select(draft.featureClassCode, created.id)
        // Re-suggest on the new far half only: it's the brand-new, disconnected
        // part that needs re-connecting (the original kept all its connections
        // and relationships, per docs/phase-20-split-copy.md). requestSuggestions
        // replaces the whole set, so firing it for both halves would just race --
        // the new part is the one that matters.
        void useAssistantStore.getState().requestSuggestions({
          featureClassCode: draft.featureClassCode,
          featureId: created.id,
        })
        clearDraft()
        return
      }

      // A panel draft doesn't create one feature -- it fans out a whole
      // switchgear panel (the panel plus its role's standard device set, all
      // containment-wired) in one transaction via /panels (api/internal/panel),
      // then places the returned panel and each device onto the board schematic
      // at the anchor + the device's own template offset. The attribute/subtype/
      // name collection is exactly the create form's; panel_role rode in via the
      // seed, so createAttributes already carries it alongside the other required
      // fields (voltage class, ...) the user filled in.
      if (draft.panel) {
        const { boardFeatureClass, boardFeatureId, diagramId, base } = draft.panel
        const panelBody: Record<string, unknown> = {
          board_feature_class: boardFeatureClass,
          board_feature_id: boardFeatureId,
          panel_feature_class: draft.featureClassCode,
          attributes: createAttributes,
        }
        if (needsSubtype) panelBody.subtype = subtypeCode
        if (name.trim()) panelBody.name = name.trim()
        const result = await apiFetch<CreatePanelResult>("/panels", {
          method: "POST",
          body: JSON.stringify(panelBody),
        })
        // Place the panel column at the anchor, then each device relative to it.
        // These placements are geometry-less classes dropped by (x, y), the same
        // upsert the digitize path uses; rotation rides from the template.
        await apiFetch(`/diagrams/${diagramId}/placements`, {
          method: "POST",
          body: JSON.stringify({
            feature_class: draft.featureClassCode,
            feature_id: result.panel.id,
            x: base.x,
            y: base.y,
            rotation: 0,
          }),
        })
        for (const device of result.devices) {
          await apiFetch(`/diagrams/${diagramId}/placements`, {
            method: "POST",
            body: JSON.stringify({
              feature_class: device.feature_class,
              feature_id: device.feature.id,
              x: base.x + device.offset_x,
              y: base.y + device.offset_y,
              rotation: device.rotation,
            }),
          })
        }
        queryClient.invalidateQueries({ queryKey: ["diagram-placements", diagramId] })
        select(draft.featureClassCode, result.panel.id)
        clearDraft()
        return
      }

      // An RMU draft doesn't create one feature -- it fans out the whole Ring
      // Main Unit (the placed RMU plus its config's ways and the internal bus,
      // all containment- and connectivity-wired) in one transaction via /rmus
      // (api/internal/rmu). Unlike a panel it DOES carry map geometry -- the RMU
      // is a placed unit -- so geometry rides along; the attribute/subtype/name
      // collection is exactly the create form's. rmu_config selects the way
      // template: it rode in via the seed, so it's already in createAttributes if
      // the class shows it on create; fall back to the menu's choice otherwise so
      // the backend always gets it.
      if (draft.rmu) {
        const rmuAttributes: Record<string, unknown> = { ...createAttributes }
        if (!("rmu_config" in rmuAttributes)) rmuAttributes.rmu_config = draft.rmu.config
        const rmuBody: Record<string, unknown> = {
          rmu_feature_class: draft.featureClassCode,
          geometry: draft.geometry,
          attributes: rmuAttributes,
        }
        if (needsSubtype) rmuBody.subtype = subtypeCode
        if (name.trim()) rmuBody.name = name.trim()
        const result = await apiFetch<{ rmu: { id: string } }>("/rmus", {
          method: "POST",
          body: JSON.stringify(rmuBody),
        })
        bumpMapRefresh()
        // Select the placed RMU. No requestSuggestions: the RMU is a container
        // carrying no topology of its own -- the network nodes are its ways and
        // bus, already wired to each other by the fan-out; the external ring
        // cables connect through the normal post-placement connect action.
        select(draft.featureClassCode, result.rmu.id)
        clearDraft()
        return
      }

      // A duct draft doesn't create one feature -- it fans out the whole cable
      // duct (the placed line plus the N DUCT_WAY children its way_count calls for,
      // all containment-wired) in one transaction via /ducts (api/internal/duct).
      // Like an RMU it carries map geometry -- the duct is a placed line -- and
      // reuses the shared create form; way_count rode in as an ordinary attribute
      // the user filled on create, so it's already in createAttributes.
      if (draft.duct) {
        const ductBody: Record<string, unknown> = {
          duct_feature_class: draft.featureClassCode,
          geometry: draft.geometry,
          attributes: createAttributes,
        }
        if (needsSubtype) ductBody.subtype = subtypeCode
        if (name.trim()) ductBody.name = name.trim()
        const result = await apiFetch<CreateDuctResult>("/ducts", {
          method: "POST",
          body: JSON.stringify(ductBody),
        })
        bumpMapRefresh()
        // Select the placed duct. No requestSuggestions: a duct is
        // network_role='none', never on the electrical graph -- its ways are
        // logical children, not traced nodes, so there's nothing to connect.
        select(draft.featureClassCode, result.duct.id)
        clearDraft()
        return
      }

      // A coupler draft doesn't create one feature -- it fans out a bus coupler
      // (the breaker plus the two connections wiring it between the picked buses
      // via busbar rules C + D) in one transaction via /couplers
      // (api/internal/coupler). Like an RMU it carries map geometry -- the breaker
      // is a placed point -- and reuses the shared create form; switch_function
      // rode in via the seed, so it's already in createAttributes. The two bus
      // picks come from the form's own busA/busB state (gated non-null above).
      if (draft.coupler) {
        const couplerBody: Record<string, unknown> = {
          breaker_feature_class: draft.featureClassCode,
          geometry: draft.geometry,
          attributes: createAttributes,
          bus_a: { feature_class: busA!.featureClass, feature_id: busA!.featureId },
          bus_b: { feature_class: busB!.featureClass, feature_id: busB!.featureId },
        }
        if (needsSubtype) couplerBody.subtype = subtypeCode
        if (name.trim()) couplerBody.name = name.trim()
        const result = await apiFetch<CreateCouplerResult>("/couplers", {
          method: "POST",
          body: JSON.stringify(couplerBody),
        })
        bumpMapRefresh()
        // Select the placed breaker. No requestSuggestions: the coupler's two
        // connections are already wired by the fan-out, so there's nothing left to
        // suggest -- the whole point of picking both buses up front.
        select(draft.featureClassCode, result.breaker.id)
        clearDraft()
        return
      }

      const body: Record<string, unknown> = { attributes: createAttributes }
      if (draft.geometry) body.geometry = draft.geometry
      if (needsSubtype) body.subtype = subtypeCode
      if (name.trim()) body.name = name.trim()
      const created = await apiFetch<{ id: string }>(`/features/${draft.featureClassCode}`, {
        method: "POST",
        body: JSON.stringify(body),
      })
      if (draft.diagramPlacement) {
        const { diagramId, x, y, rotation, promoteFromElementId, path } = draft.diagramPlacement
        await apiFetch(`/diagrams/${diagramId}/placements`, {
          method: "POST",
          body: JSON.stringify({
            feature_class: draft.featureClassCode,
            feature_id: created.id,
            x,
            y,
            rotation: rotation ?? 0,
            // A line-shaped class (diagram_geometry_type = 'line', e.g. a
            // busbar) carries the polyline it was drawn with; a point drops
            // with no path (migration 0053).
            ...(path ? { path } : {}),
          }),
        })
        // Stencil->feature promotion (Phase 25): the just-placed feature is
        // taking the spot of a decoration symbol. Now that the real placement
        // exists, delete that symbol element so the glyph is swapped for the
        // feature rather than doubled up. Elements are their own query key, so
        // invalidate them too for the canvas to drop the removed glyph.
        if (promoteFromElementId) {
          await apiFetch(`/diagrams/${diagramId}/elements/${promoteFromElementId}`, { method: "DELETE" })
          queryClient.invalidateQueries({ queryKey: ["diagram-elements", diagramId] })
        }
        queryClient.invalidateQueries({ queryKey: ["diagram-placements", diagramId] })
      } else {
        bumpMapRefresh()
      }
      select(draft.featureClassCode, created.id)
      // Fire-and-forget: a just-drawn feature with real geometry (e.g. an LV
      // conductor snapped to a pole) is exactly the moment the assistant should
      // surface the connections/relationships it could now establish -- the same
      // trigger feature-edit-form.tsx runs after a geometry edit, but for the
      // create path, which is how a feature drawn on the map first lands.
      // Skipped for a diagram-only draft: it has no map geometry to rank against.
      if (draft.geometry) {
        void useAssistantStore.getState().requestSuggestions({
          featureClassCode: draft.featureClassCode,
          featureId: created.id,
        })
      }
      clearDraft()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not save this feature.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-col gap-1">
        <label className="text-xs font-medium">Name</label>
        <input
          type="text"
          className={inputClass}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Optional -- a human-readable label"
        />
      </div>

      {needsSubtype && (
        <div className="flex flex-col gap-1">
          <label className="text-xs font-medium">
            Subtype <span className="text-destructive">*</span>
          </label>
          <select
            className={inputClass}
            value={subtypeCode}
            onChange={(e) => setSubtypeCode(e.target.value === "" ? "" : Number(e.target.value))}
          >
            <option value="">Select…</option>
            {schema.subtypes.map((st) => (
              <option key={st.code} value={st.code}>
                {st.name}
              </option>
            ))}
          </select>
        </div>
      )}

      {draft.coupler && (
        // A coupler bridges two buses -- pick both (from the option's eligible
        // classes) before Save. These map to POST /couplers' bus_a / bus_b; each
        // picker excludes the other's pick so a coupler can't wire a bus to itself.
        <>
          <CouplerBusPicker
            label="Bus A"
            busFeatureClasses={draft.coupler.busFeatureClasses}
            value={busA}
            onChange={setBusA}
            excludeId={busB?.featureId}
          />
          <CouplerBusPicker
            label="Bus B"
            busFeatureClasses={draft.coupler.busFeatureClasses}
            value={busB}
            onChange={setBusB}
            excludeId={busA?.featureId}
          />
        </>
      )}

      {visibleFields.map((field) => (
        <div key={field.name} className="flex flex-col gap-1">
          <label className="text-xs font-medium">
            {formatFieldLabel(field.name)}
            {isFieldRequired(field) && <span className="text-destructive"> *</span>}
          </label>
          <FieldInput
            field={field}
            value={attributes[field.name]}
            onChange={(value) => setAttributes((a) => ({ ...a, [field.name]: value }))}
            network={schema.network}
          />
        </div>
      ))}

      <div className="flex justify-end gap-2 pt-1">
        <Button variant="outline" size="sm" onClick={clearDraft} disabled={saving}>
          Cancel
        </Button>
        <Button size="sm" onClick={handleSave} disabled={!canSave}>
          {saving && <Loader2 className="size-4 animate-spin" />}
          Save
        </Button>
      </div>
    </div>
  )
}
