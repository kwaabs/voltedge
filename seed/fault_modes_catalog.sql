-- fault_modes_catalog.sql
-- Purpose: the per-feature-class FAILURE-MODE catalog (FMEA) that feeds the fault
-- form's class-aware "cause" picker (migration 0154). One row per (class, mode):
-- the faults that can happen on each asset, with a failure dimension, a typical
-- cause, the effect, and a severity. Idempotent via ON CONFLICT DO UPDATE, so
-- re-seeding refreshes the shipped defaults (admin edits to label/severity are a
-- re-seed away from reset -- the catalog is admin-editable at /admin once S4 lands).
--
-- Requires migration 0154 applied first. Grouped by asset category; codes are
-- UPPER_SNAKE and unique within a class.

-- =====================================================================
-- Transformers, tap changers & busbars
-- =====================================================================
INSERT INTO faults.fault_modes (feature_class_code, code, label, dimension, typical_cause, effect, severity, sort_order) VALUES
-- POWER_XFMR — Power Transformer
('POWER_XFMR','WINDING_INSULATION_FAILURE','Winding insulation failure','electrical','Ageing/overheating of paper insulation, moisture ingress or through-fault stress','Inter-turn or phase-to-earth fault, gas generation, transformer trip and outage','critical',0),
('POWER_XFMR','WINDING_DEFORMATION','Winding deformation / displacement','mechanical','Electromagnetic forces from a nearby through-fault','Clearance loss, future dielectric failure, reduced short-circuit withstand','high',1),
('POWER_XFMR','BUSHING_FLASHOVER','Bushing flashover','electrical','Surface contamination, cracked porcelain or moisture ingress','Phase-to-earth fault, possible fire, transformer outage','high',2),
('POWER_XFMR','OIL_DEGRADATION','Insulating oil degradation','ageing','Oxidation, moisture and particle contamination over service life','Reduced dielectric strength and cooling, accelerated ageing','medium',3),
('POWER_XFMR','OLTC_FAILURE','On-load tap changer failure','mechanical','Diverter contact wear, coking or drive mechanism fault','Loss of voltage regulation, arcing, possible tank fault','high',4),
('POWER_XFMR','COOLING_SYSTEM_FAILURE','Cooling system failure','thermal','Pump/fan motor failure, blocked radiators or low oil flow','Overheating, loss of rating, accelerated insulation ageing','high',5),
('POWER_XFMR','OVERLOAD_THERMAL','Sustained overload','thermal','Load beyond nameplate rating or high ambient temperature','Hot-spot overheating, gas generation, reduced life','medium',6),
('POWER_XFMR','CORE_FAULT','Core fault / circulating current','electrical','Core lamination insulation breakdown or multiple core earths','Localised overheating, gassing, efficiency loss','medium',7),
('POWER_XFMR','TANK_OIL_LEAK','Tank / gasket oil leak','mechanical','Gasket deterioration, weld crack or corrosion','Oil loss, low-oil alarm, environmental contamination','medium',8),
('POWER_XFMR','THROUGH_FAULT_DAMAGE','Cumulative through-fault damage','electrical','Repeated downstream faults imposing mechanical stress','Progressive winding loosening and insulation wear','medium',9),
-- DIST_XFMR — Distribution Transformer
('DIST_XFMR','WINDING_INSULATION_FAILURE','Winding insulation failure','electrical','Insulation ageing, moisture ingress or lightning surge','Internal short, phase-to-earth fault, transformer failure','critical',0),
('DIST_XFMR','OVERLOAD_THERMAL','Overload / overheating','thermal','Load growth beyond rating, unbalanced LV loading','Hot-spot overheating, accelerated ageing, premature failure','high',1),
('DIST_XFMR','LV_BUSHING_FAILURE','LV bushing failure','electrical','Loose connection overheating, contamination or physical damage','Phase fault, LV supply loss to customers','high',2),
('DIST_XFMR','OIL_LEAK','Oil leak / low oil level','mechanical','Corroded tank, failed gasket or vandalism','Loss of cooling and insulation, risk of failure','medium',3),
('DIST_XFMR','LIGHTNING_SURGE_DAMAGE','Lightning / surge damage','environmental','Lightning strike with inadequate surge protection','Winding or bushing failure, transformer destruction','high',4),
('DIST_XFMR','TANK_CORROSION','Tank corrosion','environmental','Atmospheric exposure, coastal salt or moisture','Oil leaks, reduced asset life','medium',5),
('DIST_XFMR','HV_FUSE_OPERATION','HV protection operation','operational','Downstream fault or transformer internal fault','Loss of supply until investigated and refused','medium',6),
('DIST_XFMR','FERRORESONANCE','Ferroresonance / overvoltage','electrical','Single-phasing with cable capacitance under light load','Overvoltage, overheating, insulation stress','low',7),
('DIST_XFMR','MOISTURE_INGRESS','Moisture ingress','environmental','Breather failure or seal degradation','Reduced oil dielectric strength, corrosion','medium',8),
-- AUX_TRANSFORMER — Station Auxiliary Transformer
('AUX_TRANSFORMER','WINDING_FAILURE','Winding insulation failure','electrical','Insulation ageing or overload','Loss of station LVAC supply, protection trip','high',0),
('AUX_TRANSFORMER','OVERLOAD_THERMAL','Auxiliary load overload','thermal','Added station loads beyond design capacity','Overheating and reduced transformer life','medium',1),
('AUX_TRANSFORMER','LV_SUPPLY_LOSS','Loss of LVAC supply','operational','Transformer fault or upstream supply loss','Loss of station services (chargers, cooling, controls)','high',2),
('AUX_TRANSFORMER','TERMINAL_OVERHEATING','Terminal / connection overheating','thermal','Loose or corroded LV terminals','Localised heating, connection failure','medium',3),
('AUX_TRANSFORMER','MOISTURE_INGRESS','Moisture ingress','environmental','Enclosure seal failure or condensation','Insulation deterioration, reduced dielectric strength','medium',4),
('AUX_TRANSFORMER','CORE_OVERHEATING','Core / no-load loss overheating','thermal','Core lamination fault or over-excitation','Overheating, gassing, efficiency loss','low',5),
('AUX_TRANSFORMER','OIL_OR_RESIN_DEGRADATION','Insulation medium degradation','ageing','Oil oxidation or resin cracking over service life','Reduced dielectric and thermal performance','medium',6),
-- EARTHING_XFMR — Earthing Transformer
('EARTHING_XFMR','WINDING_INSULATION_FAILURE','Winding insulation failure','electrical','Insulation ageing or repeated earth-fault duty','Loss of neutral earthing, phase-to-earth fault','critical',0),
('EARTHING_XFMR','THERMAL_OVERLOAD_EARTHFAULT','Earth-fault thermal overload','thermal','Prolonged earth-fault current beyond short-time rating','Overheating, winding damage, insulation failure','high',1),
('EARTHING_XFMR','NER_FAILURE','Neutral earthing resistor failure','electrical','Resistor element cracking, corrosion or overload','Loss of controlled earth-fault current limiting','high',2),
('EARTHING_XFMR','BUSHING_FLASHOVER','Bushing flashover','electrical','Contamination, moisture or cracked porcelain','Phase-to-earth fault, loss of earthing function','medium',3),
('EARTHING_XFMR','OIL_DEGRADATION','Oil degradation','ageing','Oxidation and moisture over service life','Reduced dielectric strength and cooling','medium',4),
('EARTHING_XFMR','CONNECTION_OVERHEATING','Earth connection overheating','thermal','Loose or corroded neutral/earth connection','High resistance, ineffective earthing','medium',5),
('EARTHING_XFMR','CORROSION','Tank / enclosure corrosion','environmental','Outdoor exposure and moisture','Oil leaks, reduced asset life','low',6),
-- TAP_CHANGER — Transformer Tap Changer
('TAP_CHANGER','DIVERTER_CONTACT_WEAR','Diverter switch contact wear','mechanical','Repeated switching arcing erosion','Rising contact resistance, overheating, coking','high',0),
('TAP_CHANGER','CONTACT_COKING','Contact coking / carbonisation','thermal','Arcing by-products building on contacts','Overheating, poor conduction, possible flashover','high',1),
('TAP_CHANGER','DRIVE_MECHANISM_FAILURE','Drive mechanism failure','mechanical','Motor, gearbox or linkage wear or jamming','Tap changer stalls mid-operation, voltage regulation lost','high',2),
('TAP_CHANGER','SELECTOR_MISOPERATION','Tap selector mis-operation','operational','Control fault, out-of-step or incomplete operation','Wrong tap position, overvoltage/undervoltage','medium',3),
('TAP_CHANGER','OIL_COMPARTMENT_CONTAMINATION','Diverter oil contamination','ageing','Arcing by-products degrading compartment oil','Reduced dielectric strength, flashover risk','medium',4),
('TAP_CHANGER','TRANSITION_RESISTOR_FAILURE','Transition resistor failure','electrical','Resistor burnout from prolonged mid-transition dwell','Short-circuit between taps, severe internal fault','critical',5),
('TAP_CHANGER','MOTOR_DRIVE_CONTROL_FAULT','Motor drive control fault','operational','Limit switch, relay or wiring fault','Failure to change tap, runaway or hunting','medium',6),
('TAP_CHANGER','MECHANISM_SEIZURE','Mechanism seizure','mechanical','Lack of maintenance, corrosion or lubricant failure','Tap changer immobile, stuck voltage regulation','medium',7),
-- MV_BUSBAR — MV Busbar
('MV_BUSBAR','BUSBAR_FLASHOVER','Busbar flashover','electrical','Contamination, moisture, vermin or overvoltage','Phase-to-phase or phase-to-earth fault, switchgear trip','critical',0),
('MV_BUSBAR','SUPPORT_INSULATOR_FAILURE','Support insulator failure','mechanical','Cracked, contaminated or aged insulator','Loss of clearance, flashover, busbar fault','high',1),
('MV_BUSBAR','CONNECTION_OVERHEATING','Joint / connection overheating','thermal','Loose or corroded bolted joint','High resistance hot-spot, connection failure','high',2),
('MV_BUSBAR','FOREIGN_OBJECT_CONTACT','Foreign object / vermin contact','environmental','Animal, tool or debris bridging phases','Flashover, phase fault, busbar outage','high',3),
('MV_BUSBAR','PARTIAL_DISCHARGE','Partial discharge','electrical','Insulation voids, surface tracking or contamination','Progressive insulation damage, eventual flashover','medium',4),
('MV_BUSBAR','MOISTURE_CONDENSATION','Moisture / condensation','environmental','Enclosure heater failure or poor ventilation','Surface tracking, reduced clearance, flashover risk','medium',5),
('MV_BUSBAR','THERMAL_OVERLOAD','Busbar thermal overload','thermal','Load beyond continuous current rating','Overheating, joint annealing, insulation damage','medium',6),
('MV_BUSBAR','MECHANICAL_FAULT_FORCES','Short-circuit force damage','mechanical','Electromagnetic forces during a through-fault','Busbar or support distortion, loosened joints','medium',7),
-- LV_BUSBAR — LV Busbar
('LV_BUSBAR','CONNECTION_OVERHEATING','Joint / connection overheating','thermal','Loose or corroded bolted connection','Hot-spot, burnt joint, loss of supply','high',0),
('LV_BUSBAR','BUSBAR_FLASHOVER','Busbar flashover / short','electrical','Insulation failure, vermin or foreign object','Phase fault, main fuse/breaker operation, supply loss','high',1),
('LV_BUSBAR','THERMAL_OVERLOAD','Thermal overload','thermal','Load beyond rating or phase imbalance','Overheating, insulation damage, nuisance tripping','medium',2),
('LV_BUSBAR','MOISTURE_INGRESS','Moisture / water ingress','environmental','Failed enclosure seal or flooding','Corrosion, tracking, short circuit','medium',3),
('LV_BUSBAR','SUPPORT_INSULATOR_FAILURE','Support insulator failure','mechanical','Aged or cracked support insulation','Loss of clearance, busbar sag, fault','medium',4),
('LV_BUSBAR','CORROSION','Corrosion of conductors/joints','ageing','Humidity, pollution or dissimilar-metal contact','Rising resistance, overheating, mechanical weakening','medium',5),
('LV_BUSBAR','FOREIGN_OBJECT_CONTACT','Foreign object / vermin contact','environmental','Animal, tool or debris bridging bars','Short circuit, phase fault, supply loss','medium',6)
ON CONFLICT (feature_class_code, code) DO UPDATE SET
    label = EXCLUDED.label, dimension = EXCLUDED.dimension, typical_cause = EXCLUDED.typical_cause,
    effect = EXCLUDED.effect, severity = EXCLUDED.severity, sort_order = EXCLUDED.sort_order;

-- =====================================================================
-- Switchgear & switching devices (MV & LV)
-- =====================================================================
INSERT INTO faults.fault_modes (feature_class_code, code, label, dimension, typical_cause, effect, severity, sort_order) VALUES
-- MV_BREAKER — MV Breaker
('MV_BREAKER','FAIL_TO_TRIP','Failed to trip on fault','operational','Mechanism seizure, trip-coil failure or control-supply loss','Fault not cleared, backup protection operates, wider outage','critical',0),
('MV_BREAKER','FAIL_TO_CLOSE','Failed to close on command','mechanical','Closing-coil failure, spring not charged or linkage jam','Supply cannot be restored, switching delayed','high',1),
('MV_BREAKER','VI_VACUUM_LOSS','Vacuum interrupter loss of vacuum','ageing','Bellows fatigue, seal porosity or bottle crack','Loss of interrupting capability, risk of restrike and flashover','critical',2),
('MV_BREAKER','SF6_LOW_GAS','SF6 low gas pressure','environmental','Seal degradation or valve leak lowering density','Reduced interrupting/insulation capability, lockout alarm','high',3),
('MV_BREAKER','CONTACT_EROSION','Main contact erosion/wear','ageing','Repeated fault-current interruption eroding contacts','Rising contact resistance, overheating, reduced duty life','medium',4),
('MV_BREAKER','CONTACT_WELD','Contacts welded closed','electrical','High fault current welding contacts on interruption','Breaker cannot open, device locked closed','critical',5),
('MV_BREAKER','SPRING_CHARGE_FAIL','Charging motor/spring failure','mechanical','Motor burnout, gearbox wear or spring fatigue','No stored energy, cannot complete close/open cycle','high',6),
('MV_BREAKER','MECH_SEIZURE','Operating mechanism seizure','mechanical','Corrosion, lubricant hardening or linkage wear','Slow or failed operation, timing out of spec','high',7),
('MV_BREAKER','BUSHING_FLASHOVER','Bushing/insulator flashover','environmental','Pollution, moisture or surface tracking on bushing','Phase-to-earth fault, equipment damage, outage','high',8),
('MV_BREAKER','SLOW_OPERATION','Operating time out of specification','ageing','Mechanism drag, worn dashpot or weak spring','Mis-coordination with protection, extended fault duration','medium',9),
-- MV_ISOLATOR — MV Isolator
('MV_ISOLATOR','OPERATED_ON_LOAD','Operated under load','operational','Switched while energised load current flowing','Severe arcing, flashover, blade and operator hazard','critical',0),
('MV_ISOLATOR','BLADE_SEIZURE','Blade seizure/stiff operation','mechanical','Corrosion, grit or hardened grease in hinge','Cannot open or close, isolation delayed','high',1),
('MV_ISOLATOR','POOR_CONTACT_HEATING','Contact overheating','thermal','Pitted or misaligned blade contacts raising resistance','Hot joint, oxidation, local annealing','medium',2),
('MV_ISOLATOR','INCOMPLETE_OPEN','Incomplete open gap','mechanical','Linkage wear or stop mis-adjustment','Insufficient isolation gap, unsafe for work','high',3),
('MV_ISOLATOR','INSULATOR_FLASHOVER','Support insulator flashover','environmental','Pollution, salt or moisture on insulator surface','Phase-to-earth fault, loss of isolation integrity','high',4),
('MV_ISOLATOR','LINKAGE_CORROSION','Operating linkage corrosion','ageing','Weathering of exposed rods and pins','Stiff, misaligned or failed operation','medium',5),
('MV_ISOLATOR','INTERLOCK_DEFEAT','Interlock defeated/failed','operational','Worn interlock or deliberate bypass','Out-of-sequence operation, safety hazard','high',6),
('MV_ISOLATOR','BLADE_MISALIGN','Blade misalignment','mechanical','Impact, thermal cycling or loose mounting','Fails to fully engage contacts, heating or open-circuit','medium',7),
-- MV_SECTIONALIZER — MV Sectionalizer
('MV_SECTIONALIZER','MISCOUNT','Incorrect fault-count operation','operational','Counter logic drift or sensing error','Opens on wrong count, fails to isolate faulted section','high',0),
('MV_SECTIONALIZER','FAIL_TO_OPEN','Failed to open in dead time','mechanical','Mechanism seizure or counter not reaching setting','Faulted section not isolated, upstream recloser locks out','high',1),
('MV_SECTIONALIZER','SPURIOUS_OPEN','Spurious open on transient','operational','Over-sensitive counting of inrush or through-fault','Unnecessary section outage','medium',2),
('MV_SECTIONALIZER','CONTROL_BATTERY_FLAT','Control battery depleted','electrical','Flat or aged battery, failed charger','Loss of counting/operation, device inert','high',3),
('MV_SECTIONALIZER','CT_SENSE_FAIL','Current-sensing failure','electrical','Faulty CT or wiring to control','No fault detection, sectionalizing lost','high',4),
('MV_SECTIONALIZER','MECH_SEIZURE','Operating mechanism seizure','mechanical','Corrosion or hardened lubricant','Slow or failed operation','medium',5),
('MV_SECTIONALIZER','MISCOORDINATION','Mis-coordination with recloser','operational','Settings not matched to upstream recloser cycle','Nuisance lockout or failure to sectionalize','high',6),
('MV_SECTIONALIZER','WATER_INGRESS','Water ingress to control cabinet','environmental','Failed seal or gland on enclosure','Corrosion, electronics failure, mis-operation','medium',7),
-- MV_RECLOSER — MV Recloser
('MV_RECLOSER','FAIL_TO_TRIP','Failed to trip on fault','operational','Trip-coil, control electronics or supply failure','Fault not cleared, backup protection widens outage','critical',0),
('MV_RECLOSER','FAIL_TO_RECLOSE','Failed to reclose','operational','Mechanism, control logic or stored-energy failure','Permanent outage on a transient fault','high',1),
('MV_RECLOSER','REPEATED_RECLOSE','Reclose onto permanent fault','operational','Reclose settings too aggressive','Repeated fault stress, equipment damage, through-fault','high',2),
('MV_RECLOSER','CONTROL_BATTERY_FLAT','Control battery depleted','electrical','Aged battery or failed charger','Loss of protection and reclose function','high',3),
('MV_RECLOSER','VI_VACUUM_LOSS','Vacuum interrupter loss of vacuum','ageing','Bellows fatigue or bottle crack','Loss of interrupting capability, restrike risk','critical',4),
('MV_RECLOSER','CONTACT_EROSION','Contact erosion from fault duty','ageing','Cumulative fault interruptions eroding contacts','Rising resistance, reduced interrupting life','medium',5),
('MV_RECLOSER','MECH_SEIZURE','Operating mechanism seizure','mechanical','Corrosion or lubricant hardening','Slow or failed trip/close operation','high',6),
('MV_RECLOSER','CONTROL_COMMS_LOSS','Control/SCADA comms loss','operational','Radio, cellular or link failure','Loss of remote control and status visibility','medium',7),
('MV_RECLOSER','SETTINGS_CORRUPT','Protection settings corrupted/wrong','operational','Mis-configuration or firmware fault','Mis-coordination, nuisance or failure to operate','high',8),
('MV_RECLOSER','BUSHING_FLASHOVER','Bushing flashover','environmental','Pollution or moisture on bushing surface','Phase-to-earth fault, outage','medium',9),
-- MV_SWITCH — MV Switch (load-break)
('MV_SWITCH','FAIL_TO_BREAK_LOAD','Failed to interrupt load current','operational','Arc-chute degradation or SF6/oil loss','Sustained arcing, switch damage, flashover','critical',0),
('MV_SWITCH','MECH_SEIZURE','Operating mechanism seizure','mechanical','Corrosion or hardened grease in mechanism','Cannot open or close, switching delayed','high',1),
('MV_SWITCH','SF6_LOW_GAS','SF6 low gas pressure','environmental','Seal leak reducing gas density','Reduced interrupting and insulation capability','high',2),
('MV_SWITCH','CONTACT_EROSION','Contact erosion/pitting','ageing','Repeated load switching eroding contacts','Rising resistance, overheating','medium',3),
('MV_SWITCH','CONTACT_HEATING','Contact overheating','thermal','High resistance from oxidation or misalignment','Hot joint, thermal damage, possible failure','medium',4),
('MV_SWITCH','SPRING_FAIL','Operating spring failure','mechanical','Spring fatigue or breakage','Slow or incomplete load-break operation','high',5),
('MV_SWITCH','INSULATOR_FLASHOVER','Insulator/bushing flashover','environmental','Pollution or moisture on surface','Phase-to-earth fault, outage','high',6),
('MV_SWITCH','WATER_INGRESS','Water ingress to tank/enclosure','environmental','Failed seal or breather','Internal corrosion, insulation failure','medium',7),
-- DIST_SWITCH — Distribution Switch
('DIST_SWITCH','MECH_SEIZURE','Operating mechanism seizure','mechanical','Corrosion, grit or hardened lubricant','Cannot operate, switching delayed','high',0),
('DIST_SWITCH','CONTACT_HEATING','Contact overheating','thermal','High resistance from pitting or loose contact','Hot joint, oxidation, local damage','medium',1),
('DIST_SWITCH','FAIL_TO_OPEN','Failed to open on command','operational','Mechanism jam or linkage failure','Section not isolated, switching blocked','high',2),
('DIST_SWITCH','FAIL_TO_CLOSE','Failed to close on command','mechanical','Linkage jam or spring failure','Supply restoration blocked','high',3),
('DIST_SWITCH','CORROSION','Enclosure/contact corrosion','ageing','Weathering and moisture ingress','Degraded contacts, stiff operation','medium',4),
('DIST_SWITCH','WATER_INGRESS','Water ingress to enclosure','environmental','Failed seal or gland','Internal flashover, corrosion, mis-operation','medium',5),
('DIST_SWITCH','INSULATOR_FLASHOVER','Insulator flashover','environmental','Pollution or moisture on insulator','Phase-to-earth fault, outage','high',6),
('DIST_SWITCH','OPERATED_ON_FAULT','Operated onto a fault','operational','Switching in without confirming dead section','Arc flash, equipment damage, operator hazard','high',7),
-- MV_EARTH_SWITCH — MV Earth Switch
('MV_EARTH_SWITCH','CLOSED_ON_LIVE','Earthed onto live circuit','operational','Interlock defeat or wrong confirmation of dead','Bolted earth fault, severe arc flash, operator hazard','critical',0),
('MV_EARTH_SWITCH','INTERLOCK_FAIL','Interlock failure/defeat','mechanical','Worn, broken or bypassed interlock','Out-of-sequence operation, unsafe earthing','critical',1),
('MV_EARTH_SWITCH','FAIL_TO_CLOSE','Failed to make earth','mechanical','Mechanism seizure or linkage jam','No safe earth applied, work unsafe','high',2),
('MV_EARTH_SWITCH','POOR_EARTH_CONTACT','Poor earth contact/high resistance','electrical','Corroded or misaligned earth contacts','Ineffective earth, induced-voltage hazard','high',3),
('MV_EARTH_SWITCH','BLADE_SEIZURE','Blade seizure','mechanical','Corrosion or hardened grease in hinge','Cannot apply or remove earth','high',4),
('MV_EARTH_SWITCH','CORROSION','Earth path/connection corrosion','ageing','Weathering of earth strap and terminals','Degraded earth continuity','medium',5),
('MV_EARTH_SWITCH','INCOMPLETE_CLOSE','Incomplete earth engagement','mechanical','Stop mis-adjustment or linkage wear','Partial earth, unreliable protection','high',6),
-- LV_SWITCH — LV Switch
('LV_SWITCH','CONTACT_HEATING','Contact overheating','thermal','Loose or oxidised contacts raising resistance','Hot joint, insulation damage, fire risk','high',0),
('LV_SWITCH','MECH_SEIZURE','Mechanism seizure/stiff operation','mechanical','Corrosion or hardened lubricant','Cannot operate, switching delayed','medium',1),
('LV_SWITCH','FAIL_TO_BREAK_LOAD','Failed to break load current','operational','Worn or degraded contacts/arc path','Sustained arcing, switch damage','high',2),
('LV_SWITCH','CONTACT_WELD','Contacts welded','electrical','High current or fault welding contacts','Switch cannot open','high',3),
('LV_SWITCH','CORROSION','Terminal/contact corrosion','ageing','Moisture and weathering','Rising resistance, intermittent contact','medium',4),
('LV_SWITCH','WATER_INGRESS','Water ingress to enclosure','environmental','Failed seal or damaged cover','Tracking, corrosion, earth fault','medium',5),
('LV_SWITCH','LOOSE_TERMINATION','Loose cable termination','mechanical','Poor torque or vibration loosening','Overheating, arcing, open circuit','high',6),
-- SERVICE_CUTOUT — Service Cutout
('SERVICE_CUTOUT','OVERHEAT_CONNECTION','Overheated connection','thermal','Loose or corroded terminal/fuse clip','Hot joint, melted carrier, service outage','high',0),
('SERVICE_CUTOUT','NUISANCE_FUSE_BLOW','Nuisance fuse operation','electrical','Undersized fuse or transient overload','Loss of supply without a real fault','medium',1),
('SERVICE_CUTOUT','FUSE_FAIL_TO_CLEAR','Fuse failed to clear fault','operational','Wrong rating or degraded element','Fault not cleared, upstream protection operates','high',2),
('SERVICE_CUTOUT','CARRIER_CRACK','Fuse carrier cracked/broken','mechanical','Impact, UV embrittlement or over-torque','Loss of mechanical integrity, open circuit','medium',3),
('SERVICE_CUTOUT','CLIP_TENSION_LOSS','Fuse clip tension loss','ageing','Thermal cycling relaxing spring clips','Poor contact, overheating, intermittent supply','medium',4),
('SERVICE_CUTOUT','WATER_INGRESS','Water ingress/tracking','environmental','Damaged seal or cover letting moisture in','Tracking, corrosion, earth fault','medium',5),
('SERVICE_CUTOUT','WRONG_FUSE_RATING','Incorrect fuse rating fitted','operational','Mis-selection during replacement','Mis-coordination, nuisance trips or no protection','high',6),
('SERVICE_CUTOUT','INSULATION_DEGRADE','Base insulation degradation','ageing','UV, heat and weathering of moulded base','Tracking, flashover, earth fault','medium',7),
-- LV_LINK_BOX — LV Link Box
('LV_LINK_BOX','WATER_INGRESS','Water ingress/flooding','environmental','Below-ground pit flooding or failed seal','Corrosion, tracking, phase/earth fault','high',0),
('LV_LINK_BOX','LINK_OVERHEAT','Link/connection overheating','thermal','Loose or corroded link and bolted joints','Hot joint, melted link, localised outage','high',1),
('LV_LINK_BOX','CORROSION','Internal corrosion','ageing','Damp environment corroding copper and steel','Rising resistance, degraded connections','medium',2),
('LV_LINK_BOX','FUSE_FAIL_TO_CLEAR','Fuse-link failed to clear','operational','Wrong rating or degraded element','Fault not cleared, wider LV outage','high',3),
('LV_LINK_BOX','NUISANCE_OPERATION','Nuisance fuse operation','electrical','Undersized link or overload','Unnecessary loss of supply','medium',4),
('LV_LINK_BOX','LOOSE_LINK','Loose link/bolted connection','mechanical','Poor torque or vibration','Arcing, overheating, intermittent supply','high',5),
('LV_LINK_BOX','ENCLOSURE_DAMAGE','Enclosure/lid damage','mechanical','Traffic load, impact or corrosion of lid','Exposure to ingress and public hazard','medium',6),
('LV_LINK_BOX','FLASHOVER','Internal flashover','environmental','Contamination, condensation or vermin bridging','Phase-to-earth fault, equipment damage','high',7)
ON CONFLICT (feature_class_code, code) DO UPDATE SET
    label = EXCLUDED.label, dimension = EXCLUDED.dimension, typical_cause = EXCLUDED.typical_cause,
    effect = EXCLUDED.effect, severity = EXCLUDED.severity, sort_order = EXCLUDED.sort_order;

-- =====================================================================
-- Instrument transformers & metering
-- =====================================================================
INSERT INTO faults.fault_modes (feature_class_code, code, label, dimension, typical_cause, effect, severity, sort_order) VALUES
-- MV_CT — MV Current Transformer
('MV_CT','CT_SECONDARY_OPEN','CT secondary open circuit','electrical','Loose or broken secondary wiring, open terminal, unshorted CT','Dangerous secondary overvoltage, insulation breakdown, protection mal-operation','critical',0),
('MV_CT','CT_SATURATION','Core saturation on fault','electrical','Undersized core or excessive primary fault current/remanent flux','Distorted secondary output, protection under-reach or delayed trip','high',1),
('MV_CT','CT_INSULATION_FAILURE','Primary insulation breakdown','electrical','Ageing/contaminated insulation, partial discharge, moisture ingress','Phase-to-earth fault, flashover, loss of measurement','critical',2),
('MV_CT','CT_WRONG_RATIO','Incorrect or mis-set ratio','operational','Wrong tap selected or mis-recorded nameplate ratio','Systematic metering/protection error, incorrect current scaling','high',3),
('MV_CT','CT_SECONDARY_SHORT','Secondary short circuit','electrical','Shorting link left in or faulted secondary wiring','Loss of signal to metering and protection, blind relay','high',4),
('MV_CT','CT_ACCURACY_DRIFT','Accuracy class drift','ageing','Core ageing, burden mismatch, thermal cycling','Revenue metering error outside accuracy class','medium',5),
('MV_CT','CT_TERMINAL_OVERHEAT','Secondary terminal overheating','thermal','Loose terminal, high burden, poor connection','Hot joint, intermittent signal, terminal damage','medium',6),
('MV_CT','CT_POLARITY_REVERSED','Reversed polarity','operational','Incorrect P1/P2 or S1/S2 connection at install','Directional protection mal-operation, wrong power-flow sign','high',7),
-- MV_VT — MV Voltage Transformer
('MV_VT','VT_FERRORESONANCE','Ferroresonance','electrical','Unloaded VT with cable capacitance, switching transient','Sustained overvoltage oscillation, VT overheating and failure','critical',0),
('MV_VT','VT_PRIMARY_FUSE_BLOWN','HV fuse failure','electrical','Fault, surge, or ageing of primary protection fuse','Loss of one or more phase voltages, metering/protection error','high',1),
('MV_VT','VT_WINDING_FAILURE','Winding short or open','electrical','Interturn insulation breakdown, overvoltage stress','Loss of voltage signal, burnt winding, possible fire','critical',2),
('MV_VT','VT_INSULATION_FAILURE','Insulation breakdown','electrical','Moisture ingress, partial discharge, ageing resin','Phase-earth fault, flashover, VT destruction','critical',3),
('MV_VT','VT_SECONDARY_FUSE_BLOWN','Secondary fuse/MCB tripped','electrical','Secondary fault or overload on VT burden','Loss of measured voltage, undervoltage mal-trip','high',4),
('MV_VT','VT_RATIO_ERROR','Ratio or burden error','operational','Wrong tap, excessive burden, mis-recorded ratio','Voltage measurement error, metering inaccuracy','medium',5),
('MV_VT','VT_THERMAL_OVERLOAD','Thermal overload','thermal','Sustained overvoltage or excessive secondary burden','Overheating, accelerated ageing, winding failure','medium',6),
('MV_VT','VT_CONDENSATION','Internal condensation','environmental','Breathing/temperature cycling, failed seal','Reduced insulation, tracking, eventual flashover','medium',7),
-- MV_METERING_POINT — MV Metering Point
('MV_METERING_POINT','MP_NO_READ','No reading obtained','operational','Instrument transformer, wiring, or meter fault upstream','Missing energy data, estimated billing','high',0),
('MV_METERING_POINT','MP_WIRING_ERROR','Metering wiring error','electrical','Incorrect CT/VT connection or phasing at point','Incorrect energy registration, revenue loss','high',1),
('MV_METERING_POINT','MP_PHASING_ERROR','Phase association error','operational','CT/VT cross-phased or mislabelled','Wrong power factor and energy measurement','medium',2),
('MV_METERING_POINT','MP_SEAL_BROKEN','Metrology seal broken','operational','Tamper, unauthorised access, maintenance without reseal','Loss of revenue integrity, disputed readings','high',3),
('MV_METERING_POINT','MP_BURDEN_MISMATCH','Burden out of spec','electrical','Excessive lead length or added devices on CT/VT','Accuracy outside class, metering error','medium',4),
('MV_METERING_POINT','MP_CONFIG_DRIFT','Configuration mismatch','operational','Ratio or register config not matching installed hardware','Systematic billing error','medium',5),
-- MV_METER — MV Meter
('MV_METER','MVM_METER_FAILURE','Meter hardware failure','electrical','Component failure, power-supply fault, board defect','Complete loss of metering, no energy record','high',0),
('MV_METER','MVM_COMMS_LOSS','Communications loss','operational','Modem, SIM, or network fault, antenna failure','No remote reads, manual reading required','medium',1),
('MV_METER','MVM_INCORRECT_REGISTRATION','Incorrect registration','operational','Wrong CT/VT ratio or multiplier programmed','Over/under-billing, revenue error','high',2),
('MV_METER','MVM_TAMPER','Meter tampering','operational','Deliberate manipulation, magnet, bypass','Revenue loss, inaccurate energy record','high',3),
('MV_METER','MVM_CLOCK_DRIFT','Clock or time-sync error','operational','RTC drift, failed time sync, firmware bug','Misallocated time-of-use energy, interval errors','medium',4),
('MV_METER','MVM_FIRMWARE_FAULT','Firmware fault','operational','Corrupt or buggy firmware, failed update','Freeze, reset, erroneous data','medium',5),
('MV_METER','MVM_TERMINAL_CORROSION','Terminal corrosion','environmental','Moisture ingress, dissimilar metals','Intermittent connection, reading errors','low',6),
('MV_METER','MVM_DISPLAY_FAILURE','Display failure','other','LCD/backlight ageing, connector fault','Local reading impossible, meter still records','low',7),
-- MV_METERING_CUBICLE — MV Metering Cubicle
('MV_METERING_CUBICLE','MCUB_MOISTURE_INGRESS','Moisture ingress','environmental','Failed seal/gland, condensation, poor ventilation','Corrosion, tracking, insulation degradation of instruments','high',0),
('MV_METERING_CUBICLE','MCUB_DOOR_SEAL_FAILURE','Door or gasket seal failure','mechanical','Perished gasket, warped door, missing fasteners','Water and dust ingress, loss of IP rating','medium',1),
('MV_METERING_CUBICLE','MCUB_OVERHEAT','Internal overheating','thermal','Blocked ventilation, high load, failed heater/fan','Accelerated ageing of meters and transformers','medium',2),
('MV_METERING_CUBICLE','MCUB_CORROSION','Enclosure corrosion','ageing','Coastal/industrial environment, coating breakdown','Structural weakening, earth-path degradation','medium',3),
('MV_METERING_CUBICLE','MCUB_EARTH_FAULT','Enclosure earthing defect','electrical','Loose or corroded earth bond','Touch-potential hazard, failed protection reference','high',4),
('MV_METERING_CUBICLE','MCUB_ACCESS_OBSTRUCTED','Access obstruction','operational','Vegetation, stored material, parked vehicles','Delayed reading and maintenance access','low',5),
('MV_METERING_CUBICLE','MCUB_VERMIN_INGRESS','Vermin or insect ingress','environmental','Unsealed gland/vent, nesting','Short circuits, flashover, wiring damage','medium',6),
-- NETWORK_METER — Network Meter
('NETWORK_METER','NM_NO_READ','No reading / data gap','operational','Meter, comms, or upstream transformer fault','Energy balance gap, unaccounted-for-energy error','high',0),
('NETWORK_METER','NM_COMMS_LOSS','Communications loss','operational','Network, modem, or backhaul failure','Loss of near-real-time accounting data','medium',1),
('NETWORK_METER','NM_INCORRECT_SCALING','Incorrect scaling factor','operational','Wrong CT/VT ratio or multiplier configured','Systematic energy-accounting error, skewed losses','high',2),
('NETWORK_METER','NM_CLOCK_DRIFT','Time synchronisation error','operational','RTC drift or failed NTP/sync','Interval misalignment, reconciliation errors','medium',3),
('NETWORK_METER','NM_METER_FAILURE','Meter failure','electrical','Hardware or power-supply fault','Complete loss of accounting measurement','high',4),
('NETWORK_METER','NM_FIRMWARE_FAULT','Firmware fault','operational','Corrupt or buggy firmware','Data corruption or meter reset','medium',5),
('NETWORK_METER','NM_WIRING_ERROR','Metering wiring/phasing error','electrical','Cross-phased or mis-wired CT/VT','Erroneous directional energy and loss figures','medium',6),
-- METER — Meter (LV customer)
('METER','MTR_NO_READ','No read / failed reading','operational','Meter fault, comms failure, access denied','Estimated billing, customer dispute','high',0),
('METER','MTR_TAMPER','Meter tampering','operational','Bypass, magnet, reversed connection, broken seal','Revenue loss, unsafe installation','high',1),
('METER','MTR_COMMS_LOSS','Communications loss','operational','PLC/RF/cellular outage, module failure','No remote reads, manual reading required','medium',2),
('METER','MTR_METER_FAILURE','Meter failure','electrical','Component or power-supply failure, surge damage','Loss of supply or metering, no record','high',3),
('METER','MTR_INCORRECT_REGISTRATION','Incorrect registration','operational','Wrong tariff/register or multiplier programmed','Over/under-billing error','medium',4),
('METER','MTR_MOISTURE_CORROSION','Moisture / terminal corrosion','environmental','Water ingress, condensation, poor sealing','Intermittent contact, hot terminals, reading error','medium',5),
('METER','MTR_CLOCK_FIRMWARE','Clock or firmware fault','operational','RTC drift, failed update, firmware bug','Time-of-use misallocation, meter reset','low',6),
('METER','MTR_TERMINAL_OVERHEAT','Terminal overheating','thermal','Loose terminal, overload, poor connection','Burnt terminal block, fire risk, supply loss','high',7),
('METER','MTR_DISPLAY_FAILURE','Display failure','other','LCD ageing or connector fault','Local reading impossible, still records','low',8),
-- METER_CHAMBER — Meter Chamber
('METER_CHAMBER','MCH_FLOODING','Chamber flooding','environmental','Poor drainage, groundwater, broken seal','Submerged meter, corrosion, electrical hazard','high',0),
('METER_CHAMBER','MCH_LID_DAMAGE','Cover or lid damage','mechanical','Traffic loading, vandalism, corrosion of frame','Exposed live equipment, safety and access hazard','high',1),
('METER_CHAMBER','MCH_ACCESS_OBSTRUCTED','Access obstruction','operational','Buried lid, parked vehicle, overgrowth, stored material','Cannot read or maintain meter','medium',2),
('METER_CHAMBER','MCH_SILT_DEBRIS','Silt and debris build-up','environmental','Ingress of dirt, leaves, construction spoil','Blocked chamber, concealed defects, drainage loss','low',3),
('METER_CHAMBER','MCH_STRUCTURAL_COLLAPSE','Structural collapse','mechanical','Deteriorated brick/concrete, ground movement, overload','Damaged meter and cabling, safety hazard','high',4),
('METER_CHAMBER','MCH_VERMIN_INGRESS','Vermin ingress','environmental','Unsealed ducts, open chamber','Cable damage, nesting, short circuits','medium',5),
('METER_CHAMBER','MCH_GAS_ACCUMULATION','Gas accumulation','environmental','Confined space, ingress of sewer/ground gas','Explosive/asphyxiation hazard to personnel','high',6)
ON CONFLICT (feature_class_code, code) DO UPDATE SET
    label = EXCLUDED.label, dimension = EXCLUDED.dimension, typical_cause = EXCLUDED.typical_cause,
    effect = EXCLUDED.effect, severity = EXCLUDED.severity, sort_order = EXCLUDED.sort_order;

-- =====================================================================
-- Conductors, cables, joints & civil routing
-- =====================================================================
INSERT INTO faults.fault_modes (feature_class_code, code, label, dimension, typical_cause, effect, severity, sort_order) VALUES
-- MV_CONDUCTOR — MV Conductor (overhead OR underground)
('MV_CONDUCTOR','CONDUCTOR_DOWN','Conductor down','mechanical','Storm, fallen tree, pole failure or vehicle impact','Line on the ground, phase-earth fault, public safety hazard, outage','critical',0),
('MV_CONDUCTOR','CABLE_DIGIN','Third-party cable dig-in','mechanical','Excavator or auger strike without plant location','Phase or multi-phase fault, outage, risk to excavator operator','critical',1),
('MV_CONDUCTOR','CONDUCTOR_BROKEN','Broken conductor','mechanical','Fatigue, corrosion, annealing or excessive mechanical tension','Open phase or conductor on ground, fault, loss of supply','critical',2),
('MV_CONDUCTOR','TREE_CONTACT','Vegetation contact','environmental','Tree growth or fallen limb bridging to line','Phase-earth fault, flashover, fire ignition risk, outage','high',3),
('MV_CONDUCTOR','INSULATION_BREAKDOWN','Cable insulation breakdown','electrical','Ageing XLPE or paper insulation, overvoltage or thermal stress','Phase-earth or phase-phase fault, cable section failure','critical',4),
('MV_CONDUCTOR','WATER_TREEING','Water treeing','ageing','Moisture ingress into XLPE under electrical stress over years','Progressive insulation degradation leading to breakdown','high',5),
('MV_CONDUCTOR','PARTIAL_DISCHARGE','Partial discharge','electrical','Voids, contamination or defects in cable insulation','Localised erosion of insulation, precursor to failure','medium',6),
('MV_CONDUCTOR','CONDUCTOR_CLASH','Conductor clashing','mechanical','Wind-induced galloping or inadequate phase spacing','Transient phase-phase faults, annealing, momentary outages','medium',7),
('MV_CONDUCTOR','SHEATH_DAMAGE','Cable sheath damage','mechanical','Rodent attack, installation abrasion or ground movement','Moisture ingress path, sheath earth fault, corrosion','medium',8),
('MV_CONDUCTOR','CONDUCTOR_ANNEAL','Conductor annealing','thermal','Sustained overload or fault current beyond thermal rating','Loss of tensile strength, sag increase, future break','high',9),
-- LV_CONDUCTOR — LV Conductor
('LV_CONDUCTOR','CABLE_DIGIN','Third-party cable dig-in','mechanical','Excavation strike without plant location','LV fault, loss of supply, shock risk to operator','high',0),
('LV_CONDUCTOR','CONDUCTOR_DOWN','Conductor down','mechanical','Storm, fallen tree, pole or structure failure','Live conductor on ground, public safety hazard, outage','critical',1),
('LV_CONDUCTOR','BROKEN_NEUTRAL','Broken or lost neutral','electrical','Corroded connector, break or dig-in on the neutral','Voltage imbalance, customer overvoltage, equipment damage','high',2),
('LV_CONDUCTOR','INSULATION_BREAKDOWN','Cable insulation breakdown','electrical','Ageing insulation, overload heating or moisture ingress','Phase-neutral fault, loss of supply to street','high',3),
('LV_CONDUCTOR','ABC_INSULATION_FAIL','ABC insulation failure','ageing','UV degradation and weathering of aerial bundled cable sheath','Tracking, phase-earth fault, nuisance tripping','medium',4),
('LV_CONDUCTOR','TREE_CONTACT','Vegetation contact','environmental','Tree growth against open-wire or ABC run','Abrasion, flashover, fault, outage','medium',5),
('LV_CONDUCTOR','CONNECTOR_OVERHEAT','Connector or clamp overheating','thermal','Loose or corroded LV connection under load','Hot joint, voltage drop, burn-off, local outage','high',6),
('LV_CONDUCTOR','SHEATH_DAMAGE','Cable sheath damage','mechanical','Rodent attack, abrasion or ground movement','Moisture ingress, corrosion, progressive failure','medium',7),
('LV_CONDUCTOR','CONDUCTOR_OVERLOAD','Conductor overload','thermal','Demand growth exceeding cable or line rating','Overheating, insulation ageing, sag or burn-off','medium',8),
-- MV_CABLE_JOINT — MV Cable Joint
('MV_CABLE_JOINT','JOINT_BREAKDOWN','Joint insulation breakdown','electrical','Workmanship defect, stress cone error or ageing','Phase-earth or phase-phase fault, cable outage','critical',0),
('MV_CABLE_JOINT','MOISTURE_INGRESS','Moisture ingress','environmental','Failed seal or chamber flooding entering the joint','Insulation degradation, tracking, eventual breakdown','high',1),
('MV_CABLE_JOINT','WORKMANSHIP_DEFECT','Poor workmanship','operational','Incorrect stripping, contamination or semicon error','Field enhancement, partial discharge, premature failure','high',2),
('MV_CABLE_JOINT','PARTIAL_DISCHARGE','Partial discharge','electrical','Voids or air gaps left during jointing','Progressive insulation erosion, joint failure','high',3),
('MV_CABLE_JOINT','CONNECTOR_OVERHEAT','Conductor connector overheating','thermal','Poor crimp or loose ferrule under load','Hot joint, thermal runaway, burn-out','high',4),
('MV_CABLE_JOINT','TRANSITION_FAIL','Paper-to-polymer transition failure','ageing','Oil migration or dissimilar insulation stress at transition joint','Dry-out, breakdown at the transition, outage','medium',5),
('MV_CABLE_JOINT','SHEATH_BOND_FAIL','Sheath or earth bond failure','electrical','Broken screen continuity or bonding lead','Loss of screen earth, induced voltage, fault escalation','medium',6),
('MV_CABLE_JOINT','MECHANICAL_STRESS','Mechanical stress or movement','mechanical','Ground settlement or thermal cycling pulling the joint','Conductor pull-out, insulation cracking, fault','medium',7),
-- LV_CABLE_JOINT — LV Cable Joint
('LV_CABLE_JOINT','JOINT_FAILURE','Joint failure','electrical','Workmanship defect or ageing of resin or wrap joint','Phase-neutral fault, loss of supply','high',0),
('LV_CABLE_JOINT','MOISTURE_INGRESS','Moisture ingress','environmental','Failed seal, resin void or chamber flooding','Corrosion, tracking, phase-earth fault','high',1),
('LV_CABLE_JOINT','CONNECTOR_OVERHEAT','Connector overheating','thermal','Loose or corroded crimp under load','Hot joint, voltage drop, burn-off, local outage','high',2),
('LV_CABLE_JOINT','NEUTRAL_FAIL','Neutral connection failure','electrical','Corroded or broken neutral within the joint','Voltage imbalance, customer overvoltage','high',3),
('LV_CABLE_JOINT','WORKMANSHIP_DEFECT','Poor workmanship','operational','Incomplete resin fill or incorrect connector','Early-life joint failure, intermittent faults','medium',4),
('LV_CABLE_JOINT','MECHANICAL_STRESS','Mechanical stress or movement','mechanical','Ground movement or thermal cycling','Conductor pull-out, cracked seal, ingress path','medium',5),
('LV_CABLE_JOINT','CORROSION','Conductor corrosion','ageing','Dissimilar-metal or moisture-driven corrosion','High-resistance joint, heating, open circuit','medium',6),
-- CABLE_TRENCH — Cable Trench
('CABLE_TRENCH','TRENCH_COLLAPSE','Trench wall collapse','mechanical','Poor shoring, saturated ground or surcharge load','Cable damage, exposure, safety hazard to crews','high',0),
('CABLE_TRENCH','DIGIN_EXPOSURE','Third-party dig-in exposure','mechanical','Excavation over unrecorded or shallow trench','Cable strike or exposure, fault, outage','high',1),
('CABLE_TRENCH','SUBSIDENCE','Ground subsidence','environmental','Settlement, washout or nearby excavation','Cable strain, bedding loss, mechanical damage','medium',2),
('CABLE_TRENCH','FLOODING','Trench flooding','environmental','Groundwater or surface water ingress','Standing water around cables, corrosion acceleration','medium',3),
('CABLE_TRENCH','BEDDING_LOSS','Loss of bedding or backfill','mechanical','Washout, poor reinstatement or settlement','Direct cable-to-stone contact, sheath abrasion','medium',4),
('CABLE_TRENCH','MISSING_PROTECTION','Missing tiles or marker tape','operational','Omitted protective cover or warning tape at install','Reduced dig-in warning, higher strike risk','medium',5),
('CABLE_TRENCH','THERMAL_BACKFILL','Poor thermal backfill','thermal','Incorrect backfill material raising thermal resistivity','Reduced cable rating, overheating under load','medium',6),
('CABLE_TRENCH','ROOT_INTRUSION','Root intrusion','environmental','Tree roots growing into the trench','Cable displacement, sheath pressure damage','low',7),
-- CABLE_DUCT — Cable Duct
('CABLE_DUCT','DUCT_BLOCKAGE','Duct blockage','operational','Silt, debris, collapsed liner or intruded roots','Cannot pull or replace cable, delays, rework','high',0),
('CABLE_DUCT','DUCT_COLLAPSE','Duct collapse','mechanical','Crushing load, poor bedding or material failure','Cable pinch damage, blocked route, fault','high',1),
('CABLE_DUCT','FLOODING','Duct flooding','environmental','Groundwater ingress or failed drainage','Water around cable, corrosion, ingress at joints','medium',2),
('CABLE_DUCT','DUCT_DEFORMATION','Duct deformation or ovality','mechanical','Ground movement or excessive cover load','Pulling difficulty, cable sheath abrasion','medium',3),
('CABLE_DUCT','SEAL_FAILURE','Duct seal failure','environmental','Degraded or missing end seals at chambers','Water and gas migration into chambers or substations','medium',4),
('CABLE_DUCT','SHARP_EDGE','Sharp edge or burr at entry','mechanical','Damaged duct mouth or missing bellmouth','Cable sheath cutting during pull, insulation damage','medium',5),
('CABLE_DUCT','ROOT_INTRUSION','Root intrusion','environmental','Tree roots entering joints in the duct run','Partial blockage, cable pressure, pulling obstruction','low',6),
('CABLE_DUCT','OVERHEAT_GROUPING','Thermal grouping overheat','thermal','Too many loaded cables sharing one duct','Mutual heating, derating, insulation ageing','medium',7),
-- CABLE_CHAMBER — Cable Chamber / manhole
('CABLE_CHAMBER','FLOODING','Chamber flooding','environmental','Groundwater ingress or blocked drainage sump','Submerged joints, corrosion, access and safety issues','high',0),
('CABLE_CHAMBER','COVER_DEFECT','Cover or frame defect','mechanical','Cracked, missing or displaced access cover','Trip or fall hazard, unauthorised access, ingress','high',1),
('CABLE_CHAMBER','STRUCTURAL_FAILURE','Structural wall or roof failure','mechanical','Concrete spalling, reinforcement corrosion or overload','Collapse risk, cable damage, unsafe access','high',2),
('CABLE_CHAMBER','GAS_ACCUMULATION','Hazardous gas accumulation','environmental','Ingress of sewer or ground gas into confined space','Explosion or asphyxiation risk to entrants','critical',3),
('CABLE_CHAMBER','BEARER_CORROSION','Cable bearer or cleat corrosion','ageing','Damp environment corroding support steelwork','Loss of cable support, sagging, mechanical stress','medium',4),
('CABLE_CHAMBER','DEBRIS_SILT','Silt and debris build-up','operational','Washed-in sediment and refuse over time','Blocked sump, standing water, restricted access','medium',5),
('CABLE_CHAMBER','DUCT_SEAL_FAIL','Duct entry seal failure','environmental','Degraded seals at chamber duct entries','Water migration between chamber and ducts','medium',6),
('CABLE_CHAMBER','ROOT_INTRUSION','Root intrusion','environmental','Tree roots entering through joints or cracks','Structural cracking, displacement of cables','low',7),
-- DUCT_WAY — Duct Way (route)
('DUCT_WAY','ROUTE_BLOCKAGE','Route blockage','operational','Blocked or collapsed ducts along the way','No spare capacity, cannot install new cable','high',0),
('DUCT_WAY','CAPACITY_EXHAUSTED','Duct capacity exhausted','operational','All ways occupied with no spare for reinforcement','Reinforcement delays, forced new civils','medium',1),
('DUCT_WAY','RECORD_MISMATCH','Record or as-built mismatch','operational','Inaccurate route records versus ground truth','Mis-location, dig-in risk, planning errors','high',2),
('DUCT_WAY','SUBSIDENCE','Route subsidence','environmental','Ground settlement along the duct corridor','Duct deformation, cable strain over the length','medium',3),
('DUCT_WAY','FLOODING','Route flooding','environmental','Water table or ingress affecting the whole route','Standing water through ducts, corrosion','medium',4),
('DUCT_WAY','THIRD_PARTY_CROSSING','Unmanaged third-party crossing','operational','New services crossing or disturbing the route','Damage risk, loss of cover, future dig-in','medium',5),
('DUCT_WAY','SEAL_FAILURE','Route seal failure','environmental','Degraded seals at route ends','Water and gas migration along the corridor','low',6)
ON CONFLICT (feature_class_code, code) DO UPDATE SET
    label = EXCLUDED.label, dimension = EXCLUDED.dimension, typical_cause = EXCLUDED.typical_cause,
    effect = EXCLUDED.effect, severity = EXCLUDED.severity, sort_order = EXCLUDED.sort_order;

-- =====================================================================
-- LV distribution gear, DER, storage, compensation & EV
-- =====================================================================
INSERT INTO faults.fault_modes (feature_class_code, code, label, dimension, typical_cause, effect, severity, sort_order) VALUES
-- SERVICE_POINT — Service Point
('SERVICE_POINT','LOOSE_SERVICE_CONNECTION','Loose service connection','mechanical','Poorly torqued or vibrated-loose service terminal','Intermittent supply, local heating, flicker','high',0),
('SERVICE_POINT','BURNT_SERVICE_TERMINATION','Burnt service termination','thermal','High-resistance joint overheating under load','Melted insulation, supply loss, fire risk','critical',1),
('SERVICE_POINT','SERVICE_FUSE_BLOWN','Service fuse operated','electrical','Downstream fault or sustained overload','Loss of customer supply','high',2),
('SERVICE_POINT','OPEN_SERVICE_CONDUCTOR','Open service conductor','mechanical','Broken or corroded service cable/neutral','Loss of supply or dangerous open neutral','critical',3),
('SERVICE_POINT','OPEN_NEUTRAL','Open service neutral','electrical','Failed neutral connection at service','Overvoltage on phases, equipment damage','critical',4),
('SERVICE_POINT','WATER_INGRESS_TERMINATION','Water ingress at termination','environmental','Failed sealing on service head or box','Tracking, corrosion, earth fault','medium',5),
('SERVICE_POINT','METER_TAMPER_OR_DAMAGE','Meter tamper or damage','operational','Tampering, impact or weathering','Metering error, exposed live parts','medium',6),
('SERVICE_POINT','PHASE_LOSS_SINGLE_PHASING','Single phasing','electrical','Loss of one phase upstream','Motor stall, voltage imbalance at premises','high',7),
-- STREET_LIGHT — Street Light
('STREET_LIGHT','LAMP_FAILURE','Lamp or LED module failure','ageing','End-of-life lamp or LED array degradation','Dark luminaire, reduced road lighting','medium',0),
('STREET_LIGHT','DRIVER_BALLAST_FAILURE','Driver or ballast failure','electrical','Electronic driver/ballast breakdown','Lamp will not strike, flicker or dark','medium',1),
('STREET_LIGHT','PHOTOCELL_FAILURE','Photocell failure','electrical','Failed photocontrol stuck on or off','Day burning or lamp never lights','medium',2),
('STREET_LIGHT','CABLING_FAULT','Supply cabling fault','electrical','Damaged or corroded feed/fuse carrier','Dark luminaire, earth fault','high',3),
('STREET_LIGHT','COLUMN_CORROSION','Column base corrosion','ageing','Base corrosion from moisture and road salt','Structural weakness, collapse risk','high',4),
('STREET_LIGHT','COLUMN_IMPACT_DAMAGE','Column impact damage','mechanical','Vehicle collision with column','Leaning/down column, exposed live cables','critical',5),
('STREET_LIGHT','WATER_INGRESS_DOOR','Water ingress at door','environmental','Missing door or failed seal at base','Corroded cutout, earth fault, shock risk','medium',6),
('STREET_LIGHT','FUSE_OPERATED','Cutout fuse operated','electrical','Downstream fault in luminaire or cable','Dark luminaire','low',7),
-- LV_PILLAR — LV Feeder Pillar
('LV_PILLAR','FUSE_OPERATED','Feeder fuse operated','electrical','Downstream LV fault or overload','Loss of feeder supply','high',0),
('LV_PILLAR','BUSBAR_OVERHEAT','Busbar connection overheat','thermal','Loose or corroded busbar/link joint','Hotspot, melted links, supply loss','critical',1),
('LV_PILLAR','CORROSION','Internal corrosion','ageing','Moisture and condensation over service life','Degraded connections, earth faults','medium',2),
('LV_PILLAR','WATER_INGRESS','Water ingress','environmental','Failed seals or flooding of pillar base','Tracking, corrosion, earth fault','high',3),
('LV_PILLAR','VANDALISM','Vandalism or unauthorised access','operational','Forced door or damaged enclosure','Exposed live parts, public safety risk','high',4),
('LV_PILLAR','LOOSE_CONNECTION','Loose internal connection','mechanical','Under-torqued or vibrated-loose terminal','Intermittent supply, local heating','high',5),
('LV_PILLAR','PHASE_IMBALANCE','Severe phase imbalance','electrical','Uneven load distribution across phases','Neutral overload, voltage spread','medium',6),
('LV_PILLAR','INCOMER_FAILURE','Incomer cable/joint failure','electrical','Failed incoming cable termination or joint','Total loss of pillar supply','critical',7),
-- LVAC_BOARD — Station LVAC Board
('LVAC_BOARD','SUPPLY_LOSS','Loss of LVAC supply','electrical','Failure of station auxiliary AC source','Loss of station auxiliaries and controls','critical',0),
('LVAC_BOARD','MCB_TRIP','Auxiliary breaker trip','electrical','Downstream auxiliary fault or overload','Loss of affected auxiliary circuit','high',1),
('LVAC_BOARD','BUSBAR_OVERHEAT','Busbar overheat','thermal','Loose or corroded busbar joint','Hotspot, board failure','high',2),
('LVAC_BOARD','AUTO_CHANGEOVER_FAILURE','Auto changeover failure','operational','Failed ATS between normal and standby supply','No transfer on supply loss, dead auxiliaries','critical',3),
('LVAC_BOARD','WATER_INGRESS','Water ingress','environmental','Condensation or roof/seal leak into board','Tracking, corrosion, earth fault','medium',4),
('LVAC_BOARD','LOOSE_CONNECTION','Loose connection','mechanical','Under-torqued or vibrated terminal','Intermittent auxiliary supply, heating','medium',5),
('LVAC_BOARD','EARTH_FAULT','Earth fault on auxiliary circuit','electrical','Insulation breakdown in auxiliary wiring','Protection trip, loss of auxiliaries','high',6),
-- GENERATOR — Generator
('GENERATOR','FAIL_TO_START','Fail to start','operational','Flat start battery, control or fuel fault','No standby supply on demand','critical',0),
('GENERATOR','FUEL_SYSTEM_FAULT','Fuel system fault','mechanical','Empty tank, blocked filter, air in fuel','Shutdown or failure to run under load','high',1),
('GENERATOR','GOVERNOR_FAULT','Governor/speed control fault','mechanical','Governor malfunction or actuator failure','Frequency instability, overspeed trip','high',2),
('GENERATOR','AVR_FAILURE','AVR failure','electrical','Automatic voltage regulator breakdown','Over/under voltage, excitation loss','high',3),
('GENERATOR','OVERHEAT','Engine overheat','thermal','Coolant loss, blocked radiator, fan failure','High-temperature shutdown','high',4),
('GENERATOR','WINDING_FAULT','Alternator winding fault','electrical','Insulation breakdown in stator/rotor','Earth/phase fault, protection trip','critical',5),
('GENERATOR','BEARING_FAILURE','Bearing failure','mechanical','Worn or poorly lubricated bearing','Vibration, seizure, forced outage','high',6),
('GENERATOR','REVERSE_POWER','Reverse power','electrical','Prime mover loss while paralleled','Motoring, reverse-power trip','medium',7),
('GENERATOR','SYNC_FAILURE','Synchronisation failure','operational','Faulty sync check or out-of-phase closure','Failed paralleling, shaft shock','high',8),
-- MV_BESS — MV Battery Energy Storage
('MV_BESS','CELL_FAILURE','Battery cell failure','ageing','Degraded or shorted cell in module','Reduced capacity, string isolation','high',0),
('MV_BESS','THERMAL_RUNAWAY','Thermal runaway','thermal','Internal cell short or overcharge','Fire, cascading module failure','critical',1),
('MV_BESS','BMS_FAILURE','BMS failure','electrical','Battery management system fault','Loss of cell protection, mis-operation','critical',2),
('MV_BESS','INVERTER_PCS_FAULT','Inverter/PCS fault','electrical','Power conversion system IGBT or control fault','Loss of charge/discharge capability','high',3),
('MV_BESS','COOLING_FAILURE','Cooling system failure','thermal','HVAC or liquid-cooling pump/fan failure','Overtemperature derate or trip','high',4),
('MV_BESS','DC_ARC_FAULT','DC arc fault','electrical','Loose DC connection or insulation failure','Arc, fire, string damage','critical',5),
('MV_BESS','SOC_IMBALANCE','State-of-charge imbalance','operational','Failed cell balancing across strings','Reduced usable capacity, premature trip','medium',6),
('MV_BESS','GROUND_FAULT','DC ground fault','electrical','Insulation degradation to enclosure','Ground-fault trip, shock risk','high',7),
('MV_BESS','GAS_DETECTION_ALARM','Off-gas / fire detection alarm','environmental','Cell venting detected by gas sensors','Protective shutdown, evacuation','critical',8),
-- MV_CAP_BANK — MV Capacitor Bank
('MV_CAP_BANK','CAPACITOR_UNIT_FAILURE','Capacitor unit failure','electrical','Dielectric breakdown or ageing of a can','Unbalance trip, loss of VAr support','high',0),
('MV_CAP_BANK','UNIT_FUSE_OPERATED','Unit fuse operated','electrical','Failed capacitor can clears its fuse','Unbalance, derated bank output','medium',1),
('MV_CAP_BANK','UNBALANCE_TRIP','Unbalance protection trip','electrical','Multiple failed units shifting neutral current','Bank trip, loss of reactive support','high',2),
('MV_CAP_BANK','SWITCH_FAILURE','Switching device failure','mechanical','Failed vacuum switch or contactor','Cannot switch bank in/out','high',3),
('MV_CAP_BANK','CAN_BULGE_LEAK','Can bulging or dielectric leak','ageing','Internal pressure rise from degradation','Impending rupture, oil/fluid leak','medium',4),
('MV_CAP_BANK','CONTROL_MALFUNCTION','Control malfunction','operational','Faulty VAr/voltage/time controller','Hunting or failure to switch','medium',5),
('MV_CAP_BANK','BUSHING_FLASHOVER','Bushing flashover','environmental','Pollution or moisture on bushing','Phase fault, bank trip','high',6),
('MV_CAP_BANK','INRUSH_DAMAGE','Inrush/transient damage','electrical','High inrush on energisation without reactor','Stressed units, reduced life','medium',7),
-- MV_VREG — MV Voltage Regulator
('MV_VREG','TAP_CHANGER_MECH_FAILURE','Tap changer mechanism failure','mechanical','Worn or jammed tap-changer drive','Stuck tap, loss of voltage control','high',0),
('MV_VREG','CONTROL_FAILURE','Regulator control failure','electrical','Faulty voltage control relay/electronics','No or wrong tap response, voltage error','high',1),
('MV_VREG','CONTACT_WEAR_COKING','Contact wear or coking','ageing','Arcing erosion and oil coking on contacts','Overheating, high resistance, failure','high',2),
('MV_VREG','BYPASS_SWITCH_FAILURE','Bypass switch failure','operational','Failure to bypass before maintenance','Inability to isolate, backfeed risk','critical',3),
('MV_VREG','OIL_DEGRADATION','Insulating oil degradation','ageing','Moisture or breakdown of insulating oil','Reduced dielectric strength, flashover','medium',4),
('MV_VREG','RUNAWAY_HUNTING','Tap hunting/runaway','operational','Mis-set bandwidth or control instability','Excessive tap operations, wear','medium',5),
('MV_VREG','WINDING_FAULT','Winding/insulation fault','electrical','Insulation breakdown in regulating winding','Internal fault, protection trip','critical',6),
('MV_VREG','OVERTEMPERATURE','Overtemperature','thermal','Overload or cooling degradation','Thermal trip, accelerated ageing','medium',7),
-- EV_CHARGING_STATION — EV Charging Station
('EV_CHARGING_STATION','SUPPLY_LOSS','Loss of station supply','electrical','Upstream LV fault or isolation','All chargers offline','high',0),
('EV_CHARGING_STATION','EARTH_FAULT','Station earth fault','electrical','Insulation failure in station wiring','RCD/protection trip, shock risk','high',1),
('EV_CHARGING_STATION','COMMS_FAILURE','Back-office comms failure','operational','Network/OCPP link loss','No authorisation or billing, idle station','medium',2),
('EV_CHARGING_STATION','VANDALISM','Vandalism or damage','operational','Deliberate damage to station equipment','Out of service, exposed parts','medium',3),
('EV_CHARGING_STATION','WATER_INGRESS','Water ingress','environmental','Failed enclosure seal or flooding','Corrosion, earth fault, outage','medium',4),
('EV_CHARGING_STATION','OVERLOAD_TRIP','Station overload trip','electrical','Simultaneous charging beyond capacity','Protection trip, loss of charging','medium',5),
('EV_CHARGING_STATION','LOOSE_CONNECTION','Loose supply connection','mechanical','Under-torqued incomer terminal','Heating, intermittent supply','high',6),
-- EV_CHARGER — EV Charger
('EV_CHARGER','POWER_ELECTRONICS_FAILURE','Power electronics failure','electrical','Rectifier/converter module breakdown','Charger offline, no output','high',0),
('EV_CHARGER','OVERHEATING','Charger overheating','thermal','Cooling fan failure or blocked vents','Thermal derate or shutdown','high',1),
('EV_CHARGER','COMMS_FAULT','Communication fault','operational','OCPP/vehicle comms failure','Session failure, charger unavailable','medium',2),
('EV_CHARGER','EARTH_FAULT','Earth fault','electrical','Insulation breakdown inside charger','RCD trip, shock risk','high',3),
('EV_CHARGER','CONTACTOR_FAILURE','Output contactor failure','mechanical','Welded or failed charging contactor','Cannot start/stop charge safely','high',4),
('EV_CHARGER','FIRMWARE_FAULT','Firmware/control fault','operational','Software hang or failed update','Charger lockup, requires reset','medium',5),
('EV_CHARGER','GROUND_MONITOR_FAULT','Ground monitor/IMD fault','electrical','Isolation monitor device malfunction','Nuisance lockout or missed fault','medium',6),
('EV_CHARGER','OVERCURRENT_TRIP','Overcurrent trip','electrical','Vehicle or cable fault during charge','Charge interrupted','medium',7),
-- EV_CONNECTOR — EV Connector
('EV_CONNECTOR','CONTACT_WEAR','Connector contact wear','ageing','Repeated mating cycles eroding contacts','High resistance, heating, derate','high',0),
('EV_CONNECTOR','OVERHEATING','Connector overheating','thermal','High-resistance or loose pin under load','Derate, melted housing, fire risk','critical',1),
('EV_CONNECTOR','PIN_DAMAGE','Pin or contact damage','mechanical','Bent/broken pin from misuse or impact','Failed connection, charge fault','high',2),
('EV_CONNECTOR','CABLE_DAMAGE','Charging cable damage','mechanical','Abrasion, crushing or vehicle drive-off','Exposed conductors, earth fault','high',3),
('EV_CONNECTOR','LATCH_FAILURE','Latch/lock failure','mechanical','Worn or broken connector latch','Connector disengages mid-charge','medium',4),
('EV_CONNECTOR','WATER_INGRESS','Water ingress in connector','environmental','Failed seal or holster exposure','Corrosion, earth fault','medium',5),
('EV_CONNECTOR','PROXIMITY_PILOT_FAULT','Proximity/control pilot fault','electrical','Damaged CP/PP contact or wiring','Vehicle will not initiate charge','medium',6),
('EV_CONNECTOR','VANDALISM','Connector vandalism/theft','operational','Deliberate cutting or theft of cable','Out of service, exposed conductors','high',7)
ON CONFLICT (feature_class_code, code) DO UPDATE SET
    label = EXCLUDED.label, dimension = EXCLUDED.dimension, typical_cause = EXCLUDED.typical_cause,
    effect = EXCLUDED.effect, severity = EXCLUDED.severity, sort_order = EXCLUDED.sort_order;

-- =====================================================================
-- Station DC & control, panels, containers, structures & civil
-- =====================================================================
INSERT INTO faults.fault_modes (feature_class_code, code, label, dimension, typical_cause, effect, severity, sort_order) VALUES
-- STATION_BATTERY — Station Battery
('STATION_BATTERY','CELL_OPEN_CIRCUIT','Open-circuit / failed cell','electrical','Internal plate corrosion, dry-out or terminal fracture','Loss of DC supply to protection and control, trip/close failure','critical',0),
('STATION_BATTERY','LOW_TERMINAL_VOLTAGE','Low terminal voltage','electrical','Undercharge, ageing or excessive standing load','Insufficient DC for breaker operation and relays','critical',1),
('STATION_BATTERY','SULPHATION','Plate sulphation','ageing','Prolonged undercharge or standing at low state of charge','Reduced capacity and loss of autonomy during AC loss','high',2),
('STATION_BATTERY','ELECTROLYTE_LOW','Low electrolyte level','environmental','Overcharge gassing, water loss or lack of topping-up','Plate exposure, capacity loss and cell damage','high',3),
('STATION_BATTERY','THERMAL_RUNAWAY','Thermal runaway','thermal','Overcharge with high ambient temperature on VRLA cells','Cell venting, swelling, fire risk and bank destruction','critical',4),
('STATION_BATTERY','CAPACITY_FADE','End-of-life capacity fade','ageing','Normal ageing beyond design life','Fails discharge/autonomy test, inadequate backup time','medium',5),
('STATION_BATTERY','POST_CORROSION','Terminal post corrosion','environmental','Electrolyte leakage, humidity or poor connection sealing','High resistance joint, voltage drop and local heating','medium',6),
('STATION_BATTERY','CASE_LEAK','Case cracking / electrolyte leak','mechanical','Case ageing, overpressure or physical damage','Acid spill, corrosion of rack and safety hazard','high',7),
-- BATTERY_CHARGER — Battery Charger
('BATTERY_CHARGER','OUTPUT_LOSS','Total output loss','electrical','Rectifier module failure, blown fuse or AC supply loss','Battery slowly discharges, eventual DC system collapse','critical',0),
('BATTERY_CHARGER','RECTIFIER_FAULT','Rectifier / thyristor failure','electrical','Semiconductor failure or control board fault','Loss or instability of DC output, charging stops','high',1),
('BATTERY_CHARGER','DC_OVERVOLTAGE','DC overvoltage','electrical','Voltage regulation failure or sensing fault','Battery overcharge, gassing and connected-load damage','high',2),
('BATTERY_CHARGER','AC_SUPPLY_LOSS','AC input supply loss','electrical','Auxiliary AC feeder trip or upstream outage','Charger offline, battery on standing load only','high',3),
('BATTERY_CHARGER','RIPPLE_HIGH','Excessive DC ripple','electrical','Filter capacitor degradation or faulty rectifier leg','Noise on protection circuits, reduced battery life','medium',4),
('BATTERY_CHARGER','OVERTEMP','Charger overheating','thermal','Fan failure, blocked ventilation or high ambient','Thermal derate or shutdown, reduced output','medium',5),
('BATTERY_CHARGER','ALARM_FAILURE','Alarm / annunciation failure','operational','Faulty relay card or broken wiring to SCADA','Fault conditions go unnoticed until DC failure','medium',6),
('BATTERY_CHARGER','EARTH_FAULT','DC earth fault on charger','electrical','Insulation breakdown or moisture ingress','Spurious protection operation, nuisance alarms','medium',7),
-- SUBSTATION_PANEL — Substation Panel
('SUBSTATION_PANEL','WIRING_FAULT','Internal wiring / insulation fault','electrical','Chafed insulation, rodent damage or ageing wiring','Short, open circuit or misoperation of protection','high',0),
('SUBSTATION_PANEL','TERMINAL_LOOSE','Loose terminal connection','mechanical','Vibration, thermal cycling or poor workmanship','Intermittent signal, high resistance and local heating','high',1),
('SUBSTATION_PANEL','DC_SUPPLY_LOSS','Loss of control supply to panel','electrical','Tripped MCB, blown fuse or DC distribution fault','Protection and control inoperative for the bay','critical',2),
('SUBSTATION_PANEL','RELAY_MALFUNCTION','Protection relay malfunction','electrical','Firmware fault, setting error or component failure','Failure to trip or spurious trip','critical',3),
('SUBSTATION_PANEL','ALARM_ANNUNCIATOR_FAIL','Alarm / annunciator failure','operational','Lamp, card or scheme wiring failure','Alarms not raised, operator blind to conditions','medium',4),
('SUBSTATION_PANEL','CONDENSATION','Condensation inside panel','environmental','Temperature swings, poor heater or seal failure','Corrosion and insulation degradation of components','medium',5),
('SUBSTATION_PANEL','VERMIN_INGRESS','Vermin / insect ingress','environmental','Unsealed cable entries and glands','Short circuits, nuisance trips and equipment damage','medium',6),
('SUBSTATION_PANEL','OVERHEAT','Panel internal overheating','thermal','Blocked ventilation or heater stuck on','Accelerated ageing of electronics','low',7),
-- MV_SWITCHBOARD — MV Switchboard
('MV_SWITCHBOARD','ENCLOSURE_FLOODING','Enclosure flooding / water ingress','environmental','Failed seals, blocked drains or external flooding','Insulation failure, flashover and multi-feeder outage','critical',0),
('MV_SWITCHBOARD','CONDENSATION','Internal condensation','environmental','Temperature cycling with inadequate heating','Surface tracking and corrosion on busbars','high',1),
('MV_SWITCHBOARD','BUSBAR_FLASHOVER','Busbar common-mode flashover','electrical','Contamination, condensation or vermin bridging phases','Busbar fault, loss of entire switchboard','critical',2),
('MV_SWITCHBOARD','VERMIN_INGRESS','Vermin ingress','environmental','Unsealed cable trenches and gland plates','Phase-to-phase fault and board outage','high',3),
('MV_SWITCHBOARD','DOOR_SEAL_FAILURE','Door / gasket seal failure','mechanical','Perished gaskets or warped doors','Loss of ingress protection, dust and moisture entry','medium',4),
('MV_SWITCHBOARD','CORROSION','Enclosure / frame corrosion','ageing','Humidity, coastal salt or coating breakdown','Structural weakening and reduced ingress protection','medium',5),
('MV_SWITCHBOARD','OVERHEAT_VENTILATION','Internal overheating','thermal','Blocked vents or failed forced ventilation','Thermal ageing of insulation and joints','medium',6),
('MV_SWITCHBOARD','ACCESS_OBSTRUCTION','Access / interlock obstruction','operational','Seized interlocks, corrosion or debris','Delayed switching and restricted maintenance','low',7),
-- MV_SWITCHGEAR_PANEL — MV Switchgear Panel
('MV_SWITCHGEAR_PANEL','WATER_INGRESS','Water / moisture ingress','environmental','Failed gaskets, roof leak or cable-trench water','Insulation breakdown and panel flashover','critical',0),
('MV_SWITCHGEAR_PANEL','CONDENSATION','Internal condensation','environmental','Poor heating and temperature swings','Tracking and corrosion on live parts','high',1),
('MV_SWITCHGEAR_PANEL','COMPARTMENT_FLASHOVER','Compartment flashover','electrical','Contamination, vermin or condensation bridging','Internal arc, panel loss and arc-flash hazard','critical',2),
('MV_SWITCHGEAR_PANEL','VERMIN_INGRESS','Vermin ingress','environmental','Unsealed cable entries','Phase bridging and nuisance faults','high',3),
('MV_SWITCHGEAR_PANEL','SEAL_DEGRADATION','Gasket / seal degradation','ageing','UV and thermal ageing of seals','Loss of IP rating and ingress of contaminants','medium',4),
('MV_SWITCHGEAR_PANEL','CORROSION','Panel corrosion','ageing','Humidity or coastal exposure','Frame weakening and earth-continuity loss','medium',5),
('MV_SWITCHGEAR_PANEL','INTERLOCK_SEIZURE','Mechanical interlock seizure','mechanical','Corrosion, dirt or lack of lubrication','Unable to operate or rack safely','medium',6),
('MV_SWITCHGEAR_PANEL','HEATER_FAILURE','Anti-condensation heater failure','operational','Failed element or supply loss','Condensation buildup over time','low',7),
-- BAY — Bay
('BAY','COMMON_DC_SUPPLY_LOSS','Common DC supply loss to bay','electrical','Shared DC distribution fault or tripped MCB','Loss of protection and control for all bay equipment','critical',0),
('BAY','COMMON_EARTH_FAULT','Bay earthing / bonding fault','electrical','Corroded bond, theft or broken earth connection','Loss of safe earth reference, touch-potential hazard','high',1),
('BAY','CABLE_TRENCH_FLOODING','Cable trench / pit flooding','environmental','Blocked drainage or ingress of ground water','Cable insulation damage and common-mode outage','high',2),
('BAY','AUX_SUPPLY_LOSS','Auxiliary AC supply loss','electrical','Aux transformer or feeder fault','Loss of heating, cooling and charging for the bay','high',3),
('BAY','MARSHALLING_WIRING_FAULT','Marshalling / interconnection wiring fault','electrical','Rodent, ageing or damaged inter-panel wiring','Cross-bay signal loss and misoperation','medium',4),
('BAY','ACCESS_RESTRICTION','Bay access restriction','operational','Vegetation, debris or locked/seized barriers','Delayed operation and maintenance','medium',5),
('BAY','VERMIN_NESTING','Vermin nesting in bay','environmental','Unsealed common cable routes and voids','Multiple equipment faults and fire risk','medium',6),
-- RMU — Ring Main Unit
('RMU','SF6_LEAK','SF6 gas leak / low pressure','environmental','Seal failure, tank weld defect or valve leak','Loss of insulation and interrupting capability','critical',0),
('RMU','TANK_CORROSION','Gas tank / enclosure corrosion','ageing','Ground moisture, coastal salt or coating failure','Tank perforation, SF6 loss and water ingress','high',1),
('RMU','SWITCH_MECH_SEIZURE','Switch mechanism seizure','mechanical','Lack of lubrication, corrosion or ageing','Failure to open/close, delayed isolation','high',2),
('RMU','PLINTH_FLOODING','Plinth / chamber flooding','environmental','Blocked drainage or below-grade water ingress','Cable box flooding and insulation failure','high',3),
('RMU','FUSE_FAIL_TO_OPERATE','Switch-fuse fail to operate','mechanical','Corroded striker or jammed trip linkage','Downstream fault not cleared, upstream trip','high',4),
('RMU','CABLE_BOX_INGRESS','Cable box moisture ingress','environmental','Failed gland seals or heat-shrink defects','Termination tracking and cable-box fault','medium',5),
('RMU','PRESSURE_RELIEF_CORROSION','Pressure-relief / bursting disc corrosion','ageing','Environmental corrosion of relief path','Impaired fault venting, overpressure risk','medium',6),
('RMU','OPERATING_HANDLE_ACCESS','Operating access obstruction','operational','Overgrowth, parked vehicles or seized access','Delayed network switching','low',7),
-- PACKAGED_SUBSTATION — Packaged Substation
('PACKAGED_SUBSTATION','ENCLOSURE_FLOODING','Enclosure flooding','environmental','Site flooding, blocked drains or seal failure','Insulation failure and total unit outage','critical',0),
('PACKAGED_SUBSTATION','VENTILATION_BLOCKAGE','Cooling / ventilation blockage','thermal','Blocked louvres, debris or failed fans','Transformer overheating and forced derating','high',1),
('PACKAGED_SUBSTATION','DOOR_SEAL_FAILURE','Door / seal failure','mechanical','Perished gaskets or forced/damaged doors','Loss of IP rating, water and vermin ingress','medium',2),
('PACKAGED_SUBSTATION','CORROSION','Enclosure corrosion','ageing','Coastal salt, humidity or coating breakdown','Structural weakening and water ingress','medium',3),
('PACKAGED_SUBSTATION','VERMIN_INGRESS','Vermin ingress','environmental','Unsealed cable entries and vents','Internal faults and fire risk','high',4),
('PACKAGED_SUBSTATION','VEHICLE_IMPACT','Vehicle impact on unit','mechanical','Collision at roadside or yard location','Enclosure breach and equipment damage','high',5),
('PACKAGED_SUBSTATION','CONDENSATION','Internal condensation','environmental','Temperature cycling and poor heating','Corrosion and tracking on internal equipment','medium',6),
('PACKAGED_SUBSTATION','SECURITY_BREACH','Security breach / vandalism','operational','Forced entry, theft or tampering','Equipment theft, damage and safety hazard','high',7),
-- SUBSTATION — Substation (site)
('SUBSTATION','SITE_FLOODING','Site flooding / inundation','environmental','Heavy rainfall, poor drainage or watercourse breach','Widespread equipment damage and major outage','critical',0),
('SUBSTATION','EARTH_GRID_FAILURE','Earth grid degradation / theft','electrical','Corrosion, conductor theft or poor connections','Loss of safe earthing, step/touch-potential hazard','critical',1),
('SUBSTATION','FENCE_SECURITY_BREACH','Perimeter fence / security breach','operational','Vandalism, theft or storm damage to fence','Unauthorised access, theft and safety risk','high',2),
('SUBSTATION','FIRE','Substation fire','thermal','Equipment failure, oil fire or external ignition','Multi-asset loss, extended outage and hazard','critical',3),
('SUBSTATION','OIL_BUND_FAILURE','Oil containment / bund failure','environmental','Cracked bund, blocked interceptor or overfill','Oil pollution and environmental breach','high',4),
('SUBSTATION','VEGETATION_ENCROACHMENT','Vegetation encroachment on site','environmental','Uncontrolled growth within the compound','Clearance infringement and fire-spread risk','medium',5),
('SUBSTATION','ACCESS_ROAD_BLOCKED','Access road obstruction','operational','Flooding, landslip or blocked gateway','Delayed emergency response and restoration','medium',6),
('SUBSTATION','LIGHTNING_DAMAGE','Lightning / surge site damage','environmental','Direct strike or inadequate surge protection','Multiple equipment failures across the site','high',7),
-- CONTROL_BUILDING — Control Building
('CONTROL_BUILDING','ROOF_LEAK','Roof leak / water ingress','environmental','Damaged roofing, blocked gutters or ageing seals','Water onto panels and equipment, fault risk','high',0),
('CONTROL_BUILDING','HVAC_FAILURE','HVAC / air-conditioning failure','thermal','Compressor, fan or control failure','Overheating of relays, batteries and electronics','high',1),
('CONTROL_BUILDING','FIRE','Building fire','thermal','Electrical fault, battery gassing or external cause','Loss of control and protection, major hazard','critical',2),
('CONTROL_BUILDING','FLOODING','Ground-floor / cable-basement flooding','environmental','Site flooding, drain failure or burst pipe','Damage to cabling and equipment, outage','high',3),
('CONTROL_BUILDING','STRUCTURAL_CRACKING','Structural cracking / subsidence','mechanical','Foundation movement or ground settlement','Compromised integrity and equipment misalignment','medium',4),
('CONTROL_BUILDING','SECURITY_BREACH','Security / intrusion breach','operational','Forced entry or failed access control','Unauthorised access and equipment tampering','high',5),
('CONTROL_BUILDING','VENTILATION_BATTERY_ROOM','Battery room ventilation failure','environmental','Failed extract fan in battery room','Hydrogen accumulation and explosion risk','high',6),
('CONTROL_BUILDING','DAMP_CORROSION','Damp / condensation corrosion','ageing','Poor insulation, rising damp or humidity','Corrosion of equipment and building fabric','medium',7),
-- STRUCTURE — Structure (generic civil)
('STRUCTURE','CORROSION','Structural corrosion','ageing','Moisture, coastal salt or coating breakdown','Loss of strength and eventual structural failure','high',0),
('STRUCTURE','FOUNDATION_FAILURE','Foundation failure','mechanical','Settlement, erosion or inadequate design','Tilt, instability or collapse of structure','critical',1),
('STRUCTURE','SUBSIDENCE','Ground subsidence / movement','environmental','Soil settlement, washout or mining movement','Structural distortion and misalignment','high',2),
('STRUCTURE','CRACKING_SPALLING','Concrete cracking / spalling','ageing','Reinforcement corrosion, freeze-thaw or overload','Reduced load capacity and progressive decay','medium',3),
('STRUCTURE','IMPACT_DAMAGE','Impact / collision damage','mechanical','Vehicle, plant or debris impact','Member damage and compromised integrity','high',4),
('STRUCTURE','OVERLOAD','Structural overload','mechanical','Added loads beyond design or ice/wind loading','Deformation, member failure or collapse','high',5),
('STRUCTURE','VEGETATION_ROOT_DAMAGE','Vegetation / root damage','environmental','Tree roots or growth against the structure','Foundation disturbance and cracking','low',6),
-- POLE — Pole
('POLE','POLE_FAILURE','Pole failure / fallen pole','mechanical','Rot, corrosion, vehicle impact or storm loading','Line down, outage and safety hazard','critical',0),
('POLE','GROUNDLINE_ROT','Groundline rot / decay','ageing','Fungal decay or moisture at the groundline','Loss of strength and risk of collapse','critical',1),
('POLE','POLE_LEAN','Pole lean / tilt','mechanical','Soft ground, foundation loss or load imbalance','Conductor clearance loss and failure risk','high',2),
('POLE','VEHICLE_IMPACT','Vehicle impact','mechanical','Roadside collision','Pole fracture, line down and outage','high',3),
('POLE','WOODPECKER_INSECT','Woodpecker / insect damage','environmental','Woodpecker holes or termite/borer attack','Section loss and reduced strength','medium',4),
('POLE','POLE_FIRE','Pole fire / charring','thermal','Leakage-current tracking, grass fire or bushfire','Burn-through, structural failure and outage','high',5),
('POLE','CORROSION_STEEL','Corrosion of steel/concrete pole','ageing','Coating breakdown, moisture or reinforcement corrosion','Section loss and reduced load capacity','medium',6),
('POLE','FIXINGS_DETERIORATION','Crossarm / fixings deterioration','ageing','Rot, corrosion or loosening of bolts and bands','Hardware failure and conductor displacement','medium',7),
-- TOWER — Tower (lattice/steel)
('TOWER','MEMBER_CORROSION','Steel member corrosion','ageing','Coating breakdown, coastal salt or pollution','Section loss and reduced structural capacity','high',0),
('TOWER','FOUNDATION_FAILURE','Foundation failure / uplift','mechanical','Erosion, settlement or inadequate foundation','Leg movement, tower tilt or collapse','critical',1),
('TOWER','MEMBER_THEFT','Steel member / angle theft','operational','Theft of steelwork and bracing for scrap','Loss of structural members, collapse risk','critical',2),
('TOWER','BOLT_LOOSENING','Bolt loosening / missing bolts','mechanical','Vibration, theft or poor maintenance','Joint slackness and reduced member capacity','high',3),
('TOWER','FOUNDATION_EROSION','Foundation / ground erosion','environmental','Flooding, river scour or soil washout','Undermined footing and instability','high',4),
('TOWER','OVERLOAD_WIND_ICE','Wind / ice overload','mechanical','Extreme wind, ice accretion or broken-wire loading','Member buckling or tower collapse','high',5),
('TOWER','IMPACT_DAMAGE','Impact / collision damage','mechanical','Vehicle, aircraft or debris impact','Member deformation and structural weakening','high',6),
('TOWER','GRILLAGE_CORROSION','Below-grade grillage corrosion','ageing','Soil moisture and buried-steel corrosion','Hidden section loss at foundations','medium',7)
ON CONFLICT (feature_class_code, code) DO UPDATE SET
    label = EXCLUDED.label, dimension = EXCLUDED.dimension, typical_cause = EXCLUDED.typical_cause,
    effect = EXCLUDED.effect, severity = EXCLUDED.severity, sort_order = EXCLUDED.sort_order;

-- =====================================================================
-- Protection, fuses, arresters, relays & annunciators
-- =====================================================================
INSERT INTO faults.fault_modes (feature_class_code, code, label, dimension, typical_cause, effect, severity, sort_order) VALUES
-- MV_FUSE — MV Fuse
('MV_FUSE','FUSE_BLOWN','Fuse operated (blown)','electrical','Downstream fault or sustained overload','Loss of supply to protected section','medium',0),
('MV_FUSE','NUISANCE_OPERATION','Nuisance operation','electrical','Transformer inrush or transient through-fault below damage curve','Unnecessary outage with no persistent fault found','medium',1),
('MV_FUSE','WRONG_RATING','Incorrect fuse rating fitted','operational','Wrong element size installed during replacement','Mis-coordination or failure to protect equipment','high',2),
('MV_FUSE','FAILED_TO_CLEAR','Failed to interrupt fault','electrical','Fault current exceeds interrupting rating or aged element','Sustained fault, upstream device trips, equipment damage','critical',3),
('MV_FUSE','ELEMENT_DEGRADATION','Element fatigue/derating','ageing','Thermal cycling and oxidation of fuse element over years','Reduced rating and premature or slow operation','medium',4),
('MV_FUSE','CARRIER_MECH_FAIL','Fuse carrier mechanism failure','mechanical','Corroded hinge, broken trunnion or seized drop-out','Fails to drop clear or cannot be re-fused','medium',5),
('MV_FUSE','MOISTURE_INGRESS','Moisture ingress to tube','environmental','Cracked expulsion tube or failed seal admitting water','Internal tracking and unexpected operation','medium',6),
('MV_FUSE','FLASHOVER_EXTERNAL','External flashover across fuse','environmental','Pollution, salt or bird-streamer bridging terminals','Phase-to-earth fault and loss of supply','high',7),
-- LV_FUSE — LV Fuse Way
('LV_FUSE','FUSE_BLOWN','Fuse way operated (blown)','electrical','Downstream LV fault or sustained overload','Loss of supply to connected customers','medium',0),
('LV_FUSE','OVERLOAD_SLOW_BLOW','Thermal overload operation','thermal','Sustained load above rating on the way','Nuisance outage and possible heat damage','medium',1),
('LV_FUSE','WRONG_RATING','Incorrect fuse link rating','operational','Oversized or undersized link fitted at service','Loss of discrimination or unprotected cable','high',2),
('LV_FUSE','HOT_JOINT','Loose/hot fuse contact','thermal','Loose clip tension or oxidised contact faces','Overheating, voltage drop and burn-out','high',3),
('LV_FUSE','CARRIER_DAMAGE','Carrier insulation damage','mechanical','Arc tracking or physical damage to carrier body','Flashover risk and potential phase fault','medium',4),
('LV_FUSE','CORROSION_CONTACT','Contact corrosion','environmental','Damp pillar and condensation on brass contacts','Increased resistance and intermittent supply','medium',5),
('LV_FUSE','FAILED_TO_CLEAR','Failed to interrupt fault','electrical','Fault below link breaking capacity or wrong link type','Sustained fault and upstream fuse operation','high',6),
-- MV_ARRESTER — MV Surge Arrester
('MV_ARRESTER','THERMAL_RUNAWAY','Thermal runaway','thermal','Sustained leakage heating exceeding dissipation after block degradation','Arrester failure and possible explosive rupture','critical',0),
('MV_ARRESTER','BLOCK_PUNCTURE','MOV block puncture/failure','electrical','Energy duty exceeded or moisture-degraded varistor blocks','Short-circuit to earth and loss of protection','critical',1),
('MV_ARRESTER','DISCONNECTOR_OPERATED','Ground-lead disconnector operated','operational','Internal failure triggering disconnector to isolate arrester','Line kept in service but surge protection lost','medium',2),
('MV_ARRESTER','POLLUTION_FLASHOVER','External pollution flashover','environmental','Salt, dust or industrial contamination on housing','External flashover and earth fault','high',3),
('MV_ARRESTER','MOISTURE_INGRESS','Seal failure/moisture ingress','environmental','Degraded end seals admitting moisture to blocks','Rising leakage current and eventual failure','high',4),
('MV_ARRESTER','HOUSING_DAMAGE','Housing cracking/degradation','ageing','UV, thermal cycling and tracking of polymer or porcelain','Reduced creepage and flashover risk','medium',5),
('MV_ARRESTER','LEAKAGE_CURRENT_RISE','Elevated leakage current','ageing','Gradual MOV block ageing raising resistive leakage','Reduced margin and heating toward end of life','medium',6),
('MV_ARRESTER','LEAD_CONNECTION_FAIL','Lead/earth connection failure','mechanical','Corroded or loose line or earth-lead connection','Floating arrester offering no protection','high',7),
-- PROTECTIVE_RELAY — Protective Relay
('PROTECTIVE_RELAY','FALSE_TRIP','Mal-operation (false trip)','operational','Spurious pickup from transient, wiring error or logic fault','Unnecessary outage of healthy circuit','high',0),
('PROTECTIVE_RELAY','FAIL_TO_TRIP','Failure to operate on fault','electrical','Internal fault, blocked logic or disabled element','Fault not cleared, back-up trips wider section','critical',1),
('PROTECTIVE_RELAY','SETTING_ERROR','Incorrect settings/coordination','operational','Wrong pickup, time dial or group applied at commissioning','Mis-coordination, slow or no protection','high',2),
('PROTECTIVE_RELAY','CT_VT_INPUT_LOSS','CT/VT input signal loss','electrical','Open CT circuit, blown VT fuse or loose terminal','Under-reach, over-reach or incorrect operation','high',3),
('PROTECTIVE_RELAY','AUX_SUPPLY_LOSS','Auxiliary/DC supply loss','electrical','Failed battery, charger or tripped MCB on relay supply','Relay dead and protection unavailable','critical',4),
('PROTECTIVE_RELAY','HARDWARE_FAILURE','Hardware/module failure','electrical','Failed power supply, output relay or analogue card','Protection function lost or erratic','high',5),
('PROTECTIVE_RELAY','FIRMWARE_FAULT','Firmware/software fault','operational','Firmware bug or corrupted configuration','Lock-up, watchdog reset or wrong logic','medium',6),
('PROTECTIVE_RELAY','COMMS_LOSS','Communications loss','operational','Failed port, fibre or scheme channel','Loss of teleprotection, signalling or SCADA status','medium',7),
('PROTECTIVE_RELAY','CONTACT_DEGRADATION','Output contact/mechanism degradation','ageing','Worn trip contacts or sticking electromechanical element','Slow, intermittent or failed trip output','high',8),
-- CONTROL_IED — Control IED
('CONTROL_IED','HARDWARE_FAILURE','Hardware failure','electrical','Failed CPU, power supply or I/O module','Loss of control and monitoring of bay','high',0),
('CONTROL_IED','COMMS_LOSS','Communications loss','operational','Failed Ethernet, fibre or protocol stack fault','Loss of SCADA telemetry and remote control','high',1),
('CONTROL_IED','FIRMWARE_FAULT','Firmware/software fault','operational','Firmware bug, bad upgrade or corrupted config','Lock-up, reboot loops or incorrect operation','medium',2),
('CONTROL_IED','AUX_SUPPLY_LOSS','Auxiliary/DC supply loss','electrical','Failed station battery, charger or supply MCB','IED dead, control and indication lost','critical',3),
('CONTROL_IED','IO_POINT_FAILURE','I/O point failure','electrical','Failed binary input/output or analogue channel','Wrong status indication or failed command','high',4),
('CONTROL_IED','CONFIG_ERROR','Configuration/mapping error','operational','Incorrect point mapping or scaling at commissioning','Misleading data or mis-directed control','medium',5),
('CONTROL_IED','TIME_SYNC_LOSS','Time synchronisation loss','operational','Failed GPS/PTP/NTP source or sync input','Mis-sequenced events and unreliable records','low',6),
('CONTROL_IED','OVERTEMP_DEGRADATION','Thermal/ageing degradation','ageing','Cabinet overheating or electrolytic capacitor ageing','Intermittent faults and shortened life','medium',7),
-- ANNUNCIATOR — Annunciator
('ANNUNCIATOR','LAMP_FAILURE','Alarm lamp/LED failure','electrical','Burnt-out bulb or failed LED window','Alarm condition not visibly indicated','medium',0),
('ANNUNCIATOR','FAIL_TO_ALARM','Failure to annunciate','operational','Failed input relay, logic card or broken field wiring','Genuine alarm missed by operators','high',1),
('ANNUNCIATOR','FALSE_ALARM','Spurious/standing alarm','operational','Noisy contact, chatter or mis-set alarm input','Nuisance alarms and alarm desensitisation','medium',2),
('ANNUNCIATOR','AUDIBLE_FAILURE','Audible/horn failure','electrical','Failed buzzer, horn or audible driver circuit','Alarm not heard by station staff','medium',3),
('ANNUNCIATOR','POWER_SUPPLY_LOSS','Panel power supply loss','electrical','Failed supply module or tripped supply MCB','Entire annunciator blank and inactive','high',4),
('ANNUNCIATOR','ACK_TEST_FAULT','Acknowledge/test circuit fault','operational','Faulty accept, reset or lamp-test pushbutton','Alarms cannot be acknowledged or tested','low',5),
('ANNUNCIATOR','WINDOW_LEGEND_FAULT','Window/legend degradation','ageing','Faded legend or cracked window lens','Misread or unreadable alarm indication','low',6)
ON CONFLICT (feature_class_code, code) DO UPDATE SET
    label = EXCLUDED.label, dimension = EXCLUDED.dimension, typical_cause = EXCLUDED.typical_cause,
    effect = EXCLUDED.effect, severity = EXCLUDED.severity, sort_order = EXCLUDED.sort_order;
