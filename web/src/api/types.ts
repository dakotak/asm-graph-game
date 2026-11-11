// TypeScript mirrors of the Go engine's JSON types (engine, protocol,
// puzzle, diag, transport/web). The Go code is the source of truth; keep
// field names identical to its `json` tags.

export type Word = number

// --- diagnostics (diag.Diagnostic) ---------------------------------------
export interface Diagnostic {
  severity: 'error' | 'warning'
  code?: string
  message: string
  file?: string
  line?: number
  column?: number
  path?: string
  cell?: string
  sourceLine?: number
  sourceColumn?: number
}

// --- puzzle (puzzle.Puzzle) ----------------------------------------------
export interface CellDef {
  id: string
  type: string
  program_memory?: number
  ports?: string[]
  config?: Record<string, unknown>
  metadata?: Record<string, unknown>
}

export interface TestCase {
  name: string
  inputs: Record<string, Word[]>
  outputs: Record<string, Word[]>
}

export interface Puzzle {
  schema_version: number
  id: string
  name?: string
  description?: string
  cells: CellDef[]
  tests: TestCase[]
  limits?: { max_ticks?: number }
  metadata?: Record<string, unknown>
}

// --- web API (transport/web) ---------------------------------------------
export interface PuzzleSummary {
  id: string
  name: string
  description: string
  file: string
  cellTypes: string[]
  cells: number
  programmableCells: number
  tests: number
  difficulty?: unknown
  valid: boolean
  diagnostics?: Diagnostic[]
  solutions: number
}

export interface PuzzleDocument {
  summary: PuzzleSummary
  yaml: string
  puzzle: Puzzle
}

export interface SolutionInfo {
  name: string
  modified: string
  size: number
}

// --- cell types (protocol.listCellTypes) ---------------------------------
export interface InstructionInfo {
  mnemonic: string
  opcode: string
  operands: string[] // slot kinds: "source" | "destination" | "target" | plugin kinds
  summary: string
  core: boolean
}

export interface RegisterDef {
  code: number
  name: string
  summary: string
}

export interface ConfigField {
  name: string
  type: string
  required?: boolean
  default?: unknown
  min?: number
  max?: number
  description?: string
}

export interface CellTypeInfo {
  type: string
  description: string
  programmable: boolean
  instructions?: InstructionInfo[]
  registers?: RegisterDef[]
  /** Fields CellSnapshot.state reports, in display order. */
  state?: StateField[]
  config?: ConfigField[]
}

export interface StateField {
  name: string
  kind: 'word' | 'list'
  summary: string
}

export interface PortInfo {
  name: string
  physical: boolean
  description: string
}

export interface CellTypeCatalog {
  types: CellTypeInfo[]
  operandKinds: Record<string, string>
  ports: PortInfo[]
  syntax: {
    comments: string[]
    labelSuffix: string
    caseSensitive: boolean
    operandSeparator: string
    integerFormats: string[]
  }
}

// --- compilation ---------------------------------------------------------
export interface DisasmEntry {
  index: number
  address: number
  bytes: string
  text: string
  sourceLine: number
}

export interface ProgramReport {
  cell: string
  type: string
  bytes: number
  limit: number
  instructions: number
  hex: string
  disassembly: DisasmEntry[]
}

export interface CompileResult {
  programs: ProgramReport[]
  totalBytes: number
  diagnostics: Diagnostic[]
}

export interface AssembleResult {
  ok: boolean
  bytes?: number
  limit: number
  instructions?: number
  hex?: string
  disassembly?: DisasmEntry[]
  diagnostics: Diagnostic[]
}

// --- engine state (engine.Snapshot) --------------------------------------
export type Status =
  | 'ready'
  | 'running'
  | 'passed'
  | 'failed'
  | 'deadlocked'
  | 'cycle_detected'
  | 'tick_limit_exceeded'
  | 'runtime_error'

export const TERMINAL_STATUSES: readonly Status[] = [
  'passed',
  'failed',
  'deadlocked',
  'cycle_detected',
  'tick_limit_exceeded',
  'runtime_error',
]

export interface Endpoint {
  cell: string
  port: string
}

export interface Connection {
  from: Endpoint
  to: Endpoint
}

export interface PendingSnapshot {
  received?: { operand: number; value: Word }[]
  output?: Word
  destination?: string
}

export interface PortSnapshot {
  port: string
  enabled: boolean
  peer?: Endpoint
}

export interface CellSnapshot {
  id: string
  type: string
  programmable: boolean
  programBytes?: number
  programMemory?: number
  pc: number
  address: number
  instruction?: string
  sourceLine?: number
  stage: 'idle' | 'ready' | 'write' | 'halted'
  pending?: PendingSnapshot
  request?: string
  blocked: boolean
  blockReason?: string
  halted?: boolean
  last?: string
  ports: PortSnapshot[]
  state: Record<string, unknown>
  instructionsCompleted: number
  error?: string
}

export interface InputSnapshot {
  cell: string
  stream: string
  position: number
  length: number
}

export interface Mismatch {
  index: number
  expected?: Word
  got: Word
}

export interface OutputSnapshot {
  cell: string
  stream: string
  state: 'pending' | 'passed' | 'failed'
  expected: Word[]
  received: Word[]
  mismatch?: Mismatch
}

export interface Metrics {
  ticks: number
  instructions: number
  transfers: number
  programBytes: Record<string, number>
  totalProgramBytes: number
  cellsSupplied: number
  cellsUsed: number
  connections: number
}

export interface Snapshot {
  puzzle: string
  test: string
  testIndex: number
  tick: number
  status: Status
  message?: string
  cells: CellSnapshot[]
  connections: Connection[]
  inputs: InputSnapshot[]
  outputs: OutputSnapshot[]
  metrics: Metrics
}

export interface BlockedCell {
  cell: string
  instruction?: string
  sourceLine?: number
  reason: string
}

export interface TestResult {
  test: string
  index: number
  status: Status
  passed: boolean
  message?: string
  metrics: Metrics
  outputs: OutputSnapshot[]
  blocked?: BlockedCell[]
  cycle?: { firstTick: number; repeatTick: number; period: number }
  error?: { cell: string; pc: number; instruction: string; sourceLine?: number; message: string }
  notes?: string[]
}

export interface SuiteResult {
  puzzle: string
  passed: boolean
  tests: TestResult[]
  summary: {
    passedTests: number
    totalTests: number
    totalTicks: number
    maxTicks: number
    totalInstructions: number
    totalTransfers: number
    programBytes: Record<string, number>
    totalProgramBytes: number
    cellsSupplied: number
    cellsUsed: number
    connections: number
  }
}

// --- events (engine.Event) -----------------------------------------------
export type EventType =
  | 'simulation.started'
  | 'simulation.paused'
  | 'simulation.reset'
  | 'tick.completed'
  | 'instruction.completed'
  | 'cell.blocked'
  | 'cell.unblocked'
  | 'port.transfer'
  | 'cell.state'
  | 'output.validation'
  | 'test.completed'
  | 'deadlock.detected'
  | 'cycle.detected'
  | 'simulation.completed'
  | 'runtime.error'

export interface SimEvent {
  seq: number
  tick: number
  type: EventType
  test?: string
  cell?: string
  data?: unknown
}

export interface TransferData {
  from: Endpoint
  to: Endpoint
  value: Word
}

export interface InstructionData {
  pc: number
  address: number
  instruction: string
  sourceLine?: number
  nextPc: number
  halted?: boolean
}

export interface BlockData {
  reason: string
  instruction?: string
  sourceLine?: number
}

export interface TickData {
  status: Status
  instructions: number
  transfers: number
  progress: boolean
}

export interface PausedData {
  status: Status
  reason: string
  cell?: string
  line?: number
}

export type EventLevel = 'none' | 'lifecycle' | 'ticks' | 'all'

export const PROTOCOL_VERSION = '1'
