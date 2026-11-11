// The editable solution model and its YAML (de)serialization. The format
// is the Go `solution.Solution` schema (version 2: player hardware, then
// connections and programs); layout is stored as presentation metadata
// (`metadata.layout`) and never affects execution.
import YAML from 'yaml'
import type { Connection, Puzzle } from '../api/types'

export interface Position {
  x: number
  y: number
}

/** A player-instantiated node (solution.Node). Unset fields use defaults. */
export interface HardwareNode {
  id: string
  type: string
  port_count?: number
  program_memory?: number
  port_functions?: string[]
  config?: Record<string, unknown>
}

export interface SolutionModel {
  puzzle: string
  /** hardware the player instantiated (empty for fixed-topology puzzles) */
  hardware: HardwareNode[]
  connections: Connection[]
  /** cell id -> assembly source (programmable cells only) */
  programs: Record<string, string>
  /** cell id -> graph position (presentation only) */
  layout: Record<string, Position>
  /** other metadata keys found in an imported file, preserved on save */
  extraMetadata: Record<string, unknown>
}

export function emptySolution(puzzle: Puzzle, programmable: (type: string) => boolean): SolutionModel {
  const programs: Record<string, string> = {}
  for (const c of puzzle.cells) if (programmable(c.type)) programs[c.id] = ''
  return { puzzle: puzzle.id, hardware: [], connections: [], programs, layout: {}, extraMetadata: {} }
}

/** Serializes to solution YAML (schema_version 2). Empty programs are omitted. */
export function serializeSolution(s: SolutionModel): string {
  const programs: Record<string, string> = {}
  for (const id of Object.keys(s.programs).sort()) {
    const src = s.programs[id]
    if (src.trim() !== '') programs[id] = src.endsWith('\n') ? src : src + '\n'
  }
  const layout: Record<string, Position> = {}
  for (const id of Object.keys(s.layout).sort()) {
    const p = s.layout[id]
    layout[id] = { x: Math.round(p.x), y: Math.round(p.y) }
  }
  const doc: Record<string, unknown> = {
    schema_version: 2,
    puzzle: s.puzzle,
  }
  if (s.hardware.length > 0) {
    doc.hardware = s.hardware.map((n) => {
      const out: Record<string, unknown> = { id: n.id, type: n.type }
      if (n.port_count !== undefined) out.port_count = n.port_count
      if (n.program_memory !== undefined) out.program_memory = n.program_memory
      if (n.port_functions !== undefined) out.port_functions = [...n.port_functions]
      if (n.config && Object.keys(n.config).length > 0) out.config = { ...n.config }
      return out
    })
  }
  Object.assign(doc, {
    connections: s.connections.map((c) => ({
      from: { cell: c.from.cell, port: c.from.port },
      to: { cell: c.to.cell, port: c.to.port },
    })),
    programs,
  })
  const metadata = { ...s.extraMetadata }
  if (Object.keys(layout).length > 0) metadata.layout = layout
  if (Object.keys(metadata).length > 0) doc.metadata = metadata
  return YAML.stringify(doc, { blockQuote: 'literal', lineWidth: 0 })
}

export class SolutionParseError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'SolutionParseError'
  }
}

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

/**
 * Parses solution YAML into the editable model. This only checks the
 * shape needed to edit the file; the Go backend performs full validation
 * (topology, schema, compilation) when the solution is loaded.
 */
export function parseSolution(text: string): SolutionModel {
  let raw: unknown
  try {
    raw = YAML.parse(text, { maxAliasCount: 50 })
  } catch (e) {
    throw new SolutionParseError(`Not valid YAML: ${(e as Error).message}`)
  }
  if (!isObj(raw)) throw new SolutionParseError('A solution must be a YAML mapping.')
  for (const forbidden of ['cells', 'tests', 'limits']) {
    if (forbidden in raw) throw new SolutionParseError(`Solutions cannot define "${forbidden}"; that belongs to the puzzle.`)
  }
  if (raw.schema_version !== 1 && raw.schema_version !== 2) {
    throw new SolutionParseError(`Unsupported schema_version ${String(raw.schema_version)} (expected 1 or 2).`)
  }
  const hardware: HardwareNode[] = []
  const rawHw = raw.hardware ?? []
  if (!Array.isArray(rawHw)) throw new SolutionParseError('"hardware" must be a list.')
  rawHw.forEach((n, i) => {
    if (!isObj(n) || typeof n.id !== 'string' || typeof n.type !== 'string') {
      throw new SolutionParseError(`hardware[${i}] must have "id" and "type".`)
    }
    const node: HardwareNode = { id: n.id, type: n.type }
    if (typeof n.port_count === 'number') node.port_count = n.port_count
    if (typeof n.program_memory === 'number') node.program_memory = n.program_memory
    if (Array.isArray(n.port_functions)) node.port_functions = n.port_functions.map((f) => String(f).toUpperCase())
    if (isObj(n.config)) node.config = { ...n.config }
    hardware.push(node)
  })
  if (typeof raw.puzzle !== 'string' || raw.puzzle === '') throw new SolutionParseError('Missing "puzzle" id.')
  const connections: Connection[] = []
  const rawConns = raw.connections ?? []
  if (!Array.isArray(rawConns)) throw new SolutionParseError('"connections" must be a list.')
  rawConns.forEach((c, i) => {
    const ep = (v: unknown, side: string) => {
      if (!isObj(v) || typeof v.cell !== 'string' || typeof v.port !== 'string') {
        throw new SolutionParseError(`connections[${i}].${side} must have "cell" and "port".`)
      }
      return { cell: v.cell, port: v.port.toUpperCase() }
    }
    if (!isObj(c)) throw new SolutionParseError(`connections[${i}] must be a mapping.`)
    connections.push({ from: ep(c.from, 'from'), to: ep(c.to, 'to') })
  })
  const programs: Record<string, string> = {}
  const rawProgs = raw.programs ?? {}
  if (!isObj(rawProgs)) throw new SolutionParseError('"programs" must be a mapping of cell id to source.')
  for (const [k, v] of Object.entries(rawProgs)) {
    if (typeof v !== 'string') throw new SolutionParseError(`programs.${k} must be a string.`)
    programs[k] = v
  }
  const layout: Record<string, Position> = {}
  const extraMetadata: Record<string, unknown> = {}
  if (raw.metadata !== undefined) {
    if (!isObj(raw.metadata)) throw new SolutionParseError('"metadata" must be a mapping.')
    for (const [k, v] of Object.entries(raw.metadata)) {
      if (k === 'layout' && isObj(v)) {
        for (const [id, p] of Object.entries(v)) {
          if (isObj(p) && typeof p.x === 'number' && typeof p.y === 'number' && Number.isFinite(p.x) && Number.isFinite(p.y)) {
            layout[id] = { x: p.x, y: p.y }
          }
        }
      } else {
        extraMetadata[k] = v
      }
    }
  }
  return { puzzle: raw.puzzle, hardware, connections, programs, layout, extraMetadata }
}

/**
 * Adapts an imported solution to a puzzle's cells: programs are kept for
 * every programmable cell, fixed or player-built (missing ones become
 * empty), and unknown cells' programs are dropped with a warning. Player
 * hardware is dropped (with a warning) when the puzzle has a fixed
 * topology; the backend checks everything else.
 */
export function adaptToPuzzle(
  s: SolutionModel,
  puzzle: Puzzle,
  programmable: (type: string) => boolean,
): { solution: SolutionModel; warnings: string[] } {
  const warnings: string[] = []
  const programs: Record<string, string> = {}
  let hardware = s.hardware
  if (!puzzle.hardware && hardware.length > 0) {
    warnings.push(`Dropped ${hardware.length} hardware node(s): this puzzle has a fixed topology.`)
    hardware = []
  }
  const ids = new Set([...puzzle.cells.map((c) => c.id), ...hardware.map((n) => n.id)])
  for (const c of [...puzzle.cells, ...hardware]) if (programmable(c.type)) programs[c.id] = s.programs[c.id] ?? ''
  for (const id of Object.keys(s.programs)) {
    if (!(id in programs)) warnings.push(`Dropped program for "${id}": the puzzle has no programmable cell with that id.`)
  }
  const layout: Record<string, Position> = {}
  for (const [id, p] of Object.entries(s.layout)) if (ids.has(id)) layout[id] = p
  const connections = s.connections.filter((c) => ids.has(c.from.cell) && ids.has(c.to.cell))
  if (connections.length < s.connections.length) warnings.push(`Dropped ${s.connections.length - connections.length} connection(s) to cells that do not exist.`)
  return { solution: { ...s, hardware, connections, programs, layout }, warnings }
}
