// Minimal ambient declaration for the `dxf` package (Phase 15 Slice 4). The
// package ships no reliable TypeScript types, so we declare only the sliver we
// use: `Helper.toPolylines()`, which interpolates every entity (arcs/circles to
// segments) and expands block INSERTs into flat world-coordinate polylines --
// exactly what a read-only map overlay needs. If we later need entity
// attributes (layer, color) this can be widened to the parsed entity tree.
declare module "dxf" {
  export interface DxfPolyline {
    /** World-coordinate vertices, [x, y] in the drawing's units. */
    vertices: Array<[number, number]>
    rgb?: [number, number, number]
  }
  export interface DxfPolylines {
    polylines: DxfPolyline[]
    bbox?: { min: { x: number; y: number }; max: { x: number; y: number } }
  }
  export class Helper {
    constructor(contents: string)
    toPolylines(): DxfPolylines
  }
}
