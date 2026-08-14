import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2, Plus } from "lucide-react"
import { Link } from "react-router"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { useAdminFeatureClasses } from "@/hooks/use-admin"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

/**
 * The admin catalog view -- every feature class regardless of status
 * (draft/active/deprecated), unlike the map/layers panel's own
 * useFeatureClasses hook (active only). "Compile" is offered inline for
 * a draft class -- the exact same api/internal/compiler.Compile the CLI
 * has always called, just reachable without a terminal now.
 */
export function AdminFeatureClassesPage() {
  const { data, isPending, isError } = useAdminFeatureClasses()
  const queryClient = useQueryClient()
  const [compiling, setCompiling] = useState<string | null>(null)

  async function handleCompile(code: string) {
    setCompiling(code)
    try {
      await apiFetch(`/admin/feature-classes/${code}/compile`, { method: "POST" })
      queryClient.invalidateQueries({ queryKey: ["admin", "feature-classes"] })
      queryClient.invalidateQueries({ queryKey: ["feature-classes"] })
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not compile this class.")
    } finally {
      setCompiling(null)
    }
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-5xl flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Feature classes</h1>
            <p className="text-muted-foreground text-sm">
              Every feature class defined so far, draft or compiled -- create a new one end to end (subtypes,
              fields, terminals) without writing SQL.
            </p>
          </div>
          <Button size="sm" asChild>
            <Link to="/admin/feature-classes/new">
              <Plus className="size-4" />
              New feature class
            </Link>
          </Button>
        </div>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading feature classes...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load feature classes.</p>
        ) : (
          <div className="overflow-hidden rounded-lg border">
            <table className="w-full text-sm">
              <thead className="bg-muted/50 text-muted-foreground text-left text-xs">
                <tr>
                  <th className="px-3 py-2 font-medium">Code</th>
                  <th className="px-3 py-2 font-medium">Name</th>
                  <th className="px-3 py-2 font-medium">Geometry</th>
                  <th className="px-3 py-2 font-medium">Network role</th>
                  <th className="px-3 py-2 font-medium">Status</th>
                  <th className="px-3 py-2 font-medium">Subtypes</th>
                  <th className="px-3 py-2 font-medium">Fields</th>
                  <th className="px-3 py-2 font-medium"></th>
                </tr>
              </thead>
              <tbody>
                {data.feature_classes.map((fc) => (
                  <tr key={fc.code} className="border-t">
                    <td className="px-3 py-2 font-mono text-xs">{fc.code}</td>
                    <td className="px-3 py-2">
                      {fc.name}
                      {fc.is_diagram_container && (
                        <Badge variant="outline" className="ml-2">
                          diagram container
                        </Badge>
                      )}
                    </td>
                    <td className="px-3 py-2">{fc.geometry_type}</td>
                    <td className="px-3 py-2">{fc.network_role}</td>
                    <td className="px-3 py-2">
                      <Badge variant={fc.status === "active" ? "success" : fc.status === "draft" ? "secondary" : "outline"}>
                        {fc.status}
                      </Badge>
                    </td>
                    <td className="px-3 py-2">{fc.subtype_count}</td>
                    <td className="px-3 py-2">{fc.field_count}</td>
                    <td className="px-3 py-2 text-right">
                      {fc.status === "draft" && (
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => handleCompile(fc.code)}
                          disabled={compiling === fc.code}
                        >
                          {compiling === fc.code && <Loader2 className="size-4 animate-spin" />}
                          Compile
                        </Button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}
