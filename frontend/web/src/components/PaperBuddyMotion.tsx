import './PaperBuddyMotion.css'
import { PaperBuddyUnfold, type PaperBuddyUnfoldProps } from './PaperBuddyUnfold'

interface Props extends PaperBuddyUnfoldProps {
  variant?: 'bump' | 'hop' | 'unfold'
}

/** Decorative paper-cut animation; the parent owns the loading announcement. */
export function PaperBuddyMotion({ variant = 'bump', purpose = 'loading', paused = false, className = '', loop, open, replayKey }: Props) {
  if (variant === 'unfold') return <PaperBuddyUnfold purpose={purpose} paused={paused} className={className} loop={loop} open={open} replayKey={replayKey} />
  return <div className={`paper-motion paper-motion-${variant} paper-motion-${purpose}${paused ? ' is-paused' : ''} ${className}`} aria-hidden="true">
    <div className="paper-motion-stage">
    <div className="paper-motion-ground" />
    <div className="paper-motion-character paper-motion-blue">
      <img className="paper-motion-image" src="/ip/paper-buddies-v2/blue-buddy.png" width="360" height="360" alt="" draggable={false} />
    </div>
    <div className="paper-motion-character paper-motion-ice">
      <img className="paper-motion-image" src="/ip/paper-buddies-v2/ice-buddy.png" width="360" height="360" alt="" draggable={false} />
    </div>
    <div className="paper-motion-accent">
      <div className="paper-motion-tick paper-motion-tick-left" />
      <div className="paper-motion-tick paper-motion-tick-middle" />
      <div className="paper-motion-tick paper-motion-tick-right" />
    </div>
    <div className="paper-motion-dot paper-motion-dot-one" />
    <div className="paper-motion-dot paper-motion-dot-two" />
    <div className="paper-motion-dot paper-motion-dot-three" />
    </div>
  </div>
}
