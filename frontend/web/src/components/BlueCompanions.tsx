import { useId } from 'react'

/** Soft blue companions, kept as vectors to stay light and sharp on phones. */
export function BlueCompanions({ className = '' }: { className?: string }) {
  const id = useId()
  return <svg className={`blue-companions ${className}`} viewBox="0 0 260 190" fill="none" aria-hidden="true" focusable="false">
    <defs>
      <linearGradient id={`${id}-back`} x1="128" y1="24" x2="208" y2="151" gradientUnits="userSpaceOnUse"><stop stopColor="#c4eaff" /><stop offset=".5" stopColor="#8cc7ff" /><stop offset="1" stopColor="#60a3f6" /></linearGradient>
      <linearGradient id={`${id}-front`} x1="61" y1="64" x2="135" y2="170" gradientUnits="userSpaceOnUse"><stop stopColor="#88d2ff" /><stop offset=".5" stopColor="#499cf9" /><stop offset="1" stopColor="#2864ef" /></linearGradient>
      <radialGradient id={`${id}-shine`}><stop stopColor="white" stopOpacity=".7" /><stop offset="1" stopColor="white" stopOpacity="0" /></radialGradient>
      <filter id={`${id}-shadow`} x="-50%" y="-100%" width="200%" height="300%"><feGaussianBlur stdDeviation="7" /></filter>
    </defs>
    <ellipse cx="135" cy="168" rx="75" ry="8" fill="#4d88d2" opacity=".18" filter={`url(#${id}-shadow)`} />
    <path d="M124 48C128 22 154 19 169 35C180 17 204 24 209 46C216 54 220 68 215 84C236 106 228 136 209 130L202 127C195 161 172 167 154 151C137 156 124 140 126 121C105 121 101 102 116 87C112 72 115 58 124 48Z" fill={`url(#${id}-back)`} />
    <ellipse cx="163" cy="46" rx="34" ry="27" fill={`url(#${id}-shine)`} opacity=".65" />
    <path d="M59 87C44 78 39 59 27 64C13 70 31 103 48 111C38 128 45 148 60 151C55 168 76 177 86 158C98 163 105 163 115 157C126 174 145 160 137 144C153 128 151 107 133 98C131 77 111 68 96 80C88 63 65 67 59 87Z" fill={`url(#${id}-front)`} />
    <ellipse cx="83" cy="86" rx="28" ry="20" fill={`url(#${id}-shine)`} opacity=".6" />
    <g stroke="#234264" strokeWidth="4.5" strokeLinecap="round">
      <path d="M158 76V80M183 72V76M162 92C169 101 181 98 185 89" />
      <path d="M76 105V109M99 109V113M74 121C77 133 94 137 104 124" />
    </g>
    <circle cx="67" cy="117" r="5" fill="#c0e7ff" opacity=".6" /><circle cx="113" cy="121" r="5" fill="#c0e7ff" opacity=".6" />
    <path d="M57 35L60 44L69 47L60 50L57 59L54 50L45 47L54 44Z" fill="#77b5f6" />
    <path d="M228 49V59M223 54H233" stroke="#91bdf4" strokeWidth="2.5" strokeLinecap="round" />
    <circle cx="27" cy="137" r="3" fill="#bfd7f5" />
  </svg>
}
