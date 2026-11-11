// Execution state mirrored from the backend: connection status, the
// current machine view (snapshot + applied events), transfer activity for
// edge highlighting, the bounded event log, and test results.
import { create } from 'zustand'
import type { ConnectionState } from '../api/rpc'
import type { PausedData, TestResult } from '../api/types'
import type { LogEntry } from '../lib/eventLog'
import { emptyView, type SimView } from '../lib/simReducer'

export interface EdgeActivity {
  value: number
  tick: number
  /** monotonically increasing so repeated transfers re-trigger animation */
  serial: number
  /** writer endpoint key "cell:port" for the direction of travel */
  from: string
}

export interface Notice {
  id: number
  level: 'info' | 'warn' | 'error' | 'success'
  text: string
}

export interface SimState {
  conn: ConnectionState
  connError: string | null
  serverVersion: string | null
  /** solution version currently loaded in the backend session (-1: none) */
  loadedVersion: number
  view: SimView
  /** a machine exists in the backend session */
  started: boolean
  /** a background run is active */
  running: boolean
  pause: PausedData | null
  /** index of the test case used for interactive debugging */
  testIndex: number
  edges: Record<string, EdgeActivity>
  log: LogEntry[]
  results: Record<string, TestResult>
  /** names of tests currently running via runTest/runTests */
  testsRunning: string[]
  busy: string | null
  notices: Notice[]
}

export const useSim = create<SimState>()(() => ({
  conn: 'idle',
  connError: null,
  serverVersion: null,
  loadedVersion: -1,
  view: emptyView,
  started: false,
  running: false,
  pause: null,
  testIndex: 0,
  edges: {},
  log: [],
  results: {},
  testsRunning: [],
  busy: null,
  notices: [],
}))

let noticeId = 0
export function notify(level: Notice['level'], text: string, ttlMs = level === 'error' ? 9000 : 4000) {
  const id = ++noticeId
  useSim.setState((s) => ({ notices: [...s.notices.slice(-4), { id, level, text }] }))
  if (ttlMs > 0) setTimeout(() => dismissNotice(id), ttlMs)
}
export function dismissNotice(id: number) {
  useSim.setState((s) => ({ notices: s.notices.filter((n) => n.id !== id) }))
}
