// Hardware helpers: resolving every cell (fixed puzzle cells plus the
// player's nodes) to its effective configuration, and small mirrors of the
// backend's default and cost rules so the editor can react instantly.
//
// The Go backend stays authoritative: it validates every node, computes
// the real costs (solution.HardwareReport) and the scores. These mirrors
// only drive what the UI shows before the backend's answer arrives, and
// the UI prefers the backend's report whenever it has one.
import type {
  CellTypeCatalog,
  ConfigField,
  CostModel,
  EffectiveHardware,
  HardwareCategory,
  HardwareReport,
  HardwareSpec,
  Puzzle,
} from '../api/types'
import type { HardwareNode, SolutionModel } from './solution'

/** A cell as the editor sees it: fixed or player-built, fully resolved. */
export interface CellView {
  id: string
  type: string
  /** true for hardware the player instantiated */
  player: boolean
  category: HardwareCategory
  programmable: boolean
  portCount: number
  /** per port: enabled (fixed cells may have disabled ports) */
  enabled: boolean[]
  /** per port function (peripherals with port functions) */
  portFunctions?: string[]
  programMemory: number
  config?: Record<string, unknown>
}

export const LEGACY_PORTS = 4

export function portName(i: number): string {
  return `P${i}`
}

export function portIndex(name: string): number {
  const m = /^P(0|[1-9]\d?)$/.exec(name.toUpperCase())
  return m ? Number(m[1]) : -1
}

export function typeSpec(catalog: CellTypeCatalog | null, type: string): HardwareSpec | undefined {
  return catalog?.types.find((t) => t.type === type)?.hardware
}

/** Mirrors cell.PortFunctions.DefaultAssignment. */
export function pluginDefaultFunctions(spec: HardwareSpec, n: number): string[] {
  const d = spec.portFunctions?.default ?? []
  return Array.from({ length: n }, (_, i) => (d.length ? d[i % d.length] : ''))
}

/** Mirrors puzzle.Effective.DefaultFunctions (falls back to the plugin default). */
export function defaultFunctions(spec: HardwareSpec, eff: EffectiveHardware | undefined, n: number): string[] | undefined {
  const pf = spec.portFunctions
  if (!pf) return undefined
  const plugin = pluginDefaultFunctions(spec, n)
  if (!pf.configurable || !eff?.functions) return plugin
  const allowed = new Set(eff.functions)
  const counts: Record<string, number> = {}
  const room = (f: string) => allowed.has(f) && (!eff.functionMax?.[f] || (counts[f] ?? 0) < eff.functionMax[f])
  const out = plugin.map((f) => {
    if (room(f)) {
      counts[f] = (counts[f] ?? 0) + 1
      return f
    }
    return ''
  })
  return out.map((f, i) => {
    if (f) return f
    for (const cand of eff.functions!) {
      if (room(cand)) {
        counts[cand] = (counts[cand] ?? 0) + 1
        return cand
      }
    }
    return plugin[i]
  })
}

/** Mirrors cell.Cost. */
export function estimateCost(model: CostModel, n: { ports: number; programMemory: number; config?: Record<string, unknown> }): number {
  let c = model.base + model.perPort * n.ports
  for (const p of model.properties ?? []) {
    const raw = p.property === 'program_memory' ? n.programMemory : n.config?.[p.property]
    const v = typeof raw === 'number' ? raw : 0
    if (v > 0 && p.per > 0) c += Math.ceil(v / p.per) * p.cost
  }
  return c
}

/** Mirrors cell.WithDefaults: unset fields take their schema default. */
export function withConfigDefaults(fields: ConfigField[] | undefined, config: Record<string, unknown> | undefined): Record<string, unknown> | undefined {
  let out = config
  for (const f of fields ?? []) {
    if (f.default === undefined || f.default === null || (config && f.name in config)) continue
    out = { ...(out ?? {}), [f.name]: f.default }
  }
  return out
}

/** Resolves a player node's effective configuration. */
export function resolveNode(node: HardwareNode, spec: HardwareSpec | undefined, eff: EffectiveHardware | undefined, fields?: ConfigField[]) {
  const portCount = node.port_count ?? eff?.ports.default ?? spec?.ports.default ?? LEGACY_PORTS
  const programMemory = node.program_memory ?? eff?.programMemory?.default ?? spec?.programMemory?.default ?? 0
  const portFunctions = node.port_functions ?? (spec ? defaultFunctions(spec, eff, portCount) : undefined)
  return { portCount, programMemory, portFunctions, config: withConfigDefaults(fields, node.config) }
}

/**
 * Every cell of the machine, fixed puzzle cells first (puzzle order), then
 * the player's nodes (solution order), as the engine orders them.
 */
export function resolveCells(
  puzzle: Puzzle,
  solution: SolutionModel | null,
  catalog: CellTypeCatalog | null,
  effective: EffectiveHardware[] | null,
  report: HardwareReport | null,
): CellView[] {
  const out: CellView[] = []
  const v1 = puzzle.schema_version < 2
  for (const c of puzzle.cells) {
    const spec = typeSpec(catalog, c.type)
    const n = v1 ? LEGACY_PORTS : (c.port_count ?? spec?.ports.default ?? LEGACY_PORTS)
    const listed = c.ports?.map((p) => portIndex(p))
    const enabled = Array.from({ length: n }, (_, i) => (listed ? listed.includes(i) : true))
    out.push({
      id: c.id,
      type: c.type,
      player: false,
      category: spec?.category ?? (spec?.programMemory ? 'processor' : 'peripheral'),
      programmable: !!catalog?.types.find((t) => t.type === c.type)?.programmable,
      portCount: n,
      enabled,
      portFunctions: c.port_functions ?? (spec?.portFunctions ? pluginDefaultFunctions(spec, n) : undefined),
      programMemory: c.program_memory ?? 0,
      config: c.config,
    })
  }
  for (const node of solution?.hardware ?? []) {
    const spec = typeSpec(catalog, node.type)
    const eff = effective?.find((e) => e.type === node.type)
    const r = resolveNode(node, spec, eff, catalog?.types.find((t) => t.type === node.type)?.config)
    const reported = report?.nodes.find((n) => n.id === node.id && n.type === node.type && n.ports === r.portCount)
    out.push({
      id: node.id,
      type: node.type,
      player: true,
      category: spec?.category ?? 'peripheral',
      programmable: !!catalog?.types.find((t) => t.type === node.type)?.programmable,
      portCount: r.portCount,
      enabled: Array.from({ length: r.portCount }, () => true),
      portFunctions: node.port_functions ?? reported?.portFunctions ?? r.portFunctions,
      programMemory: r.programMemory,
      config: r.config,
    })
  }
  return out
}

/** Live cost of a player node: the backend's figure when current, else the mirror. */
export function nodeCost(cell: CellView, eff: EffectiveHardware | undefined, report: HardwareReport | null): number {
  const local = eff ? estimateCost(eff.cost, { ports: cell.portCount, programMemory: cell.programMemory, config: cell.config }) : 0
  const r = report?.nodes.find((n) => n.id === cell.id && n.type === cell.type)
  return r && r.ports === cell.portCount && (r.programMemory ?? 0) === (cell.programmable ? cell.programMemory : 0) && r.cost === local ? r.cost : local
}

export function totalCost(cells: CellView[], effective: EffectiveHardware[] | null, report: HardwareReport | null): number {
  return cells.filter((c) => c.player).reduce((n, c) => n + nodeCost(c, effective?.find((e) => e.type === c.type), report), 0)
}

/**
 * Which way data can flow through a port, seen from a neighbour: "in" if
 * neighbours can only write to it, "out" if they can only read, "both" for
 * processor ports. Stream inputs only send and outputs only receive.
 */
export function portDirection(cell: CellView, port: string, catalog: CellTypeCatalog | null, role?: 'input' | 'output'): 'in' | 'out' | 'both' {
  if (role === 'input') return 'out'
  if (role === 'output') return 'in'
  const i = portIndex(port)
  const fn = cell.portFunctions?.[i]
  if (fn) {
    const def = typeSpec(catalog, cell.type)?.portFunctions?.functions.find((f) => f.name === fn)
    if (def) return def.direction
  }
  return 'both'
}

/** A fresh, unused node id for a type, e.g. "cpu1" or "stack2". */
export function nextNodeId(type: string, taken: Set<string>): string {
  const prefix = type === 'standard' ? 'cpu' : type.replace(/[^A-Za-z0-9]/g, '').slice(0, 12) || 'node'
  for (let i = 1; ; i++) {
    const id = `${prefix}${i}`
    if (!taken.has(id)) return id
  }
}

export type Side = 'left' | 'right' | 'top' | 'bottom'
const SIDES: Side[] = ['left', 'right', 'top', 'bottom']

/**
 * Where each port's handle goes: P0 left, P1 right, P2 top, P3 bottom, then
 * around again, spreading ports that share a side evenly along it.
 */
export function portPlacement(n: number): { side: Side; offset: number }[] {
  const perSide = SIDES.map((_, s) => Math.max(0, Math.ceil((n - s) / 4)))
  return Array.from({ length: n }, (_, i) => {
    const s = i % 4
    const k = Math.floor(i / 4)
    return { side: SIDES[s], offset: ((k + 1) / (perSide[s] + 1)) * 100 }
  })
}
