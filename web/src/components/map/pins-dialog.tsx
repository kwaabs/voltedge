import { type FormEvent, useEffect, useState } from "react"
import { MapPin, Navigation, Trash2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { cn } from "@/lib/utils"
import { useMapGotoStore } from "@/stores/map-goto-store"
import { useMapPinPlacementStore } from "@/stores/map-pin-placement-store"
import { useMapPinsStore } from "@/stores/map-pins-store"
import { useMapViewStore } from "@/stores/map-view-store"
import { inputClass } from "./field-utils"

/** The centre of the current map viewport as [lng, lat], or null before the
 * map has rendered once (map-view-store.ts's bbox is null until then). Used to
 * pre-fill a new pin's coordinates with wherever the user is currently looking,
 * so "add pin" bookmarks the visible area by default. */
function viewCenter(): { lng: number; lat: number } | null {
  const bbox = useMapViewStore.getState().bbox
  if (!bbox) return null
  const [minX, minY, maxX, maxY] = bbox
  return { lng: (minX + maxX) / 2, lat: (minY + maxY) / 2 }
}

/**
 * The saved-pins (bookmarks) manager -- add a named location and jump back to
 * any saved one. Pins live per-browser (map-pins-store.ts); this dialog is the
 * only place to create/remove them, and "Go" flies the map there via the
 * shared go-to token (map-goto-store.ts). A plain Dialog rather than a
 * dropdown so the text inputs keep focus (Radix menus steal it) and there's
 * room for the list; no Popover primitive exists in this app anyway.
 */
export function PinsDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const pins = useMapPinsStore((s) => s.pins)
  const addPin = useMapPinsStore((s) => s.addPin)
  const removePin = useMapPinsStore((s) => s.removePin)
  const clearPins = useMapPinsStore((s) => s.clearPins)
  const goTo = useMapGotoStore((s) => s.goTo)

  const [name, setName] = useState("")
  const [lat, setLat] = useState("")
  const [lng, setLng] = useState("")

  // Each time the dialog opens, seed the coordinate fields. If the user just
  // dropped a pin on the map (reopening the dialog), use that exact point;
  // otherwise default to the current map centre ("bookmark what I'm looking at").
  // Name is cleared either way so a stale one doesn't linger.
  useEffect(() => {
    if (!open) return
    setName("")
    const picked = useMapPinPlacementStore.getState().picked
    if (picked) {
      setLat(picked.lat.toFixed(5))
      setLng(picked.lng.toFixed(5))
      useMapPinPlacementStore.getState().consume()
      return
    }
    const center = viewCenter()
    setLat(center ? center.lat.toFixed(5) : "")
    setLng(center ? center.lng.toFixed(5) : "")
  }, [open])

  const latNum = Number(lat)
  const lngNum = Number(lng)
  const coordsValid =
    lat.trim() !== "" &&
    lng.trim() !== "" &&
    Number.isFinite(latNum) &&
    Number.isFinite(lngNum) &&
    latNum >= -90 &&
    latNum <= 90 &&
    lngNum >= -180 &&
    lngNum <= 180
  const canAdd = name.trim() !== "" && coordsValid

  function handleAdd(e: FormEvent) {
    e.preventDefault()
    if (!canAdd) return
    addPin({ name: name.trim(), lng: lngNum, lat: latNum })
    setName("")
  }

  function handleGo(pin: { lng: number; lat: number }) {
    goTo({ lng: pin.lng, lat: pin.lat })
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Pins</DialogTitle>
          <DialogDescription>
            Saved locations, kept in this browser. Add the spot you're looking
            at or drop a pin on the map, then jump back to it any time.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={handleAdd} className="flex flex-col gap-2">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Pin name"
            aria-label="Pin name"
            className={inputClass}
          />
          <div className="flex gap-2">
            <input
              value={lat}
              onChange={(e) => setLat(e.target.value)}
              placeholder="Latitude"
              aria-label="Latitude"
              inputMode="decimal"
              className={cn(inputClass, "tabular-nums")}
            />
            <input
              value={lng}
              onChange={(e) => setLng(e.target.value)}
              placeholder="Longitude"
              aria-label="Longitude"
              inputMode="decimal"
              className={cn(inputClass, "tabular-nums")}
            />
          </div>
          <div className="flex items-center justify-between gap-2">
            {/* Drop on map: arm the placement picker and close the dialog; the next
                map click captures the point and reopens this pre-filled. */}
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                useMapPinPlacementStore.getState().arm()
                onOpenChange(false)
              }}
            >
              <MapPin className="size-4" />
              Drop on map
            </Button>
            <Button type="submit" size="sm" disabled={!canAdd}>
              Add pin
            </Button>
          </div>
        </form>

        <div className="border-t pt-3">
          {pins.length === 0 ? (
            <p className="text-muted-foreground text-sm">No pins yet.</p>
          ) : (
            <>
              <div className="mb-2 flex items-center justify-between">
                <span className="text-muted-foreground text-xs">
                  {pins.length} pin{pins.length === 1 ? "" : "s"}
                </span>
                <button
                  type="button"
                  onClick={clearPins}
                  className="text-muted-foreground hover:text-destructive text-xs"
                >
                  Clear all
                </button>
              </div>
              <ul className="flex max-h-64 flex-col gap-1 overflow-y-auto">
                {pins.map((pin) => (
                  <li
                    key={pin.id}
                    className="hover:bg-accent flex items-center gap-2 rounded-md px-2 py-1.5"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="truncate text-sm">{pin.name}</div>
                      <div className="text-muted-foreground text-xs tabular-nums">
                        {pin.lat.toFixed(5)}, {pin.lng.toFixed(5)}
                      </div>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="size-7"
                      aria-label={`Go to ${pin.name}`}
                      onClick={() => handleGo(pin)}
                    >
                      <Navigation className="size-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="text-muted-foreground hover:text-destructive size-7"
                      aria-label={`Delete ${pin.name}`}
                      onClick={() => removePin(pin.id)}
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </li>
                ))}
              </ul>
            </>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
