import { ScrollView } from '@tarojs/components'
import { ArrowRight, CakeSlice, CalendarDays, Check, CircleUserRound, Link2, MessageCircle, Pencil, UserRound, UsersRound } from './Icons'
import { useState } from 'react'
import type { Member } from '../types'
import { Avatar } from './Avatar'
import { CharacterPicker } from './CharacterPicker'
import { MemberForm } from './Details'
import './Onboarding.css'

interface Props {
  step: 'profile' | 'bind' | 'usage'
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
  const stepIndex = step === 'profile' ? 0 : step === 'bind' ? 1 : 2
  const steps = ['填写资料', '连接彼此', '开始使用']

  return <div className="app-shell onboarding-page">
    <header className="onboarding-header"><h1>贴贴清单</h1><span>初次见面</span></header>
    <ScrollView scrollY enhanced showScrollbar={false} className="onboarding-scroll" aria-hidden={pickerOpen} style={{ pointerEvents: pickerOpen ? 'none' : undefined }}>
      <div className="onboarding-progress" aria-label={`第 ${stepIndex + 1} 步，共 3 步`}>
        {steps.map((label, index) => <span className={index === stepIndex ? 'is-current' : index < stepIndex ? 'is-done' : ''} key={label}>
          {index > 0 && <i aria-hidden="true" />}<span>{index < stepIndex ? <Check size={13} /> : index + 1}</span>{label}
        </span>)}
      </div>

      {step === 'profile' ? <>
        <div className="onboarding-intro"><h2>先认识你，{username}</h2><p>填一点关于自己的信息，以后也能在「我的」里修改</p></div>
        <div className="onboarding-avatar-row">
          <button type="button" className="onboarding-avatar-button" aria-label="选择头像" onClick={() => setPickerOpen(true)}><Avatar member={{ ...member, avatar }} size="large" /><span><Pencil size={12} /></span></button>
          <div><strong>选一张喜欢的头像</strong></div>
        </div>
        <div className="onboarding-profile-form"><MemberForm member={{ ...member, avatar }} onSave={onSaveMember} onSaved={onNext} submitLabel="保存并继续" required notify={notify} /></div>
      </> : step === 'bind' ? <>
        <div className="onboarding-intro"><h2>先连接彼此</h2><p>引导结束后，在「我们」里和 TA 互换邀请码，完成绑定。</p></div>
        <div className="onboarding-bind-note"><span><Link2 size={19} aria-hidden="true" /></span><p>绑定成功后，就能一起聊天、安排提醒。</p></div>
        <button type="button" className="primary-button onboarding-done" onClick={onNext}>了解怎么使用 <ArrowRight size={16} /></button>
      </> : <>
        <div className="onboarding-intro"><h2>一起贴贴吧</h2><p>在「我们」发消息，提醒、生日和纪念日都可以直接说。</p></div>
        <div className="onboarding-usage-guide">
          <div className="onboarding-usage-heading"><MessageCircle size={16} aria-hidden="true" /><span>试试这样说</span></div>
          <div className="onboarding-usage-example is-both">
            <div className="onboarding-usage-example-title"><span className="onboarding-usage-icon"><UsersRound size={17} aria-hidden="true" /></span><strong>提醒两个人</strong></div>
            <p>明晚 8 点提醒我们买菜</p>
          </div>
          <div className="onboarding-usage-example is-self">
            <div className="onboarding-usage-example-title"><span className="onboarding-usage-icon"><CircleUserRound size={17} aria-hidden="true" /></span><strong>只提醒自己</strong></div>
            <p>明早 9 点提醒我带钥匙</p>
          </div>
          <div className="onboarding-usage-example is-partner">
            <div className="onboarding-usage-example-title"><span className="onboarding-usage-icon"><UserRound size={17} aria-hidden="true" /></span><strong>只提醒 TA</strong></div>
            <p>周五早上提醒 TA 带伞</p>
          </div>
          <div className="onboarding-usage-example is-birthday">
            <div className="onboarding-usage-example-title"><span className="onboarding-usage-icon"><CakeSlice size={17} aria-hidden="true" /></span><strong>生日</strong></div>
            <p>TA 是 1998 年 11 月 16 日出生的</p>
          </div>
          <div className="onboarding-usage-example is-anniversary">
            <div className="onboarding-usage-example-title"><span className="onboarding-usage-icon"><CalendarDays size={17} aria-hidden="true" /></span><strong>建个纪念日</strong></div>
            <p>我们是 2025 年 5 月 20 日在一起的</p>
          </div>
        </div>
        <button type="button" className="primary-button onboarding-done" onClick={onDone}>进入首页 <ArrowRight size={16} /></button>
      </>}
    </ScrollView>
    {pickerOpen && <CharacterPicker selectedAvatar={avatar} onSelect={setAvatar} onClose={() => setPickerOpen(false)} />}
    {toast && <div className="toast" role="status">{toast}</div>}
  </div>
}
