import { type FormEvent, useState } from 'react'
import type { AdminAccountData } from '../../api/adminSession'
import { AdminActionFooter, AdminWorkspaceHeading, adminErrorText } from './AdminPrimitives'

export interface AdminAccountSectionProps {
  account: AdminAccountData
  onUpdate: (username: string, currentPassword: string, newPassword: string) => Promise<void>
  onLogout?: () => void
}

export function validAdminAccountUsername(username: string): boolean {
  return /^[A-Za-z0-9._-]{3,64}$/.test(username.trim())
}

export default function AdminAccountSection({ account, onUpdate, onLogout }: AdminAccountSectionProps) {
  const [message, setMessage] = useState<{ kind: 'error' | 'success'; text: string } | null>(null)
  const [submitting, setSubmitting] = useState(false)

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    const formData = new FormData(event.currentTarget)
    const username = String(formData.get('account-username') ?? '').trim()
    const currentPassword = String(formData.get('current-password') ?? '')
    const newPassword = String(formData.get('new-password') ?? '')
    const confirmPassword = String(formData.get('confirm-password') ?? '')
    if (!validAdminAccountUsername(username)) {
      setMessage({ kind: 'error', text: '账号只能使用 3-64 位字母、数字、点、短横线或下划线。' })
      return
    }
    if (currentPassword === '') {
      setMessage({ kind: 'error', text: '请输入当前密码确认修改。' })
      return
    }
    if (newPassword !== '' && newPassword.length < 8) {
      setMessage({ kind: 'error', text: '新密码至少 8 位；不改密码可留空。' })
      return
    }
    if (newPassword !== confirmPassword) {
      setMessage({ kind: 'error', text: '两次输入的新密码不一致。' })
      return
    }
    setSubmitting(true)
    setMessage(null)
    onUpdate(username, currentPassword, newPassword)
      .then(() => setMessage({ kind: 'success', text: '账户已更新。' }))
      .catch((error: unknown) => setMessage({ kind: 'error', text: adminErrorText(error, '账户更新失败。') }))
      .finally(() => setSubmitting(false))
  }

  return (
    <section className="admin-account-section admin-workspace-panel" aria-label="账户设置">
      <AdminWorkspaceHeading title="登录信息" />
      <form className="admin-account-form admin-node-edit-form is-sectioned admin-workspace-form" aria-label="修改账号和密码" aria-busy={submitting} inert={submitting ? true : undefined} onSubmit={handleSubmit}>
        <section className="admin-workspace-card" aria-label="登录信息">
          <div className="admin-form-grid">
            <label>
              <span>账号</span>
              <input name="account-username" autoComplete="username" defaultValue={account.username} />
            </label>
            <label>
              <span>当前密码</span>
              <input name="current-password" type="password" autoComplete="current-password" />
            </label>
            <label>
              <span>新密码</span>
              <input name="new-password" type="password" autoComplete="new-password" placeholder="留空则不修改" />
            </label>
            <label>
              <span>确认新密码</span>
              <input name="confirm-password" type="password" autoComplete="new-password" placeholder="留空则不修改" />
            </label>
          </div>
          {message && <p className={`admin-install-error${message.kind === 'success' ? ' is-success' : ''}`}>{message.text}</p>}
          <AdminActionFooter>
            {onLogout && <button className="admin-account-logout-button" type="button" onClick={onLogout}>退出登录</button>}
            <button type="submit" disabled={submitting}>{submitting ? '保存中…' : '保存账户'}</button>
          </AdminActionFooter>
        </section>
      </form>
    </section>
  )
}
