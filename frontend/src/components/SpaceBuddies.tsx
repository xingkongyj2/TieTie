import { Image, type ImageProps } from '@tarojs/components'
import { spaceBuddyArtwork as artwork } from '../data/ipCharacters'
import { assetUrl } from '../lib/assets'

/** Native widthFix keeps each original character's own aspect ratio. */
export function SpaceBuddy({ variant, className = '', alt = '', mode = 'widthFix' }: { variant: keyof typeof artwork; className?: string; alt?: string; mode?: ImageProps['mode'] }) {
  return <Image className={`h5-img space-buddy ${className}`} src={assetUrl(artwork[variant].src)} mode={mode} ariaLabel={alt} />
}

/** The original pair, composed at its original 210 by 170 canvas coordinates. */
export function SpaceBuddies({ className = '' }: { className?: string }) {
  return <div className={`space-buddies ${className}`} aria-hidden="true">
    <Image className="h5-img space-buddies-decor" src="/assets/decor/pair-decor.png" mode="scaleToFill" />
    <Image className="h5-img mine-buddy-back" src={assetUrl(artwork.ice.src)} mode="aspectFit" />
    <Image className="h5-img mine-buddy-front" src={assetUrl(artwork.blue.src)} mode="aspectFit" />
  </div>
}
