import { assetUrl } from '../lib/assets'
import { ScrollView } from '@tarojs/components'
import Taro from '@tarojs/taro'
import { ArrowUpRight, Check, ChevronRight, Copy, LogOut, MessageCircle, Pencil, Plus, Sparkle, Unlink } from './Icons'
import { useEffect, useRef, useState } from 'react'
import type { Member, RelationshipState } from '../types'
import { Avatar } from './Avatar'
import { CharacterPicker } from './CharacterPicker'
import { MemberForm } from './Details'
import { Sheet } from './Sheet'
import { SpaceBuddies } from './SpaceBuddies'
import { WechatReminderSettings } from './WechatReminderSettings'
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
  onWechatSubscriptionChange?: (subscription: import('../api/wechat-subscription').WechatSubscription) => void
  onEditProfileInitialHandled?: () => void
}

export function Mine({ editProfileInitially, state, username, code, hasSession, onSaveMember, onLogout, onExitSession, notify, onWechatSubscriptionChange, onEditProfileInitialHandled }: Props) {
  const self = state.members.find((member) => member.id === 'self')!
  const [pickerOpen, setPickerOpen] = useState(false)
  const [editorOpen, setEditorOpen] = useState(!!editProfileInitially)
  const [exitOpen, setExitOpen] = useState(false)
  const [softwarePanel, setSoftwarePanel] = useState<'about' | 'contact' | null>(null)
  const [savingAvatar, setSavingAvatar] = useState(false)
  const [savingProfile, setSavingProfile] = useState(false)
  const [exiting, setExiting] = useState(false)
  const [copied, setCopied] = useState(false)
  const changingAvatar = useRef(false)
  const exitLock = useRef(false)
  const mounted = useRef(true)
  const mineScrollTop = useRef(0)
  const softwareScrollTop = useRef(0)
  const [restoreScrollTop, setRestoreScrollTop] = useState<number | undefined>()
  const saving = savingAvatar || savingProfile
  const pageOverlayOpen = pickerOpen || editorOpen || exitOpen
  const gender = self.gender === 'male' ? '男生' : self.gender === 'female' ? '女生' : '暂不填写'

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  useEffect(() => {
    if (!editProfileInitially) return
    setEditorOpen(true)
    onEditProfileInitialHandled?.()
  }, [editProfileInitially, onEditProfileInitialHandled])

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

  const copyContact = async (data: string, label: string) => {
    try {
      await Taro.setClipboardData({ data })
      if (mounted.current) notify(`${label}已复制`)
    } catch {
      if (mounted.current) notify('复制失败，请重试')
    }
  }

  const openSoftwarePanel = (panel: 'about' | 'contact') => {
    softwareScrollTop.current = mineScrollTop.current
    Taro.createSelectorQuery().select('#mine-scroll').scrollOffset((offset) => {
      if (offset && typeof offset.scrollTop === 'number') softwareScrollTop.current = offset.scrollTop
    }).exec()
    setSoftwarePanel(panel)
  }

  const closeSoftwarePanel = () => {
    setSoftwarePanel(null)
    setRestoreScrollTop(softwareScrollTop.current)
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
    <ScrollView id="mine-scroll" scrollY enhanced showScrollbar={false} scrollTop={restoreScrollTop} className="tab-page-scroll mine-scroll" aria-hidden={pageOverlayOpen} style={{ pointerEvents: pageOverlayOpen ? 'none' : undefined }} onScroll={(event) => { if (!softwarePanel) mineScrollTop.current = event.detail.scrollTop }}>
      {process.env.TARO_ENV !== 'weapp' && <header className="mine-header">
        <div><h1>我的</h1></div>
        <span className="mine-header-sparkle" aria-hidden="true"><Sparkle size={18} strokeWidth={1.6} /></span>
      </header>}

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

      {process.env.TARO_ENV === 'weapp' && <WechatReminderSettings key={self.userId} accountId={self.userId} notify={notify} onSubscriptionChange={onWechatSubscriptionChange} />}

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
      </section>

      <section className="mine-software" aria-labelledby="mine-software-title">
        <div className="mine-section-heading"><h2 id="mine-software-title"><span className="reminder-title-lettering">软件信息</span></h2></div>
        <div className="mine-software-links">
          <button type="button" className="mine-software-link" onClick={() => openSoftwarePanel('about')}>
            <span className="mine-software-icon"><Sparkle size={19} aria-hidden="true" /></span><span className="mine-software-label">关于贴贴</span><ChevronRight size={16} aria-hidden="true" />
          </button>
          <button type="button" className="mine-software-link" onClick={() => openSoftwarePanel('contact')}>
            <span className="mine-software-icon"><MessageCircle size={19} aria-hidden="true" /></span><span className="mine-software-label">联系贴贴</span><ChevronRight size={16} aria-hidden="true" />
          </button>
        </div>
      </section>

      <footer className="mine-footer"><div className="mine-footer-actions">{hasSession && <button type="button" className="secondary-button" disabled={saving || exiting} onClick={() => setExitOpen(true)}><Unlink size={14} aria-hidden="true" />退出专属空间</button>}<button type="button" className="secondary-button" disabled={saving || exiting} onClick={() => { onLogout(); notify('已退出登录') }}><LogOut size={14} aria-hidden="true" />退出登录</button></div></footer>
    </ScrollView>
    {softwarePanel && <Sheet title={softwarePanel === 'about' ? '关于贴贴' : '联系贴贴'} className="mine-software-sheet" onClose={closeSoftwarePanel}>
      {softwarePanel === 'about' ? <div className="mine-software-content">
        <p className="mine-software-intro">让 AI 走进生活，主动记住小事。</p>
        <p>女朋友比我细心，我却常忘记家务、取快递。于是想做一个会主动提醒的小助手，让两个人的日常少一点遗漏。</p>
        <p>这就是贴贴的初衷：让 AI 从等你开口，变成主动关心。</p>
        <ul className="mine-software-ideas">
          <li>为自己、为两个人，或直接为 TA 设置提醒。</li>
          <li>天气变化时，提醒添衣、带伞。</li>
          <li>生日和纪念日临近时，记得提前提醒。</li>
        </ul>
      </div> : <div className="mine-software-content">
        <p>遇到问题，或有想法和建议，都欢迎来找我。</p>
        <div className="mine-contact-list">
          <button type="button" className="mine-contact-row" aria-label="复制小红书名称：贴贴清单" onClick={() => void copyContact('贴贴清单', '小红书名称')}>
            <span className="mine-contact-copy"><span>小红书搜索</span><strong>贴贴清单</strong></span><Copy size={17} aria-hidden="true" />
          </button>
          <button type="button" className="mine-contact-row" aria-label="复制QQ号：2580130642" onClick={() => void copyContact('2580130642', 'QQ号')}>
            <span className="mine-contact-copy"><span>QQ</span><strong>2580130642</strong></span><Copy size={17} aria-hidden="true" />
          </button>
        </div>
      </div>}
    </Sheet>}
    {pickerOpen && <CharacterPicker selectedAvatar={self.avatar} onSelect={(avatar) => { void selectAvatar(avatar) }} onClose={() => setPickerOpen(false)} />}
    {exitOpen && <Sheet title="退出专属空间" onClose={() => setExitOpen(false)}>{(close) => <div className="mine-exit-sheet">
      <p>退出后，你和 TA 都会回到绑定引导页，要重新输入对方邀请码才能继续聊天。</p>
      <p>重新绑定时会创建新的会话和记忆空间。</p>
      <div className="mine-exit-actions">
        <button type="button" className="secondary-button" disabled={exiting} onClick={close}>再想想</button>
        <button type="button" className="primary-button" disabled={exiting} onClick={() => void exitSession(close)}>{exiting ? '正在退出…' : '确认退出'}</button>
      </div>
    </div>}</Sheet>}
    {editorOpen && <Sheet title="编辑我的小档案" className="mine-profile-sheet" onClose={() => setEditorOpen(false)}>{(close) => <div className="mine-profile-editor">
      <MemberForm member={self} showName onSave={async (member) => {
        setSavingProfile(true)
        try { await onSaveMember(member); close() }
        finally { setSavingProfile(false) }
      }} notify={notify} />
    </div>}</Sheet>}
  </section>
}
