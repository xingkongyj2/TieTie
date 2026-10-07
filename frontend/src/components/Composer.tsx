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
import { useVoiceRecognition } from '../hooks/useVoiceRecognition';
import { appendVoiceDraft } from '../lib/voiceDraft';

type VoiceTouch = { identifier: number; clientY: number };

interface Props { members: Member[]; sending: boolean; processing: boolean; stopping: boolean; disabled?: boolean; disabledReason?: string; placeholder?: string; dismissSignal?: number; onSend: (text: string, files: MiniFile[], visibility: 'shared' | 'private') => Promise<boolean>; onStop: () => Promise<void>; onTool: (tool: 'reminders' | 'anniversary') => void; onError: (text: string) => void }

/** Native input and touch events keep the existing composer and transition classes. */
export function Composer({ members, sending, processing, stopping, disabled = false, disabledReason, placeholder = '说点什么，让我们更近一点…', dismissSignal, onSend, onStop, onTool, onError }: Props) {
  const [text, setText] = useState('');
  const [visibility, setVisibility] = useState<'shared' | 'private'>('shared');
  const [expanded, setExpanded] = useState(false);
  const [voiceOverflow, setVoiceOverflow] = useState('');
  const [inputFocused, setInputFocused] = useState(false);
  const [caret, setCaret] = useState(-1);
  const [files, setFiles] = useState<MiniFile[]>([]);
  const [queryingWeather, setQueryingWeather] = useState(false);
  const partner = members.find((member) => member.id === 'partner' && member.userId && member.name !== '另一位成员');
  const names = partner ? [partner.name] : [];
  const toPartner = mentionRanges(text, names).length > 0;
  const inputDisabled = sending || disabled || processing;
  const hasContent = !!text.trim() || files.length > 0;
  // WeChat applies a native disabled color to the button. These icons are
  // CSS-mask views, so paint the mask itself in every disabled state.
  const disabledIconStyle = inputDisabled ? { color: '#7b879a', backgroundColor: '#7b879a' } : undefined;
  const focusTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const holdTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const suppressCompactClickRef = useRef(false);
  const gestureRef = useRef<{ startY: number; touchId: number; cancelled: boolean } | null>(null);
  const selectingRef = useRef(false);
  const sendingRef = useRef(false);
  const voice = useVoiceRecognition(!inputDisabled, (result) => {
    gestureRef.current = null;
    const next = appendVoiceDraft(text, result);
    setText(next.text); setCaret(next.text.length); setExpanded(true); setInputFocused(false);
    setVoiceOverflow(next.overflow);
  }, onError);
  const voiceActive = voice.phase !== 'idle';
  const privateControlDisabled = inputDisabled || voiceActive || toPartner;
  const voiceCancelling = voiceActive && voice.cancelIntent;
  const voiceSeconds = Math.min(60, Math.floor(voice.elapsedMs / 1000));
  const clearHold = () => { if (holdTimerRef.current) clearTimeout(holdTimerRef.current); holdTimerRef.current = null; };
  const cancelVoice = () => { clearHold(); gestureRef.current = null; voice.controller.cancel(); };
  useEffect(() => {
    const stopVisibility = onAppVisibilityChange((visible) => { if (!visible) { cancelVoice(); setInputFocused(false); } });
    return () => { stopVisibility(); if (focusTimerRef.current) clearTimeout(focusTimerRef.current); clearHold(); voice.controller.cancel(); };
  }, []);
  useEffect(() => { if (dismissSignal !== undefined) { cancelVoice(); setInputFocused(false); setExpanded(false); void Taro.hideKeyboard().catch(() => {}); } }, [dismissSignal]);
  useEffect(() => { if (inputDisabled) cancelVoice(); }, [inputDisabled]);
  useEffect(() => { if (voice.phase === 'idle' && !holdTimerRef.current) gestureRef.current = null; }, [voice.phase]);
  useEffect(() => {
    if (voice.phase === 'listening' && !voice.cancelIntent) void Taro.vibrateShort({ type: 'light' }).catch(() => {});
  }, [voice.phase, voice.cancelIntent]);
  useEffect(() => { if (processing) { setInputFocused(false); setExpanded(false); } }, [processing]);
  const openEditor = (focusInput = true) => {
    if (sending || disabled || processing || voiceActive) return;
    setExpanded(true);
    if (focusTimerRef.current) clearTimeout(focusTimerRef.current);
    if (focusInput) focusTimerRef.current = setTimeout(() => { focusTimerRef.current = null; setInputFocused(true); }, 110);
  };
  const selectFiles = async () => {
    if (selectingRef.current || sending || disabled || processing || voiceActive) return;
    if (files.length >= 4) { onError('一次最多添加 4 个附件。'); return; }
    selectingRef.current = true;
    setInputFocused(false);
    try {
      const selected = await chooseAttachments(4 - files.length);
      if (selected.length) { const next = [...files, ...selected]; validateAttachments(next); setFiles(next); setExpanded(true); }
    } catch (error) { onError(error instanceof Error ? error.message : '无法添加附件。'); }
    finally { selectingRef.current = false; }
  };
  const startVoice = (touch: VoiceTouch | undefined) => {
    if (!touch || inputDisabled || gestureRef.current || voice.controller.getSnapshot().phase !== 'idle') return;
    if (voiceOverflow) { onError('上一段识别文字已保留，请先复制或处理后再录音。'); return; }
    if (text.length >= 2000) { onError('草稿已达到 2000 字，请先发送或删减后再说。'); return; }
    clearHold();
    if (focusTimerRef.current) { clearTimeout(focusTimerRef.current); focusTimerRef.current = null; }
    setInputFocused(false); void Taro.hideKeyboard().catch(() => {});
    gestureRef.current = { startY: touch.clientY, touchId: touch.identifier, cancelled: false };
    void voice.controller.start().then(() => {
      if (voice.controller.getSnapshot().phase === 'idle') gestureRef.current = null;
    });
  };
  const releaseVoice = () => {
    const gesture = gestureRef.current;
    clearHold(); gestureRef.current = null;
    if (gesture) voice.controller.finish();
  };
  const compactTouchStart = (touch: VoiceTouch | undefined) => {
    if (!touch || inputDisabled || gestureRef.current || voiceActive) return;
    clearHold(); suppressCompactClickRef.current = false;
    gestureRef.current = { startY: touch.clientY, touchId: touch.identifier, cancelled: false };
    holdTimerRef.current = setTimeout(() => { holdTimerRef.current = null; gestureRef.current = null; suppressCompactClickRef.current = true; startVoice(touch); }, 360);
  };
  const touchMove = (touches: ArrayLike<VoiceTouch>) => {
    const gesture = gestureRef.current; if (!gesture) return;
    const touch = Array.from(touches).find(item => item.identifier === gesture.touchId); if (!touch) return;
    if (holdTimerRef.current) { if (Math.abs(touch.clientY - gesture.startY) > 16) { clearHold(); gestureRef.current = null; suppressCompactClickRef.current = true; } return; }
    const cancelled = touch.clientY < gesture.startY - 65;
    if (cancelled !== gesture.cancelled) {
      gesture.cancelled = cancelled; voice.controller.setCancelIntent(cancelled);
      if (cancelled) void Taro.vibrateShort({ type: 'light' }).catch(() => {});
    }
  };
  const touchEnd = (touches: ArrayLike<VoiceTouch>) => {
    if (!gestureRef.current || !Array.from(touches).some(touch => touch.identifier === gestureRef.current?.touchId)) return;
    if (holdTimerRef.current) { clearHold(); gestureRef.current = null; } else releaseVoice();
  };
  const setDraftAt = (next: string, at: number) => { setText(next); setCaret(at); nextFrame(() => setInputFocused(true)); };
  const editDraft = (previous: string, next: string, at: number) => {
    const edited = atomicMentionEdit(previous, next, names);
    const completed = completePartnerMention(previous, edited, partner?.name ?? '');
    if (completed) setDraftAt(completed.text, completed.caret);
    else if (edited !== next) setDraftAt(edited, Math.max(0, at + edited.length - next.length));
    else { setText(edited); setCaret(at); }
  };
  const mentionPartner = () => {
    if (!partner || sending || disabled || processing || voiceActive) return;
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
    if ((!draft && !files.length) || sending || disabled || processing || voiceActive || sendingRef.current) return;
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
    if (queryingWeather || sending || disabled || voiceActive) return;
    const self = members.find((member) => member.id === 'self');
    const region = self?.region;
    const location = region?.cityCode ? [region.province, region.city === region.province ? '' : region.city, region.district].filter(Boolean).join('') : '';
    setQueryingWeather(true);
    try { await onSend(location ? `贴贴，我这边（${location}）现在天气怎么样呀？` : '贴贴，我这边现在天气怎么样呀？帮我查查吧～', [], visibility); }
    catch (error) { onError(error instanceof Error ? error.message : '天气查询暂未发送，请再试一次。'); }
    finally { setQueryingWeather(false); }
  };
  return <footer className={`composer-area${voiceActive ? ' is-voice-active' : ''}${voiceCancelling ? ' is-voice-cancel' : ''}${processing ? ' is-processing' : ''}${inputDisabled ? ' is-disabled' : ''}`}>
    <div className="quick-actions">
      <button type="button" aria-label="@TA" aria-pressed={toPartner} title={disabled ? disabledReason : undefined} disabled={inputDisabled || voiceActive || !partner} onClick={mentionPartner}><AtSign size={14} /><span>TA</span></button>
      <button type="button" disabled={inputDisabled || voiceActive} onClick={() => onTool('anniversary')}><CalendarDays size={14} /><span>小纪念</span></button>
      <button type="button" disabled={queryingWeather || sending || disabled || voiceActive} onClick={() => void queryWeather()}><CloudSun size={14} /><span>{queryingWeather ? '查询中' : '查天气'}</span></button>
    </div>
    <div className={`composer-stage${expanded && !processing ? ' is-expanded' : ''}`}>
    <div className="composer-compact" aria-hidden={expanded && !processing || voiceActive}>
      <button type="button" className="composer-compact-text" disabled={inputDisabled} onTouchStart={(event) => compactTouchStart(event.touches[0])} onTouchMove={(event) => touchMove(event.touches)} onTouchEnd={(event) => touchEnd(event.changedTouches)} onTouchCancel={cancelVoice} onClick={() => { if (suppressCompactClickRef.current) { suppressCompactClickRef.current = false; return; } openEditor(); }} aria-label={processing ? 'AI正在处理' : disabledReason ?? '发消息或按住说话'}><span style={inputDisabled ? { color: '#7a879a' } : undefined}>{processing ? 'AI正在处理' : text || (files.length ? `已选 ${files.length} 个附件，点此继续` : disabled && disabledReason || '发消息或按住说话')}</span></button>
      <button type="button" className="attachment-button" aria-label="添加附件或图片" title="添加附件或图片" disabled={inputDisabled} onClick={() => { void selectFiles(); }}><Paperclip size={19} strokeWidth={2} style={disabledIconStyle} /></button>
      <button type="button" className="voice-button" aria-label="按住说话转文字，上滑取消，松手后可编辑" title="按住说话转文字" disabled={inputDisabled} onTouchStart={(event) => startVoice(event.touches[0])} onTouchMove={(event) => touchMove(event.touches)} onTouchEnd={(event) => touchEnd(event.changedTouches)} onTouchCancel={cancelVoice}><Mic size={19} strokeWidth={2} aria-hidden="true" style={disabledIconStyle} /></button>
      {processing ? <button type="button" className="send-button stop-button" aria-label="停止AI处理" aria-busy={stopping} disabled={stopping} onClick={() => void onStop().catch((error) => onError(error instanceof Error ? error.message : '停止失败，请再试一次。'))}>{stopping ? <span className="spinner" aria-hidden="true" /> : <Square size={15} fill="currentColor" strokeWidth={2} aria-hidden="true" />}</button>
        : <button type="button" className={`send-button${!hasContent || sending || disabled ? ' is-disabled' : ''}`} aria-label="发送消息" disabled={(!text.trim() && !files.length) || sending || disabled} onClick={() => void send()}>{sending ? <span className="spinner" /> : <ArrowUp size={22} strokeWidth={2.2} />}</button>}
    </div>
    <div className="composer-expanded-shell" aria-hidden={!expanded || processing || voiceActive}>
    <div className="composer-expanded-content">
    {files.length > 0 && <div className="composer-attachments" aria-label="待发送附件">{files.map((file, index) => <div className="attachment-chip" key={`${file.name}-${index}`}>
      {isImageAttachment(file) ? <Image className="h5-img" src={file.path} mode="aspectFill" style={{ width: '27px', height: '27px' }} /> : /\.(xlsx|xls|xlsm|xlsb)$/i.test(file.name) ? <FileSpreadsheet size={16} /> : <FileText size={16} />}
      <span title={attachmentDisplayName(file.name)}>{attachmentDisplayName(file.name)}</span>
      <button type="button" aria-label={`移除 ${file.name}`} disabled={inputDisabled || voiceActive} onClick={() => setFiles((current) => current.filter((_, at) => at !== index))}><X size={13} /></button>
    </div>)}</div>}
    <div className={`composer${inputFocused ? ' is-focused' : ''}`}>
      <div className="composer-input-wrap">
      <Textarea className="h5-textarea composer-textarea" aria-label="聊天消息" placeholder={process.env.TARO_ENV === 'weapp' ? '' : (processing ? 'AI正在处理' : placeholder)}
        autoHeight maxlength={2000} value={text} focus={inputFocused} cursor={caret} fixed adjustPosition={false}
        holdKeyboard showConfirmBar={false} disableDefaultPadding
        disabled={inputDisabled || voiceActive} style={{ color: '#303641', ...(process.env.TARO_ENV === 'h5' ? { WebkitTextFillColor: '#303641' } : {}), visibility: voiceActive ? 'hidden' : 'visible', opacity: 1, minHeight: '44px', maxHeight: '188px' }}
        onFocus={() => setInputFocused(true)} onBlur={(event) => { setInputFocused(false); setCaret(event.detail.cursor); }}
        onInput={(event) => { editDraft(text, event.detail.value, event.detail.cursor); }} />
      {process.env.TARO_ENV === 'weapp' && !text && <span className="composer-placeholder" aria-hidden="true">{processing ? 'AI正在处理' : placeholder}</span>}
      </div>
      <div className="composer-toolbar">
      <div className="composer-private-control">
        <button type="button" className={`composer-private-button${!toPartner && visibility === 'private' ? ' is-selected' : ''}${privateControlDisabled ? ' is-disabled' : ''}`} disabled={privateControlDisabled} aria-pressed={!toPartner && visibility === 'private'} onClick={() => setVisibility((current) => current === 'private' ? 'shared' : 'private')}><EyeOff size={13} style={privateControlDisabled ? { color: '#7b879a', backgroundColor: '#7b879a' } : undefined} /><span>消息TA不可见</span></button>
      </div>
      <button type="button" className="attachment-button" aria-label="添加附件或图片" title="添加附件或图片" disabled={inputDisabled} onClick={() => void selectFiles()}><Paperclip size={19} strokeWidth={2} style={disabledIconStyle} /></button>
      <button type="button" className="voice-button" aria-label="按住继续说话转文字，上滑取消" title="按住继续说话" disabled={inputDisabled} onTouchStart={(event) => startVoice(event.touches[0])} onTouchMove={(event) => touchMove(event.touches)} onTouchEnd={(event) => touchEnd(event.changedTouches)} onTouchCancel={cancelVoice}><Mic size={19} strokeWidth={2} aria-hidden="true" style={disabledIconStyle} /></button>
      {processing ? <button type="button" className="send-button stop-button" aria-label="停止AI处理" aria-busy={stopping} disabled={stopping} onClick={() => void onStop().catch((error) => onError(error instanceof Error ? error.message : '停止失败，请再试一次。'))}>{stopping ? <span className="spinner" aria-hidden="true" /> : <Square size={15} fill="currentColor" strokeWidth={2} aria-hidden="true" />}</button>
        : <button type="button" className={`send-button${!hasContent || sending || disabled ? ' is-disabled' : ''}`} aria-label="发送消息" aria-disabled={!hasContent || sending || disabled} disabled={sending || disabled} onClick={() => { if (hasContent) void send(); }}>{sending ? <span className="spinner" /> : <ArrowUp size={22} strokeWidth={2.2} />}</button>}
      </div>
    </div>
    </div>
    </div>
    </div>
    <div className={`composer-voice-panel${voice.phase === 'processing' ? ' is-transcribing' : ''}`} role="status" aria-live="polite" aria-hidden={!voiceActive}>
      <div className="composer-voice-hint"><span>{voiceCancelling ? '松手取消 · 下移继续' : voice.phase === 'preparing' ? '正在准备麦克风…' : voice.phase === 'processing' ? '正在识别…' : voiceSeconds >= 50 ? `还可说 ${60 - voiceSeconds} 秒 · 上滑取消` : '上滑取消'}</span></div>
      <div className="composer-voice-field">
        {voice.phase === 'preparing' || voice.phase === 'processing' ? <span className="spinner" aria-hidden="true" /> : <div className={`composer-voice-wave${voice.phase === 'listening' && !voiceCancelling ? ' is-speaking' : ''}`} aria-hidden="true"><i /><i /><i /><i /><i /><i /><i /><i /><i /></div>}
      </div>
    </div>
    {!voiceActive && voiceOverflow && <div className="composer-dictation-overflow" role="status"><span>识别文字超出 2000 字限制，原草稿和这段文字都已保留。</span><div className="composer-dictation-preview">{voiceOverflow}</div><div className="composer-dictation-actions"><button type="button" onClick={() => void Taro.setClipboardData({ data: voiceOverflow }).catch(() => onError('复制失败，请再试一次。'))}>复制识别文字</button><button type="button" disabled={inputDisabled} onClick={() => { const next = appendVoiceDraft(text, voiceOverflow); if (next.overflow) { onError('草稿还放不下，请先发送或删减后再添加。'); return; } setText(next.text); setCaret(next.text.length); setVoiceOverflow(''); setExpanded(true); }}>加入草稿</button><button type="button" onClick={() => setVoiceOverflow('')}>丢弃</button></div></div>}
    {!toPartner && <p className="composer-caption"><span>✧</span> 默认和贴贴聊，输入 @ 直接告诉对方 <span>✧</span></p>}
  </footer>;
}
