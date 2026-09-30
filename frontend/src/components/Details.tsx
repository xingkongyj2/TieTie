import { ArrowLeft, Bell, Cake, Check, ChevronRight, CloudSun, Heart, Moon, Palette, PawPrint, RefreshCw, ShieldCheck, Sparkles } from 'lucide-react';
import { useState, type FormEvent, type ReactNode } from 'react';
import type { AISettings, Member, MemberId, RelationshipState } from '../types';
import { Avatar } from './Avatar';
import { CharacterPicker } from './CharacterPicker';
import { characterForAvatar, characters } from '../data/characters';
import type { CloudSession } from '../api/qoder';

interface CloudControls {
  selectedId: string | null;
  session: CloudSession | null;
  loading: boolean;
  refreshing: boolean;
  submitting: boolean;
  busy: boolean;
  error: string;
  reload: () => Promise<void>;
}

interface Props { state: RelationshipState; cloud: CloudControls; onBack: () => void; onSaveMember: (member: Member) => Promise<void>; onSaveSettings: (settings: AISettings) => Promise<void>; notify: (text: string) => void; onLogout: () => void; username: string }

const statusLabels: Record<string, string> = {
  idle: '可以继续聊天', running: '正在回复', rescheduling: '正在恢复连接',
  canceling: '正在停止', terminated: '会话已结束', archived: '会话已归档',
};

function ToggleRow({ icon, title, description, checked, onChange }: { icon: ReactNode; title: string; description: string; checked: boolean; onChange: () => void }) {
  return <div className="setting-row"><span className="setting-icon">{icon}</span><div className="setting-copy"><strong>{title}</strong><p>{description}</p></div><button type="button" className={`toggle ${checked ? 'is-on' : ''}`} role="switch" aria-checked={checked} aria-label={title} onClick={onChange}><span /></button></div>;
}

function CharacterField({ avatar, onOpen }: { avatar: string; onOpen: () => void }) {
  const character = characterForAvatar(avatar);
  return <section className="form-card character-field">
    <div className="character-field-heading"><span><Palette size={15} />专属小形象</span><span>{characters.length} 位伙伴，随心挑</span></div>
    <button type="button" className="character-current" onClick={onOpen} aria-label="打开角色图鉴，更换头像">
      <span className="character-current-image" style={{ backgroundColor: character?.color ?? '#eef2ff' }}><img src={avatar} alt="当前角色" /></span>
      <span className="character-current-copy"><strong>{character?.name ?? '我的小伙伴'}</strong><span>{character?.animal ?? '专属形象'} · {character?.personality ?? '和你在一起'}</span><span className="character-palette">{characters.slice(0, 8).map((item) => <i key={item.id} style={{ backgroundColor: item.color }} />)}</span></span>
      <span className="character-current-action">换一只 <ChevronRight size={14} /></span>
    </button>
  </section>;
}

function AIForm({ state, onSave, onSaveMember, notify }: { state: RelationshipState; onSave: Props['onSaveSettings']; onSaveMember: Props['onSaveMember']; notify: Props['notify'] }) {
  const ai = state.members.find((member) => member.id === 'ai')!;
  const [draft, setDraft] = useState(state.settings);
  const [avatar, setAvatar] = useState(ai.avatar);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const toggle = (key: 'sharedReminders' | 'weatherCare' | 'anniversaryReminders' | 'quietHours') => setDraft((value) => ({ ...value, [key]: !value[key] }));
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!draft.name.trim()) return;
    setBusy(true);
    try {
      await onSave({ ...draft, name: draft.name.trim() });
      if (avatar !== ai.avatar) await onSaveMember({ ...ai, avatar, name: draft.name.trim() });
      notify('偏好已保存到此设备 ฅ՞•ﻌ•՞ฅ');
    }
    catch { notify('偏好没保存成功，请再试一下。'); }
    finally { setBusy(false); }
  };
  return <><form className="detail-form" onSubmit={(event) => void submit(event)}>
    <fieldset className="form-fields" disabled={busy}>
    <div className="detail-intro"><span className="eyebrow">YOUR LITTLE SIDEKICK</span><h2>小事交给我，<br />你们好好在一起<span className="blue-dot">.</span></h2><p>不偏心、不说教，偶尔卖个萌。</p></div>
    <CharacterField avatar={avatar} onOpen={() => setPickerOpen(true)} />
    <section className="form-card"><label className="field-label" htmlFor="ai-name">怎么称呼我 <span>起个只有你们懂的名字</span></label><input id="ai-name" className="line-input" maxLength={16} required value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} />
      <div className="tone-heading">我的说话方式</div><div className="tone-options">{([{ value: 'warm', label: '温柔陪伴', emoji: '☁️' }, { value: 'playful', label: '调皮一点', emoji: '🐾' }, { value: 'concise', label: '简单直接', emoji: '⚡' }] as const).map((tone) => <button type="button" className={draft.tone === tone.value ? 'selected' : ''} aria-pressed={draft.tone === tone.value} key={tone.value} onClick={() => setDraft({ ...draft, tone: tone.value })}><span>{tone.emoji}</span>{tone.label}{draft.tone === tone.value && <Check size={11} />}</button>)}</div>
    </section>
    <div className="section-label"><Sparkles size={14} /><h3>这些小事，我来操心</h3></div>
    <section className="form-card settings-card">
      <ToggleRow icon={<Bell size={18} />} title="共享提醒" description="重要的小事，替你们一起记着" checked={draft.sharedReminders} onChange={() => toggle('sharedReminders')} />
      <ToggleRow icon={<CloudSun size={19} />} title="天气关怀" description="变天的时候，多一句暖暖的叮嘱" checked={draft.weatherCare} onChange={() => toggle('weatherCare')} />
      <ToggleRow icon={<Cake size={18} />} title="纪念日小惊喜" description="提前 3 天，提醒你准备一点心意" checked={draft.anniversaryReminders} onChange={() => toggle('anniversaryReminders')} />
      <ToggleRow icon={<Moon size={18} />} title="晚安，轻声模式" description="23:00 — 08:00，把安静留给你们" checked={draft.quietHours} onChange={() => toggle('quietHours')} />
    </section>
    <p className="settings-note">这些偏好仅保存在此设备，尚未同步到云端 AI；实际回复由云端配置决定。</p>
    <div className="form-bottom"><button className="primary-button" disabled={busy} type="submit">{busy ? '正在记住…' : '保存我的偏好'}<Check size={17} /></button></div>
    </fieldset>
  </form>{pickerOpen && <CharacterPicker selectedAvatar={avatar} ownerName={draft.name || '贴贴'} onSelect={setAvatar} onClose={() => setPickerOpen(false)} />}</>;
}

function MemberForm({ member, onSave, notify }: { member: Member; onSave: Props['onSaveMember']; notify: Props['notify'] }) {
  const [draft, setDraft] = useState(member);
  const [pickerOpen, setPickerOpen] = useState(false);
  const [hobby, setHobby] = useState('');
  const [busy, setBusy] = useState(false);
  const addHobby = () => {
    const value = hobby.trim();
    if (value && !draft.hobbies.includes(value) && draft.hobbies.length < 8) { setDraft({ ...draft, hobbies: [...draft.hobbies, value] }); setHobby(''); }
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    const hobbies = hobby.trim() && !draft.hobbies.includes(hobby.trim()) && draft.hobbies.length < 8 ? [...draft.hobbies, hobby.trim()] : draft.hobbies;
    try { await onSave({ ...draft, name: draft.name.trim(), hobbies }); setDraft({ ...draft, hobbies }); setHobby(''); notify('小档案收好啦，懂你又多一点点 ♡'); }
    catch { notify('小档案还没存好，再试一次吧。'); }
    finally { setBusy(false); }
  };
  return <><form className="detail-form" onSubmit={(event) => void submit(event)}>
    <fieldset className="form-fields" disabled={busy}>
    <div className="detail-intro"><span className="eyebrow">A LITTLE MORE ABOUT YOU</span><h2>每个小偏好，<br />都值得被记住<span className="blue-dot">.</span></h2><p>多认识{member.id === 'self' ? '你' : member.name}一点，关心就更贴心一点。</p></div>
    <CharacterField avatar={draft.avatar} onOpen={() => setPickerOpen(true)} />
    <section className="form-card profile-card">
      <label className="field-label" htmlFor="member-name">小窝里的名字 <span>{member.id === 'self' ? '这是你呀' : '你的特别的人'}</span></label><input className="line-input" id="member-name" maxLength={16} required value={draft.name} onChange={(event) => setDraft({ ...draft, name: event.target.value })} />
      <label className="field-label spaced-label" htmlFor="birthday">生日 <Cake size={15} /></label><input className="line-input" id="birthday" type="date" max={new Date(Date.now() - new Date().getTimezoneOffset() * 60_000).toISOString().split('T')[0]} value={draft.birthday} onChange={(event) => setDraft({ ...draft, birthday: event.target.value })} />
    </section>
    <section className="form-card"><label className="field-label" htmlFor="hobby-input">喜欢的事物 <span>最多 8 个小偏好</span></label><div className="hobby-tags">{draft.hobbies.map((item) => <button key={item} type="button" aria-label={`移除爱好：${item}`} onClick={() => setDraft({ ...draft, hobbies: draft.hobbies.filter((h) => h !== item) })}>{item}<span>×</span></button>)}</div><div className="hobby-input-row"><input id="hobby-input" placeholder="比如：少冰三分糖 🧋" maxLength={20} value={hobby} onChange={(event) => setHobby(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.nativeEvent.isComposing) { event.preventDefault(); addHobby(); } }} /><button type="button" disabled={!hobby.trim() || draft.hobbies.length >= 8} aria-label="添加爱好" onClick={addHobby}>添加</button></div></section>
    <section className="form-card"><label className="field-label" htmlFor="member-bio">关于{member.id === 'self' ? '我' : 'TA'}的小备注 <Heart size={14} /></label><textarea id="member-bio" placeholder="一点小习惯、一份小心愿，都可以告诉贴贴…" rows={3} maxLength={200} value={draft.bio} onChange={(event) => setDraft({ ...draft, bio: event.target.value })} /><div className="input-counter">{draft.bio.length}/200</div></section>
    <p className="settings-note"><ShieldCheck size={13} />资料仅保存在此设备，供本地演示使用。</p>
    <div className="form-bottom"><button className="primary-button" type="submit" disabled={busy}>{busy ? '正在收好…' : '保存这份小档案'}<Check size={17} /></button></div>
    </fieldset>
  </form>{pickerOpen && <CharacterPicker selectedAvatar={draft.avatar} ownerName={draft.name || member.name} onSelect={(nextAvatar) => setDraft((current) => ({ ...current, avatar: nextAvatar }))} onClose={() => setPickerOpen(false)} />}</>;
}

export function Details({ state, cloud, onBack, onSaveMember, onSaveSettings, notify, onLogout, username }: Props) {
  const [selected, setSelected] = useState<MemberId>('ai');
  const order: MemberId[] = ['self', 'ai', 'partner'];
  return <section className="details-view" aria-label="小窝详情">
    <header className="details-header"><button className="icon-button back-button" aria-label="返回聊天" onClick={onBack}><ArrowLeft size={21} /></button><h1>小窝详情</h1><Heart size={19} className="header-heart" /></header>
    <div className="details-scroll">
      <div className="members-caption"><span>小窝成员 <b>3</b></span><span>我们三个，刚刚好 <PawPrint size={11} /></span></div>
      <div className="member-selector" role="tablist" aria-label="选择成员">{order.map((id) => {
        const member = state.members.find((m) => m.id === id)!;
        return <button type="button" key={id} className={`member-tile ${id === selected ? 'selected' : ''}`} role="tab" aria-selected={id === selected} aria-controls={`panel-${id}`} id={`tab-${id}`} onClick={() => setSelected(id)}><Avatar member={member} size="large" /><strong>{member.name}{id === 'self' && <span>（我）</span>}</strong><span className="member-role">{id === 'ai' ? '专属 AI 搭子' : id === 'self' ? '小窝主人' : '特别的那个人'}</span>{id === selected && <span className="member-selected-dot" />}</button>;
      })}</div>
      <div role="tabpanel" id={`panel-${selected}`} aria-labelledby={`tab-${selected}`} key={selected}>
        {selected === 'ai' ? <AIForm state={state} onSave={onSaveSettings} onSaveMember={onSaveMember} notify={notify} /> : <MemberForm member={state.members.find((m) => m.id === selected)!} onSave={onSaveMember} notify={notify} />}
      </div>
      <div className="details-footnote">小小的我们，大大的默契 <ChevronRight size={11} /></div>
      <section className="details-cloud-settings" aria-label="云端会话设置">
        <div className="section-label"><CloudSun size={14} /><h3>云端会话</h3></div>
        <p className="details-cloud-hint">你们绑定后共享同一个专属会话，聊天记录都在这里。</p>
        <div className="cloud-session-bar">
          <div className="cloud-session-select"><label htmlFor="cloud-session">当前会话</label><p id="cloud-session" className="cloud-session-name"><Heart size={13} />我们俩的专属小窝</p>
          <p aria-live="polite">{cloud.loading ? '正在加载聊天记录…' : cloud.error ? '请刷新会话后再继续' : cloud.busy ? '伙伴正在处理消息…' : cloud.session ? statusLabels[cloud.session.status] ?? `会话状态：${cloud.session.status}` : '会话准备中…'}</p></div>
          <button className="icon-button cloud-refresh" aria-label="刷新云端会话" disabled={cloud.loading || cloud.refreshing || cloud.submitting} onClick={() => void cloud.reload()}><RefreshCw size={17} className={cloud.loading || cloud.refreshing ? 'is-spinning' : ''} /></button>
        </div>
        {cloud.error && <div className="cloud-error" role="alert"><span>{cloud.error}</span><button disabled={cloud.loading || cloud.refreshing || cloud.submitting} onClick={() => void cloud.reload()}>重试</button></div>}
        <div className="account-bar">
          <span className="account-name"><ShieldCheck size={14} />当前账号：{username}</span>
          <button type="button" className="account-logout" onClick={() => { onLogout(); notify('已退出登录'); }}>退出登录</button>
        </div>
      </section>
    </div>
  </section>;
}
