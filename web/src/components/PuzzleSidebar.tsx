import clsx from 'clsx'
import { controller } from '../services/controller'
import { useSim } from '../state/sim'
import { cellStream, cellType, useWorkspace } from '../state/workspace'
import { PlainText, StatusBadge } from './ui'

export function PuzzleSidebar() {
  const doc = useWorkspace((s) => s.puzzleDoc)
  const catalog = useWorkspace((s) => s.catalog)
  const selected = useWorkspace((s) => s.selectedCell)
  const programs = useWorkspace((s) => s.compile.programs)
  const assemble = useWorkspace((s) => s.assemble)
  const testIndex = useSim((s) => s.testIndex)
  const results = useSim((s) => s.results)
  if (!doc) return <div className="p-3 text-fg-muted">No puzzle open.</div>
  const p = doc.puzzle
  const t0 = p.tests[0]
  return (
    <div className="h-full overflow-auto bg-panel" data-testid="puzzle-sidebar">
      <section className="border-b border-line p-3">
        <h1 className="text-[15px] font-semibold text-fg">{p.name ?? p.id}</h1>
        <p className="font-mono text-[10px] text-fg-faint">{p.id}</p>
        {p.description && <PlainText text={p.description.trim()} className="mt-2 leading-relaxed text-fg-muted" />}
      </section>

      <section className="border-b border-line p-3">
        <h2 className="label-caps mb-2">Streams</h2>
        <ul className="space-y-1 font-mono text-[11px]">
          {Object.keys(t0?.inputs ?? {}).map((s) => (
            <li key={`in-${s}`} className="text-fg-muted">
              <span className="text-accent">IN </span>
              {s}
            </li>
          ))}
          {Object.keys(t0?.outputs ?? {}).map((s) => (
            <li key={`out-${s}`} className="text-fg-muted">
              <span className="text-ok">OUT</span> {s}
            </li>
          ))}
        </ul>
        {p.limits?.max_ticks && <p className="mt-2 font-mono text-[10px] text-fg-faint">tick limit {p.limits.max_ticks}</p>}
      </section>

      <section className="border-b border-line p-3">
        <h2 className="label-caps mb-2">Cells</h2>
        <ul className="space-y-0.5">
          {p.cells.map((c) => {
            const t = cellType(catalog, c.type)
            const bytes = assemble[c.id]?.bytes ?? programs[c.id]?.bytes
            const stream = cellStream(c)
            return (
              <li key={c.id}>
                <button
                  className={clsx(
                    'flex w-full items-center gap-2 rounded-sm px-1.5 py-1 text-left font-mono text-[11px]',
                    selected === c.id ? 'bg-accent/15 text-fg' : 'text-fg-muted hover:bg-panel-2',
                  )}
                  onClick={() => useWorkspace.getState().select(c.id)}
                  title={t?.description}
                >
                  <span className="truncate text-fg">{c.id}</span>
                  <span className="text-[9px] tracking-widest uppercase">{c.type}</span>
                  <span className="ml-auto">
                    {t?.programmable ? `${bytes ?? '?'}/${c.program_memory}B` : (stream ?? '')}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      </section>

      <section className="p-3">
        <h2 className="label-caps mb-2">Test cases ({p.tests.length})</h2>
        <ul className="space-y-0.5">
          {p.tests.map((t, i) => {
            const r = results[t.name]
            return (
              <li key={t.name}>
                <button
                  className={clsx(
                    'flex w-full items-center gap-2 rounded-sm px-1.5 py-1 text-left font-mono text-[11px]',
                    testIndex === i ? 'bg-accent/15 text-fg' : 'text-fg-muted hover:bg-panel-2',
                  )}
                  onClick={() => void controller.selectTest(i)}
                  title="Debug this test case with Run/Step"
                >
                  <span className="truncate">{t.name}</span>
                  <span className="ml-auto">{r ? <StatusBadge status={r.status} /> : <span className="text-fg-faint">—</span>}</span>
                </button>
              </li>
            )
          })}
        </ul>
      </section>
    </div>
  )
}
