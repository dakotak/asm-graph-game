// Automatic graph layout with dagre. Visual positions never affect
// execution; this only produces a readable starting arrangement. dagre
// breaks cycles internally, so cyclic topologies are fine.
import { graphlib, layout } from '@dagrejs/dagre'
import type { CellDef, Connection } from '../api/types'
import type { Position } from './solution'

export const NODE_WIDTH = 220
export const NODE_HEIGHT = 150

export function autoLayout(cells: CellDef[], connections: Connection[], isInput: (c: CellDef) => boolean, isOutput: (c: CellDef) => boolean): Record<string, Position> {
  const g = new graphlib.Graph({ multigraph: true })
  g.setGraph({ rankdir: 'LR', nodesep: 50, ranksep: 90, marginx: 20, marginy: 20 })
  g.setDefaultEdgeLabel(() => ({}))
  for (const c of cells) g.setNode(c.id, { width: NODE_WIDTH, height: NODE_HEIGHT })
  connections.forEach((c, i) => {
    // Orient edges input -> output where the direction is obvious so data
    // flows left to right; otherwise keep the solution's from -> to.
    let a = c.from.cell
    let b = c.to.cell
    const ca = cells.find((x) => x.id === a)
    const cb = cells.find((x) => x.id === b)
    if ((cb && isInput(cb)) || (ca && isOutput(ca))) [a, b] = [b, a]
    g.setEdge(a, b, {}, `e${i}`)
  })
  // Unconnected cells: inputs on the left, outputs on the right.
  const inputs = cells.filter(isInput)
  const outputs = cells.filter(isOutput)
  layout(g)
  const pos: Record<string, Position> = {}
  for (const c of cells) {
    const n = g.node(c.id) as { x: number; y: number } | undefined
    pos[c.id] = n ? { x: n.x - NODE_WIDTH / 2, y: n.y - NODE_HEIGHT / 2 } : { x: 0, y: 0 }
  }
  if (connections.length === 0) {
    // Without edges dagre stacks everything in one column; use three columns.
    const middle = cells.filter((c) => !isInput(c) && !isOutput(c))
    const col = (list: CellDef[], x: number) =>
      list.forEach((c, i) => {
        pos[c.id] = { x, y: i * (NODE_HEIGHT + 40) }
      })
    col(inputs, 0)
    col(middle, NODE_WIDTH + 120)
    col(outputs, 2 * (NODE_WIDTH + 120))
  }
  return pos
}
