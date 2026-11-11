// User interface preferences persisted in localStorage. Losing them is
// harmless; nothing important is stored only here.
import { create } from 'zustand'
import { createJSONStorage, persist } from 'zustand/middleware'

export type Speed = 'step' | '1' | '5' | '20' | 'max'

export type RunSpeed = Exclude<Speed, 'step'>
export const SPEEDS: { id: RunSpeed; label: string; delayMs: number }[] = [
  { id: '1', label: '1 tick/s', delayMs: 1000 },
  { id: '5', label: '5 ticks/s', delayMs: 200 },
  { id: '20', label: '20 ticks/s', delayMs: 50 },
  { id: 'max', label: 'Maximum', delayMs: 0 },
]

export interface PrefsState {
  theme: 'dark' | 'light'
  speed: Exclude<Speed, 'step'>
  snapToGrid: boolean
  animateTransfers: boolean
  showDisassembly: boolean
  cycleDetection: boolean
  logFilterCell: string
  logFilterCategory: string
  lastPuzzle: string | null
  layouts: Record<string, Record<string, number>>
  set(p: Partial<Omit<PrefsState, 'set' | 'saveLayout'>>): void
  saveLayout(id: string, layout: Record<string, number>): void
}

function safeStorage(): Storage {
  try {
    const k = '__asmgraph_probe__'
    window.localStorage.setItem(k, '1')
    window.localStorage.removeItem(k)
    return window.localStorage
  } catch {
    const mem = new Map<string, string>()
    return {
      getItem: (k) => mem.get(k) ?? null,
      setItem: (k, v) => void mem.set(k, v),
      removeItem: (k) => void mem.delete(k),
      clear: () => mem.clear(),
      key: () => null,
      length: 0,
    }
  }
}

export const usePrefs = create<PrefsState>()(
  persist(
    (set) => ({
      theme: 'dark',
      speed: '5',
      snapToGrid: true,
      animateTransfers: true,
      showDisassembly: false,
      cycleDetection: true,
      logFilterCell: '',
      logFilterCategory: '',
      lastPuzzle: null,
      layouts: {},
      set: (p) => set(p),
      saveLayout: (id, layout) => set((s) => ({ layouts: { ...s.layouts, [id]: layout } })),
    }),
    { name: 'asmgraph.prefs', version: 1, storage: createJSONStorage(safeStorage) },
  ),
)

// --- local drafts (crash/refresh recovery; files on the server remain the
// source of truth, and export is always available) ------------------------

const draftKey = (puzzleId: string) => `asmgraph.draft.${puzzleId}`

export function saveDraft(puzzleId: string, yaml: string, name: string) {
  try {
    safeStorage().setItem(draftKey(puzzleId), JSON.stringify({ yaml, name, at: Date.now() }))
  } catch {
    /* quota or privacy mode: drafts are best-effort */
  }
}

export function loadDraft(puzzleId: string): { yaml: string; name: string; at: number } | null {
  try {
    const raw = safeStorage().getItem(draftKey(puzzleId))
    if (!raw) return null
    const d = JSON.parse(raw)
    return typeof d?.yaml === 'string' ? d : null
  } catch {
    return null
  }
}

export function clearDraft(puzzleId: string) {
  try {
    safeStorage().removeItem(draftKey(puzzleId))
  } catch {
    /* ignore */
  }
}

const solvedKey = 'asmgraph.solved'
export function markSolved(puzzleId: string) {
  try {
    const s = new Set<string>(JSON.parse(safeStorage().getItem(solvedKey) ?? '[]'))
    s.add(puzzleId)
    safeStorage().setItem(solvedKey, JSON.stringify([...s]))
  } catch {
    /* ignore */
  }
}
export function solvedPuzzles(): Set<string> {
  try {
    return new Set<string>(JSON.parse(safeStorage().getItem(solvedKey) ?? '[]'))
  } catch {
    return new Set()
  }
}
