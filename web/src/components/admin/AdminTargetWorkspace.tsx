import { type FormEvent, useState } from 'react'
import type { AdminProbeTargetInput, AdminProbeTargetUpdateInput } from '../../api/adminClient'
import { sortAdminProbeTargets } from '../../lib/adminCollections'
import { runMaybePromise } from '../../lib/maybePromise'
import type { AdminNode, AdminProbeTarget, ProbeType } from '../../types'
import { AdminExpandedCheckList, AdminSegmentedField } from './AdminFields'
import { AdminInlineSortList } from './AdminInlineSortList'
import { AdminDeleteConfirmModal, AdminFormSection, AdminModal, AdminActionFooter, AdminRowActions, AdminWorkspaceHeading } from './AdminPrimitives'
import { formatTargetAssignmentSummary, formatTargetEndpoint, normalizeTargetFormType, parsePositiveInt, targetAssignmentRows, targetTypeOptions } from './adminOperationalModel'
import type { AdminTargetWorkspaceProps, MaybePromise } from './adminOperationalTypes'

export function AdminTargetWorkspace({ targets, nodes, onCreate, onUpdate, onReorder, onDelete }: AdminTargetWorkspaceProps) {
  const [creatingTarget, setCreatingTarget] = useState(false)
  const [editingTargetId, setEditingTargetId] = useState<string | null>(null)
  const editingTarget = editingTargetId ? targets.find((target) => target.id === editingTargetId) : undefined
  const sortedTargets = sortAdminProbeTargets(targets)

  return (
    <section className="admin-target-section admin-workspace-panel" aria-label="admin probe target list">
      <AdminWorkspaceHeading
        title="延迟监控"
        actions={
          <button className="admin-primary-action" type="button" onClick={() => setCreatingTarget(true)}>添加目标</button>
        }
      />

      {targets.length === 0 && <div className="admin-state-card">还没有探针目标。</div>}
      {targets.length > 0 && <AdminTargetList targets={sortedTargets} onEdit={setEditingTargetId} onDelete={onDelete} onReorder={onReorder} />}

      {creatingTarget && (
        <AdminTargetCreateModal
          nodes={nodes}
          onClose={() => setCreatingTarget(false)}
          onCreate={onCreate}
        />
      )}

      {editingTarget && (
        <AdminTargetEditModal
          key={editingTarget.id}
          target={editingTarget}
          nodes={nodes}
          onClose={() => setEditingTargetId(null)}
          onUpdate={onUpdate}
        />
      )}
    </section>
  )
}

function AdminTargetList({ targets, onEdit, onDelete, onReorder }: { targets: AdminProbeTarget[]; onEdit: (targetId: string) => void; onDelete: (targetId: string) => MaybePromise; onReorder: (targetIds: string[]) => MaybePromise }) {
  const [pendingDelete, setPendingDelete] = useState<AdminProbeTarget | null>(null)

  return (
    <>
      <AdminInlineSortList
        items={targets}
        className="admin-target-list"
        listLabel="延迟监控目标列表"
        itemLabel="延迟监控"
        getDisplayName={(target) => target.name}
        listHeader={(
          <div className="admin-list-head" aria-hidden="true">
            <span>目标</span>
            <span>地址</span>
            <span>节点</span>
            <span>操作</span>
          </div>
        )}
        renderRow={(target, { dragHandle }) => (
          <>
            <div className="admin-list-main admin-inline-sort-main">
              {dragHandle}
              <strong>{target.name}</strong>
            </div>
            <span data-label="地址">{formatTargetEndpoint(target)}</span>
            <span data-label="节点">{formatTargetAssignmentSummary(target)}</span>
            <AdminRowActions
              entityLabel="目标"
              name={target.name}
              onEdit={() => onEdit(target.id)}
              onDelete={() => setPendingDelete(target)}
            />
          </>
        )}
        onReorder={(nextTargets) => onReorder(nextTargets.map((target) => target.id))}
      />
      {pendingDelete && (
        <AdminDeleteConfirmModal
          title="删除延迟监控"
          subjectName={pendingDelete.name}
          confirmLabel="删除延迟监控"
          onClose={() => setPendingDelete(null)}
          onConfirm={() => onDelete(pendingDelete.id)}
        />
      )}
    </>
  )
}

function AdminTargetCreateModal({ nodes, onCreate, onClose }: { nodes: AdminNode[]; onCreate: (input: AdminProbeTargetInput) => MaybePromise; onClose: () => void }) {
  const [targetType, setTargetType] = useState<ProbeType>('tcping')
  const [assignmentNodeIds, setAssignmentNodeIds] = useState<string[]>([])
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    const formData = new FormData(event.currentTarget)
    const name = String(formData.get('new-target-name') ?? '').trim()
    const type = normalizeTargetFormType(String(formData.get('new-target-type') ?? 'tcping'))
    const address = String(formData.get('new-target-address') ?? '').trim()
    const port = type === 'tcping' ? parsePositiveInt(String(formData.get('new-target-port') ?? '')) : null
    if (name === '' || address === '' || (type === 'tcping' && port === null)) return
    setSubmitting(true)
    setFormError(null)
    runMaybePromise(() => onCreate({
      name,
      type,
      address,
      port,
      count: parsePositiveInt(String(formData.get('new-target-count') ?? '')) ?? 3,
      timeoutMs: parsePositiveInt(String(formData.get('new-target-timeout-ms') ?? '')) ?? 1000,
      intervalSec: parsePositiveInt(String(formData.get('new-target-interval-sec') ?? '')) ?? 30,
      assignments: nodes.map((node) => ({ nodeId: node.id, enabled: assignmentNodeIds.includes(node.id) })),
    }))
      .then(() => onClose())
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '添加失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <AdminModal title="添加延迟监控目标" closeDisabled={submitting} onClose={onClose}>
      <form className="admin-target-create-form admin-node-edit-form is-sectioned" aria-label="添加探针目标" aria-busy={submitting} inert={submitting ? true : undefined} onSubmit={handleSubmit}>
        <AdminFormSection title="目标信息">
          <div className="admin-form-grid">
            <label>
              <span>目标名称</span>
              <input name="new-target-name" autoComplete="off" placeholder="Example HTTPS" />
            </label>
            <AdminSegmentedField name="new-target-type" label="类型" value={targetType} onChange={(value) => setTargetType(normalizeTargetFormType(value))} options={targetTypeOptions} />
            <label>
              <span>地址</span>
              <input name="new-target-address" autoComplete="off" placeholder="example.com" />
            </label>
            {targetType === 'tcping' && (
              <label>
                <span>端口</span>
                <input name="new-target-port" type="number" min="1" max="65535" defaultValue="443" />
              </label>
            )}
          </div>
        </AdminFormSection>
        {nodes.length > 0 && (
          <AdminFormSection title="启用服务器">
            <AdminExpandedCheckList
              title="已启用服务器"
              panelLabel="选择服务器"
              emptyText="暂无服务器"
              options={nodes.map((node) => ({ value: node.id, label: node.displayName || node.id }))}
              value={assignmentNodeIds}
              onChange={setAssignmentNodeIds}
            />
          </AdminFormSection>
        )}
        <AdminFormSection title="探测参数">
          <div className="admin-form-grid">
            <label>
              <span>次数</span>
              <input name="new-target-count" type="number" min="1" defaultValue="3" />
            </label>
            <label>
              <span>超时 ms</span>
              <input name="new-target-timeout-ms" type="number" min="1" defaultValue="1000" />
            </label>
            <label>
              <span>间隔 s</span>
              <input name="new-target-interval-sec" type="number" min="1" defaultValue="30" />
            </label>
          </div>
        </AdminFormSection>
        <AdminActionFooter error={formError}>
          <button type="submit" disabled={submitting}>{submitting ? '添加中…' : '添加目标'}</button>
        </AdminActionFooter>
      </form>
    </AdminModal>
  )
}

function AdminTargetEditModal({ target, nodes, onUpdate, onClose }: { target: AdminProbeTarget; nodes: AdminNode[]; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => MaybePromise; onClose: () => void }) {
  const [targetType, setTargetType] = useState<ProbeType>(target.type)
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const assignmentRows = targetAssignmentRows(target, nodes)
  const [assignmentNodeIds, setAssignmentNodeIds] = useState<string[]>(() => assignmentRows.filter((assignment) => assignment.enabled).map((assignment) => assignment.nodeId))
  const selectedAssignmentNodes = new Set(assignmentNodeIds)

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    const formData = new FormData(event.currentTarget)
    const type = normalizeTargetFormType(String(formData.get('target-type') ?? targetType))
    const port = type === 'tcping' ? parsePositiveInt(String(formData.get('target-port') ?? '')) : null
    if (type === 'tcping' && port === null) return
    setSubmitting(true)
    setFormError(null)
    runMaybePromise(() => onUpdate(target.id, {
      name: String(formData.get('target-name') ?? ''),
      type,
      address: String(formData.get('target-address') ?? ''),
      port,
      count: parsePositiveInt(String(formData.get('target-count') ?? '')) ?? target.count,
      timeoutMs: parsePositiveInt(String(formData.get('target-timeout-ms') ?? '')) ?? target.timeoutMs,
      intervalSec: parsePositiveInt(String(formData.get('target-interval-sec') ?? '')) ?? target.intervalSec,
      assignments: assignmentRows.length > 0
        ? assignmentRows.map((assignment) => ({
            nodeId: assignment.nodeId,
            enabled: selectedAssignmentNodes.has(assignment.nodeId),
          }))
        : undefined,
    }))
      .then(() => onClose())
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '保存失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <AdminModal title="编辑延迟监控" closeDisabled={submitting} onClose={onClose}>
      <form className="admin-target-edit-form admin-node-edit-form is-sectioned" aria-label={`${target.name} 探针目标编辑`} aria-busy={submitting} inert={submitting ? true : undefined} onSubmit={handleSubmit}>
        <AdminFormSection title="目标信息">
          <div className="admin-form-grid">
            <label>
              <span>目标名</span>
              <input name="target-name" defaultValue={target.name} autoComplete="off" />
            </label>
            <AdminSegmentedField name="target-type" label="类型" value={targetType} onChange={(value) => setTargetType(normalizeTargetFormType(value))} options={targetTypeOptions} />
            <label>
              <span>地址</span>
              <input name="target-address" defaultValue={target.address} autoComplete="off" />
            </label>
            {targetType === 'tcping' && (
              <label>
                <span>端口</span>
                <input name="target-port" type="number" min="1" max="65535" defaultValue={target.port ?? ''} />
              </label>
            )}
          </div>
        </AdminFormSection>
        <AdminFormSection title="探测参数">
          <div className="admin-form-grid">
            <label>
              <span>次数</span>
              <input name="target-count" type="number" min="1" defaultValue={target.count} />
            </label>
            <label>
              <span>超时 ms</span>
              <input name="target-timeout-ms" type="number" min="1" defaultValue={target.timeoutMs} />
            </label>
            <label>
              <span>间隔 s</span>
              <input name="target-interval-sec" type="number" min="1" defaultValue={target.intervalSec} />
            </label>
          </div>
        </AdminFormSection>
        {assignmentRows.length > 0 && (
          <AdminFormSection title="按服务器启用">
            <AdminExpandedCheckList
              title="已启用服务器"
              panelLabel="选择服务器"
              emptyText="暂无服务器"
              options={assignmentRows.map((assignment) => ({ value: assignment.nodeId, label: assignment.nodeDisplayName || assignment.nodeId }))}
              value={assignmentNodeIds}
              onChange={setAssignmentNodeIds}
            />
          </AdminFormSection>
        )}
        <AdminActionFooter error={formError}>
          <button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存目标'}</button>
        </AdminActionFooter>
      </form>
    </AdminModal>
  )
}


export default AdminTargetWorkspace
