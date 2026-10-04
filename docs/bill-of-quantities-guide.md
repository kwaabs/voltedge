# VoltEdge Bill of Quantities & Cost Catalog Guide

A how-to manual for putting **money on the network**: pricing any slice of the model as a costed schedule, and administering the price book that makes those numbers appear.

This guide has two audiences that meet in the middle. **Estimators and planners** run the Bill of Quantities (BoQ) tool from the map and read a schedule — they need only `features:read`. **Administrators** maintain the catalog behind it — currencies, unit rates, and per-feature overrides — which is metadata configuration and needs `metadata:write`. Both jobs are covered here; the administrative sections are marked so you can skip them if the price book is someone else's responsibility.

**The recurring cast.** To keep examples concrete this guide reuses the house cast: **Tom** (network analyst, here wearing an estimator's hat) prices scopes and exports schedules; **Ada** (system administrator) owns the cost catalog; **Dana** (editor) sets the occasional per-feature override when a real-world price departs from the book. The running assets are the shared **Feeder 7** (11 kV, Oakwood Zone) and its `MV_CONDUCTOR` spans and `DIST_XFMR` transformers.

**Legend.** 🔒 marks an action that needs a specific permission (named inline). 🗺️ marks something that also depends on your **territory access**. 💡 is a tip, ⚠️ a caution. Two lanes run through the chapters: **▶** introduces the "how to operate it" steps, **∑** the "how it actually computes" explanation you can read or skip.

---

## Chapter 1 — What the Bill of Quantities is

A Bill of Quantities answers a deceptively simple question: *"what is in this part of the network, and what would it cost?"* VoltEdge builds it directly from the live model — no spreadsheet, no separate asset register. You pick a **scope** (what to count), the tool rolls the scoped features up into quantities, multiplies each quantity by a **catalog rate**, and hands back a grouped, totalled schedule you can read on screen or export.

Two ideas are worth holding from the start:

- **Quantities come from the model; prices come from the catalog.** The quantities are never guessed — a conductor's length is measured from its geometry, a transformer is counted as one item. What turns a quantity into money is a **rate** you (or an administrator) have entered. No rate, no price.
- **A costing gap is shown, never hidden.** If a scoped feature has no applicable rate, it does **not** silently become zero. It appears in a separate **Unpriced** section with the quantity it would have contributed and the reason no rate applied. The grand total is therefore always honest: it is the sum of what *could* be priced, with the gaps listed beside it.

The tool is read-only and read-scoped: it can only price what you are allowed to see. 🗺️ If your territory access hides a feeder, it is absent from your BoQ too — you cannot price what you cannot list.

---

## Chapter 2 — Pricing a scope (▶ the estimator's workflow)

🔒 *Needs `features:read` — i.e. anyone who can read the map.*

Tom has been asked for a rough materials cost of everything on Feeder 7. Here is the whole workflow.

**1. Open the panel.** On the map toolbar, press the **Bill of Quantities** button (the calculator icon). A floating "Bill of Quantities" panel opens; it can be moved and resized, and stays put as you pan the map.

**2. Choose a scope.** The **Scope** dropdown offers four ways to say *what to count*:

| Scope | What it prices | How you narrow it |
| --- | --- | --- |
| **Whole network** | Every feature of each chosen class | Optionally tick specific **Classes**; leave empty for all |
| **Territory** | Everything inside one territory **and its sub-territories** | Pick the **Territory**; optionally tick **Classes** |
| **Current selection** | The one feature selected on the map | Select a feature first |
| **Trace result** | Every feature the last trace reached | Run a trace first |

For "the whole of Feeder 7", Tom runs a feeder trace first, then chooses **Trace result** — the scope becomes exactly the features the trace reached. (He could instead price by **Territory** if Feeder 7 maps cleanly onto an Oakwood Zone territory.)

💡 The **Whole network** and **Territory** scopes expose a **Classes** multi-select. Leaving it empty prices *every* class; ticking a few (say just `MV_CONDUCTOR` and `DIST_XFMR`) restricts the schedule to those. The other two scopes already name an explicit feature set, so they have no class filter.

**3. Press Price.** The schedule appears below: priced lines grouped by class, each class subtotalled, under a single grand total. Beneath it, any **Unpriced** buckets are listed in amber.

**4. Switch currency (optional).** Once a result exists, a **Currency** dropdown lists every configured currency. Pick another and the schedule re-prices instantly — the conversion happens server-side off the same base figures, so switching presentation currency never changes what was counted, only how it reads (see §5.2).

**5. Export.** Two buttons sit beside Price once there is a result:

- **PDF** — opens an A4 schedule **in a preview dialog** (`bill-of-quantities-<CUR>.pdf`): a title block naming the scope, currency and date, the grouped line-item table with subtotals and grand total, the Unpriced section, and a page footer. It's rendered entirely in the browser and shown inline; the dialog's **Print** and **Download** buttons send it to a printer or save it, and **Close** dismisses it without either. Nothing leaves the app until you choose Download or Print.
- **CSV** — saves `bill-of-quantities-<CUR>.csv`: one row per priced line then per unpriced item, with a `Status` column distinguishing the two, ending in a grand-total row. The spreadsheet-friendly form.

⚠️ Both exports are a **snapshot of the schedule on screen**, in the currency currently shown. Re-price (or re-run the trace) before exporting if the network has changed since you last pressed Price.

### 2.1 Reading the schedule

Each priced line reads **Item · Qty · Unit · Rate · Total**:

- **Item** is the grouping within a class — its subtype and/or material (e.g. *Underground · XLPE 185mm²*), or *(all)* when a single class-default rate covers everything.
- **Qty** is the rolled-up quantity: a count for discrete assets, a length in km for linear ones.
- **Unit** is `each` or `km`.
- **Rate** is the unit price in the shown currency.
- **Total** is Qty × Rate.

The footer line reports the priced-line count and names the currency; when you are viewing a non-base currency it also shows the base. The **Unpriced** block, if present, lists each gap with its quantity, unit and a plain-language reason — the administrator's to-do list for closing the costing coverage.

---

## Chapter 3 — The cost model (∑ how a price is resolved)

This chapter explains how a quantity becomes a number. Administrators need it to know *where* to enter a rate; estimators can read it to understand why a line priced the way it did.

Every feature class is priced one of two ways, decided by its geometry:

### 3.1 Linear classes — priced per kilometre

A class drawn as a **line** (a conductor, a cable) is priced by **length**. The length is computed from the geometry as a true geodesic distance — `ST_Length` over the geometry transformed to a geography — so it is metres-on-the-ground, not degrees, summed and divided into kilometres. The **rate** for that length is resolved in this order:

1. **The feature's material**, via its material coded field. If the feature records a material whose **material spec** (`/admin/materials`) carries a `cost_per_km`, that is the rate. This is the preferred path — it means two spans of the same class but different conductor price differently, correctly.
2. **A class-level `per_km` rate** from the catalog price book, as a fallback when the material carries no per-km cost.

If neither resolves, the length is **unpriced**.

💡 This is why conductor prices live on their **material specs**, not only in the price book: a conductor's cost is a property of the cable you chose, and VoltEdge already models that choice. The cost catalog's class-level `per_km` rows are the backstop for classes you price by length but don't vary by material.

### 3.2 Discrete classes — priced 'each'

A class that is a point or has no length (a transformer, a switch, a meter) is priced **per item** from the catalog's **unit-rate price book** (`metadata.cost_rates`). A rate is keyed by **feature class + subtype**:

- A rate with a **specific subtype** prices only features of that subtype.
- A rate with **no subtype** is the **class default**, covering every subtype that has no more specific rate of its own.

So you can price a `DIST_XFMR` at one default rate and still override a particular rating (a subtype) with its own line. A discrete feature whose class has neither a matching-subtype rate nor a class default is **unpriced**.

### 3.3 Per-feature overrides — the exception that wins

Sometimes a single real asset cost something the book doesn't capture — a one-off price, a salvaged unit, a negotiated figure. A **per-feature override** (Chapter 6) pins an exact unit cost to **one feature**, in the base currency, in that feature's natural unit (`per km` for a line, `each` for a discrete asset). An overridden feature is pulled out of the normal roll-up and priced at its override — and is therefore **never** unpriced. Everything else in its class still prices off the catalog as usual.

### 3.4 Precedence, in one line

> **per-feature override** ▸ else **material `cost_per_km`** (linear) / **subtype rate** (discrete) ▸ else **class default** (`per_km` fallback / default `each` rate) ▸ else **Unpriced**.

---

## Chapter 4 — Administering currencies 🔒

🔒 *The cost catalog is admin-only to read as well as write — the `/admin` screens inherit the `metadata:write` gate. If you can't see `/admin/cost-catalog`, you lack the permission.*

Ada administers the catalog at **Admin → Cost catalog** (`/admin/cost-catalog`). The page has two cards — **Currencies** and the **Unit-rate price book** — plus pointers out to material specs and the per-feature overrides.

### 4.1 The base currency

VoltEdge stores **every** cost — catalog rates, material per-km costs, overrides — in **one base currency**. This is the single source of truth; every other currency is a *presentation* layer computed from it.

- Exactly one currency is flagged **base**. Its conversion factor is pinned to **1** (it is the unit everything is denominated in), and the form forces and locks that.
- The base currency cannot be deleted, and the system enforces that there is always exactly one.

💡 Choose the base currency deliberately and early — it is the denomination in which you'll actually type every rate. Changing it later means re-interpreting every stored figure. Seeded installs start with **USD** as base.

### 4.2 Adding a presentation currency

To let estimators read schedules in, say, Ghana Cedi, Ada presses **Add currency** and enters:

- **Code** — a short code like `GHS` (fixed once created).
- **Name** — e.g. *Ghana Cedi*.
- **Per base unit** — *how many of this currency equal one base unit*. If one base unit (USD) is worth 12 GHS, the factor is **12**.

Now the BoQ's **Currency** dropdown offers GHS, and choosing it multiplies every base figure by 12 for display. ⚠️ These are **static** conversion factors, not a live FX feed — update them when your rates move. Deleting a presentation currency leaves the base-denominated prices untouched; schedules simply can no longer be shown in it.

---

## Chapter 5 — Administering the price book 🔒

The **Unit-rate price book** card lists the `each`/`per_km` rates keyed by class and subtype. This is the backbone that turns discrete-asset counts (and un-material'd lengths) into money.

### 5.1 Adding a rate

Ada presses **Add rate** and fills:

- **Feature class** — the class to price (fixed once created).
- **Subtype** — *Class default (all subtypes)* for a catch-all, or a specific subtype for a targeted rate.
- **Unit basis** — `each (per item)` for discrete assets, or `per_km (per km of length)` for the linear fallback.
- **Unit rate** — the price, **in the base currency**.
- **Notes** — free text (e.g. a supplier quote reference).

The uniqueness rule is **one rate per (class, subtype)** — including one class-default (no-subtype) row per class. The form blocks a duplicate.

💡 **Where to put conductor costs.** For conductors you price by material, don't add a class `per_km` rate — set `cost_per_km` on the **material spec** (`/admin/materials`) instead, so each cable prices correctly. Use a class `per_km` rate only for linear classes you *don't* vary by material.

### 5.2 How currency conversion interacts with rates

Every rate you type is in the **base** currency. The estimator's currency dropdown never changes a stored rate — it reconverts the whole already-computed schedule for display. So a rate is entered once, in base, and read in any currency. This is why §4.1 insists the base currency be chosen deliberately: it is the unit of the entire price book.

### 5.3 Closing the Unpriced gap

The fastest way to drive the catalog to completeness is to **run a whole-network BoQ and read the Unpriced section**. Each entry names a class (and subtype/material) with no rate. Add the missing rate, re-price, and watch the entry move up into the priced schedule. An empty Unpriced section means full costing coverage for that scope.

⚠️ Deleting a rate doesn't error loudly — features it used to price simply **revert to unpriced** until another rate covers them. The confirmation dialog says exactly this.

---

## Chapter 6 — Per-feature overrides 🔒

🔒 *Needs `metadata:write`. The override controls are hidden entirely from users without it.*

When one real asset cost something the book can't express, Dana sets a **per-feature override** without touching the catalog everyone else prices from.

**▶ Steps.** Open the feature on the map, go to its **Materials** tab, and find the **Cost override** section. Press **Set override**, enter the unit cost (shown in the base currency, in the feature's natural unit — *per km* for a line, *each* for a discrete asset), and save. An amber badge marks the feature as overridden. **Change** edits it; the reset/clear action removes it and returns the feature to normal catalog pricing.

**∑ What it does.** The override is stored against that one feature (`metadata.feature_cost_overrides`), in the base currency. At pricing time the feature is excluded from its class's normal roll-up and priced at the override instead — so it contributes its own line, is flagged as overridden, and is **never** unpriced. No other feature in the class is affected.

💡 Use overrides sparingly and for genuine exceptions. If you find yourself overriding every feature of a subtype, that's a signal the *catalog* rate is wrong — fix the rate in the price book (§5.1) so the correction is reusable, rather than repeating it feature by feature.

---

## Chapter 7 — Beyond the panel

A few things worth knowing at the edges:

- **Pricing from an agent.** The BoQ engine is exposed as the **`run_bill_of_quantities`** MCP tool (gated `features:read`), so an AI assistant can price a scope and read back the same grouped schedule the panel shows. The catalog-administration side (currencies, rates, overrides) is **not** in MCP — it stays a deliberate admin-screen action.
- **Scopes the panel doesn't surface yet.** The engine also supports **radius** and **bounding-box** spatial scopes. They are reachable through the API but not wired into the panel, because they need a map draw/point gesture that isn't built. The four scopes in Chapter 2 are what the UI offers today.
- **What's planned, not built.** Server-side CSV/XLSX export and a per-assembly Bill of Materials are the next slice; today's exports are the client-side PDF and CSV in Chapter 2.

---

## Appendix — Quick reference

**Permissions.** Run a BoQ: `features:read`. Administer the catalog (currencies, rates) and set per-feature overrides: `metadata:write` (which the `/admin` metadata screens gate on for reading too).

**Where each price lives.**

| You want to price… | Set it at… |
| --- | --- |
| A conductor by its cable type | `cost_per_km` on the **material spec** — `/admin/materials` |
| A linear class not varied by material | A `per_km` rate — **Cost catalog → price book** |
| A discrete asset (default for the class) | An `each` class-default rate — **Cost catalog → price book** |
| A discrete asset of one rating only | An `each` rate for that **subtype** — price book |
| One specific real asset, one-off | A **per-feature override** — feature's **Materials** tab |

**Why is something Unpriced?** No rate resolved through the precedence chain (§3.4): the conductor's material has no `cost_per_km` and the class has no `per_km` fallback, or the discrete asset's class has neither a matching-subtype rate nor a class default. Add the missing rate (or an override) and re-price.

**Grounded in the code.** Engine: [api/internal/boq](api/internal/boq/) and [api/internal/crud/boq.go](api/internal/crud/boq.go). Catalog metadata & migrations: `migrations/0127_create_cost_rates_and_currencies.sql`, `migrations/0128_create_feature_cost_overrides.sql`. UI: [web/src/components/map/boq-panel.tsx](web/src/components/map/boq-panel.tsx), [web/src/routes/admin-cost-catalog.tsx](web/src/routes/admin-cost-catalog.tsx), the per-feature override in [web/src/components/map/feature-panel.tsx](web/src/components/map/feature-panel.tsx), and the PDF writer [web/src/lib/boq-print.ts](web/src/lib/boq-print.ts).
