import { useState } from 'react'
import type { AdminNodeCreateInput, AdminNodeUpdateInput } from '../../api/adminClient'
import { sortAdminNodes, sortAdminProbeTargets } from '../../lib/adminCollections'
import { nodeStatusLabels, type NodeVisualStatus } from '../../lib/nodeHealth'
import type { AdminNode, AdminNodeInstallCommand, AdminProbeTarget } from '../../types'
import { ServerFlag } from '../ServerFlag'
import { AdminDateField, AdminExpandedCheckList, AdminSegmentedField } from './AdminFields'
import { AdminInstallCommand } from './AdminInstallCommand'
import { AdminInlineSortList } from './AdminInlineSortList'
import { AdminFormModal, AdminFormSection, AdminListHead, AdminListWorkspace, AdminRowActions, useAdminDeleteConfirm, useAdminSubmit } from './AdminPrimitives'
import { billingCycleOptions, billingModeOptions, formatQuotaValue, formatRenewalAmountInput, normalizeBillingCycle, parseMonthlyResetDay, parseQuota, parseRenewalAmount, quotaUnitForBytes, quotaUnitOptions, renewalCurrencyOptions } from './adminOperationalModel'
import type { AdminNodeWorkspaceProps, MaybePromise } from './adminOperationalTypes'

export function AdminNodeWorkspace({ nodes, targets, onCreate, onUpdate, onReorder, onDelete, onInstallCommand }: AdminNodeWorkspaceProps) {
  return (
    <AdminListWorkspace
      className="admin-node-section"
      ariaLabel="admin node list"
      title="服务器列表"
      addLabel="添加服务器"
      emptyText="还没有节点。"
      items={nodes}
      renderList={(onEdit) => <AdminNodeList nodes={sortAdminNodes(nodes)} onEdit={onEdit} onDelete={onDelete} onReorder={onReorder} onInstallCommand={onInstallCommand} />}
      renderCreate={(onClose) => <AdminNodeCreateModal onClose={onClose} onCreate={onCreate} onInstallCommand={onInstallCommand} />}
      renderEdit={(node, onClose) => <AdminNodeEditModal node={node} targets={targets} onClose={onClose} onUpdate={onUpdate} onInstallCommand={onInstallCommand} />}
    />
  )
}

/** Admin dot: disabled first; anything not reported online or warning reads as offline. */
function nodeLiveState(node: AdminNode): { tone: string; label: string } {
  if (node.disabled) return { tone: 'disabled', label: '已停用' }
  const tone: NodeVisualStatus = node.status === 'online' ? 'online' : node.status === 'warning' ? 'warning' : 'offline'
  return { tone, label: nodeStatusLabels[tone] }
}

function AdminNodeList({ nodes, onEdit, onDelete, onReorder, onInstallCommand }: { nodes: AdminNode[]; onEdit: (nodeId: string) => void; onDelete: (nodeId: string) => MaybePromise; onReorder: (nodeIds: string[]) => MaybePromise; onInstallCommand: (nodeId: string) => Promise<AdminNodeInstallCommand> }) {
  const { requestDelete, deleteModal } = useAdminDeleteConfirm<AdminNode>({ entityLabel: '服务器', getName: (node) => node.displayName, onConfirm: (node) => onDelete(node.id) })

  return (
    <>
      <AdminInlineSortList
        items={nodes}
        listLabel="服务器列表"
        itemLabel="服务器"
        getDisplayName={(node) => node.displayName}
        listHeader={<AdminListHead columns={['服务器', '公网 IP', 'Agent 版本', '操作']} />}
        renderRow={(node, { dragHandle }) => {
          const live = nodeLiveState(node)
          const primaryIP = node.publicIPv4 || node.publicIPv6
          return (
            <>
              <div className="admin-list-main admin-inline-sort-main">
                {dragHandle}
                <span className={`admin-node-live-dot is-${live.tone}`} title={live.label} aria-label={live.label} />
                <ServerFlag countryCode={node.countryCode} className="admin-list-flag" />
                <span className="admin-node-name-stack">
                  <strong className="admin-node-title"><span>{node.displayName}</span></strong>
                  {primaryIP && <span className="admin-list-mobile-meta">{primaryIP}</span>}
                </span>
              </div>
              <span data-label="公网 IP" className={`admin-ip-stack${node.publicIPv6 ? '' : ' is-single'}`}>
                {node.publicIPv4 && <span>{node.publicIPv4}</span>}
                {node.publicIPv6 && <span>{node.publicIPv6}</span>}
                {!primaryIP && <span>—</span>}
              </span>
              <span data-label="Agent 版本" className="admin-node-version">{node.agentVersion || '—'}</span>
              <div className="admin-node-row-actions">
                <AdminInstallCommand nodeId={node.id} nodeName={node.displayName} onInstallCommand={onInstallCommand} variant="icon" />
                <AdminRowActions
                  entityLabel="服务器"
                  name={node.displayName}
                  onEdit={() => onEdit(node.id)}
                  onDelete={() => requestDelete(node)}
                />
              </div>
            </>
          )
        }}
        onReorder={(nextNodes) => onReorder(nextNodes.map((node) => node.id))}
      />
      {deleteModal}
    </>
  )
}

type AdminNodeBillingInput = Pick<AdminNodeCreateInput, 'expiryDate' | 'expiryPermanent' | 'billingCycle' | 'renewalAmount' | 'renewalCurrency' | 'billingMode' | 'monthlyResetDay' | 'monthlyQuotaBytes'>

/** Reads the shared 账单与流量 fields. Fallbacks come from the node being
    edited, or the create-form defaults when adding a server. */
function nodeBillingInputFromForm(formData: FormData, node?: AdminNode): AdminNodeBillingInput {
  return {
    expiryDate: String(formData.get('expiry-date') ?? '').trim(),
    expiryPermanent: formData.get('expiry-permanent') === '1',
    billingCycle: String(formData.get('billing-cycle') ?? '').trim(),
    renewalAmount: parseRenewalAmount(String(formData.get('renewal-amount') ?? '')),
    renewalCurrency: String(formData.get('renewal-currency') ?? (node ? node.renewalCurrency ?? 'CNY' : 'CNY')),
    billingMode: String(formData.get('billing-mode') ?? (node ? node.billingMode : 'both')),
    monthlyResetDay: parseMonthlyResetDay(String(formData.get('monthly-reset-day') ?? '')) ?? (node ? node.monthlyResetDay : 1),
    monthlyQuotaBytes: parseQuota(String(formData.get('monthly-quota') ?? ''), String(formData.get('monthly-quota-unit') ?? (node ? quotaUnitForBytes(node.monthlyQuotaBytes) : 'GB'))),
  }
}

function AdminNodeNameSection({ node, disabled = false }: { node?: AdminNode; disabled?: boolean }) {
  return (
    <AdminFormSection title="服务器名称">
      <div className="admin-form-grid">
        <label className="admin-label-without-caption">
          <input name="display-name" defaultValue={node?.displayName} autoComplete="off" aria-label="服务器名称" disabled={disabled} />
        </label>
      </div>
    </AdminFormSection>
  )
}

function AdminNodeBillingSection({ node, disabled = false }: { node?: AdminNode; disabled?: boolean }) {
  return (
    <AdminFormSection title="账单与流量">
      <div className="admin-billing-grid">
        <div className="admin-billing-row admin-billing-row--cycle">
          <AdminDateField className="admin-billing-control admin-billing-control--expiry" name="expiry-date" label="到期日" defaultValue={node?.expiryDate ?? ''} defaultPermanent={node?.expiryPermanent} permanentLabel="设为永久" disabled={disabled} />
          <label className="admin-billing-control admin-billing-control--amount">
            <span>续费金额</span>
            <input name="renewal-amount" type="number" min="0" max="1000000000" step="0.01" inputMode="decimal" defaultValue={node ? formatRenewalAmountInput(node.renewalAmount) : undefined} disabled={disabled} />
          </label>
          <AdminSegmentedField className="admin-billing-control admin-billing-control--currency" name="renewal-currency" label="币种" defaultValue={node?.renewalCurrency || 'CNY'} options={renewalCurrencyOptions} disabled={disabled} />
          <AdminSegmentedField className="admin-billing-control admin-billing-control--cycle" name="billing-cycle" label="账单周期" defaultValue={node ? normalizeBillingCycle(node.billingCycle) : '月'} options={billingCycleOptions} disabled={disabled} />
        </div>
        <div className="admin-billing-row admin-billing-row--traffic">
          <label className="admin-billing-control admin-billing-control--reset">
            <span>月流量重置日</span>
            <input name="monthly-reset-day" type="number" min="1" max="31" step="1" defaultValue={node?.monthlyResetDay || 1} disabled={disabled} />
          </label>
          <AdminSegmentedField className="admin-billing-control admin-billing-control--mode" name="billing-mode" label="流量计费口径" defaultValue={node?.billingMode || 'both'} options={billingModeOptions} disabled={disabled} />
          <label className="admin-billing-control admin-billing-control--quota">
            <span>月配额</span>
            <input name="monthly-quota" type="number" min="0" step="0.01" defaultValue={node ? formatQuotaValue(node.monthlyQuotaBytes) : undefined} disabled={disabled} />
          </label>
          <AdminSegmentedField className="admin-billing-control admin-billing-control--unit" name="monthly-quota-unit" label="配额单位" defaultValue={node ? quotaUnitForBytes(node.monthlyQuotaBytes) : 'GB'} options={quotaUnitOptions} disabled={disabled} />
        </div>
      </div>
    </AdminFormSection>
  )
}

function AdminNodeCreateModal({ onCreate, onInstallCommand, onClose }: { onCreate: (input: AdminNodeCreateInput) => Promise<AdminNode | void>; onInstallCommand: (nodeId: string) => Promise<AdminNodeInstallCommand>; onClose: () => void }) {
  const [createdNode, setCreatedNode] = useState<AdminNode | null>(null)
  const { submitting, error, setError, submit } = useAdminSubmit()
  const created = Boolean(createdNode)

  const handleSubmit = (form: HTMLFormElement) => {
    const formData = new FormData(form)
    const displayName = String(formData.get('display-name') ?? '').trim()
    if (displayName === '') {
      setError('请先填写服务器名称。')
      return
    }
    void submit(() => onCreate({ displayName, ...nodeBillingInputFromForm(formData) }), '添加服务器失败', (node) => {
      if (node) setCreatedNode(node)
    })
  }

  return (
    <AdminFormModal
      title="添加服务器"
      formLabel="添加服务器"
      formClassName="admin-node-create-form"
      submitting={submitting}
      error={error}
      onClose={onClose}
      onSubmit={handleSubmit}
      footer={<button type="submit" disabled={submitting || created}>{submitting ? '添加中…' : created ? '服务器已添加' : '添加服务器'}</button>}
    >
      <AdminNodeNameSection disabled={created} />
      <AdminNodeBillingSection disabled={created} />
      {createdNode && (
        <AdminInstallCommand
          nodeId={createdNode.id}
          initialMessage={<>已添加：{createdNode.displayName}</>}
          blocked={submitting}
          onInstallCommand={onInstallCommand}
        />
      )}
    </AdminFormModal>
  )
}

function AdminNodeEditModal({ node, targets, onUpdate, onInstallCommand, onClose }: { node: AdminNode; targets: AdminProbeTarget[]; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => MaybePromise; onInstallCommand: (nodeId: string) => Promise<AdminNodeInstallCommand>; onClose: () => void }) {
  const { submitting, error, submit } = useAdminSubmit()
  const sortedTargets = sortAdminProbeTargets(targets)
  const initialSelectedTargetIds = sortedTargets.filter((target) => target.assignments.some((assignment) => assignment.nodeId === node.id && assignment.enabled)).map((target) => target.id)
  const [selectedTargetIds, setSelectedTargetIds] = useState<string[]>(initialSelectedTargetIds)
  const [homeTargetId, setHomeTargetId] = useState<string>(node.homeProbeTargetId && initialSelectedTargetIds.includes(node.homeProbeTargetId) ? node.homeProbeTargetId : '')

  const updateSelectedTargetIds = (nextTargetIds: string[]) => {
    setSelectedTargetIds(nextTargetIds)
    if (homeTargetId !== '' && !nextTargetIds.includes(homeTargetId)) {
      setHomeTargetId('')
    }
  }

  const handleSubmit = (form: HTMLFormElement) => {
    const formData = new FormData(form)
    const displayName = String(formData.get('display-name') ?? '').trim()
    const selectedTargets = new Set(selectedTargetIds)
    void submit(() => onUpdate(node.id, {
      displayName: displayName || node.displayName,
      homeProbeTargetId: selectedTargets.has(homeTargetId) ? homeTargetId : '',
      ...nodeBillingInputFromForm(formData, node),
      probeTargetIds: [...selectedTargets],
    }), '保存失败', onClose)
  }

  return (
    <AdminFormModal
      title="编辑服务器"
      formLabel={`${node.displayName} 节点编辑`}
      submitting={submitting}
      error={error}
      onClose={onClose}
      onSubmit={handleSubmit}
      footer={<button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存服务器'}</button>}
    >
      <AdminNodeNameSection node={node} />
      <AdminFormSection title="关联延迟监控">
        {sortedTargets.length === 0 ? (
          <div className="admin-state-card is-compact">暂无延迟监控。</div>
        ) : (
          <AdminExpandedCheckList
            title="已选延迟监控"
            panelLabel="选择监控服务"
            emptyText="暂无延迟监控"
            options={sortedTargets.map((target) => ({ value: target.id, label: target.name }))}
            value={selectedTargetIds}
            onChange={updateSelectedTargetIds}
            renderRight={(option) => (
              <label className="admin-home-monitor-radio">
                <input
                  type="radio"
                  name={`home-monitor-${node.id}`}
                  aria-label={`首页展示 ${option.label}`}
                  checked={homeTargetId === option.value}
                  onChange={() => setHomeTargetId(option.value)}
                />
                <span>首页展示</span>
              </label>
            )}
          />
        )}
      </AdminFormSection>
      <AdminNodeBillingSection node={node} />
      <AdminInstallCommand nodeId={node.id} onInstallCommand={onInstallCommand} />
    </AdminFormModal>
  )
}


export default AdminNodeWorkspace
