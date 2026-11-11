// Client-side connection checks. These only prevent obviously invalid
// edits in the graph editor; the Go backend validates every topology again
// when a solution is loaded (graph.Build).
import type { CellDef, Connection, Endpoint } from '../api/types'

export const PHYSICAL_PORTS = ['P0', 'P1', 'P2', 'P3'] as const
export type PhysicalPort = (typeof PHYSICAL_PORTS)[number]

export function enabledPorts(cell: CellDef): Set<string> {
  return new Set(cell.ports === undefined || cell.ports === null ? PHYSICAL_PORTS : cell.ports.map((p) => p.toUpperCase()))
}

export function endpointKey(e: Endpoint): string {
  return `${e.cell}:${e.port}`
}

/** Stable id for a connection, independent of direction. */
export function connectionId(c: Connection): string {
  const a = endpointKey(c.from)
  const b = endpointKey(c.to)
  return a < b ? `${a}--${b}` : `${b}--${a}`
}

export function sameEndpoint(a: Endpoint, b: Endpoint): boolean {
  return a.cell === b.cell && a.port === b.port
}

/** Finds the connection index joining two endpoints in either direction. */
export function findConnection(conns: Connection[], a: Endpoint, b: Endpoint): number {
  return conns.findIndex(
    (c) => (sameEndpoint(c.from, a) && sameEndpoint(c.to, b)) || (sameEndpoint(c.from, b) && sameEndpoint(c.to, a)),
  )
}

/** Returns the connection using an endpoint, if any. */
export function connectionAt(conns: Connection[], e: Endpoint): Connection | undefined {
  return conns.find((c) => sameEndpoint(c.from, e) || sameEndpoint(c.to, e))
}

export type ConnectCheck = { ok: true } | { ok: false; reason: string }

/** Mirrors graph.Build's rules for a single new connection. */
export function checkConnection(cells: CellDef[], conns: Connection[], a: Endpoint, b: Endpoint): ConnectCheck {
  const ca = cells.find((c) => c.id === a.cell)
  const cb = cells.find((c) => c.id === b.cell)
  if (!ca || !cb) return { ok: false, reason: 'Unknown cell.' }
  for (const [cell, ep] of [
    [ca, a],
    [cb, b],
  ] as const) {
    if (!(PHYSICAL_PORTS as readonly string[]).includes(ep.port)) return { ok: false, reason: `${ep.port} is not a physical port.` }
    if (!enabledPorts(cell).has(ep.port)) return { ok: false, reason: `${cell.id}.${ep.port} is disabled by the puzzle.` }
    if (connectionAt(conns, ep)) return { ok: false, reason: `${cell.id}.${ep.port} is already connected.` }
  }
  if (a.cell === b.cell) return { ok: false, reason: 'A cell cannot connect to itself.' }
  return { ok: true }
}
