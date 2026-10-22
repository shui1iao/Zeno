import { type CSSProperties, type ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { useDismissibleAnchoredPopover } from '../../hooks/useDismissibleAnchoredPopover'
import type { AdminNodeInstallCommand } from '../../types'
import { OverlaySurface } from '../OverlaySurface'
import { calculateAnchoredPopoverStyle, measureAnchoredPopoverHeight } from './AdminFields'
import { AdminFormSection } from './AdminPrimitives'

export type AgentInstallPlatform = 'linux' | 'macos' | 'windows'

export type InstallCommandState =
  | { kind: 'idle' }
  | { kind: 'loading' }
  | { kind: 'ready'; command: string; commands: Partial<Record<AgentInstallPlatform, string>>; platform: AgentInstallPlatform | null }
  | { kind: 'error'; message: string }

type InstallNoticeState =
  | { kind: 'idle' }
  | { kind: 'ready'; message: string }
  | { kind: 'warning'; message: string }
  | { kind: 'error'; message: string }

type TriggerRect = Pick<DOMRect, 'top' | 'right' | 'bottom' | 'left' | 'width' | 'height'>

const agentInstallPlatforms: Array<{ value: AgentInstallPlatform; label: string }> = [
  { value: 'linux', label: 'Linux' },
  { value: 'macos', label: 'macOS' },
  { value: 'windows', label: 'Windows' },
]

export function installCommandForPlatform(state: InstallCommandState, platform: AgentInstallPlatform): string {
  if (state.kind !== 'ready') return ''
  return state.commands[platform] || state.command
}

export function installCommandReady(result: AdminNodeInstallCommand): InstallCommandState {
  return {
    kind: 'ready',
    command: result.command,
    commands: { linux: result.command, ...result.commands },
    platform: null,
  }
}

export function installCommandButtonDisabled(blocked: boolean, state: InstallCommandState): boolean {
  return blocked || state.kind === 'loading'
}

export function isCurrentInstallCommandRequest(requestSequence: number, currentSequence: number, requestNodeId: string, currentNodeId: string): boolean {
  return requestSequence === currentSequence && requestNodeId === currentNodeId
}

export function shouldOpenInstallPlatformPicker(openAfterGenerate: boolean, blocked: boolean): boolean {
  return openAfterGenerate && !blocked
}

export function calculateInstallPlatformMenuStyle(trigger: TriggerRect, viewport: { width: number; height: number }, height = 124): CSSProperties {
  return calculateAnchoredPopoverStyle(trigger, viewport, { width: 184, height })
}

function useInstallPlatformMenuPosition(open: boolean, triggerRef: React.RefObject<HTMLButtonElement | null>, popoverRef: React.RefObject<HTMLDivElement | null>): CSSProperties {
  const [style, setStyle] = useState<CSSProperties>({})

  useLayoutEffect(() => {
    if (!open) return undefined
    const updatePosition = () => {
      const trigger = triggerRef.current
      if (!trigger) return
      const height = measureAnchoredPopoverHeight(popoverRef.current, 124)
      setStyle(calculateInstallPlatformMenuStyle(trigger.getBoundingClientRect(), { width: window.innerWidth, height: window.innerHeight }, height))
    }
    updatePosition()
    const frame = window.requestAnimationFrame(updatePosition)
    const settleTimer = window.setTimeout(updatePosition, 80)
    window.addEventListener('resize', updatePosition)
    window.addEventListener('scroll', updatePosition, true)
    return () => {
      window.cancelAnimationFrame(frame)
      window.clearTimeout(settleTimer)
      window.removeEventListener('resize', updatePosition)
      window.removeEventListener('scroll', updatePosition, true)
    }
  }, [open, triggerRef, popoverRef])

  return style
}

function AdminInstallPlatformPopover({ state, style, popoverRef, onSelect }: {
  state: InstallCommandState
  style: CSSProperties
  popoverRef: React.RefObject<HTMLDivElement | null>
  onSelect: (platform: AgentInstallPlatform) => void
}) {
  if (state.kind !== 'ready') return null
  const popover = (
    <OverlaySurface ref={popoverRef} className="admin-install-platforms" style={style} role="group" aria-label="选择 Agent 安装系统">
      {agentInstallPlatforms.map((platform) => (
        <button key={platform.value} type="button" data-active={state.platform === platform.value} onClick={() => onSelect(platform.value)}>{platform.label}</button>
      ))}
    </OverlaySurface>
  )
  return typeof document === 'undefined' ? popover : createPortal(popover, document.body)
}

export function AdminInstallCommand({ nodeId, initialMessage, blocked = false, onInstallCommand }: {
  nodeId: string
  initialMessage?: ReactNode
  blocked?: boolean
  onInstallCommand: (nodeId: string) => Promise<AdminNodeInstallCommand>
}) {
  const [installCommandState, setInstallCommandState] = useState<InstallCommandState>({ kind: 'idle' })
  const [installCommandNodeId, setInstallCommandNodeId] = useState(nodeId)
  const [installCopyState, setInstallCopyState] = useState<InstallNoticeState>({ kind: 'idle' })
  const [installPlatformPickerOpen, setInstallPlatformPickerOpen] = useState(false)
  const installCopyButtonRef = useRef<HTMLButtonElement>(null)
  const installPlatformPopoverRef = useRef<HTMLDivElement>(null)
  const dismissInstallPlatformPicker = useCallback(() => setInstallPlatformPickerOpen(false), [])
  const requestSequenceRef = useRef(0)
  const copySequenceRef = useRef(0)
  const activeNodeIdRef = useRef(nodeId)
  const blockedRef = useRef(blocked)
  activeNodeIdRef.current = nodeId
  blockedRef.current = blocked

  const currentInstallCommandState: InstallCommandState = installCommandNodeId === nodeId ? installCommandState : { kind: 'idle' }
  const installPlatformPickerVisible = installPlatformPickerOpen && !blocked && currentInstallCommandState.kind === 'ready'
  useDismissibleAnchoredPopover(installPlatformPickerVisible, installCopyButtonRef, installPlatformPopoverRef, dismissInstallPlatformPicker)
  const installPlatformMenuStyle = useInstallPlatformMenuPosition(installPlatformPickerVisible, installCopyButtonRef, installPlatformPopoverRef)

  useEffect(() => {
    requestSequenceRef.current += 1
    copySequenceRef.current += 1
    setInstallCommandNodeId(nodeId)
    setInstallCommandState({ kind: 'idle' })
    setInstallCopyState({ kind: 'idle' })
    setInstallPlatformPickerOpen(false)
  }, [nodeId])

  useEffect(() => () => {
    requestSequenceRef.current += 1
    copySequenceRef.current += 1
  }, [])

  useEffect(() => {
    if (blocked) {
      copySequenceRef.current += 1
      setInstallPlatformPickerOpen(false)
    }
  }, [blocked])

  const requestInstallCommand = (openPickerAfterGenerate = false) => {
    if (blocked) return
    const requestNodeId = nodeId
    const requestSequence = requestSequenceRef.current + 1
    requestSequenceRef.current = requestSequence
    setInstallCommandNodeId(requestNodeId)
    setInstallCommandState({ kind: 'loading' })
    setInstallCopyState({ kind: 'idle' })
    setInstallPlatformPickerOpen(false)
    void onInstallCommand(requestNodeId)
      .then((result) => {
        if (!isCurrentInstallCommandRequest(requestSequence, requestSequenceRef.current, requestNodeId, activeNodeIdRef.current)) return
        if (result.nodeId !== requestNodeId) {
          setInstallCommandState({ kind: 'error', message: '返回的安装命令与当前服务器不匹配' })
          return
        }
        setInstallCommandState(installCommandReady(result))
        if (shouldOpenInstallPlatformPicker(openPickerAfterGenerate, blockedRef.current)) setInstallPlatformPickerOpen(true)
      })
      .catch((error: unknown) => {
        if (!isCurrentInstallCommandRequest(requestSequence, requestSequenceRef.current, requestNodeId, activeNodeIdRef.current)) return
        setInstallCommandState({ kind: 'error', message: error instanceof Error ? error.message : 'unknown error' })
      })
  }

  const handleCopyInstallCommand = () => {
    if (installCommandButtonDisabled(blocked, currentInstallCommandState)) return
    if (currentInstallCommandState.kind !== 'ready') {
      requestInstallCommand(true)
      return
    }
    setInstallPlatformPickerOpen(true)
    setInstallCopyState({ kind: 'idle' })
  }

  const handleCopyInstallPlatform = (platform: AgentInstallPlatform) => {
    const command = installCommandForPlatform(currentInstallCommandState, platform)
    if (!command) return
    const copyNodeId = nodeId
    const copySequence = copySequenceRef.current + 1
    copySequenceRef.current = copySequence
    const copyStillCurrent = () => (
      isCurrentInstallCommandRequest(copySequence, copySequenceRef.current, copyNodeId, activeNodeIdRef.current)
      && !blockedRef.current
    )
    const offerManualCopy = () => {
      if (!copyStillCurrent()) return
      const manualCopy = window.prompt('自动复制失败，请手动复制以下安装命令：', command)
      if (!copyStillCurrent()) return
      setInstallPlatformPickerOpen(false)
      setInstallCopyState(manualCopy === null
        ? { kind: 'error', message: '复制未完成，请重试。' }
        : { kind: 'warning', message: '已显示安装命令，请完成手动复制。' })
    }
    void copyTextToClipboard(command)
      .then((copied) => {
        if (!copyStillCurrent()) return
        if (!copied) {
          offerManualCopy()
          return
        }
        setInstallCommandState((current) => current.kind === 'ready' ? { ...current, platform } : current)
        setInstallPlatformPickerOpen(false)
        setInstallCopyState({ kind: 'ready', message: '安装命令已复制。' })
      })
      .catch(offerManualCopy)
  }

  return (
    <AdminFormSection title="Agent 接入">
      <div className="admin-inline-actions admin-install-copy-row">
        <div className="admin-install-copy-menu">
          <button
            ref={installCopyButtonRef}
            className="admin-install-copy-button"
            type="button"
            aria-haspopup="menu"
            aria-expanded={installPlatformPickerVisible}
            onClick={handleCopyInstallCommand}
            disabled={installCommandButtonDisabled(blocked, currentInstallCommandState)}
          >
            {currentInstallCommandState.kind === 'loading' ? '生成中…' : '复制安装命令'}
          </button>
          {installPlatformPickerVisible && (
            <AdminInstallPlatformPopover
              state={currentInstallCommandState}
              style={installPlatformMenuStyle}
              popoverRef={installPlatformPopoverRef}
              onSelect={handleCopyInstallPlatform}
            />
          )}
        </div>
        {installCopyState.kind === 'idle' && initialMessage && <span className="admin-inline-note is-success">{initialMessage}</span>}
        {installCopyState.kind !== 'idle' && <span className={`admin-inline-note${installCopyState.kind === 'ready' ? ' is-success' : installCopyState.kind === 'warning' ? ' is-warning' : ' is-error'}`}>{installCopyState.message}</span>}
      </div>
      {currentInstallCommandState.kind === 'error' && <div className="admin-install-error">安装命令生成失败：{currentInstallCommandState.message}</div>}
    </AdminFormSection>
  )
}

interface ClipboardWriter {
  writeText: (text: string) => Promise<void>
}

export async function copyTextToClipboard(
  text: string,
  clipboard: ClipboardWriter | undefined = typeof navigator === 'undefined' ? undefined : navigator.clipboard,
): Promise<boolean> {
  if (!clipboard || typeof clipboard.writeText !== 'function') return false
  try {
    await clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}
