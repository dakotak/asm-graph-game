import clsx from 'clsx'
import { useEffect, useMemo, useRef, useState } from 'react'
import type { Diagnostic, OutputSnapshot, TestResult } from '../api/types'
import { controller } from '../services/controller'
import { EVENT_CATEGORIES } from '../lib/eventLog'
import { usePrefs } from '../state/prefs'
import { useSim } from '../state/sim'
import { revealLine, useUi, type BottomTab } from '../state/ui'
import { useWorkspace } from '../state/workspace'
import { Button, StatusBadge, Tabs } from './ui'

export function BottomPanel() {
  const tab = useUi((s) => s.bottomTab)
  const nDiag = useWorkspace((s) => s.diagnostics.filter((d) => d.severity === 'error').length)
  const nLog = useSim((s) => s.log.length)
  const tabs: { id: BottomTab; label: React.ReactNode }[] = [
    { id: 'tests', label: 'Tests' },
    { id: 'diagnostics', label: <>Diagnostics{nDiag > 0 && <span className="ml-1 text-bad">{nDiag}</span>}</> },
    { id: 'events', label: <>Events{nLog > 0 && <span className="ml-1 text-fg-faint">{nLog}</span>}</> },
    { id: 'program', label: 'Bytecode' },
  ]
  return (
    <div className="flex h-full flex-col bg-panel">
      <Tabs tabs={tabs} value={tab} onChange={(t) => useUi.setState({ bottomTab: t })} />
      <div className="min-h-0 flex-1 overflow-auto">
        {tab === 'tests' && <TestsPanel />}
        {tab === 'diagnostics' && <DiagnosticsPanel />}
        {tab === 'events' && <EventsPanel />}
        {tab === 'program' && <BytecodePanel />}
      </div>
    </div>
  )
}

// --- tests ---------------------------------------------------------------

export function TestsPanel() {
  const tests = useWorkspace((s) => s.puzzleDoc?.puzzle.tests)
  const results = useSim((s) => s.results)
  const running = useSim((s) => s.testsRunning)
  const conn = useSim((s) => s.conn)
  const cycles = usePrefs((s) => s.cycleDetection)
  const [open, setOpen] = useState<string | null>(null)
  if (!tests) return <p className="p-3 text-fg-muted">No puzzle open.</p>
  const done = tests.filter((t) => results[t.name])
  const passed = done.filter((t) => results[t.name].passed).length
  return (
    <div data-testid="tests-panel">
      <div className="flex items-center gap-2 border-b border-line px-3 py-1.5">
        <Button onClick={() => void controller.runTests()} disabled={conn !== 'open' || running.length > 0}>
          {running.length > 0 ? 'Running…' : 'Run all tests'}
        </Button>
        <label className="flex items-center gap-1 text-[11px] text-fg-muted" title="Detect infinite loops exactly (uses more memory)">
          <input type="checkbox" checked={cycles} onChange={(e) => usePrefs.getState().set({ cycleDetection: e.target.checked })} />
          cycle detection
        </label>
        {done.length > 0 && (
          <span className={clsx('ml-auto font-mono text-[11px]', passed === tests.length ? 'text-ok' : 'text-fg-muted')} data-testid="tests-summary">
            {passed}/{tests.length} passed
          </span>
        )}
      </div>
      <table className="w-full text-[12px]">
        <thead>
          <tr className="text-left">
            {['', 'Test', 'Status', 'Ticks', 'Instr.', 'Transfers', 'Outputs', ''].map((h, i) => (
              <th key={i} className="label-caps border-b border-line px-2 py-1 font-normal">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {tests.map((t, i) => {
            const r = results[t.name]
            const isRunning = running.includes(t.name)
            const expanded = open === t.name
            return (
              <TestRow
                key={t.name}
                name={t.name}
                index={i}
                result={r}
                running={isRunning}
                expanded={expanded}
                onToggle={() => setOpen(expanded ? null : t.name)}
                expectedOutputs={t.outputs}
              />
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function TestRow({
  name,
  index,
  result: r,
  running,
  expanded,
  onToggle,
  expectedOutputs,
}: {
  name: string
  index: number
  result?: TestResult
  running: boolean
  expanded: boolean
  onToggle: () => void
  expectedOutputs: Record<string, number[]>
}) {
  const conn = useSim((s) => s.conn)
  return (
    <>
      <tr className={clsx('border-b border-line hover:bg-panel-2', r && !r.passed && 'bg-bad/5')} data-testid={`test-row-${name}`}>
        <td className="w-6 px-2">
          <button onClick={onToggle} aria-label={expanded ? 'Collapse' : 'Expand'} aria-expanded={expanded} className="text-fg-muted">
            {expanded ? '▾' : '▸'}
          </button>
        </td>
        <td className="px-2 py-1 font-mono">{name}</td>
        <td className="px-2">{running ? <span className="font-mono text-[11px] text-run">running…</span> : r ? <StatusBadge status={r.status} /> : <span className="text-fg-faint">—</span>}</td>
        <td className="px-2 font-mono">{r?.metrics.ticks ?? ''}</td>
        <td className="px-2 font-mono">{r?.metrics.instructions ?? ''}</td>
        <td className="px-2 font-mono">{r?.metrics.transfers ?? ''}</td>
        <td className="px-2 font-mono text-[11px] text-fg-muted">
          {r ? r.outputs.map((o) => `${o.stream} ${o.received.length}/${o.expected.length}`).join(', ') : Object.keys(expectedOutputs).join(', ')}
        </td>
        <td className="px-2 text-right whitespace-nowrap">
          <Button variant="ghost" disabled={conn !== 'open' || running} onClick={() => void controller.runTest(name)} title="Run this test on a fresh machine">
            Run
          </Button>
          <Button
            variant="ghost"
            disabled={conn !== 'open'}
            onClick={() => void controller.start(index)}
            title="Load this test into the debugger"
          >
            Debug
          </Button>
        </td>
      </tr>
      {expanded && (
        <tr className="border-b border-line bg-bg/40">
          <td />
          <td colSpan={7} className="px-2 py-2">
            {r ? <TestDetails r={r} /> : <ExpectedOnly outputs={expectedOutputs} />}
          </td>
        </tr>
      )}
    </>
  )
}

function ExpectedOnly({ outputs }: { outputs: Record<string, number[]> }) {
  return (
    <div className="font-mono text-[11px] text-fg-muted">
      Not run yet. Expected:{' '}
      {Object.entries(outputs).map(([k, v]) => (
        <span key={k} className="mr-3">
          {k} = [{v.join(', ')}]
        </span>
      ))}
    </div>
  )
}

function TestDetails({ r }: { r: TestResult }) {
  return (
    <div className="space-y-2 text-[12px]">
      {r.message && <p className={clsx(r.passed ? 'text-fg-muted' : 'text-bad')}>{r.message}</p>}
      {r.outputs.map((o) => (
        <OutputCompare key={o.cell} o={o} />
      ))}
      {r.error && (
        <p className="text-bad">
          Runtime error in{' '}
          <button className="underline" onClick={() => r.error?.sourceLine && jump(r.error.cell, r.error.sourceLine)}>
            {r.error.cell} line {r.error.sourceLine}
          </button>
          : {r.error.instruction} — {r.error.message}
        </p>
      )}
      {r.cycle && (
        <p className="text-bad">
          Infinite loop: state at tick {r.cycle.repeatTick} repeats tick {r.cycle.firstTick} (period {r.cycle.period}).
        </p>
      )}
      {r.blocked && r.blocked.length > 0 && (
        <div>
          <p className="label-caps mb-1">Blocked cells</p>
          {r.blocked.map((b) => (
            <p key={b.cell} className="font-mono text-[11px] text-warn">
              <button className="underline" onClick={() => b.sourceLine && jump(b.cell, b.sourceLine)}>
                {b.cell}:{b.sourceLine}
              </button>{' '}
              {b.instruction} — {b.reason}
            </p>
          ))}
        </div>
      )}
      {r.notes?.map((n) => (
        <p key={n} className="text-fg-muted">
          note: {n}
        </p>
      ))}
    </div>
  )
}

/** Expected vs actual values, aligned by index, with the first mismatch marked. */
export function OutputCompare({ o }: { o: OutputSnapshot }) {
  const n = Math.max(o.expected.length, o.received.length)
  return (
    <div>
      <p className="mb-0.5 font-mono text-[11px]">
        <span className="text-fg-muted">output</span> {o.stream}{' '}
        <span className={clsx(o.state === 'passed' ? 'text-ok' : o.state === 'failed' ? 'text-bad' : 'text-fg-muted')}>{o.state}</span>
      </p>
      <div className="overflow-x-auto">
        <table className="font-mono text-[11px]" data-testid={`compare-${o.stream}`}>
          <tbody>
            <tr>
              <td className="pr-2 text-fg-muted">expected</td>
              {Array.from({ length: n }, (_, i) => (
                <td key={i} className={clsx('border border-line px-1.5 text-center', o.mismatch?.index === i && 'bg-bad/15')}>
                  {i < o.expected.length ? o.expected[i] : <span className="text-fg-faint">∅</span>}
                </td>
              ))}
            </tr>
            <tr>
              <td className="pr-2 text-fg-muted">actual</td>
              {Array.from({ length: n }, (_, i) => {
                const has = i < o.received.length
                const bad = o.mismatch?.index === i
                const good = has && !bad && o.expected[i] === o.received[i]
                return (
                  <td key={i} className={clsx('border border-line px-1.5 text-center', bad && 'bg-bad/15 text-bad', good && 'text-ok')}>
                    {has ? o.received[i] : <span className="text-fg-faint">·</span>}
                  </td>
                )
              })}
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  )
}

function jump(cell: string, line: number) {
  useWorkspace.getState().select(cell)
  setTimeout(() => revealLine(cell, line), 0)
}

// --- diagnostics -----------------------------------------------------------

export function DiagnosticsPanel() {
  const diags = useWorkspace((s) => s.diagnostics)
  const assemble = useWorkspace((s) => s.assemble)
  // Live assembler errors for every cell, plus the last compile/validation result.
  const live: Diagnostic[] = useMemo(
    () => Object.entries(assemble).flatMap(([cell, r]) => r.diagnostics.map((d) => ({ ...d, cell }))),
    [assemble],
  )
  const all = useMemo(() => {
    const seen = new Set<string>()
    return [...diags, ...live].filter((d) => {
      const k = `${d.cell}|${d.sourceLine}|${d.path}|${d.message}`
      if (seen.has(k)) return false
      seen.add(k)
      return true
    })
  }, [diags, live])
  if (all.length === 0) return <p className="p-3 text-fg-muted" data-testid="diagnostics-panel">No problems. Compile (Mod+Shift+B) to validate the whole solution.</p>
  return (
    <ul className="divide-y divide-line" data-testid="diagnostics-panel">
      {all.map((d, i) => (
        <li key={i}>
          <button
            className="flex w-full items-start gap-2 px-3 py-1 text-left hover:bg-panel-2"
            onClick={() => d.cell && d.sourceLine && jump(d.cell, d.sourceLine)}
          >
            <span className={clsx('font-mono', d.severity === 'error' ? 'text-bad' : 'text-warn')} aria-label={d.severity}>
              {d.severity === 'error' ? '✗' : '!'}
            </span>
            <span className="w-40 shrink-0 truncate font-mono text-[11px] text-fg-muted">
              {d.cell ? `${d.cell}${d.sourceLine ? `:${d.sourceLine}:${d.sourceColumn ?? 1}` : ''}` : (d.path ?? d.file ?? '')}
            </span>
            <span className="text-fg">{d.message}</span>
          </button>
        </li>
      ))}
    </ul>
  )
}

// --- events ------------------------------------------------------------------

const MAX_RENDERED = 400

export function EventsPanel() {
  const log = useSim((s) => s.log)
  const cells = useWorkspace((s) => s.puzzleDoc?.puzzle.cells)
  const cellFilter = usePrefs((s) => s.logFilterCell)
  const catFilter = usePrefs((s) => s.logFilterCategory)
  const ref = useRef<HTMLDivElement>(null)
  const stick = useRef(true)
  const filtered = useMemo(() => {
    const types = catFilter ? new Set(EVENT_CATEGORIES[catFilter] ?? []) : null
    const out = log.filter((e) => (!cellFilter || e.cells.includes(cellFilter)) && (!types || types.has(e.type)))
    return out.slice(-MAX_RENDERED)
  }, [log, cellFilter, catFilter])
  useEffect(() => {
    const el = ref.current
    if (el && stick.current) el.scrollTop = el.scrollHeight
  }, [filtered])
  return (
    <div className="flex h-full flex-col" data-testid="events-panel">
      <div className="flex items-center gap-2 border-b border-line px-3 py-1">
        <select
          className="h-6 rounded-sm border border-line bg-panel-2 px-1 font-mono text-[11px]"
          value={cellFilter}
          onChange={(e) => usePrefs.getState().set({ logFilterCell: e.target.value })}
          aria-label="Filter by cell"
        >
          <option value="">all cells</option>
          {cells?.map((c) => (
            <option key={c.id} value={c.id}>
              {c.id}
            </option>
          ))}
        </select>
        <select
          className="h-6 rounded-sm border border-line bg-panel-2 px-1 font-mono text-[11px]"
          value={catFilter}
          onChange={(e) => usePrefs.getState().set({ logFilterCategory: e.target.value })}
          aria-label="Filter by event type"
        >
          <option value="">all events</option>
          {Object.keys(EVENT_CATEGORIES).map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
        <span className="ml-auto font-mono text-[10px] text-fg-faint">
          last {log.length} events (bounded) · runs at maximum speed log lifecycle events only
        </span>
      </div>
      <div
        ref={ref}
        className="min-h-0 flex-1 overflow-auto font-mono text-[11px]"
        onScroll={(e) => {
          const el = e.currentTarget
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24
        }}
      >
        {filtered.length === 0 && <p className="p-3 font-sans text-[12px] text-fg-muted">No events yet. Step or run a simulation.</p>}
        {filtered.map((e) => (
          <div key={e.seq} className="flex gap-3 px-3 hover:bg-panel-2">
            <span className="w-16 shrink-0 text-right text-fg-faint">tick {e.tick}</span>
            <span
              className={clsx(
                e.level === 'error' && 'text-bad',
                e.level === 'warn' && 'text-warn',
                e.level === 'success' && 'text-ok',
                e.level === 'info' && 'text-fg',
              )}
            >
              {e.text}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

// --- bytecode ----------------------------------------------------------------

export function BytecodePanel() {
  const cell = useWorkspace((s) => s.selectedCell)
  const r = useWorkspace((s) => (cell ? s.assemble[cell] : undefined))
  const pc = useSim((s) => (cell && s.started ? s.view.cells[cell]?.pc : undefined))
  if (!cell || !r) return <p className="p-3 text-fg-muted">Select a programmable cell to see its compiled bytecode.</p>
  if (!r.disassembly) return <p className="p-3 text-fg-muted">The program does not assemble; see Diagnostics.</p>
  return (
    <div className="p-3" data-testid="bytecode-panel">
      <p className="mb-2 font-mono text-[11px] text-fg-muted">
        {cell}: {r.bytes} bytes, {r.instructions} instructions · bytecode v1 · <span className="text-fg">{r.hex || '(empty)'}</span>
      </p>
      <table className="font-mono text-[11px]">
        <thead>
          <tr className="text-left text-fg-muted">
            <th className="pr-4 font-normal">#</th>
            <th className="pr-4 font-normal">addr</th>
            <th className="pr-4 font-normal">bytes</th>
            <th className="pr-4 font-normal">instruction</th>
            <th className="font-normal">line</th>
          </tr>
        </thead>
        <tbody>
          {r.disassembly.map((d) => (
            <tr key={d.index} className={clsx(pc === d.index && 'bg-run/15')}>
              <td className="pr-4 text-fg-faint">{pc === d.index ? '▶' : d.index}</td>
              <td className="pr-4">@{d.address}</td>
              <td className="pr-4 text-fg-muted">{d.bytes.match(/../g)?.join(' ')}</td>
              <td className="pr-4">{d.text}</td>
              <td className="text-fg-faint">{d.sourceLine}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
