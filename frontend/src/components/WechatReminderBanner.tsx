import { ArrowRight, Bell } from './Icons'

export function WechatReminderBanner({ onOpen }: { onOpen: () => void }) {
  return <button type="button" className="wechat-reminder-banner" aria-label="开启提醒" onClick={onOpen}>
    <span className="wechat-reminder-banner-copy"><Bell size={16} aria-hidden="true" /><span>开启提醒</span></span>
    <ArrowRight size={16} aria-hidden="true" />
  </button>
}
