import type { ReactNode } from "react"
import { Activity, CheckCircle2, Loader2, Radio, ShieldAlert } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { type ReconItem, useApplyTelemetry, useTelemetryReconciliation } from "@/hooks/use-telemetry"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

function fmtAge(s: number): string {
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  return `${Math.floor(m / 60)}h ${m % 60}m`
}

function Tile({
  icon,
  label,
  value,
  tone = "default",
}: {
  icon: ReactNode
  label: string
  value: number
  tone?: "default" | "ok" | "warn" | "alert"
}) {
  const chip =
    tone === "alert"
      ? "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300"
      : tone === "warn"
        ? "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300"
        : tone === "ok"
          ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300"
          : "bg-muted text-muted-foreground"
  return (
    <div className="bg-card flex items-center gap-3 rounded-lg border p-3">
      <div className={cn("flex size-9 shrink-0 items-center justify-center rounded-lg", chip)}>{icon}</div>
      <div className="min-w-0">
        <div className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">{label}</div>
        <div className="text-xl leading-tight font-semibold tabular-nums">{value.toLocaleString()}</div>
      </div>
    </div>
  )
}

/** Live state: device telemetry reconciled against the model. Shows where the
 * field and the model disagree (or a device has gone stale) and lets an operator
 * apply the field's reading to correct the model. Gated at the route by
 * telemetry:read; Apply needs oms:write. */
export function LiveStatePage() {
  const recon = useTelemetryReconciliation()
  const apply = useApplyTelemetry()
  const canApply = usePermission()("oms:write")
  const d = recon.data

  function doApply(it: ReconItem) {
    apply.mutate(
      { featureClass: it.feature_class_code, featureId: it.feature_id },
      {
        onSuccess: () => useToastStore.getState().pushSuccess(`Model set to ${it.telemetry_state} from telemetry.`),
        onError: (e) =>
          useToastStore.getState().pushError(e instanceof ApiError ? e.message : "Could not apply telemetry."),
      },
    )
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-4xl flex-col gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold">
            <Radio className="size-5" />
            Live state
          </h1>
          <p className="text-muted-foreground text-sm">
            Device telemetry reconciled against the model — where the field and the model disagree, or a device has gone
            quiet. Apply the field's reading to correct the model.
          </p>
        </div>

        {recon.isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Reconciling…
          </div>
        ) : recon.isError || !d ? (
          <p className="text-destructive text-sm">Could not load the reconciliation.</p>
        ) : (
          <>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
              <Tile icon={<Radio className="size-5" />} label="Telemetered" value={d.total} />
              <Tile icon={<CheckCircle2 className="size-5" />} label="Agreeing" value={d.agreeing} tone="ok" />
              <Tile
                icon={<ShieldAlert className="size-5" />}
                label="Disagreeing"
                value={d.disagreeing}
                tone={d.disagreeing > 0 ? "alert" : "default"}
              />
              <Tile
                icon={<Activity className="size-5" />}
                label="Stale"
                value={d.stale}
                tone={d.stale > 0 ? "warn" : "default"}
              />
            </div>

            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm">Needs attention</CardTitle>
                <CardDescription>
                  {d.items.length === 0
                    ? "Everything telemetered agrees with the model and is fresh."
                    : `${d.items.length} device${d.items.length === 1 ? "" : "s"} disagree or stale (quiet > ${d.stale_seconds}s)`}
                </CardDescription>
              </CardHeader>
              {d.items.length > 0 && (
                <CardContent className="pt-0">
                  <div className="overflow-hidden rounded-md border">
                    <table className="w-full text-xs">
                      <thead className="bg-muted/50 text-muted-foreground">
                        <tr className="[&>th]:px-2 [&>th]:py-1.5 [&>th]:text-left [&>th]:font-medium">
                          <th>Device</th>
                          <th>Model</th>
                          <th>Field</th>
                          <th>Status</th>
                          <th>Age</th>
                          <th></th>
                        </tr>
                      </thead>
                      <tbody>
                        {d.items.map((it) => {
                          const disagree = !it.agree && it.model_state != null && it.telemetry_state != null
                          return (
                            <tr
                              key={`${it.feature_class_code}:${it.feature_id}`}
                              className="border-border/60 border-t [&>td]:px-2 [&>td]:py-1.5"
                            >
                              <td>
                                <span className="font-medium">{it.feature_class_code}</span>{" "}
                                <span className="text-muted-foreground font-mono">{it.feature_id.slice(0, 8)}</span>
                              </td>
                              <td className="tabular-nums">{it.model_state ?? "—"}</td>
                              <td className="tabular-nums">{it.telemetry_state ?? "—"}</td>
                              <td>
                                {disagree && (
                                  <span className="rounded bg-red-100 px-1.5 py-0.5 text-[11px] font-medium text-red-700 dark:bg-red-950 dark:text-red-300">
                                    disagree
                                  </span>
                                )}
                                {it.stale && (
                                  <span className="ml-1 rounded bg-amber-100 px-1.5 py-0.5 text-[11px] font-medium text-amber-700 dark:bg-amber-950 dark:text-amber-300">
                                    stale
                                  </span>
                                )}
                              </td>
                              <td className="text-muted-foreground tabular-nums">{fmtAge(it.age_seconds)}</td>
                              <td className="text-right">
                                {canApply && disagree && it.telemetry_state && (
                                  <Button
                                    variant="outline"
                                    size="sm"
                                    disabled={apply.isPending}
                                    onClick={() => doApply(it)}
                                    title={`Set the model device_state to ${it.telemetry_state}`}
                                  >
                                    Apply field
                                  </Button>
                                )}
                              </td>
                            </tr>
                          )
                        })}
                      </tbody>
                    </table>
                  </div>
                  <p className="text-muted-foreground mt-2 text-[11px]">
                    "Apply field" writes the telemetry state onto the model (logged as a telemetry-sourced operation). A
                    stale row has no apply — the device is quiet, not disagreeing.
                  </p>
                </CardContent>
              )}
            </Card>
          </>
        )}
      </div>
    </div>
  )
}
