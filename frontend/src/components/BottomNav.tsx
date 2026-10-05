import { Bell, MessageCircle, UserRound } from './Icons'
import './BottomNav.css'

export type MainView = 'we' | 'things' | 'mine'

const items = [
  { id: 'we', label: '我们', icon: MessageCircle },
  { id: 'things', label: '待办', icon: Bell },
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
      <span className="nav-icon"><Icon size={19} strokeWidth={view === id ? 1.8 : 1.7} aria-hidden="true" /></span>
    </button>)}
  </nav>
}
