import { Ellipsis } from './Icons'

/** This native navigation title sits outside each page's scrolling content. */
export function MiniPageHeader({ title, onAction, onDismiss }: {
  title: string
  onAction?: () => void
  onDismiss?: () => void
}) {
  return <header className="mini-page-header" onClick={onDismiss}>
    <h1>{title}</h1>
    {onAction && <button type="button" className="icon-button details-button mini-page-action" aria-label="查看角色信息" onClick={onAction}><Ellipsis size={18} /></button>}
  </header>
}
