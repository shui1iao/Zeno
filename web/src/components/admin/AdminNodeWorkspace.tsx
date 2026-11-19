import { type FormEvent, useState } from 'react'
import type { AdminNodeCreateInput, AdminNodeUpdateInput } from '../../api/adminClient'
import { sortAdminNodes, sortAdminProbeTargets } from '../../lib/adminCollections'
import { runMaybePromise } from '../../lib/maybePromise'
import type { AdminNode, AdminNodeInstallCommand, AdminProbeTarget } from '../../types'
import { ServerFlag } from '../ServerFlag'
import { formatCurrencyAmount, normalizeCurrencyCode } from '../../lib/currency'
import { AdminDateField, AdminExpandedCheckList, AdminSegmentedField } from './AdminFields'
import { AdminInstallCommand } from './AdminInstallCommand'
import { AdminInlineSortList } from './AdminInlineSortList'
import { AdminDeleteConfirmModal, AdminModalPresence, AdminFormSection, AdminModal, AdminActionFooter, AdminRowActions, AdminWorkspaceHeading } from './AdminPrimitives'
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
      {nodes.length > 0 && <AdminNodeList nodes={orderedNodes} onEdit={setEditingNodeId} onDelete={onDelete} onReorder={onReorder} onInstallCommand={onInstallCommand} />}

      <AdminModalPresence>{creatingNode && (
        <AdminNodeCreateModal
          onClose={() => setCreatingNode(false)}
          onCreate={onCreate}
          onInstallCommand={onInstallCommand}
        />
      )}</AdminModalPresence>

      <AdminModalPresence>{editingNode && (
        <AdminNodeEditModal
          key={editingNode.id}
          node={editingNode}
          targets={targets}
          onClose={() => setEditingNodeId(null)}
          onUpdate={onUpdate}
          onInstallCommand={onInstallCommand}
        />
      )}</AdminModalPresence>
    </section>
  )
}

function adminNodeBillingText(node: AdminNode): string {
  const amount = node.renewalAmount
  if (amount === null || amount === undefined || !Number.isFinite(amount) || amount <= 0) return '--'
  const cycle = (node.billingCycle ?? '').trim()
  return `${formatCurrencyAmount(amount, normalizeCurrencyCode(node.renewalCurrency), { spaced: true })}${cycle ? ` / ${cycle}` : ''}`
}

function billingCycleMonths(cycle: string | undefined): number {
  const value = (cycle ?? '').trim()
  if (value === '') return 0
  if (value.includes('五年') || value.includes('5年') || value.includes('5 年')) return 60
  if (value.includes('三年') || value.includes('3年') || value.includes('3 年')) return 36
  if (value.includes('两年') || value.includes('二年') || value.includes('2年') || value.includes('2 年')) return 24
  if (value.includes('半年') || value.includes('半 年')) return 6
  if (value.includes('季')) return 3
  if (value.includes('年')) return 12
  if (value.includes('月')) return 1
  return 0
}

function addMonthsClamped(year: number, month: number, day: number, offsetMonths: number): { year: number; month: number; day: number; ms: number } {
  const totalMonths = year * 12 + (month - 1) + offsetMonths
  const nextYear = Math.floor(totalMonths / 12)
  const nextMonth = totalMonths - nextYear * 12 + 1
  const lastDay = new Date(Date.UTC(nextYear, nextMonth, 0)).getUTCDate()
  const clampedDay = Math.min(day, lastDay)
  return { year: nextYear, month: nextMonth, day: clampedDay, ms: Date.UTC(nextYear, nextMonth - 1, clampedDay) }
}

function adminNodeExpiry(node: AdminNode, now: Date = new Date()): string {
  if (node.expiryPermanent) return '永久'
  const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(node.expiryDate ?? '')
  if (!match) return '未设置到期日'
  const anchorYear = Number(match[1])
  const anchorMonth = Number(match[2])
  const anchorDay = Number(match[3])
  const todayYear = now.getFullYear()
  const todayMonth = now.getMonth() + 1
  const todayDay = now.getDate()
  const today = Date.UTC(todayYear, todayMonth - 1, todayDay)
  const cycleMonths = billingCycleMonths(node.billingCycle)
  let target = { year: anchorYear, month: anchorMonth, day: anchorDay, ms: Date.UTC(anchorYear, anchorMonth - 1, anchorDay) }
  if (cycleMonths > 0) {
    const monthsApart = (todayYear - anchorYear) * 12 + (todayMonth - anchorMonth)
    let offset = monthsApart > 0 ? Math.floor(monthsApart / cycleMonths) * cycleMonths : 0
    target = addMonthsClamped(anchorYear, anchorMonth, anchorDay, offset)
    while (target.ms <= today) {
      offset += cycleMonths
      target = addMonthsClamped(anchorYear, anchorMonth, anchorDay, offset)
    }
    while (true) {
      const prev = addMonthsClamped(anchorYear, anchorMonth, anchorDay, offset - cycleMonths)
      if (prev.ms <= today) break
      offset -= cycleMonths
      target = prev
    }
  }
  const days = Math.ceil((target.ms - today) / 86400000)
  const pad = (n: number) => String(n).padStart(2, '0')
  const date = `${target.year}-${pad(target.month)}-${pad(target.day)}`
  if (days < 0) return `${date} · 已过期`
  return `${date} · 剩余 ${days} 天`
}

function AdminNodeList({ nodes, onEdit, onDelete, onReorder, onInstallCommand }: { nodes: AdminNode[]; onEdit: (nodeId: string) => void; onDelete: (nodeId: string) => MaybePromise; onReorder: (nodeIds: string[]) => MaybePromise; onInstallCommand: (nodeId: string) => Promise<AdminNodeInstallCommand> }) {
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
            <span>账单 · 到期</span>
            <span>Agent 版本</span>
            <span>操作</span>
          </div>
        )}
        renderRow={(node, { dragHandle }) => {
          const liveTone = node.disabled ? 'disabled' : node.status === 'online' ? 'online' : node.status === 'warning' ? 'warning' : 'offline'
          const liveLabel = node.disabled ? '已停用' : node.status === 'online' ? '在线' : node.status === 'warning' ? '异常' : '离线'
          const expiry = adminNodeExpiry(node)
          return (
            <>
              <div className="admin-list-main admin-inline-sort-main">
                {dragHandle}
                <span className={`admin-node-live-dot is-${liveTone}`} title={liveLabel} aria-label={liveLabel} />
                <ServerFlag countryCode={node.countryCode} className="admin-list-flag" />
                <span className="admin-node-name-stack">
                  <strong className="admin-node-title"><span>{node.displayName}</span></strong>
                  <span className={`admin-node-subline is-${liveTone}`}>
                    <span className="admin-node-subline__status">{liveLabel}</span>
                    <span className="admin-node-subline__ip">{node.publicIPv4 || node.publicIPv6 || ''}</span>
                  </span>
                </span>
              </div>
              <span data-label="公网 IP" className={`admin-ip-stack${node.publicIPv6 ? '' : ' is-single'}`}>
                {node.publicIPv4 && <span>{node.publicIPv4}</span>}
                {node.publicIPv6 && <span>{node.publicIPv6}</span>}
                {!node.publicIPv4 && !node.publicIPv6 && <span>—</span>}
              </span>
              <span data-label="账单" className="admin-node-billing">
                <span>{adminNodeBillingText(node)}</span>
                <span className="admin-node-expiry">{expiry}</span>
              </span>
              <span data-label="Agent 版本" className="admin-node-version">{node.agentVersion || '—'}</span>
              <div className="admin-node-row-actions">
                <AdminInstallCommand nodeId={node.id} nodeName={node.displayName} onInstallCommand={onInstallCommand} variant="icon" />
                <AdminRowActions
                  entityLabel="服务器"
                  name={node.displayName}
                  onEdit={() => onEdit(node.id)}
                  onDelete={() => setPendingDelete(node)}
                />
              </div>
            </>
          )
        }}
        onReorder={(nextNodes) => onReorder(nextNodes.map((node) => node.id))}
      />
      <AdminModalPresence>{pendingDelete && (
        <AdminDeleteConfirmModal
          title="删除服务器"
          subjectName={pendingDelete.displayName}
          confirmLabel="删除服务器"
          onClose={() => setPendingDelete(null)}
          onConfirm={() => onDelete(pendingDelete.id)}
        />
      )}</AdminModalPresence>
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
        <div className="admin-modal-scroll">
          <AdminFormSection title="服务器名称">
            <div className="admin-form-grid">
              <label className="admin-label-without-caption">
                <input name="new-display-name" autoComplete="off" aria-label="服务器名称" disabled={Boolean(createdNode)} />
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
        </div>
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
        <div className="admin-modal-scroll">
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
        </div>
        <AdminActionFooter error={formError}>
          <button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存服务器'}</button>
        </AdminActionFooter>
      </form>
    </AdminModal>
  )
}


export default AdminNodeWorkspace
