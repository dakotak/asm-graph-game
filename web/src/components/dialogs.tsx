import clsx from 'clsx'
import { useEffect, useMemo, useRef, useState } from 'react'
import { http } from '../api/http'
import type { SolutionInfo } from '../api/types'
import { commands, confirmDiscard, displayKeys, exportSolution, importSolution, runCommand } from '../commands'
import { controller } from '../services/controller'
import { solvedPuzzles } from '../state/prefs'
import { dismissNotice, useSim } from '../state/sim'
import { useUi } from '../state/ui'
import { useWorkspace } from '../state/workspace'
import { Button, Kbd, Modal, formatValue } from './ui'

// --- puzzle browser --------------------------------------------------------

export function PuzzleBrowser() {
  const open = useUi((s) => s.browser)
  const puzzles = useWorkspace((s) => s.puzzles)
  const current = useWorkspace((s) => s.puzzleDoc?.puzzle.id)
  const [q, setQ] = useState('')
  useEffect(() => {
    if (open) void controller.refreshPuzzles()
  }, [open])
  const solved = useMemo(() => (open ? solvedPuzzles() : new Set<string>()), [open])
  if (!open) return null
  const close = () => useUi.setState({ browser: false })
  const list = puzzles.filter((p) => `${p.name} ${p.id} ${p.description}`.toLowerCase().includes(q.toLowerCase()))
  const choose = async (id: string) => {
    if (id !== current && !(await confirmDiscard('Opening another puzzle'))) return
    close()
    if (id !== current) await controller.openPuzzle(id)
  }
  return (
    <Modal title="Puzzles" onClose={close} width="max-w-3xl">
      <div className="border-b border-line p-2">
        <input
          className="h-8 w-full rounded-sm border border-line bg-bg px-2 text-fg outline-none focus:border-accent"
          placeholder="Search puzzles…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          aria-label="Search puzzles"
        />
      </div>
      <ul className="divide-y divide-line" data-testid="puzzle-list">
        {list.length === 0 && <li className="p-4 text-fg-muted">No puzzles found. Is the server's --puzzles directory correct?</li>}
        {list.map((p) => (
          <li key={p.id}>
            <button
              className={clsx('block w-full px-3 py-2.5 text-left hover:bg-panel-2', p.id === current && 'bg-accent/10', !p.valid && 'opacity-70')}
              onClick={() => void choose(p.id)}
              disabled={!p.valid}
              data-testid={`puzzle-${p.id}`}
            >
              <div className="flex items-baseline gap-2">
                <span className="font-semibold text-fg">{p.name}</span>
                <span className="font-mono text-[10px] text-fg-faint">{p.id}</span>
                {(solved.has(p.id) || p.bests) && <span className="text-[11px] text-ok">✓ solved</span>}
                {p.hardware && <span className="rounded-sm bg-accent/15 px-1 text-[10px] text-accent">build your hardware</span>}
                {!p.valid && <span className="text-[11px] text-bad">✗ invalid: {p.diagnostics?.[0]?.message}</span>}
                <span className="ml-auto font-mono text-[11px] text-fg-muted">
                  {p.difficulty !== undefined && <>difficulty {formatValue(p.difficulty)} · </>}
                  {p.programmableCells} programmable / {p.cells} cells · {p.tests} tests
                  {p.solutions > 0 && <> · {p.solutions} saved</>}
                </span>
              </div>
              {p.description && <p className="mt-0.5 line-clamp-2 whitespace-pre-wrap text-fg-muted">{p.description.trim()}</p>}
              <div className="mt-1 flex items-center gap-1">
                {p.cellTypes.map((t) => (
                  <span key={t} className="rounded-sm border border-line px-1 font-mono text-[10px] text-fg-muted">
                    {t}
                  </span>
                ))}
                {p.bests && (
                  <span className="ml-auto font-mono text-[10px] text-fg-muted" title="Personal bests (each dimension separately)">
                    best ¤{p.bests.cost?.score.cost ?? '—'} · {p.bests.ticks?.score.ticks ?? '—'} ticks · {p.bests.bytes?.score.bytes ?? '—'} bytes
                  </span>
                )}
              </div>
            </button>
          </li>
        ))}
      </ul>
    </Modal>
  )
}

// --- saved solutions --------------------------------------------------------

export function SolutionsDialog() {
  const open = useUi((s) => s.solutions)
  const puzzleId = useWorkspace((s) => s.puzzleDoc?.puzzle.id)
  const current = useWorkspace((s) => s.solutionName)
  const [list, setList] = useState<SolutionInfo[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  useEffect(() => {
    if (!open || !puzzleId) return
    setList(null)
    http.listSolutions(puzzleId).then(setList, (e) => setError((e as Error).message))
  }, [open, puzzleId])
  if (!open) return null
  const close = () => useUi.setState({ solutions: false })
  return (
    <Modal
      title={`Solutions — ${puzzleId ?? ''}`}
      onClose={close}
      footer={
        <>
          <Button onClick={() => (close(), runCommand('new-solution'))}>New empty</Button>
          <Button onClick={() => (close(), importSolution())}>Import YAML…</Button>
          <Button onClick={() => (close(), exportSolution())}>Export YAML</Button>
          <Button variant="primary" onClick={() => (close(), runCommand('save-copy'))}>
            Save a copy…
          </Button>
        </>
      }
    >
      {error && <p className="p-3 text-bad">{error}</p>}
      {!list && !error && <p className="p-3 text-fg-muted">Loading…</p>}
      {list && list.length === 0 && <p className="p-3 text-fg-muted">No saved solutions yet. Save with Mod+S.</p>}
      <ul className="divide-y divide-line">
        {list?.map((s) => (
          <li key={s.name}>
            <button
              className={clsx('flex w-full items-center gap-3 px-3 py-2 text-left hover:bg-panel-2', s.name === current && 'bg-accent/10')}
              onClick={async () => {
                if (!(await confirmDiscard('Opening another solution'))) return
                close()
                await controller.loadSolution(s.name)
              }}
            >
              <span className="font-mono text-fg">{s.name}</span>
              {s.name === current && <span className="text-[11px] text-accent">current</span>}
              <span className="ml-auto text-[11px] text-fg-muted">{new Date(s.modified).toLocaleString()}</span>
            </button>
          </li>
        ))}
      </ul>
    </Modal>
  )
}

// --- command palette -------------------------------------------------------

interface PaletteItem {
  id: string
  title: string
  hint?: string
  enabled: boolean
  run: () => unknown
}

function fuzzy(q: string, text: string): boolean {
  const t = text.toLowerCase()
  let i = 0
  for (const ch of q.toLowerCase()) {
    i = t.indexOf(ch, i)
    if (i < 0) return false
    i++
  }
  return true
}

export function CommandPalette() {
  const open = useUi((s) => s.palette)
  const puzzles = useWorkspace((s) => s.puzzles)
  const cells = useWorkspace((s) => s.cells)
  const [q, setQ] = useState('')
  const [sel, setSel] = useState(0)
  const listRef = useRef<HTMLUListElement>(null)
  useEffect(() => {
    if (open) {
      setQ('')
      setSel(0)
    }
  }, [open])
  const items: PaletteItem[] = useMemo(() => {
    if (!open) return []
    const base: PaletteItem[] = commands.map((c) => ({
      id: c.id,
      title: `${c.group}: ${c.title}`,
      hint: c.keys ? displayKeys(c.keys) : undefined,
      enabled: c.enabled?.() ?? true,
      run: c.run,
    }))
    for (const c of cells ?? []) {
      base.push({ id: `cell:${c.id}`, title: `Go to cell: ${c.id} (${c.type})`, enabled: true, run: () => useWorkspace.getState().select(c.id) })
    }
    for (const p of puzzles) {
      base.push({
        id: `puzzle:${p.id}`,
        title: `Open puzzle: ${p.name}`,
        enabled: p.valid,
        run: async () => {
          if (await confirmDiscard('Opening another puzzle')) await controller.openPuzzle(p.id)
        },
      })
    }
    return base
  }, [open, cells, puzzles])
  const filtered = items.filter((i) => fuzzy(q, i.title))
  if (!open) return null
  const close = () => useUi.setState({ palette: false })
  const exec = (item: PaletteItem | undefined) => {
    if (!item || !item.enabled) return
    close()
    void item.run()
  }
  return (
    <div className="fixed inset-0 z-50 flex justify-center bg-black/40 pt-[12vh]" onMouseDown={(e) => e.target === e.currentTarget && close()}>
      <div role="dialog" aria-label="Command palette" className="h-fit w-full max-w-xl rounded-sm border border-line-strong bg-panel shadow-2xl">
        <input
          autoFocus
          className="h-10 w-full border-b border-line bg-transparent px-3 text-[14px] text-fg outline-none"
          placeholder="Type a command, cell or puzzle…"
          value={q}
          onChange={(e) => {
            setQ(e.target.value)
            setSel(0)
          }}
          onKeyDown={(e) => {
            if (e.key === 'Escape') close()
            else if (e.key === 'ArrowDown') {
              e.preventDefault()
              setSel((s) => Math.min(s + 1, filtered.length - 1))
            } else if (e.key === 'ArrowUp') {
              e.preventDefault()
              setSel((s) => Math.max(s - 1, 0))
            } else if (e.key === 'Enter') exec(filtered[sel])
          }}
          role="combobox"
          aria-expanded="true"
          aria-controls="palette-list"
          aria-activedescendant={filtered[sel] ? `palette-${filtered[sel].id}` : undefined}
        />
        <ul ref={listRef} id="palette-list" role="listbox" className="max-h-[50vh] overflow-auto py-1">
          {filtered.slice(0, 60).map((item, i) => (
            <li
              key={item.id}
              id={`palette-${item.id}`}
              role="option"
              aria-selected={i === sel}
              aria-disabled={!item.enabled}
              className={clsx(
                'flex cursor-pointer items-center px-3 py-1.5',
                i === sel && 'bg-accent/15',
                !item.enabled && 'opacity-40',
              )}
              onMouseEnter={() => setSel(i)}
              onClick={() => exec(item)}
              ref={(el) => {
                if (i === sel) el?.scrollIntoView?.({ block: 'nearest' })
              }}
            >
              <span>{item.title}</span>
              {item.hint && (
                <span className="ml-auto">
                  <Kbd>{item.hint}</Kbd>
                </span>
              )}
            </li>
          ))}
          {filtered.length === 0 && <li className="px-3 py-2 text-fg-muted">No matches.</li>}
        </ul>
      </div>
    </div>
  )
}

// --- help ------------------------------------------------------------------

export function HelpDialog() {
  const open = useUi((s) => s.help)
  const catalog = useWorkspace((s) => s.catalog)
  if (!open) return null
  const groups = ['Run', 'Solution', 'Navigate', 'View', 'Help'] as const
  return (
    <Modal title="Help & keyboard shortcuts" onClose={() => useUi.setState({ help: false })} width="max-w-3xl">
      <div className="grid grid-cols-2 gap-6 p-4">
        <div>
          {groups.map((g) => (
            <section key={g} className="mb-3">
              <h3 className="label-caps mb-1">{g}</h3>
              <table className="w-full text-[12px]">
                <tbody>
                  {commands
                    .filter((c) => c.group === g)
                    .map((c) => (
                      <tr key={c.id}>
                        <td className="py-0.5 pr-3">{c.title}</td>
                        <td className="text-right">{c.keys ? <Kbd>{displayKeys(c.keys)}</Kbd> : <span className="text-fg-faint">palette</span>}</td>
                      </tr>
                    ))}
                </tbody>
              </table>
            </section>
          ))}
          <section>
            <h3 className="label-caps mb-1">Editor</h3>
            <table className="w-full text-[12px]">
              <tbody>
                <tr>
                  <td className="py-0.5">Toggle breakpoint (or click the gutter)</td>
                  <td className="text-right">
                    <Kbd>F9</Kbd>
                  </td>
                </tr>
                <tr>
                  <td className="py-0.5">Completion / instruction help</td>
                  <td className="text-right">
                    <Kbd>Ctrl Space</Kbd>
                  </td>
                </tr>
              </tbody>
            </table>
          </section>
        </div>
        <div className="space-y-3 text-[12px] text-fg-muted">
          <section>
            <h3 className="label-caps mb-1">How it works</h3>
            <p>
              Connect ports by dragging from one port square to another. Select a wire and press Delete to remove it. Write assembly for each
              processor, Compile, then Step (F10) or Run (F5). Run tests runs every test case on a fresh machine.
            </p>
            <p className="mt-1">
              Everything is computed by the Go engine: sizes come from the real assembler, and execution, blocking and validation from the
              simulator. Editing the solution resets the running simulation.
            </p>
          </section>
          <section>
            <h3 className="label-caps mb-1">Port legend</h3>
            <ul className="space-y-0.5">
              <li>▢ hollow — available, not connected</li>
              <li>■ filled — connected</li>
              <li>
                <span className="text-warn">■</span> amber — cell is waiting on this port
              </li>
              <li>
                <span className="text-accent">■</span> accent — value transferred this tick
              </li>
              <li>Ports the puzzle disables are not shown.</li>
            </ul>
          </section>
          {catalog && (
            <section>
              <h3 className="label-caps mb-1">Ports in assembly</h3>
              <ul className="space-y-0.5">
                {catalog.ports.map((p) => (
                  <li key={p.name}>
                    <span className="font-mono text-fg">{p.name}</span> — {p.description}
                  </li>
                ))}
              </ul>
            </section>
          )}
        </div>
      </div>
    </Modal>
  )
}

// --- confirm / prompt --------------------------------------------------------

export function ModalHost() {
  const m = useUi((s) => s.modal)
  const [value, setValue] = useState('')
  useEffect(() => setValue(m?.defaultValue ?? ''), [m])
  if (!m) return null
  const finish = (v: string | boolean | null) => {
    useUi.setState({ modal: null })
    m.resolve(v)
  }
  const error = m.kind === 'prompt' && m.validate ? m.validate(value) : null
  const submit = () => (m.kind === 'confirm' ? finish(true) : !error && finish(value))
  return (
    <Modal
      title={m.title}
      onClose={() => finish(m.kind === 'confirm' ? false : null)}
      footer={
        <>
          <Button onClick={() => finish(m.kind === 'confirm' ? false : null)}>Cancel</Button>
          <Button variant={m.danger ? 'danger' : 'primary'} onClick={submit} disabled={!!error} data-testid="modal-confirm">
            {m.confirmLabel ?? 'OK'}
          </Button>
        </>
      }
    >
      <form
        className="space-y-2 p-3"
        onSubmit={(e) => {
          e.preventDefault()
          submit()
        }}
      >
        {m.message && <p className="text-fg-muted">{m.message}</p>}
        {m.kind === 'prompt' && (
          <>
            <input
              className="h-8 w-full rounded-sm border border-line bg-bg px-2 font-mono text-fg outline-none focus:border-accent"
              value={value}
              onChange={(e) => setValue(e.target.value)}
              aria-label={m.title}
              data-testid="modal-input"
            />
            {error && <p className="text-[11px] text-bad">{error}</p>}
          </>
        )}
      </form>
    </Modal>
  )
}

// --- notices & connection ------------------------------------------------------

export function Notices() {
  const notices = useSim((s) => s.notices)
  return (
    <div className="pointer-events-none fixed right-3 bottom-3 z-40 flex w-96 flex-col gap-2" aria-live="polite">
      {notices.map((n) => (
        <div
          key={n.id}
          role={n.level === 'error' ? 'alert' : 'status'}
          className={clsx(
            'pointer-events-auto flex gap-2 rounded-sm border bg-panel px-3 py-2 text-[12px] shadow-lg',
            n.level === 'error' && 'border-bad/60',
            n.level === 'warn' && 'border-warn/60',
            n.level === 'success' && 'border-ok/60',
            n.level === 'info' && 'border-line-strong',
          )}
        >
          <span
            aria-hidden
            className={clsx(n.level === 'error' && 'text-bad', n.level === 'warn' && 'text-warn', n.level === 'success' && 'text-ok', n.level === 'info' && 'text-accent')}
          >
            {n.level === 'error' ? '✗' : n.level === 'warn' ? '!' : n.level === 'success' ? '✓' : 'i'}
          </span>
          <span className="flex-1 text-fg">{n.text}</span>
          <button className="text-fg-muted hover:text-fg" onClick={() => dismissNotice(n.id)} aria-label="Dismiss">
            ✕
          </button>
        </div>
      ))}
    </div>
  )
}

export function ConnectionBanner() {
  const conn = useSim((s) => s.conn)
  const error = useSim((s) => s.connError)
  if (conn === 'open' && !error) return null
  if (conn === 'idle') return null
  return (
    <div className="flex items-center gap-3 border-b border-warn/50 bg-warn/10 px-3 py-1.5 text-[12px]" role="alert" data-testid="connection-banner">
      <span className="text-warn">!</span>
      <span>
        {conn === 'open'
          ? error
          : conn === 'connecting'
            ? 'Connecting to the asmgraph server…'
            : `Not connected to the asmgraph server${error ? ` (${error})` : ''}. Your edits are kept locally.`}
      </span>
      {conn !== 'open' && conn !== 'connecting' && (
        <Button className="ml-auto" onClick={() => controller.reconnect()}>
          Reconnect
        </Button>
      )}
    </div>
  )
}
