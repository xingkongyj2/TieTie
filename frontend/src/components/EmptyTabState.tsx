import { MessageCircle } from './Icons'
import './EmptyTabState.css'

interface Props {
  kind: 'reminders' | 'countdown' | 'anniversary' | 'care'
  title: string
  example: string
}

export function EmptyTabState({ kind, title, example }: Props) {
  return <section className={`empty-tab-state is-${kind}`} role="status">
    <div className="empty-tab-card">
      <h2>{title}</h2>
      <div className="empty-tab-example">
        <span><MessageCircle size={14} aria-hidden="true" />试试这样说</span>
        <p>“{example}”</p>
      </div>
    </div>
  </section>
}
