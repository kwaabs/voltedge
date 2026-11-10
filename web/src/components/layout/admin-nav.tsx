import {
  Activity,
  Building2,
  CalendarClock,
  ClipboardCheck,
  Clock,
  Cloud,
  Coins,
  Database,
  FileInput,
  Gauge,
  History,
  Inbox,
  KeyRound,
  LandPlot,
  Layers,
  LayoutGrid,
  type LucideIcon,
  MapPin,
  Package,
  QrCode,
  Rows3,
  Shapes,
  Share2,
  ShieldCheck,
  Stamp,
  Tags,
  TriangleAlert,
  Truck,
  UsersRound,
  Webhook,
  Workflow,
  Wrench,
  Zap,
} from "lucide-react"

/** One admin destination. `perm` is the permission the API enforces; the
 * sidebar and the /admin homepage both hide an item the caller can't use, so
 * the nav never offers a dead end (the API stays the real gate). */
export interface AdminNavItem {
  to: string
  label: string
  description: string
  icon: LucideIcon
  perm: string
}

/** A titled cluster of admin destinations, rendered as one section on the
 * /admin homepage. */
export interface AdminNavGroup {
  label: string
  items: AdminNavItem[]
}

/**
 * The admin menu, in one place so the sidebar's single "Admin" entry and the
 * /admin homepage stay in lockstep. Grouped by what each destination governs,
 * not by permission -- a section only renders when the caller can reach at
 * least one item inside it (see filterAdminNav). Previously these lived inline
 * in app-sidebar's `adminItems`; the list had grown long enough that a landing
 * page reads better than fifteen stacked sidebar rows.
 */
export const adminNavGroups: AdminNavGroup[] = [
  {
    label: "Metadata & schema",
    items: [
      {
        to: "/admin/feature-classes",
        label: "Feature classes",
        description: "Author what a feature class is — subtypes, fields, domains.",
        icon: Layers,
        perm: "metadata:write",
      },
      {
        to: "/admin/domains",
        label: "Domains",
        description: "Coded-value lists that constrain feature attributes.",
        icon: Tags,
        perm: "metadata:write",
      },
      {
        to: "/admin/field-groups",
        label: "Field groups",
        description: "Group fields into the sections shown on the attribute panel.",
        icon: Rows3,
        perm: "metadata:write",
      },
      {
        to: "/admin/materials",
        label: "Materials",
        description: "The materials catalog features draw their specs from.",
        icon: Wrench,
        perm: "metadata:write",
      },
      {
        to: "/admin/protection-curves",
        label: "Protection curves",
        description: "Fuse melt/clear and relay TCC curves the coordination engine interpolates.",
        icon: Activity,
        perm: "metadata:write",
      },
      {
        to: "/admin/assemblies",
        label: "Assembly kits",
        description: "Author 'place one anchor, get a pre-wired set' kits — components, wiring and an auto single-line.",
        icon: Package,
        perm: "assemblies:write",
      },
      {
        to: "/admin/cost-catalog",
        label: "Cost catalog",
        description: "Currencies and the unit-rate price book the Bill of Quantities prices against.",
        icon: Coins,
        perm: "metadata:write",
      },
      {
        to: "/admin/crews",
        label: "Crews",
        description: "Field crews the Outage Management board dispatches — name, kind, availability and status.",
        icon: Truck,
        perm: "oms:write",
      },
      {
        to: "/admin/subcontractors",
        label: "Subcontractors",
        description: "External firms the utility tasks field work to — they don't operate in this system; tasking records who is responsible.",
        icon: Building2,
        perm: "wam:write",
      },
      {
        to: "/admin/fault-modes",
        label: "Fault-mode catalog",
        description: "The failure modes that can happen on each asset class — the fault form's class-aware cause picker.",
        icon: TriangleAlert,
        perm: "metadata:write",
      },
      {
        to: "/admin/validation-rules",
        label: "Data-quality rules",
        description: "Fields expected to be populated on each class — the attribute audit flags the gaps in the issue queue.",
        icon: ShieldCheck,
        perm: "metadata:write",
      },
      {
        to: "/admin/inspection-templates",
        label: "Inspection templates",
        description: "Reusable inspection checklists — a failing item with a severity auto-raises a defect when recorded. Run inspections from Works or a feature on the map.",
        icon: ClipboardCheck,
        perm: "metadata:write",
      },
      {
        to: "/admin/asset-life",
        label: "Asset lifecycle",
        description: "Expected service life per class and the aging report — which in-service assets are due or overdue for replacement.",
        icon: CalendarClock,
        perm: "metadata:write",
      },
      {
        to: "/admin/external-systems",
        label: "External systems",
        description: "APIs a feature's live links fetch data from (CIS, billing…).",
        icon: Cloud,
        perm: "metadata:write",
      },
      {
        to: "/admin/webhooks",
        label: "Webhooks",
        description: "Inbound endpoints external systems push feature changes into.",
        icon: Webhook,
        perm: "metadata:write",
      },
      {
        to: "/admin/cim-mappings",
        label: "CIM export mapping",
        description: "Map classes and attributes onto the CIM export profile.",
        icon: Share2,
        perm: "metadata:write",
      },
      {
        to: "/admin/cim-sync-targets",
        label: "CIM sync targets",
        description: "Feed external systems incremental CIM (difference models).",
        icon: Share2,
        perm: "metadata:write",
      },
      {
        to: "/admin/imports",
        label: "Data import",
        description: "Map external source columns onto feature-class fields and import.",
        icon: FileInput,
        perm: "metadata:write",
      },
    ],
  },
  {
    label: "Symbology & map",
    items: [
      {
        to: "/admin/symbology-icons",
        label: "Symbology icons",
        description: "The icon library map symbology rules draw from.",
        icon: Shapes,
        perm: "metadata:write",
      },
      {
        to: "/admin/stencils",
        label: "Stencil promotion",
        description: "Which diagram stencils promote to real map features.",
        icon: Stamp,
        perm: "metadata:write",
      },
      {
        to: "/admin/diagrams",
        label: "Diagram rules",
        description: "Rules that drive one-line diagram placement.",
        icon: Workflow,
        perm: "metadata:write",
      },
      {
        to: "/admin/map-settings",
        label: "Map settings",
        description: "The default map view, basemaps and annotation visibility.",
        icon: MapPin,
        perm: "metadata:write",
      },
    ],
  },
  {
    label: "Access & operations",
    items: [
      {
        to: "/admin/users",
        label: "Users",
        description: "User accounts, roles and spatial territory access.",
        icon: UsersRound,
        perm: "admin:users",
      },
      {
        to: "/admin/roles",
        label: "Roles & permissions",
        description: "Job-family roles (engineering, operations, asset management…) and the permissions each bundles. Clone a system role to tailor it.",
        icon: ShieldCheck,
        perm: "admin:users",
      },
      {
        to: "/admin/logging",
        label: "Logging",
        description: "The API server's log verbosity — raise it to debug an incident, lower it after.",
        icon: Gauge,
        perm: "admin:users",
      },
      {
        to: "/admin/db",
        label: "Database explorer",
        description: "Browse tables, views and functions and run SQL directly — admin / break-glass only, every query audited.",
        icon: Database,
        perm: "admin:db",
      },
      {
        to: "/admin/activity",
        label: "Activity audit",
        description: "Who changed what, and when — the feature change log sliced by person and time.",
        icon: History,
        perm: "admin:users",
      },
      {
        to: "/admin/security-log",
        label: "Sign-in log",
        description: "The security audit trail — sign-ins, failures, SSO, logouts and role changes, with who, when, IP and browser.",
        icon: KeyRound,
        perm: "admin:users",
      },
      {
        to: "/admin/edit-sessions",
        label: "Edit sessions",
        description: "Each edit-mode session start-to-end — who edited, how long, how many changes, which classes. Admins see everyone; a supervisor sees their own and their reports'.",
        icon: Clock,
        // Derived capability: admin / break-glass / supervisor (see use-auth.ts).
        perm: "edit-sessions:view",
      },
      {
        to: "/admin/territories",
        label: "Territories",
        description: "The territory hierarchy and scoping rollout.",
        icon: LandPlot,
        perm: "territories:write",
      },
      {
        to: "/admin/circuits",
        label: "Circuits",
        description: "Circuit definitions features are assigned to.",
        icon: Zap,
        perm: "circuits:write",
      },
      {
        to: "/admin/asset-tags",
        label: "Asset tags",
        description: "Browse, search, print and manage every asset tag's lifecycle.",
        icon: QrCode,
        perm: "tags:read",
      },
      {
        to: "/admin/field-packs",
        label: "Field packs",
        description: "Offline field-pack builds for the field tool.",
        icon: Package,
        perm: "admin:field-packs",
      },
      {
        to: "/admin/sync-review",
        label: "Sync review",
        description: "Review and post edits synced back from the field.",
        icon: Inbox,
        perm: "sync:review",
      },
    ],
  },
]

/** All admin items flattened -- the sidebar uses this to decide whether to
 * show its single "Admin" entry at all (any item reachable ⇒ show it). */
export const adminNavItems: AdminNavItem[] = adminNavGroups.flatMap((g) => g.items)

/** The grid icon stands in for the admin landing page in the sidebar. */
export const AdminNavIcon = LayoutGrid

/** Keep only the groups (and, within them, the items) the caller can reach.
 * `can` is usePermission()'s predicate. */
export function filterAdminNav(can: (perm: string) => boolean): AdminNavGroup[] {
  return adminNavGroups
    .map((group) => ({ ...group, items: group.items.filter((item) => can(item.perm)) }))
    .filter((group) => group.items.length > 0)
}
