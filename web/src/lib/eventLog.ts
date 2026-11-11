// A bounded, human-readable execution log built from committed events.
import type { BlockData, EventType, InstructionData, PausedData, SimEvent, TestResult, TransferData } from '../api/types'

export interface LogEntry {
  seq: number
  tick: number
  type: EventType
  cell?: string
  /** cells involved (both ends for transfers), used by the cell filter */
  cells: string[]
  text: string
  level: 'info' | 'warn' | 'error' | 'success'
}

export const EVENT_CATEGORIES: Record<string, EventType[]> = {
  transfers: ['port.transfer'],
  instructions: ['instruction.completed'],
  blocking: ['cell.blocked', 'cell.unblocked'],
  outputs: ['output.validation'],
  lifecycle: [
    'simulation.started',
    'simulation.paused',
    'simulation.reset',
    'simulation.completed',
    'test.completed',
    'deadlock.detected',
    'cycle.detected',
    'runtime.error',
  ],
}

/** Formats an event for the log, or returns null for events not worth logging. */
export function formatEvent(e: SimEvent): LogEntry | null {
  const base = { seq: e.seq, tick: e.tick, type: e.type, cell: e.cell }
  switch (e.type) {
    case 'port.transfer': {
      const d = e.data as TransferData
      return { ...base, cells: [d.from.cell, d.to.cell], level: 'info', text: `${d.from.cell}.${d.from.port} → ${d.to.cell}.${d.to.port}  ${d.value}` }
    }
    case 'instruction.completed': {
      const d = e.data as InstructionData
      return {
        ...base,
        cells: e.cell ? [e.cell] : [],
        level: 'info',
        text: `${e.cell} executed ${d.instruction}${d.sourceLine ? ` (line ${d.sourceLine})` : ''}${d.halted ? ' — halted' : ''}`,
      }
    }
    case 'cell.blocked': {
      const d = e.data as BlockData
      return { ...base, cells: e.cell ? [e.cell] : [], level: 'warn', text: `${e.cell} blocked: ${d.reason}` }
    }
    case 'cell.unblocked':
      return { ...base, cells: e.cell ? [e.cell] : [], level: 'info', text: `${e.cell} unblocked` }
    case 'output.validation': {
      const d = e.data as { stream: string; state: string; received: unknown[]; expected: unknown[] }
      return {
        ...base,
        cells: e.cell ? [e.cell] : [],
        level: d.state === 'failed' ? 'error' : d.state === 'passed' ? 'success' : 'info',
        text: `output "${d.stream}" ${d.state} (${d.received.length}/${d.expected.length})`,
      }
    }
    case 'simulation.started':
      return { ...base, cells: [], level: 'info', text: `started test "${e.test ?? ''}"` }
    case 'simulation.reset':
      return { ...base, cells: [], level: 'info', text: 'reset' }
    case 'simulation.paused': {
      const d = e.data as PausedData
      return {
        ...base,
        cells: d.cell ? [d.cell] : [],
        level: 'warn',
        text: d.reason === 'breakpoint' ? `paused at breakpoint ${d.cell}:${d.line}` : `paused (${d.reason})`,
      }
    }
    case 'simulation.completed':
    case 'test.completed': {
      const r = e.data as TestResult
      return {
        ...base,
        cells: [],
        level: r.passed ? 'success' : 'error',
        text: `${e.type === 'test.completed' ? 'test' : 'simulation'} "${r.test}" ${r.status.replace(/_/g, ' ')}${r.message ? `: ${r.message}` : ''}`,
      }
    }
    case 'deadlock.detected':
      return { ...base, cells: [], level: 'error', text: 'deadlock detected' }
    case 'cycle.detected':
      return { ...base, cells: [], level: 'error', text: 'repeated machine state (infinite loop) detected' }
    case 'runtime.error': {
      const d = e.data as { cell: string; message: string }
      return { ...base, cells: [d.cell], level: 'error', text: `runtime error in ${d.cell}: ${d.message}` }
    }
    default:
      return null
  }
}

/** Fixed-capacity ring buffer; push is O(1) and memory is bounded. */
export class RingBuffer<T> {
  private buf: (T | undefined)[]
  private start = 0
  private len = 0
  readonly capacity: number
  constructor(capacity: number) {
    this.capacity = Math.max(1, capacity)
    this.buf = new Array(this.capacity)
  }
  push(v: T) {
    const i = (this.start + this.len) % this.capacity
    this.buf[i] = v
    if (this.len < this.capacity) this.len++
    else this.start = (this.start + 1) % this.capacity
  }
  clear() {
    this.buf = new Array(this.capacity)
    this.start = 0
    this.len = 0
  }
  get size() {
    return this.len
  }
  toArray(): T[] {
    const out: T[] = []
    for (let i = 0; i < this.len; i++) out.push(this.buf[(this.start + i) % this.capacity] as T)
    return out
  }
}
