import { Bell, MessageCircle, UserRound } from 'lucide-react'
import './BottomNav.css'

export type MainView = 'we' | 'things' | 'mine'

const items = [
  { id: 'we', label: '我们', icon: MessageCircle },
  { id: 'things', label: '提醒', icon: Bell },
  { id: 'mine', label: '我的', icon: UserRound },
] as const

export function BottomNav({ view, onChange }: { view: MainView; onChange: (view: MainView) => void }) {
  return <nav className="bottom-nav frost-nav" aria-label="底部菜单">
    {items.map(({ id, label, icon: Icon }) => <button
      type="button"
      key={id}
      className={view === id ? 'is-active' : ''}
      aria-label={label}
      aria-current={view === id ? 'page' : undefined}
      onClick={() => onChange(id)}
    >
      <span className="nav-icon"><Icon size={23} strokeWidth={1.7} aria-hidden="true" /></span>
    </button>)}
  </nav>
}
