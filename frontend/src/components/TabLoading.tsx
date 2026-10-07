import { PaperBuddyMotion } from './PaperBuddyMotion'

/** 倒计时、纪念日和贴贴提醒共用接力跳跃动效。 */
export function TabLoading() {
  return <div className="tab-loading" role="status" aria-live="polite" aria-label="正在加载">
    <PaperBuddyMotion variant="hop" purpose="loading" />
  </div>
}
