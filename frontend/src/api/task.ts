/**
 * Taro requests report completion through callbacks and a PromiseTask at once.
 * Our transport owns the callback result, so consume only the duplicate promise
 * rejection while retaining the original abort/chunk/header task methods.
 */
export function consumeTaskRejection<T>(task: T): T {
  const promised = task as { catch?: (onRejected: (reason: unknown) => void) => unknown }
  if (typeof promised?.catch === 'function') void promised.catch(() => {})
  return task
}
