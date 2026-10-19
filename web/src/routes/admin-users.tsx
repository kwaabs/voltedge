import { useMemo, useState } from "react"
import { KeyRound, Loader2, MapPin, Plus, Search, Settings2, ShieldAlert, Unlink, UserPlus, X } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { usePermission, useSession } from "@/hooks/use-auth"
import { useTerritories } from "@/hooks/use-territories"
import {
  type AdminRole,
  type AdminUser,
  useAdminRoles,
  useAdminUsers,
  useAssignRole,
  useCreateUser,
  useGrantTerritory,
  useRemoveRole,
  useRevokeTerritory,
  useSetManager,
  useSetUserPassword,
  useSetUserStatus,
  useToggleLocalAuth,
  useUnlinkIdentity,
  useUpdateUser,
} from "@/hooks/use-users"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

/** Human labels for the identity providers a row can carry. */
const PROVIDER_LABEL: Record<string, string> = {
  local: "Password",
  azuread: "Microsoft",
  google: "Google",
}

/**
 * The global local-auth switch (api/internal/auth setLocalAuthHandler),
 * gated by admin:auth-settings -- distinct from the admin:users this page
 * otherwise needs, so it's only rendered for callers who hold it. This is
 * the break-glass runbook lever: off in normal operation, flipped on during
 * an SSO outage so ordinary local accounts can work, then off again.
 */
function LocalAuthSwitchCard({ enabled }: { enabled: boolean }) {
  const toggle = useToggleLocalAuth()
  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex items-center gap-2 text-sm">
          <ShieldAlert className="text-muted-foreground size-4" />
          Local password sign-in
        </CardTitle>
        <CardDescription>
          Break-glass admins can always sign in with a password. This switch controls whether ordinary local accounts
          can too -- keep it off unless SSO is unavailable.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex items-center justify-between">
        <span className={`text-sm font-medium ${enabled ? "text-foreground" : "text-muted-foreground"}`}>
          Currently {enabled ? "enabled for everyone" : "restricted to break-glass"}
        </span>
        <Button
          variant={enabled ? "outline" : "default"}
          size="sm"
          disabled={toggle.isPending}
          onClick={() =>
            toggle.mutate(!enabled, {
              onError: (err) => toastError(err, "Could not change the switch."),
            })
          }
        >
          {toggle.isPending && <Loader2 className="size-4 animate-spin" />}
          {enabled ? "Restrict to break-glass" : "Enable for everyone"}
        </Button>
      </CardContent>
    </Card>
  )
}

/** The create-account form (POST /auth/admin/users), as a modal dialog. */
function CreateUserDialog({
  open,
  onOpenChange,
  roles,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  roles: AdminRole[]
}) {
  const [email, setEmail] = useState("")
  const [name, setName] = useState("")
  const [role, setRole] = useState("")
  const [breakGlass, setBreakGlass] = useState(false)
  const [password, setPassword] = useState("")
  const create = useCreateUser()

  // A break-glass account must carry a password; an ordinary account may be
  // SSO-only (no password) and link its provider on first login.
  const passwordRequired = breakGlass
  const canSave =
    email.trim().length > 0 &&
    name.trim().length > 0 &&
    (!passwordRequired || password.length >= 12) &&
    !create.isPending

  // Clear the fields on close so reopening the dialog is always a fresh
  // form rather than showing the last attempt's input.
  function close() {
    setEmail("")
    setName("")
    setRole("")
    setBreakGlass(false)
    setPassword("")
    onOpenChange(false)
  }

  function handleSave() {
    create.mutate(
      {
        email: email.trim(),
        display_name: name.trim(),
        break_glass: breakGlass,
        role: role || undefined,
        password: password || undefined,
      },
      {
        onSuccess: close,
        onError: (err) => toastError(err, "Could not create this account."),
      },
    )
  }

  return (
    <Dialog
      open={open}
      // Guard against dismissing (Esc / overlay / X) mid-request, matching
      // the confirm dialogs -- an in-flight create shouldn't be abandoned
      // with the outcome unknown.
      onOpenChange={(next) => {
        if (!next && !create.isPending) close()
      }}
    >
      <DialogContent
        showClose={!create.isPending}
        // This form holds unsaved input, so a stray click on the backdrop
        // (or an Escape press) shouldn't discard it -- the dialog only
        // closes via the explicit Close, Cancel, or a successful create.
        onInteractOutside={(e) => e.preventDefault()}
        onEscapeKeyDown={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>New account</DialogTitle>
          <DialogDescription>
            Accounts are always pre-provisioned here -- SSO never creates one. An unknown SSO sign-in is rejected until
            a matching account exists.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1">
              <label className={labelClass}>
                Email <span className="text-destructive">*</span>
              </label>
              <input
                className={inputClass}
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="person@yourutility.com"
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className={labelClass}>
                Display name <span className="text-destructive">*</span>
              </label>
              <input
                className={inputClass}
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Jane Doe"
              />
            </div>
          </div>

          <div className="flex flex-col gap-1">
            <label className={labelClass}>Initial role</label>
            <select className={inputClass} value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="">no role (authorized for nothing yet)</option>
              {roles.map((r) => (
                <option key={r.key} value={r.key}>
                  {r.name} ({r.key})
                </option>
              ))}
            </select>
          </div>

          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={breakGlass} onChange={(e) => setBreakGlass(e.target.checked)} />
            Break-glass account (bypasses the local-auth switch)
          </label>

          <div className="flex flex-col gap-1">
            <label className={labelClass}>
              Local password {passwordRequired ? <span className="text-destructive">*</span> : "(optional)"}
            </label>
            <input
              className={inputClass}
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={passwordRequired ? "at least 12 characters" : "leave blank for an SSO-only account"}
            />
            {passwordRequired && password.length > 0 && password.length < 12 && (
              <span className="text-destructive text-xs">Must be at least 12 characters.</span>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" size="sm" onClick={close} disabled={create.isPending}>
            Cancel
          </Button>
          <Button size="sm" onClick={handleSave} disabled={!canSave}>
            {create.isPending && <Loader2 className="size-4 animate-spin" />}
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/** "Set a new password" form, revealed per user as a dialog. */
function SetPasswordRow({ user, onDone }: { user: AdminUser; onDone: () => void }) {
  const [password, setPassword] = useState("")
  const setPw = useSetUserPassword()
  const canSave = password.length >= 12

  function handleSave() {
    setPw.mutate(
      { userId: user.id, password },
      { onSuccess: onDone, onError: (err) => toastError(err, "Could not set the password.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title="Set password"
      description={`A new password for ${user.email}.`}
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={canSave}
      saving={setPw.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>New password</label>
        <input
          className={inputClass}
          type="password"
          autoComplete="new-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          placeholder="at least 12 characters"
        />
      </div>
    </FormDialog>
  )
}

/** Inline display-name editor inside the Manage dialog (email is the login +
 * SSO-link key, shown read-only). Saves on its own button; the rest of the
 * dialog's sections mutate live. */
function DisplayNameEditor({ user }: { user: AdminUser }) {
  const [name, setName] = useState(user.display_name)
  const update = useUpdateUser()
  const trimmed = name.trim()
  const canSave = trimmed.length > 0 && trimmed !== user.display_name && !update.isPending

  return (
    <div className="flex flex-col gap-1.5">
      <span className={labelClass}>Display name</span>
      <div className="flex items-center gap-2">
        <input
          className={`${inputClass} max-w-xs`}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Ama Mensah"
        />
        <Button
          size="sm"
          variant="outline"
          disabled={!canSave}
          onClick={() =>
            update.mutate(
              { userId: user.id, displayName: trimmed },
              { onError: (err) => toastError(err, "Could not update the account.") },
            )
          }
        >
          {update.isPending && <Loader2 className="size-4 animate-spin" />}
          Save
        </Button>
      </div>
      <span className="text-muted-foreground text-xs">
        Email ({user.email}) is the sign-in and SSO-link identity and can't be changed.
      </span>
    </div>
  )
}

/** Roles editor: the assigned-role chips (removable) + an add-role dropdown. */
function RolesEditor({ user, roles }: { user: AdminUser; roles: AdminRole[] }) {
  const assignRole = useAssignRole()
  const removeRole = useRemoveRole()
  const [addingRole, setAddingRole] = useState("")
  const userRoles = user.roles ?? []
  const assignable = roles.filter((r) => !userRoles.includes(r.key))

  return (
    <div className="flex flex-col gap-1.5">
      <span className={labelClass}>Roles</span>
      <div className="flex flex-wrap items-center gap-1">
        {userRoles.length === 0 && (
          <span className="text-muted-foreground text-xs">none -- this account is authorized for nothing</span>
        )}
        {userRoles.map((r) => (
          <span
            key={r}
            className="bg-muted text-muted-foreground group flex items-center gap-1 rounded-md py-0.5 pr-0.5 pl-1.5 text-xs"
          >
            {r}
            <button
              type="button"
              className="hover:text-destructive rounded p-0.5"
              title="Revoke role"
              onClick={() =>
                removeRole.mutate(
                  { userId: user.id, role: r },
                  { onError: (err) => toastError(err, "Could not revoke the role.") },
                )
              }
            >
              <X className="size-3" />
            </button>
          </span>
        ))}
        {assignable.length > 0 && (
          <div className="flex items-center gap-1">
            <select
              className={`${inputClass} h-7 py-0`}
              value={addingRole}
              onChange={(e) => setAddingRole(e.target.value)}
            >
              <option value="">add role...</option>
              {assignable.map((r) => (
                <option key={r.key} value={r.key}>
                  {r.name}
                </option>
              ))}
            </select>
            <Button
              variant="ghost"
              size="icon"
              className="size-7"
              disabled={!addingRole || assignRole.isPending}
              onClick={() =>
                assignRole.mutate(
                  { userId: user.id, role: addingRole },
                  {
                    onSuccess: () => setAddingRole(""),
                    onError: (err) => toastError(err, "Could not assign the role."),
                  },
                )
              }
            >
              <Plus className="size-4" />
            </Button>
          </div>
        )}
      </div>
    </div>
  )
}

/** Sign-in identities: the linked providers, with unlink for the SSO ones. */
function IdentitiesEditor({ user, onUnlink }: { user: AdminUser; onUnlink: (provider: string) => void }) {
  const userProviders = user.providers ?? []
  return (
    <div className="flex flex-col gap-1.5">
      <span className={labelClass}>Sign-in identities</span>
      <div className="flex flex-wrap items-center gap-1">
        {userProviders.length === 0 && <span className="text-muted-foreground text-xs">none linked yet</span>}
        {userProviders.map((p) => {
          const isSSO = p === "azuread" || p === "google"
          return (
            <span
              key={p}
              className="bg-muted text-muted-foreground flex items-center gap-1 rounded-md py-0.5 pr-0.5 pl-1.5 text-xs"
            >
              {PROVIDER_LABEL[p] ?? p}
              {isSSO && (
                <button
                  type="button"
                  className="hover:text-destructive rounded p-0.5"
                  title="Unlink this SSO identity"
                  onClick={() => onUnlink(p)}
                >
                  <Unlink className="size-3" />
                </button>
              )}
            </span>
          )
        })}
      </div>
      <span className="text-muted-foreground text-xs">
        SSO identities link automatically on the account's first successful sign-in; they can't be pre-set here, only
        removed.
      </span>
    </div>
  )
}

/**
 * Phase 58 spatial access: the user's manager (the org-tree edge access
 * cascades UP) and their direct territory grants (each cascades DOWN to its
 * whole subtree). Enforcement is gated on the territory_enforcement_enabled
 * flag, so on a fresh install these controls are informational until an admin
 * flips it on -- the copy says as much.
 */
function SpatialAccessSection({ user, allUsers }: { user: AdminUser; allUsers: AdminUser[] }) {
  const { data: territoriesData } = useTerritories()
  const grantTerritory = useGrantTerritory()
  const revokeTerritory = useRevokeTerritory()
  const setManager = useSetManager()
  const [addingTerritory, setAddingTerritory] = useState("")

  const territories = territoriesData?.territories ?? []
  const grantedIds = new Set(user.territories.map((t) => t.territory_id))
  // A user can't manage themselves, and the picker offers every other account;
  // the server also rejects a cycle, this just trims the obvious self case.
  const managerOptions = allUsers.filter((u) => u.id !== user.id)
  // Territories not already granted, ordered top-first (level_rank) then name
  // so the picker reads like the tree.
  const assignable = territories
    .filter((t) => !grantedIds.has(t.id))
    .sort((a, b) => a.level_rank - b.level_rank || a.name.localeCompare(b.name))

  return (
    <div className="flex flex-col gap-3">
      {/* Manager (org-tree edge) */}
      <div className="flex flex-col gap-1.5">
        <span className={labelClass}>Manager</span>
        <div className="flex items-center gap-2">
          <select
            className={`${inputClass} h-7 max-w-xs py-0`}
            value={user.manager_id ?? ""}
            disabled={setManager.isPending}
            onChange={(e) =>
              setManager.mutate(
                { userId: user.id, managerId: e.target.value || null },
                { onError: (err) => toastError(err, "Could not set the manager.") },
              )
            }
          >
            <option value="">no manager (top of the org)</option>
            {managerOptions.map((u) => (
              <option key={u.id} value={u.id}>
                {u.display_name} ({u.email})
              </option>
            ))}
          </select>
          {setManager.isPending && <Loader2 className="text-muted-foreground size-4 animate-spin" />}
        </div>
        <span className="text-muted-foreground text-xs">
          A manager inherits every territory granted to their reports, down the whole org tree.
        </span>
      </div>

      {/* Territory grants */}
      <div className="flex flex-col gap-1.5">
        <span className={labelClass}>Territories</span>
        <div className="flex flex-wrap items-center gap-1">
          {user.territories.length === 0 && (
            <span className="text-muted-foreground text-xs">none granted -- this account can see no features when scoping is on</span>
          )}
          {user.territories.map((t) => (
            <span
              key={t.territory_id}
              className="bg-muted text-muted-foreground group flex items-center gap-1 rounded-md py-0.5 pr-0.5 pl-1.5 text-xs"
              title={`${t.level_code}: ${t.territory_name}`}
            >
              <MapPin className="size-3" />
              {t.territory_name}
              <button
                type="button"
                className="hover:text-destructive rounded p-0.5"
                title="Revoke this territory"
                onClick={() =>
                  revokeTerritory.mutate(
                    { userId: user.id, territoryId: t.territory_id },
                    { onError: (err) => toastError(err, "Could not revoke the territory.") },
                  )
                }
              >
                <X className="size-3" />
              </button>
            </span>
          ))}
          {assignable.length > 0 && (
            <div className="flex items-center gap-1">
              <select
                className={`${inputClass} h-7 py-0`}
                value={addingTerritory}
                onChange={(e) => setAddingTerritory(e.target.value)}
              >
                <option value="">grant territory...</option>
                {assignable.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name} ({t.level_code})
                  </option>
                ))}
              </select>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                disabled={!addingTerritory || grantTerritory.isPending}
                onClick={() =>
                  grantTerritory.mutate(
                    { userId: user.id, territoryId: addingTerritory },
                    {
                      onSuccess: () => setAddingTerritory(""),
                      onError: (err) => toastError(err, "Could not grant the territory."),
                    },
                  )
                }
              >
                <Plus className="size-4" />
              </Button>
            </div>
          )}
        </div>
        <span className="text-muted-foreground text-xs">
          A granted territory covers its whole subtree. Access takes effect immediately.
        </span>
      </div>
    </div>
  )
}

/**
 * The full per-account editor, opened from a row's Manage action. Everything
 * that doesn't fit a compact table row lives here: display name, roles, linked
 * sign-in identities, manager, and territory grants. The sections mutate live
 * (each hook invalidates the user list), so there's no dialog-level Save --
 * Close just dismisses.
 */
function ManageUserDialog({
  user,
  roles,
  allUsers,
  onClose,
}: {
  user: AdminUser
  roles: AdminRole[]
  allUsers: AdminUser[]
  onClose: () => void
}) {
  const unlink = useUnlinkIdentity()
  const [unlinkTarget, setUnlinkTarget] = useState<string | null>(null)

  return (
    <Dialog open onOpenChange={(next) => next || onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            {user.display_name}
            {user.break_glass && (
              <span className="bg-amber-500/15 rounded px-1.5 py-0.5 text-xs font-medium text-amber-600 dark:text-amber-400">
                break-glass
              </span>
            )}
            {user.status === "disabled" && (
              <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs font-medium">disabled</span>
            )}
          </DialogTitle>
          <DialogDescription>{user.email}</DialogDescription>
        </DialogHeader>

        <div className="flex max-h-[65vh] flex-col gap-4 overflow-y-auto pr-1">
          <DisplayNameEditor user={user} />
          <RolesEditor user={user} roles={roles} />
          <IdentitiesEditor user={user} onUnlink={setUnlinkTarget} />
          <SpatialAccessSection user={user} allUsers={allUsers} />
        </div>

        <DialogFooter>
          <Button variant="outline" size="sm" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>

      <ConfirmDeleteDialog
        open={unlinkTarget !== null}
        title={`Unlink ${PROVIDER_LABEL[unlinkTarget ?? ""] ?? unlinkTarget ?? ""} from ${user.email}?`}
        description="The user won't be able to sign in with this provider until they link it again on a future SSO login. This can't be undone directly."
        confirmLabel="Unlink"
        onOpenChange={(open) => !open && setUnlinkTarget(null)}
        onConfirm={async () => {
          if (!unlinkTarget) return
          await unlink.mutateAsync({ userId: user.id, provider: unlinkTarget })
          setUnlinkTarget(null)
        }}
      />
    </Dialog>
  )
}

/** One compact table row: identity + a role/territory summary + quick actions
 * (Manage, Set password, Disable/Enable). The heavy editing is in the Manage
 * dialog; the row carries only its own Disable confirm. */
function UserTableRow({
  user,
  isSelf,
  managerName,
  onManage,
  onPassword,
}: {
  user: AdminUser
  isSelf: boolean
  managerName: string | null
  onManage: () => void
  onPassword: () => void
}) {
  const setStatus = useSetUserStatus()
  const [disableConfirm, setDisableConfirm] = useState(false)

  const disabled = user.status === "disabled"
  const userRoles = user.roles ?? []
  const shownRoles = userRoles.slice(0, 3)
  const extraRoles = userRoles.length - shownRoles.length

  return (
    <tr className={cn("border-t align-middle", disabled && "opacity-55")}>
      {/* Identity */}
      <td className="px-3 py-2">
        <div className="flex items-center gap-2">
          <span className="font-medium">{user.display_name}</span>
          {user.break_glass && (
            <span className="bg-amber-500/15 rounded px-1 py-0.5 text-[10px] font-medium text-amber-600 dark:text-amber-400">
              break-glass
            </span>
          )}
          {disabled && (
            <span className="bg-muted text-muted-foreground rounded px-1 py-0.5 text-[10px] font-medium">disabled</span>
          )}
        </div>
        <div className="text-muted-foreground truncate text-xs">{user.email}</div>
      </td>

      {/* Roles (read-only summary; edit in Manage) */}
      <td className="px-3 py-2">
        {userRoles.length === 0 ? (
          <span className="text-muted-foreground text-xs">—</span>
        ) : (
          <div className="flex flex-wrap gap-1">
            {shownRoles.map((r) => (
              <span key={r} className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">
                {r}
              </span>
            ))}
            {extraRoles > 0 && <span className="text-muted-foreground text-xs">+{extraRoles}</span>}
          </div>
        )}
      </td>

      {/* Manager */}
      <td className="text-muted-foreground px-3 py-2 text-xs">{managerName ?? "—"}</td>

      {/* Territory count */}
      <td className="px-3 py-2 text-xs">
        {user.territories.length > 0 ? (
          <span className="inline-flex items-center gap-1">
            <MapPin className="text-muted-foreground size-3" />
            {user.territories.length}
          </span>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </td>

      {/* Actions */}
      <td className="px-3 py-2">
        <div className="flex items-center justify-end gap-1">
          <Button variant="ghost" size="icon" className="size-7" title="Manage account" onClick={onManage}>
            <Settings2 className="size-4" />
          </Button>
          <Button variant="ghost" size="icon" className="size-7" title="Set password" onClick={onPassword}>
            <KeyRound className="size-4" />
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="h-7"
            disabled={setStatus.isPending || (isSelf && !disabled)}
            title={isSelf && !disabled ? "You can't disable your own account" : undefined}
            onClick={() => {
              if (disabled) {
                setStatus.mutate(
                  { userId: user.id, status: "active" },
                  { onError: (err) => toastError(err, "Could not enable the account.") },
                )
              } else {
                setDisableConfirm(true)
              }
            }}
          >
            {disabled ? "Enable" : "Disable"}
          </Button>
        </div>
      </td>

      <ConfirmDeleteDialog
        open={disableConfirm}
        title={`Disable ${user.email}?`}
        description="Their live sessions are revoked immediately and they can't sign in until re-enabled. Their roles and identities are kept."
        confirmLabel="Disable"
        onOpenChange={(open) => !open && setDisableConfirm(false)}
        onConfirm={async () => {
          await setStatus.mutateAsync({ userId: user.id, status: "disabled" })
        }}
      />
    </tr>
  )
}

type StatusFilter = "all" | "active" | "disabled" | "break-glass"

export function AdminUsersPage() {
  const { data, isPending, isError } = useAdminUsers()
  const { data: rolesData } = useAdminRoles()
  const can = usePermission()
  const [creating, setCreating] = useState(false)
  const [manageId, setManageId] = useState<string | null>(null)
  const [passwordId, setPasswordId] = useState<string | null>(null)

  // Filters: free-text (name/email), a role, and a status facet -- a 2000-row
  // account list is unmanageable without narrowing it first.
  const [query, setQuery] = useState("")
  const [roleFilter, setRoleFilter] = useState("")
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all")

  const roles = rolesData?.roles ?? []
  const users = useMemo(() => data?.users ?? [], [data])
  // The signed-in user's own email, so a row for yourself can disable its own
  // "Disable" button. The server enforces this too; this is the matching UI.
  const selfEmail = useSession().data?.email
  // id -> display name, to show a user's manager by name in the table.
  const nameById = useMemo(() => new Map(users.map((u) => [u.id, u.display_name])), [users])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return users.filter((u) => {
      if (q && !`${u.display_name} ${u.email}`.toLowerCase().includes(q)) return false
      if (roleFilter && !(u.roles ?? []).includes(roleFilter)) return false
      if (statusFilter === "active" && u.status !== "active") return false
      if (statusFilter === "disabled" && u.status !== "disabled") return false
      if (statusFilter === "break-glass" && !u.break_glass) return false
      return true
    })
  }, [users, query, roleFilter, statusFilter])

  const manageUser = manageId ? users.find((u) => u.id === manageId) ?? null : null
  const passwordUser = passwordId ? users.find((u) => u.id === passwordId) ?? null : null

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Users</h1>
            <p className="text-muted-foreground text-sm">
              Pre-provision accounts, assign roles, manage sign-in identities, and disable access.
            </p>
          </div>
          <Button size="sm" onClick={() => setCreating(true)}>
            <UserPlus className="size-4" />
            New account
          </Button>
        </div>

        {can("admin:auth-settings") && data && <LocalAuthSwitchCard enabled={data.local_auth_enabled} />}

        <CreateUserDialog open={creating} onOpenChange={setCreating} roles={roles} />
        {manageUser && (
          <ManageUserDialog user={manageUser} roles={roles} allUsers={users} onClose={() => setManageId(null)} />
        )}
        {passwordUser && <SetPasswordRow user={passwordUser} onDone={() => setPasswordId(null)} />}

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading accounts...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load accounts.</p>
        ) : users.length === 0 ? (
          <p className="text-muted-foreground text-sm">No accounts yet.</p>
        ) : (
          <>
            {/* Filter bar: one row, the search twice as wide as each select,
                the count trailing. inputClass carries w-full, so each control
                fills its own flex column rather than the whole row. */}
            <div className="flex items-center gap-2">
              <div className="relative flex-[2]">
                <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2" />
                <input
                  className={`${inputClass} h-8 pl-8`}
                  placeholder="Search name or email…"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                />
              </div>
              <select
                className={`${inputClass} h-8 flex-1`}
                value={roleFilter}
                onChange={(e) => setRoleFilter(e.target.value)}
              >
                <option value="">All roles</option>
                {roles.map((r) => (
                  <option key={r.key} value={r.key}>
                    {r.name}
                  </option>
                ))}
              </select>
              <select
                className={`${inputClass} h-8 flex-1`}
                value={statusFilter}
                onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
              >
                <option value="all">All statuses</option>
                <option value="active">Active</option>
                <option value="disabled">Disabled</option>
                <option value="break-glass">Break-glass</option>
              </select>
              <span className="text-muted-foreground shrink-0 text-xs tabular-nums">
                {filtered.length} of {users.length}
              </span>
            </div>

            {filtered.length === 0 ? (
              <p className="text-muted-foreground text-sm">No accounts match these filters.</p>
            ) : (
              <div className="overflow-hidden rounded-lg border">
                <table className="w-full text-sm">
                  <thead className="bg-muted/50 text-muted-foreground text-left text-xs">
                    <tr>
                      <th className="px-3 py-2 font-medium">Account</th>
                      <th className="px-3 py-2 font-medium">Roles</th>
                      <th className="px-3 py-2 font-medium">Manager</th>
                      <th className="px-3 py-2 font-medium">Territories</th>
                      <th className="px-3 py-2 font-medium text-right">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {filtered.map((u) => (
                      <UserTableRow
                        key={u.id}
                        user={u}
                        isSelf={u.email === selfEmail}
                        managerName={u.manager_id ? nameById.get(u.manager_id) ?? null : null}
                        onManage={() => setManageId(u.id)}
                        onPassword={() => setPasswordId(u.id)}
                      />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}
