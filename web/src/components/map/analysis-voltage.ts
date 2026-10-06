import { useEffect } from "react"

import { useFeatureClasses } from "@/hooks/use-feature-classes"

/**
 * Resolve the "Nominal voltage (V)" input shared by the analysis panels into the
 * value sent as nominal_voltage_v -- or UNDEFINED when the field is left blank,
 * which omits it so the backend derives it from a boundary-transformer start
 * (its secondary voltage). That makes "analyse the LV network below this
 * transformer" need no voltage input. A non-transformer start with a blank field
 * gets a clear backend error (nominal_voltage_v is required), so the panel need
 * not know the start's class.
 *
 * Only use this for panels whose engine accepts an omitted nominal (fault,
 * coordination, and the sweep-based load-flow / voltage-profile). Panels whose
 * engine still requires a nominal keep validating a positive number.
 */
export function resolveNominalVoltage(
  raw: string,
): { ok: true; value?: number } | { ok: false; message: string } {
  if (raw.trim() === "") return { ok: true } // omitted -> backend derives (transformer start)
  const v = Number(raw)
  if (!Number.isFinite(v) || v <= 0) {
    return {
      ok: false,
      message: "Nominal voltage must be a positive number, or leave it blank to use a transformer start's secondary.",
    }
  }
  return { ok: true, value: v }
}

/** Placeholder/hint for the optional voltage input. */
export const NOMINAL_VOLTAGE_HINT = "blank → transformer secondary"

/**
 * Default the "Nominal voltage" field to BLANK whenever the armed start is a
 * boundary transformer (so its secondary voltage is derived), else to the
 * panel's usual default. Runs each time a new start is armed, so "Analyse LV
 * network" (arm a study from a transformer) lands on a ready-to-run blank field.
 */
export function useDeriveVoltageDefault(
  armedStart: { feature_class: string } | null | undefined,
  defaultVoltage: string,
  setVoltageInput: (v: string) => void,
) {
  const { data } = useFeatureClasses()
  useEffect(() => {
    if (!armedStart) return
    const fc = data?.feature_classes.find((c) => c.code === armedStart.feature_class)
    setVoltageInput(fc?.boundary_role === "transformer" ? "" : defaultVoltage)
  }, [armedStart, data, defaultVoltage, setVoltageInput])
}
