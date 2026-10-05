import { ScrollView } from '@tarojs/components'
import { Input, Form, SubmitButton } from './Fields';
import { ArrowLeft, Cake, Check } from './Icons';
import { useRef, useState, type FormEvent } from 'react';
import type { AISettings, Member, MemberId, RelationshipState } from '../types';
import { Avatar } from './Avatar';
import { DatePicker } from './DatePicker';
import { PartnerImpression } from './PartnerImpression';
import { RegionPicker } from './RegionPicker';
interface Props { sessionId?: string; state: RelationshipState; onBack: () => void; onSaveMember: (member: Member) => Promise<void>; onSaveSettings: (settings: AISettings) => Promise<void>; notify: (text: string) => void }

function AIForm({ state, onSave, notify }: { state: RelationshipState; onSave: Props['onSaveSettings']; notify: Props['notify'] }) {
  const [tone, setTone] = useState(state.settings.tone);
  const [busy, setBusy] = useState(false);
  const saving = useRef(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (saving.current) return;
    saving.current = true;
    setBusy(true);
    try {
      await onSave({ ...state.settings, tone });
      notify('说话方式已保存 ฅ՞•ﻌ•՞ฅ');
    }
    catch { notify('偏好没保存成功，请再试一下。'); }
    finally { saving.current = false; setBusy(false); }
  };
  return <Form className="detail-form" onSubmit={(event) => void submit(event)}>
    <fieldset className="form-fields" disabled={busy}>
    <section className="form-card">
      <div className="tone-heading">说话方式</div><div className="tone-options">{([{ value: 'warm', label: '温柔陪伴', emoji: '☁️' }, { value: 'playful', label: '调皮一点', emoji: '🐾' }, { value: 'concise', label: '简单直接', emoji: '⚡' }] as const).map((option) => <button type="button" className={tone === option.value ? 'selected' : ''} aria-pressed={tone === option.value} key={option.value} disabled={busy} onClick={() => setTone(option.value)}><span>{option.emoji}</span>{option.label}{tone === option.value && <Check size={11} />}</button>)}</div>
    </section>
    <div className="form-bottom"><SubmitButton className="primary-button" disabled={busy} >{busy ? '正在保存…' : '保存'}</SubmitButton></div>
    </fieldset>
  </Form>;
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
  const today = new Date(Date.now() + 8 * 60 * 60_000).toISOString().slice(0, 10);
  return iso <= today ? iso : null;
};

type RequiredField = 'name' | 'gender' | 'birthday' | 'region';

export function MemberForm({ member, onSave, onSaved, submitLabel = '保存', required = false, showName = false, notify }: { member: Member; onSave: Props['onSaveMember']; onSaved?: () => void; submitLabel?: string; required?: boolean; showName?: boolean; notify: Props['notify'] }) {
  const [draft, setDraft] = useState({ name: member.name, gender: member.gender, hobbies: member.hobbies, region: member.region });
  const [birthday, setBirthday] = useState(displayBirthday(member.birthday));
  const [regionValid, setRegionValid] = useState(true);
  const [hobby, setHobby] = useState('');
  const [hobbyFocused, setHobbyFocused] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<Partial<Record<RequiredField, string>>>({});
  const [busy, setBusy] = useState(false);
  const saving = useRef(false);
  const clearError = (field: RequiredField) => setFieldErrors((current) => ({ ...current, [field]: undefined }));
  const addHobby = () => {
    const value = hobby.trim();
    if (value && !draft.hobbies.includes(value) && draft.hobbies.length < 8) { setDraft({ ...draft, hobbies: [...draft.hobbies, value] }); setHobby(''); }
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (saving.current) return;
    const iso = parseBirthday(birthday);
    const hobbies = hobby.trim() && !draft.hobbies.includes(hobby.trim()) && draft.hobbies.length < 8 ? [...draft.hobbies, hobby.trim()] : draft.hobbies;
    if (showName && !draft.name.trim()) { setFieldErrors((current) => ({ ...current, name: '请输入名称' })); return; }
    if (required) {
      const errors: Partial<Record<RequiredField, string>> = {};
      if (draft.gender !== 'male' && draft.gender !== 'female') errors.gender = '请选择性别';
      if (!birthday) errors.birthday = '请选择生日';
      else if (iso === null) errors.birthday = '请选择有效的生日';
      if (!draft.region?.cityCode || !regionValid) errors.region = '请选择完整的地区';
      setFieldErrors(errors);
      if (Object.keys(errors).length) { notify('请先填写完整资料，再继续。'); return; }
    }
    if (iso === null) { notify('生日还差几位，填成 YYYY.MM.DD 再保存。'); return; }
    if (!regionValid || draft.region && !draft.region.cityCode) { notify('选好城市和区／县，再保存吧。'); return; }
    saving.current = true;
    setBusy(true);
    try { await onSave({ ...member, ...draft, name: draft.name.trim(), birthday: iso, hobbies }); setDraft({ ...draft, name: draft.name.trim(), hobbies }); setHobby(''); notify('小档案收好啦，懂你又多一点点 ♡'); onSaved?.(); }
    catch { notify('小档案还没存好，再试一次吧。'); }
    finally { saving.current = false; setBusy(false); }
  };
  return <Form className="detail-form" onSubmit={(event) => void submit(event)}>
    <fieldset className="form-fields" disabled={busy}>
    {showName && <section className="form-card profile-card"><label className="field-label" htmlFor="member-name">名称</label><Input className="line-input" id="member-name" disabled={busy} value={draft.name} maxLength={24} autoComplete="nickname" aria-invalid={!!fieldErrors.name} onChange={(event) => { setDraft({ ...draft, name: event.target.value }); clearError('name'); }} />{fieldErrors.name && <p className="member-field-error" role="alert">{fieldErrors.name}</p>}</section>}
    <section className="form-card profile-card">
      <div className="field-label" id="member-gender-label"><span>性别 {required && <span className="field-required">必填</span>}</span></div><div className="gender-options" role="group" aria-labelledby="member-gender-label" aria-invalid={!!fieldErrors.gender}>{([{ value: 'male', label: '男' }, { value: 'female', label: '女' }] as const).map((option) => <button type="button" key={option.value} className={draft.gender === option.value ? 'selected' : ''} aria-pressed={draft.gender === option.value} disabled={busy} onClick={() => { setDraft({ ...draft, gender: option.value }); clearError('gender'); }}>{option.label}</button>)}</div>{fieldErrors.gender && <p className="member-field-error" role="alert">{fieldErrors.gender}</p>}
      <label className="field-label spaced-label" htmlFor="birthday"><span>生日 {required && <span className="field-required">必填</span>}</span><Cake size={15} /></label><DatePicker disabled={busy} id="birthday" title="选择生日" placeholder="" clearLabel={required ? undefined : '清空生日'} value={birthday} onChange={(value) => { setBirthday(value); clearError('birthday'); }} />{fieldErrors.birthday && <p className="member-field-error" role="alert">{fieldErrors.birthday}</p>}
    </section>
    <div className="member-region-field"><RegionPicker disabled={busy} value={draft.region} required={required} onChange={(region) => { setDraft((current) => ({ ...current, region })); clearError('region'); }} onValidityChange={setRegionValid} />{fieldErrors.region && <p className="member-field-error" role="alert">{fieldErrors.region}</p>}</div>
    <section className="form-card"><label className="field-label" htmlFor="hobby-input">喜欢的事物</label><div className="hobby-tags">{draft.hobbies.map((item) => <button key={item} type="button" disabled={busy} aria-label={`移除爱好：${item}`} onClick={() => setDraft({ ...draft, hobbies: draft.hobbies.filter((h) => h !== item) })}>{item}<span>×</span></button>)}</div><div className={`hobby-input-row${hobbyFocused ? ' is-focused' : ''}`}><Input onFocus={() => setHobbyFocused(true)} onBlur={() => setHobbyFocused(false)} disabled={busy} id="hobby-input" placeholder="添加爱好" maxLength={20} value={hobby} onChange={(event) => setHobby(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.nativeEvent.isComposing) { event.preventDefault(); addHobby(); } }} /><button type="button" disabled={busy || !hobby.trim() || draft.hobbies.length >= 8} aria-label="添加爱好" onClick={addHobby}>添加</button></div></section>
    <div className="form-bottom"><SubmitButton className="primary-button"  disabled={busy}>{busy ? '正在收好…' : submitLabel}</SubmitButton></div>
    </fieldset>
  </Form>;
}

export function Details({ state, sessionId, onBack, onSaveSettings, notify }: Props) {
  const [selected, setSelected] = useState<Extract<MemberId, 'ai' | 'partner'>>('ai');
  const order = ['ai', 'partner'] as const;
  return <section className="details-view" aria-label="角色信息">
    <header className="details-header"><button className="icon-button back-button" aria-label="返回聊天" onClick={onBack}><ArrowLeft size={21} /></button><h1>角色信息</h1></header>
    <ScrollView scrollY enhanced showScrollbar={false} className="details-scroll">
      <div className="member-selector" role="tablist" aria-label="选择成员">{order.map((id) => {
        const member = state.members.find((m) => m.id === id)!;
        return <button type="button" key={id} className={`member-tile ${id === selected ? 'selected' : ''}`} role="tab" aria-selected={id === selected} aria-controls={`panel-${id}`} id={`tab-${id}`} aria-label={member.name} onClick={() => setSelected(id)}><Avatar member={member} size="normal" showAILabel={id === 'ai'} /><strong>{member.name}</strong></button>;
      })}</div>
      <div className="details-member-panel" role="tabpanel" id={`panel-${selected}`} aria-labelledby={`tab-${selected}`} key={selected}>
        {selected === 'ai' ? <AIForm state={state} onSave={onSaveSettings} notify={notify} /> : <PartnerImpression key={sessionId} sessionId={sessionId} member={state.members.find((m) => m.id === selected)!} notify={notify} />}
      </div>
    </ScrollView>
  </section>;
}
