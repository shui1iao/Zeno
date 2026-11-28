import { useState } from 'react'
import type { AdminProbeTargetInput, AdminProbeTargetUpdateInput } from '../../api/adminClient'
import { sortAdminProbeTargets } from '../../lib/adminCollections'
import type { AdminNode, AdminProbeTarget, ProbeType } from '../../types'
import { AdminExpandedCheckList, AdminSegmentedField } from './AdminFields'
import { AdminInlineSortList } from './AdminInlineSortList'
import { AdminFormModal, AdminFormSection, AdminListHead, AdminListWorkspace, AdminRowActions, useAdminDeleteConfirm, useAdminSubmit } from './AdminPrimitives'
import { formatTargetAssignmentSummary, formatTargetEndpoint, normalizeTargetFormType, parsePositiveInt, targetAssignmentRows, targetTypeOptions } from './adminOperationalModel'
import type { AdminTargetWorkspaceProps, MaybePromise } from './adminOperationalTypes'

export function AdminTargetWorkspace({ targets, nodes, onCreate, onUpdate, onReorder, onDelete }: AdminTargetWorkspaceProps) {
  return (
    <AdminListWorkspace
      className="admin-target-section"
      ariaLabel="admin probe target list"
      title="延迟监控"
      addLabel="添加目标"
      emptyText="还没有目标。"
      items={targets}
      renderList={(onEdit) => <AdminTargetList targets={sortAdminProbeTargets(targets)} onEdit={onEdit} onDelete={onDelete} onReorder={onReorder} />}
      renderCreate={(onClose) => <AdminTargetModal mode="create" nodes={nodes} onClose={onClose} onCreate={onCreate} />}
      renderEdit={(target, onClose) => <AdminTargetModal mode="edit" target={target} nodes={nodes} onClose={onClose} onUpdate={onUpdate} />}
    />
  )
}

function targetTypeLabel(type: ProbeType): string {
  return targetTypeOptions.find((option) => option.value === type)?.label ?? type
}

function AdminTargetList({ targets, onEdit, onDelete, onReorder }: { targets: AdminProbeTarget[]; onEdit: (targetId: string) => void; onDelete: (targetId: string) => MaybePromise; onReorder: (targetIds: string[]) => MaybePromise }) {
  const { requestDelete, deleteModal } = useAdminDeleteConfirm<AdminProbeTarget>({ entityLabel: '目标', getName: (target) => target.name, onConfirm: (target) => onDelete(target.id) })

  return (
    <>
      <AdminInlineSortList
        items={targets}
        className="admin-target-list"
        listLabel="延迟监控目标列表"
        itemLabel="延迟监控"
        getDisplayName={(target) => target.name}
        listHeader={<AdminListHead columns={['目标', '地址', '探测参数', '节点', '操作']} />}
        renderRow={(target, { dragHandle }) => (
          <>
            <div className="admin-list-main admin-inline-sort-main">
              {dragHandle}
              <span className="admin-node-name-stack">
                <strong className="admin-target-title">
                  <span>{target.name}</span>
                  <span className="admin-type-badge">{targetTypeLabel(target.type)}</span>
                </strong>
                <span className="admin-list-mobile-meta">{formatTargetEndpoint(target)}</span>
              </span>
            </div>
            <span data-label="地址" className="admin-target-endpoint">{formatTargetEndpoint(target)}</span>
            <span data-label="探测参数" className="admin-target-params">{target.count} 次 · 超时 {target.timeoutMs} ms · 每 {target.intervalSec} s</span>
            <span data-label="节点">{formatTargetAssignmentSummary(target)}</span>
            <AdminRowActions
              entityLabel="目标"
              name={target.name}
              onEdit={() => onEdit(target.id)}
              onDelete={() => requestDelete(target)}
            />
          </>
        )}
        onReorder={(nextTargets) => onReorder(nextTargets.map((target) => target.id))}
      />
      {deleteModal}
    </>
  )
}

type AdminTargetModalProps =
  | { mode: 'create'; nodes: AdminNode[]; onCreate: (input: AdminProbeTargetInput) => MaybePromise; onClose: () => void }
  | { mode: 'edit'; target: AdminProbeTarget; nodes: AdminNode[]; onUpdate: (targetId: string, input: AdminProbeTargetUpdateInput) => MaybePromise; onClose: () => void }

/** One dialog for adding and editing a latency target; editing pre-fills the
    target and falls back to its stored probe parameters. */
function AdminTargetModal(props: AdminTargetModalProps) {
  const target = props.mode === 'edit' ? props.target : null
  const editing = target !== null
  const [targetType, setTargetType] = useState<ProbeType>(target?.type ?? 'tcping')
  const { submitting, error, submit } = useAdminSubmit()
  const assignmentOptions = target
    ? targetAssignmentRows(target, props.nodes).map((assignment) => ({ nodeId: assignment.nodeId, label: assignment.nodeDisplayName || assignment.nodeId, enabled: assignment.enabled }))
    : props.nodes.map((node) => ({ nodeId: node.id, label: node.displayName || node.id, enabled: false }))
  const [assignmentNodeIds, setAssignmentNodeIds] = useState<string[]>(() => assignmentOptions.filter((assignment) => assignment.enabled).map((assignment) => assignment.nodeId))

  const handleSubmit = (form: HTMLFormElement) => {
    const formData = new FormData(form)
    const field = (name: string) => String(formData.get(name) ?? '')
    const type = normalizeTargetFormType(String(formData.get('target-type') ?? targetType))
    const port = type === 'tcping' ? parsePositiveInt(field('target-port')) : null
    if (type === 'tcping' && port === null) return
    const selected = new Set(assignmentNodeIds)
    const assignments = assignmentOptions.map((assignment) => ({ nodeId: assignment.nodeId, enabled: selected.has(assignment.nodeId) }))
    const probe = {
      count: parsePositiveInt(field('target-count')) ?? target?.count ?? 3,
      timeoutMs: parsePositiveInt(field('target-timeout-ms')) ?? target?.timeoutMs ?? 1000,
      intervalSec: parsePositiveInt(field('target-interval-sec')) ?? target?.intervalSec ?? 30,
    }
    if (props.mode === 'edit') {
      void submit(() => props.onUpdate(props.target.id, {
        name: field('target-name'),
        type,
        address: field('target-address'),
        port,
        ...probe,
        assignments: assignments.length > 0 ? assignments : undefined,
      }), '保存失败', props.onClose)
      return
    }
    const name = field('target-name').trim()
    const address = field('target-address').trim()
    if (name === '' || address === '') return
    void submit(() => props.onCreate({ name, type, address, port, ...probe, assignments }), '添加失败', props.onClose)
  }

  const formLabel = editing ? `编辑目标 ${target.name}` : '添加目标'
  return (
    <AdminFormModal
      title={editing ? '编辑目标' : '添加目标'}
      formLabel={formLabel}
      formClassName={editing ? 'admin-target-edit-form' : 'admin-target-create-form'}
      submitting={submitting}
      error={error}
      onClose={props.onClose}
      onSubmit={handleSubmit}
      footer={<button type="submit" disabled={submitting}>{editing ? (submitting ? '保存中…' : '保存目标') : (submitting ? '添加中…' : '添加目标')}</button>}
    >
      <AdminFormSection title="目标信息">
        <div className="admin-form-grid">
          <label>
            <span>目标名称</span>
            <input name="target-name" defaultValue={target?.name} autoComplete="off" placeholder={editing ? undefined : 'Example HTTPS'} />
          </label>
          <AdminSegmentedField name="target-type" label="类型" value={targetType} onChange={(value) => setTargetType(normalizeTargetFormType(value))} options={targetTypeOptions} />
          <label>
            <span>地址</span>
            <input name="target-address" defaultValue={target?.address} autoComplete="off" placeholder={editing ? undefined : 'example.com'} />
          </label>
          {targetType === 'tcping' && (
            <label>
              <span>端口</span>
              <input name="target-port" type="number" min="1" max="65535" defaultValue={target ? target.port ?? '' : '443'} />
            </label>
          )}
        </div>
      </AdminFormSection>
      <AdminFormSection title="探测参数">
        <div className="admin-form-grid">
          <label>
            <span>次数</span>
            <input name="target-count" type="number" min="1" defaultValue={target?.count ?? 3} />
          </label>
          <label>
            <span>超时 ms</span>
            <input name="target-timeout-ms" type="number" min="1" defaultValue={target?.timeoutMs ?? 1000} />
          </label>
          <label>
            <span>间隔 s</span>
            <input name="target-interval-sec" type="number" min="1" defaultValue={target?.intervalSec ?? 30} />
          </label>
        </div>
      </AdminFormSection>
      {assignmentOptions.length > 0 && (
        <AdminFormSection title="启用服务器">
          <AdminExpandedCheckList
            title="已启用服务器"
            panelLabel="选择服务器"
            emptyText="暂无服务器"
            options={assignmentOptions.map((assignment) => ({ value: assignment.nodeId, label: assignment.label }))}
            value={assignmentNodeIds}
            onChange={setAssignmentNodeIds}
          />
        </AdminFormSection>
      )}
    </AdminFormModal>
  )
}


export default AdminTargetWorkspace
