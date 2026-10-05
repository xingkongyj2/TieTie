import Taro from '@tarojs/taro'

let visible = true
const subscribers = new Set<(visible: boolean) => void>()
export const isAppVisible = (): boolean => visible
export function setAppVisible(value: boolean): void {
  visible = value
  subscribers.forEach(callback => callback(value))
}
export function onAppVisibilityChange(callback: (visible: boolean) => void): () => void {
  subscribers.add(callback)
  return () => { subscribers.delete(callback) }
}
export const nextFrame = (callback: () => void): number => setTimeout(() => Taro.nextTick(callback), 16) as unknown as number
export const cancelFrame = (id: number): void => { clearTimeout(id) }
