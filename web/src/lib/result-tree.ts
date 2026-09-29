/**
 * Generic spanning-tree flattener for analysis result panels.
 *
 * Every engine that returns a per-node result with a single parent pointer (the
 * feature one hop closer to the walk's start) can be read either as a flat table
 * or as the tree the walk actually produced. This module turns a flat list of
 * `{ key, parentKey }` rows into a depth-ordered, collapse-aware line list with
 * the rail/connector metadata a tree renderer needs -- the same shape the trace
 * result panel grew inline first (see trace-result-table.tsx's buildTreeLines);
 * pulled out here so the load-flow family (and later the rest of the analyses)
 * share one tested implementation instead of each hand-rolling the walk.
 *
 * It is deliberately data-shape-agnostic: the caller maps its own node type into
 * `TreeItem<T>` (a stable key, an optional in-set parent key, and the row data),
 * and gets back `TreeLine<T>` carrying that same data plus the layout. Rendering
 * -- which cells, which columns, the connector SVG -- stays in the component.
 */

/** One row to place in the tree: a stable key, the key of the parent that feeds
 * it (null for a root, or when the parent fell outside the result set), and the
 * caller's own row payload carried through untouched. */
export interface TreeItem<T> {
  key: string
  parentKey: string | null
  data: T
}

/** One line of the flattened tree render: the row's data plus its depth
 * (indent), whether it has children (gets a chevron), the size of its whole
 * subtree (collapse-independent), whether it's the root, and the connector-rail
 * metadata. Mirrors trace-result-table.tsx's own TreeLine so a renderer can be
 * shared. */
export interface TreeLine<T> {
  key: string
  data: T
  depth: number
  hasChildren: boolean
  descendantCount: number
  isRoot: boolean
  /** Ancestor rail columns for the connector guides, one per ancestor level
   * above this node's own connector (length depth-1; empty for the root and its
   * direct children). true = that ancestor still has siblings below, so a
   * vertical "│" rail is drawn through this row at that column; false = blank. */
  railColumns: boolean[]
  /** Whether this node is the last of its parent's children -- picks the elbow
   * "└" (last) vs. tee "├" (more siblings follow) connector. Meaningless for the
   * root (rendered without a connector). */
  isLastChild: boolean
}

/**
 * Builds the depth-ordered, collapse-aware line list from each item's parent
 * pointer. Root is the item whose key === rootKey; any item whose parent isn't
 * itself in the set (an orphan, or a scope-clipped parent) is reparented under
 * the root so nothing is dropped -- or, when the root itself isn't present,
 * becomes a top-level root of its own so the result reads as a forest.
 * Self-references and cycles are guarded by a visited set (a node is emitted at
 * most once). Collapsed keys hide their subtree but stay visible themselves.
 */
export function buildTreeLines<T>(items: TreeItem<T>[], rootKey: string, collapsed: Set<string>): TreeLine<T>[] {
  const byKey = new Map<string, TreeItem<T>>()
  for (const it of items) byKey.set(it.key, it)
  const rootExists = byKey.has(rootKey)

  const childrenByKey = new Map<string, TreeItem<T>[]>()
  const roots: TreeItem<T>[] = []
  for (const it of items) {
    if (it.key === rootKey) {
      roots.push(it)
      continue
    }
    const pk = it.parentKey
    const attachTo = pk && pk !== it.key && byKey.has(pk) ? pk : rootExists ? rootKey : null
    if (attachTo === null) {
      roots.push(it)
      continue
    }
    const list = childrenByKey.get(attachTo)
    if (list) list.push(it)
    else childrenByKey.set(attachTo, [it])
  }

  // Subtree size (every node below a node), collapse-independent -- shown on
  // collapsible rows so a collapsed branch still tells you how much it hides.
  // childrenByKey is a forest (each row pushed under exactly one parent), so
  // this can't cycle; the in-progress guard is belt-and-braces.
  const descCount = new Map<string, number>()
  const inProgress = new Set<string>()
  const countDescendants = (k: string): number => {
    const memo = descCount.get(k)
    if (memo !== undefined) return memo
    if (inProgress.has(k)) return 0
    inProgress.add(k)
    const kids = childrenByKey.get(k) ?? []
    let total = kids.length
    for (const c of kids) total += countDescendants(c.key)
    inProgress.delete(k)
    descCount.set(k, total)
    return total
  }

  const lines: TreeLine<T>[] = []
  const visited = new Set<string>()
  const walk = (it: TreeItem<T>, depth: number, railColumns: boolean[], isLastChild: boolean) => {
    if (visited.has(it.key)) return
    visited.add(it.key)
    const kids = childrenByKey.get(it.key) ?? []
    lines.push({
      key: it.key,
      data: it.data,
      depth,
      hasChildren: kids.length > 0,
      descendantCount: countDescendants(it.key),
      isRoot: it.key === rootKey,
      railColumns,
      isLastChild,
    })
    if (kids.length > 0 && !collapsed.has(it.key)) {
      const childRail = depth === 0 ? [] : [...railColumns, !isLastChild]
      kids.forEach((c, i) => walk(c, depth + 1, childRail, i === kids.length - 1))
    }
  }
  for (const root of roots) walk(root, 0, [], true)
  // Safety net: anything not reached (a malformed cycle) still gets listed.
  for (const it of items) if (!visited.has(it.key)) walk(it, 0, [], true)
  return lines
}

/**
 * Keys on the path from `key` up to the tree root, read straight off the
 * rendered (depth-ordered) line list: an ancestor of a visible row is always
 * visible and always precedes it, so walking backwards and picking up each
 * next-shallower depth collects the whole chain to the root. Includes `key`
 * itself. Empty when key is null or isn't currently shown. Powers the hover
 * highlight -- pointing at any node lights the route back to the source.
 */
export function ancestorPathKeys<T>(lines: TreeLine<T>[], key: string | null): Set<string> {
  const path = new Set<string>()
  if (!key) return path
  const idx = lines.findIndex((l) => l.key === key)
  if (idx < 0) return path
  path.add(key)
  let needDepth = lines[idx].depth - 1
  for (let i = idx - 1; i >= 0 && needDepth >= 0; i--) {
    if (lines[i].depth === needDepth) {
      path.add(lines[i].key)
      needDepth -= 1
    }
  }
  return path
}
