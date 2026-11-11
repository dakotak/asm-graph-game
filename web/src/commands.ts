// Every user action lives here once: the toolbar, the command palette, the
// keyboard shortcuts and the help panel all read this registry.
//
// Shortcuts avoid keys Monaco uses for editing: debugger keys (F5, F6, F9,
// F10), Mod+Shift chords, and Alt+digit/bracket for focus and cell switching.
import { controller } from './services/controller'
import { isTerminal } from './lib/status'
import { usePrefs } from './state/prefs'
import { notify, useSim } from './state/sim'
import { confirmDialog, promptDialog, useUi } from './state/ui'
import { useWorkspace } from './state/workspace'
import { isValidSolutionName } from './api/library'

export interface Command {
  id: string
  title: string
  group: 'Run' | 'Solution' | 'Navigate' | 'View' | 'Help'
  /** display form, e.g. "Mod+Shift+B" */
  keys?: string
  enabled?: () => boolean
  run: () => void | Promise<unknown>
}

export const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)

export function displayKeys(keys: string): string {
  return keys
    .replace(/Mod/g, isMac ? '⌘' : 'Ctrl')
    .replace(/Alt/g, isMac ? '⌥' : 'Alt')
    .replace(/Shift/g, isMac ? '⇧' : 'Shift')
    .replace(/\+/g, isMac ? '' : '+')
}

const hasPuzzle = () => !!useWorkspace.getState().solution
const connected = () => useSim.getState().conn === 'open'
const canExecute = () => hasPuzzle() && connected()

/** Asks before discarding unsaved changes. */
export async function confirmDiscard(action: string): Promise<boolean> {
  if (!useWorkspace.getState().dirty) return true
  return confirmDialog({
    title: 'Discard unsaved changes?',
    message: `Your solution has unsaved changes. ${action} will discard them. Save first (Mod+S) to keep them.`.replace('Mod', isMac ? '⌘' : 'Ctrl'),
    confirmLabel: 'Discard changes',
    danger: true,
  })
}

export async function saveSolution(asCopy = false): Promise<boolean> {
  const ws = useWorkspace.getState()
  if (!ws.solution) return false
  let name = asCopy ? '' : ws.solutionName
  if (!name) {
    const picked = await promptDialog({
      title: asCopy ? 'Save a copy as' : 'Save solution as',
      message: 'Solutions are stored in this browser. Export them to keep a file.',
      defaultValue: asCopy && ws.solutionName ? `${ws.solutionName}-copy` : ws.solutionName || 'main',
      confirmLabel: 'Save',
      validate: (v) => (isValidSolutionName(v) ? null : 'Use letters, digits, ".", "_" or "-" (max 64).'),
    })
    if (!picked) return false
    name = picked
  }
  return controller.save(name)
}

export function exportSolution() {
  const out = controller.exportYaml()
  if (!out) return
  const url = URL.createObjectURL(new Blob([out.yaml], { type: 'application/yaml' }))
  const a = document.createElement('a')
  a.href = url
  a.download = out.filename
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

export function importSolution() {
  const input = document.createElement('input')
  input.type = 'file'
  input.accept = '.yaml,.yml,text/yaml'
  input.onchange = async () => {
    const f = input.files?.[0]
    if (!f) return
    if (f.size > 1 << 20) {
      notify('error', 'That file is larger than 1 MiB; solutions are much smaller.')
      return
    }
    if (!(await confirmDiscard('Importing'))) return
    try {
      controller.importYaml(await f.text())
    } catch (e) {
      notify('error', `Import failed: ${(e as Error).message}`)
    }
  }
  input.click()
}

/** Imports a puzzle file into the browser library and opens it. */
export function importPuzzle() {
  const input = document.createElement('input')
  input.type = 'file'
  input.accept = '.yaml,.yml,text/yaml'
  input.onchange = async () => {
    const f = input.files?.[0]
    if (!f) return
    if (f.size > 4 << 20) {
      notify('error', 'That file is larger than 4 MiB.')
      return
    }
    await controller.importPuzzle(f.name, await f.text())
  }
  input.click()
}

function cycleCell(dir: 1 | -1) {
  const ws = useWorkspace.getState()
  const cells = ws.cells.map((c) => c.id)
  if (cells.length === 0) return
  const i = ws.selectedCell ? cells.indexOf(ws.selectedCell) : -1
  ws.select(cells[(i + dir + cells.length) % cells.length])
}

export const commands: Command[] = [
  { id: 'compile', title: 'Compile solution', group: 'Run', keys: 'Mod+Shift+B', enabled: canExecute, run: () => controller.compile() },
  {
    id: 'run',
    title: 'Run / continue',
    group: 'Run',
    keys: 'F5',
    enabled: () => canExecute() && !useSim.getState().running,
    run: () => controller.run(),
  },
  { id: 'pause', title: 'Pause', group: 'Run', keys: 'F6', enabled: () => useSim.getState().running, run: () => controller.pause() },
  {
    id: 'step',
    title: 'Step one tick',
    group: 'Run',
    keys: 'F10',
    enabled: () => canExecute() && !useSim.getState().running,
    run: () => controller.step(),
  },
  {
    id: 'reset',
    title: 'Reset simulation',
    group: 'Run',
    keys: 'Shift+F5',
    enabled: () => connected() && useSim.getState().started,
    run: () => controller.reset(),
  },
  {
    id: 'tests',
    title: 'Run all tests',
    group: 'Run',
    keys: 'Mod+Shift+Enter',
    enabled: () => canExecute() && useSim.getState().testsRunning.length === 0,
    run: () => controller.runTests(),
  },
  {
    id: 'toggle-cycles',
    title: 'Toggle cycle detection',
    group: 'Run',
    run: () => usePrefs.getState().set({ cycleDetection: !usePrefs.getState().cycleDetection }),
  },
  { id: 'save', title: 'Save solution', group: 'Solution', keys: 'Mod+S', enabled: hasPuzzle, run: () => saveSolution(false) },
  { id: 'save-copy', title: 'Save a copy…', group: 'Solution', keys: 'Mod+Shift+S', enabled: hasPuzzle, run: () => saveSolution(true) },
  { id: 'open-solution', title: 'Open saved solution…', group: 'Solution', enabled: hasPuzzle, run: () => useUi.setState({ solutions: true }) },
  {
    id: 'new-solution',
    title: 'Reset to an empty solution',
    group: 'Solution',
    enabled: hasPuzzle,
    run: async () => {
      if (await confirmDiscard('Starting an empty solution')) controller.newSolution()
    },
  },
  { id: 'export', title: 'Export solution YAML', group: 'Solution', enabled: hasPuzzle, run: exportSolution },
  { id: 'import', title: 'Import solution YAML…', group: 'Solution', enabled: hasPuzzle, run: importSolution },
  { id: 'open-puzzle', title: 'Browse puzzles…', group: 'Navigate', keys: 'Mod+O', run: () => useUi.setState({ browser: true }) },
  { id: 'import-puzzle', title: 'Import puzzle file…', group: 'Navigate', run: importPuzzle },
  { id: 'focus-graph', title: 'Focus graph', group: 'Navigate', keys: 'Alt+1', run: () => useUi.setState({ focusGraph: useUi.getState().focusGraph + 1 }) },
  { id: 'focus-editor', title: 'Focus assembly editor', group: 'Navigate', keys: 'Alt+2', run: () => useUi.setState({ focusEditor: useUi.getState().focusEditor + 1 }) },
  { id: 'next-cell', title: 'Next cell', group: 'Navigate', keys: 'Alt+]', enabled: hasPuzzle, run: () => cycleCell(1) },
  { id: 'prev-cell', title: 'Previous cell', group: 'Navigate', keys: 'Alt+[', enabled: hasPuzzle, run: () => cycleCell(-1) },
  { id: 'palette', title: 'Command palette', group: 'Navigate', keys: 'Mod+Shift+P', run: () => useUi.setState({ palette: true }) },
  { id: 'auto-layout', title: 'Auto-arrange graph', group: 'View', enabled: hasPuzzle, run: () => controller.autoLayout() },
  { id: 'fit-view', title: 'Fit graph to view', group: 'View', run: () => useUi.setState({ fitView: useUi.getState().fitView + 1 }) },
  { id: 'toggle-snap', title: 'Toggle snap to grid', group: 'View', run: () => usePrefs.getState().set({ snapToGrid: !usePrefs.getState().snapToGrid }) },
  {
    id: 'toggle-anim',
    title: 'Toggle transfer animation',
    group: 'View',
    run: () => usePrefs.getState().set({ animateTransfers: !usePrefs.getState().animateTransfers }),
  },
  {
    id: 'toggle-theme',
    title: 'Toggle light/dark theme',
    group: 'View',
    run: () => usePrefs.getState().set({ theme: usePrefs.getState().theme === 'dark' ? 'light' : 'dark' }),
  },
  { id: 'reconnect', title: 'Restart the engine', group: 'Help', enabled: () => !connected(), run: () => controller.reconnect() },
  { id: 'help', title: 'Keyboard shortcuts & help', group: 'Help', keys: '?', run: () => useUi.setState({ help: true }) },
]

export function runCommand(id: string) {
  const c = commands.find((x) => x.id === id)
  if (c && (c.enabled?.() ?? true)) return c.run()
}

// --- shortcut matching ---------------------------------------------------

function matches(keys: string, e: KeyboardEvent): boolean {
  const parts = keys.split('+')
  const key = parts.pop()!
  const want = { mod: parts.includes('Mod'), shift: parts.includes('Shift'), alt: parts.includes('Alt') }
  const mod = isMac ? e.metaKey : e.ctrlKey
  if (want.mod !== mod || want.alt !== e.altKey) return false
  if (key === '?') return e.key === '?'
  if (want.shift !== e.shiftKey) return false
  if (/^F\d+$/.test(key) || key === 'Enter') return e.key === key
  if (key === '[') return e.code === 'BracketLeft'
  if (key === ']') return e.code === 'BracketRight'
  if (/^\d$/.test(key)) return e.code === `Digit${key}`
  return e.code === `Key${key.toUpperCase()}`
}

function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  if (!el) return false
  return el.isContentEditable || el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT' || !!el.closest?.('.monaco-editor')
}

/** Installs global shortcuts (capture phase, so they work inside Monaco). */
export function installShortcuts(): () => void {
  const handler = (e: KeyboardEvent) => {
    const ui = useUi.getState()
    if (ui.modal || ui.palette) return // dialogs own the keyboard
    for (const c of commands) {
      if (!c.keys) continue
      if (c.keys === '?' && isTyping(e.target)) continue
      if (matches(c.keys, e)) {
        e.preventDefault()
        e.stopPropagation()
        if (c.enabled?.() ?? true) void c.run()
        return
      }
    }
  }
  window.addEventListener('keydown', handler, true)
  return () => window.removeEventListener('keydown', handler, true)
}

export { isTerminal }
