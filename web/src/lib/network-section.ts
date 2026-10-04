/**
 * Network sectioning (migration 0129 boundary_role). A "section" is the span of
 * the network between boundary devices; a feature's section is identified by its
 * nearest UPSTREAM boundary device. Because every trace and load-flow result
 * already carries a parent tree (parent_feature_*), the section is derived here
 * on the client -- no engine change, always correct, nothing stored.
 *
 * The kind of section follows the kind of boundary device (its boundary_role):
 * `protective` devices carve a protection zone, `switching` devices a switching
 * section, a `transformer` originates the next-voltage network, a `source` the
 * (sub)network itself.
 */

export interface SectionNode {
  feature_class: string
  feature_id: string
  parent_feature_class?: string | null
  parent_feature_id?: string | null
}

export interface SectionBoundary {
  feature_class: string
  feature_id: string
  role: string
}

const keyOf = (c: string, i: string) => `${c}:${i}`

/** Build a class-code -> boundary_role lookup from the feature-class list. */
export function boundaryRoleMap(
  classes: { code: string; boundary_role?: string }[] | undefined,
): Map<string, string> {
  const m = new Map<string, string>()
  for (const c of classes ?? []) if (c.boundary_role) m.set(c.code, c.boundary_role)
  return m
}

/**
 * Climb the parent tree from a node to its nearest ancestor whose class is a
 * network boundary device. Self-inclusive: a boundary device is the head of its
 * OWN section (so "the section past recloser R3" includes R3 and everything down
 * to the next device). Returns null if no boundary is found up to the root.
 */
export function nearestBoundary(
  node: SectionNode,
  byKey: Map<string, SectionNode>,
  roleByClass: Map<string, string>,
): SectionBoundary | null {
  let cur: SectionNode | undefined = node
  const seen = new Set<string>()
  while (cur) {
    const k = keyOf(cur.feature_class, cur.feature_id)
    if (seen.has(k)) break // defensive: never loop on a malformed parent chain
    seen.add(k)
    const role = roleByClass.get(cur.feature_class)
    if (role) return { feature_class: cur.feature_class, feature_id: cur.feature_id, role }
    if (!cur.parent_feature_class || !cur.parent_feature_id) break
    cur = byKey.get(keyOf(cur.parent_feature_class, cur.parent_feature_id))
  }
  return null
}

/** Index a list of section nodes by their `feature_class:feature_id` key. */
export function indexByKey<T extends SectionNode>(nodes: T[]): Map<string, T> {
  const m = new Map<string, T>()
  for (const n of nodes) m.set(keyOf(n.feature_class, n.feature_id), n)
  return m
}
