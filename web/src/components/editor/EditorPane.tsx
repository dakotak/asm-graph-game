// The assembly editor. One Monaco instance is created once; each
// programmable cell has its own text model, so switching cells keeps undo
// history, cursor and scroll position. All language knowledge (mnemonics,
// operands, docs) comes from the backend catalog, and every size or error
// shown here comes from the Go assembler.
import clsx from 'clsx'
import { useEffect, useMemo, useRef } from 'react'
import { languageId, monaco, registerLanguages } from '../../editor/monaco'
import { controller } from '../../services/controller'
import { usePrefs } from '../../state/prefs'
import { useSim } from '../../state/sim'
import { useUi } from '../../state/ui'
import { cellType, useWorkspace } from '../../state/workspace'
import { Button } from '../ui'
import type { Diagnostic } from '../../api/types'

export default function EditorPane() {
  const host = useRef<HTMLDivElement>(null)
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null)
  const models = useRef(new Map<string, monaco.editor.ITextModel>())
  const viewStates = useRef(new Map<string, monaco.editor.ICodeEditorViewState | null>())
  const currentCell = useRef<string | null>(null)
  const applyingExternal = useRef(false)
  const decorations = useRef<monaco.editor.IEditorDecorationsCollection | null>(null)

  const catalog = useWorkspace((s) => s.catalog)
  const puzzleId = useWorkspace((s) => s.puzzleDoc?.puzzle.id ?? null)
  const cellId = useWorkspace((s) => s.selectedCell)
  const def = useWorkspace((s) => s.puzzleDoc?.puzzle.cells.find((c) => c.id === s.selectedCell))
  const typeInfo = useMemo(() => (def ? cellType(catalog, def.type) : undefined), [catalog, def])
  const programmable = !!typeInfo?.programmable
  const theme = usePrefs((s) => s.theme)

  // Create the editor once.
  useEffect(() => {
    if (!host.current) return
    const ed = monaco.editor.create(host.current, {
      model: null,
      theme: usePrefs.getState().theme === 'dark' ? 'asmgraph-dark' : 'asmgraph-light',
      fontFamily: "'IBM Plex Mono', ui-monospace, Menlo, monospace",
      fontSize: 13,
      lineHeight: 20,
      glyphMargin: true,
      lineNumbersMinChars: 3,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      automaticLayout: true,
      renderLineHighlight: 'line',
      fixedOverflowWidgets: true,
      tabSize: 4,
      insertSpaces: true,
      wordBasedSuggestions: 'off',
      quickSuggestions: { other: true, comments: false, strings: false },
      suggestOnTriggerCharacters: true,
      folding: false,
      overviewRulerLanes: 2,
      scrollbar: { verticalScrollbarSize: 8, horizontalScrollbarSize: 8 },
      ariaLabel: 'Assembly program',
      // Use the classic textarea input path: it is the most robust with IMEs,
      // assistive technology and browser automation.
      editContext: false,
    })
    editorRef.current = ed
    decorations.current = ed.createDecorationsCollection()
    const sub = ed.onDidChangeModelContent(() => {
      const cell = currentCell.current
      const model = ed.getModel()
      if (!cell || !model || applyingExternal.current) return
      controller.setProgram(cell, model.getValue())
    })
    const mouse = ed.onMouseDown((e) => {
      const t = e.target.type
      if (t === monaco.editor.MouseTargetType.GUTTER_GLYPH_MARGIN || t === monaco.editor.MouseTargetType.GUTTER_LINE_DECORATIONS) {
        const line = e.target.position?.lineNumber
        if (line && currentCell.current) void controller.toggleBreakpoint(currentCell.current, line)
      }
    })
    ed.addCommand(monaco.KeyCode.F9, () => {
      const line = ed.getPosition()?.lineNumber
      if (line && currentCell.current) void controller.toggleBreakpoint(currentCell.current, line)
    })
    return () => {
      sub.dispose()
      mouse.dispose()
      ed.dispose()
      models.current.forEach((m) => m.dispose())
      models.current.clear()
      editorRef.current = null
    }
  }, [])

  useEffect(() => {
    if (catalog) registerLanguages(catalog)
  }, [catalog])

  useEffect(() => {
    monaco.editor.setTheme(theme === 'dark' ? 'asmgraph-dark' : 'asmgraph-light')
  }, [theme])

  // A new puzzle discards all models.
  useEffect(() => {
    models.current.forEach((m) => m.dispose())
    models.current.clear()
    viewStates.current.clear()
  }, [puzzleId])

  // Switch models when the selected cell changes.
  useEffect(() => {
    const ed = editorRef.current
    if (!ed) return
    if (currentCell.current) viewStates.current.set(currentCell.current, ed.saveViewState())
    if (!cellId || !def || !programmable) {
      currentCell.current = null
      ed.setModel(null)
      return
    }
    let model = models.current.get(cellId)
    const source = useWorkspace.getState().solution?.programs[cellId] ?? ''
    if (!model || model.isDisposed()) {
      model = monaco.editor.createModel(source, languageId(def.type), monaco.Uri.parse(`asmgraph://${puzzleId}/${cellId}.asm`))
      models.current.set(cellId, model)
    }
    currentCell.current = cellId
    ed.setModel(model)
    const vs = viewStates.current.get(cellId)
    if (vs) ed.restoreViewState(vs)
  }, [cellId, def, programmable, puzzleId])

  // Apply external program changes (load, import, new solution) to models
  // without echoing them back as edits.
  useEffect(
    () =>
      useWorkspace.subscribe((s, prev) => {
        if (s.solution?.programs === prev.solution?.programs) return
        for (const [cell, model] of models.current) {
          const src = s.solution?.programs[cell] ?? ''
          if (!model.isDisposed() && model.getValue() !== src) {
            applyingExternal.current = true
            model.setValue(src)
            applyingExternal.current = false
          }
        }
      }),
    [],
  )

  // Markers: live assembler diagnostics plus compile diagnostics.
  const assemble = useWorkspace((s) => (cellId ? s.assemble[cellId] : undefined))
  const diagnostics = useWorkspace((s) => s.diagnostics)
  useEffect(() => {
    if (!cellId) return
    const model = models.current.get(cellId)
    if (!model || model.isDisposed()) return
    const all: Diagnostic[] = []
    if (assemble && assemble.source === model.getValue()) all.push(...assemble.diagnostics)
    all.push(...diagnostics.filter((d) => d.cell === cellId && d.sourceLine))
    const seen = new Set<string>()
    const markers: monaco.editor.IMarkerData[] = []
    for (const d of all) {
      const key = `${d.sourceLine}:${d.sourceColumn}:${d.message}`
      if (seen.has(key) || !d.sourceLine) continue
      seen.add(key)
      const line = Math.min(d.sourceLine, model.getLineCount())
      const col = Math.max(1, d.sourceColumn ?? 1)
      const word = model.getWordAtPosition({ lineNumber: line, column: col })
      markers.push({
        severity: d.severity === 'warning' ? monaco.MarkerSeverity.Warning : monaco.MarkerSeverity.Error,
        message: d.message,
        startLineNumber: line,
        startColumn: col,
        endLineNumber: line,
        endColumn: word ? word.endColumn : model.getLineMaxColumn(line),
      })
    }
    monaco.editor.setModelMarkers(model, 'asmgraph', markers)
  }, [assemble, diagnostics, cellId])

  // Execution and breakpoint decorations.
  const snap = useSim((s) => (cellId ? s.view.cells[cellId] : undefined))
  const started = useSim((s) => s.started)
  const breakpoints = useWorkspace((s) => (cellId ? s.breakpoints[cellId] : undefined))
  useEffect(() => {
    const ed = editorRef.current
    const coll = decorations.current
    if (!ed || !coll || !ed.getModel()) return
    const model = ed.getModel()!
    const decs: monaco.editor.IModelDeltaDecoration[] = []
    for (let l = 1; l <= model.getLineCount(); l++) {
      const bp = breakpoints?.includes(l)
      decs.push({ range: new monaco.Range(l, 1, l, 1), options: { glyphMarginClassName: bp ? 'asm-bp-glyph' : 'asm-bp-hint', glyphMarginHoverMessage: { value: bp ? 'Breakpoint (click or F9 to remove)' : 'Click to add a breakpoint' } } })
    }
    if (started && snap?.sourceLine && snap.sourceLine <= model.getLineCount()) {
      const blocked = snap.blocked
      decs.push({
        range: new monaco.Range(snap.sourceLine, 1, snap.sourceLine, 1),
        options: {
          isWholeLine: true,
          className: blocked ? 'asm-blocked-line' : 'asm-active-line',
          linesDecorationsClassName: blocked ? 'asm-pc-glyph-blocked' : 'asm-pc-glyph',
          hoverMessage: blocked && snap.blockReason ? { value: `Blocked: ${snap.blockReason}` } : undefined,
        },
      })
      ed.revealLineInCenterIfOutsideViewport(snap.sourceLine)
    }
    coll.set(decs)
  }, [snap, started, breakpoints, cellId])

  // Focus and reveal requests.
  const focusSignal = useUi((s) => s.focusEditor)
  useEffect(() => {
    if (focusSignal) editorRef.current?.focus()
  }, [focusSignal])
  const reveal = useUi((s) => s.reveal)
  useEffect(() => {
    if (!reveal || reveal.cell !== cellId) return
    const ed = editorRef.current
    if (!ed) return
    ed.revealLineInCenter(reveal.line)
    ed.setPosition({ lineNumber: reveal.line, column: 1 })
    ed.focus()
  }, [reveal, cellId])

  return (
    <div className="flex h-full flex-col bg-panel" data-testid="editor-pane">
      <EditorHeader />
      <div className="relative min-h-0 flex-1">
        <div ref={host} className={clsx('absolute inset-0', !programmable && 'invisible')} data-testid="monaco-host" />
        {!programmable && (
          <div className="absolute inset-0 flex items-center justify-center p-6 text-center text-fg-muted">
            {def ? (
              <div>
                <p className="label-caps mb-1">{def.id} is not programmable</p>
                <p>{typeInfo?.description}</p>
              </div>
            ) : (
              <p>Select a processor cell to edit its program.</p>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

function EditorHeader() {
  const cellId = useWorkspace((s) => s.selectedCell)
  const def = useWorkspace((s) => s.puzzleDoc?.puzzle.cells.find((c) => c.id === s.selectedCell))
  const assemble = useWorkspace((s) => (cellId ? s.assemble[cellId] : undefined))
  const source = useWorkspace((s) => (cellId ? s.solution?.programs[cellId] : undefined))
  const catalog = useWorkspace((s) => s.catalog)
  const programmable = def ? (cellType(catalog, def.type)?.programmable ?? false) : false
  const limit = def?.program_memory ?? 0
  const stale = assemble && assemble.source !== source
  const bytes = assemble?.bytes
  const over = bytes !== undefined && bytes > limit
  const errors = assemble ? assemble.diagnostics.filter((d) => d.severity === 'error').length : 0
  return (
    <div className="flex h-9 shrink-0 items-center gap-3 border-b border-line px-3">
      <span className="label-caps">Program</span>
      <span className="font-mono text-[12px] text-fg">{cellId ?? '—'}</span>
      {def && <span className="font-mono text-[10px] tracking-widest text-fg-muted uppercase">{def.type}</span>}
      {programmable && (
        <div className="ml-auto flex items-center gap-2" data-testid="memory-indicator">
          <span className="font-mono text-[11px] whitespace-nowrap text-fg-muted" title="Program memory: encoded bytecode size from the Go assembler">
            Program Memory:
          </span>
          <span className={clsx('font-mono text-[12px] whitespace-nowrap', over ? 'text-bad' : 'text-fg', stale && 'opacity-50')} data-testid="memory-value">
            {bytes ?? '?'} / {limit} bytes
          </span>
          <div className="h-1.5 w-20 rounded-sm bg-panel-2" aria-hidden>
            <div className={clsx('h-full rounded-sm', over ? 'bg-bad' : 'bg-accent')} style={{ width: `${Math.min(100, ((bytes ?? 0) / Math.max(1, limit)) * 100)}%` }} />
          </div>
          {over && <span className="text-[11px] text-bad">✗ over by {bytes! - limit}</span>}
          {!over && errors > 0 && <span className="text-[11px] text-bad">✗ {errors} error{errors > 1 ? 's' : ''}</span>}
          {!over && errors === 0 && assemble && !stale && <span className="text-[11px] text-ok">✓</span>}
          <Button variant="ghost" onClick={() => controller.compile()} title="Compile the whole solution (Mod+Shift+B)">
            Compile
          </Button>
        </div>
      )}
    </div>
  )
}
