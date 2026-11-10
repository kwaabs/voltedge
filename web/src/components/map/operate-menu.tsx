import { useLayoutEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate } from "react-router"
import {
  Bell,
  Info,
  ListChecks,
  LifeBuoy,
  Loader2,
  PlugZap,
  PowerOff,
  ShieldAlert,
  ShieldOff,
  Split,
} from "lucide-react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { usePermission } from "@/hooks/use-auth"
import { useAlarms } from "@/hooks/use-alarms"
import { useOperateDevice } from "@/hooks/use-loop-fix"
import { useCreatePlannedSwitching, useIncidents } from "@/hooks/use-oms"
import { useDeviceTags, useFeatureGrounds, usePlaceTag, useRemoveTag } from "@/hooks/use-safety"
import { useTraceConfigurations } from "@/hooks/use-trace-configurations"
import { apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useDeviceDetailStore } from "@/stores/device-detail-store"
import { useFaultCurrentStore } from "@/stores/fault-current-store"
import { type IsolationResult, useIsolateStore } from "@/stores/isolate-store"
import { useLoadFlowStore } from "@/stores/load-flow-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"
import { useTraceStore } from "@/stores/trace-store"

const MENU_WIDTH = 248

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function errMsg(e: unknown, fallback: string): string {
  return e instanceof Error ? e.message : fallback
}

interface OperateMenuProps {
  featureClass: string
  featureId: string
  /** The device's live state ("OPEN" | "CLOSED") if the class carries one, else
   * null (a non-switch feature). Drives which operate action is a no-op. */
  deviceState: string | null
  /** Cursor position (client coords) the right-click fired at. */
  x: number
  y: number
  /** True when the right-clicked feature is a conductor (a wire), not an operable
   * device: device actions (operate / tag / isolate / switching order) don't apply,
   * so only the Details section (attributes, and grounds if any) is shown. */
  isConductor?: boolean
  onClose: () => void
}

/** Section heading inside the menu. */
function Section({ label }: { label: string }) {
  return (
    <div className="text-muted-foreground px-3 pb-0.5 pt-2 text-[10px] font-medium uppercase tracking-wide">
      {label}
    </div>
  )
}

/** One menu row. */
function Item({
  icon,
  label,
  onClick,
  disabled,
  busy,
  danger,
}: {
  icon: React.ReactNode
  label: string
  onClick: () => void
  disabled?: boolean
  busy?: boolean
  danger?: boolean
}) {
  return (
    <button
      type="button"
      disabled={disabled || busy}
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm",
        disabled || busy ? "text-muted-foreground cursor-not-allowed" : "hover:bg-accent",
        danger && !disabled && "text-destructive",
      )}
    >
      <span className="flex size-4 shrink-0 items-center justify-center">
        {busy ? <Loader2 className="size-4 animate-spin" /> : icon}
      </span>
      <span className="truncate">{label}</span>
    </button>
  )
}

/**
 * The right-click "operate" menu for a device on the operations schematic. Composes
 * the existing operational endpoints -- operate (safety-interlocked), safety tag,
 * isolate study, seeded switching order, FLISR restoration -- into one cursor-
 * positioned menu. Every write confirms first; the server enforces the safety-tag
 * interlock on operate (there is no client bypass). Actions the caller lacks the
 * permission for are hidden.
 */
export function OperateMenu({ featureClass, featureId, deviceState, x, y, isConductor = false, onClose }: OperateMenuProps) {
  const can = usePermission()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const pushSuccess = useToastStore((s) => s.pushSuccess)
  const pushError = useToastStore((s) => s.pushError)

  const canOperate = can("oms:write")
  const canSafety = can("safety:write")
  const canSafetyRead = can("safety:read")
  const canTrace = can("trace:run")
  const canOms = can("oms:read")

  const operate = useOperateDevice()
  const placeTag = usePlaceTag()
  const removeTag = useRemoveTag()
  const createOrder = useCreatePlannedSwitching()
  const { data: tagsData } = useDeviceTags(canSafetyRead ? featureClass : undefined, canSafetyRead ? featureId : undefined)
  const { data: groundsData } = useFeatureGrounds(canSafetyRead ? featureClass : undefined, canSafetyRead ? featureId : undefined)
  const { data: alarmData } = useAlarms({ status: "live" }, canOms)
  const { data: traceConfigs } = useTraceConfigurations()
  const { data: incidentData } = useIncidents({ openOnly: true, enabled: canOms })
  const [isolating, setIsolating] = useState(false)
  // Operating a device is confirmed in a proper dialog (not a native window.confirm).
  const [confirmTarget, setConfirmTarget] = useState<"OPEN" | "CLOSED" | null>(null)

  const label = `${humanize(featureClass)} ${shortId(featureId)}`
  const state = deviceState ? deviceState.toUpperCase() : null
  const isOperable = state === "OPEN" || state === "CLOSED"
  const isolationConfig = traceConfigs?.trace_configurations.find((c) => c.direction === "DOWNSTREAM")
  const blockingTag = (tagsData?.tags ?? []).find((t) => t.blocks_operation && t.status === "active")
  const openIncident = (incidentData?.incidents ?? []).find(
    (i) => i.predicted_device_class === featureClass && i.predicted_device_id === featureId,
  )
  // Context for the "Details" section -- only surface an item when there's something
  // to show for this device.
  const deviceAlarmCount = (alarmData?.alarms ?? []).filter(
    (a) => a.device_class === featureClass && a.device_id === featureId,
  ).length
  const safetyCount = (tagsData?.tags ?? []).length + (groundsData?.grounds ?? []).length
  const seeAttributes = () => {
    useSelectionStore.getState().select(featureClass, featureId)
    onClose()
  }
  const showDetail = (sect: "alarms" | "safety" | "incident") => {
    useDeviceDetailStore.getState().open(featureClass, featureId, sect)
    onClose()
  }

  const runOperate = (target: "OPEN" | "CLOSED") => {
    operate.mutate(
      { featureClass, featureId, state: target },
      {
        onSuccess: () => {
          pushSuccess(`${label} set to ${target}.`)
          qc.invalidateQueries({ queryKey: ["energization"] })
          qc.invalidateQueries({ queryKey: ["operations", "abnormal"] })
          qc.invalidateQueries({ queryKey: ["oms", "incidents"] })
          onClose()
        },
        // The server rejects a blocked operation naming the safety tag -- surface it.
        onError: (e) => {
          pushError(errMsg(e, "Could not operate the device."))
          onClose()
        },
      },
    )
  }

  const doPlaceTag = (tag_type: "danger" | "out_of_service") => {
    placeTag.mutate(
      { device_class: featureClass, device_id: featureId, tag_type },
      {
        onSuccess: () => {
          pushSuccess(`${humanize(tag_type)} tag placed on ${label}.`)
          onClose()
        },
        onError: (e) => {
          pushError(errMsg(e, "Could not place the tag."))
          onClose()
        },
      },
    )
  }

  const doRemoveTag = () => {
    if (!blockingTag) return
    removeTag.mutate(
      { id: blockingTag.id },
      {
        onSuccess: () => {
          pushSuccess(`Tag removed from ${label}.`)
          onClose()
        },
        onError: (e) => {
          pushError(errMsg(e, "Could not remove the tag."))
          onClose()
        },
      },
    )
  }

  const doIsolate = async () => {
    if (!isolationConfig) return
    // Isolate overlays are mutually exclusive with the other analysis overlays, so
    // clear them first (mirrors feature-panel's runIsolate) -- otherwise a stale
    // trace result would out-rank the isolation in roleFor's priority order.
    useTraceStore.getState().clear()
    useLoadFlowStore.getState().clear()
    useFaultCurrentStore.getState().clear()
    useIsolateStore.getState().setLoading(true)
    setIsolating(true)
    try {
      const result = await apiFetch<IsolationResult>("/trace/isolate", {
        method: "POST",
        body: JSON.stringify({
          trace_configuration: isolationConfig.code,
          device: { feature_class: featureClass, feature_id: featureId },
        }),
      })
      useIsolateStore.getState().setResult(result)
    } catch (e) {
      useIsolateStore.getState().clear()
      pushError(errMsg(e, "Could not compute the isolation."))
    } finally {
      setIsolating(false)
      onClose()
    }
  }

  const doNewOrder = () => {
    createOrder.mutate(
      { reference: "", target_device: { feature_class: featureClass, feature_id: featureId } },
      {
        onSuccess: () => {
          pushSuccess(`Switching order drafted for ${label}.`)
          navigate("/switching")
          onClose()
        },
        onError: (e) => {
          pushError(errMsg(e, "Could not create the switching order."))
          onClose()
        },
      },
    )
  }

  const doRestoration = () => {
    if (!openIncident) return
    navigate(`/outages/${openIncident.id}`)
    onClose()
  }

  // Clamp the menu inside the viewport by MEASURING it after render -- the menu height
  // varies with how many sections/items show (operate + safety + study + details), so a
  // fixed estimate cut it off near the bottom. Start at the cursor, then flip up / nudge
  // in once we know the real size. A tall menu scrolls (maxHeight below) rather than
  // spilling off-screen.
  const menuRef = useRef<HTMLDivElement | null>(null)
  const [pos, setPos] = useState<{ left: number; top: number }>(() => ({
    left: Math.min(x, window.innerWidth - MENU_WIDTH - 8),
    top: y,
  }))
  useLayoutEffect(() => {
    const el = menuRef.current
    if (!el) return
    const w = el.offsetWidth
    const h = el.offsetHeight
    const left = Math.max(8, Math.min(x, window.innerWidth - w - 8))
    let top = y
    if (top + h > window.innerHeight - 8) top = Math.max(8, window.innerHeight - h - 8)
    setPos({ left, top })
    // Re-measure whenever the anchor or the content height could change.
  }, [x, y, isConductor, canOperate, canSafety, deviceAlarmCount, safetyCount, openIncident, blockingTag])

  // Device-only sections are hidden for a conductor (a wire isn't operated / tagged /
  // isolated); only the Details section (attributes, grounds) applies.
  const showSafety = canSafety && !isConductor
  const showStudy = !isConductor && ((canTrace && isolationConfig) || canOperate || (canOms && openIncident))

  return (
    <>
      {/* Backdrop: a click (or right-click) anywhere outside closes the menu. */}
      <div
        className="fixed inset-0 z-40"
        onClick={onClose}
        onContextMenu={(e) => {
          e.preventDefault()
          onClose()
        }}
      />
      <div
        ref={menuRef}
        className="bg-popover text-popover-foreground fixed z-50 overflow-y-auto rounded-md border py-1 shadow-md"
        style={{ left: pos.left, top: pos.top, width: MENU_WIDTH, maxHeight: "calc(100vh - 16px)" }}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="truncate px-3 pb-1 pt-1.5 text-xs font-medium">{label}</div>

        {!isConductor && canOperate && (
          <>
            <Section label="Operate" />
            <Item
              icon={<PlugZap className="size-4" />}
              label="Close"
              disabled={!isOperable || state === "CLOSED"}
              busy={operate.isPending}
              onClick={() => setConfirmTarget("CLOSED")}
            />
            <Item
              icon={<PowerOff className="size-4" />}
              label="Open"
              disabled={!isOperable || state === "OPEN"}
              busy={operate.isPending}
              onClick={() => setConfirmTarget("OPEN")}
            />
          </>
        )}

        {showSafety && (
          <>
            <Section label="Safety" />
            {blockingTag ? (
              <Item
                icon={<ShieldOff className="size-4" />}
                label="Remove safety tag"
                busy={removeTag.isPending}
                onClick={doRemoveTag}
              />
            ) : (
              <>
                <Item
                  icon={<ShieldAlert className="size-4" />}
                  label="Place danger tag"
                  danger
                  busy={placeTag.isPending}
                  onClick={() => doPlaceTag("danger")}
                />
                <Item
                  icon={<ShieldAlert className="size-4" />}
                  label="Place out-of-service tag"
                  busy={placeTag.isPending}
                  onClick={() => doPlaceTag("out_of_service")}
                />
              </>
            )}
          </>
        )}

        {showStudy && (
          <>
            <Section label="Study & plan" />
            {canTrace && isolationConfig && (
              <Item
                icon={<Split className="size-4" />}
                label="Isolate — what loses power"
                busy={isolating}
                onClick={doIsolate}
              />
            )}
            {canOms && openIncident && (
              <Item
                icon={<LifeBuoy className="size-4" />}
                label="Suggest restoration"
                onClick={doRestoration}
              />
            )}
            {canOperate && (
              <Item
                icon={<ListChecks className="size-4" />}
                label="New switching order"
                busy={createOrder.isPending}
                onClick={doNewOrder}
              />
            )}
          </>
        )}

        <Section label="Details" />
        <Item icon={<Info className="size-4" />} label="See attributes" onClick={seeAttributes} />
        {canOms && deviceAlarmCount > 0 && (
          <Item
            icon={<Bell className="size-4" />}
            label={`Alarms (${deviceAlarmCount})`}
            onClick={() => showDetail("alarms")}
          />
        )}
        {canSafetyRead && safetyCount > 0 && (
          <Item
            icon={<ShieldAlert className="size-4" />}
            label={`Safety (${safetyCount})`}
            onClick={() => showDetail("safety")}
          />
        )}
        {canOms && openIncident && (
          <Item
            icon={<LifeBuoy className="size-4" />}
            label="On an open incident"
            onClick={() => showDetail("incident")}
          />
        )}
      </div>

      {/* Operate confirmation -- a proper dialog, not a native window.confirm. */}
      <AlertDialog open={confirmTarget != null} onOpenChange={(o) => !o && !operate.isPending && setConfirmTarget(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirmTarget === "OPEN" ? "Open" : "Close"} this device?</AlertDialogTitle>
            <AlertDialogDescription>
              Set <span className="font-medium">{label}</span> to{" "}
              <span className="font-medium">{confirmTarget === "OPEN" ? "OPEN" : "CLOSED"}</span>. This changes live
              network state.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={operate.isPending}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault()
                if (confirmTarget) runOperate(confirmTarget)
              }}
              disabled={operate.isPending}
            >
              {operate.isPending && <Loader2 className="size-4 animate-spin" />}
              {confirmTarget === "OPEN" ? "Open" : "Close"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
