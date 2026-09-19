import { apiFetch } from "@/lib/api"
import { type ParseOptions, parseFile } from "@/lib/import-formats"

/**
 * Browser-side ingest for the admin ETL import engine (docs/etl-import-engine.md).
 *
 * This is the "hybrid preview, server load" front end: every supported source
 * format is normalized HERE into one canonical shape -- a list of GeoJSON
 * Features (`properties` + optional `geometry`) -- so the mapping UI is
 * format-agnostic, and the same list is serialized to newline-delimited GeoJSON
 * (NDJSON) and uploaded to object storage for the server load job to stream. The
 * server therefore only ever has to parse ONE format (NDJSON), not five.
 *
 * The geometry-carrying formats (GeoJSON / Shapefile / KML / DXF) reuse the
 * existing Phase 15 overlay parser (`@/lib/import-formats`), which already
 * normalizes them to EPSG:4326 GeoJSON. CSV -- which that parser doesn't handle
 * and which is the most common admin import -- is parsed here into attribute-only
 * Features (geometry null); its geometry, if any, is assembled server-side at
 * load time from the profile's geometry_mode (lon/lat or WKT columns).
 */

/** The source formats the ETL importer can ingest from a file in v1. Live-DB
 * connectors (postgis/mssql) are a later phase and never reach this module. */
export type EtlSourceFormat = "csv" | "geojson" | "shapefile" | "kml" | "dxf"

/** A minimal GeoJSON geometry -- we never introspect it here, only carry it. */
export interface GeoJSONGeometry {
  type: string
  coordinates?: unknown
  geometries?: unknown
}

/**
 * The canonical per-row shape, both in memory (for the mapping preview) and on
 * the wire: one GeoJSON Feature per source row. `geometry` is null for CSV
 * (assembled server-side) and for geometry-none target classes. Serialized one
 * per line as NDJSON for the load artifact.
 */
export interface NdjsonFeature {
  type: "Feature"
  properties: Record<string, unknown>
  geometry: GeoJSONGeometry | null
}

/** Result of normalizing a picked file: the ordered column names discovered in
 * the source (for the mapping UI) and every row as a canonical Feature. */
export interface ParsedEtlSource {
  format: EtlSourceFormat
  /** Source column names, in first-seen order, across all rows. */
  columns: string[]
  features: NdjsonFeature[]
}

/** Maps a file extension to its ETL source format. `.txt`/`.tsv` are treated as
 * delimited text (CSV parser sniffs the delimiter); `.kmz` shares the KML
 * family. */
const EXT_FORMAT: Record<string, EtlSourceFormat> = {
  csv: "csv",
  tsv: "csv",
  txt: "csv",
  geojson: "geojson",
  json: "geojson",
  kml: "kml",
  kmz: "kml",
  zip: "shapefile",
  dxf: "dxf",
}

/** File-picker `accept` list of every extension the ETL importer can parse. */
export const ETL_IMPORT_ACCEPT = ".csv,.tsv,.txt,.geojson,.json,.kml,.kmz,.zip,.dxf"

/** Detects the ETL source format from a filename, or null if unsupported. */
export function detectEtlFormat(filename: string): EtlSourceFormat | null {
  const ext = filename.toLowerCase().match(/\.([^.]+)$/)?.[1]
  if (!ext) return null
  return EXT_FORMAT[ext] ?? null
}

/**
 * Parses a picked file into the canonical {columns, features} shape, dispatching
 * on its extension. CSV is parsed here; the geometry formats reuse the Phase 15
 * overlay parser (which needs a source CRS for a `.prj`-less shapefile or any
 * DXF -- passed through via `opts`). Throws a user-facing Error on an
 * unsupported type, a parse failure, or an empty result.
 */
export async function parseEtlSource(
  file: File,
  opts: ParseOptions = {},
): Promise<ParsedEtlSource> {
  const format = detectEtlFormat(file.name)
  if (!format) throw new Error("Unsupported file type.")

  if (format === "csv") return parseCsvFile(await file.text())

  // GeoJSON / Shapefile / KML / DXF: reuse the overlay parser, then flatten its
  // 4326 FeatureCollection into our canonical Features + discovered columns.
  const parsed = await parseFile(file, opts)
  const features: NdjsonFeature[] = []
  const columns: string[] = []
  const seen = new Set<string>()
  for (const raw of parsed.geojson.features) {
    const f = raw as { properties?: Record<string, unknown>; geometry?: GeoJSONGeometry | null }
    const properties = f.properties ?? {}
    for (const key of Object.keys(properties)) {
      if (!seen.has(key)) {
        seen.add(key)
        columns.push(key)
      }
    }
    features.push({ type: "Feature", properties, geometry: f.geometry ?? null })
  }
  if (features.length === 0) throw new Error("No rows found in the file.")
  return { format, columns, features }
}

/**
 * Parses CSV/TSV text into attribute-only Features (geometry null). The header
 * row supplies the column names; every subsequent row becomes one Feature whose
 * `properties` map header -> raw string cell. Geometry, if the target class has
 * any, is built server-side from the profile's lon/lat or WKT columns, so no
 * coordinate interpretation happens here.
 */
export function parseCsvFile(text: string): ParsedEtlSource {
  const rows = parseDelimited(text)
  if (rows.length === 0) throw new Error("The file is empty.")

  const header = rows[0].map((h) => h.trim())
  if (header.every((h) => h === "")) throw new Error("The header row is empty.")
  // De-duplicate blank/repeated headers so property keys stay addressable.
  const columns = dedupeHeaders(header)

  const features: NdjsonFeature[] = []
  for (let i = 1; i < rows.length; i++) {
    const cells = rows[i]
    // Skip a trailing blank line (a lone empty cell from a final newline).
    if (cells.length === 1 && cells[0] === "") continue
    const properties: Record<string, unknown> = {}
    for (let c = 0; c < columns.length; c++) {
      properties[columns[c]] = c < cells.length ? cells[c] : ""
    }
    features.push({ type: "Feature", properties, geometry: null })
  }
  if (features.length === 0) throw new Error("No data rows found (only a header).")
  return { format: "csv", columns, features }
}

/** Serializes canonical Features to the newline-delimited GeoJSON (NDJSON)
 * artifact the load job streams: one complete Feature JSON object per line. */
export function featuresToNdjson(features: NdjsonFeature[]): string {
  return features.map((f) => JSON.stringify(f)).join("\n")
}

/** A small sample of rows for the mapping preview, without shipping the whole
 * file into the UI. */
export function sampleFeatures(parsed: ParsedEtlSource, n = 20): NdjsonFeature[] {
  return parsed.features.slice(0, n)
}

// ---------------------------------------------------------------------------
// Server round-trips (S3): presigned upload + synchronous dry-run
// ---------------------------------------------------------------------------
//
// These call the features:write-gated /imports surface (api/internal/importer).
// The wizard (S5) stitches them together: normalize a file -> upload the NDJSON
// artifact -> dry-run a sample -> enqueue the run. They live here beside the
// normalization they build on so the whole browser side of the engine is one
// module.

/** What POST /imports/uploads returns: a server-minted object key (never
 * client-controlled) and a short-lived presigned PUT URL to RustFS. */
export interface ImportUploadTarget {
  object_key: string
  upload_url: string
}

/** Mints an upload slot for the NDJSON artifact. The filename is informational
 * (logging/UX); the object key is decided server-side. */
export async function registerImportUpload(filename: string): Promise<ImportUploadTarget> {
  return apiFetch<ImportUploadTarget>("/imports/uploads", {
    method: "POST",
    body: JSON.stringify({ filename }),
  })
}

/** PUTs the NDJSON straight to RustFS via the presigned URL -- a raw cross-origin
 * fetch, NOT apiFetch: the URL carries its own auth in the signature and must not
 * get our session cookie / CSRF header. */
export async function putNdjsonArtifact(uploadUrl: string, ndjson: string): Promise<void> {
  const res = await fetch(uploadUrl, {
    method: "PUT",
    body: ndjson,
    headers: { "Content-Type": "application/x-ndjson" },
  })
  if (!res.ok) throw new Error(`Uploading the source artifact failed (HTTP ${res.status}).`)
}

/** Register + PUT in one step: serializes the features to NDJSON, uploads them,
 * and returns the stored object key the run job (S4) will reference. */
export async function uploadEtlArtifact(features: NdjsonFeature[], filename: string): Promise<string> {
  const target = await registerImportUpload(filename)
  await putNdjsonArtifact(target.upload_url, featuresToNdjson(features))
  return target.object_key
}

// ---------------------------------------------------------------------------
// GeoPackage (v2, server-parsed): upload the raw file, list its layers server-
// side, dry-run + run reading the sample/rows from object storage by object key.
// ---------------------------------------------------------------------------
//
// Unlike the v1 formats, the browser does NOT parse a GeoPackage: it uploads the
// raw .gpkg verbatim and the server (api/internal/importer/geopackage.go) opens
// it. So there is no browser-side column discovery or inline sample; the wizard
// asks the server for the file's layers and lets it read the dry-run sample.

/** `accept` list for the GeoPackage file picker. */
export const ETL_GEOPACKAGE_ACCEPT = ".gpkg"
/** `accept` list for the SpatiaLite file picker (SpatiaLite databases use a range
 * of conventional SQLite extensions). */
export const ETL_SPATIALITE_ACCEPT = ".sqlite,.sqlite3,.db"

/** The file picker `accept` list for a server-parsed source format. */
export function serverParsedAccept(sourceFormat: string): string {
  return sourceFormat === "spatialite" ? ETL_SPATIALITE_ACCEPT : ETL_GEOPACKAGE_ACCEPT
}

/** Mints an upload slot for a RAW SQLite-family file (artifact = the source
 * format, e.g. "geopackage" -> a .gpkg key, "spatialite" -> a .sqlite key),
 * distinct from the NDJSON artifact slot. */
export async function registerServerParsedUpload(
  filename: string,
  format: string,
): Promise<ImportUploadTarget> {
  return apiFetch<ImportUploadTarget>("/imports/uploads", {
    method: "POST",
    body: JSON.stringify({ filename, artifact: format }),
  })
}

/** PUTs a raw file (the .gpkg/.sqlite blob) straight to RustFS via the presigned
 * URL -- a raw cross-origin fetch, not apiFetch, for the same reason
 * putNdjsonArtifact is (the URL carries its own signed auth). */
export async function putRawArtifact(uploadUrl: string, file: Blob): Promise<void> {
  const res = await fetch(uploadUrl, {
    method: "PUT",
    body: file,
    headers: { "Content-Type": "application/octet-stream" },
  })
  if (!res.ok) throw new Error(`Uploading the file failed (HTTP ${res.status}).`)
}

/** Register + PUT a raw SQLite-family file (GeoPackage / SpatiaLite) in one step;
 * returns the stored object key. */
export async function uploadServerParsedFile(file: File, format: string): Promise<string> {
  const target = await registerServerParsedUpload(file.name, format)
  await putRawArtifact(target.upload_url, file)
  return target.object_key
}

/** One feature layer of an uploaded SQLite-family file (mirrors importer.GpkgLayer). */
export interface GpkgLayer {
  name: string
  geometry_column: string
  geometry_type: string
  srid: number
  feature_count: number
}

/** Lists a staged SQLite-family file's feature layers server-side (POST
 * /imports/layers) so the wizard can pin/override the source layer. The format
 * tells the server which metadata catalog to read (GeoPackage vs SpatiaLite).
 * features-gated. */
export async function listServerParsedLayers(objectKey: string, format: string): Promise<GpkgLayer[]> {
  const res = await apiFetch<{ layers: GpkgLayer[] }>("/imports/layers", {
    method: "POST",
    body: JSON.stringify({ object_key: objectKey, source_format: format }),
  })
  return res.layers ?? []
}

/** One active, runnable import profile (mirrors importer.ProfileSummary). This
 * is the wizard's picker source -- a features-gated read of the active profiles,
 * distinct from the metadata-gated admin authoring list (AdminImportProfile), so
 * a run-only operator never needs metadata:read. */
export interface EtlImportProfile {
  code: string
  name: string
  feature_class_code: string
  subtype_code?: number | null
  source_format: string
  /** For a multi-layer server-parsed source (GeoPackage): the profile's pinned
   * default feature layer, if any. Null for the single-layer file formats. */
  source_layer?: string | null
  write_mode: string
  match_fields: string[]
  geometry_mode: string
  geometry_lon_column?: string | null
  geometry_lat_column?: string | null
  geometry_wkt_column?: string | null
}

/** Source formats parsed server-side (the raw file is uploaded verbatim and the
 * server opens it) rather than normalized to NDJSON in the browser. The
 * SQLite-family formats GeoPackage (S1) and SpatiaLite (S2). */
export function isServerParsedFormat(sourceFormat: string): boolean {
  return sourceFormat === "geopackage" || sourceFormat === "spatialite"
}

/** Live-database source formats (v2-S3+): there is NO artifact at all -- the
 * worker connects out to a remote database and streams rows. The wizard skips
 * the file step entirely; the server samples/streams straight from the profile's
 * saved connection (its source table/query). PostGIS (S3); MSSQL (S4) later. */
export function isLiveDBFormat(sourceFormat: string): boolean {
  return sourceFormat === "postgis" || sourceFormat === "mssql"
}

/** Lists the active import profiles the caller can run (GET /imports/profiles).
 * features:read-gated -- the wizard's first step. */
export async function listRunnableProfiles(): Promise<EtlImportProfile[]> {
  const res = await apiFetch<{ import_profiles: EtlImportProfile[] }>("/imports/profiles")
  return res.import_profiles
}

/** One sample row's dry-run verdict (mirrors importer.DryRunRow). */
export interface EtlDryRunRow {
  row_number: number
  valid: boolean
  error?: string
  /** The mapped, coerced target attributes -- what this source row becomes. */
  attributes?: Record<string, unknown>
  has_geometry: boolean
}

/** The whole sample's dry-run report (mirrors importer.DryRunReport). */
export interface EtlDryRunReport {
  feature_class_code: string
  subtype_code?: number | null
  write_mode: string
  geometry_mode: string
  match_fields: string[]
  total: number
  valid: number
  invalid: number
  rows: EtlDryRunRow[]
}

/** Runs a synchronous dry-run of a sample against a saved profile: the server
 * applies the profile's mappings + geometry assembly and validates each row
 * through the same crud checks a real create runs, writing nothing. */
export async function runImportDryRun(
  profileCode: string,
  sample: NdjsonFeature[],
): Promise<EtlDryRunReport> {
  return apiFetch<EtlDryRunReport>("/imports/dry-run", {
    method: "POST",
    body: JSON.stringify({ profile_code: profileCode, sample }),
  })
}

/** Server-side dry-run for a server-parsed source (GeoPackage): the browser can't
 * sample the file, so it hands the server the staged object key (+ optional layer
 * override) and the server reads a preview off it and validates it exactly as the
 * inline dry-run does. */
export async function runImportDryRunFromObject(
  profileCode: string,
  objectKey: string,
  sourceLayer?: string | null,
): Promise<EtlDryRunReport> {
  return apiFetch<EtlDryRunReport>("/imports/dry-run", {
    method: "POST",
    body: JSON.stringify({
      profile_code: profileCode,
      object_key: objectKey,
      source_layer: sourceLayer ?? "",
    }),
  })
}

/** Server-side dry-run for a live-database source (PostGIS): there is no staged
 * object, so the server samples straight off the profile's remote table/query (+
 * optional table override) and validates it exactly as the inline dry-run does. */
export async function runImportDryRunLive(
  profileCode: string,
  sourceLayer?: string | null,
): Promise<EtlDryRunReport> {
  return apiFetch<EtlDryRunReport>("/imports/dry-run", {
    method: "POST",
    body: JSON.stringify({
      profile_code: profileCode,
      source_layer: sourceLayer ?? "",
    }),
  })
}

// ---------------------------------------------------------------------------
// Load jobs (S4): enqueue a run over an uploaded artifact, then poll its status.
// ---------------------------------------------------------------------------

/** One per-row failure in a run's error report (mirrors importer.jobRowError). */
export interface EtlJobRowError {
  row_number: number
  message: string
}

/** One import run's status + tallies (mirrors importer.jobResponse). `errors`
 * is populated only on the single-job status view, not the history list. */
export interface EtlImportJob {
  id: string
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled"
  feature_class_code: string
  dry_run: boolean
  total_rows: number | null
  inserted_count: number
  updated_count: number
  skipped_count: number
  error_count: number
  error?: string
  created_at: string
  started_at?: string
  finished_at?: string
  errors?: EtlJobRowError[]
}

/** Enqueues a load run over an already-uploaded NDJSON artifact (object_key from
 * uploadEtlArtifact). dryRun validates + tallies the whole file without writing.
 * Returns the job id to poll with getImportJob. Requires features:write. */
export async function enqueueImportJob(
  profileCode: string,
  objectKey: string,
  dryRun = false,
  sourceLayer?: string | null,
): Promise<string> {
  const res = await apiFetch<{ job_id: string }>("/imports/jobs", {
    method: "POST",
    body: JSON.stringify({
      profile_code: profileCode,
      object_key: objectKey,
      dry_run: dryRun,
      source_layer: sourceLayer ?? "",
    }),
  })
  return res.job_id
}

/** Enqueues a load run for a live-database source (PostGIS): no artifact, so the
 * object key is empty and the server connects out to the profile's saved
 * connection. A table override (source_layer) is optional. Requires
 * features:write. */
export async function enqueueLiveImportJob(
  profileCode: string,
  dryRun = false,
  sourceLayer?: string | null,
): Promise<string> {
  const res = await apiFetch<{ job_id: string }>("/imports/jobs", {
    method: "POST",
    body: JSON.stringify({
      profile_code: profileCode,
      object_key: "",
      dry_run: dryRun,
      source_layer: sourceLayer ?? "",
    }),
  })
  return res.job_id
}

/** Fetches one run's current status, tallies, and per-row error report. Poll
 * this until `status` is a terminal value (succeeded/failed/cancelled). */
export async function getImportJob(jobId: string): Promise<EtlImportJob> {
  return apiFetch<EtlImportJob>(`/imports/jobs/${jobId}`)
}

/** Lists the caller's most recent runs (newest first) for a history panel. */
export async function listImportJobs(): Promise<EtlImportJob[]> {
  return apiFetch<EtlImportJob[]>("/imports/jobs")
}

// ---------------------------------------------------------------------------
// CSV/TSV parsing (RFC 4180, hand-rolled -- no dependency)
// ---------------------------------------------------------------------------

/**
 * Parses delimited text into a grid of string cells, honouring RFC 4180:
 * fields may be double-quoted; a quoted field may contain the delimiter, CR, and
 * LF; a literal quote inside a quoted field is escaped by doubling (""). The
 * delimiter is sniffed from the first line among comma, tab, and semicolon (the
 * three real-world CSV/TSV separators). A leading UTF-8 BOM is stripped. Both
 * LF and CRLF line endings are accepted.
 */
export function parseDelimited(input: string): string[][] {
  const text = input.charCodeAt(0) === 0xfeff ? input.slice(1) : input
  const delimiter = sniffDelimiter(text)

  const rows: string[][] = []
  let row: string[] = []
  let field = ""
  let inQuotes = false
  let started = false // any character seen for the current record?

  for (let i = 0; i < text.length; i++) {
    const ch = text[i]

    if (inQuotes) {
      if (ch === '"') {
        if (text[i + 1] === '"') {
          field += '"'
          i++ // consume the escaped quote
        } else {
          inQuotes = false
        }
      } else {
        field += ch
      }
      continue
    }

    if (ch === '"') {
      inQuotes = true
      started = true
    } else if (ch === delimiter) {
      row.push(field)
      field = ""
      started = true
    } else if (ch === "\n" || ch === "\r") {
      // End the record on LF or CRLF (swallow the LF of a CRLF pair).
      if (ch === "\r" && text[i + 1] === "\n") i++
      row.push(field)
      rows.push(row)
      row = []
      field = ""
      started = false
    } else {
      field += ch
      started = true
    }
  }

  // Flush a final field/record not terminated by a trailing newline.
  if (started || field !== "" || row.length > 0) {
    row.push(field)
    rows.push(row)
  }
  return rows
}

/** Picks the delimiter by counting comma / tab / semicolon occurrences on the
 * first line (outside quotes is a fair approximation for a header). Defaults to
 * comma when none appear. */
function sniffDelimiter(text: string): string {
  const firstLine = text.split(/\r?\n/, 1)[0] ?? ""
  const counts: Record<string, number> = { ",": 0, "\t": 0, ";": 0 }
  let inQuotes = false
  for (const ch of firstLine) {
    if (ch === '"') inQuotes = !inQuotes
    else if (!inQuotes && ch in counts) counts[ch]++
  }
  let best = ","
  for (const d of ["\t", ";"]) if (counts[d] > counts[best]) best = d
  return best
}

/** Ensures every column name is unique and non-empty so property keys stay
 * addressable: blanks become `column_N`, and a repeat gets a `_2`, `_3`, ... */
function dedupeHeaders(header: string[]): string[] {
  const used = new Set<string>()
  return header.map((raw, i) => {
    let name = raw === "" ? `column_${i + 1}` : raw
    if (used.has(name)) {
      let n = 2
      while (used.has(`${name}_${n}`)) n++
      name = `${name}_${n}`
    }
    used.add(name)
    return name
  })
}
