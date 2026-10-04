import { useEffect, useState } from "react"
import { Check, Gauge, Loader2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { type LogLevel, useLogSettings, useSetLogLevel } from "@/hooks/use-log-settings"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

/** Human copy for each level, in increasing quietness. Kept in the UI (the API
 * returns only the option codes) so the control can explain the trade-off. */
const LEVEL_COPY: Record<LogLevel, { label: string; blurb: string }> = {
  debug: {
    label: "Debug",
    blurb: "Everything, including verbose internals. Loud — turn on briefly to chase a problem, then lower it.",
  },
  info: {
    label: "Info",
    blurb: "The default. One summary line per request plus worker lifecycle — the standing production level.",
  },
  warn: {
    label: "Warn",
    blurb: "Problems only: client errors (4xx) and above. Request lines for ordinary 2xx/3xx traffic stop.",
  },
  error: {
    label: "Error",
    blurb: "Quietest. Server errors (5xx) only. There is no 'off' — the audit trail is never fully silenced.",
  },
}

const ORDER: LogLevel[] = ["debug", "info", "warn", "error"]

export function AdminLoggingPage() {
  const settings = useLogSettings()
  const setLevel = useSetLogLevel()

  // The pending selection, seeded from the active level once it loads. null until
  // then so we don't flash a wrong default.
  const [selected, setSelected] = useState<LogLevel | null>(null)
  useEffect(() => {
    if (settings.data) setSelected(settings.data.level)
  }, [settings.data])

  const active = settings.data?.level
  const options = settings.data?.options ?? ORDER
  const dirty = selected != null && selected !== active

  function apply() {
    if (!selected) return
    setLevel.mutate(selected, {
      onSuccess: () =>
        useToastStore.getState().pushSuccess(`Log level set to ${LEVEL_COPY[selected].label}.`),
      onError: (err) =>
        useToastStore
          .getState()
          .pushError(err instanceof ApiError ? err.message : "Could not set the log level."),
    })
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full max-w-2xl flex-col gap-4">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold">
            <Gauge className="size-5" />
            Logging
          </h1>
          <p className="text-muted-foreground text-sm">
            The API server's structured-logging verbosity. Changes take effect immediately — no restart —
            and are remembered across restarts. This governs the <strong>API server</strong>; the background
            workers follow their own <code>LOG_LEVEL</code> environment setting.
          </p>
        </div>

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Minimum level</CardTitle>
            <CardDescription>
              Only messages at or above the chosen level are emitted. Lower = more detail.
            </CardDescription>
          </CardHeader>
          <CardContent className="pt-0">
            {settings.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" />
                Loading…
              </div>
            ) : settings.isError ? (
              <p className="text-destructive text-sm">Could not load the current log level.</p>
            ) : (
              <div className="flex flex-col gap-1.5">
                {options.map((lvl) => {
                  const copy = LEVEL_COPY[lvl]
                  const isSelected = selected === lvl
                  const isActive = active === lvl
                  return (
                    <button
                      key={lvl}
                      type="button"
                      onClick={() => setSelected(lvl)}
                      className={cn(
                        "flex items-start gap-3 rounded-md border p-2.5 text-left transition-colors",
                        isSelected
                          ? "border-primary bg-primary/5"
                          : "hover:bg-muted/50 border-transparent",
                      )}
                    >
                      <span
                        className={cn(
                          "mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border",
                          isSelected ? "border-primary bg-primary text-primary-foreground" : "border-muted-foreground/40",
                        )}
                      >
                        {isSelected && <Check className="size-3" />}
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex items-center gap-2 text-sm font-medium">
                          {copy.label}
                          {isActive && (
                            <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 text-xs text-emerald-600 dark:text-emerald-400">
                              active
                            </span>
                          )}
                        </span>
                        <span className="text-muted-foreground block text-xs">{copy.blurb}</span>
                      </span>
                    </button>
                  )
                })}

                <div className="mt-2 flex items-center justify-end">
                  <Button size="sm" onClick={apply} disabled={!dirty || setLevel.isPending}>
                    {setLevel.isPending && <Loader2 className="size-3.5 animate-spin" />}
                    Apply
                  </Button>
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  )
}
