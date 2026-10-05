import { isAppVisible, onAppVisibilityChange } from '../lib/platform'
import { useCallback, useEffect, useRef, useState } from 'react'
import { anniversariesApi, type AnniversaryResult } from '../api/anniversaries'

const empty: AnniversaryResult = { anniversaries: [], featured: null, nextCursor: '', spaceCreatedAt: '' }
export function useAnniversaries(sessionId?: string) {
  const scope = useRef(sessionId); scope.current = sessionId
  const sequence = useRef(0)
  const [loaded, setLoaded] = useState<{ sessionId: string; data: AnniversaryResult; pages: number } | null>(null)
  const loadedRef = useRef(loaded); loadedRef.current = loaded
  const [loading, setLoading] = useState(false)
  const loadingRef = useRef(loading); loadingRef.current = loading
  const [error, setError] = useState('')
  const reload = useCallback(async (more = false) => {
    if (!sessionId) return
    const requestId = ++sequence.current
    setLoading(true)
    try {
      const cursor = more && loaded?.sessionId === sessionId ? loaded.data.nextCursor : ''
      const data = await anniversariesApi.list(sessionId, cursor, cursor && loaded?.sessionId === sessionId ? loaded.data.deletionCursor : '')
      if (scope.current !== sessionId || requestId !== sequence.current) return
      setLoaded(current => ({ sessionId, pages: cursor && !data.resetRequired && current?.sessionId === sessionId ? current.pages + 1 : 1, data: { ...data, anniversaries: cursor && !data.resetRequired && current?.sessionId === sessionId
        ? [...new Map([...current.data.anniversaries.filter(item => !data.removedIds?.includes(item.id)), ...data.anniversaries].map(item => [item.id, { ...item, pinned: item.id === data.featured?.id }])).values()]
        : data.anniversaries } }))
      setError('')
    } catch (problem) {
      if (scope.current === sessionId && requestId === sequence.current) setError(problem instanceof Error ? problem.message : '纪念日暂时没加载出来。')
    } finally {
      if (scope.current === sessionId && requestId === sequence.current) setLoading(false)
    }
  }, [sessionId, loaded?.sessionId, loaded?.data.nextCursor])
  const reloadRef = useRef(reload); reloadRef.current = reload
  useEffect(() => {
    ++sequence.current; setError(''); setLoading(false)
    if (!sessionId) return
    void reloadRef.current()
    // Refresh only the loaded pages, preserving keyset pagination and cursor.
    const timer = setInterval(() => {
      if (isAppVisible()) void refreshFeatured()
    }, 15000)
    const refreshFeatured = async () => {
      if (loadingRef.current) return
      const requestId = ++sequence.current
      try {
        const previous = loadedRef.current
        const first = await anniversariesApi.list(sessionId, '', previous?.sessionId === sessionId ? previous.data.deletionCursor : '')
        if (scope.current !== sessionId || requestId !== sequence.current) return
        setLoaded(current => current?.sessionId === sessionId && current.pages > 1 && !first.resetRequired ? { sessionId, pages: current.pages, data: { ...current.data,
          nextCursor: current.pages > 1 ? current.data.nextCursor : first.nextCursor,
          featured: first.featured, spaceCreatedAt: first.spaceCreatedAt, deletionCursor: first.deletionCursor,
          anniversaries: [...new Map([...current.data.anniversaries.filter(item => !first.removedIds?.includes(item.id)), ...first.anniversaries].map(item => [item.id, { ...item, pinned: item.id === first.featured?.id }])).values()],
        } } : { sessionId, pages: 1, data: first })
      } catch { /* Explicit refresh reports errors without replacing live data. */ }
    }
    const dispose = onAppVisibilityChange(visible => { if (visible) void refreshFeatured() })
    return () => { clearInterval(timer); dispose() }
  }, [sessionId])
  const pin = async (id: string, pinned: boolean) => {
    if (!sessionId) throw new Error('请先新建两人空间。')
    ++sequence.current
    setLoading(false)
    const result = await anniversariesApi.pin(sessionId, id, pinned)
    if (scope.current !== sessionId) return
    // Discard reads started while the pin request was still in flight.
    ++sequence.current
    setLoading(false)
    setLoaded(current => current?.sessionId === sessionId ? { sessionId, pages: current.pages, data: { ...current.data,
      featured: pinned ? result.anniversary : null,
      anniversaries: current.data.anniversaries.map(item => item.id === id ? result.anniversary : pinned ? { ...item, pinned: false } : item),
    } } : current)
  }
  return { ...(loaded && loaded.sessionId === sessionId ? loaded.data : empty), loading, error, reload: () => reload(false), loadMore: () => reload(true), pin }
}
export type AnniversaryState = ReturnType<typeof useAnniversaries>
