import { spaceBuddyArtwork as artwork } from '../data/ipCharacters'

/** An individual IP character. Use an empty alt for decorative placements. */
export function SpaceBuddy({ variant, className = '', alt = '' }: { variant: keyof typeof artwork; className?: string; alt?: string }) {
  const { src, width, height } = artwork[variant]
  return <img className={`space-buddy ${className}`} src={src} width={width} height={height} alt={alt} />
}

/** The original pair, composed from the same standalone character artwork. */
export function SpaceBuddies({ className = '' }: { className?: string }) {
  return <svg className={`space-buddies ${className}`} viewBox="0 0 210 170" fill="none" aria-hidden="true" focusable="false">
    <ellipse cx="111" cy="144" rx="87" ry="15" stroke="#B7D3F2" strokeDasharray="3 6" transform="rotate(-16 111 144)" />
    <g className="mine-buddy-back"><image href={artwork.ice.src} x={artwork.ice.x} y={artwork.ice.y} width={artwork.ice.width} height={artwork.ice.height} /></g>
    <g className="mine-buddy-front"><image href={artwork.blue.src} x={artwork.blue.x} y={artwork.blue.y} width={artwork.blue.width} height={artwork.blue.height} /></g>
    <path d="M46 14L50 27L63 31L50 35L46 48L42 35L29 31L42 27L46 14Z" fill="#F1D58D" fillOpacity=".8" />
    <path d="M186 37V47M181 42H191" stroke="#EAB7CB" strokeWidth="2" strokeLinecap="round" />
    <circle cx="17" cy="118" r="3" fill="#B8DBFF" />
  </svg>
}
