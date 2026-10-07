import { Image, View } from '@tarojs/components'
import './PaperBuddyMotion.css'
import { PaperBuddyUnfold, type PaperBuddyUnfoldProps } from './PaperBuddyUnfold'

interface Props extends PaperBuddyUnfoldProps {
  variant?: 'bump' | 'hop' | 'unfold'
}

/** Decorative paper-cut animation; the parent owns the loading announcement. */
export function PaperBuddyMotion({ variant = 'bump', purpose = 'loading', paused = false, className = '', loop, open, replayKey }: Props) {
  if (variant === 'unfold') return <PaperBuddyUnfold purpose={purpose} paused={paused} className={className} loop={loop} open={open} replayKey={replayKey} />
  return <View className={`paper-motion paper-motion-${variant} paper-motion-${purpose}${paused ? ' is-paused' : ''} ${className}`} aria-hidden>
    <View className="paper-motion-stage">
    <View className="paper-motion-ground" />
    <View className="paper-motion-character paper-motion-blue">
      <Image className="paper-motion-image" src="https://mayjimages.s3.bitiful.net/tietie/blue-buddy.png" mode="aspectFit" />
    </View>
    <View className="paper-motion-character paper-motion-ice">
      <Image className="paper-motion-image" src="https://mayjimages.s3.bitiful.net/tietie/ice-buddy.png" mode="aspectFit" />
    </View>
    <View className="paper-motion-accent">
      <View className="paper-motion-tick paper-motion-tick-left" />
      <View className="paper-motion-tick paper-motion-tick-middle" />
      <View className="paper-motion-tick paper-motion-tick-right" />
    </View>
    <View className="paper-motion-dot paper-motion-dot-one" />
    <View className="paper-motion-dot paper-motion-dot-two" />
    <View className="paper-motion-dot paper-motion-dot-three" />
    </View>
  </View>
}
