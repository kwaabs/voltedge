import { BookOpen, Loader2, Lock } from "lucide-react"
import { useMemo } from "react"
import { NavLink, useParams } from "react-router"
import ReactMarkdown from "react-markdown"
import remarkGfm from "remark-gfm"

import type { Components } from "react-markdown"

import { MermaidDiagram } from "@/components/docs/mermaid-diagram"
import { ApiError } from "@/lib/api"
import { useDoc, useDocs, type DocSummary } from "@/hooks/use-docs"
import { cn } from "@/lib/utils"

/**
 * Render a fenced ```mermaid block as a diagram instead of source text; every
 * other code block falls through to the default <code>/<pre> the wrapper's
 * prose classes style. `pre` is unwrapped for mermaid so the diagram isn't
 * boxed in the grey code-block chrome (MermaidDiagram supplies its own frame).
 */
const markdownComponents: Components = {
  code({ className, children, ...props }) {
    if (/\blanguage-mermaid\b/.test(className ?? "")) {
      return <MermaidDiagram chart={String(children).replace(/\n$/, "")} />
    }
    return (
      <code className={className} {...props}>
        {children}
      </code>
    )
  },
  pre({ children, ...props }) {
    const child = Array.isArray(children) ? children[0] : children
    const childClass =
      child && typeof child === "object" && "props" in child
        ? ((child as { props?: { className?: string } }).props?.className ?? "")
        : ""
    if (/\blanguage-mermaid\b/.test(childClass)) {
      return <>{children}</>
    }
    return <pre {...props}>{children}</pre>
  },
}

/**
 * The in-app documentation viewer. A left rail lists every manual the server
 * says this user may read (administrative guides are filtered out server-side
 * for a caller without the permission -- see api/internal/docs), grouped by
 * category; the right pane renders the selected manual's Markdown.
 *
 * The permission limit is enforced by the API, not here: an admin manual never
 * appears in the list for an unauthorised user, and a direct GET /docs/{id}
 * for one is refused 403 -- the frontend just reflects that outcome.
 */
export function DocsPage() {
  const { id } = useParams()
  const listQuery = useDocs()
  const docs = listQuery.data ?? []

  // Preserve manifest order within each category (the server sends them in
  // display order); group only splits them into labelled sections.
  const groups = useMemo(() => {
    const byCategory = new Map<string, DocSummary[]>()
    for (const d of docs) {
      const arr = byCategory.get(d.category) ?? []
      arr.push(d)
      byCategory.set(d.category, arr)
    }
    return Array.from(byCategory, ([label, items]) => ({ label, items }))
  }, [docs])

  return (
    <div className="flex h-[calc(100svh-3.5rem)] flex-col">
      <div className="flex min-h-0 flex-1">
        {/* Left rail: the library index. */}
        <nav className="w-72 shrink-0 overflow-y-auto border-r p-3">
          <div className="mb-3 flex items-center gap-2 px-1">
            <BookOpen className="text-primary size-4" />
            <span className="font-semibold tracking-tight">Documentation</span>
          </div>

          {listQuery.isLoading ? (
            <div className="text-muted-foreground flex items-center gap-2 px-1 text-sm">
              <Loader2 className="size-4 animate-spin" /> Loading…
            </div>
          ) : listQuery.isError ? (
            <p className="text-destructive px-1 text-sm">Couldn't load the documentation index.</p>
          ) : docs.length === 0 ? (
            <p className="text-muted-foreground px-1 text-sm">No documents are available.</p>
          ) : (
            <div className="flex flex-col gap-4">
              {groups.map((group) => (
                <div key={group.label} className="flex flex-col gap-1">
                  <div className="text-muted-foreground px-1 text-xs font-medium tracking-wide uppercase">
                    {group.label}
                  </div>
                  {group.items.map((d) => (
                    <NavLink
                      key={d.id}
                      to={`/docs/${d.id}`}
                      className={({ isActive }) =>
                        cn(
                          "flex items-start gap-2 rounded-md px-2 py-1.5 text-sm transition-colors",
                          isActive
                            ? "bg-accent text-accent-foreground font-medium"
                            : "hover:bg-accent/50 text-foreground",
                        )
                      }
                    >
                      <span className="flex-1">{d.title}</span>
                      {d.required_permission && (
                        <Lock className="text-muted-foreground mt-0.5 size-3 shrink-0" aria-label="Restricted" />
                      )}
                    </NavLink>
                  ))}
                </div>
              ))}
            </div>
          )}
        </nav>

        {/* Right pane: the selected manual. */}
        <div className="min-w-0 flex-1 overflow-y-auto">
          <DocContentPane id={id ?? null} docs={docs} />
        </div>
      </div>
    </div>
  )
}

function DocContentPane({ id, docs }: { id: string | null; docs: DocSummary[] }) {
  const docQuery = useDoc(id)

  if (!id) {
    return (
      <div className="mx-auto max-w-2xl p-10 text-center">
        <BookOpen className="text-muted-foreground/60 mx-auto size-10" />
        <h1 className="mt-4 text-lg font-semibold">VoltEdge documentation</h1>
        <p className="text-muted-foreground mt-1 text-sm">
          Pick a manual from the left to start reading. {docs.length > 0 && `${docs.length} available.`}
        </p>
      </div>
    )
  }

  if (docQuery.isLoading) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 p-10 text-sm">
        <Loader2 className="size-4 animate-spin" /> Loading…
      </div>
    )
  }

  if (docQuery.isError) {
    // A 403 here means the manual is administrative and this user lacks the
    // permission -- the server-enforced limit, surfaced plainly.
    const err = docQuery.error
    const message = err instanceof Error ? err.message : ""
    const restricted = err instanceof ApiError && err.status === 403
    return (
      <div className="mx-auto max-w-2xl p-10 text-center">
        <Lock className="text-muted-foreground/60 mx-auto size-8" />
        <h1 className="mt-4 text-base font-semibold">
          {restricted ? "This manual is restricted" : "Couldn't open this manual"}
        </h1>
        <p className="text-muted-foreground mt-1 text-sm">
          {restricted
            ? "You don't have the permission required to read this administrative guide."
            : message || "The document could not be loaded."}
        </p>
      </div>
    )
  }

  const doc = docQuery.data
  if (!doc) return null

  return (
    <article className="mx-auto max-w-5xl px-12 py-10">
      <header className="mb-8 border-b pb-5">
        <div className="text-muted-foreground text-xs font-medium tracking-wide uppercase">{doc.category}</div>
        <h1 className="mt-1.5 text-3xl font-semibold tracking-tight">{doc.title}</h1>
        <p className="text-muted-foreground mt-2 text-base">{doc.description}</p>
      </header>
      <div
        className={cn(
          "text-[15.5px] leading-[1.8]",
          "[&_h1]:mt-10 [&_h1]:mb-4 [&_h1]:text-2xl [&_h1]:font-semibold [&_h1]:tracking-tight",
          "[&_h2]:mt-9 [&_h2]:mb-4 [&_h2]:border-b [&_h2]:pb-2 [&_h2]:text-xl [&_h2]:font-semibold",
          "[&_h3]:mt-7 [&_h3]:mb-3 [&_h3]:text-lg [&_h3]:font-semibold",
          "[&_h4]:mt-6 [&_h4]:mb-2 [&_h4]:text-base [&_h4]:font-semibold",
          "[&_p]:my-4",
          "[&_ul]:my-4 [&_ul]:list-disc [&_ul]:pl-6 [&_ol]:my-4 [&_ol]:list-decimal [&_ol]:pl-6 [&_li]:my-1.5",
          "[&_a]:text-primary [&_a]:underline [&_a]:underline-offset-2",
          "[&_strong]:font-semibold",
          "[&_blockquote]:my-4 [&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-4 [&_blockquote]:text-muted-foreground [&_blockquote]:italic",
          "[&_code]:bg-muted [&_code]:rounded [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[0.85em]",
          "[&_pre]:my-5 [&_pre]:overflow-x-auto [&_pre]:rounded-md [&_pre]:bg-muted [&_pre]:p-4 [&_pre]:text-sm [&_pre_code]:bg-transparent [&_pre_code]:p-0",
          "[&_table]:my-5 [&_table]:w-full [&_table]:border-collapse [&_table]:text-sm",
          "[&_th]:border [&_th]:border-border [&_th]:bg-muted [&_th]:px-3 [&_th]:py-1.5 [&_th]:text-left [&_th]:font-semibold",
          "[&_td]:border [&_td]:border-border [&_td]:px-3 [&_td]:py-1.5 [&_td]:align-top",
          "[&_hr]:my-8 [&_hr]:border-border",
          "[&_img]:my-4 [&_img]:max-w-full [&_img]:rounded-md",
        )}
      >
        <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
          {doc.content}
        </ReactMarkdown>
      </div>
    </article>
  )
}
