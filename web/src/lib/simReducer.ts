// Applies committed simulation events to a local copy of the backend
// snapshot. The backend is authoritative: events carry the committed state
// (cell.state carries the full CellSnapshot), so this reducer only copies
// data; it never computes machine behaviour. Missed events (seq gaps) are
// reported so the caller can resynchronize with getState.
import type {
  BlockData,
  CellSnapshot,
  InputSnapshot,
  OutputSnapshot,
  SimEvent,
  Snapshot,
  Status,
  TestResult,
  TickData,
  TransferData,
} from '../api/types'

export interface SimView {
  /** Snapshot metadata (cells/outputs/inputs are kept in maps below). */
  meta: Omit<Snapshot, 'cells' | 'outputs' | 'inputs'> | null
  cells: Record<string, CellSnapshot>
  outputs: Record<string, OutputSnapshot>
  inputs: Record<string, InputSnapshot>
  tick: number
  status: Status | null
  result: TestResult | null
}

export const emptyView: SimView = { meta: null, cells: {}, outputs: {}, inputs: {}, tick: 0, status: null, result: null }

export function viewFromSnapshot(s: Snapshot): SimView {
  const { cells, outputs, inputs, ...meta } = s
  const view: SimView = { meta, cells: {}, outputs: {}, inputs: {}, tick: s.tick, status: s.status, result: null }
  for (const c of cells) view.cells[c.id] = c
  for (const o of outputs) view.outputs[o.cell] = o
  for (const i of inputs) view.inputs[i.cell] = i
  return view
}

export interface Transfer extends TransferData {
  tick: number
  seq: number
}

export interface ApplyResult {
  view: SimView
  transfers: Transfer[]
  /** true when events were missed and getState should be called */
  resync: boolean
  lastSeq: number
  terminal: boolean
}

/**
 * Applies a batch of events in order. Unknown event types are ignored so
 * newer backends stay compatible.
 */
export function applyEvents(prev: SimView, lastSeq: number, events: SimEvent[]): ApplyResult {
  let view = prev
  let cellsCopied = false
  let outputsCopied = false
  let inputsCopied = false
  let resync = false
  let terminal = false
  const transfers: Transfer[] = []
  const mut = () => {
    if (view === prev) view = { ...prev }
  }
  for (const e of events) {
    if (lastSeq > 0 && e.seq !== lastSeq + 1) resync = true
    lastSeq = e.seq
    switch (e.type) {
      case 'simulation.started': {
        view = viewFromSnapshot(e.data as Snapshot)
        cellsCopied = outputsCopied = inputsCopied = true
        break
      }
      case 'cell.state': {
        const c = e.data as CellSnapshot
        mut()
        if (!cellsCopied) {
          view.cells = { ...view.cells }
          cellsCopied = true
        }
        view.cells[c.id] = c
        // Input progress is part of the input cell's plugin state.
        const inp = view.inputs[c.id]
        const pos = (c.state as { position?: unknown }).position
        if (inp && typeof pos === 'number') {
          if (!inputsCopied) {
            view.inputs = { ...view.inputs }
            inputsCopied = true
          }
          view.inputs[c.id] = { ...inp, position: pos }
        }
        break
      }
      case 'cell.blocked':
      case 'cell.unblocked': {
        const id = e.cell
        if (!id || !view.cells[id]) break
        mut()
        if (!cellsCopied) {
          view.cells = { ...view.cells }
          cellsCopied = true
        }
        const d = e.data as BlockData | undefined
        view.cells[id] = { ...view.cells[id], blocked: e.type === 'cell.blocked', blockReason: d?.reason }
        break
      }
      case 'output.validation': {
        const o = e.data as OutputSnapshot
        mut()
        if (!outputsCopied) {
          view.outputs = { ...view.outputs }
          outputsCopied = true
        }
        view.outputs[o.cell] = o
        break
      }
      case 'port.transfer':
        transfers.push({ ...(e.data as TransferData), tick: e.tick, seq: e.seq })
        break
      case 'tick.completed': {
        mut()
        view.tick = e.tick
        view.status = (e.data as TickData).status
        break
      }
      case 'simulation.completed': {
        const r = e.data as TestResult
        mut()
        view.result = r
        view.status = r.status
        view.tick = r.metrics.ticks
        const outs: Record<string, OutputSnapshot> = {}
        for (const o of r.outputs) outs[o.cell] = o
        view.outputs = { ...view.outputs, ...outs }
        outputsCopied = true
        terminal = true
        break
      }
      default:
        break
    }
  }
  return { view, transfers, resync, lastSeq, terminal }
}
