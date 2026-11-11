// Derives per-port presentation state from backend snapshots. Pure: it
// only interprets the CellSnapshot fields the engine reports (request,
// blocked, last, ports) — no execution semantics are re-implemented.
import type { CellSnapshot, Connection } from '../../api/types'
import { connectionId } from '../../lib/connections'
import { portName, type CellView } from '../../lib/hardware'

export type PortVisual = 'free' | 'connected' | 'waiting' | 'active'

export interface PortInfo {
  port: string
  /** port number (P3 → 3), which decides where its handle sits */
  index: number
  /** peripheral port function, e.g. "PUSH" */
  function?: string
  visual: PortVisual
  peer?: string // "cell.port"
  connection?: string // connection id
}

export function portsOf(
  cell: CellView,
  connections: Connection[],
  snap: CellSnapshot | undefined,
  activeConnections: Set<string>,
): PortInfo[] {
  const cellId = cell.id
  const waiting = waitingPorts(snap)
  // Ports the puzzle disables are not part of the cell at all, so they are
  // not shown.
  const ports: { port: string; index: number }[] = []
  cell.enabled.forEach((on, index) => on && ports.push({ port: portName(index), index }))
  return ports.map(({ port, index }) => {
    const c = connections.find((x) => (x.from.cell === cellId && x.from.port === port) || (x.to.cell === cellId && x.to.port === port))
    const peer = c ? (c.from.cell === cellId && c.from.port === port ? c.to : c.from) : undefined
    const id = c ? connectionId(c) : undefined
    let visual: PortVisual = 'free'
    if (id && activeConnections.has(id)) visual = 'active'
    else if (waiting.has(port) && (c || waiting.has('*unconnected'))) visual = 'waiting'
    else if (c) visual = 'connected'
    else if (waiting.has(port)) visual = 'waiting'
    return { port, index, function: cell.portFunctions?.[index], visual, peer: peer ? `${peer.cell}.${peer.port}` : undefined, connection: id }
  })
}

/** Ports the cell is blocked on according to its last request. */
export function waitingPorts(snap: CellSnapshot | undefined): Set<string> {
  const out = new Set<string>()
  if (!snap || !snap.blocked || !snap.request) return out
  const sel = snap.request.split(' ')[1]
  if (sel === 'ANY') {
    for (const p of snap.ports) if (p.peer) out.add(p.port)
  } else if (sel === 'LAST') {
    if (snap.last) out.add(snap.last)
  } else if (sel) {
    out.add(sel)
    out.add('*unconnected')
  }
  return out
}

export type CellActivity = 'idle' | 'ready' | 'running' | 'blocked' | 'waiting' | 'halted' | 'error' | 'done'

export function cellActivity(snap: CellSnapshot | undefined, simStarted: boolean): CellActivity {
  if (!snap || !simStarted) return 'idle'
  if (snap.error) return 'error'
  if (snap.halted) return 'halted'
  // Stream cells wait on ports by design; only processors are "blocked".
  if (snap.blocked) return snap.programmable ? 'blocked' : 'waiting'
  if (snap.stage === 'idle') return 'idle'
  return snap.instructionsCompleted > 0 || snap.stage === 'write' ? 'running' : 'ready'
}

export const ACTIVITY_LABEL: Record<CellActivity, { label: string; glyph: string; cls: string }> = {
  idle: { label: 'IDLE', glyph: '○', cls: 'text-fg-faint' },
  ready: { label: 'READY', glyph: '◇', cls: 'text-fg-muted' },
  running: { label: 'RUNNING', glyph: '●', cls: 'text-run' },
  blocked: { label: 'BLOCKED', glyph: '⏸', cls: 'text-warn' },
  waiting: { label: 'WAITING', glyph: '…', cls: 'text-fg-muted' },
  halted: { label: 'HALTED', glyph: '■', cls: 'text-fg-muted' },
  error: { label: 'ERROR', glyph: '!', cls: 'text-bad' },
  done: { label: 'DONE', glyph: '✓', cls: 'text-ok' },
}
