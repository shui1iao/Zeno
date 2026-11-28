import { memo } from 'react'
import type { HomeCardNode } from '../types'
import { nodeStatusLabels, nodeVisualStatus } from './ServerCard'

function sameStatusMatrix(left?: ReadonlyArray<HomeCardNode>, right?: ReadonlyArray<HomeCardNode>): boolean {
  if (left === right) return true
  if (!left || !right || left.length !== right.length) return false
  return left.every((node, index) => node.id === right[index].id && node.status === right[index].status && node.displayName === right[index].displayName)
}

/** One cell per node in display order; offline cells are red and open the node. */
const HomeStatusMatrix = memo(function HomeStatusMatrix({ nodes, onOpenNode }: { nodes: ReadonlyArray<HomeCardNode>; onOpenNode?: (nodeId: string) => void }) {
  if (nodes.length === 0) return null
  return (
    <span className="home-summary__matrix" role="group" aria-label="节点状态">
      {nodes.map((node) => {
        const status = nodeVisualStatus(node.status)
        const label = `${node.displayName} · ${nodeStatusLabels[status]}`
        return (
          <button
            key={node.id}
            type="button"
            className={status === 'online' ? undefined : `is-${status}`}
            title={label}
            aria-label={label}
            onClick={() => onOpenNode?.(node.id)}
          />
        )
      })}
    </span>
  )
}, (prev, next) => prev.onOpenNode === next.onOpenNode && sameStatusMatrix(prev.nodes, next.nodes))

export default HomeStatusMatrix
