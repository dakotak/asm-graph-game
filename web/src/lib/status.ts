// Presentation of execution statuses: every status has a text label and a
// glyph so color is never the only signal.
import type { Status } from '../api/types'

export type UiStatus = Status | 'idle' | 'paused' | 'stale'

export interface StatusStyle {
  label: string
  glyph: string
  tone: 'neutral' | 'run' | 'warn' | 'ok' | 'bad'
}

export const STATUS_STYLES: Record<UiStatus, StatusStyle> = {
  idle: { label: 'Idle', glyph: '○', tone: 'neutral' },
  stale: { label: 'Edited', glyph: '◌', tone: 'neutral' },
  ready: { label: 'Ready', glyph: '◇', tone: 'neutral' },
  running: { label: 'Running', glyph: '▶', tone: 'run' },
  paused: { label: 'Paused', glyph: '❚❚', tone: 'warn' },
  passed: { label: 'Passed', glyph: '✓', tone: 'ok' },
  failed: { label: 'Failed', glyph: '✗', tone: 'bad' },
  deadlocked: { label: 'Deadlocked', glyph: '⊘', tone: 'bad' },
  cycle_detected: { label: 'Cycle detected', glyph: '↻', tone: 'bad' },
  tick_limit_exceeded: { label: 'Tick limit', glyph: '⧗', tone: 'bad' },
  runtime_error: { label: 'Runtime error', glyph: '!', tone: 'bad' },
}

export const TONE_CLASSES: Record<StatusStyle['tone'], string> = {
  neutral: 'text-fg-muted border-line',
  run: 'text-run border-run/50',
  warn: 'text-warn border-warn/50',
  ok: 'text-ok border-ok/50',
  bad: 'text-bad border-bad/50',
}

export function isTerminal(s: Status | null | undefined): boolean {
  return s != null && s !== 'ready' && s !== 'running'
}
