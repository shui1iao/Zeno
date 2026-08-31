import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { AdminInlineSortList, adminSortAutoScrollVelocity, moveAdminItemInOrder, persistAdminItemOrder, placeAdminItemBesideTarget } from './AdminInlineSortList'

type Item = { id: string; name: string }

const items: Item[] = [
  { id: 'a', name: 'Alpha' },
  { id: 'b', name: 'Beta' },
]

describe('admin inline ordering helpers', () => {
  it('moves an item one step in either direction without mutating the source array', () => {
    const ids = ['a', 'b', 'c', 'd']

    expect(moveAdminItemInOrder(ids, 'a', 'c')).toEqual(['b', 'c', 'a', 'd'])
    expect(moveAdminItemInOrder(ids, 'd', 'b')).toEqual(['a', 'd', 'b', 'c'])
    expect(ids).toEqual(['a', 'b', 'c', 'd'])
    expect(moveAdminItemInOrder(ids, 'missing', 'b')).toBe(ids)
  })

  it('places the dragged item on the intended half of the target row', () => {
    const ids = ['a', 'b', 'c', 'd']

    expect(placeAdminItemBesideTarget(ids, 'a', 'c', false)).toEqual(['b', 'a', 'c', 'd'])
    expect(placeAdminItemBesideTarget(ids, 'a', 'c', true)).toEqual(['b', 'c', 'a', 'd'])
    expect(placeAdminItemBesideTarget(ids, 'd', 'b', false)).toEqual(['a', 'd', 'b', 'c'])
    expect(placeAdminItemBesideTarget(ids, 'd', 'b', true)).toEqual(['a', 'b', 'd', 'c'])
    expect(placeAdminItemBesideTarget(ids, 'a', 'a', false)).toBe(ids)
    expect(placeAdminItemBesideTarget(ids, 'missing', 'b', false)).toBe(ids)
  })

  it('auto-scrolls only near list edges and accelerates toward the boundary', () => {
    expect(adminSortAutoScrollVelocity(150, 100, 300)).toBe(0)
    expect(adminSortAutoScrollVelocity(120, 100, 300)).toBeLessThan(0)
    expect(adminSortAutoScrollVelocity(95, 100, 300)).toBe(-14)
    expect(adminSortAutoScrollVelocity(280, 100, 300)).toBeGreaterThan(0)
    expect(adminSortAutoScrollVelocity(305, 100, 300)).toBe(14)
    expect(adminSortAutoScrollVelocity(100, 100, 100)).toBe(0)
  })

  it('persists the reordered items immediately through the supplied callback', async () => {
    const saved: string[][] = []
    const itemById = new Map(items.map((item) => [item.id, item]))

    await persistAdminItemOrder(['b', 'a'], itemById, (nextItems) => {
      saved.push(nextItems.map((item) => item.id))
    })

    expect(saved).toEqual([['b', 'a']])
  })
})

describe('AdminInlineSortList', () => {
  it('renders drag handles directly in the supplied list rows', () => {
    const html = renderToStaticMarkup(
      <AdminInlineSortList
        items={items}
        listLabel="服务器列表"
        itemLabel="服务器"
        getDisplayName={(item) => item.name}
        listHeader={<div className="admin-list-head"><span>服务器</span></div>}
        renderRow={(item, { dragHandle }) => (
          <>
            <div className="admin-list-main admin-inline-sort-main">{dragHandle}<strong>{item.name}</strong></div>
            <span>{item.id}</span>
          </>
        )}
        onReorder={() => {}}
      />,
    )

    expect(html).toContain('admin-inline-sort-list')
    expect(html).toContain('admin-inline-sort-row')
    expect(html).toContain('aria-label="拖动 服务器 Alpha 调整顺序"')
    expect(html).toContain('aria-label="拖动 服务器 Beta 调整顺序"')
    expect(html).toContain('title="拖动调整顺序"')
    expect(html).not.toContain('admin-sort-modal')
    expect(html).not.toContain('admin-inline-sort-actions')
    expect(html).not.toContain('保存排序')
    expect(html).not.toContain('取消')
  })
})
