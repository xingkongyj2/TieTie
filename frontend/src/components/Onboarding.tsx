import { ArrowRight, Check, Link2, MessageCircle, Pencil } from 'lucide-react'
import { useState } from 'react'
import type { Member } from '../types'
import { Avatar } from './Avatar'
import { CharacterPicker } from './CharacterPicker'
import { MemberForm } from './Details'
import { SpaceBuddies } from './SpaceBuddies'
import './Onboarding.css'

interface Props {
  step: 'profile' | 'usage'
  username: string
  member: Member
  onSaveMember: (member: Member) => Promise<void>
  onNext: () => void
  onDone: () => void
  notify: (message: string) => void
  toast: string
}

/** 注册成功后只出现一次；未走完时刷新或重新登录会继续当前步骤。 */
export function Onboarding({ step, username, member, onSaveMember, onNext, onDone, notify, toast }: Props) {
  const [avatar, setAvatar] = useState(member.avatar)
  const [pickerOpen, setPickerOpen] = useState(false)

  return <div className="app-shell onboarding-page">
    <header className="onboarding-header"><h1>贴贴清单</h1><span>初次见面</span></header>
    <div className="onboarding-scroll" inert={pickerOpen}>
      <div className="onboarding-progress" aria-label={`第 ${step === 'profile' ? '1' : '2'} 步，共 2 步`}>
        <span className="is-current"><span>{step === 'profile' ? '1' : <Check size={13} />}</span>认识你</span>
        <i aria-hidden="true" />
        <span className={step === 'usage' ? 'is-current' : ''}><span>2</span>一起开始</span>
      </div>

      {step === 'profile' ? <>
        <div className="onboarding-intro"><span className="onboarding-eyebrow">01 · 我的小档案</span><h2>先认识你，{username}</h2><p>选一张头像，填一点关于自己的事。以后也能在「我的」里修改。</p></div>
        <div className="onboarding-avatar-row">
          <button type="button" className="onboarding-avatar-button" aria-label="选择头像" onClick={() => setPickerOpen(true)}><Avatar member={{ ...member, avatar }} size="large" /><span><Pencil size={12} /></span></button>
          <div><strong>选一张喜欢的头像</strong><p>性别、生日、地区和爱好都可以慢慢补充。</p></div>
        </div>
        <div className="onboarding-profile-form"><MemberForm member={{ ...member, avatar }} onSave={onSaveMember} onSaved={onNext} submitLabel="保存并继续" notify={notify} /></div>
        <button type="button" className="onboarding-skip" onClick={onNext}>稍后再填 <ArrowRight size={14} /></button>
      </> : <>
        <div className="onboarding-intro"><span className="onboarding-eyebrow">02 · 一起开始</span><h2>接下来，和 TA 贴贴</h2><p>两步就能开启你们的专属空间。</p></div>
        <div className="onboarding-usage-art"><SpaceBuddies /></div>
        <div className="onboarding-guide-list">
          <div><span className="onboarding-guide-icon"><Link2 size={19} /></span><div><strong>先连接彼此</strong><p>把你的邀请码发给 TA，再输入 TA 的邀请码完成绑定。</p></div></div>
          <div><span className="onboarding-guide-icon"><MessageCircle size={19} /></span><div><strong>在「我们」里聊聊</strong><p>告诉贴贴想记住的事，或让它帮你们安排提醒。</p></div></div>
        </div>
        <button type="button" className="primary-button onboarding-done" onClick={onDone}>开始使用 <ArrowRight size={16} /></button>
      </>}
    </div>
    {pickerOpen && <CharacterPicker selectedAvatar={avatar} onSelect={setAvatar} onClose={() => setPickerOpen(false)} />}
    {toast && <div className="toast" role="status">{toast}</div>}
  </div>
}
