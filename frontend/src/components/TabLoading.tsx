import { SpaceBuddy } from './SpaceBuddies'

const SPARK = 'M12 1.6 L14.1 9.9 L22.4 12 L14.1 14.1 L12 22.4 L9.9 14.1 L1.6 12 L9.9 9.9 Z'

/**
 * 各页共用状态占位。
 * - 加载中（默认）：带动效的吉祥物 IP（漂浮+呼吸+虚线环+公转点+闪烁星+投影），无文字。
 * - empty=true：没数据，去掉所有动效，只留一枚静态吉祥物 IP，下方给一行说明文字。
 */
export function TabLoading({ empty = false, text, inline = false }: { empty?: boolean; text?: string; inline?: boolean }) {
  const cls = `tab-loading${empty ? ' tab-loading-empty' : ''}${inline ? ' tab-loading-inline' : ''}`
  if (empty) return <div className={cls}>
    <div className="tab-loading-stage">
      <span className="tab-loading-buddy-wrap"><SpaceBuddy variant="ice" className="tab-loading-buddy" /></span>
    </div>
    {text && <p className="tab-loading-note">{text}</p>}
  </div>
  return <div className={cls} role="status" aria-live="polite">
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
