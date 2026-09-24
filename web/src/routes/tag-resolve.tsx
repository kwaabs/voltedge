import { useEffect, useState } from "react"
import { useNavigate, useParams } from "react-router"
import { Loader2, QrCode, Search, TriangleAlert } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useResolveTag } from "@/hooks/use-asset-tags"
import { ApiError } from "@/lib/api"
import { useSelectionStore } from "@/stores/selection-store"

// The scan / deep-link landing surface (asset-tagging module S4). A scanned QR
// encodes `<origin>/t/<code>` (see tag-label.tsx); this page turns that code
// into a feature and jumps to the map with it selected + focused -- the same
// store-then-navigate handoff the /features "Show on map" button uses. It also
// carries a manual code-entry box, the fallback for a label whose QR won't scan
// but whose printed code a field tech can key in. Resolution runs through the
// scoped, draft-aware backend resolver, so an unknown / retired / out-of-scope
// code lands here as a plain "not found" that never leaks whether the asset
// exists.

const inputClass = "border-input bg-background w-full rounded-md border px-3 py-2 text-sm"

export function TagResolvePage() {
  const { code } = useParams()
  const navigate = useNavigate()
  const resolve = useResolveTag(code)
  const [entry, setEntry] = useState("")

  // On a hit, select the feature (focus: true pans/zooms the map to it even when
  // off-screen or not yet loaded) then replace this interstitial with the map --
  // replace so Back doesn't bounce the user through the resolver again.
  useEffect(() => {
    if (resolve.isSuccess && resolve.data) {
      const { feature_class_code, feature_id } = resolve.data.tag
      useSelectionStore.getState().select(feature_class_code, feature_id, { focus: true })
      navigate("/", { replace: true })
    }
  }, [resolve.isSuccess, resolve.data, navigate])

  const go = () => {
    const c = entry.trim()
    if (c) navigate(`/t/${encodeURIComponent(c)}`)
  }

  const notFound = resolve.error instanceof ApiError && resolve.error.status === 404
  const otherError = resolve.isError && !notFound

  return (
    <div className="mx-auto flex min-h-[60vh] w-full max-w-md flex-col items-center justify-center gap-6 p-6 text-center">
      <div className="bg-primary/10 text-primary flex size-14 items-center justify-center rounded-full">
        <QrCode className="size-7" />
      </div>

      {/* Resolving an incoming code */}
      {code && resolve.isPending && (
        <div className="flex flex-col items-center gap-2">
          <Loader2 className="text-muted-foreground size-5 animate-spin" />
          <p className="text-sm">
            Resolving <span className="font-mono font-medium">{code}</span>…
          </p>
        </div>
      )}

      {code && notFound && (
        <div className="flex flex-col items-center gap-1">
          <p className="font-medium">Tag not found</p>
          <p className="text-muted-foreground text-sm">
            <span className="font-mono">{code}</span> doesn&apos;t match any asset you can view. It may be an unknown
            code, a retired label, or an asset outside your area.
          </p>
        </div>
      )}

      {code && otherError && (
        <div className="text-destructive flex flex-col items-center gap-1">
          <TriangleAlert className="size-5" />
          <p className="text-sm">
            {resolve.error instanceof ApiError ? resolve.error.message : "Could not resolve this tag."}
          </p>
        </div>
      )}

      {/* Manual entry -- always available; the sole content when no code is in the URL */}
      {(!code || notFound || otherError) && (
        <div className="w-full">
          {!code && <p className="text-muted-foreground mb-3 text-sm">Enter an asset tag code to open it on the map.</p>}
          <div className="flex items-center gap-2">
            <input
              autoFocus
              value={entry}
              onChange={(e) => setEntry(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && go()}
              placeholder="e.g. VE-XXXXXXX"
              className={inputClass}
            />
            <Button onClick={go} disabled={entry.trim() === ""}>
              <Search className="size-4" />
              Open
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
