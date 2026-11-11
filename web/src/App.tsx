import { lazy, Suspense, useEffect, useRef } from 'react'
import { Group, Panel, Separator, type PanelImperativeHandle } from 'react-resizable-panels'
import { commands, installShortcuts } from './commands'
import { BottomPanel } from './components/BottomPanel'
import { CellInspector } from './components/CellInspector'
import {
  CommandPalette,
  ConnectionBanner,
  HelpDialog,
  ModalHost,
  Notices,
  PuzzleBrowser,
  SolutionsDialog,
} from './components/dialogs'
import { GraphView } from './components/graph/GraphView'
import { PuzzleSidebar } from './components/PuzzleSidebar'
import { Toolbar } from './components/Toolbar'
import { controller } from './services/controller'
import { usePrefs } from './state/prefs'
import { useWorkspace } from './state/workspace'

const EditorPane = lazy(() => import('./components/editor/EditorPane'))

/** Panel handles, so commands can collapse/expand panels. */
export const panels: Record<string, PanelImperativeHandle | null> = {}

function togglePanel(id: string) {
  const p = panels[id]
  if (!p) return
  if (p.isCollapsed()) p.expand()
  else p.collapse()
}

for (const [id, title] of [
  ['left', 'Toggle puzzle sidebar'],
  ['bottom', 'Toggle bottom panel'],
  ['right', 'Toggle editor panel'],
  ['inspector', 'Toggle cell inspector'],
] as const) {
  if (!commands.some((c) => c.id === `panel-${id}`)) commands.push({ id: `panel-${id}`, title, group: 'View', run: () => togglePanel(id) })
}

function PersistedGroup({ id, orientation, children }: { id: string; orientation: 'horizontal' | 'vertical'; children: React.ReactNode }) {
  const saved = usePrefs.getState().layouts[id]
  return (
    <Group
      id={id}
      orientation={orientation}
      defaultLayout={saved}
      onLayoutChanged={(layout) => usePrefs.getState().saveLayout(id, layout)}
      className="h-full w-full"
    >
      {children}
    </Group>
  )
}

function PanelSlot({ id, children, ...props }: { id: string; children: React.ReactNode } & Omit<React.ComponentProps<typeof Panel>, 'id' | 'panelRef'>) {
  const ref = useRef<PanelImperativeHandle | null>(null)
  useEffect(() => {
    panels[id] = ref.current
    return () => {
      panels[id] = null
    }
  }, [id])
  return (
    <Panel id={id} panelRef={ref} {...props}>
      {children}
    </Panel>
  )
}

export default function App() {
  const theme = usePrefs((s) => s.theme)
  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark')
  }, [theme])

  useEffect(() => {
    const uninstall = installShortcuts()
    void controller.init()
    const beforeUnload = (e: BeforeUnloadEvent) => {
      if (useWorkspace.getState().dirty) {
        e.preventDefault()
        e.returnValue = ''
      }
    }
    window.addEventListener('beforeunload', beforeUnload)
    return () => {
      uninstall()
      window.removeEventListener('beforeunload', beforeUnload)
      controller.dispose()
    }
  }, [])

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <Toolbar />
      <ConnectionBanner />
      <main className="min-h-0 flex-1">
        <PersistedGroup id="main" orientation="horizontal">
          <PanelSlot id="left" defaultSize="18" minSize="12" collapsible collapsedSize={0}>
            <PuzzleSidebar />
          </PanelSlot>
          <Separator />
          <PanelSlot id="center" minSize="25">
            <PersistedGroup id="center" orientation="vertical">
              <PanelSlot id="graph" minSize="20">
                <GraphView />
              </PanelSlot>
              <Separator />
              <PanelSlot id="bottom" defaultSize="32" minSize="10" collapsible collapsedSize={0}>
                <BottomPanel />
              </PanelSlot>
            </PersistedGroup>
          </PanelSlot>
          <Separator />
          <PanelSlot id="right" defaultSize="34" minSize="20" collapsible collapsedSize={0}>
            <PersistedGroup id="right" orientation="vertical">
              <PanelSlot id="editor" defaultSize="60" minSize="20">
                <Suspense fallback={<div className="p-3 text-fg-muted">Loading editor…</div>}>
                  <EditorPane />
                </Suspense>
              </PanelSlot>
              <Separator />
              <PanelSlot id="inspector" minSize="10" collapsible collapsedSize={0}>
                <CellInspector />
              </PanelSlot>
            </PersistedGroup>
          </PanelSlot>
        </PersistedGroup>
      </main>
      <PuzzleBrowser />
      <SolutionsDialog />
      <CommandPalette />
      <HelpDialog />
      <ModalHost />
      <Notices />
    </div>
  )
}
