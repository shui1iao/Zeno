import { type FormEvent, type ReactNode, createContext, useContext, useEffect, useId, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { OverlayOwnerContext, useOverlayPresence } from '../../hooks/useOverlayPresence'
import { focusWithoutScroll, overlayTabStops } from '../../lib/overlayFocus'
import { runMaybePromise, type MaybePromise } from '../../lib/maybePromise'

const AdminModalState = createContext<{ state: 'open' | 'closed'; finishExit: () => void }>({ state: 'open', finishExit: () => {} })
export function AdminModalPresence({ children }: { children: ReactNode }) {
  const presence = useOverlayPresence(Boolean(children))
  const previous = useRef(children)
  if (children) previous.current = children
  return presence.present ? <AdminModalState value={presence}>{children || previous.current}</AdminModalState> : null
}

function AdminModalLayer({ children }: { children: ReactNode }) {
  if (typeof document === 'undefined') return <>{children}</>
  return createPortal(children, document.body)
}

export function AdminModal({ title, onClose, children, className, descriptionId, closeDisabled = false }: { title: string; onClose: () => void; children: ReactNode; className?: string; descriptionId?: string; closeDisabled?: boolean }) {
  const { state, finishExit } = useContext(AdminModalState)
  const active = state === 'open'
  const modalRef = useRef<HTMLElement | null>(null)
  const titleId = useId()
  useEffect(() => {
    const modal = modalRef.current
    if (!modal || typeof document === 'undefined') return undefined
    const previouslyFocused = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const initialTarget = modal.querySelector<HTMLElement>([
      '[autofocus]',
      '.admin-modal-body input:not([disabled])',
      '.admin-modal-body textarea:not([disabled])',
      '.admin-modal-body select:not([disabled])',
      '.admin-modal-body button:not([disabled])',
      'button:not([disabled])',
      '[tabindex]:not([tabindex="-1"])',
    ].join(','))
    focusWithoutScroll(window.matchMedia('(pointer: coarse), (max-width: 767px)').matches ? modal : initialTarget ?? modal)
    return () => {
      if (previouslyFocused?.isConnected) focusWithoutScroll(previouslyFocused)
    }
  }, [])
  const handleKeyDown = (event: React.KeyboardEvent<HTMLElement>) => {
    if (event.key === 'Escape') {
      event.preventDefault()
      event.stopPropagation()
      if (closeDisabled || !active) return
      onClose()
      return
    }
    if (event.key !== 'Tab') return
    const modal = modalRef.current
    if (!modal) return
    const focusable = overlayTabStops(modal)
    if (focusable.length === 0) {
      event.preventDefault()
      focusWithoutScroll(modal)
      return
    }
    const first = focusable[0]
    const last = focusable[focusable.length - 1]
    const focused = document.activeElement
    if (event.shiftKey && (focused === first || !modal.contains(focused))) {
      event.preventDefault()
      focusWithoutScroll(last)
    } else if (!event.shiftKey && (focused === last || !modal.contains(focused))) {
      event.preventDefault()
      focusWithoutScroll(first)
    }
  }
  return (
    <AdminModalLayer>
      <div className="admin-modal-backdrop" role="presentation" data-overlay-state={state} inert={!active} aria-hidden={!active || undefined}>
        <section ref={modalRef} className={`admin-modal${className ? ` ${className}` : ''}`} role="dialog" aria-modal="true" aria-labelledby={titleId} aria-describedby={descriptionId} tabIndex={-1} data-overlay-state={state} onAnimationEnd={event => { if (event.target === event.currentTarget && !active) finishExit() }} onKeyDown={handleKeyDown}>
          <header className="admin-modal-header">
            <div>
              <h3 id={titleId}>{title}</h3>
            </div>
            <button className="admin-modal-close" type="button" onClick={onClose} aria-label="关闭弹窗" disabled={closeDisabled}>×</button>
          </header>
          <OverlayOwnerContext value={active}><div className="admin-modal-body">{children}</div></OverlayOwnerContext>
        </section>
      </div>
    </AdminModalLayer>
  )
}

export function AdminActionFooter({ children, error, className = '' }: { children: ReactNode; error?: string | null; className?: string }) {
  const classes = ['admin-action-footer', className].filter(Boolean).join(' ')
  return (
    <div className={classes}>
      {error && <span className="admin-inline-note admin-action-footer-note is-error">{error}</span>}
      {children}
    </div>
  )
}

export function AdminDeleteConfirmModal({ title, subjectName, confirmLabel, hint = '删除后无法恢复。', onConfirm, onClose }: { title: string; subjectName: string; confirmLabel: string; hint?: string; onConfirm: () => MaybePromise; onClose: () => void }) {
  const [submitting, setSubmitting] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const descriptionId = useId()

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (submitting) return
    setSubmitting(true)
    setFormError(null)
    runMaybePromise(onConfirm)
      .then(onClose)
      .catch((error: unknown) => setFormError(error instanceof Error ? error.message : '删除失败'))
      .finally(() => setSubmitting(false))
  }

  return (
    <AdminModal title={title} className="admin-delete-modal" descriptionId={descriptionId} closeDisabled={submitting} onClose={() => { if (!submitting) onClose() }}>
      <form className="admin-delete-confirm" aria-busy={submitting} inert={submitting ? true : undefined} onSubmit={handleSubmit}>
        <div id={descriptionId} className="admin-delete-confirm__content">
          <p className="admin-delete-confirm__lead">确认删除「<strong>{subjectName}</strong>」？</p>
          <p className="admin-delete-confirm__hint">{hint}</p>
        </div>
        {(formError || submitting) && (
          <div className={`admin-delete-feedback${formError ? ' is-error' : ' is-pending'}`} aria-live="polite" aria-atomic="true">
            {formError ? `删除失败：${formError}` : '正在删除…'}
          </div>
        )}
        <AdminActionFooter>
          <button type="button" disabled={submitting} onClick={onClose}>取消</button>
          <button className="is-danger" type="submit" disabled={submitting}>{submitting ? '删除中…' : confirmLabel}</button>
        </AdminActionFooter>
      </form>
    </AdminModal>
  )
}

export function AdminFormSection({ title, children, className = '' }: { title: string; children: ReactNode; className?: string }) {
  return (
    <section className={`admin-form-section${className ? ` ${className}` : ''}`} aria-label={title}>
      <h4 className="admin-form-section-title">{title}</h4>
      {children}
    </section>
  )
}

export function AdminWorkspaceHeading({ title, actions }: { title: string; actions?: ReactNode }) {
  return (
    <header className="admin-section-heading">
      <div><h3>{title}</h3></div>
      {actions && <div className="admin-section-actions">{actions}</div>}
    </header>
  )
}

export function AdminRowActions({ entityLabel, actionEntityLabel = entityLabel, name, onEdit, onDelete }: { entityLabel: string; actionEntityLabel?: string; name: string; onEdit: () => void; onDelete: () => void }) {
  const editLabel = `编辑${actionEntityLabel}`
  const deleteLabel = `删除${actionEntityLabel}`
  return (
    <div className="admin-row-actions admin-icon-actions">
      <button className="admin-row-action is-icon" type="button" aria-label={`编辑${entityLabel} ${name}`} title={editLabel} onClick={onEdit}><EditActionIcon /><span className="sr-only">{editLabel}</span></button>
      <button className="admin-row-action is-icon is-danger" type="button" aria-label={`删除${entityLabel} ${name}`} title={deleteLabel} onClick={onDelete}><TrashActionIcon /><span className="sr-only">{deleteLabel}</span></button>
    </div>
  )
}

export function AdminCredentialField({ name, placeholder }: { name: string; placeholder: string }) {
  const [visible, setVisible] = useState(false)
  const inputId = useId()
  const actionLabel = visible ? '隐藏 Telegram Bot Token' : '显示 Telegram Bot Token'

  return (
    <div className="admin-form-control admin-secret-field">
      <label htmlFor={inputId}>Telegram Bot Token</label>
      <div className="admin-secret-input">
        <input
          id={inputId}
          name={name}
          type={visible ? 'text' : 'password'}
          autoComplete="new-password"
          placeholder={placeholder}
        />
        <button
          className="admin-secret-toggle"
          type="button"
          aria-label={actionLabel}
          aria-pressed={visible}
          aria-controls={inputId}
          title={actionLabel}
          onClick={() => setVisible((current) => !current)}
        >
          <SecretVisibilityIcon visible={visible} />
        </button>
      </div>
    </div>
  )
}

function EditActionIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M12 20h9" />
      <path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z" />
    </svg>
  )
}

function TrashActionIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M3 6h18" />
      <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
      <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" />
      <path d="M10 11v6M14 11v6" />
    </svg>
  )
}

function SecretVisibilityIcon({ visible }: { visible: boolean }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true">
      <path d="M2.5 12s3.5-6 9.5-6 9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6Z" />
      <circle cx="12" cy="12" r="2.75" />
      {visible && <path d="m4 4 16 16" />}
    </svg>
  )
}
