// Detailed, generic view of one cell's committed state. Registers, stacks
// and other storage come from the plugin's Inspect() map, so new cell types
// are displayed without frontend changes.
import clsx from 'clsx'
import type { ReactNode } from 'react'
import type { Connection } from '../api/types'
import { enabledPorts } from '../lib/connections'
import { useSim } from '../state/sim'
import { cellStream, cellType, useWorkspace } from '../state/workspace'
import { formatValue } from './ui'
import { portsOf, waitingPorts } from './graph/portState'

const NO_CONNECTIONS: Connection[] = []

function Row({ k, children, mono = true }: { k: string; children: ReactNode; mono?: boolean }) {
  return (
    <div className="flex gap-3 py-0.5">
      <span className="w-24 shrink-0 text-fg-muted">{k}</span>
      <span className={clsx('min-w-0 break-words text-fg', mono && 'font-mono')}>{children}</span>
    </div>
  )
}

export function CellInspector() {
  const id = useWorkspace((s) => s.selectedCell)
  const def = useWorkspace((s) => s.puzzleDoc?.puzzle.cells.find((c) => c.id === s.selectedCell))
  const catalog = useWorkspace((s) => s.catalog)
  const connections = useWorkspace((s) => s.solution?.connections ?? NO_CONNECTIONS)
  const snap = useSim((s) => (id ? s.view.cells[id] : undefined))
  const started = useSim((s) => s.started)
  const input = useSim((s) => (id ? s.view.inputs[id] : undefined))
  const output = useSim((s) => (id ? s.view.outputs[id] : undefined))
  if (!id || !def) return <div className="p-3 text-fg-muted">Select a cell to inspect it.</div>
  const type = cellType(catalog, def.type)
  const ports = portsOf(id, enabledPorts(def), connections, started ? snap : undefined, new Set())
  const waiting = started ? waitingPorts(snap) : new Set<string>()
  return (
    <div className="h-full overflow-auto bg-panel p-3 text-[12px]" data-testid="cell-inspector">
      <div className="mb-2 flex items-baseline gap-2">
        <span className="font-mono text-[13px] font-semibold">{id}</span>
        <span className="font-mono text-[10px] tracking-widest text-fg-muted uppercase">{def.type}</span>
      </div>
      {type?.description && <p className="mb-3 text-fg-muted">{type.description}</p>}

      <h3 className="label-caps mb-1">Execution</h3>
      {!started || !snap ? (
        <p className="mb-3 text-fg-faint">Not running. Step (F10) or Run (F5) to start.</p>
      ) : (
        <div className="mb-3">
          {snap.programmable && (
            <>
              <Row k="pc">
                {snap.pc} <span className="text-fg-muted">@{snap.address}</span>
                {snap.sourceLine ? <span className="text-fg-muted"> · line {snap.sourceLine}</span> : null}
              </Row>
              <Row k="instruction">{snap.instruction ?? '—'}</Row>
            </>
          )}
          <Row k="stage">{snap.stage}</Row>
          {snap.request && <Row k="request">{snap.request}</Row>}
          {snap.blocked && (
            <Row k="blocked">
              <span className="text-warn">⏸ {snap.blockReason}</span>
            </Row>
          )}
          {snap.pending && (
            <Row k="pending">
              {snap.pending.received?.map((r) => (
                <span key={r.operand} className="mr-2">
                  received op{r.operand + 1}={r.value}
                </span>
              ))}
              {snap.pending.output !== undefined && (
                <span className="text-warn">
                  holding {snap.pending.output} for {snap.pending.destination}
                </span>
              )}
            </Row>
          )}
          {snap.last && <Row k="LAST">{snap.last}</Row>}
          {snap.halted && <Row k="halted">yes</Row>}
          {snap.error && (
            <Row k="error">
              <span className="text-bad">{snap.error}</span>
            </Row>
          )}
          <Row k="completed">{snap.instructionsCompleted} instructions</Row>
        </div>
      )}

      {started && snap && Object.keys(snap.state).length > 0 && (
        <>
          <h3 className="label-caps mb-1">Storage</h3>
          <div className="mb-3" data-testid="cell-state">
            {Object.entries(snap.state).map(([k, v]) =>
              Array.isArray(v) ? (
                <Row key={k} k={k}>
                  {v.length === 0 ? (
                    <span className="text-fg-faint">empty</span>
                  ) : (
                    <span className="flex flex-col-reverse">
                      {v.map((x, i) => (
                        <span key={i} className={clsx(i === v.length - 1 && 'text-accent')}>
                          [{i}] {formatValue(x)}
                          {i === v.length - 1 ? ' ← top' : ''}
                        </span>
                      ))}
                    </span>
                  )}
                </Row>
              ) : (
                <Row key={k} k={k}>
                  {formatValue(v)}
                </Row>
              ),
            )}
          </div>
        </>
      )}

      {input && (
        <>
          <h3 className="label-caps mb-1">Input stream</h3>
          <Row k={input.stream}>
            {input.position} / {input.length} consumed
          </Row>
        </>
      )}
      {output && (
        <>
          <h3 className="label-caps mt-2 mb-1">Output validation</h3>
          <Row k="state">
            <span className={clsx(output.state === 'passed' ? 'text-ok' : output.state === 'failed' ? 'text-bad' : 'text-fg')}>{output.state}</span>
          </Row>
          <Row k="expected">{formatValue(output.expected)}</Row>
          <Row k="received">{formatValue(output.received)}</Row>
          {output.mismatch && (
            <Row k="mismatch">
              <span className="text-bad">
                #{output.mismatch.index}: expected {output.mismatch.expected ?? '(nothing)'}, got {output.mismatch.got}
              </span>
            </Row>
          )}
        </>
      )}

      <h3 className="label-caps mt-3 mb-1">Ports</h3>
      <table className="w-full font-mono text-[11px]">
        <tbody>
          {ports.map((p) => (
            <tr key={p.port} className="border-t border-line">
              <td className="py-0.5 pr-2">{p.port}</td>
              <td className="pr-2">{p.peer ? `↔ ${p.peer}` : 'free'}</td>
              <td className="text-warn">{waiting.has(p.port) ? 'waiting' : ''}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {def.config && Object.keys(def.config).length > 0 && (
        <>
          <h3 className="label-caps mt-3 mb-1">Config</h3>
          {Object.entries(def.config).map(([k, v]) => (
            <Row key={k} k={k}>
              {formatValue(v)}
            </Row>
          ))}
        </>
      )}
      {cellStream(def) === undefined && type?.programmable && (
        <p className="mt-3 text-[11px] text-fg-faint">Program memory: {def.program_memory} bytes</p>
      )}
    </div>
  )
}
