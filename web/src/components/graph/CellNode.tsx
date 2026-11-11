import clsx from 'clsx'
import { memo, useMemo } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { useShallow } from 'zustand/react/shallow'
import type { CellSnapshot, CellTypeInfo } from '../../api/types'
import { enabledPorts } from '../../lib/connections'
import { NODE_WIDTH } from '../../lib/layout'
import { useSim } from '../../state/sim'
import { cellStream, cellType, useWorkspace } from '../../state/workspace'
import { formatValue } from '../ui'
import { ACTIVITY_LABEL, cellActivity, portsOf, type PortInfo } from './portState'

const PORT_POSITION: Record<string, Position> = { P0: Position.Left, P1: Position.Right, P2: Position.Top, P3: Position.Bottom }

export interface CellNodeData extends Record<string, unknown> {
  cellId: string
}

function CellNodeImpl({ data, selected }: NodeProps & { data: CellNodeData }) {
  const id = data.cellId
  const def = useWorkspace((s) => s.puzzleDoc?.puzzle.cells.find((c) => c.id === id))
  const type = useWorkspace((s) => (def ? cellType(s.catalog, def.type) : undefined))
  const connections = useWorkspace(useShallow((s) => s.solution?.connections.filter((c) => c.from.cell === id || c.to.cell === id) ?? []))
  const snap = useSim((s) => s.view.cells[id])
  const started = useSim((s) => s.started)
  const tick = useSim((s) => s.view.tick)
  // Connections that carried a value in the current tick.
  const activeKey = useSim((s) => {
    const ids: string[] = []
    for (const [k, a] of Object.entries(s.edges)) {
      if (a.tick === tick && k.split('--').some((e) => e.slice(0, e.lastIndexOf(':')) === id)) ids.push(k)
    }
    return ids.sort().join('|')
  })
  const ports = useMemo(
    () => (def ? portsOf(id, enabledPorts(def), connections, snap, new Set(activeKey ? activeKey.split('|') : [])) : []),
    [def, id, connections, snap, activeKey],
  )
  if (!def) return null
  const activity = cellActivity(snap, started)
  const act = ACTIVITY_LABEL[activity]
  return (
    <div
      className={clsx(
        'rounded-sm border bg-panel font-mono text-[11px] shadow-sm',
        selected ? 'border-accent ring-1 ring-accent' : 'border-line-strong',
        activity === 'blocked' && !selected && 'border-warn/70',
        activity === 'error' && 'border-bad',
      )}
      style={{ width: NODE_WIDTH }}
      data-testid={`cell-${id}`}
      data-activity={activity}
    >
      <div className="flex items-center gap-2 border-b border-line bg-panel-2 px-2 py-1">
        <span className="truncate font-semibold text-fg">{id}</span>
        <span className="ml-auto text-[9px] tracking-widest text-fg-muted uppercase">{def.type}</span>
      </div>
      <div className="space-y-1 px-2 py-1.5">
        {type?.programmable ? (
          <ProcessorBody id={id} snap={snap} started={started} memory={def.program_memory ?? 0} />
        ) : (
          <StreamBody id={id} snap={snap} started={started} />
        )}
      </div>
      <div className={clsx('flex items-center gap-1 border-t border-line px-2 py-0.5 text-[10px]', act.cls)} title={snap?.blockReason ?? undefined}>
        <span aria-hidden>{act.glyph}</span>
        <span>{act.label}</span>
        {snap?.blocked && snap.request && <span className="ml-1 truncate text-fg-muted">{snap.request}</span>}
      </div>
      {ports.map((p) => (
        <PortHandle key={p.port} info={p} type={type} />
      ))}
    </div>
  )
}

function PortHandle({ info }: { info: PortInfo; type?: CellTypeInfo }) {
  const pos = PORT_POSITION[info.port]
  const vertical = pos === Position.Top || pos === Position.Bottom
  const styles: Record<PortInfo['visual'], string> = {
    disabled: 'border-dashed !border-fg-faint !bg-transparent opacity-50',
    free: '!border-wire !bg-bg',
    connected: '!border-wire !bg-wire',
    waiting: '!border-warn !bg-warn/40',
    active: '!border-accent !bg-accent',
  }
  const title = `${info.port}: ${
    info.visual === 'disabled' ? 'disabled by the puzzle' : info.peer ? `connected to ${info.peer}` : 'available — drag to connect'
  }${info.visual === 'waiting' ? ' (waiting)' : info.visual === 'active' ? ' (transferring)' : ''}`
  return (
    <Handle
      id={info.port}
      type="source"
      position={pos}
      isConnectable={info.enabled && info.visual !== 'connected' && !info.peer}
      className={clsx('!border-2', styles[info.visual])}
      title={title}
      aria-label={title}
      data-port={info.port}
      data-visual={info.visual}
    >
      <span
        className={clsx(
          'pointer-events-none absolute font-mono text-[9px] text-fg-muted',
          vertical ? 'left-4 top-1/2 -translate-y-1/2' : 'top-3.5 left-1/2 -translate-x-1/2',
        )}
      >
        {info.port}
        {info.visual === 'disabled' ? '×' : ''}
      </span>
    </Handle>
  )
}

function ProcessorBody({ id, snap, started, memory }: { id: string; snap?: CellSnapshot; started: boolean; memory: number }) {
  const bytes = useWorkspace((s) => s.assemble[id]?.bytes ?? s.compile.programs[id]?.bytes)
  const hasErrors = useWorkspace((s) => (s.assemble[id] ? !s.assemble[id].ok : false))
  const pct = bytes !== undefined && memory > 0 ? Math.min(100, (bytes / memory) * 100) : 0
  const scalars = snap ? Object.entries(snap.state).filter(([, v]) => typeof v === 'number') : []
  const lists = snap ? Object.entries(snap.state).filter(([, v]) => Array.isArray(v)) : []
  return (
    <>
      <div className="flex gap-2 text-fg-muted">
        <span>PC</span>
        <span className="text-fg">{started && snap ? String(snap.pc).padStart(2, '0') : '--'}</span>
        <span className="truncate text-fg" title={snap?.instruction}>
          {started && snap?.instruction ? snap.instruction : ''}
        </span>
      </div>
      <div className="flex flex-wrap gap-x-3 gap-y-0.5">
        {started && scalars.length > 0 ? (
          scalars.map(([k, v]) => (
            <span key={k} className="text-fg-muted">
              {k} <span className="text-fg">{formatValue(v)}</span>
            </span>
          ))
        ) : (
          <span className="text-fg-faint">{started ? 'no registers' : 'not running'}</span>
        )}
      </div>
      {started &&
        lists.map(([k, v]) => {
          const arr = v as unknown[]
          return (
            <div key={k} className="truncate text-fg-muted" title={formatValue(arr)}>
              {k}[{arr.length}] {arr.length ? <>top <span className="text-fg">{formatValue(arr[arr.length - 1])}</span></> : 'empty'}
            </div>
          )
        })}
      {snap?.pending?.output !== undefined && (
        <div className="text-warn">
          ↦ {snap.pending.output} → {snap.pending.destination}
        </div>
      )}
      <div className="flex items-center gap-2 pt-0.5" title="Program memory (encoded bytecode bytes, from the Go assembler)">
        <span className="text-fg-muted">CODE</span>
        <div className="h-1.5 flex-1 rounded-sm bg-panel-2">
          <div
            className={clsx('h-full rounded-sm', bytes !== undefined && bytes > memory ? 'bg-bad' : hasErrors ? 'bg-warn' : 'bg-accent/70')}
            style={{ width: `${pct}%` }}
          />
        </div>
        <span className={clsx(bytes !== undefined && bytes > memory ? 'text-bad' : 'text-fg')}>
          {bytes ?? '?'}/{memory}
        </span>
      </div>
    </>
  )
}

function StreamBody({ id, snap, started }: { id: string; snap?: CellSnapshot; started: boolean }) {
  const def = useWorkspace((s) => s.puzzleDoc?.puzzle.cells.find((c) => c.id === id))
  const test = useWorkspace((s) => s.puzzleDoc?.puzzle.tests)
  const testIndex = useSim((s) => s.testIndex)
  const input = useSim((s) => s.view.inputs[id])
  const output = useSim((s) => s.view.outputs[id])
  const stream = def ? cellStream(def) : undefined
  const t = test?.[testIndex]
  if (stream && t && stream in t.inputs) {
    const values = t.inputs[stream]
    const pos = started && input ? input.position : 0
    return (
      <>
        <div className="text-fg-muted">
          IN <span className="text-fg">{stream}</span>
          <span className="float-right">
            {pos}/{values.length}
          </span>
        </div>
        <ValueStrip values={values} cursor={pos} />
      </>
    )
  }
  if (stream && t && stream in t.outputs) {
    const expected = t.outputs[stream]
    const received = started && output ? output.received : []
    const state = started && output ? output.state : 'pending'
    return (
      <>
        <div className="text-fg-muted">
          OUT <span className="text-fg">{stream}</span>
          <span className={clsx('float-right', state === 'passed' ? 'text-ok' : state === 'failed' ? 'text-bad' : '')}>
            {state === 'passed' ? '✓ ' : state === 'failed' ? '✗ ' : ''}
            {received.length}/{expected.length}
          </span>
        </div>
        <ValueStrip values={expected} received={received} mismatch={output?.mismatch?.index} />
      </>
    )
  }
  // Unknown non-programmable plugin: show its state generically.
  const entries = snap ? Object.entries(snap.state) : []
  return (
    <div className="space-y-0.5">
      {entries.length === 0 && <span className="text-fg-faint">no state</span>}
      {entries.map(([k, v]) => (
        <div key={k} className="truncate text-fg-muted">
          {k} <span className="text-fg">{formatValue(v)}</span>
        </div>
      ))}
    </div>
  )
}

/** A compact strip of stream values; consumed/received values are marked. */
function ValueStrip({ values, cursor, received, mismatch }: { values: number[]; cursor?: number; received?: number[]; mismatch?: number }) {
  const shown = values.slice(0, 8)
  return (
    <div className="flex flex-wrap gap-1">
      {shown.map((v, i) => {
        const consumed = cursor !== undefined && i < cursor
        const got = received && i < received.length
        const bad = mismatch === i
        return (
          <span
            key={i}
            className={clsx(
              'rounded-sm border px-1 text-[10px]',
              bad ? 'border-bad text-bad' : got ? 'border-ok/60 text-ok' : consumed ? 'border-line text-fg-faint line-through' : 'border-line text-fg',
              cursor === i && 'border-accent',
            )}
          >
            {v}
          </span>
        )
      })}
      {values.length > shown.length && <span className="text-fg-faint">+{values.length - shown.length}</span>}
      {values.length === 0 && <span className="text-fg-faint">empty</span>}
    </div>
  )
}

export const CellNode = memo(CellNodeImpl)
