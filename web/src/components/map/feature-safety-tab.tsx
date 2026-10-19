import { useState } from "react"
import { Loader2, Plus, ShieldAlert, Zap } from "lucide-react"

import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import {
  type SafetyGround,
  type SafetyTag,
  type TagType,
  TAG_TYPES,
  useApplyGround,
  useDeviceTags,
  useFeatureGrounds,
  usePlaceTag,
  useRemoveGround,
  useRemoveTag,
} from "@/hooks/use-safety"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

// The map attribute panel's "Safety" tab (api/internal/safety): the active safety
// TAGS and applied GROUNDS on the selected feature, placed/removed inline. A
// blocking tag (danger/out_of_service) bars the device from being operated -- a
// switching step that tries flips nothing and returns the tag's reason. Shown for
// any network feature; self-gated on safety:write for the write controls.

const inputClass = "border-input bg-background w-full rounded-sm border px-2 py-1 text-sm"

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString()
}

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}

const TAG_BADGE: Record<string, string> = {
  danger: "bg-destructive/15 text-destructive",
  out_of_service: "bg-destructive/10 text-destructive",
  caution: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  information: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
}

/** Place a tag on the selected device, inline. */
function NewTagForm({
  deviceClass,
  deviceId,
  onDone,
}: {
  deviceClass: string
  deviceId: string
  onDone: () => void
}) {
  const place = usePlaceTag()
  const [tagType, setTagType] = useState<TagType>("danger")
  const [reason, setReason] = useState("")

  function submit() {
    if (place.isPending) return
    place.mutate(
      { device_class: deviceClass, device_id: deviceId, tag_type: tagType, reason: reason.trim() || undefined },
      { onSuccess: onDone, onError: (err) => toastError(err, "Could not place the tag.") },
    )
  }

  const blocks = TAG_TYPES.find((t) => t.value === tagType)?.blocks

  return (
    <div className="flex flex-col gap-2 rounded-md border p-2">
      <select value={tagType} onChange={(e) => setTagType(e.target.value as TagType)} className={inputClass}>
        {TAG_TYPES.map((t) => (
          <option key={t.value} value={t.value}>
            {t.label}
          </option>
        ))}
      </select>
      <input
        value={reason}
        onChange={(e) => setReason(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && submit()}
        placeholder="Reason (e.g. crew working downstream)"
        className={inputClass}
      />
      {blocks && (
        <p className="text-muted-foreground text-xs">
          This tag <b>bars operation</b> of the device until it is removed.
        </p>
      )}
      <div className="flex items-center gap-2">
        <Button size="sm" onClick={submit} disabled={place.isPending}>
          {place.isPending ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
          Place tag
        </Button>
        <Button variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

function TagRow({ tag, canWrite }: { tag: SafetyTag; canWrite: boolean }) {
  const remove = useRemoveTag()
  return (
    <li className="flex items-start gap-2 px-3 py-2 text-sm">
      <span className={cn("mt-0.5 rounded px-1.5 py-0.5 text-xs font-medium capitalize", TAG_BADGE[tag.tag_type] ?? "bg-muted")}>
        {humanize(tag.tag_type)}
      </span>
      <div className="min-w-0 flex-1">
        {tag.reason && <div className="truncate">{tag.reason}</div>}
        <div className="text-muted-foreground text-xs">
          {tag.blocks_operation ? "Bars operation · " : ""}
          placed {formatWhen(tag.placed_at)}
          {tag.clearance_id ? " · under a clearance" : ""}
        </div>
      </div>
      {canWrite && (
        <Button
          variant="ghost"
          size="sm"
          className="h-6 px-2 text-xs"
          disabled={remove.isPending || !!tag.clearance_id}
          title={tag.clearance_id ? "Bound to a clearance — surrender it to release" : "Remove this tag"}
          onClick={() => remove.mutate({ id: tag.id }, { onError: (err) => toastError(err, "Could not remove the tag.") })}
        >
          {remove.isPending ? <Loader2 className="size-3.5 animate-spin" /> : "Remove"}
        </Button>
      )}
    </li>
  )
}

function GroundRow({ ground, canWrite }: { ground: SafetyGround; canWrite: boolean }) {
  const remove = useRemoveGround()
  return (
    <li className="flex items-start gap-2 px-3 py-2 text-sm">
      <span className="mt-0.5 flex items-center gap-1 rounded bg-green-600/10 px-1.5 py-0.5 text-xs font-medium text-green-700 dark:text-green-400">
        <Zap className="size-3" /> Earth
      </span>
      <div className="min-w-0 flex-1">
        {ground.reason && <div className="truncate">{ground.reason}</div>}
        <div className="text-muted-foreground text-xs">
          applied {formatWhen(ground.applied_at)}
          {ground.clearance_id ? " · under a clearance" : ""}
        </div>
      </div>
      {canWrite && (
        <Button
          variant="ghost"
          size="sm"
          className="h-6 px-2 text-xs"
          disabled={remove.isPending || !!ground.clearance_id}
          title={ground.clearance_id ? "Bound to a clearance — surrender it to release" : "Remove this earth"}
          onClick={() => remove.mutate({ id: ground.id }, { onError: (err) => toastError(err, "Could not remove the earth.") })}
        >
          {remove.isPending ? <Loader2 className="size-3.5 animate-spin" /> : "Remove"}
        </Button>
      )}
    </li>
  )
}

/**
 * The per-feature Safety section: the device's active tags + applied grounds,
 * with inline, write-gated controls to place a tag or apply an earth. Reads the
 * selection from the store (the same shape FaultsTab/OperationsTab use).
 */
export function SafetyTab() {
  const selected = useSelectionStore((s) => s.selected)
  const canWrite = usePermission()("safety:write")
  const [addingTag, setAddingTag] = useState(false)
  const featureClass = selected?.featureClassCode
  const featureId = selected?.featureId
  const applyGround = useApplyGround()
  const tags = useDeviceTags(featureClass, featureId)
  const grounds = useFeatureGrounds(featureClass, featureId)

  if (!selected || !featureClass || !featureId) return null

  const tagRows = tags.data?.tags ?? []
  const groundRows = grounds.data?.grounds ?? []
  const blocked = tagRows.some((t) => t.blocks_operation)

  return (
    <div className="flex flex-col gap-3">
      {blocked && (
        <div className="bg-destructive/10 text-destructive flex items-center gap-1.5 rounded-md px-2 py-1.5 text-xs font-medium">
          <ShieldAlert className="size-3.5" />
          This device is tagged — it cannot be operated until the tag is cleared.
        </div>
      )}

      {/* Tags */}
      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <span className="text-muted-foreground text-xs font-medium">
            {tagRows.length} active tag{tagRows.length === 1 ? "" : "s"}
          </span>
          {canWrite && !addingTag && (
            <Button variant="outline" size="sm" className="h-7" onClick={() => setAddingTag(true)}>
              <Plus className="size-3.5" />
              Place tag
            </Button>
          )}
        </div>
        {addingTag && <NewTagForm deviceClass={featureClass} deviceId={featureId} onDone={() => setAddingTag(false)} />}
        {tags.isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" /> Loading…
          </div>
        ) : tags.isError ? (
          <p className="text-destructive text-sm">Could not load tags.</p>
        ) : tagRows.length === 0 ? (
          <p className="text-muted-foreground text-sm">No safety tags on this device.</p>
        ) : (
          <ul className="flex flex-col divide-y rounded-md border">
            {tagRows.map((t) => (
              <TagRow key={t.id} tag={t} canWrite={canWrite} />
            ))}
          </ul>
        )}
      </div>

      {/* Grounds */}
      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between">
          <span className="text-muted-foreground text-xs font-medium">
            {groundRows.length} applied earth{groundRows.length === 1 ? "" : "s"}
          </span>
          {canWrite && (
            <Button
              variant="outline"
              size="sm"
              className="h-7"
              disabled={applyGround.isPending}
              onClick={() =>
                applyGround.mutate(
                  { feature_class: featureClass, feature_id: featureId },
                  { onError: (err) => toastError(err, "Could not apply the earth.") },
                )
              }
            >
              {applyGround.isPending ? <Loader2 className="size-3.5 animate-spin" /> : <Zap className="size-3.5" />}
              Apply earth
            </Button>
          )}
        </div>
        {groundRows.length > 0 && (
          <ul className="flex flex-col divide-y rounded-md border">
            {groundRows.map((g) => (
              <GroundRow key={g.id} ground={g} canWrite={canWrite} />
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
