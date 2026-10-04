import { SpaceBuddy } from './SpaceBuddies'

const SPARK = 'M12 1.6 L14.1 9.9 L22.4 12 L14.1 14.1 L12 22.4 L9.9 14.1 L1.6 12 L9.9 9.9 Z'

/** 各页共用的加载动效。 */
export function TabLoading() {
  return <div className="tab-loading" role="status" aria-live="polite" aria-label="正在加载">
    <div className="tab-loading-stage">
      <svg className="tab-loading-ring" viewBox="0 0 200 200" aria-hidden="true"><circle cx="100" cy="100" r="80" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeDasharray="0.5 15" /></svg>
      <span className="tab-loading-orbit"><i /><i /></span>
      <svg className="tab-loading-sparkle sp-a" viewBox="0 0 24 24" aria-hidden="true"><path d={SPARK} fill="currentColor" /></svg>
      <svg className="tab-loading-sparkle sp-b" viewBox="0 0 24 24" aria-hidden="true"><path d={SPARK} fill="currentColor" /></svg>
      <svg className="tab-loading-sparkle sp-c" viewBox="0 0 24 24" aria-hidden="true"><path d={SPARK} fill="currentColor" /></svg>
      <span className="tab-loading-buddy-wrap"><SpaceBuddy variant="ice" className="tab-loading-buddy" /></span>
      <span className="tab-loading-shadow" />
    </div>
  </div>
}
