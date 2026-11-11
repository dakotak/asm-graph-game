import {
  Background,
  BackgroundVariant,
  ConnectionMode,
  Controls,
  MiniMap,
  ReactFlow,
  ReactFlowProvider,
  useNodesInitialized,
  useReactFlow,
  type Connection as RFConnection,
  type Edge,
  type IsValidConnection,
  type Node,
  type NodeChange,
  applyNodeChanges,
} from '@xyflow/react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { controller } from '../../services/controller'
import { checkConnection, connectionId } from '../../lib/connections'
import { usePrefs } from '../../state/prefs'
import { notify } from '../../state/sim'
import { useUi } from '../../state/ui'
import { directionOf, useWorkspace } from '../../state/workspace'
import { Button } from '../ui'
import { CellNode, type CellNodeData } from './CellNode'
import { WireEdge } from './WireEdge'

const nodeTypes = { cell: CellNode }
const edgeTypes = { wire: WireEdge }

function Graph() {
  const cells = useWorkspace((s) => s.cells)
  const layout = useWorkspace((s) => s.solution?.layout)
  const connections = useWorkspace((s) => s.solution?.connections)
  const selectedCell = useWorkspace((s) => s.selectedCell)
  const selectedConnection = useWorkspace((s) => s.selectedConnection)
  const snap = usePrefs((s) => s.snapToGrid)
  const theme = usePrefs((s) => s.theme)
  const fitSignal = useUi((s) => s.fitView)
  const focusSignal = useUi((s) => s.focusGraph)
  const rf = useReactFlow()
  const wrapper = useRef<HTMLDivElement>(null)

  // Nodes are local state while dragging; positions are committed to the
  // solution (as presentation metadata) when a drag ends.
  const [nodes, setNodes] = useState<Node<CellNodeData>[]>([])
  useEffect(() => {
    setNodes(
      (cells ?? []).map((c) => ({
        id: c.id,
        type: 'cell',
        position: layout?.[c.id] ?? { x: 0, y: 0 },
        data: { cellId: c.id },
        // Player-built hardware can be removed with Delete; puzzle cells cannot.
        deletable: c.player,
        selected: c.id === selectedCell,
      })),
    )
  }, [cells, layout, selectedCell])

  const edges: Edge[] = useMemo(
    () =>
      (connections ?? []).map((c) => {
        const id = connectionId(c)
        return {
          id,
          type: 'wire',
          source: c.from.cell,
          sourceHandle: c.from.port,
          target: c.to.cell,
          targetHandle: c.to.port,
          selected: id === selectedConnection,
          ariaLabel: `Connection ${c.from.cell}.${c.from.port} to ${c.to.cell}.${c.to.port}`,
        }
      }),
    [connections, selectedConnection],
  )

  useEffect(() => {
    if (fitSignal) requestAnimationFrame(() => void rf.fitView({ padding: 0.15, duration: 200 }))
  }, [fitSignal, rf])
  // Fit each newly opened puzzle once React Flow has measured its nodes
  // (a fit requested before measurement would be a no-op).
  const nodesReady = useNodesInitialized()
  const puzzleId = useWorkspace((s) => s.puzzleDoc?.puzzle.id)
  const fittedFor = useRef<string | null>(null)
  useEffect(() => {
    if (nodesReady && puzzleId && fittedFor.current !== puzzleId) {
      fittedFor.current = puzzleId
      void rf.fitView({ padding: 0.15 })
    }
  }, [nodesReady, puzzleId, rf])
  useEffect(() => {
    if (focusSignal) wrapper.current?.querySelector<HTMLElement>('.react-flow__pane, .react-flow')?.focus()
  }, [focusSignal])

  const onNodesChange = useCallback((changes: NodeChange<Node<CellNodeData>>[]) => {
    setNodes((ns) => applyNodeChanges(changes, ns))
    const done: Record<string, { x: number; y: number }> = {}
    for (const ch of changes) {
      if (ch.type === 'position' && ch.dragging === false && ch.position) done[ch.id] = ch.position
    }
    if (Object.keys(done).length) controller.movedCells(done)
  }, [])

  const isValidConnection: IsValidConnection = useCallback((c) => {
    const ws = useWorkspace.getState()
    if (!ws.puzzleDoc || !ws.solution || !c.sourceHandle || !c.targetHandle) return false
    return checkConnection(
      ws.cells,
      ws.solution.connections,
      { cell: c.source, port: c.sourceHandle },
      { cell: c.target, port: c.targetHandle },
      directionOf(ws),
    ).ok
  }, [])

  const onConnect = useCallback((c: RFConnection) => {
    if (!c.sourceHandle || !c.targetHandle) return
    const r = controller.connect({ cell: c.source, port: c.sourceHandle }, { cell: c.target, port: c.targetHandle })
    if (!r.ok) notify('warn', r.reason)
  }, [])

  return (
    <div ref={wrapper} className="relative h-full w-full" data-testid="graph-canvas">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onConnect={onConnect}
        isValidConnection={isValidConnection}
        connectionMode={ConnectionMode.Loose}
        onEdgesDelete={(es) => es.forEach((e) => controller.disconnect(e.id))}
        onNodesDelete={(ns) => ns.forEach((n) => controller.removeNode(n.id))}
        onNodeClick={(_, n) => {
          useWorkspace.getState().select(n.id)
          useWorkspace.getState().selectConnection(null)
        }}
        onNodeDoubleClick={() => useUi.setState({ focusEditor: useUi.getState().focusEditor + 1 })}
        onEdgeClick={(_, e) => useWorkspace.getState().selectConnection(e.id)}
        onPaneClick={() => useWorkspace.getState().selectConnection(null)}
        deleteKeyCode={['Delete', 'Backspace']}
        snapToGrid={snap}
        snapGrid={[10, 10]}
        fitView
        fitViewOptions={{ padding: 0.15 }}
        minZoom={0.2}
        maxZoom={2}
        colorMode={theme}
        proOptions={{ hideAttribution: true }}
        nodesFocusable
        edgesFocusable
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1} color="var(--c-line)" />
        <Controls showInteractive={false} position="bottom-left" />
        <MiniMap pannable zoomable position="bottom-right" nodeColor="var(--c-line-strong)" maskColor="rgb(0 0 0 / 0.25)" style={{ width: 140, height: 90 }} />
      </ReactFlow>
      <GraphToolbar />
    </div>
  )
}

/**
 * Add-hardware menu. Shown for puzzles that let players build hardware;
 * fixed-topology puzzles get a label explaining why nothing can be added.
 */
function AddHardwareMenu() {
  const catalog = useWorkspace((s) => s.hardwareCatalog)
  const hardware = useWorkspace((s) => s.solution?.hardware)
  const counts = useMemo(() => {
    const m: Record<string, number> = {}
    for (const n of hardware ?? []) m[n.type] = (m[n.type] ?? 0) + 1
    return m
  }, [hardware])
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as globalThis.Node)) setOpen(false)
    }
    window.addEventListener('mousedown', close)
    return () => window.removeEventListener('mousedown', close)
  }, [open])
  if (!catalog) {
    return (
      <span
        className="flex items-center px-2 text-[11px] text-fg-faint"
        title="This puzzle supplies all of its hardware. Puzzles marked “build your hardware” in the puzzle browser let you add your own."
        data-testid="fixed-hardware"
      >
        Fixed hardware
      </span>
    )
  }
  return (
    <div ref={ref} className="relative">
      <Button variant="ghost" onClick={() => setOpen((o) => !o)} aria-expanded={open} data-testid="add-hardware" title="Add a hardware node">
        + Add node ▾
      </Button>
      {open && (
        <ul className="absolute top-full left-0 z-20 mt-1 min-w-48 rounded-sm border border-line bg-panel p-1 shadow-md" role="menu">
          {catalog.map((e) => {
            const n = counts[e.type] ?? 0
            const full = e.max > 0 && n >= e.max
            return (
              <li key={e.type}>
                <button
                  role="menuitem"
                  disabled={full}
                  className="flex w-full items-baseline gap-2 rounded-sm px-2 py-1 text-left font-mono text-[12px] text-fg hover:bg-panel-2 disabled:opacity-40"
                  onClick={() => {
                    controller.addNode(e.type)
                    setOpen(false)
                  }}
                  data-testid={`add-${e.type}`}
                >
                  {e.type}
                  <span className="text-[9px] tracking-widest text-fg-muted uppercase">{e.spec.category}</span>
                  <span className="ml-auto text-[10px] text-fg-muted">{full ? `max ${e.max}` : e.max > 0 ? `${n}/${e.max}` : n || ''}</span>
                </button>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

function GraphToolbar() {
  const snap = usePrefs((s) => s.snapToGrid)
  const anim = usePrefs((s) => s.animateTransfers)
  const selectedConnection = useWorkspace((s) => s.selectedConnection)
  const hasPuzzle = useWorkspace((s) => !!s.puzzleDoc)
  if (!hasPuzzle) return null
  return (
    <div className="absolute top-2 left-2 z-10 flex gap-1 rounded-sm border border-line bg-panel/90 p-1 backdrop-blur">
      <AddHardwareMenu />
      <Button variant="ghost" onClick={() => controller.autoLayout()} title="Arrange cells automatically (dagre)">
        Auto layout
      </Button>
      <Button variant="ghost" onClick={() => useUi.setState({ fitView: useUi.getState().fitView + 1 })}>
        Fit
      </Button>
      <Button variant="ghost" aria-pressed={snap} onClick={() => usePrefs.getState().set({ snapToGrid: !snap })}>
        {snap ? '▣' : '□'} Snap
      </Button>
      <Button variant="ghost" aria-pressed={anim} onClick={() => usePrefs.getState().set({ animateTransfers: !anim })} title="Animate port transfers">
        {anim ? '▣' : '□'} Animate
      </Button>
      {selectedConnection && (
        <Button variant="danger" onClick={() => controller.disconnect(selectedConnection)} title="Delete the selected connection (Del)">
          Disconnect
        </Button>
      )}
    </div>
  )
}

export function GraphView() {
  const hasPuzzle = useWorkspace((s) => !!s.puzzleDoc)
  if (!hasPuzzle) {
    return (
      <div className="flex h-full items-center justify-center text-fg-muted">
        <div className="text-center">
          <p className="label-caps mb-2">No puzzle open</p>
          <Button onClick={() => useUi.setState({ browser: true })}>Browse puzzles</Button>
        </div>
      </div>
    )
  }
  return (
    <ReactFlowProvider>
      <Graph />
    </ReactFlowProvider>
  )
}
