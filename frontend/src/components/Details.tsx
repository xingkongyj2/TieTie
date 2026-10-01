import { ArrowLeft, Cake, Check } from 'lucide-react';
import { useState, type FormEvent } from 'react';
import type { AISettings, Member, MemberId, RelationshipState } from '../types';
import { Avatar } from './Avatar';
import { DatePicker } from './DatePicker';
import { PartnerImpression } from './PartnerImpression';
interface Props { sessionId?: string; state: RelationshipState; onBack: () => void; onSaveMember: (member: Member) => Promise<void>; onSaveSettings: (settings: AISettings) => Promise<void>; notify: (text: string) => void }

function AIForm({ state, onSave, notify }: { state: RelationshipState; onSave: Props['onSaveSettings']; notify: Props['notify'] }) {
  const [tone, setTone] = useState(state.settings.tone);
  const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    try {
      await onSave({ ...state.settings, tone });
      notify('偏好已保存到此设备 ฅ՞•ﻌ•՞ฅ');
    }
    catch { notify('偏好没保存成功，请再试一下。'); }
    finally { setBusy(false); }
  };
  return <form className="detail-form" onSubmit={(event) => void submit(event)}>
    <fieldset className="form-fields" disabled={busy}>
    <section className="form-card">
      <div className="tone-heading">说话方式</div><div className="tone-options">{([{ value: 'warm', label: '温柔陪伴', emoji: '☁️' }, { value: 'playful', label: '调皮一点', emoji: '🐾' }, { value: 'concise', label: '简单直接', emoji: '⚡' }] as const).map((option) => <button type="button" className={tone === option.value ? 'selected' : ''} aria-pressed={tone === option.value} key={option.value} onClick={() => setTone(option.value)}><span>{option.emoji}</span>{option.label}{tone === option.value && <Check size={11} />}</button>)}</div>
    </section>
    <div className="form-bottom"><button className="primary-button" disabled={busy} type="submit">{busy ? '正在保存…' : '保存'}</button></div>
    </fieldset>
  </form>;
}

/** 生日数据里存 YYYY-MM-DD，界面上按整站的点号格式输入。 */
const displayBirthday = (iso: string) => /^\d{4}-\d{2}-\d{2}$/.test(iso) ? iso.replaceAll('-', '.') : '';
/** 返回待存的 ISO 串；填了但不是合法且不晚于今天的日期时返回 null。 */
const parseBirthday = (shown: string): string | null => {
  const digits = shown.replace(/\D/g, '');
  if (!digits) return '';
  if (digits.length !== 8) return null;
  const iso = `${digits.slice(0, 4)}-${digits.slice(4, 6)}-${digits.slice(6, 8)}`;
  const date = new Date(`${iso}T12:00:00Z`);
  if (Number.isNaN(date.getTime()) || date.toISOString().slice(0, 10) !== iso) return null;
  const today = new Date(Date.now() - new Date().getTimezoneOffset() * 60_000).toISOString().slice(0, 10);
  return iso <= today ? iso : null;
};

export function MemberForm({ member, onSave, notify }: { member: Member; onSave: Props['onSaveMember']; notify: Props['notify'] }) {
  const [draft, setDraft] = useState({ gender: member.gender, hobbies: member.hobbies });
  const [birthday, setBirthday] = useState(displayBirthday(member.birthday));
  const [hobby, setHobby] = useState('');
  const [busy, setBusy] = useState(false);
  const addHobby = () => {
    const value = hobby.trim();
    if (value && !draft.hobbies.includes(value) && draft.hobbies.length < 8) { setDraft({ ...draft, hobbies: [...draft.hobbies, value] }); setHobby(''); }
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const iso = parseBirthday(birthday);
    if (iso === null) { notify('生日还差几位，填成 YYYY.MM.DD 再保存。'); return; }
    setBusy(true);
    const hobbies = hobby.trim() && !draft.hobbies.includes(hobby.trim()) && draft.hobbies.length < 8 ? [...draft.hobbies, hobby.trim()] : draft.hobbies;
    try { await onSave({ ...member, ...draft, birthday: iso, hobbies }); setDraft({ ...draft, hobbies }); setHobby(''); notify('小档案收好啦，懂你又多一点点 ♡'); }
    catch { notify('小档案还没存好，再试一次吧。'); }
    finally { setBusy(false); }
  };
  return <form className="detail-form" onSubmit={(event) => void submit(event)}>
    <fieldset className="form-fields" disabled={busy}>
    <section className="form-card profile-card">
      <div className="field-label" id="member-gender-label">性别</div><div className="gender-options" role="group" aria-labelledby="member-gender-label">{([{ value: 'male', label: '男' }, { value: 'female', label: '女' }] as const).map((option) => <button type="button" key={option.value} className={draft.gender === option.value ? 'selected' : ''} aria-pressed={draft.gender === option.value} onClick={() => setDraft({ ...draft, gender: option.value })}>{option.label}</button>)}</div>
      <label className="field-label spaced-label" htmlFor="birthday">生日 <Cake size={15} /></label><DatePicker id="birthday" value={birthday} onChange={setBirthday} />
    </section>
    <section className="form-card"><label className="field-label" htmlFor="hobby-input">喜欢的事物</label><div className="hobby-tags">{draft.hobbies.map((item) => <button key={item} type="button" aria-label={`移除爱好：${item}`} onClick={() => setDraft({ ...draft, hobbies: draft.hobbies.filter((h) => h !== item) })}>{item}<span>×</span></button>)}</div><div className="hobby-input-row"><input id="hobby-input" placeholder="添加爱好" maxLength={20} value={hobby} onChange={(event) => setHobby(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.nativeEvent.isComposing) { event.preventDefault(); addHobby(); } }} /><button type="button" disabled={!hobby.trim() || draft.hobbies.length >= 8} aria-label="添加爱好" onClick={addHobby}>添加</button></div></section>
    <div className="form-bottom"><button className="primary-button" type="submit" disabled={busy}>{busy ? '正在收好…' : '保存'}</button></div>
    </fieldset>
  </form>;
}

export function Details({ state, sessionId, onBack, onSaveSettings, notify }: Props) {
  const [selected, setSelected] = useState<Extract<MemberId, 'ai' | 'partner'>>('ai');
  const order = ['ai', 'partner'] as const;
  return <section className="details-view" aria-label="角色信息">
    <header className="details-header"><button className="icon-button back-button" aria-label="返回聊天" onClick={onBack}><ArrowLeft size={21} /></button><h1>角色信息</h1></header>
    <div className="details-scroll">
      <div className="member-selector" role="tablist" aria-label="选择成员">{order.map((id) => {
        const member = state.members.find((m) => m.id === id)!;
        return <button type="button" key={id} className={`member-tile ${id === selected ? 'selected' : ''}`} role="tab" aria-selected={id === selected} aria-controls={`panel-${id}`} id={`tab-${id}`} aria-label={id === 'ai' ? `${member.name}，AI 伙伴` : member.name} onClick={() => setSelected(id)}><Avatar member={member} size="normal" /><strong>{member.name}</strong>{id === 'ai' && <span className="member-type-hint">AI 伙伴</span>}</button>;
      })}</div>
      <div role="tabpanel" id={`panel-${selected}`} aria-labelledby={`tab-${selected}`} key={selected}>
        {selected === 'ai' ? <AIForm state={state} onSave={onSaveSettings} notify={notify} /> : <PartnerImpression key={sessionId} sessionId={sessionId} member={state.members.find((m) => m.id === selected)!} notify={notify} />}
      </div>
    </div>
  </section>;
}
