import { PaperBuddyMotion } from './PaperBuddyMotion'

/** 各页共用的加载动效。 */
export function TabLoading() {
  return <div className="tab-loading" role="status" aria-live="polite" aria-label="正在加载">
    <PaperBuddyMotion variant="hop" />
  </div>
}
