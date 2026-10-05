import { assetUrl } from '../lib/assets'
import { ScrollView } from '@tarojs/components'
import Taro from '@tarojs/taro'
import { ArrowUpRight, Check, Copy, LogOut, Pencil, Plus, Sparkle, Unlink } from './Icons'
import { useEffect, useRef, useState } from 'react'
import type { Member, RelationshipState } from '../types'
import { Avatar } from './Avatar'
import { CharacterPicker } from './CharacterPicker'
import { MemberForm } from './Details'
import { Sheet } from './Sheet'
import { SpaceBuddies } from './SpaceBuddies'
import './Mine.css'

interface Props {
  editProfileInitially?: boolean
  state: RelationshipState
  username: string
  code: string
  hasSession: boolean
  onSaveMember: (member: Member) => Promise<void>
  onLogout: () => void
  onExitSession: () => Promise<void>
  notify: (text: string) => void
}

export function Mine({ editProfileInitially, state, username, code, hasSession, onSaveMember, onLogout, onExitSession, notify }: Props) {
  const self = state.members.find((member) => member.id === 'self')!
  const [pickerOpen, setPickerOpen] = useState(false)
  const [editorOpen, setEditorOpen] = useState(!!editProfileInitially)
  const [exitOpen, setExitOpen] = useState(false)
  const [savingAvatar, setSavingAvatar] = useState(false)
  const [savingProfile, setSavingProfile] = useState(false)
  const [exiting, setExiting] = useState(false)
  const [copied, setCopied] = useState(false)
  const changingAvatar = useRef(false)
  const exitLock = useRef(false)
  const mounted = useRef(true)
  const saving = savingAvatar || savingProfile
  const gender = self.gender === 'male' ? '男生' : self.gender === 'female' ? '女生' : '暂不填写'

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  useEffect(() => {
    if (!copied) return
    const timer = setTimeout(() => setCopied(false), 2200)
    return () => clearTimeout(timer)
  }, [copied])

  const copyCode = async () => {
    try {
      await Taro.setClipboardData({ data: code });
      if (!mounted.current) return;
      setCopied(true);
      notify('邀请码已复制，分享给想贴贴的人吧');
    } catch {
      if (mounted.current) notify('复制失败，请长按邀请码手动复制');
    }
  }

  const selectAvatar = async (avatar: string) => {
    if (avatar === self.avatar || changingAvatar.current || savingProfile) return
    changingAvatar.current = true
    setSavingAvatar(true)
    try {
      await Taro.getImageInfo({ src: assetUrl(avatar) })
      if (!mounted.current) return
      await onSaveMember({ ...self, avatar })
      if (mounted.current) notify('头像已更新')
    } catch {
      if (mounted.current) notify('头像切换失败，请再试一次。')
    } finally {
      changingAvatar.current = false
      if (mounted.current) setSavingAvatar(false)
    }
  }

  const exitSession = async (close: () => void) => {
    if (exitLock.current) return
    exitLock.current = true
    setExiting(true)
    try {
      await onExitSession()
      close()
      notify('已退出专属空间，重新绑定后才能继续聊天')
    } catch (e) {
      notify(e instanceof Error ? e.message : '退出专属空间失败，请稍后重试。')
    } finally {
      exitLock.current = false
      setExiting(false)
    }
  }
  return <section className="tab-page mine-page" aria-label="我的">
    <ScrollView scrollY enhanced showScrollbar={false} className="tab-page-scroll mine-scroll" aria-hidden={pickerOpen || editorOpen || exitOpen} style={{ pointerEvents: pickerOpen || editorOpen || exitOpen ? 'none' : undefined }}>
      <header className="mine-header">
        <div><h1>我的</h1></div>
        <span className="mine-header-sparkle" aria-hidden="true"><Sparkle size={18} strokeWidth={1.6} /></span>
      </header>

      <section className="mine-identity-card" aria-label="我的个人空间">
        <SpaceBuddies className="mine-buddies" />
        <div className="mine-identity">
          <button type="button" className={`mine-avatar-edit${savingAvatar ? ' is-busy' : ''}`} aria-label={savingAvatar ? '正在切换头像' : '修改头像'} aria-busy={savingAvatar} disabled={saving} onClick={() => setPickerOpen(true)}>
            <Avatar member={self} size="large" />
            {savingAvatar ? <span className="mine-avatar-loading" role="status" aria-live="polite"><span className="spinner" aria-hidden="true" /><span className="sr-only">正在切换头像</span></span> : <span className="mine-avatar-pencil"><Pencil size={12} aria-hidden="true" /></span>}
          </button>
          <div className="mine-name"><div className="mine-name-heading"><h2>{self.name || username}</h2><button type="button" className="mine-name-edit" aria-label="修改名称" title="修改名称" disabled={saving} onClick={() => setEditorOpen(true)}><Pencil size={14} aria-hidden="true" /></button></div><p>有自己的小宇宙，也有在意的人。</p></div>
        </div>
      </section>

      <section className="mine-invite" aria-label="我的邀请码">
        <div className="mine-invite-copy"><span>我的邀请码</span><strong>{code}</strong></div>
        <button type="button" className={`mine-copy-button ${copied ? 'is-copied' : ''}`} aria-label={copied ? '邀请码已复制' : '复制邀请码'} title={copied ? '已复制' : '复制邀请码'} onClick={() => void copyCode()}>
          <span className="mine-copy-mark" aria-hidden="true">{copied ? <Check size={17} strokeWidth={1.7} /> : <Copy size={17} strokeWidth={1.7} />}</span>
        </button>
      </section>

      <section className="mine-about" aria-labelledby="mine-about-title">
        <div className="mine-section-heading"><h2 id="mine-about-title"><span className="reminder-title-lettering">关于我</span></h2><button type="button" className="mine-edit-button" disabled={saving} onClick={() => setEditorOpen(true)}>编辑资料<ArrowUpRight size={16} aria-hidden="true" /></button></div>
        <div className="mine-profile-card">
          <dl className="mine-facts">
            <div><dt>性别</dt><dd className={self.gender && self.gender !== 'unspecified' ? '' : 'is-empty'}>{gender}</dd></div>
            <div><dt>生日</dt><dd className={self.birthday ? '' : 'is-empty'}>{self.birthday ? self.birthday.replaceAll('-', '.') : '还没填写'}</dd></div>
            <div className="mine-region"><dt>地区</dt><dd className={self.region ? '' : 'is-empty'}>{self.region ? [self.region.province, self.region.province === self.region.city ? '' : self.region.city, self.region.district].filter(Boolean).join(' · ') : '还没填写'}</dd></div>
          </dl>
          <div className="mine-interests">
            <h3>喜欢的事物</h3>
            {self.hobbies.length ? <ul className="mine-interest-tags">{self.hobbies.map((hobby, index) => <li key={`${hobby}-${index}`}><span aria-hidden="true">#</span>{hobby}</li>)}</ul> : <button type="button" className="mine-add-interests" disabled={saving} onClick={() => setEditorOpen(true)}><Plus size={15} aria-hidden="true" />放进一点你的热爱</button>}
          </div>
        </div>
        <p className="mine-about-note">小小世界 · 好好做自己</p>
      </section>

      <footer className="mine-footer"><div className="mine-footer-actions">{hasSession && <button type="button" className="secondary-button" disabled={saving || exiting} onClick={() => setExitOpen(true)}><Unlink size={14} aria-hidden="true" />退出专属空间</button>}<button type="button" className="secondary-button" disabled={saving || exiting} onClick={() => { onLogout(); notify('已退出登录') }}><LogOut size={14} aria-hidden="true" />退出登录</button></div></footer>
    </ScrollView>
    {pickerOpen && <CharacterPicker selectedAvatar={self.avatar} onSelect={(avatar) => { void selectAvatar(avatar) }} onClose={() => setPickerOpen(false)} />}
    {exitOpen && <Sheet title="退出专属空间" onClose={() => setExitOpen(false)}>{(close) => <div className="mine-exit-sheet">
      <p>退出后，你和 TA 都会回到绑定引导页，要重新输入对方邀请码才能继续聊天。</p>
      <p>重新绑定时会创建新的会话和记忆空间。</p>
      <div className="mine-exit-actions">
        <button type="button" className="secondary-button" disabled={exiting} onClick={close}>再想想</button>
        <button type="button" className="primary-button" disabled={exiting} onClick={() => void exitSession(close)}>{exiting ? '正在退出…' : '确认退出'}</button>
      </div>
    </div>}</Sheet>}
    {editorOpen && <Sheet title="编辑我的小档案" onClose={() => setEditorOpen(false)}>{(close) => <div className="mine-profile-editor">
      <MemberForm member={self} showName onSave={async (member) => {
        setSavingProfile(true)
        try { await onSaveMember(member); close() }
        finally { setSavingProfile(false) }
      }} notify={notify} />
    </div>}</Sheet>}
  </section>
}
