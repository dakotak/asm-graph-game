import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import type { TestResult } from '../api/types'
import { emptySolution } from '../lib/solution'
import { viewFromSnapshot } from '../lib/simReducer'
import { useSim } from '../state/sim'
import { useUi } from '../state/ui'
import { useWorkspace } from '../state/workspace'
import { catalog, puzzle, snapshot } from '../test/fakeBackend'
import { DiagnosticsPanel, EventsPanel, TestsPanel } from './BottomPanel'
import { CommandPalette } from './dialogs'
import { Toolbar, uiStatus } from './Toolbar'
import { cellActivity, portsOf, waitingPorts } from './graph/portState'

const initialWs = useWorkspace.getState()
const initialSim = useSim.getState()

function loadPuzzle() {
  useWorkspace.setState({
    catalog,
    puzzleDoc: { summary: { id: puzzle.id, name: 'Double the Input' } as never, yaml: '', puzzle },
    solution: emptySolution(puzzle, (t) => t === 'standard'),
    selectedCell: 'processor',
  })
}

beforeEach(() => {
  useWorkspace.setState(initialWs, true)
  useSim.setState(initialSim, true)
  loadPuzzle()
})
afterEach(cleanup)

const failed: TestResult = {
  test: 'negative',
  index: 1,
  status: 'failed',
  passed: false,
  message: 'output "result" expected -2 at position 0 but received 7',
  metrics: { ...snapshot().metrics, ticks: 5, instructions: 4, transfers: 2 },
  outputs: [{ cell: 'output', stream: 'result', state: 'failed', expected: [-2], received: [7], mismatch: { index: 0, expected: -2, got: 7 } }],
  blocked: [{ cell: 'processor', instruction: 'MOV ACC, P2', sourceLine: 3, reason: 'cannot write to P2: port is not connected' }],
}

describe('TestsPanel', () => {
  it('lists every test case from the puzzle file and renders backend results', () => {
    useSim.setState({
      conn: 'open',
      results: {
        positive: { ...failed, test: 'positive', index: 0, status: 'passed', passed: true, message: 'all output streams match', outputs: [], blocked: [] },
        negative: failed,
      },
    })
    render(<TestsPanel />)
    expect(screen.getByTestId('tests-summary')).toHaveTextContent('1/2 passed')
    const row = screen.getByTestId('test-row-negative')
    expect(within(row).getByText('Failed')).toBeInTheDocument()
    expect(within(row).getByText('5')).toBeInTheDocument()
    fireEvent.click(within(row).getByRole('button', { name: 'Expand' }))
    expect(screen.getByText(/expected -2 at position 0/)).toBeInTheDocument()
    const table = screen.getByTestId('compare-result')
    expect(table).toHaveTextContent('-2')
    expect(table).toHaveTextContent('7')
    expect(screen.getByText(/port is not connected/)).toBeInTheDocument()
  })

  it('shows expected outputs for tests that have not run', () => {
    render(<TestsPanel />)
    fireEvent.click(within(screen.getByTestId('test-row-positive')).getByRole('button', { name: 'Expand' }))
    expect(screen.getByText(/result = \[2, 4\]/)).toBeInTheDocument()
  })
})

describe('DiagnosticsPanel', () => {
  it('merges compile and live assembler diagnostics and jumps to source', () => {
    useWorkspace.setState({
      diagnostics: [{ severity: 'error', message: 'port P2 is disabled', path: 'connections[0].from.port' }],
      assemble: { processor: { ok: false, limit: 32, source: 'MUL 2', diagnostics: [{ severity: 'error', message: 'unknown instruction MUL', sourceLine: 1, sourceColumn: 1 }] } },
    })
    render(<DiagnosticsPanel />)
    expect(screen.getByText('port P2 is disabled')).toBeInTheDocument()
    fireEvent.click(screen.getByText('unknown instruction MUL'))
    expect(useWorkspace.getState().selectedCell).toBe('processor')
  })
})

describe('Toolbar execution controls', () => {
  it('disables execution while disconnected', () => {
    useSim.setState({ conn: 'reconnecting' })
    render(<Toolbar />)
    expect(screen.getByTestId('run')).toBeDisabled()
    expect(screen.getByTestId('step')).toBeDisabled()
    expect(screen.getByTestId('compile')).toBeDisabled()
    expect(screen.getByTestId('connection')).toHaveAttribute('data-state', 'reconnecting')
  })

  it('reflects running, paused and terminal states', () => {
    useSim.setState({ conn: 'open' })
    render(<Toolbar />)
    expect(screen.getByTestId('run')).toBeEnabled()
    expect(screen.getByTestId('reset')).toBeDisabled() // nothing started yet
    expect(screen.getByTestId('sim-status')).toHaveTextContent('Idle')
    act(() => useSim.setState({ started: true, running: true, view: viewFromSnapshot(snapshot(12, 'running')) }))
    expect(screen.getByTestId('pause')).toBeEnabled()
    expect(screen.getByTestId('step')).toBeDisabled()
    expect(screen.getByTestId('tick')).toHaveTextContent('00012')
    expect(screen.getByTestId('sim-status')).toHaveTextContent('Running')
    act(() => useSim.setState({ running: false }))
    expect(screen.getByTestId('run')).toHaveTextContent('Continue')
    expect(screen.getByTestId('sim-status')).toHaveTextContent('Paused')
    act(() => useSim.setState({ view: viewFromSnapshot(snapshot(20, 'deadlocked')) }))
    expect(screen.getByTestId('sim-status')).toHaveTextContent('Deadlocked')
    expect(screen.getByTestId('run')).toHaveTextContent('Run')
  })

  it('marks unsaved changes', () => {
    useWorkspace.setState({ dirty: true })
    render(<Toolbar />)
    expect(screen.getByTestId('dirty-indicator')).toBeInTheDocument()
  })

  it('derives the displayed status', () => {
    expect(uiStatus({ started: false, running: false, status: null, pause: null, hasEdits: false })).toBe('idle')
    expect(uiStatus({ started: true, running: false, status: 'ready', pause: null, hasEdits: false })).toBe('ready')
    expect(uiStatus({ started: true, running: false, status: 'cycle_detected', pause: null, hasEdits: false })).toBe('cycle_detected')
  })
})

describe('CommandPalette', () => {
  it('filters commands, cells and puzzles', () => {
    useWorkspace.setState({ puzzles: [{ id: 'double-input', name: 'Double the Input', valid: true } as never] })
    render(<CommandPalette />)
    act(() => useUi.setState({ palette: true }))
    const input = screen.getByRole('combobox')
    fireEvent.change(input, { target: { value: 'go proc' } })
    expect(screen.getByRole('option', { name: /Go to cell: processor/ })).toBeInTheDocument()
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(useWorkspace.getState().selectedCell).toBe('processor')
    expect(useUi.getState().palette).toBe(false)
  })
})

describe('EventsPanel', () => {
  it('filters the bounded log by cell', () => {
    useSim.setState({
      log: [
        { seq: 1, tick: 1, type: 'port.transfer', cells: ['input', 'processor'], text: 'input.P0 → processor.P0  1', level: 'info' },
        { seq: 2, tick: 2, type: 'instruction.completed', cells: ['other'], text: 'other executed NOP', level: 'info' },
      ],
    })
    render(<EventsPanel />)
    expect(screen.getByText('other executed NOP')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Filter by cell'), { target: { value: 'processor' } })
    expect(screen.queryByText('other executed NOP')).not.toBeInTheDocument()
    expect(screen.getByText(/input.P0 → processor.P0/)).toBeInTheDocument()
  })
})

describe('port and cell presentation', () => {
  const snap = {
    ...snapshot().cells[0],
    blocked: true,
    request: 'read ANY',
    ports: [
      { port: 'P0', enabled: true, peer: { cell: 'input', port: 'P0' } },
      { port: 'P1', enabled: true },
    ],
  }
  it('derives waiting ports from the backend request', () => {
    expect([...waitingPorts(snap)]).toEqual(['P0'])
    expect([...waitingPorts({ ...snap, request: 'write LAST', last: 'P1' })]).toEqual(['P1'])
    expect(waitingPorts({ ...snap, blocked: false }).size).toBe(0)
  })
  it('classifies ports', () => {
    const conns = [{ from: { cell: 'input', port: 'P0' }, to: { cell: 'processor', port: 'P0' } }]
    const ports = portsOf('processor', new Set(['P0', 'P1', 'P2']), conns, snap, new Set())
    expect(ports.map((p) => p.visual)).toEqual(['waiting', 'free', 'free', 'disabled'])
    const active = portsOf('processor', new Set(['P0']), conns, undefined, new Set(['input:P0--processor:P0']))
    expect(active[0].visual).toBe('active')
  })
  it('labels blocked processors and waiting stream cells', () => {
    expect(cellActivity(snap, true)).toBe('blocked')
    expect(cellActivity({ ...snap, programmable: false }, true)).toBe('waiting')
    expect(cellActivity({ ...snap, blocked: false, halted: true }, true)).toBe('halted')
    expect(cellActivity(snap, false)).toBe('idle')
  })
})
