import { type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type PointerEvent as ReactPointerEvent, type ReactNode, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { runMaybePromise } from '../../lib/maybePromise'
import type { MaybePromise } from './adminOperationalTypes'

type SortableItem = { id: string }

type AdminInlineSortDragState = {
  sourceId: string
  pointerId: number
  startY: number
  currentY: number
  rect: { top: number; left: number; width: number; height: number }
  originIds: string[]
}

export function moveAdminItemInOrder(itemIds: string[], sourceId: string, targetId: string): string[] {
  const sourceIndex = itemIds.indexOf(sourceId)
  const targetIndex = itemIds.indexOf(targetId)
  if (sourceIndex < 0 || targetIndex < 0 || sourceIndex === targetIndex) return itemIds
  const nextIds = [...itemIds]
  const [source] = nextIds.splice(sourceIndex, 1)
  nextIds.splice(targetIndex, 0, source)
  return nextIds
}

export function placeAdminItemBesideTarget(itemIds: string[], sourceId: string, targetId: string, afterTarget: boolean): string[] {
  if (sourceId === targetId || !itemIds.includes(sourceId) || !itemIds.includes(targetId)) return itemIds
  const nextIds = itemIds.filter((itemId) => itemId !== sourceId)
  const targetIndex = nextIds.indexOf(targetId)
  nextIds.splice(targetIndex + (afterTarget ? 1 : 0), 0, sourceId)
  return nextIds.every((itemId, index) => itemId === itemIds[index]) ? itemIds : nextIds
}

export function adminSortAutoScrollVelocity(pointerY: number, top: number, bottom: number): number {
  if (!Number.isFinite(pointerY) || !Number.isFinite(top) || !Number.isFinite(bottom) || bottom <= top) return 0
  const edgeSize = Math.min(48, (bottom - top) / 2)
  const maxVelocity = 14
  if (pointerY < top + edgeSize) {
    return -Math.max(1, Math.ceil(maxVelocity * Math.min(1, (top + edgeSize - pointerY) / edgeSize)))
  }
  if (pointerY > bottom - edgeSize) {
    return Math.max(1, Math.ceil(maxVelocity * Math.min(1, (pointerY - (bottom - edgeSize)) / edgeSize)))
  }
  return 0
}

function sameIds(left: string[], right: string[]): boolean {
  return left.length === right.length && left.every((id, index) => id === right[index])
}

export function persistAdminItemOrder<T extends SortableItem>(itemIds: string[], itemById: Map<string, T>, onReorder: (items: T[]) => MaybePromise): Promise<void> {
  const nextItems = itemIds.map((itemId) => itemById.get(itemId)).filter((item): item is T => Boolean(item))
  if (nextItems.length !== itemIds.length) return Promise.reject(new Error('排序项目已发生变化，请刷新后重试'))
  return runMaybePromise(() => onReorder(nextItems))
}

export interface AdminInlineSortRowContext {
  dragHandle: ReactNode
}

export interface AdminInlineSortListProps<T extends SortableItem> {
  items: T[]
  listLabel: string
  itemLabel: string
  className?: string
  getDisplayName: (item: T) => string
  listHeader: ReactNode
  renderRow: (item: T, context: AdminInlineSortRowContext) => ReactNode
  onReorder: (items: T[]) => MaybePromise
}

export function AdminInlineSortList<T extends SortableItem>({ items, listLabel, itemLabel, className = '', getDisplayName, listHeader, renderRow, onReorder }: AdminInlineSortListProps<T>) {
  const incomingIds = items.map((item) => item.id)
  const incomingOrderKey = incomingIds.join('\u0000')
  const itemById = new Map(items.map((item) => [item.id, item]))
  const [orderedIds, setOrderedIds] = useState(incomingIds)
  const [dragState, setDragState] = useState<AdminInlineSortDragState | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [sortAnnouncement, setSortAnnouncement] = useState('')
  const isDragging = dragState !== null
  const dragStateRef = useRef<AdminInlineSortDragState | null>(null)
  const dragFrameRef = useRef<number | null>(null)
  const autoScrollFrameRef = useRef<number | null>(null)
  const autoScrollVelocityRef = useRef(0)
  const pointerPositionRef = useRef<{ x: number; y: number } | null>(null)
  const dragPreviewRef = useRef<HTMLElement | null>(null)
  const orderedIdsRef = useRef(orderedIds)
  const itemByIdRef = useRef(itemById)
  const getDisplayNameRef = useRef(getDisplayName)

  orderedIdsRef.current = orderedIds
  itemByIdRef.current = itemById
  getDisplayNameRef.current = getDisplayName

  useEffect(() => {
    setOrderedIds((currentIds) => {
      if (!isDragging && !submitting) {
        if (sameIds(currentIds, incomingIds)) return currentIds
        orderedIdsRef.current = incomingIds
        return incomingIds
      }
      const availableIds = new Set(incomingIds)
      const retainedIds = currentIds.filter((itemId) => availableIds.has(itemId))
      const retainedSet = new Set(retainedIds)
      const appendedIds = incomingIds.filter((itemId) => !retainedSet.has(itemId))
      const nextIds = appendedIds.length === 0 && retainedIds.length === currentIds.length ? currentIds : [...retainedIds, ...appendedIds]
      orderedIdsRef.current = nextIds
      return nextIds
    })
  }, [incomingOrderKey, isDragging, submitting])

  const orderedItems = orderedIds.map((itemId) => itemById.get(itemId)).filter((item): item is T => Boolean(item))
  const activeDragItem = dragState ? itemById.get(dragState.sourceId) : undefined

  const persistOrder = (nextIds: string[], rollbackIds: string[], successAnnouncement: string) => {
    if (submitting || sameIds(nextIds, rollbackIds)) return
    orderedIdsRef.current = nextIds
    setOrderedIds(nextIds)
    setSubmitting(true)
    setFormError(null)
    setSortAnnouncement('正在应用排序')
    persistAdminItemOrder(nextIds, itemByIdRef.current, onReorder)
      .then(() => setSortAnnouncement(successAnnouncement))
      .catch((error: unknown) => {
        orderedIdsRef.current = rollbackIds
        setOrderedIds(rollbackIds)
        setFormError(error instanceof Error ? error.message : '应用排序失败')
        setSortAnnouncement('排序应用失败，已恢复原顺序')
      })
      .finally(() => setSubmitting(false))
  }

  const moveItemByStep = (itemId: string, step: -1 | 1) => {
    if (submitting) return
    const currentIds = orderedIdsRef.current
    const sourceIndex = currentIds.indexOf(itemId)
    const targetIndex = sourceIndex + step
    const targetId = currentIds[targetIndex]
    const item = itemById.get(itemId)
    if (!targetId || !item) return
    const nextIds = moveAdminItemInOrder(currentIds, itemId, targetId)
    persistOrder(nextIds, currentIds, `${getDisplayName(item)} 已调整为第 ${targetIndex + 1} 位`)
  }

  const handleSortKeyDown = (event: ReactKeyboardEvent<HTMLButtonElement>, itemId: string) => {
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      moveItemByStep(itemId, -1)
    } else if (event.key === 'ArrowDown') {
      event.preventDefault()
      moveItemByStep(itemId, 1)
    }
  }

  const beginPointerDrag = (event: ReactPointerEvent<HTMLButtonElement>, itemId: string) => {
    if (submitting || !event.isPrimary || event.button !== 0) return
    const row = event.currentTarget.closest<HTMLElement>('.admin-inline-sort-row')
    if (!row) return
    event.preventDefault()
    const bounds = row.getBoundingClientRect()
    const nextDrag: AdminInlineSortDragState = {
      sourceId: itemId,
      pointerId: event.pointerId,
      startY: event.clientY,
      currentY: event.clientY,
      rect: { top: bounds.top, left: bounds.left, width: bounds.width, height: bounds.height },
      originIds: [...orderedIdsRef.current],
    }
    dragStateRef.current = nextDrag
    setDragState(nextDrag)
  }

  useEffect(() => {
    if (!dragStateRef.current) return undefined
    const updateDropTarget = (clientX: number, clientY: number, currentDrag: AdminInlineSortDragState) => {
      const targetRow = document.elementFromPoint(clientX, clientY)?.closest<HTMLElement>('.admin-inline-sort-row:not(.is-placeholder)')
      const targetId = targetRow?.dataset.sortItemId
      if (!targetId || targetId === currentDrag.sourceId) return
      const targetBounds = targetRow.getBoundingClientRect()
      const afterTarget = clientY >= targetBounds.top + targetBounds.height / 2
      const nextIds = placeAdminItemBesideTarget(orderedIdsRef.current, currentDrag.sourceId, targetId, afterTarget)
      if (nextIds === orderedIdsRef.current) return
      orderedIdsRef.current = nextIds
      setOrderedIds(nextIds)
    }
    const flushDragFrame = () => {
      const currentDrag = dragStateRef.current
      const pointer = pointerPositionRef.current
      if (!currentDrag || !pointer) return
      dragPreviewRef.current?.style.setProperty('--admin-sort-drag-y', `${currentDrag.currentY - currentDrag.startY}px`)
      updateDropTarget(pointer.x, pointer.y, currentDrag)
    }
    const publishDragPosition = () => {
      if (dragFrameRef.current !== null) return
      dragFrameRef.current = window.requestAnimationFrame(() => {
        dragFrameRef.current = null
        flushDragFrame()
      })
    }
    const stopAutoScroll = () => {
      autoScrollVelocityRef.current = 0
      if (autoScrollFrameRef.current !== null) {
        window.cancelAnimationFrame(autoScrollFrameRef.current)
        autoScrollFrameRef.current = null
      }
    }
    const startAutoScroll = () => {
      if (autoScrollFrameRef.current !== null || autoScrollVelocityRef.current === 0) return
      const scroll = () => {
        autoScrollFrameRef.current = null
        const currentDrag = dragStateRef.current
        const pointer = pointerPositionRef.current
        const velocity = autoScrollVelocityRef.current
        if (!currentDrag || !pointer || velocity === 0) return
        window.scrollBy(0, velocity)
        publishDragPosition()
        autoScrollFrameRef.current = window.requestAnimationFrame(scroll)
      }
      autoScrollFrameRef.current = window.requestAnimationFrame(scroll)
    }
    const finishDrag = (cancelled: boolean) => {
      const currentDrag = dragStateRef.current
      if (!currentDrag) return
      if (dragFrameRef.current !== null) {
        window.cancelAnimationFrame(dragFrameRef.current)
        dragFrameRef.current = null
      }
      stopAutoScroll()
      if (!cancelled) flushDragFrame()
      pointerPositionRef.current = null
      if (cancelled) {
        orderedIdsRef.current = currentDrag.originIds
        setOrderedIds(currentDrag.originIds)
      } else {
        const finalIds = orderedIdsRef.current
        const item = itemByIdRef.current.get(currentDrag.sourceId)
        const finalIndex = finalIds.indexOf(currentDrag.sourceId)
        if (item && finalIndex >= 0 && !sameIds(finalIds, currentDrag.originIds)) {
          persistOrder(finalIds, currentDrag.originIds, `${getDisplayNameRef.current(item)} 已调整为第 ${finalIndex + 1} 位`)
        }
      }
      dragStateRef.current = null
      setDragState(null)
    }
    const handlePointerMove = (event: PointerEvent) => {
      const currentDrag = dragStateRef.current
      if (!currentDrag || currentDrag.pointerId !== event.pointerId) return
      if (event.cancelable) event.preventDefault()
      dragStateRef.current = { ...currentDrag, currentY: event.clientY }
      pointerPositionRef.current = { x: event.clientX, y: event.clientY }
      publishDragPosition()
      autoScrollVelocityRef.current = adminSortAutoScrollVelocity(event.clientY, 32, Math.max(64, window.innerHeight - 32))
      if (autoScrollVelocityRef.current === 0) stopAutoScroll()
      else startAutoScroll()
    }
    const handlePointerUp = (event: PointerEvent) => {
      if (dragStateRef.current?.pointerId !== event.pointerId) return
      if (event.cancelable) event.preventDefault()
      finishDrag(false)
    }
    const handlePointerCancel = (event: PointerEvent) => {
      if (dragStateRef.current?.pointerId === event.pointerId) finishDrag(true)
    }
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') finishDrag(true)
    }
    const handleWindowBlur = () => finishDrag(true)
    window.addEventListener('pointermove', handlePointerMove, { passive: false })
    window.addEventListener('pointerup', handlePointerUp, { passive: false })
    window.addEventListener('pointercancel', handlePointerCancel)
    window.addEventListener('keydown', handleKeyDown)
    window.addEventListener('blur', handleWindowBlur)
    return () => {
      window.removeEventListener('pointermove', handlePointerMove)
      window.removeEventListener('pointerup', handlePointerUp)
      window.removeEventListener('pointercancel', handlePointerCancel)
      window.removeEventListener('keydown', handleKeyDown)
      window.removeEventListener('blur', handleWindowBlur)
      if (dragFrameRef.current !== null) {
        window.cancelAnimationFrame(dragFrameRef.current)
        dragFrameRef.current = null
      }
      stopAutoScroll()
    }
  }, [dragState !== null])

  const createDragHandle = (item: T, isPreview = false) => isPreview ? (
    <span className="admin-drag-handle" aria-hidden="true"><AdminSortGrip /></span>
  ) : (
    <button
      className="admin-drag-handle"
      type="button"
      disabled={submitting}
      aria-label={`拖动 ${itemLabel} ${getDisplayName(item)} 调整顺序`}
      title="拖动调整顺序"
      onPointerDown={(event) => beginPointerDrag(event, item.id)}
      onKeyDown={(event) => handleSortKeyDown(event, item.id)}
    ><AdminSortGrip /></button>
  )

  return (
    <>
      <div className={['admin-list', 'admin-inline-sort-list', className].filter(Boolean).join(' ')} role="list" aria-label={listLabel} aria-busy={submitting}>
        {listHeader}
        {orderedItems.map((item) => {
          const isPlaceholder = dragState?.sourceId === item.id
          return (
            <article
              className={`admin-list-row admin-inline-sort-row${isPlaceholder ? ' is-placeholder' : ''}`}
              role="listitem"
              key={item.id}
              data-sort-item-id={item.id}
            >
              {renderRow(item, { dragHandle: createDragHandle(item) })}
            </article>
          )
        })}
      </div>
      {formError && <p className="admin-inline-note is-error" role="alert">{formError}</p>}
      <p className="sr-only" aria-live="polite" aria-atomic="true">{sortAnnouncement}</p>
      {dragState && activeDragItem && typeof document !== 'undefined' && createPortal(
        <article
          ref={dragPreviewRef}
          className="zeno-overlay-surface admin-list-row admin-inline-sort-row is-drag-preview"
          aria-hidden="true"
          style={{
            '--admin-sort-drag-y': '0px',
            top: dragState.rect.top,
            left: dragState.rect.left,
            width: dragState.rect.width,
            height: dragState.rect.height,
          } as CSSProperties}
        >
          {renderRow(activeDragItem, { dragHandle: createDragHandle(activeDragItem, true) })}
        </article>,
        document.body,
      )}
    </>
  )
}

function AdminSortGrip() {
  return (
    <svg viewBox="0 0 20 20" aria-hidden="true">
      <circle cx="7" cy="5" r="1" /><circle cx="13" cy="5" r="1" />
      <circle cx="7" cy="10" r="1" /><circle cx="13" cy="10" r="1" />
      <circle cx="7" cy="15" r="1" /><circle cx="13" cy="15" r="1" />
    </svg>
  )
}
