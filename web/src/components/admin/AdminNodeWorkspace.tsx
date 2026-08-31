import { type FormEvent, useState } from 'react'
import type { AdminNodeCreateInput, AdminNodeUpdateInput } from '../../api/adminClient'
import { sortAdminNodes, sortAdminProbeTargets } from '../../lib/adminCollections'
import { runMaybePromise } from '../../lib/maybePromise'
import type { AdminNode, AdminNodeInstallCommand, AdminProbeTarget } from '../../types'
import { ServerFlag } from '../ServerFlag'
import { AdminDateField, AdminExpandedCheckList, AdminSegmentedField } from './AdminFields'
import { AdminInstallCommand } from './AdminInstallCommand'
import { AdminInlineSortList } from './AdminInlineSortList'
import { AdminDeleteConfirmModal, AdminFormSection, AdminModal, AdminActionFooter, AdminRowActions, AdminWorkspaceHeading } from './AdminPrimitives'
import { billingCycleOptions, billingModeOptions, formatQuotaValue, formatRenewalAmountInput, normalizeBillingCycle, parseMonthlyResetDay, parseQuota, parseRenewalAmount, quotaUnitForBytes, quotaUnitOptions, renewalCurrencyOptions } from './adminOperationalModel'
import type { AdminNodeWorkspaceProps, MaybePromise } from './adminOperationalTypes'

export function AdminNodeWorkspace({ nodes, targets, onCreate, onUpdate, onReorder, onDelete, onInstallCommand }: AdminNodeWorkspaceProps) {
  const [creatingNode, setCreatingNode] = useState(false)
  const [editingNodeId, setEditingNodeId] = useState<string | null>(null)
  const editingNode = editingNodeId ? nodes.find((node) => node.id === editingNodeId) : undefined
  const orderedNodes = sortAdminNodes(nodes)

  return (
    <section className="admin-node-section admin-workspace-panel" aria-label="admin node list">
      <AdminWorkspaceHeading
        title="服务器列表"
        actions={
          <button className="admin-primary-action" type="button" onClick={() => setCreatingNode(true)}>添加服务器</button>
        }
      />

      {nodes.length === 0 && <div className="admin-state-card">还没有节点。</div>}
      {nodes.length > 0 && <AdminNodeList nodes={orderedNodes} onEdit={setEditingNodeId} onDelete={onDelete} onReorder={onReorder} />}

      {creatingNode && (
        <AdminNodeCreateModal
          onClose={() => setCreatingNode(false)}
          onCreate={onCreate}
          onInstallCommand={onInstallCommand}
        />
      )}

      {editingNode && (
        <AdminNodeEditModal
          key={editingNode.id}
          node={editingNode}
          targets={targets}
          onClose={() => setEditingNodeId(null)}
          onUpdate={onUpdate}
          onInstallCommand={onInstallCommand}
        />
      )}
    </section>
  )
}

function AdminNodeList({ nodes, onEdit, onDelete, onReorder }: { nodes: AdminNode[]; onEdit: (nodeId: string) => void; onDelete: (nodeId: string) => MaybePromise; onReorder: (nodeIds: string[]) => MaybePromise }) {
  const [pendingDelete, setPendingDelete] = useState<AdminNode | null>(null)

  return (
    <>
      <AdminInlineSortList
        items={nodes}
        listLabel="服务器列表"
        itemLabel="服务器"
        getDisplayName={(node) => node.displayName}
        listHeader={(
          <div className="admin-list-head" aria-hidden="true">
            <span>服务器</span>
            <span>公网 IP</span>
            <span>Agent 版本</span>
            <span>操作</span>
          </div>
        )}
        renderRow={(node, { dragHandle }) => (
          <>
            <div className="admin-list-main admin-inline-sort-main">
              {dragHandle}
              <strong className="admin-node-title"><ServerFlag countryCode={node.countryCode} className="admin-list-flag" /><span>{node.displayName}</span></strong>
            </div>
            <span data-label="公网 IP" className={`admin-ip-stack${node.publicIPv6 ? '' : ' is-single'}`}>
              {node.publicIPv4 && <span>{node.publicIPv4}</span>}
              {node.publicIPv6 && <span>{node.publicIPv6}</span>}
              {!node.publicIPv4 && !node.publicIPv6 && <span>—</span>}
            </span>
            <span data-label="Agent 版本">{node.agentVersion || '—'}</span>
            <AdminRowActions
              entityLabel="服务器"
              name={node.displayName}
              onEdit={() => onEdit(node.id)}
              onDelete={() => setPendingDelete(node)}
            />
          </>
        )}
        onReorder={(nextNodes) => onReorder(nextNodes.map((node) => node.id))}
      />
      {pendingDelete && (
        <AdminDeleteConfirmModal
          title="删除服务器"
          subjectName={pendingDelete.displayName}
          confirmLabel="删除服务器"
          onClose={() => setPendingDelete(null)}
          onConfirm={() => onDelete(pendingDelete.id)}
        />
      )}
    </>
  )
}

function AdminNodeCreateModal({ onCreate, onInstallCommand, onClose }: { onCreate: (input: AdminNodeCreateInput) => Promise<AdminNode | void>; onInstallCommand: (nodeId: string) => Promise<AdminNodeInstallCommand>; onClose: () => void }) {
  const [createdNode, setCreatedNode] = useState<AdminNode | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)

  const nodeInputFromForm = (form: HTMLFormElement): AdminNodeCreateInput | null => {
    const formData = new FormData(form)
    const displayName = String(formData.get('new-display-name') ?? '').trim()
    if (displayName === '') return null
    return {
      displayName,
      expiryDate: String(formData.get('new-expiry-date') ?? '').trim(),
      expiryPermanent: formData.get('new-expiry-permanent') === '1',
      billingCycle: String(formData.get('new-billing-cycle') ?? '').trim(),
      renewalAmount: parseRenewalAmount(String(formData.get('new-renewal-amount') ?? '')),
      renewalCurrency: String(formData.get('new-renewal-currency') ?? 'CNY'),
      billingMode: String(formData.get('new-billing-mode') ?? 'both'),
      monthlyResetDay: parseMonthlyResetDay(String(formData.get('new-monthly-reset-day') ?? '')) ?? 1,
      monthlyQuotaBytes: parseQuota(String(formData.get('new-monthly-quota') ?? ''), String(formData.get('new-monthly-quota-unit') ?? 'GB')),
    }
  }

  const createNodeFromForm = (form: HTMLFormElement): Promise<AdminNode | null> => {
    if (submitting) return Promise.resolve(null)
    const input = nodeInputFromForm(form)
    if (!input) {
      setFormError('请先填写服务器名称。')
      return Promise.resolve(null)
    }
    setSubmitting(true)
    setFormError(null)
    return onCreate(input)
      .then((node) => {
        if (node) setCreatedNode(node)
        return node ?? null
      })
      .catch((error: unknown) => {
        setFormError(error instanceof Error ? error.message : '添加服务器失败')
        return null
      })
      .finally(() => setSubmitting(false))
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    createNodeFromForm(event.currentTarget)
  }

  return (
    <AdminModal title="添加服务器" closeDisabled={submitting} onClose={onClose}>
      <form className="admin-node-create-form admin-node-edit-form is-sectioned" aria-label="添加服务器" aria-busy={submitting} inert={submitting ? true : undefined} onSubmit={handleSubmit}>
        <AdminFormSection title="服务器名称">
          <div className="admin-form-grid">
            <label>
              <span>服务器名称</span>
              <input name="new-display-name" autoComplete="off" placeholder="New Server" disabled={Boolean(createdNode)} />
            </label>
          </div>
        </AdminFormSection>
        <AdminFormSection title="账单与流量">
          <div className="admin-billing-grid">
            <div className="admin-billing-row admin-billing-row--cycle">
              <AdminDateField className="admin-billing-control admin-billing-control--expiry" name="new-expiry-date" label="到期日" permanentLabel="设为永久" disabled={Boolean(createdNode)} />
              <label className="admin-billing-control admin-billing-control--amount">
                <span>续费金额</span>
                <input name="new-renewal-amount" type="number" min="0" max="1000000000" step="0.01" inputMode="decimal" disabled={Boolean(createdNode)} />
              </label>
              <AdminSegmentedField className="admin-billing-control admin-billing-control--currency" name="new-renewal-currency" label="币种" defaultValue="CNY" options={renewalCurrencyOptions} disabled={Boolean(createdNode)} />
              <AdminSegmentedField className="admin-billing-control admin-billing-control--cycle" name="new-billing-cycle" label="账单周期" defaultValue="月" options={billingCycleOptions} disabled={Boolean(createdNode)} />
            </div>
            <div className="admin-billing-row admin-billing-row--traffic">
              <label className="admin-billing-control admin-billing-control--reset">
                <span>月流量重置日</span>
                <input name="new-monthly-reset-day" type="number" min="1" max="31" step="1" defaultValue="1" disabled={Boolean(createdNode)} />
              </label>
              <AdminSegmentedField className="admin-billing-control admin-billing-control--mode" name="new-billing-mode" label="流量计费口径" defaultValue="both" options={billingModeOptions} disabled={Boolean(createdNode)} />
              <label className="admin-billing-control admin-billing-control--quota">
                <span>月配额</span>
                <input name="new-monthly-quota" type="number" min="0" step="0.01" disabled={Boolean(createdNode)} />
              </label>
              <AdminSegmentedField className="admin-billing-control admin-billing-control--unit" name="new-monthly-quota-unit" label="配额单位" defaultValue="GB" options={quotaUnitOptions} disabled={Boolean(createdNode)} />
            </div>
          </div>
        </AdminFormSection>
        {createdNode && (
          <AdminInstallCommand
            nodeId={createdNode.id}
            initialMessage={<>已添加：{createdNode.displayName}</>}
            blocked={submitting}
            onInstallCommand={onInstallCommand}
          />
        )}
        <AdminActionFooter error={formError}>
          <button type="submit" disabled={submitting || Boolean(createdNode)}>{submitting ? '添加中…' : createdNode ? '服务器已添加' : '添加服务器'}</button>
        </AdminActionFooter>
      </form>
    </AdminModal>
  )
}

function AdminNodeEditModal({ node, targets, onUpdate, onInstallCommand, onClose }: { node: AdminNode; targets: AdminProbeTarget[]; onUpdate: (nodeId: string, input: AdminNodeUpdateInput) => MaybePromise; onInstallCommand: (nodeId: string) => Promise<AdminNodeInstallCommand>; onClose: () => void }) {
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
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

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    const formData = new FormData(event.currentTarget)
    const displayName = String(formData.get('display-name') ?? '').trim()
    const selectedTargets = new Set(selectedTargetIds)
    setSubmitting(true)
    setFormError(null)
    runMaybePromise(() => onUpdate(node.id, {
      displayName: displayName || node.displayName,
      homeProbeTargetId: selectedTargets.has(homeTargetId) ? homeTargetId : '',
      expiryDate: String(formData.get('expiry-date') ?? '').trim(),
      expiryPermanent: formData.get('expiry-permanent') === '1',
      billingCycle: String(formData.get('billing-cycle') ?? '').trim(),
      renewalAmount: parseRenewalAmount(String(formData.get('renewal-amount') ?? '')),
      renewalCurrency: String(formData.get('renewal-currency') ?? node.renewalCurrency ?? 'CNY'),
      billingMode: String(formData.get('billing-mode') ?? node.billingMode),
      monthlyResetDay: parseMonthlyResetDay(String(formData.get('monthly-reset-day') ?? '')) ?? node.monthlyResetDay,
      monthlyQuotaBytes: parseQuota(String(formData.get('monthly-quota') ?? ''), String(formData.get('monthly-quota-unit') ?? quotaUnitForBytes(node.monthlyQuotaBytes))),
      probeTargetIds: [...selectedTargets],
    }))
      .then(() => onClose())
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '保存失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <AdminModal title="编辑服务器" closeDisabled={submitting} onClose={onClose}>
      <form className="admin-node-edit-form is-sectioned" aria-label={`${node.displayName} 节点编辑`} aria-busy={submitting} inert={submitting ? true : undefined} onSubmit={handleSubmit}>
        <AdminFormSection title="服务器名称">
          <div className="admin-form-grid">
            <label className="admin-label-without-caption">
              <input name="display-name" defaultValue={node.displayName} autoComplete="off" aria-label="服务器名称" />
            </label>
          </div>
        </AdminFormSection>
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
        <AdminFormSection title="账单与流量">
          <div className="admin-billing-grid">
            <div className="admin-billing-row admin-billing-row--cycle">
              <AdminDateField className="admin-billing-control admin-billing-control--expiry" name="expiry-date" label="到期日" defaultValue={node.expiryDate ?? ''} defaultPermanent={node.expiryPermanent} permanentLabel="设为永久" />
              <label className="admin-billing-control admin-billing-control--amount">
                <span>续费金额</span>
                <input name="renewal-amount" type="number" min="0" max="1000000000" step="0.01" inputMode="decimal" defaultValue={formatRenewalAmountInput(node.renewalAmount)} />
              </label>
              <AdminSegmentedField className="admin-billing-control admin-billing-control--currency" name="renewal-currency" label="币种" defaultValue={node.renewalCurrency || 'CNY'} options={renewalCurrencyOptions} />
              <AdminSegmentedField className="admin-billing-control admin-billing-control--cycle" name="billing-cycle" label="账单周期" defaultValue={normalizeBillingCycle(node.billingCycle)} options={billingCycleOptions} />
            </div>
            <div className="admin-billing-row admin-billing-row--traffic">
              <label className="admin-billing-control admin-billing-control--reset">
                <span>月流量重置日</span>
                <input name="monthly-reset-day" type="number" min="1" max="31" step="1" defaultValue={node.monthlyResetDay || 1} />
              </label>
              <AdminSegmentedField className="admin-billing-control admin-billing-control--mode" name="billing-mode" label="流量计费口径" defaultValue={node.billingMode || 'both'} options={billingModeOptions} />
              <label className="admin-billing-control admin-billing-control--quota">
                <span>月配额</span>
                <input name="monthly-quota" type="number" min="0" step="0.01" defaultValue={formatQuotaValue(node.monthlyQuotaBytes)} />
              </label>
              <AdminSegmentedField className="admin-billing-control admin-billing-control--unit" name="monthly-quota-unit" label="配额单位" defaultValue={quotaUnitForBytes(node.monthlyQuotaBytes)} options={quotaUnitOptions} />
            </div>
          </div>
        </AdminFormSection>
        <AdminInstallCommand nodeId={node.id} onInstallCommand={onInstallCommand} />
        <AdminActionFooter error={formError}>
          <button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存服务器'}</button>
        </AdminActionFooter>
      </form>
    </AdminModal>
  )
}


export default AdminNodeWorkspace
