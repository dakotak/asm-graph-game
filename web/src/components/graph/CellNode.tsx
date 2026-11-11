import clsx from 'clsx'
import { memo, useMemo } from 'react'
import { Handle, Position, type NodeProps } from '@xyflow/react'
import { useShallow } from 'zustand/react/shallow'
import type { CellSnapshot, StateField } from '../../api/types'
import { nodeCost, portPlacement, type Side } from '../../lib/hardware'
import { NODE_WIDTH } from '../../lib/layout'
import { useSim } from '../../state/sim'
import { cellStream, cellType, findCell, streamRole, useWorkspace } from '../../state/workspace'
import { formatValue } from '../ui'
import { ACTIVITY_LABEL, cellActivity, portsOf, type PortInfo } from './portState'

const SIDE_POSITION: Record<Side, Position> = { left: Position.Left, right: Position.Right, top: Position.Top, bottom: Position.Bottom }

/**
 * Port labels sit outside the node, beside the handle and off to one side of
 * the wire that leaves it, so they never overlap the border, the node's
 * content, or the connection itself.
 */
const LABEL_POSITION: Record<Position, string> = {
  [Position.Left]: 'right-full bottom-full mr-0.5 mb-0.5',
  [Position.Right]: 'left-full bottom-full ml-0.5 mb-0.5',
  [Position.Top]: 'left-full bottom-full ml-0.5 mb-0.5',
  [Position.Bottom]: 'left-full top-full ml-0.5 mt-0.5',
}

type Role = 'input' | 'output' | undefined

const ROLE_STYLE = {
  input: { label: 'INPUT', glyph: '▶', border: 'border-in/70', chip: 'bg-in/15 text-in', edge: 'border-l-4 border-l-in' },
  output: { label: 'OUTPUT', glyph: '◀', border: 'border-out/70', chip: 'bg-out/15 text-out', edge: 'border-r-4 border-r-out' },
} as const

/**
 * Where a port's handle sits: P0 left, P1 right, P2 top, P3 bottom, then
 * around again, spread evenly along each side. Data flows left to right in
 * the auto layout, so a stream cell with a single port puts it on the side
 * facing the processors: inputs on the right, outputs on the left.
 */
function handlePlacement(index: number, portCount: number, role: Role): { position: Position; offset: number } {
  if (role && portCount === 1) return { position: role === 'input' ? Position.Right : Position.Left, offset: 50 }
  const p = portPlacement(portCount)[index] ?? { side: 'left' as Side, offset: 50 }
  return { position: SIDE_POSITION[p.side], offset: p.offset }
}

export interface CellNodeData extends Record<string, unknown> {
  cellId: string
}

function CellNodeImpl({ data, selected }: NodeProps & { data: CellNodeData }) {
  const id = data.cellId
  const def = useWorkspace((s) => findCell(s.cells, id))
  const type = useWorkspace((s) => (def ? cellType(s.catalog, def.type) : undefined))
  const tests = useWorkspace((s) => s.puzzleDoc?.puzzle.tests)
  const cost = useWorkspace((s) => (def?.player ? nodeCost(def, s.hardwareCatalog?.find((e) => e.type === def.type), s.hardwareReport) : null))
  // Backend constraint violations for this node (paths hardware[i]...).
  const issues = useWorkspace(
    useShallow((s) => {
      const i = s.solution?.hardware.findIndex((n) => n.id === id) ?? -1
      return i < 0 ? [] : s.hardwareIssues.filter((d) => d.path === `hardware[${i}]` || d.path?.startsWith(`hardware[${i}].`)).map((d) => d.message)
    }),
  )
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
    () => (def ? portsOf(def, connections, snap, new Set(activeKey ? activeKey.split('|') : [])) : []),
    [def, connections, snap, activeKey],
  )
  if (!def) return null
  const activity = cellActivity(snap, started)
  const act = ACTIVITY_LABEL[activity]
  const role: Role = type?.programmable ? undefined : streamRole(def, tests)
  const rs = role ? ROLE_STYLE[role] : undefined
  const peripheral = !type?.programmable && !role
  return (
    <div
      className={clsx(
        'border bg-panel font-mono text-[11px] shadow-sm',
        rs ? ['rounded-md', rs.edge] : peripheral ? 'rounded-lg border-dashed' : 'rounded-sm',
        selected ? 'border-accent ring-1 ring-accent' : issues.length ? 'border-bad' : (rs?.border ?? 'border-line-strong'),
        activity === 'blocked' && !selected && 'border-warn/70',
        activity === 'error' && 'border-bad',
      )}
      style={{ width: NODE_WIDTH }}
      data-testid={`cell-${id}`}
      data-activity={activity}
      data-role={role}
      data-player={def.player || undefined}
    >
      <div className="flex items-center gap-2 border-b border-line bg-panel-2 px-2 py-1">
        {rs && role === 'input' && <RoleChip role={role} />}
        <span className="truncate font-semibold text-fg">{id}</span>
        {rs && role === 'output' && <RoleChip role={role} />}
        {!rs && <span className="ml-auto text-[9px] tracking-widest text-fg-muted uppercase">{def.type}</span>}
        {cost !== null && (
          <span className="shrink-0 rounded-sm bg-accent/15 px-1 text-[9px] text-accent" title="Hardware cost of this node" data-testid={`cost-${id}`}>
            ¤{cost}
          </span>
        )}
      </div>
      {issues.length > 0 && (
        <div className="border-b border-line bg-bad/10 px-2 py-0.5 text-[10px] text-bad" title={issues.join('\n')} data-testid={`issues-${id}`}>
          ✗ {issues[0]}
          {issues.length > 1 && ` (+${issues.length - 1})`}
        </div>
      )}
      <div className="space-y-1 px-2 py-1.5">
        {type?.programmable ? (
          <ProcessorBody id={id} snap={snap} started={started} memory={def.programMemory} fields={type.state} />
        ) : peripheral ? (
          <PeripheralBody snap={snap} started={started} fields={type?.state} config={def.config} />
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
        <PortHandle key={p.port} info={p} placement={handlePlacement(p.index, def.portCount, role)} role={role} />
      ))}
    </div>
  )
}

function RoleChip({ role }: { role: 'input' | 'output' }) {
  const rs = ROLE_STYLE[role]
  return (
    <span
      className={clsx('shrink-0 rounded-sm px-1 text-[9px] font-semibold tracking-widest', rs.chip, role === 'output' && 'ml-auto')}
      title={role === 'input' ? 'Input: sends the test input stream into the graph' : 'Output: collects values and checks them against the expected stream'}
    >
      {role === 'input' ? `${rs.label} ${rs.glyph}` : `${rs.glyph} ${rs.label}`}
    </span>
  )
}

function PortHandle({ info, placement, role }: { info: PortInfo; placement: { position: Position; offset: number }; role: Role }) {
  const pos = placement.position
  const vertical = pos === Position.Left || pos === Position.Right
  const styles: Record<PortInfo['visual'], string> = {
    free: role === 'input' ? '!border-in !bg-bg' : role === 'output' ? '!border-out !bg-bg' : '!border-wire !bg-bg',
    connected: '!border-wire !bg-wire',
    waiting: '!border-warn !bg-warn/40',
    active: '!border-accent !bg-accent',
  }
  const fn = info.function ? ` (${info.function})` : ''
  const title = `${info.port}${fn}: ${info.peer ? `connected to ${info.peer}` : 'available — drag to connect'}${info.visual === 'waiting' ? ' (waiting)' : info.visual === 'active' ? ' (transferring)' : ''}`
  return (
    <Handle
      id={info.port}
      type="source"
      position={pos}
      isConnectable={!info.peer}
      className={clsx('!border-2', styles[info.visual])}
      style={vertical ? { top: `${placement.offset}%` } : { left: `${placement.offset}%` }}
      title={title}
      aria-label={title}
      data-port={info.port}
      data-function={info.function}
      data-visual={info.visual}
    >
      <span className={clsx('pointer-events-none absolute rounded-sm bg-bg/80 px-0.5 font-mono text-[10px] leading-none whitespace-nowrap text-fg-muted', LABEL_POSITION[pos])}>
        {info.port}
        {info.function && <span className="ml-0.5 text-accent">{info.function}</span>}
      </span>
    </Handle>
  )
}

/**
 * Passive hardware: no program, just its stored data (e.g. a stack's
 * values, top first) and its configuration.
 */
function PeripheralBody({ snap, started, fields, config }: { snap?: CellSnapshot; started: boolean; fields?: StateField[]; config?: Record<string, unknown> }) {
  const live = started && snap ? snap.state : undefined
  const rows = stateRows(fields, live).filter((r) => r.name !== 'capacity')
  const capacity = live?.capacity ?? config?.capacity
  return (
    <>
      {rows.map((r) => {
        const arr = Array.isArray(r.value) ? (r.value as number[]) : []
        if (r.kind !== 'list') {
          return (
            <div key={r.name} className="text-fg-muted">
              {r.name} <span className={live ? 'text-fg' : 'text-fg-faint'}>{live ? formatValue(r.value) : '0'}</span>
            </div>
          )
        }
        const top = [...arr].reverse().slice(0, 8)
        return (
          <div key={r.name} title={r.summary || undefined}>
            <div className="text-fg-muted">
              {r.name}
              <span className="float-right">
                {arr.length}
                {capacity !== undefined ? `/${String(capacity)}` : ''}
              </span>
            </div>
            <div className="flex flex-wrap gap-1">
              {top.map((v, i) => (
                <span key={i} className={clsx('rounded-sm border px-1 text-[10px]', i === 0 ? 'border-accent text-fg' : 'border-line text-fg-muted')}>
                  {v}
                </span>
              ))}
              {arr.length > top.length && <span className="text-fg-faint">+{arr.length - top.length}</span>}
              {arr.length === 0 && <span className="text-fg-faint">{live ? 'empty' : 'not running'}</span>}
            </div>
          </div>
        )
      })}
    </>
  )
}

/**
 * Rows to show for a processor's state: the fields its type declares (so
 * ACC/BAK are visible before a run, at their initial value 0), followed by
 * anything else the snapshot reports.
 */
function stateRows(fields: StateField[] | undefined, state: Record<string, unknown> | undefined) {
  const declared = fields ?? []
  const rows = declared.map((f) => ({ name: f.name, kind: f.kind, summary: f.summary, value: state?.[f.name] }))
  for (const [name, value] of Object.entries(state ?? {})) {
    if (!declared.some((f) => f.name === name)) rows.push({ name, kind: Array.isArray(value) ? 'list' : 'word', summary: '', value })
  }
  return rows
}

function ProcessorBody({ id, snap, started, memory, fields }: { id: string; snap?: CellSnapshot; started: boolean; memory: number; fields?: StateField[] }) {
  const bytes = useWorkspace((s) => s.assemble[id]?.bytes ?? s.compile.programs[id]?.bytes)
  const hasErrors = useWorkspace((s) => (s.assemble[id] ? !s.assemble[id].ok : false))
  const pct = bytes !== undefined && memory > 0 ? Math.min(100, (bytes / memory) * 100) : 0
  // Before a run there is no snapshot: show declared fields at their initial values, dimmed.
  const live = started && snap ? snap.state : undefined
  const rows = stateRows(fields, live)
  const scalars = rows.filter((r) => r.kind === 'word' && (live === undefined || typeof r.value === 'number'))
  const lists = rows.filter((r) => r.kind === 'list')
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
        {scalars.length > 0 ? (
          scalars.map((r) => (
            <span key={r.name} className="text-fg-muted" title={r.summary || undefined} data-register={r.name}>
              {r.name} <span className={live ? 'text-fg' : 'text-fg-faint'}>{live ? formatValue(r.value) : '0'}</span>
            </span>
          ))
        ) : (
          <span className="text-fg-faint">no registers</span>
        )}
      </div>
      {lists.map((r) => {
        const arr = Array.isArray(r.value) ? (r.value as unknown[]) : []
        return (
          <div key={r.name} className={clsx('truncate', live ? 'text-fg-muted' : 'text-fg-faint')} title={live ? formatValue(arr) : r.summary || undefined}>
            {r.name}[{arr.length}] {arr.length ? <>top <span className="text-fg">{formatValue(arr[arr.length - 1])}</span></> : 'empty'}
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
          stream <span className="text-fg">{stream}</span>
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
          expects <span className="text-fg">{stream}</span>
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
