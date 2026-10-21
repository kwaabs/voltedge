import { usePermission } from "@/hooks/use-auth"
import { useEditModeStore } from "@/stores/edit-mode-store"

/**
 * The one predicate editing affordances should gate on: the user both HOLDS the
 * `features:write` permission AND has turned on edit mode (edit-mode-store). View
 * mode (the default each session) hides create menus, feature-panel write actions,
 * and arms no map-edit gestures; flipping the Edit toggle reveals them.
 *
 * Use this in place of a raw `usePermission("features:write")` wherever the check
 * governs *visibility of an edit affordance*. Do NOT use it where the permission
 * check is a genuine capability gate the server also enforces and that isn't about
 * showing/hiding an edit control -- those stay on usePermission so behaviour
 * doesn't hinge on a UI mode.
 */
export function useEditing(): boolean {
  const canWrite = usePermission()("features:write")
  const editMode = useEditModeStore((s) => s.editMode)
  return canWrite && editMode
}

/** Whether the Edit toggle should be offered at all -- i.e. the user could edit
 * if they turned the mode on. The toggle button hides entirely for a read-only
 * user, who has nothing to switch into. */
export function useCanEnterEditMode(): boolean {
  return usePermission()("features:write")
}
