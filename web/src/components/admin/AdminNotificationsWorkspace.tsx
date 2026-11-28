import { useState } from 'react'
import type { AdminAlertRuleUpdateInput, AdminNotificationChannelCreateInput, AdminNotificationChannelUpdateInput } from '../../api/adminClient'
import { runMaybePromise } from '../../lib/maybePromise'
import type { AdminAlertRule, AdminNode, AdminNotificationChannel } from '../../types'
import { AdminExpandedCheckList } from './AdminFields'
import { AdminCredentialField, AdminModalPresence, AdminFormModal, AdminFormSection, AdminListHead, AdminModal, AdminRowActions, AdminWorkspaceHeading, adminErrorText, useAdminDeleteConfirm, useAdminSubmit } from './AdminPrimitives'
import { formatAlertRuleNote, formatAlertRuleScope, formatRenewalDayOption, normalizeRenewalDays, parseNonNegativeInt, parsePercentage, renewalDayOptions } from './adminOperationalModel'
import type { AdminNotificationsWorkspaceProps, MaybePromise } from './adminOperationalTypes'

function AdminAlertRulesSection({ rules, nodes, onUpdate }: { rules: AdminAlertRule[]; nodes: AdminNode[]; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => MaybePromise }) {
  const [editingRule, setEditingRule] = useState<AdminAlertRule | null>(null)
  const [addingRule, setAddingRule] = useState(false)
  const addedRules = rules.filter((rule) => rule.enabled)
  const availableRules = rules.filter((rule) => !rule.enabled)

  return (
    <section className="admin-notification-group admin-alert-rule-section" aria-label="通知类型规则">
      <div className="admin-block-heading">
        <h4>通知类型</h4>
        <button className="admin-primary-action" type="button" onClick={() => setAddingRule(true)}>添加通知类型</button>
      </div>
      {addedRules.length === 0 && <div className="admin-state-card">还没有添加通知类型。</div>}
      {addedRules.length > 0 && <AdminAlertRuleList rules={addedRules} onEdit={setEditingRule} onUpdate={onUpdate} />}

      <AdminModalPresence>{addingRule && (
        <AdminAlertRuleAddModal
          rules={availableRules}
          nodes={nodes}
          onClose={() => setAddingRule(false)}
          onAdd={onUpdate}
        />
      )}</AdminModalPresence>

      <AdminModalPresence>{editingRule && (
        <AdminAlertRuleEditModal
          rule={editingRule}
          nodes={nodes}
          onClose={() => setEditingRule(null)}
          onUpdate={onUpdate}
        />
      )}</AdminModalPresence>
    </section>
  )
}

function AdminAlertRuleList({ rules, onEdit, onUpdate }: { rules: AdminAlertRule[]; onEdit: (rule: AdminAlertRule) => void; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => MaybePromise }) {
  const { requestDelete, deleteModal } = useAdminDeleteConfirm<AdminAlertRule>({ entityLabel: '通知类型', getName: (rule) => rule.name, onConfirm: (rule) => onUpdate(rule.id, { enabled: false }), hint: '删除后可重新添加。' })

  return (
    <>
      <div className="admin-list admin-alert-rule-list" role="list" aria-label="通知类型列表">
        <AdminListHead columns={['通知类型', '状态', '操作']} />
        {rules.map((rule) => (
          <div className="admin-list-row" role="listitem" key={rule.id}>
            <div className="admin-list-main">
              <strong>{rule.name}</strong>
              {formatAlertRuleNote(rule) && <small>{formatAlertRuleNote(rule)}</small>}
            </div>
            <AdminStatusBadge label={rule.enabled ? '启用中' : '已停用'} status={rule.enabled ? 'online' : 'disabled'} dataLabel="状态" />
            <AdminRowActions
              entityLabel="通知类型"
              name={rule.name}
              onEdit={() => onEdit(rule)}
              onDelete={() => requestDelete(rule)}
            />
          </div>
        ))}
      </div>
      {deleteModal}
    </>
  )
}

function AdminAlertRuleAddModal({ rules, nodes, onAdd, onClose }: { rules: AdminAlertRule[]; nodes: AdminNode[]; onAdd: (ruleId: string, input: AdminAlertRuleUpdateInput) => MaybePromise; onClose: () => void }) {
  const [submittingRuleId, setSubmittingRuleId] = useState<string | null>(null)
  const [formError, setFormError] = useState<string | null>(null)
  const addRule = (ruleId: string) => {
    if (submittingRuleId !== null) return
    setSubmittingRuleId(ruleId)
    setFormError(null)
    runMaybePromise(() => onAdd(ruleId, { enabled: true }))
      .then(() => onClose())
      .catch((error: unknown) => setFormError(adminErrorText(error, '添加失败')))
      .finally(() => setSubmittingRuleId(null))
  }
  return (
    <AdminModal title="添加通知类型" closeDisabled={submittingRuleId !== null} onClose={onClose}>
      <div className="admin-alert-rule-add-form admin-node-edit-form is-sectioned" aria-label="添加通知类型" aria-busy={submittingRuleId !== null} inert={submittingRuleId !== null ? true : undefined}>
        <AdminFormSection title="通知类型">
          <div className="admin-rule-picker" role="list" aria-label="可添加通知类型">
            {rules.length === 0 && <div className="admin-state-card">所有通知类型都已添加。</div>}
            {rules.map((rule) => (
              <div className="admin-rule-picker-row" role="listitem" key={rule.id}>
                <div className="admin-list-main">
                  <strong>{rule.name}</strong>
                  <small>{formatAlertRuleNote(rule) || formatAlertRuleScope(rule, nodes)}</small>
                </div>
                <button className="admin-primary-action" type="button" onClick={() => addRule(rule.id)} disabled={submittingRuleId !== null}>{submittingRuleId === rule.id ? '添加中…' : '添加'}</button>
              </div>
            ))}
          </div>
        </AdminFormSection>
        {formError && <div className="admin-install-error">{formError}</div>}
      </div>
    </AdminModal>
  )
}

function AdminAlertRuleEditModal({ rule, nodes, onUpdate, onClose }: { rule: AdminAlertRule; nodes: AdminNode[]; onUpdate: (ruleId: string, input: AdminAlertRuleUpdateInput) => MaybePromise; onClose: () => void }) {
  const { submitting, error: formError, submit } = useAdminSubmit()
  const initialScopeNodeIds = rule.scopeNodeIds.length === 0 ? nodes.map((node) => node.id) : rule.scopeNodeIds
  const [scopeNodeIds, setScopeNodeIds] = useState(initialScopeNodeIds)
  const isRenewalRule = rule.metric === 'expiry_days'
  const [renewalDays, setRenewalDays] = useState(() => normalizeRenewalDays(rule.renewalDays, rule.threshold))
  const isResourceRule = rule.category === 'resource' && rule.thresholdUnit === '%'
  const supportsDuration = !isRenewalRule
  const handleSubmit = (form: HTMLFormElement) => {
    const formData = new FormData(form)
    const resourceThreshold = isResourceRule ? parsePercentage(String(formData.get('rule-threshold-percent') ?? '')) : null
    const durationSec = supportsDuration ? parseNonNegativeInt(String(formData.get('rule-duration-sec') ?? '')) : null
    const persistedScopeNodeIds = scopeNodeIds.length === nodes.length ? [] : scopeNodeIds
    void submit(() => onUpdate(rule.id, {
      enabled: formData.get('rule-enabled') === 'on',
      ...(isRenewalRule ? { renewalDays } : {}),
      ...(isResourceRule && resourceThreshold !== null ? { threshold: resourceThreshold } : {}),
      ...(supportsDuration && durationSec !== null ? { durationSec } : {}),
      scopeNodeIds: persistedScopeNodeIds,
    }), '保存失败', onClose)
  }

  return (
    <AdminFormModal
      title="编辑通知类型"
      modalClassName="admin-alert-rule-modal"
      formClassName="admin-alert-rule-edit-form"
      formLabel={`${rule.name} 通知类型编辑`}
      submitting={submitting}
      error={formError}
      onClose={onClose}
      onSubmit={handleSubmit}
      footer={<button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存通知类型'}</button>}
    >
          <AdminFormSection title="通知设置">
            <div className="admin-form-grid admin-alert-rule-settings-grid">
              {isRenewalRule && (
                <AdminExpandedCheckList
                  title="已选提醒时间"
                  panelLabel="选择提醒时间"
                  options={renewalDayOptions.map((days) => ({ value: String(days), label: formatRenewalDayOption(days) }))}
                  value={renewalDays.map(String)}
                  onChange={(values) => setRenewalDays(values.map(Number))}
                  allowEmpty={false}
                />
              )}
              {supportsDuration && (
                <label>
                  <span>{isResourceRule ? '统计窗口 s' : '确认时间 s'}</span>
                  <input name="rule-duration-sec" type="number" min="0" defaultValue={rule.durationSec} />
                </label>
              )}
              {isResourceRule && (
                <label>
                  <span>平均超过 %</span>
                  <input name="rule-threshold-percent" type="number" min="0" max="100" step="0.1" defaultValue={rule.threshold} />
                </label>
              )}
            </div>
          </AdminFormSection>
          <AdminFormSection title="通知状态">
            <label className="admin-node-toggle admin-alert-rule-enabled-toggle">
              <input name="rule-enabled" type="checkbox" defaultChecked={rule.enabled} />
              <span>启用通知类型</span>
            </label>
          </AdminFormSection>
          {nodes.length > 0 && (
            <AdminFormSection title="作用服务器">
              <AdminExpandedCheckList
                title="已选服务器"
                panelLabel="选择服务器"
                options={nodes.map((node) => ({ value: node.id, label: node.displayName || node.id }))}
                value={scopeNodeIds}
                onChange={setScopeNodeIds}
                allowEmpty={false}
              />
            </AdminFormSection>
          )}
    </AdminFormModal>
  )
}

export function AdminNotificationsWorkspace({ channels, rules, nodes, onChannelCreate, onChannelUpdate, onChannelDelete, onChannelTest, onRuleUpdate }: AdminNotificationsWorkspaceProps) {
  const [creatingChannel, setCreatingChannel] = useState(false)
  const [editingChannel, setEditingChannel] = useState<AdminNotificationChannel | null>(null)

  return (
    <section className="admin-notification-section admin-workspace-panel admin-workspace-panel--grouped" aria-label="admin notification settings">
      <AdminWorkspaceHeading
        title="通知渠道"
        actions={<button className="admin-primary-action" type="button" onClick={() => setCreatingChannel(true)}>添加通知渠道</button>}
      />
      <div className="admin-workspace-card-list">
        <section className="admin-workspace-card" aria-label="通知设置">
          <section className="admin-notification-group" aria-label="通知渠道">
            {channels.length === 0 && <div className="admin-state-card">还没有通知渠道。</div>}
            {channels.length > 0 && <AdminNotificationChannelList channels={channels} onDelete={onChannelDelete} onEdit={setEditingChannel} onUpdate={onChannelUpdate} onTest={onChannelTest} />}
          </section>
          <AdminAlertRulesSection rules={rules} nodes={nodes} onUpdate={onRuleUpdate} />
        </section>
      </div>

      <AdminModalPresence>{creatingChannel && (
        <AdminNotificationChannelModal
          mode="create"
          onClose={() => setCreatingChannel(false)}
          onCreate={onChannelCreate}
        />
      )}</AdminModalPresence>
      <AdminModalPresence>{editingChannel && (
        <AdminNotificationChannelModal
          mode="edit"
          channel={editingChannel}
          onClose={() => setEditingChannel(null)}
          onTest={onChannelTest}
          onUpdate={onChannelUpdate}
        />
      )}</AdminModalPresence>
    </section>
  )
}

function AdminNotificationChannelList({ channels, onDelete, onEdit, onUpdate, onTest }: { channels: AdminNotificationChannel[]; onDelete: (channelId: string) => MaybePromise; onEdit: (channel: AdminNotificationChannel) => void; onUpdate?: (channelId: string, input: AdminNotificationChannelUpdateInput) => MaybePromise; onTest?: (channelId: string) => void }) {
  const { requestDelete, deleteModal } = useAdminDeleteConfirm<AdminNotificationChannel>({ entityLabel: '通知渠道', getName: (channel) => channel.name, onConfirm: (channel) => onDelete(channel.id) })
  const [togglingId, setTogglingId] = useState<string | null>(null)
  const [toggleError, setToggleError] = useState<string | null>(null)
  const toggleChannel = (channel: AdminNotificationChannel) => {
    if (!onUpdate || togglingId !== null) return
    setTogglingId(channel.id)
    setToggleError(null)
    runMaybePromise(() => onUpdate(channel.id, { enabled: !channel.enabled }))
      .catch((error: unknown) => setToggleError(adminErrorText(error, '更新失败')))
      .finally(() => setTogglingId(null))
  }

  return (
    <>
      <div className="admin-list admin-notification-list" role="list" aria-label="通知渠道列表">
        <AdminListHead columns={['渠道', '状态', '操作']} />
        {channels.map((channel) => (
          <div className="admin-list-row" role="listitem" key={channel.id}>
            <div className="admin-list-main">
              <strong>{channel.name}</strong>
              <small>{channel.credentialSet ? 'Bot Token 已保存' : '未保存 Bot Token'}{channel.destination ? ' · Chat ID 已配置' : ''}</small>
            </div>
            {onUpdate ? (
              <span data-label="状态" className={`admin-inline-switch${channel.enabled ? ' is-on' : ''}`}>
                <button
                  type="button"
                  role="switch"
                  className="admin-switch"
                  aria-checked={channel.enabled}
                  aria-label={`${channel.enabled ? '停用' : '启用'}通知渠道 ${channel.name}`}
                  disabled={togglingId !== null}
                  onClick={() => toggleChannel(channel)}
                />
                <span>{togglingId === channel.id ? '保存中…' : channel.enabled ? '启用中' : '已停用'}</span>
              </span>
            ) : (
              <AdminStatusBadge label={channel.enabled ? '启用中' : '已停用'} status={channel.enabled ? 'online' : 'disabled'} dataLabel="状态" />
            )}
            <div className="admin-node-row-actions">
              {onTest && <button className="admin-row-action admin-row-text-action" type="button" onClick={() => onTest(channel.id)}>测试发送</button>}
              <AdminRowActions
                entityLabel="通知渠道"
                actionEntityLabel="渠道"
                name={channel.name}
                onEdit={() => onEdit(channel)}
                onDelete={() => requestDelete(channel)}
              />
            </div>
          </div>
        ))}
        {toggleError && <div className="admin-inline-note is-error" role="alert">{toggleError}</div>}
      </div>
      {deleteModal}
    </>
  )
}

function AdminStatusBadge({ label, status, dataLabel }: { label: string; status: 'online' | 'disabled'; dataLabel?: string }) {
  return <span data-label={dataLabel} className={`admin-node-status admin-status-indicator status-${status}`}><i className="admin-status-dot" aria-hidden="true" />{label}</span>
}

type AdminNotificationChannelModalProps =
  | { mode: 'create'; onCreate: (input: AdminNotificationChannelCreateInput) => MaybePromise; onClose: () => void }
  | { mode: 'edit'; channel: AdminNotificationChannel; onUpdate: (channelId: string, input: AdminNotificationChannelUpdateInput) => MaybePromise; onTest: (channelId: string) => void; onClose: () => void }

function AdminNotificationChannelModal(props: AdminNotificationChannelModalProps) {
  const { submitting, error: formError, submit } = useAdminSubmit()
  const editing = props.mode === 'edit'
  const channel = editing ? props.channel : null
  const handleSubmit = (form: HTMLFormElement) => {
    const formData = new FormData(form)
    const name = String(formData.get('channel-name') ?? '').trim()
    const destination = String(formData.get('channel-destination') ?? '').trim()
    const credential = String(formData.get('channel-credential') ?? '').trim()
    if (name === '' || (!editing && (destination === '' || credential === ''))) return
    const enabled = formData.get('channel-enabled') === 'on'
    const action = props.mode === 'edit'
      ? () => props.onUpdate(props.channel.id, { name, ...(destination !== '' ? { destination } : {}), ...(credential !== '' ? { credential } : {}), enabled })
      : () => props.onCreate({ name, destination, credential, enabled })
    void submit(action, editing ? '保存失败' : '添加失败', props.onClose)
  }

  return (
    <AdminFormModal
      title={editing ? '编辑通知渠道' : '添加通知渠道'}
      modalClassName="admin-notification-channel-modal"
      formClassName={`admin-notification-${editing ? 'edit' : 'create'}-form`}
      formLabel={editing ? '编辑通知渠道' : '添加通知渠道'}
      submitting={submitting}
      error={formError}
      onClose={props.onClose}
      onSubmit={handleSubmit}
      footer={(
        <>
          {props.mode === 'edit' && <button type="button" onClick={() => props.onTest(props.channel.id)}>测试发送</button>}
          <button type="submit" disabled={submitting}>{submitting ? (editing ? '保存中…' : '添加中…') : (editing ? '保存通知渠道' : '添加通知渠道')}</button>
        </>
      )}
    >
          <AdminFormSection title="渠道配置">
            <div className="admin-form-grid admin-channel-form-grid">
              <label>
                <span>渠道名称</span>
                <input name="channel-name" autoComplete="off" defaultValue={channel?.name} placeholder={editing ? undefined : 'Zeno Telegram'} />
              </label>
              <label>
                <span>Telegram Chat ID</span>
                <input name="channel-destination" autoComplete="off" defaultValue={channel?.destination} placeholder={editing ? undefined : '请输入 Telegram Chat ID'} />
              </label>
              <AdminCredentialField name="channel-credential" placeholder={channel?.credentialSet ? '留空则保留当前凭据' : '请输入 Telegram Bot Token'} />
              <label className="admin-node-toggle admin-channel-enabled-toggle">
                <input name="channel-enabled" type="checkbox" defaultChecked={channel?.enabled ?? true} />
                <span>{editing ? '启用渠道' : '创建后启用渠道'}</span>
              </label>
            </div>
          </AdminFormSection>
    </AdminFormModal>
  )
}

export default AdminNotificationsWorkspace
