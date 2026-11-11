// Transient UI state: open dialogs, the active bottom tab, and promise-based
// confirm/prompt dialogs (so services can ask the user without window.confirm).
import { create } from 'zustand'

export type BottomTab = 'tests' | 'diagnostics' | 'events' | 'program'

export interface ModalRequest {
  kind: 'confirm' | 'prompt'
  title: string
  message?: string
  confirmLabel?: string
  danger?: boolean
  defaultValue?: string
  validate?: (v: string) => string | null
  resolve: (v: string | boolean | null) => void
}

export interface UiState {
  palette: boolean
  help: boolean
  browser: boolean
  solutions: boolean
  bottomTab: BottomTab
  modal: ModalRequest | null
  /** incremented to ask the editor / graph to take focus */
  focusEditor: number
  focusGraph: number
  fitView: number
  /** ask the editor to reveal a source line in a cell */
  reveal: { cell: string; line: number; n: number } | null
  set(p: Partial<Omit<UiState, 'set'>>): void
}

export const useUi = create<UiState>()((set) => ({
  palette: false,
  help: false,
  browser: false,
  solutions: false,
  bottomTab: 'tests',
  modal: null,
  focusEditor: 0,
  focusGraph: 0,
  fitView: 0,
  reveal: null,
  set: (p) => set(p),
}))

export function confirmDialog(opts: { title: string; message?: string; confirmLabel?: string; danger?: boolean }): Promise<boolean> {
  return new Promise((resolve) => {
    useUi.setState({ modal: { kind: 'confirm', ...opts, resolve: (v) => resolve(v === true) } })
  })
}

export function promptDialog(opts: {
  title: string
  message?: string
  defaultValue?: string
  confirmLabel?: string
  validate?: (v: string) => string | null
}): Promise<string | null> {
  return new Promise((resolve) => {
    useUi.setState({ modal: { kind: 'prompt', ...opts, resolve: (v) => resolve(typeof v === 'string' ? v : null) } })
  })
}

export function revealLine(cell: string, line: number) {
  const n = (useUi.getState().reveal?.n ?? 0) + 1
  useUi.setState({ reveal: { cell, line, n } })
}
