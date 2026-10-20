# VoltEdge Fault-Mode Catalog (FMEA) Guide

**Written for:** control-room and field engineers who **log faults** and need to pick the right failure mode, and administrators who **maintain the catalog** behind that picker.

When you log a fault, you describe it three different ways. Two were always there; this guide is about the third — the **failure mode**, the per-asset-class taxonomy of *what physically failed*.

**Legend.** 🔒 a permission. 💡 a tip. ⚠️ an honest limitation. **▶** how to operate it; **∑** how it's modelled.

---

## 1. The three ways a fault is described

A logged fault (`faults.fault_events`) carries three independent descriptors — don't confuse them:

| Field | Question it answers | Example | Nature |
|---|---|---|---|
| **`fault_type`** | What *kind of short*? | SLG, LL, LLG, 3-phase, earth | Electrical signature — the vocabulary the fault-current and protection engines speak. The same small set for **every** class. |
| **`fault_mode`** | What *physically failed*? | *Winding insulation failure*, *Conductor down*, *Pole fire* | The **failure mode** — a catalog keyed to the **asset class**, so a transformer's choices differ from a conductor's. **This guide.** |
| **`protective_device`** | What *cleared* it? | the breaker/fuse/recloser that operated | The device reference. |

`fault_mode` is the structured sibling of the old free-text **`cause`** column. `cause` still exists — it's now the *elaboration* / "Other" escape when a real-world mode isn't in the catalog.

---

## 2. The FMEA record

Each catalog entry is a light **FMEA** (Failure Mode and Effects Analysis) record:

- **Code** — a stable identifier (`WINDING_INSULATION_FAILURE`); what a logged fault stores. Fixed once created.
- **Label** — the human name shown in the picker (*Winding insulation failure*).
- **Dimension** — the kind of failure, for grouping and reporting. One of: **electrical** · **mechanical** · **thermal** · **environmental** · **ageing** · **operational** · **other**.
- **Typical cause** — what usually brings it about (one phrase).
- **Effect** — what it does to the asset / network (one phrase).
- **Severity** — **low** · **medium** · **high** · **critical**.

∑ It lives in `faults.fault_modes`, keyed by `feature_class_code` + `code` (unique per class), with a `sort_order` for display and an `active` flag to retire a mode without deleting the faults that reference it.

---

## 3. Logging with the class-aware picker (▶)

🔒 *Needs `faults:write` — the same gate as logging any fault.*

On the map, select the faulted asset and open the **Faults** tab. Press **Log**:

1. **What faulted** — a title.
2. **Failure mode** — a dropdown of *the faults that can happen on this asset class*. Pick one and a helper line shows its **severity**, **dimension** and **effect**. If the real cause isn't listed, choose **Other / not listed** and describe it in the free-text field.
3. **Fault type** — the electrical classification (SLG/LL/…), independent of the mode.
4. Optionally elaborate in the **detail / cause** field.

The mode is **optional** — a fault can be logged before the mode is known and classified later. An uncatalogued class simply shows no dropdown and falls back to free text.

💡 Because the mode is coded, faults become **reportable by failure mode** — which feeds the WAM **defect** a fault books (the asset failure to repair).

---

## 4. Administering the catalog 🔒

🔒 *Editing the catalog needs `metadata:write` — admin-only, like the cost and protection-curve catalogs. Logging faults (`faults:write`) does **not** let you rewrite the taxonomy.*

Go to **Admin → Fault-mode catalog** (`/admin/fault-modes`). Pick a feature class to see its modes, then **Add**, **edit** (pencil) or **delete** (trash) them. Each entry is the FMEA record of §2. Retiring a mode (clear **Active**) removes it from the picker while leaving logged faults intact.

💡 The shipped defaults are **illustrative, utility-grade starting points** — tune the labels, severities and causes to your own standards and asset base. Re-running the seed (`task seed-fault-modes`) refreshes the shipped defaults.

---

## 5. The catalog — every mode, by asset category

The shipped catalog covers all **63 feature classes**, grouped below by asset category. Each line lists a class and its failure modes (the live, per-mode FMEA detail — cause, effect, severity — is in the admin editor).

### 5.1 Transformers, tap changers & busbars

- **POWER_XFMR** (Power Transformer) — Winding insulation failure · Winding deformation · Bushing flashover · Insulating oil degradation · OLTC failure · Cooling system failure · Sustained overload · Core fault · Tank/gasket oil leak · Cumulative through-fault damage
- **DIST_XFMR** (Distribution Transformer) — Winding insulation failure · Overload/overheating · LV bushing failure · Oil leak/low oil · Lightning/surge damage · Tank corrosion · HV protection operation · Ferroresonance/overvoltage · Moisture ingress
- **AUX_TRANSFORMER** (Station Auxiliary Transformer) — Winding insulation failure · Auxiliary load overload · Loss of LVAC supply · Terminal/connection overheating · Moisture ingress · Core overheating · Insulation medium degradation
- **EARTHING_XFMR** (Earthing Transformer) — Winding insulation failure · Earth-fault thermal overload · Neutral earthing resistor failure · Bushing flashover · Oil degradation · Earth connection overheating · Tank/enclosure corrosion
- **TAP_CHANGER** (Tap Changer) — Diverter contact wear · Contact coking · Drive mechanism failure · Selector mis-operation · Diverter oil contamination · Transition resistor failure · Motor drive control fault · Mechanism seizure
- **MV_BUSBAR** (MV Busbar) — Busbar flashover · Support insulator failure · Joint/connection overheating · Foreign object/vermin contact · Partial discharge · Moisture/condensation · Thermal overload · Short-circuit force damage
- **LV_BUSBAR** (LV Busbar) — Joint/connection overheating · Busbar flashover/short · Thermal overload · Moisture/water ingress · Support insulator failure · Corrosion · Foreign object/vermin contact

### 5.2 Switchgear & switching devices

- **MV_BREAKER** (MV Breaker) — Failed to trip · Failed to close · Vacuum interrupter loss of vacuum · SF6 low gas · Contact erosion · Contacts welded closed · Charging motor/spring failure · Mechanism seizure · Bushing flashover · Operating time out of spec
- **MV_ISOLATOR** (MV Isolator) — Operated under load · Blade seizure · Contact overheating · Incomplete open gap · Support insulator flashover · Linkage corrosion · Interlock defeated/failed · Blade misalignment
- **MV_SECTIONALIZER** (MV Sectionalizer) — Incorrect fault-count operation · Failed to open in dead time · Spurious open · Control battery depleted · Current-sensing failure · Mechanism seizure · Mis-coordination with recloser · Water ingress to control cabinet
- **MV_RECLOSER** (MV Recloser) — Failed to trip · Failed to reclose · Reclose onto permanent fault · Control battery depleted · Vacuum interrupter loss of vacuum · Contact erosion · Mechanism seizure · Control/SCADA comms loss · Settings corrupted/wrong · Bushing flashover
- **MV_SWITCH** (MV Switch) — Failed to interrupt load · Mechanism seizure · SF6 low gas · Contact erosion/pitting · Contact overheating · Operating spring failure · Insulator/bushing flashover · Water ingress
- **DIST_SWITCH** (Distribution Switch) — Mechanism seizure · Contact overheating · Failed to open · Failed to close · Enclosure/contact corrosion · Water ingress · Insulator flashover · Operated onto a fault
- **MV_EARTH_SWITCH** (MV Earth Switch) — Earthed onto live circuit · Interlock failure/defeat · Failed to make earth · Poor earth contact · Blade seizure · Earth path corrosion · Incomplete earth engagement
- **LV_SWITCH** (LV Switch) — Contact overheating · Mechanism seizure · Failed to break load · Contacts welded · Terminal/contact corrosion · Water ingress · Loose cable termination
- **SERVICE_CUTOUT** (Service Cutout) — Overheated connection · Nuisance fuse operation · Fuse failed to clear · Carrier cracked/broken · Clip tension loss · Water ingress/tracking · Wrong fuse rating · Base insulation degradation
- **LV_LINK_BOX** (LV Link Box) — Water ingress/flooding · Link/connection overheating · Internal corrosion · Fuse-link failed to clear · Nuisance operation · Loose link · Enclosure/lid damage · Internal flashover

### 5.3 Protection, fuses, arresters & relays

- **MV_FUSE** (MV Fuse) — Fuse blown · Nuisance operation · Wrong rating fitted · Failed to interrupt · Element fatigue/derating · Carrier mechanism failure · Moisture ingress to tube · External flashover
- **LV_FUSE** (LV Fuse Way) — Fuse way blown · Thermal overload operation · Wrong link rating · Loose/hot fuse contact · Carrier insulation damage · Contact corrosion · Failed to interrupt
- **MV_ARRESTER** (MV Surge Arrester) — Thermal runaway · MOV block puncture · Ground-lead disconnector operated · Pollution flashover · Seal failure/moisture ingress · Housing cracking · Elevated leakage current · Lead/earth connection failure
- **PROTECTIVE_RELAY** (Protective Relay) — Mal-operation (false trip) · Failure to operate · Incorrect settings/coordination · CT/VT input loss · Auxiliary/DC supply loss · Hardware failure · Firmware fault · Comms loss · Output contact degradation
- **CONTROL_IED** (Control IED) — Hardware failure · Comms loss · Firmware fault · Auxiliary/DC supply loss · I/O point failure · Configuration/mapping error · Time-sync loss · Thermal/ageing degradation
- **ANNUNCIATOR** (Annunciator) — Lamp/LED failure · Failure to annunciate · Spurious/standing alarm · Audible/horn failure · Panel power loss · Acknowledge/test circuit fault · Window/legend degradation

### 5.4 Instrument transformers & metering

- **MV_CT** (MV Current Transformer) — Secondary open circuit · Core saturation · Primary insulation breakdown · Wrong/mis-set ratio · Secondary short circuit · Accuracy drift · Secondary terminal overheating · Reversed polarity
- **MV_VT** (MV Voltage Transformer) — Ferroresonance · HV fuse failure · Winding short/open · Insulation breakdown · Secondary fuse/MCB tripped · Ratio/burden error · Thermal overload · Internal condensation
- **MV_METERING_POINT** (MV Metering Point) — No reading · Wiring error · Phase association error · Metrology seal broken · Burden out of spec · Configuration mismatch
- **MV_METER** (MV Meter) — Hardware failure · Comms loss · Incorrect registration · Tampering · Clock/time-sync error · Firmware fault · Terminal corrosion · Display failure
- **MV_METERING_CUBICLE** (MV Metering Cubicle) — Moisture ingress · Door/gasket seal failure · Internal overheating · Enclosure corrosion · Enclosure earthing defect · Access obstruction · Vermin ingress
- **NETWORK_METER** (Network Meter) — No reading/data gap · Comms loss · Incorrect scaling · Time-sync error · Meter failure · Firmware fault · Wiring/phasing error
- **METER** (Meter) — No read · Tampering · Comms loss · Meter failure · Incorrect registration · Moisture/terminal corrosion · Clock/firmware fault · Terminal overheating · Display failure
- **METER_CHAMBER** (Meter Chamber) — Flooding · Cover/lid damage · Access obstruction · Silt/debris build-up · Structural collapse · Vermin ingress · Gas accumulation

### 5.5 Conductors, cables, joints & civil routing

- **MV_CONDUCTOR** (MV Conductor — overhead or underground) — Conductor down · Third-party dig-in · Broken conductor · Vegetation contact · Cable insulation breakdown · Water treeing · Partial discharge · Conductor clashing · Sheath damage · Conductor annealing
- **LV_CONDUCTOR** (LV Conductor) — Third-party dig-in · Conductor down · Broken/lost neutral · Insulation breakdown · ABC insulation failure · Vegetation contact · Connector/clamp overheating · Sheath damage · Overload
- **MV_CABLE_JOINT** (MV Cable Joint) — Joint insulation breakdown · Moisture ingress · Poor workmanship · Partial discharge · Connector overheating · Paper-to-polymer transition failure · Sheath/earth bond failure · Mechanical stress
- **LV_CABLE_JOINT** (LV Cable Joint) — Joint failure · Moisture ingress · Connector overheating · Neutral connection failure · Poor workmanship · Mechanical stress · Conductor corrosion
- **CABLE_TRENCH** (Cable Trench) — Trench wall collapse · Third-party dig-in exposure · Ground subsidence · Flooding · Loss of bedding/backfill · Missing tiles/marker tape · Poor thermal backfill · Root intrusion
- **CABLE_DUCT** (Cable Duct) — Duct blockage · Duct collapse · Flooding · Deformation/ovality · Seal failure · Sharp edge at entry · Root intrusion · Thermal grouping overheat
- **CABLE_CHAMBER** (Cable Chamber) — Flooding · Cover/frame defect · Structural failure · Hazardous gas accumulation · Bearer/cleat corrosion · Silt/debris build-up · Duct entry seal failure · Root intrusion
- **DUCT_WAY** (Duct Way) — Route blockage · Capacity exhausted · Record/as-built mismatch · Subsidence · Flooding · Unmanaged third-party crossing · Seal failure

### 5.6 LV gear, DER, storage, compensation & EV

- **SERVICE_POINT** (Service Point) — Loose service connection · Burnt service termination · Service fuse operated · Open service conductor · Open neutral · Water ingress at termination · Meter tamper/damage · Single phasing
- **STREET_LIGHT** (Street Light) — Lamp/LED failure · Driver/ballast failure · Photocell failure · Supply cabling fault · Column base corrosion · Column impact damage · Water ingress at door · Cutout fuse operated
- **LV_PILLAR** (LV Feeder Pillar) — Feeder fuse operated · Busbar connection overheat · Internal corrosion · Water ingress · Vandalism · Loose internal connection · Severe phase imbalance · Incomer cable/joint failure
- **LVAC_BOARD** (Station LVAC Board) — Loss of LVAC supply · Auxiliary breaker trip · Busbar overheat · Auto-changeover failure · Water ingress · Loose connection · Earth fault on auxiliary circuit
- **GENERATOR** (Generator) — Fail to start · Fuel system fault · Governor fault · AVR failure · Engine overheat · Alternator winding fault · Bearing failure · Reverse power · Synchronisation failure
- **MV_BESS** (MV Battery Storage) — Cell failure · Thermal runaway · BMS failure · Inverter/PCS fault · Cooling failure · DC arc fault · State-of-charge imbalance · DC ground fault · Off-gas/fire alarm
- **MV_CAP_BANK** (MV Capacitor Bank) — Capacitor unit failure · Unit fuse operated · Unbalance trip · Switching device failure · Can bulging/leak · Control malfunction · Bushing flashover · Inrush/transient damage
- **MV_VREG** (MV Voltage Regulator) — Tap changer mechanism failure · Control failure · Contact wear/coking · Bypass switch failure · Oil degradation · Tap hunting/runaway · Winding fault · Overtemperature
- **EV_CHARGING_STATION** (EV Charging Station) — Loss of station supply · Station earth fault · Back-office comms failure · Vandalism · Water ingress · Overload trip · Loose supply connection
- **EV_CHARGER** (EV Charger) — Power electronics failure · Overheating · Comms fault · Earth fault · Output contactor failure · Firmware fault · Ground-monitor/IMD fault · Overcurrent trip
- **EV_CONNECTOR** (EV Connector) — Contact wear · Overheating · Pin/contact damage · Cable damage · Latch/lock failure · Water ingress · Proximity/control-pilot fault · Vandalism/theft

### 5.7 Station DC & control, panels, containers, structures & civil

- **STATION_BATTERY** (Station Battery) — Open-circuit/failed cell · Low terminal voltage · Plate sulphation · Low electrolyte · Thermal runaway · Capacity fade · Terminal post corrosion · Case leak
- **BATTERY_CHARGER** (Battery Charger) — Total output loss · Rectifier/thyristor failure · DC overvoltage · AC input supply loss · Excessive DC ripple · Overheating · Alarm/annunciation failure · DC earth fault
- **SUBSTATION_PANEL** (Substation Panel) — Internal wiring fault · Loose terminal · Loss of control supply · Relay malfunction · Alarm/annunciator failure · Condensation · Vermin ingress · Internal overheating
- **MV_SWITCHBOARD** (MV Switchboard) — Enclosure flooding · Condensation · Busbar common-mode flashover · Vermin ingress · Door/gasket seal failure · Frame corrosion · Internal overheating · Access/interlock obstruction
- **MV_SWITCHGEAR_PANEL** (MV Switchgear Panel) — Water/moisture ingress · Condensation · Compartment flashover · Vermin ingress · Seal degradation · Panel corrosion · Interlock seizure · Anti-condensation heater failure
- **BAY** (Bay) — Common DC supply loss · Bay earthing/bonding fault · Cable trench/pit flooding · Auxiliary AC supply loss · Marshalling wiring fault · Access restriction · Vermin nesting
- **RMU** (Ring Main Unit) — SF6 leak/low pressure · Gas tank corrosion · Switch mechanism seizure · Plinth/chamber flooding · Switch-fuse fail to operate · Cable box moisture ingress · Pressure-relief corrosion · Operating access obstruction
- **PACKAGED_SUBSTATION** (Packaged Substation) — Enclosure flooding · Ventilation blockage · Door/seal failure · Enclosure corrosion · Vermin ingress · Vehicle impact · Condensation · Security breach/vandalism
- **SUBSTATION** (Substation) — Site flooding · Earth grid degradation/theft · Fence/security breach · Fire · Oil bund failure · Vegetation encroachment · Access road obstruction · Lightning/surge damage
- **CONTROL_BUILDING** (Control Building) — Roof leak · HVAC failure · Fire · Flooding · Structural cracking/subsidence · Security breach · Battery-room ventilation failure · Damp/condensation corrosion
- **STRUCTURE** (Structure) — Structural corrosion · Foundation failure · Ground subsidence · Concrete cracking/spalling · Impact damage · Structural overload · Vegetation/root damage
- **POLE** (Pole) — Pole failure/fallen pole · Groundline rot · Pole lean/tilt · Vehicle impact · Woodpecker/insect damage · Pole fire/charring · Steel/concrete corrosion · Crossarm/fixings deterioration
- **TOWER** (Tower) — Member corrosion · Foundation failure/uplift · Member/angle theft · Bolt loosening · Foundation/ground erosion · Wind/ice overload · Impact damage · Grillage corrosion

---

## 6. Honest limits

- ⚠️ **The shipped catalog is illustrative.** The modes, and especially their **severities and causes**, are utility-grade starting points generated to be comprehensive — **review and tune them** to your standards and asset base before treating them as authoritative. It's all admin-editable (§4) and re-seedable.
- ⚠️ **The catalog seeds `draft`-free but needs loading.** Apply migration `0154`, then `task seed-fault-modes` (idempotent).
- The mode is **optional** on a fault and keyed by the asset's class — an **asset-less** fault (anchored only by a location or an incident) has no class, so no picker; use the free-text cause.
- A deleted or retired mode never orphans a logged fault: `fault_mode` is a bare code, so records keep their value even after a mode leaves the picker.

---

## Related guides

- **[Faults](faults-guide.md)** — logging, tracking and clearing fault events, and booking the WAM defect a fault leaves behind.
- **[Switching & Protection](switching-and-protection-guide.md)** — the protective devices a fault's `protective_device` names.
- **[Feature Catalog](feature-catalog.md)** — the per-class reference for the 63 asset classes this catalog covers.

**Grounded in the code.** Schema: `migrations/0154_create_fault_modes.sql`. Seed: `seed/fault_modes_catalog.sql` (loaded by `task seed-fault-modes`). Engine: [api/internal/faults](api/internal/faults/) (`ListFaultModes` + the catalog CRUD in `store.go`, the `/faults/modes` routes in `http.go`). UI: the picker in [web/src/components/map/feature-faults-tab.tsx](web/src/components/map/feature-faults-tab.tsx), the admin editor in [web/src/routes/admin-fault-modes.tsx](web/src/routes/admin-fault-modes.tsx).
