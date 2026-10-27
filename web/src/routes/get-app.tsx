import { useEffect, useState } from "react"
import { Download, Loader2, Smartphone, Zap } from "lucide-react"
import * as QRCode from "qrcode"

import { useAppRelease } from "@/hooks/use-app-release"

/** Bytes → a short human string (1024-based). */
function formatBytes(bytes: number): string {
  if (!bytes) return ""
  const units = ["B", "KB", "MB", "GB"]
  let value = bytes
  let i = 0
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024
    i++
  }
  return `${value.toFixed(value < 10 && i > 0 ? 1 : 0)} ${units[i]}`
}

/**
 * The public "Get the field app" page -- a lightweight, login-free download
 * landing (not behind the app shell), so a field tech on a fresh phone can scan
 * the QR or tap Download to install the signed APK. Backed by GET /app/latest
 * (the published release manifest) and GET /app/download (302 → the APK).
 */
export function GetAppPage() {
  const release = useAppRelease()
  // Absolute URL so the QR works when scanned from another device, and the tap
  // on-device hits the API's public download redirect.
  const downloadUrl = `${window.location.origin}/api/app/download`
  const [qr, setQr] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    QRCode.toString(downloadUrl, { type: "svg", margin: 0, errorCorrectionLevel: "M" })
      .then((svg) => alive && setQr(svg))
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [downloadUrl])

  return (
    <div className="bg-muted/30 flex min-h-screen items-center justify-center p-6">
      <div className="bg-card w-full max-w-md rounded-xl border p-6 shadow-sm">
        <div className="flex items-center gap-2">
          <span className="bg-primary text-primary-foreground flex size-8 items-center justify-center rounded-md">
            <Zap className="size-4" />
          </span>
          <div className="leading-tight">
            <div className="font-semibold">VoltEdge Field</div>
            <div className="text-muted-foreground text-xs">Offline field tool for Android</div>
          </div>
        </div>

        <div className="mt-5">
          {release.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 py-8 text-sm">
              <Loader2 className="size-4 animate-spin" /> Checking for the latest release…
            </div>
          ) : release.isError || !release.data ? (
            <div className="text-muted-foreground py-8 text-center text-sm">
              No release has been published yet. Check back once a build has been uploaded.
            </div>
          ) : (
            <>
              <div className="flex items-center justify-between">
                <div>
                  <div className="text-sm font-medium">Version {release.data.version}</div>
                  <div className="text-muted-foreground text-xs">
                    {formatBytes(release.data.size_bytes)}
                    {release.data.released_at && ` · ${new Date(release.data.released_at).toLocaleDateString()}`}
                  </div>
                </div>
              </div>

              {release.data.notes && (
                <p className="text-muted-foreground mt-3 text-sm whitespace-pre-line">{release.data.notes}</p>
              )}

              {/* QR for scanning from a phone; the whole card is also tap-to-download. */}
              <div className="mt-5 flex flex-col items-center gap-3">
                {qr && (
                  <div
                    className="size-44 rounded-lg border bg-white p-2 [&>svg]:size-full"
                    dangerouslySetInnerHTML={{ __html: qr }}
                  />
                )}
                <a
                  href={downloadUrl}
                  className="bg-primary text-primary-foreground hover:bg-primary/90 inline-flex w-full items-center justify-center gap-2 rounded-md px-4 py-2.5 text-sm font-medium"
                >
                  <Download className="size-4" /> Download APK
                </a>
              </div>

              <div className="text-muted-foreground mt-5 flex items-start gap-2 rounded-md border p-3 text-xs">
                <Smartphone className="mt-0.5 size-4 shrink-0" />
                <div>
                  <p className="text-foreground font-medium">Installing</p>
                  <p>
                    Open this page on the Android device (or scan the QR), tap Download, then open the file.
                    You may need to allow installing from this browser (“unknown sources”) the first time.
                  </p>
                </div>
              </div>

              {release.data.sha256 && (
                <p className="text-muted-foreground mt-3 font-mono text-[10px] break-all">
                  sha256 {release.data.sha256}
                </p>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
