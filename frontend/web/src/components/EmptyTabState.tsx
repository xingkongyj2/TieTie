import { CalendarDays, CloudSun, Hourglass, MessageCircle } from 'lucide-react'
import './EmptyTabState.css'

interface Props {
  kind: 'countdown' | 'anniversary' | 'care'
  title: string
  example: string
}

export function EmptyTabState({ kind, title, example }: Props) {
  const Icon = kind === 'countdown' ? Hourglass : kind === 'anniversary' ? CalendarDays : CloudSun
  return <section className={`empty-tab-state is-${kind}`} role="status">
    <div className="empty-tab-card">
      <span className="empty-tab-art" aria-hidden="true"><Icon size={28} strokeWidth={1.7} /></span>
      <h2>{title}</h2>
      <div className="empty-tab-example">
        <span><MessageCircle size={14} aria-hidden="true" />试试这样说</span>
        <p>“{example}”</p>
      </div>
    </div>
  </section>
}
