import { useEffect, useMemo, useRef, useSyncExternalStore } from 'react'
import { createVoiceRecognition } from '../lib/voiceRecognition'
import { authorizeWechatVoice, getWechatRecognitionManager } from '../lib/wechatVoice'

export function useVoiceRecognition(enabled: boolean, onResult: (text: string) => void, onError: (message: string) => void) {
  const callbacks = useRef({ enabled, onResult, onError })
  callbacks.current = { enabled, onResult, onError }
  const controller = useMemo(() => createVoiceRecognition({
    authorize: authorizeWechatVoice,
    getManager: getWechatRecognitionManager,
    onResult: text => { if (callbacks.current.enabled) callbacks.current.onResult(text) },
    onError: message => { if (callbacks.current.enabled) callbacks.current.onError(message) },
  }), [])
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot)
  useEffect(() => { if (!enabled) controller.cancel() }, [controller, enabled])
  // cancel leaves the manager's drain callbacks alive until the old recording
  // ends. It also tolerates React's development effect cleanup/replay.
  useEffect(() => () => { controller.cancel() }, [controller])
  return { ...state, controller }
}
