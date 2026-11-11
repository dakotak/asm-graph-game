// Client-side connection checks. These only prevent obviously invalid
// edits in the graph editor; the Go backend validates every topology again
// when a solution is loaded (graph.Build).
import type { Connection, Endpoint } from '../api/types'
import { portIndex, portName, type CellView } from './hardware'

/** The names of a cell's enabled ports. */
export function enabledPorts(cell: CellView): Set<string> {
  const out = new Set<string>()
  cell.enabled.forEach((on, i) => on && out.add(portName(i)))
  return out
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

/** Port direction lookup, see hardware.portDirection. */
export type DirectionOf = (cell: CellView, port: string) => 'in' | 'out' | 'both'

/**
 * Mirrors graph.Build's rules for a single new connection, and also
 * refuses to join two ports that can never exchange a value (for example
 * two PUSH ports, or an input stream to a stack's POP port).
 */
export function checkConnection(cells: CellView[], conns: Connection[], a: Endpoint, b: Endpoint, direction?: DirectionOf): ConnectCheck {
  const ca = cells.find((c) => c.id === a.cell)
  const cb = cells.find((c) => c.id === b.cell)
  if (!ca || !cb) return { ok: false, reason: 'Unknown cell.' }
  for (const [cell, ep] of [
    [ca, a],
    [cb, b],
  ] as const) {
    const i = portIndex(ep.port)
    if (i < 0) return { ok: false, reason: `${ep.port} is not a physical port.` }
    if (i >= cell.portCount) return { ok: false, reason: `${cell.id} has no port ${ep.port}.` }
    if (!cell.enabled[i]) return { ok: false, reason: `${cell.id}.${ep.port} is disabled by the puzzle.` }
    if (connectionAt(conns, ep)) return { ok: false, reason: `${cell.id}.${ep.port} is already connected.` }
  }
  if (a.cell === b.cell) return { ok: false, reason: 'A cell cannot connect to itself.' }
  if (direction) {
    const da = direction(ca, a.port)
    const db = direction(cb, b.port)
    if (da !== 'both' && da === db) {
      const what = da === 'in' ? 'only receive values' : 'only send values'
      return { ok: false, reason: `${a.cell}.${a.port} and ${b.cell}.${b.port} both ${what}, so they can never exchange data.` }
    }
  }
  return { ok: true }
}
