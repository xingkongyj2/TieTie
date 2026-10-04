import { Cake } from 'lucide-react'
import { useId } from 'react'
import type { AnniversaryReminderSettings } from '../api/anniversaries'

export function AnniversaryReminders({ settings, disabled, onToggle }: {
  settings: AnniversaryReminderSettings; disabled: boolean; onToggle: () => void
}) {
  const descriptionId = useId()
  return <div className="anniversary-reminders">
    <div className="setting-row">
      <span className="setting-icon"><Cake size={18} /></span>
      <div className="setting-copy"><strong>纪念日提醒</strong><p id={descriptionId}>每年提前 3 天，08:00 提醒我们</p></div>
      <button type="button" className={`toggle ${settings.enabled ? 'is-on' : ''}`} role="switch" aria-checked={settings.enabled} aria-label="纪念日提醒" aria-describedby={descriptionId} disabled={disabled} onClick={onToggle}><span /></button>
    </div>
  </div>
}
