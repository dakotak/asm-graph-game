import { BaseEdge, EdgeLabelRenderer, getSmoothStepPath, type EdgeProps } from '@xyflow/react'
import { memo } from 'react'
import { useSim } from '../../state/sim'

/**
 * A connection between two ports. When the backend reports a transfer on
 * this connection, the wire flashes and shows the transferred value next
 * to the writer's end for a couple of ticks.
 */
function WireEdgeImpl({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, source, sourceHandleId, selected }: EdgeProps) {
  const activity = useSim((s) => s.edges[id])
  const tick = useSim((s) => s.view.tick)
  const [path, labelX, labelY] = getSmoothStepPath({ sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, borderRadius: 6 })
  const fresh = activity && tick - activity.tick <= 1
  // Direction of travel relative to this edge's drawn source.
  const forward = activity ? activity.from === `${source}:${sourceHandleId}` : true
  return (
    <>
      <BaseEdge
        id={id}
        path={path}
        className={fresh ? 'edge-flash' : undefined}
        key={fresh ? activity.serial : 'idle'}
        style={{ strokeWidth: selected ? 3 : 2, stroke: selected ? 'var(--c-accent)' : undefined }}
        interactionWidth={16}
      />
      {fresh && (
        <EdgeLabelRenderer>
          <div
            key={activity.serial}
            className="value-pop pointer-events-none absolute rounded-sm border border-accent bg-panel px-1 font-mono text-[11px] text-accent"
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
            data-testid={`transfer-${id}`}
          >
            {forward ? '' : '◂ '}
            {activity.value}
            {forward ? ' ▸' : ''}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
}

export const WireEdge = memo(WireEdgeImpl)
