import { ArrowUp, AtSign, CalendarDays, CloudSun, EyeOff, FileSpreadsheet, FileText, Mic, Paperclip, Square, X } from './Icons';
import { useEffect, useRef, useState } from 'react';
import Taro from '@tarojs/taro';
import { Image, Textarea } from '@tarojs/components';
import type { Member } from '../types';
import { atomicMentionEdit, completePartnerMention, mentionRanges } from '../lib/mentions';
import { isImageAttachment, validateAttachments } from '../api/qoder';
import { chooseAttachments, type MiniFile } from '../lib/files';
import { attachmentDisplayName } from '../lib/attachments';
import { nextFrame, onAppVisibilityChange } from '../lib/platform';

interface Props { members: Member[]; sending: boolean; processing: boolean; stopping: boolean; disabled?: boolean; disabledReason?: string; placeholder?: string; dismissSignal?: number; onSend: (text: string, files: MiniFile[], visibility: 'shared' | 'private') => Promise<boolean>; onStop: () => Promise<void>; onTool: (tool: 'reminders' | 'anniversary') => void; onError: (text: string) => void }

/** Native input and touch events keep the existing composer and transition classes. */
export function Composer({ members, sending, processing, stopping, disabled = false, disabledReason, placeholder = '说点什么，让我们更近一点…', dismissSignal, onSend, onStop, onTool, onError }: Props) {
  const [text, setText] = useState('');
  const [visibility, setVisibility] = useState<'shared' | 'private'>('shared');
  const [expanded, setExpanded] = useState(false);
  const [voiceState, setVoiceState] = useState<'idle' | 'listening' | 'cancel' | 'processing'>('idle');
  const voiceActive = voiceState === 'listening';
  const [inputFocused, setInputFocused] = useState(false);
  const [caret, setCaret] = useState(-1);
  const [files, setFiles] = useState<MiniFile[]>([]);
  const [queryingWeather, setQueryingWeather] = useState(false);
  const partner = members.find((member) => member.id === 'partner' && member.userId && member.name !== '另一位成员');
  const names = partner ? [partner.name] : [];
  const toPartner = mentionRanges(text, names).length > 0;
  const privateControlDisabled = sending || disabled || processing || toPartner;
  const hasContent = !!text.trim() || files.length > 0;
  // WeChat applies a native disabled color to the button. These icons are
  // CSS-mask views, so paint the mask itself while the compact composer is
  // showing the AI processing state.
  const processingIconStyle = processing ? { color: '#7b879a', backgroundColor: '#7b879a' } : undefined;
  const focusTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const holdTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const suppressCompactClickRef = useRef(false);
  const gestureRef = useRef<{ startY: number; cancelled: boolean } | null>(null);
  const selectingRef = useRef(false);
  const sendingRef = useRef(false);
  const clearHold = () => { if (holdTimerRef.current) clearTimeout(holdTimerRef.current); holdTimerRef.current = null; };
  const cancelVoice = () => { clearHold(); gestureRef.current = null; setVoiceState('idle'); };
  useEffect(() => {
    const stopVisibility = onAppVisibilityChange((visible) => { if (!visible) { cancelVoice(); setInputFocused(false); } });
    return () => { stopVisibility(); if (focusTimerRef.current) clearTimeout(focusTimerRef.current); clearHold(); };
  }, []);
  useEffect(() => { if (dismissSignal !== undefined) { setInputFocused(false); setExpanded(false); void Taro.hideKeyboard().catch(() => {}); } }, [dismissSignal]);
  useEffect(() => { if (processing) { setInputFocused(false); setExpanded(false); } }, [processing]);
  const openEditor = (focusInput = true) => {
    if (sending || disabled || processing) return;
    setExpanded(true);
    if (focusTimerRef.current) clearTimeout(focusTimerRef.current);
    if (focusInput) focusTimerRef.current = setTimeout(() => { focusTimerRef.current = null; setInputFocused(true); }, 110);
  };
  const selectFiles = async () => {
    if (selectingRef.current || sending || disabled || processing) return;
    if (files.length >= 4) { onError('一次最多添加 4 个附件。'); return; }
    selectingRef.current = true;
    setInputFocused(false);
    try {
      const selected = await chooseAttachments(4 - files.length);
      if (selected.length) { const next = [...files, ...selected]; validateAttachments(next); setFiles(next); setExpanded(true); }
    } catch (error) { onError(error instanceof Error ? error.message : '无法添加附件。'); }
    finally { selectingRef.current = false; }
  };
  const startVoice = (startY = 0) => {
    if (sending || disabled || processing || gestureRef.current) return;
    clearHold(); setInputFocused(false); void Taro.hideKeyboard().catch(() => {});
    gestureRef.current = { startY, cancelled: false };
    setVoiceState('listening');
  };
  const releaseVoice = () => {
    const gesture = gestureRef.current;
    cancelVoice();
    if (gesture && !gesture.cancelled) onError('小程序语音转写尚未配置，请点击输入框打字。');
  };
  const compactTouchStart = (startY: number) => {
    if (sending || disabled || processing) return;
    clearHold(); suppressCompactClickRef.current = false;
    gestureRef.current = { startY, cancelled: false };
    holdTimerRef.current = setTimeout(() => { holdTimerRef.current = null; gestureRef.current = null; suppressCompactClickRef.current = true; startVoice(startY); }, 360);
  };
  const touchMove = (y: number) => {
    const gesture = gestureRef.current; if (!gesture) return;
    if (holdTimerRef.current) { if (Math.abs(y - gesture.startY) > 16) { clearHold(); gestureRef.current = null; } return; }
    gesture.cancelled = y < gesture.startY - 65;
    setVoiceState(gesture.cancelled ? 'cancel' : 'listening');
  };
  const touchEnd = () => { if (holdTimerRef.current) { clearHold(); gestureRef.current = null; } else if (gestureRef.current) releaseVoice(); };
  const setDraftAt = (next: string, at: number) => { setText(next); setCaret(at); nextFrame(() => setInputFocused(true)); };
  const editDraft = (previous: string, next: string, at: number) => {
    const edited = atomicMentionEdit(previous, next, names);
    const completed = completePartnerMention(previous, edited, partner?.name ?? '');
    if (completed) setDraftAt(completed.text, completed.caret);
    else if (edited !== next) setDraftAt(edited, Math.max(0, at + edited.length - next.length));
    else { setText(edited); setCaret(at); }
  };
  const mentionPartner = () => {
    if (!partner || sending || disabled || processing) return;
    if (!expanded) openEditor();
    const existing = mentionRanges(text, names);
    if (existing.length) {
      let next = text; let position = caret < 0 ? text.length : caret;
      for (const mention of existing.reverse()) {
        const end = mention.end + (next[mention.end] === ' ' ? 1 : 0);
        next = next.slice(0, mention.start) + next.slice(end);
        if (position > mention.start) position = position >= end ? position - (end - mention.start) : mention.start;
      }
      setDraftAt(next, position); return;
    }
    const at = caret < 0 ? text.length : Math.min(caret, text.length);
    const before = text.slice(0, at);
    const token = `${before && !/\s$/.test(before) ? ' ' : ''}@${partner.name} `;
    const next = before + token + text.slice(at);
    if (next.length > 2000) { onError('消息太长了，暂时无法添加 @。'); return; }
    setDraftAt(next, at + token.length);
  };
  const send = async () => {
    const draft = text.trim();
    if ((!draft && !files.length) || sending || disabled || sendingRef.current) return;
    sendingRef.current = true; cancelVoice();
    const submittedFiles = files; const submittedVisibility = toPartner ? 'shared' : visibility;
    const pending = onSend(draft, submittedFiles, submittedVisibility);
    setText(''); setFiles([]); setVisibility('shared'); setCaret(-1); setInputFocused(false); void Taro.hideKeyboard().catch(() => {});
    try {
      if (!await pending) { setText((current) => current || draft); setFiles((current) => current.length ? current : submittedFiles); setVisibility(submittedVisibility); setExpanded(true); }
    } catch (error) {
      setText((current) => current || draft); setFiles((current) => current.length ? current : submittedFiles); setVisibility(submittedVisibility); setExpanded(true);
      onError(error instanceof Error ? error.message : '消息还没发出去，草稿帮你留着啦。');
    } finally { sendingRef.current = false; }
  };
  const queryWeather = async () => {
    if (queryingWeather || sending || disabled) return;
    const self = members.find((member) => member.id === 'self');
    const region = self?.region;
    const location = region?.cityCode ? [region.province, region.city === region.province ? '' : region.city, region.district].filter(Boolean).join('') : '';
    setQueryingWeather(true);
    try { await onSend(location ? `贴贴，我这边（${location}）现在天气怎么样呀？` : '贴贴，我这边现在天气怎么样呀？帮我查查吧～', [], visibility); }
    catch (error) { onError(error instanceof Error ? error.message : '天气查询暂未发送，请再试一次。'); }
    finally { setQueryingWeather(false); }
  };
  return <footer className={`composer-area${voiceState !== 'idle' ? ' is-voice-active' : ''}${voiceState === 'cancel' ? ' is-voice-cancel' : ''}${processing ? ' is-processing' : ''}`}>
    <div className="quick-actions">
      <button type="button" aria-label="@TA" aria-pressed={toPartner} title={disabled ? disabledReason : undefined} disabled={sending || disabled || processing || !partner} onClick={mentionPartner}><AtSign size={14} /><span>TA</span></button>
      <button type="button" disabled={sending || disabled || processing} onClick={() => onTool('anniversary')}><CalendarDays size={14} /><span>小纪念</span></button>
      <button type="button" disabled={queryingWeather || sending || disabled} onClick={() => void queryWeather()}><CloudSun size={14} /><span>{queryingWeather ? '查询中' : '查天气'}</span></button>
    </div>
    <div className={`composer-stage${expanded && !processing ? ' is-expanded' : ''}`}>
    <div className="composer-compact" aria-hidden={expanded && !processing || voiceState !== 'idle'}>
      <button type="button" className="composer-compact-text" disabled={sending || disabled || processing} onTouchStart={(event) => compactTouchStart(event.touches[0]?.clientY ?? 0)} onTouchMove={(event) => touchMove(event.touches[0]?.clientY ?? 0)} onTouchEnd={touchEnd} onTouchCancel={cancelVoice} onClick={() => { if (suppressCompactClickRef.current) { suppressCompactClickRef.current = false; return; } openEditor(); }} aria-label={processing ? 'AI正在处理' : disabledReason ?? '发消息或按住说话'}><span>{processing ? 'AI正在处理' : text || (files.length ? `已选 ${files.length} 个附件，点此继续` : disabled && disabledReason || '发消息或按住说话')}</span></button>
      <button type="button" className="attachment-button" aria-label="添加附件或图片" title="添加附件或图片" disabled={sending || disabled || processing} onClick={() => { void selectFiles(); }}><Paperclip size={19} strokeWidth={2} style={processingIconStyle} /></button>
      <button type="button" className="voice-button" aria-label="语音转写尚未启用，按住可查看提示，上移取消" title="语音转写尚未启用" disabled={sending || disabled || processing} onTouchStart={(event) => startVoice(event.touches[0]?.clientY ?? 0)} onTouchMove={(event) => touchMove(event.touches[0]?.clientY ?? 0)} onTouchEnd={releaseVoice} onTouchCancel={cancelVoice} ><Mic size={19} strokeWidth={2} aria-hidden="true" style={processingIconStyle} /></button>
      {processing ? <button type="button" className="send-button stop-button" aria-label="停止AI处理" aria-busy={stopping} disabled={stopping} onClick={() => void onStop().catch((error) => onError(error instanceof Error ? error.message : '停止失败，请再试一次。'))}>{stopping ? <span className="spinner" aria-hidden="true" /> : <Square size={15} fill="currentColor" strokeWidth={2} aria-hidden="true" />}</button>
        : <button type="button" className={`send-button${!hasContent || sending || disabled ? ' is-disabled' : ''}`} aria-label="发送消息" disabled={(!text.trim() && !files.length) || sending || disabled} onClick={() => void send()}>{sending ? <span className="spinner" /> : <ArrowUp size={22} strokeWidth={2.2} />}</button>}
    </div>
    <div className="composer-expanded-shell" aria-hidden={!expanded || processing || voiceState !== 'idle'}>
    <div className="composer-expanded-content">
    {files.length > 0 && <div className="composer-attachments" aria-label="待发送附件">{files.map((file, index) => <div className="attachment-chip" key={`${file.name}-${index}`}>
      {isImageAttachment(file) ? <Image className="h5-img" src={file.path} mode="aspectFill" style={{ width: '27px', height: '27px' }} /> : /\.(xlsx|xls|xlsm|xlsb)$/i.test(file.name) ? <FileSpreadsheet size={16} /> : <FileText size={16} />}
      <span title={attachmentDisplayName(file.name)}>{attachmentDisplayName(file.name)}</span>
      <button type="button" aria-label={`移除 ${file.name}`} disabled={sending} onClick={() => setFiles((current) => current.filter((_, at) => at !== index))}><X size={13} /></button>
    </div>)}</div>}
    <div className={`composer${inputFocused ? ' is-focused' : ''}`}>
      <div className="composer-input-wrap">
      <Textarea className="h5-textarea composer-textarea" aria-label="聊天消息" placeholder={process.env.TARO_ENV === 'weapp' ? '' : (processing ? 'AI正在处理' : placeholder)}
        autoHeight maxlength={2000} value={text} focus={inputFocused} cursor={caret} fixed adjustPosition={false}
        holdKeyboard showConfirmBar={false} disableDefaultPadding
        disabled={sending || disabled || processing} style={{ color: '#303641', minHeight: '44px', maxHeight: '188px' }}
        onFocus={() => setInputFocused(true)} onBlur={(event) => { setInputFocused(false); setCaret(event.detail.cursor); }}
        onInput={(event) => { editDraft(text, event.detail.value, event.detail.cursor); }} />
      {process.env.TARO_ENV === 'weapp' && !text && <span className="composer-placeholder" aria-hidden="true">{processing ? 'AI正在处理' : placeholder}</span>}
      </div>
      <div className="composer-toolbar">
      <div className="composer-private-control">
        <button type="button" className={`composer-private-button${!toPartner && visibility === 'private' ? ' is-selected' : ''}${privateControlDisabled ? ' is-disabled' : ''}`} disabled={privateControlDisabled} aria-pressed={!toPartner && visibility === 'private'} onClick={() => setVisibility((current) => current === 'private' ? 'shared' : 'private')}><EyeOff size={13} style={privateControlDisabled ? { color: '#7b879a', backgroundColor: '#7b879a' } : undefined} /><span>消息TA不可见</span></button>
      </div>
      <button type="button" className="attachment-button" aria-label="添加附件或图片" title="添加附件或图片" disabled={sending || disabled || processing} onClick={() => void selectFiles()}><Paperclip size={19} strokeWidth={2} /></button>
      {processing ? <button type="button" className="send-button stop-button" aria-label="停止AI处理" aria-busy={stopping} disabled={stopping} onClick={() => void onStop().catch((error) => onError(error instanceof Error ? error.message : '停止失败，请再试一次。'))}>{stopping ? <span className="spinner" aria-hidden="true" /> : <Square size={15} fill="currentColor" strokeWidth={2} aria-hidden="true" />}</button>
        : <button type="button" className={`send-button${!hasContent || sending || disabled ? ' is-disabled' : ''}`} aria-label="发送消息" aria-disabled={!hasContent || sending || disabled} disabled={sending || disabled} onClick={() => { if (hasContent) void send(); }}>{sending ? <span className="spinner" /> : <ArrowUp size={22} strokeWidth={2.2} />}</button>}
      </div>
    </div>
    </div>
    </div>
    </div>
    <div className="composer-voice-panel" role="status" aria-live="polite" aria-hidden={voiceState === 'idle'}>
      <div className="composer-voice-hint"><span>{voiceState === 'cancel' ? '松手取消' : '麦克风尚未接入'}</span><span>{voiceState === 'cancel' ? '下移继续' : '松手后可点击输入文字'}</span></div>
      <div className="composer-voice-field">
        <div className={`composer-voice-wave${voiceActive && voiceState === 'listening' ? ' is-speaking' : ''}`} aria-hidden="true"><i /><i /><i /><i /><i /><i /><i /><i /><i /></div>
      </div>
    </div>
    {!toPartner && <p className="composer-caption"><span>✧</span> 默认和贴贴聊，输入 @ 直接告诉对方 <span>✧</span></p>}
  </footer>;
}
