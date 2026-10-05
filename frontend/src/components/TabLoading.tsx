import { Image } from '@tarojs/components'
import { SpaceBuddy } from './SpaceBuddies'

/** 各页共用的加载动效。 */
export function TabLoading() {
  return <div className="tab-loading" role="status" aria-live="polite" aria-label="正在加载">
    <div className="tab-loading-stage">
      <Image className="h5-img tab-loading-ring" mode="scaleToFill" src="/assets/decor/loading-ring.png" aria-hidden="true" />
      <span className="tab-loading-orbit"><i /><i /></span>
      <Image mode="scaleToFill" className="h5-img tab-loading-sparkle sp-a" src="/assets/decor/loading-spark.png" aria-hidden="true" />
      <Image mode="scaleToFill" className="h5-img tab-loading-sparkle sp-b" src="/assets/decor/loading-spark-pink.png" aria-hidden="true" />
      <Image mode="scaleToFill" className="h5-img tab-loading-sparkle sp-c" src="/assets/decor/loading-spark-blue.png" aria-hidden="true" />
      <span className="tab-loading-buddy-wrap"><SpaceBuddy variant="ice" className="tab-loading-buddy" /></span>
      <span className="tab-loading-shadow" />
    </div>
  </div>
}
