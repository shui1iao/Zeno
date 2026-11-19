import { useEffect, useReducer, useRef, useState } from 'react'
import { AdminSettingsConflictError, createAdminNode, createAdminNotificationChannel, createAdminProbeTarget, deleteAdminNode, deleteAdminNotificationChannel, deleteAdminProbeTarget, fetchAdminAlertRules, fetchAdminNodes, fetchAdminNotificationChannels, fetchAdminProbeTargets, fetchAdminSettings, reorderAdminNodes, reorderAdminProbeTargets, requestAdminNodeInstallCommand, testAdminNotificationChannel, updateAdminAlertRule, updateAdminNode, updateAdminNotificationChannel, updateAdminProbeTarget, updateAdminSettings, type AdminAlertRuleUpdateInput, type AdminNodeCreateInput, type AdminNodeUpdateInput, type AdminNotificationChannelCreateInput, type AdminNotificationChannelUpdateInput, type AdminProbeTargetInput, type AdminProbeTargetUpdateInput, type AdminSettingsUpdateInput } from '../api/adminClient'
import { fetchAdminAccount, loginAdmin, logoutAdmin, probeAdminCookieSession, rememberAdminCookieSessionProbe, updateAdminAccount } from '../api/adminSession'
import { defaultSettings } from '../lib/appearance'
import { createAdminMutationCoordinator, runAdminMutationLease } from '../lib/adminMutationCoordinator'
import { adminStateReducer } from '../lib/adminStateReducer'
import { isAdminUnauthorizedError, remoteInsecureAgentControllerURL } from '../lib/adminSettings'
import { adminCookieSessionMarker, captureAdminTokenIdentity, clearStoredAdminToken, clearStoredAdminTokenIfCurrent, isAdminTokenIdentityCurrent, loadStoredAdminToken, rememberAdminToken, type AdminTokenIdentity } from '../lib/adminToken'
import { createMutationEpoch } from '../lib/mutationEpoch'
import { startLiveRefresh } from '../lib/liveRefresh'
import type { AdminAuthState } from '../lib/adminModel'
import type { AdminNode, AdminNodeInstallCommand, AdminSettings } from '../types'

interface AdminControllerOptions {
  initialSettings?: AdminSettings
  onTokenChange?: (token: string) => void
  onSettingsChange?: (settings: AdminSettings) => void
}

export function useAdminController(isAdminRoute: boolean, { initialSettings = defaultSettings, onTokenChange, onSettingsChange }: AdminControllerOptions = {}) {
  const adminMutationEpochRef = useRef(createMutationEpoch())
  const adminMutationCoordinatorRef = useRef(createAdminMutationCoordinator<AdminTokenIdentity>(isAdminTokenIdentityCurrent))
  const adminSessionProbeRef = useRef(0)
  const [adminToken, setAdminToken] = useState(loadStoredAdminToken)
  const [adminSessionReady, setAdminSessionReady] = useState(adminToken !== '')
  const adminTokenRef = useRef(adminToken)
  const [adminAuthState, setAdminAuthState] = useState<AdminAuthState>({ kind: 'idle' })
  const [adminState, dispatchAdminState] = useReducer(adminStateReducer, { kind: 'idle' })
  const [settings, setSettings] = useState<AdminSettings>(initialSettings)
  const onSettingsChangeRef = useRef(onSettingsChange)
  onSettingsChangeRef.current = onSettingsChange
  const commitServerSettings = (nextSettings: AdminSettings) => {
    setSettings(nextSettings)
    onSettingsChangeRef.current?.(nextSettings)
  }

  const commitAdminToken = (token: string) => {
    adminTokenRef.current = token
    setAdminToken(token)
  }

  useEffect(() => {
    if (adminTokenRef.current !== '') {
      setAdminSessionReady(true)
      return undefined
    }
    let cancelled = false
    const probe = ++adminSessionProbeRef.current
    // HttpOnly cookies cannot be inspected by JavaScript. Probe the account
    // endpoint once so a secure cookie session survives a page refresh without
    // ever copying its replayable token into JS state or browser storage.
    probeAdminCookieSession()
      .then((authenticated) => {
        if (cancelled || probe !== adminSessionProbeRef.current || !authenticated) return
        rememberAdminToken()
        commitAdminToken(adminCookieSessionMarker)
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled && probe === adminSessionProbeRef.current) setAdminSessionReady(true)
      })
    return () => { cancelled = true }
  }, [])

  const clearAdminSession = (identity?: AdminTokenIdentity): boolean => {
    if (!adminMutationCoordinatorRef.current.beginSessionTransition(identity)) return false
    if (identity) {
      if (!clearStoredAdminTokenIfCurrent(identity)) {
        adminMutationCoordinatorRef.current.endSessionTransition()
        return false
      }
    } else {
      clearStoredAdminToken()
    }
    rememberAdminCookieSessionProbe(false)
    commitAdminToken('')
    dispatchAdminState({ type: 'state.idle' })
    adminMutationCoordinatorRef.current.endSessionTransition()
    return true
  }

  const expireAdminSession = (identity: AdminTokenIdentity): boolean => {
    if (!clearAdminSession(identity)) return false
    setAdminAuthState({ kind: 'error', message: '登录已过期，请重新登录。' })
    return true
  }

  useEffect(() => { onTokenChange?.(adminToken) }, [adminToken, onTokenChange])
  useEffect(() => {
    if (adminToken === '') setSettings(initialSettings)
  }, [adminToken, initialSettings])

  useEffect(() => {
    if (!isAdminRoute) return
    if (adminToken === '') {
      dispatchAdminState({ type: 'state.idle' })
      return
    }

    let cancelled = false
    let loadedOnce = false
    const loadAdminNodes = async (signal?: AbortSignal) => {
      const mutationSnapshot = adminMutationEpochRef.current.snapshot()
      if (!adminMutationEpochRef.current.isCurrent(mutationSnapshot)) return
      const requestToken = adminToken
      const requestTokenIdentity = captureAdminTokenIdentity(requestToken)
      if (!loadedOnce) dispatchAdminState({ type: 'state.loading' })
      try {
        const [nodesData, results] = await Promise.all([
          fetchAdminNodes(requestToken, signal),
          Promise.allSettled([
            fetchAdminSettings(requestToken, signal),
            fetchAdminAccount(requestToken, signal),
            fetchAdminProbeTargets(requestToken, signal),
            fetchAdminNotificationChannels(requestToken, signal),
            fetchAdminAlertRules(requestToken, signal),
          ]),
        ])
        loadedOnce = true
        if (cancelled || signal?.aborted || !adminMutationEpochRef.current.isCurrent(mutationSnapshot)) return
        dispatchAdminState({ type: 'nodes.loaded', nodes: nodesData.nodes })
        const [settingsResult, accountResult, targetsResult, channelsResult, alertRulesResult] = results
        const unauthorizedResult = results.find((result) => result.status === 'rejected' && isAdminUnauthorizedError(result.reason))
        if (unauthorizedResult) {
          expireAdminSession(requestTokenIdentity)
          return
        }
        if (settingsResult.status === 'fulfilled') commitServerSettings(settingsResult.value)
        dispatchAdminState({
          type: 'details.loaded',
          account: accountResult.status === 'fulfilled' ? accountResult.value : undefined,
          targets: targetsResult.status === 'fulfilled' ? targetsResult.value.targets : undefined,
          notificationChannels: channelsResult.status === 'fulfilled' ? channelsResult.value.channels : undefined,
          alertRules: alertRulesResult.status === 'fulfilled' ? alertRulesResult.value.rules : undefined,
        })
      } catch (error: unknown) {
        loadedOnce = true
        if (cancelled || signal?.aborted || !adminMutationEpochRef.current.isCurrent(mutationSnapshot) || (error instanceof DOMException && error.name === 'AbortError')) return
        if (isAdminUnauthorizedError(error)) {
          expireAdminSession(requestTokenIdentity)
          return
        }
        dispatchAdminState({ type: 'state.error', message: error instanceof Error ? error.message : 'unknown error' })
      }
    }

    const stopRefresh = startLiveRefresh(loadAdminNodes, { immediate: true, timeoutMs: 10_000 })
    return () => {
      cancelled = true
      stopRefresh()
    }
  }, [isAdminRoute, adminToken])

  const submitAdminLogin = (username: string, password: string) => {
    const trimmedUsername = username.trim()
    if (trimmedUsername === '' || password === '') return
    const probe = ++adminSessionProbeRef.current
    setAdminAuthState({ kind: 'loading' })
    loginAdmin(trimmedUsername, password)
      .then((session) => {
        if (probe !== adminSessionProbeRef.current) return
        rememberAdminCookieSessionProbe(true)
        rememberAdminToken(session.token)
        commitAdminToken(session.token)
        setAdminAuthState({ kind: 'idle' })
      })
      .catch((error: unknown) => {
        if (probe !== adminSessionProbeRef.current) return
        setAdminAuthState({ kind: 'error', message: error instanceof Error ? error.message : '登录失败' })
      })
  }

  const clearAdminToken = () => {
    adminSessionProbeRef.current += 1
    adminMutationCoordinatorRef.current.beginSessionTransition()
    const requestToken = adminTokenRef.current
    if (requestToken === '') {
      clearAdminSession()
      setAdminAuthState({ kind: 'idle' })
      return
    }
    const finishMutation = adminMutationEpochRef.current.beginMutation()
    const requestTokenIdentity = captureAdminTokenIdentity(requestToken)
    setAdminAuthState({ kind: 'loading' })
    logoutAdmin(requestToken)
      .then(() => {
        if (clearAdminSession(requestTokenIdentity)) setAdminAuthState({ kind: 'idle' })
      })
      .catch((error: unknown) => {
        if (isAdminUnauthorizedError(error)) {
          expireAdminSession(requestTokenIdentity)
          return
        }
        setAdminAuthState({ kind: 'error', message: error instanceof Error ? `退出失败：${error.message}` : '退出失败' })
      })
      .finally(() => {
        adminMutationCoordinatorRef.current.endSessionTransition()
        finishMutation()
      })
  }

  const handleAdminRequestError = (error: unknown, requestTokenIdentity: AdminTokenIdentity) => {
    if (isAdminUnauthorizedError(error)) {
      expireAdminSession(requestTokenIdentity)
      return
    }
    dispatchAdminState({ type: 'state.error', message: error instanceof Error ? error.message : 'unknown error' })
  }

  const runAdminMutation = <T, R = void>(operation: (requestToken: string, signal: AbortSignal) => Promise<T>, applyResult: (value: T) => R): Promise<R | undefined> => {
    const requestToken = adminTokenRef.current
    if (requestToken === '') return Promise.reject(new Error('missing admin token'))
    const requestTokenIdentity = captureAdminTokenIdentity(requestToken)
    const lease = adminMutationCoordinatorRef.current.begin(requestTokenIdentity)
    if (!lease) return Promise.reject(new DOMException('admin session transition in progress', 'AbortError'))
    const finishMutation = adminMutationEpochRef.current.beginMutation()
    return runAdminMutationLease(lease, (signal) => operation(requestToken, signal), applyResult)
      .catch((error: unknown) => {
        // Mutation forms surface their own errors. Keep the ready dashboard mounted so
        // a failed save/delete does not replace the form with the page-level error state.
        if (isAdminUnauthorizedError(error)) expireAdminSession(requestTokenIdentity)
        throw error
      })
      .finally(finishMutation)
  }

  const updateAdminAccountDetails = (username: string, currentPassword: string, newPassword: string): Promise<void> => runAdminMutation(
    (requestToken, signal) => updateAdminAccount(requestToken, username, currentPassword, newPassword, signal),
    (session) => {
      rememberAdminToken(session.token)
      commitAdminToken(session.token)
      dispatchAdminState({ type: 'account.updated', username: session.username })
    },
  ).then(() => {})

  const createAdminNodeDetails = (input: AdminNodeCreateInput): Promise<AdminNode | undefined> => runAdminMutation(
    (requestToken, signal) => createAdminNode(requestToken, input, signal),
    (createdNode) => {
      dispatchAdminState({ type: 'node.created', node: createdNode })
      return createdNode
    },
  )

  const requestAdminInstallCommand = (nodeId: string): Promise<AdminNodeInstallCommand> => {
    if (adminTokenRef.current === '') return Promise.reject(new Error('missing admin token'))
    const controllerURL = settings.agentControllerUrl.trim() || (typeof window === 'undefined' ? '' : window.location.origin)
    if (remoteInsecureAgentControllerURL(controllerURL) && typeof window !== 'undefined' && !window.confirm('当前 Agent 接入地址使用远程 HTTP，enrollment/runtime token 将以明文传输。仅应在可信隔离网络使用，确认继续生成安装命令？')) {
      return Promise.reject(new Error('已取消生成明文 HTTP 安装命令。'))
    }
    return runAdminMutation(
      (requestToken, signal) => requestAdminNodeInstallCommand(requestToken, nodeId, controllerURL, signal),
      (command) => command,
    ).then((command) => {
      if (command === undefined) throw new DOMException('stale admin session', 'AbortError')
      return command
    })
  }

  const updateAdminNodeDetails = (nodeId: string, input: AdminNodeUpdateInput): Promise<void> => runAdminMutation(
    (requestToken, signal) => updateAdminNode(requestToken, nodeId, input, signal),
    (updatedNode) => {
      dispatchAdminState({ type: 'node.updated', node: updatedNode, probeTargetIds: input.probeTargetIds })
    },
  ).then(() => {})

  const reorderAdminNodeDetails = (nodeIds: string[]): Promise<void> => runAdminMutation(
    (requestToken, signal) => reorderAdminNodes(requestToken, nodeIds, signal),
    () => dispatchAdminState({ type: 'nodes.reordered', nodeIds }),
  ).then(() => {})

  const deleteAdminNodeDetails = (nodeId: string): Promise<void> => runAdminMutation(
    (requestToken, signal) => deleteAdminNode(requestToken, nodeId, signal),
    () => {
      dispatchAdminState({ type: 'node.deleted', nodeId })
    },
  ).then(() => {})

  const createAdminProbeTargetDetails = (input: AdminProbeTargetInput): Promise<void> => runAdminMutation(
    (requestToken, signal) => createAdminProbeTarget(requestToken, input, signal),
    (createdTarget) => {
      dispatchAdminState({ type: 'target.created', target: createdTarget })
    },
  ).then(() => {})

  const updateAdminProbeTargetDetails = (targetId: string, input: AdminProbeTargetUpdateInput): Promise<void> => runAdminMutation(
    (requestToken, signal) => updateAdminProbeTarget(requestToken, targetId, input, signal),
    (updatedTarget) => {
      dispatchAdminState({ type: 'target.updated', target: updatedTarget })
    },
  ).then(() => {})

  const reorderAdminProbeTargetDetails = (targetIds: string[]): Promise<void> => runAdminMutation(
    (requestToken, signal) => reorderAdminProbeTargets(requestToken, targetIds, signal),
    () => dispatchAdminState({ type: 'targets.reordered', targetIds }),
  ).then(() => {})

  const deleteAdminProbeTargetDetails = (targetId: string): Promise<void> => runAdminMutation(
    (requestToken, signal) => deleteAdminProbeTarget(requestToken, targetId, signal),
    () => {
      dispatchAdminState({ type: 'target.deleted', targetId })
    },
  ).then(() => {})

  const createAdminNotificationChannelDetails = (input: AdminNotificationChannelCreateInput): Promise<void> => runAdminMutation(
    (requestToken, signal) => createAdminNotificationChannel(requestToken, input, signal),
    (createdChannel) => {
      dispatchAdminState({ type: 'channel.created', channel: createdChannel })
    },
  ).then(() => {})

  const updateAdminNotificationChannelDetails = (channelId: string, input: AdminNotificationChannelUpdateInput): Promise<void> => runAdminMutation(
    (requestToken, signal) => updateAdminNotificationChannel(requestToken, channelId, input, signal),
    (updatedChannel) => {
      dispatchAdminState({ type: 'channel.updated', channel: updatedChannel })
    },
  ).then(() => {})

  const deleteAdminNotificationChannelDetails = (channelId: string): Promise<void> => runAdminMutation(
    (requestToken, signal) => deleteAdminNotificationChannel(requestToken, channelId, signal),
    () => {
      dispatchAdminState({ type: 'channel.deleted', channelId })
    },
  ).then(() => {})

  const testAdminNotificationChannelDetails = (channelId: string) => {
    void runAdminMutation(
      (requestToken, signal) => testAdminNotificationChannel(requestToken, channelId, signal),
      () => undefined,
    ).catch((error: unknown) => {
      if (error instanceof DOMException && error.name === 'AbortError') return
      const requestToken = adminTokenRef.current
      if (requestToken !== '') handleAdminRequestError(error, captureAdminTokenIdentity(requestToken))
    })
  }

  const updateAdminAlertRuleDetails = (ruleId: string, input: AdminAlertRuleUpdateInput): Promise<void> => runAdminMutation(
    (requestToken, signal) => updateAdminAlertRule(requestToken, ruleId, input, signal),
    (updatedRule) => {
      dispatchAdminState({ type: 'rule.updated', rule: updatedRule })
    },
  ).then(() => {})

  const updateAdminSettingsDetails = (input: AdminSettingsUpdateInput): Promise<AdminSettings | undefined> => runAdminMutation(
    (requestToken, signal) => updateAdminSettings(requestToken, input, signal),
    (updatedSettings) => {
      commitServerSettings(updatedSettings)
      return updatedSettings
    },
  ).catch((error: unknown) => {
    // Conflict responses carry a fresh authenticated snapshot. Keep the app
    // shell and the settings form on the same revision immediately instead of
    // waiting for the next periodic admin refresh.
    if (error instanceof AdminSettingsConflictError) commitServerSettings(error.latestSettings)
    throw error
  })

  return {
    adminToken,
    adminSessionReady,
    adminAuthState,
    adminState,
    settings,
    expireAdminSession,
    submitAdminLogin,
    clearAdminToken,
    updateAdminAccountDetails,
    createAdminNodeDetails,
    updateAdminNodeDetails,
    reorderAdminNodeDetails,
    deleteAdminNodeDetails,
    requestAdminInstallCommand,
    createAdminProbeTargetDetails,
    updateAdminProbeTargetDetails,
    reorderAdminProbeTargetDetails,
    deleteAdminProbeTargetDetails,
    createAdminNotificationChannelDetails,
    updateAdminNotificationChannelDetails,
    deleteAdminNotificationChannelDetails,
    testAdminNotificationChannelDetails,
    updateAdminAlertRuleDetails,
    updateAdminSettingsDetails,
  }
}
