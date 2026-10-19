# Operational Safety — Tags, Grounds & Clearances

**Written for:** control-room operators and safety officers who protect a crew before work starts, field crews who apply earths on the ground, and engineers who want to know exactly how the interlock refuses an operation.

Before a crew works on a de-energised section, the operator applies **safety protections** that make it impossible to re-energise the work area by mistake — and VoltEdge now enforces that. This is the Oracle-NMS-style **safety document** layer: **tags** on devices, **grounds** (applied earths) on the isolated section, and **clearances** (permits-to-work) that bind them and hand the area to a crew. The payoff is the **interlock**: a switching step that would operate a tagged device is **refused**.

```
  isolate + earth (switching order)  ─►  ISSUE CLEARANCE  ─►  crew works  ─►  SURRENDER
   (open devices · apply earths)          (danger-tags the             (releases the tags
                                           opened devices,              & grounds; devices
                                           grounds the earths)          operable again)
                                                  │
                                      a BLOCKING tag ⇒ the device
                                      cannot be operated (the gate)
```

Two layers, as elsewhere:
- **▶ Operator / crew** — the device panel's **Safety** tab (place a tag, apply an earth, remove them) and the switching panel's **Issue clearance**.
- **∑ Engineer** — the three records, the gate in the switching-step executor, the endpoints, and the honest limits.

**Legend.** 🔒 a permission. 💡 a tip. ⚠️ an honest limitation. **▶** operate lane; **∑** model lane.

> **Grounding note.** Drawn from the shipped code: schema `migrations/0151_create_safety.sql` (+ `0152` permissions); engine `api/internal/safety` (`store.go`, `models.go`, `http.go`); the interlock in `api/internal/oms/switching.go` (`applyStepDeviceState` → `safety.OperationBlocked`); the switching bridge `api/internal/oms/clearance.go` (`IssueClearanceFromOrder`); UI in `web/src/components/map/feature-safety-tab.tsx` + `web/src/hooks/use-safety.ts`. Safety records are **not** compiled `gis.*` features; they live in the `safety` schema with bare device/feature refs (no FK), the same convention OMS/faults/ops use.

---

## 1. Why it exists

VoltEdge could already **isolate** a section (an OMS switching order opens the device and earths the boundary) and **flip `device_state`** when a step is ticked. But nothing stopped a dispatcher from re-closing a device a crew is working behind. The safety layer is that missing **interlock**: once a device carries a blocking tag, the switching path refuses to operate it — the model enforces the same discipline a paper permit does on the ground.

---

## 2. The three records

| Record | Table | What it holds | On operation |
|---|---|---|---|
| **Tag** | `safety.tags` | a restriction on one **device** | `danger` / `out_of_service` **BLOCK** operation; `caution` / `information` advise |
| **Ground** | `safety.grounds` | an applied safety **earth** at a network point | must be removed before re-energising (advisory in this release) |
| **Clearance** | `safety.clearances` | a **permit-to-work** over a work area | **owns** the tags/grounds that protect it; surrendering it releases them |

A tag or ground can be **ad-hoc** (placed on its own) or **bound to a clearance** (`clearance_id`). A clearance moves through `issued → surrendered` (or `cancelled`); both end states **release** every tag and ground the clearance owns.

---

## 3. The interlock — a blocking tag bars operation

This is the heart of it. When a switching step's **open**/**close** is executed (or reverted), the executor first asks `safety.OperationBlocked(device)`. If an **active, blocking** tag is on the device, the step is **refused** — nothing flips — with the tag's reason:

```
cannot operate MV_RECLOSER a1b2c3d4: a "danger" safety tag is in place
— crew working downstream (remove the tag or surrender its clearance first)
```

The check runs inside the step's transaction, so a blocked step leaves the step **not executed** and the device **unchanged**. It applies to executing *and* reverting (any state change on a tagged device is barred). `caution`/`information` tags do **not** block — they're advisory.

> ⚠️ The gate is wired on the **switching-step** operate path (the operational route). Editing `device_state` directly through the feature panel's attribute PATCH is **not yet gated** — a flagged follow-up, because it would couple the generic `crud` layer to `safety`. Operate through switching orders for the interlock to apply.

---

## 4. Who may do what

A dedicated `safety:read` / `safety:write` pair (`migrations/0152`), deliberately **not** folded into `oms:write` — a blocking tag bars operation for everyone, so the right to protect (and release) a work area is granted on its own.

| Action | Permission | viewer | editor / crew | admin |
|---|---|---|---|---|
| See tags, grounds, clearances | `safety:read` | ✓ | ✓ | ✓ |
| Place/remove tags & grounds, issue/surrender clearances | `safety:write` | — | ✓ | ✓ |

Issuing a clearance **from a switching order** needs `safety:write` on top of the `oms:write` the switching surface already requires (an in-handler escalation). `break-glass-admin` holds both.

---

## 5. ▶ The operator flow

> **How to — protect a work area** 🔒 `safety:write`
> **Per device:** open the feature → **Safety** tab → **Place tag** (pick *Danger — do not operate*, give a reason). The tab shows a red "this device is tagged — it cannot be operated" banner. **Apply earth** records a ground on the point. Remove either when done (a clearance-bound one is released by surrendering the clearance, not here).
> **From a switching order (the clean way):** after the crew opens and earths the section, open the incident's switching order → **Issue clearance**. It creates the permit and **auto-applies** a danger tag on every device the order *opened* and a ground on every point it *earthed*, all bound to the clearance. Those devices now refuse to operate.
> **Hand back:** when the work is done, **surrender** the clearance — every tag and ground it owns is released, and the devices are operable again.

---

## 6. ∑ Endpoints & agent tools

Reads are `safety:read`, writes `safety:write`.

| Verb & path | What |
|---|---|
| `GET /safety/tags` · `POST /safety/tags` · `POST /safety/tags/{id}/remove` | list / place / clear a tag |
| `GET /safety/grounds` · `POST /safety/grounds` · `POST /safety/grounds/{id}/remove` | list / apply / remove an earth |
| `GET /safety/clearances` · `GET /safety/clearances/{id}` | the board · one permit with its tags/grounds |
| `POST /safety/clearances/{id}/surrender` · `.../cancel` | hand back / void — both release the protections |
| `POST /oms/switching-orders/{id}/clearance` | issue a permit from a switching order (also needs `safety:write`) |

Agent / MCP tools mirror them: `safety_list_tags` / `_grounds` / `_clearances`, `safety_get_clearance`, `safety_place_tag` / `_remove_tag`, `safety_apply_ground` / `_remove_ground`, `safety_issue_clearance_from_order`, `safety_surrender_clearance` / `_cancel_clearance`.

---

## 7. Honest limits

- ⚠️ **The gate covers the switching path, not a direct `device_state` edit.** Operate through switching orders for the interlock; the panel attribute PATCH is a flagged follow-up.
- ⚠️ **Grounds are advisory in this release.** A ground is recorded and surfaced, but closing a device toward a still-grounded section is not yet blocked (a planned guard).
- ⚠️ **Clearances are created `issued`.** There's no `requested → issued` approval step yet (the status exists for a future workflow).
- ⚠️ **No standalone Clearances board UI yet.** You interact per-device (Safety tab) and issue-from-order; a dedicated clearances view/surrender screen is a follow-up (the API and agent tools are complete, so it's reachable now).
- ⚠️ Like the rest of the operational modules, safety records are **not territory-scoped**.

---

## Related guides

- **[OMS — Outage Management](oms-guide.md)** — the switching orders whose isolate steps a clearance protects, and whose execute path the interlock guards.
- **[Switching & Protection](switching-and-protection-guide.md)** — the devices you tag and the `device_state` the interlock stops you flipping.
- **[Device Operations](operations-guide.md)** — the log that records the operations a tag prevents (or allows).
