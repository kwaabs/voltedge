import { useState } from "react"
import { KeyRound, Loader2, MapPin, Pencil, Plus, ShieldAlert, Unlink, UserPlus, X } from "lucide-react"

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

/** "Edit account" form, revealed per user as a dialog. Only the display name
 * is editable; email is the login + SSO-link key and is shown read-only. */
function EditNameRow({ user, onDone }: { user: AdminUser; onDone: () => void }) {
  const [name, setName] = useState(user.display_name)
  const update = useUpdateUser()
  const trimmed = name.trim()
  const canSave = trimmed.length > 0 && trimmed !== user.display_name

  function handleSave() {
    update.mutate(
      { userId: user.id, displayName: trimmed },
      { onSuccess: onDone, onError: (err) => toastError(err, "Could not update the account.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onDone()}
      title="Edit account"
      description={`Update the display name for ${user.email}.`}
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={canSave}
      saving={update.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Display name</label>
        <input
          className={inputClass}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Ama Mensah"
        />
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Email</label>
        <input className={inputClass} value={user.email} disabled />
        <span className="text-muted-foreground text-xs">
          Email is the sign-in and SSO-link identity and can't be changed here.
        </span>
      </div>
    </FormDialog>
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

/** One account card: identity, roles, linked providers, and actions. */
function UserRow({
  user,
  roles,
  isSelf,
  allUsers,
}: {
  user: AdminUser
  roles: AdminRole[]
  isSelf: boolean
  allUsers: AdminUser[]
}) {
  const assignRole = useAssignRole()
  const removeRole = useRemoveRole()
  const setStatus = useSetUserStatus()
  const unlink = useUnlinkIdentity()
  const [addingRole, setAddingRole] = useState("")
  const [showEdit, setShowEdit] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [unlinkTarget, setUnlinkTarget] = useState<string | null>(null)
  const [disableConfirm, setDisableConfirm] = useState(false)

  const disabled = user.status === "disabled"
  const userRoles = user.roles ?? []
  const userProviders = user.providers ?? []
  const assignable = roles.filter((r) => !userRoles.includes(r.key))

  return (
    <Card className={disabled ? "opacity-60" : undefined}>
      <CardHeader className="flex flex-row items-start gap-2 pb-2">
        <div className="min-w-0 flex-1">
          <CardTitle className="flex items-center gap-2 text-sm">
            {user.display_name}
            {user.break_glass && (
              <span className="bg-amber-500/15 text-amber-600 rounded px-1.5 py-0.5 text-xs font-medium dark:text-amber-400">
                break-glass
              </span>
            )}
            {disabled && (
              <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs font-medium">disabled</span>
            )}
          </CardTitle>
          <CardDescription className="truncate">{user.email}</CardDescription>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <Button variant="ghost" size="icon" className="size-7" title="Edit account" onClick={() => setShowEdit(true)}>
            <Pencil className="size-4" />
          </Button>
          <Button variant="ghost" size="icon" className="size-7" title="Set password" onClick={() => setShowPassword((v) => !v)}>
            <KeyRound className="size-4" />
          </Button>
          <Button
            variant="outline"
            size="sm"
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
      </CardHeader>

      <CardContent className="flex flex-col gap-3 pt-0">
        {/* Roles */}
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

        {/* Linked sign-in identities */}
        <div className="flex flex-col gap-1.5">
          <span className={labelClass}>Sign-in identities</span>
          <div className="flex flex-wrap items-center gap-1">
            {userProviders.length === 0 && (
              <span className="text-muted-foreground text-xs">none linked yet</span>
            )}
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
                      onClick={() => setUnlinkTarget(p)}
                    >
                      <Unlink className="size-3" />
                    </button>
                  )}
                </span>
              )
            })}
          </div>
          <span className="text-muted-foreground text-xs">
            SSO identities link automatically on the account's first successful sign-in; they can't be pre-set here,
            only removed.
          </span>
        </div>

        {/* Spatial access: manager edge + territory grants (Phase 58) */}
        <SpatialAccessSection user={user} allUsers={allUsers} />

        {showEdit && <EditNameRow user={user} onDone={() => setShowEdit(false)} />}
        {showPassword && <SetPasswordRow user={user} onDone={() => setShowPassword(false)} />}
      </CardContent>

      <ConfirmDeleteDialog
        open={unlinkTarget !== null}
        title={`Unlink ${PROVIDER_LABEL[unlinkTarget ?? ""] ?? unlinkTarget ?? ""} from ${user.email}?`}
        description="The user won't be able to sign in with this provider until they link it again on a future SSO login. This can't be undone directly."
        confirmLabel="Unlink"
        onOpenChange={(open) => !open && setUnlinkTarget(null)}
        onConfirm={async () => {
          if (!unlinkTarget) return
          await unlink.mutateAsync({ userId: user.id, provider: unlinkTarget })
        }}
      />

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
    </Card>
  )
}

export function AdminUsersPage() {
  const { data, isPending, isError } = useAdminUsers()
  const { data: rolesData } = useAdminRoles()
  const can = usePermission()
  const [creating, setCreating] = useState(false)

  const roles = rolesData?.roles ?? []
  // The signed-in user's own email, so a row for yourself can disable its
  // own "Disable" button. The server enforces this too; this is just the
  // matching UI affordance.
  const selfEmail = useSession().data?.email

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

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading accounts...
          </div>
        ) : isError || !data ? (
          <p className="text-destructive text-sm">Could not load accounts.</p>
        ) : data.users.length === 0 ? (
          <p className="text-muted-foreground text-sm">No accounts yet.</p>
        ) : (
          <div className="flex flex-col gap-2">
            {data.users.map((u) => (
              <UserRow key={u.id} user={u} roles={roles} isSelf={u.email === selfEmail} allUsers={data.users} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
