import clsx from 'clsx'
import { commands, displayKeys, saveSolution } from '../commands'
import { controller } from '../services/controller'
import { isTerminal, type UiStatus } from '../lib/status'
import { SPEEDS, usePrefs } from '../state/prefs'
import { useSim } from '../state/sim'
import { useUi } from '../state/ui'
import { useWorkspace } from '../state/workspace'
import { Button, StatusBadge } from './ui'

function useCmd(id: string) {
  const c = commands.find((x) => x.id === id)!
  // Re-render on any state that affects enablement.
  useSim((s) => `${s.conn}|${s.running}|${s.started}|${s.testsRunning.length}`)
  useWorkspace((s) => !!s.solution)
  return { enabled: c.enabled?.() ?? true, title: `${c.title}${c.keys ? ` (${displayKeys(c.keys)})` : ''}`, run: () => void c.run() }
}

export function uiStatus(s: { started: boolean; running: boolean; status: string | null; pause: unknown; hasEdits: boolean }): UiStatus {
  if (!s.started) return 'idle'
  if (s.running) return 'running'
  if (s.status && isTerminal(s.status as never)) return s.status as UiStatus
  if (s.pause) return 'paused'
  if (s.status === 'running') return 'paused'
  return (s.status as UiStatus) ?? 'ready'
}

export function Toolbar() {
  const puzzle = useWorkspace((s) => s.puzzleDoc?.summary)
  const solutionName = useWorkspace((s) => s.solutionName)
  const dirty = useWorkspace((s) => s.dirty)
  const tests = useWorkspace((s) => s.puzzleDoc?.puzzle.tests)
  const compileState = useWorkspace((s) => s.compile.state)
  const compiledUpToDate = useWorkspace((s) => s.compile.state === 'ok' && s.compile.version === s.version)
  const started = useSim((s) => s.started)
  const running = useSim((s) => s.running)
  const status = useSim((s) => s.view.status)
  const tick = useSim((s) => s.view.tick)
  const pause = useSim((s) => s.pause)
  const testIndex = useSim((s) => s.testIndex)
  const speed = usePrefs((s) => s.speed)
  const theme = usePrefs((s) => s.theme)
  const compile = useCmd('compile')
  const run = useCmd('run')
  const pauseCmd = useCmd('pause')
  const step = useCmd('step')
  const reset = useCmd('reset')
  const runTests = useCmd('tests')
  const st = uiStatus({ started, running, status, pause, hasEdits: false })
  const resumable = started && !running && !isTerminal(status)

  return (
    <header className="flex h-11 shrink-0 items-center gap-2 overflow-x-auto overflow-y-hidden border-b border-line bg-panel px-2 [scrollbar-width:none]" role="toolbar" aria-label="Main toolbar">
      <span className="shrink-0 font-mono text-[13px] font-semibold tracking-tight text-fg">
        asm<span className="text-accent">graph</span>
      </span>
      <div className="mx-1 h-5 w-px bg-line" />
      <Button variant="ghost" onClick={() => useUi.setState({ browser: true })} title="Browse puzzles (Mod+O)" data-testid="puzzle-button">
        <span className="max-w-48 truncate text-fg">{puzzle?.name ?? 'Open puzzle…'}</span>
        <span className="text-fg-muted">▾</span>
      </Button>
      {puzzle && (
        <Button variant="ghost" onClick={() => useUi.setState({ solutions: true })} title="Saved solutions">
          <span className="font-mono text-[11px] text-fg-muted">{solutionName || 'unsaved'}</span>
          {dirty && (
            <span className="text-warn" aria-label="unsaved changes" title="Unsaved changes" data-testid="dirty-indicator">
              ●
            </span>
          )}
        </Button>
      )}
      {puzzle && (
        <Button variant="ghost" onClick={() => void saveSolution(false)} title="Save (Mod+S)" disabled={!dirty && !!solutionName}>
          Save
        </Button>
      )}
      <div className="mx-1 h-5 w-px bg-line" />

      <Button onClick={compile.run} disabled={!compile.enabled || compileState === 'running'} title={compile.title} data-testid="compile">
        <span aria-hidden className={clsx(compiledUpToDate ? 'text-ok' : compileState === 'error' ? 'text-bad' : 'text-fg-muted')}>
          {compileState === 'running' ? '…' : compiledUpToDate ? '✓' : compileState === 'error' ? '✗' : '⚙'}
        </span>
        Compile
      </Button>
      {running ? (
        <Button variant="primary" onClick={pauseCmd.run} disabled={!pauseCmd.enabled} title={pauseCmd.title} data-testid="pause">
          ❚❚ Pause
        </Button>
      ) : (
        <Button variant="primary" onClick={run.run} disabled={!run.enabled} title={run.title} data-testid="run">
          ▶ {resumable ? 'Continue' : 'Run'}
        </Button>
      )}
      <Button onClick={step.run} disabled={!step.enabled} title={step.title} data-testid="step">
        ⤼ Step
      </Button>
      <Button onClick={reset.run} disabled={!reset.enabled} title={reset.title} data-testid="reset">
        ⟲ Reset
      </Button>
      <select
        className="h-7 rounded-sm border border-line bg-panel-2 px-1 font-mono text-[11px] text-fg"
        value={speed}
        onChange={(e) => void controller.setSpeed(e.target.value as typeof speed)}
        aria-label="Execution speed"
        title="Execution speed (pacing only; results are identical at every speed)"
      >
        {SPEEDS.map((s) => (
          <option key={s.id} value={s.id}>
            {s.label}
          </option>
        ))}
      </select>
      <select
        className="h-7 max-w-36 rounded-sm border border-line bg-panel-2 px-1 font-mono text-[11px] text-fg"
        value={testIndex}
        onChange={(e) => void controller.selectTest(Number(e.target.value))}
        aria-label="Test case to debug"
        title="Test case used by Run/Step"
        disabled={!tests?.length}
      >
        {(tests ?? []).map((t, i) => (
          <option key={t.name} value={i}>
            test: {t.name}
          </option>
        ))}
      </select>
      <div className="flex items-center gap-2 pl-1">
        <span className="font-mono text-[11px] text-fg-muted">
          TICK <span className="text-fg" data-testid="tick">{String(started ? tick : 0).padStart(5, '0')}</span>
        </span>
        <span data-testid="sim-status">
          <StatusBadge status={st} />
        </span>
        {pause?.reason === 'breakpoint' && !running && (
          <span className="font-mono text-[11px] text-warn">
            ● {pause.cell}:{pause.line}
          </span>
        )}
      </div>
      <div className="mx-1 h-5 w-px bg-line" />
      <Button onClick={runTests.run} disabled={!runTests.enabled} title={runTests.title} data-testid="run-tests">
        ☰ Run tests
      </Button>

      <div className="ml-auto flex items-center gap-1">
        <ConnectionDot />
        <Button variant="ghost" onClick={() => useUi.setState({ palette: true })} title="Command palette (Mod+Shift+P)">
          ⌘ Commands
        </Button>
        <Button variant="ghost" onClick={() => useUi.setState({ help: true })} title="Shortcuts and help (?)" aria-label="Help">
          ?
        </Button>
        <Button
          variant="ghost"
          onClick={() => usePrefs.getState().set({ theme: theme === 'dark' ? 'light' : 'dark' })}
          title="Toggle theme"
          aria-label="Toggle theme"
        >
          {theme === 'dark' ? '☾' : '☀'}
        </Button>
      </div>
    </header>
  )
}

function ConnectionDot() {
  const conn = useSim((s) => s.conn)
  const ok = conn === 'open'
  return (
    <span
      className={clsx('flex items-center gap-1 px-1 font-mono text-[10px]', ok ? 'text-ok' : 'text-warn')}
      title={ok ? 'The engine (WebAssembly) is running' : `Engine: ${conn}`}
      data-testid="connection"
      data-state={conn}
    >
      <span aria-hidden>{ok ? '●' : '○'}</span>
      {ok ? 'online' : conn}
    </span>
  )
}
