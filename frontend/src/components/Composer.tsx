import { ArrowUp, AtSign, Bell, CalendarDays, ChevronDown, CloudSun, EyeOff, FileSpreadsheet, FileText, Mic, Paperclip, X } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react';
import { MentionText } from './MentionText';
import type { Member } from '../types';
import { atomicMentionEdit, completePartnerMention, deleteMention, expandMentionSelection, mentionRanges } from '../lib/mentions';
import { isImageAttachment, validateAttachments } from '../api/qoder';
import { attachmentDisplayName } from '../lib/attachments';

interface Props { members: Member[]; sending: boolean; disabled?: boolean; placeholder?: string; onSend: (text: string, files: File[], visibility: 'shared' | 'private') => Promise<boolean>; onTool: (tool: 'reminders' | 'anniversary') => void; onError: (text: string) => void; onExpandedChange?: (expanded: boolean) => void }

interface SpeechResult { results: ArrayLike<ArrayLike<{ transcript: string }>> }
interface SpeechFailure { error: string }
interface SpeechRecognitionLike {
  lang: string
  continuous: boolean
  interimResults: boolean
  onstart: (() => void) | null
  onresult: ((event: SpeechResult) => void) | null
  onerror: ((event: SpeechFailure) => void) | null
  onend: (() => void) | null
  onspeechstart: (() => void) | null
  onspeechend: (() => void) | null
  start(): void
  stop(): void
  abort(): void
}
type SpeechWindow = Window & { SpeechRecognition?: new () => SpeechRecognitionLike; webkitSpeechRecognition?: new () => SpeechRecognitionLike }
interface VoiceGesture {
  pointerId: number | null
  startY: number
  cancelled: boolean
  released: boolean
  completed: boolean
  transcript: string
  timer?: ReturnType<typeof setTimeout>
  cleanup?: () => void
  finish: () => void
}

export function Composer({ members, sending, disabled = false, placeholder = '说点什么，让我们更近一点…', onSend, onTool, onError, onExpandedChange }: Props) {
  const [text, setText] = useState('');
  const [visibility, setVisibility] = useState<'shared' | 'private'>('shared');
  const [expanded, setExpanded] = useState(false);
  const [voiceState, setVoiceState] = useState<'idle' | 'listening' | 'cancel' | 'processing'>('idle');
  const [voiceActive, setVoiceActive] = useState(false);
  const [voiceText, setVoiceText] = useState('');
  const partner = members.find((m) => m.id === 'partner' && m.userId && m.name !== '另一位成员');
  const names = partner ? [partner.name] : [];
  const toPartner = mentionRanges(text, names).length > 0;
  const [files, setFiles] = useState<File[]>([]);
  const [previews, setPreviews] = useState<string[]>([]);
  const [queryingWeather, setQueryingWeather] = useState(false);
  const mirrorRef = useRef<HTMLDivElement>(null);
  const compositionRef = useRef<string | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const speechRef = useRef<SpeechRecognitionLike | null>(null);
  const gestureRef = useRef<VoiceGesture | null>(null);
  useLayoutEffect(() => { onExpandedChange?.(expanded); }, [expanded, onExpandedChange]);
  useEffect(() => () => onExpandedChange?.(false), [onExpandedChange]);
  useLayoutEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    const resize = () => {
      input.style.height = '0px';
      input.style.height = `${Math.min(input.scrollHeight, 188)}px`;
      if (mirrorRef.current) mirrorRef.current.scrollTop = input.scrollTop;
    };
    resize();
    let width = input.clientWidth;
    const observer = new ResizeObserver(() => {
      if (input.clientWidth !== width) { width = input.clientWidth; resize(); }
    });
    observer.observe(input);
    return () => observer.disconnect();
  }, [text]);
  useEffect(() => () => {
    if (gestureRef.current?.timer) clearTimeout(gestureRef.current.timer);
    gestureRef.current = null;
    speechRef.current?.abort(); speechRef.current = null;
  }, []);
  useEffect(() => {
    const urls = files.map((file) => isImageAttachment(file) ? URL.createObjectURL(file) : '');
    setPreviews(urls);
    return () => urls.forEach((url) => { if (url) URL.revokeObjectURL(url); });
  }, [files]);
  const addFiles = (selected: File[]) => {
    try { const next = [...files, ...selected]; validateAttachments(next); setFiles(next); }
    catch (error) { onError(error instanceof Error ? error.message : '无法添加附件。'); }
  };
  const openEditor = () => {
    if (sending) return;
    setExpanded(true);
    requestAnimationFrame(() => inputRef.current?.focus({ preventScroll: true }));
  };
  const closeEditor = () => {
    inputRef.current?.blur();
    setExpanded(false);
  };
  const resetVoice = () => {
    setVoiceState('idle');
    setVoiceActive(false);
    setVoiceText('');
  };
  const cancelVoice = () => {
    const gesture = gestureRef.current;
    if (!gesture) return;
    gesture.cancelled = true;
    gesture.completed = true;
    if (gesture.timer) clearTimeout(gesture.timer);
    gesture.cleanup?.();
    gestureRef.current = null;
    speechRef.current?.abort(); speechRef.current = null;
    resetVoice();
  };
  const startVoice = (startY = 0, pointerId: number | null = null) => {
    if (sending || disabled || gestureRef.current) return;
    inputRef.current?.blur();
    const speechWindow = window as SpeechWindow;
    const Recognition = speechWindow.SpeechRecognition ?? speechWindow.webkitSpeechRecognition;
    if (!Recognition) { onError('当前浏览器不支持语音识别，请点击输入框打字。'); return; }
    const recognition = new Recognition();
    const gesture: VoiceGesture = {
      pointerId, startY, cancelled: false, released: false, completed: false, transcript: '',
      finish: () => {
        if (gesture.completed) return;
        gesture.completed = true;
        if (gesture.timer) clearTimeout(gesture.timer);
        gesture.cleanup?.();
        if (gestureRef.current === gesture) gestureRef.current = null;
        speechRef.current = null;
        resetVoice();
        const spoken = gesture.transcript.trim();
        if (!spoken) { onError('没听清说话内容，请按住再试一次。'); return; }
        void onSend(spoken, [], visibility).then((sent) => {
          if (!sent) { setText((current) => current || spoken); setExpanded(true); }
        }).catch((error) => {
          setText((current) => current || spoken); setExpanded(true);
          onError(error instanceof Error ? error.message : '语音消息没发出去，文字已放回输入框。');
        });
      },
    };
    gestureRef.current = gesture;
    if (pointerId !== null) {
      const move = (event: PointerEvent) => {
        if (event.pointerId !== pointerId || gesture.released) return;
        gesture.cancelled = event.clientY < gesture.startY - 65;
        setVoiceState(gesture.cancelled ? 'cancel' : 'listening');
      };
      const up = (event: PointerEvent) => { if (event.pointerId === pointerId) releaseVoice(); };
      const interrupted = () => cancelVoice();
      window.addEventListener('pointermove', move);
      window.addEventListener('pointerup', up);
      window.addEventListener('pointercancel', interrupted);
      window.addEventListener('blur', interrupted);
      gesture.cleanup = () => {
        window.removeEventListener('pointermove', move);
        window.removeEventListener('pointerup', up);
        window.removeEventListener('pointercancel', interrupted);
        window.removeEventListener('blur', interrupted);
      };
    }
    speechRef.current = recognition;
    recognition.lang = 'zh-CN';
    recognition.continuous = true;
    recognition.interimResults = true;
    recognition.onstart = () => setVoiceState('listening');
    recognition.onspeechstart = () => setVoiceActive(true);
    recognition.onspeechend = () => setVoiceActive(false);
    recognition.onresult = (event) => {
      const spoken = Array.from(event.results).map((result) => result[0]?.transcript ?? '').join('').trim().slice(0, 2000);
      gesture.transcript = spoken;
      setVoiceText(spoken);
      if (spoken) setVoiceActive(true);
    };
    recognition.onerror = (event) => {
      if (gesture.cancelled || gesture.completed) return;
      if (event.error === 'not-allowed' || event.error === 'service-not-allowed') {
        cancelVoice(); onError('请允许浏览器使用麦克风后再试。');
      } else if (event.error !== 'no-speech' && event.error !== 'aborted') {
        cancelVoice(); onError('语音识别暂时不可用，请点击输入框打字。');
      }
    };
    recognition.onend = () => {
      if (speechRef.current === recognition) speechRef.current = null;
      setVoiceActive(false);
      if (gesture.released && !gesture.cancelled) gesture.finish();
    };
    setVoiceState('listening');
    setVoiceText('');
    try { recognition.start(); }
    catch { cancelVoice(); onError('无法启动语音识别，请重试。'); }
  };
  const releaseVoice = () => {
    const gesture = gestureRef.current;
    if (!gesture || gesture.released) return;
    gesture.released = true;
    if (gesture.cancelled) { cancelVoice(); return; }
    setVoiceState('processing');
    if (speechRef.current) {
      try { speechRef.current.stop(); } catch { gesture.finish(); }
      if (!gesture.completed) gesture.timer = setTimeout(gesture.finish, 1400);
    } else gesture.finish();
  };
  const voicePointerDown = (event: ReactPointerEvent<HTMLButtonElement>) => {
    event.preventDefault();
    if (gestureRef.current) return;
    startVoice(event.clientY, event.pointerId);
    if (gestureRef.current) event.currentTarget.setPointerCapture(event.pointerId);
  };
  const setDraftAt = (next: string, at: number) => {
    setText(next);
    requestAnimationFrame(() => {
      const input = inputRef.current;
      if (input?.value === next) { input.focus(); input.setSelectionRange(at, at); }
    });
  };
  const editDraft = (previous: string, next: string, at: number) => {
    const edited = atomicMentionEdit(previous, next, names);
    const completed = completePartnerMention(previous, edited, partner?.name ?? '');
    if (completed) setDraftAt(completed.text, completed.caret);
    else if (edited !== next) setDraftAt(edited, Math.max(0, at + edited.length - next.length));
    else setText(edited);
  };
  const removeMention = (direction: 'backward' | 'forward') => {
    const input = inputRef.current; if (!input) return false;
    const edit = deleteMention(text, input.selectionStart, input.selectionEnd, direction, names);
    if (!edit) return false;
    setDraftAt(edit.text, edit.caret); return true;
  };
  const mentionPartner = () => {
    if (!partner || sending) return;
    if (!expanded) openEditor();
    const existing = mentionRanges(text, names);
    if (existing.length) {
      let next = text;
      let caret = inputRef.current?.selectionStart ?? text.length;
      for (const mention of existing.reverse()) {
        const end = mention.end + (next[mention.end] === ' ' ? 1 : 0);
        next = next.slice(0, mention.start) + next.slice(end);
        if (caret > mention.start) caret = caret >= end ? caret - (end - mention.start) : mention.start;
      }
      setDraftAt(next, caret);
      return;
    }
    const input = inputRef.current;
    const [from, to] = expandMentionSelection(text, input?.selectionStart ?? text.length, input?.selectionEnd ?? text.length, names);
    const before = text.slice(0, from);
    const token = `${before && !/\s$/.test(before) ? ' ' : ''}@${partner.name} `;
    const next = before + token + text.slice(to);
    if (next.length > 2000) { onError('消息太长了，暂时无法添加 @。'); return; }
    setDraftAt(next, from + token.length);
  };
  const send = async () => {
    const draft = text.trim();
    if ((!draft && !files.length) || sending || disabled) return;
    speechRef.current?.abort();
    const submittedFiles = files;
    const submittedVisibility = toPartner ? 'shared' : visibility;
    const pending = onSend(draft, submittedFiles, submittedVisibility);
    setText(''); setFiles([]); setVisibility('shared');
    closeEditor();
    try {
      const success = await pending;
      if (!success) {
        setText((current) => current || draft);
        setFiles((current) => current.length ? current : submittedFiles);
        setVisibility(submittedVisibility);
        setExpanded(true);
      }
    } catch (error) {
      setText((current) => current || draft);
      setFiles((current) => current.length ? current : submittedFiles);
      setVisibility(submittedVisibility);
      setExpanded(true);
      onError(error instanceof Error ? error.message : '消息还没发出去，草稿帮你留着啦。');
    }
  };
  const queryWeather = async () => {
    if (queryingWeather || sending || disabled) return;
    const locationOf = (member?: Member) => member?.region?.cityCode
      ? [member.region.province, member.region.city === member.region.province ? '' : member.region.city, member.region.district].filter(Boolean).join('')
      : '';
    const selfLocation = locationOf(members.find((member) => member.id === 'self'));
    const question = selfLocation
      ? `贴贴，我这边（${selfLocation}）现在天气怎么样呀？`
      : '贴贴，我这边现在天气怎么样呀？帮我查查吧～';
    setQueryingWeather(true);
    try { await onSend(question, [], visibility); }
    catch (error) { onError(error instanceof Error ? error.message : '天气查询暂未发送，请再试一次。'); }
    finally { setQueryingWeather(false); }
  };
  return <footer className={`composer-area${voiceState !== 'idle' ? ' is-voice-active' : ''}${voiceState === 'cancel' ? ' is-voice-cancel' : ''}`}>
    <div className="quick-actions">
      <button type="button" aria-label="@TA" aria-pressed={toPartner} disabled={sending || !partner} onMouseDown={(event) => event.preventDefault()} onClick={mentionPartner}><AtSign size={14} /><span>TA</span></button>
      <button onClick={() => onTool('reminders')}><Bell size={14} /><span>添加提醒</span></button>
      <button onClick={() => onTool('anniversary')}><CalendarDays size={14} /><span>小纪念</span></button>
      <button type="button" disabled={queryingWeather || sending || disabled} onClick={() => void queryWeather()}><CloudSun size={14} /><span>{queryingWeather ? '查询中' : '查天气'}</span></button>
    </div>
    <div className={`composer-stage${expanded ? ' is-expanded' : ''}`}>
    <div className="composer-compact" aria-hidden={expanded || voiceState !== 'idle'}>
      <button type="button" className="composer-compact-text" disabled={sending} onClick={openEditor} aria-label="发消息，打开输入框"><span>{text || (files.length ? `已选 ${files.length} 个附件，点此继续` : '发消息…')}</span></button>
      <button type="button" className="composer-compact-voice" aria-label="按住说话，松手发送，上移取消" disabled={sending || disabled} onPointerDown={voicePointerDown} onContextMenu={(event) => event.preventDefault()} onClick={(event) => { if (event.detail === 0) gestureRef.current ? releaseVoice() : startVoice(); }}><Mic size={18} /><span>按住说话</span></button>
    </div>
    <div className="composer-expanded-shell" aria-hidden={!expanded || voiceState !== 'idle'}>
    {files.length > 0 && <div className="composer-attachments" aria-label="待发送附件">{files.map((file, index) => <div className="attachment-chip" key={`${file.name}-${index}`}>
      {previews[index] ? <img src={previews[index]} alt="" /> : /\.(xlsx|xls|xlsm|xlsb)$/i.test(file.name) ? <FileSpreadsheet size={16} /> : <FileText size={16} />}
      <span title={attachmentDisplayName(file.name)}>{attachmentDisplayName(file.name)}</span>
      <button type="button" aria-label={`移除 ${file.name}`} disabled={sending} onClick={() => setFiles((current) => current.filter((_, at) => at !== index))}><X size={13} /></button>
    </div>)}</div>}
    <form className="composer" onSubmit={(event) => { event.preventDefault(); void send(); }}>
      <input ref={fileRef} type="file" className="visually-hidden" aria-label="选择文件或图片" multiple onChange={(event) => { addFiles(Array.from(event.target.files ?? [])); event.target.value = ''; }} />
      <div className="composer-input-wrap">
      <div ref={mirrorRef} className="composer-input-mirror" aria-hidden="true"><MentionText text={text + '\u200b'} names={names} className="composer-mention-token" /></div>
      <textarea ref={inputRef} aria-label="聊天消息" placeholder={placeholder} rows={1} enterKeyHint="enter" maxLength={2000} value={text} disabled={sending} onBlur={() => { requestAnimationFrame(() => { if (!text && !files.length && !document.activeElement?.closest('.composer-expanded-shell')) setExpanded(false); }); }} onChange={(event) => { if (compositionRef.current !== null) setText(event.target.value); else editDraft(text, event.target.value, event.target.selectionStart); }} onCompositionStart={() => { compositionRef.current = text; }} onCompositionEnd={(event) => { const previous = compositionRef.current ?? text; compositionRef.current = null; editDraft(previous, event.currentTarget.value, event.currentTarget.selectionStart); }} onScroll={(event) => { if (mirrorRef.current) mirrorRef.current.scrollTop = event.currentTarget.scrollTop; }} onBeforeInput={(event) => {
        const kind = (event.nativeEvent as InputEvent).inputType;
        if (kind === 'deleteContentBackward' && removeMention('backward') || kind === 'deleteContentForward' && removeMention('forward')) event.preventDefault();
      }} onKeyDown={(event) => {
        if (event.nativeEvent.isComposing) return;
        if (event.key === 'Backspace' && removeMention('backward') || event.key === 'Delete' && removeMention('forward')) { event.preventDefault(); return; }
        // 桌面 Enter 发送、Shift + Enter 换行；触屏键盘 Enter 换行。
        if (event.key === 'Enter' && !event.shiftKey && window.matchMedia('(hover: hover) and (pointer: fine)').matches) { event.preventDefault(); void send(); }
      }} onPaste={(event) => { const pasted = Array.from(event.clipboardData.files); if (pasted.length) { event.preventDefault(); addFiles(pasted); } }} />
      </div>
      <div className="composer-toolbar">
      <div className="composer-private-control">
        <button type="button" className={`composer-private-button ${!toPartner && visibility === 'private' ? 'is-selected' : ''}`} disabled={sending || toPartner} aria-pressed={!toPartner && visibility === 'private'} onClick={() => setVisibility((current) => current === 'private' ? 'shared' : 'private')}><EyeOff size={13} /><span>消息TA不可见</span></button>
      </div>
      <button type="button" className="composer-collapse" aria-label="收起输入框" onClick={closeEditor}><ChevronDown size={18} /></button>
      <div className="voice-control">
        <button type="button" className="voice-button" aria-label="按住说话，松手发送，上移取消" title="按住说话" disabled={sending || disabled} onPointerDown={voicePointerDown} onContextMenu={(event) => event.preventDefault()} onClick={(event) => { if (event.detail === 0) gestureRef.current ? releaseVoice() : startVoice(); }}><Mic size={19} strokeWidth={2} aria-hidden="true" /></button>
      </div>
      <button type="button" className="attachment-button" aria-label="添加附件或图片" title="添加附件或图片" disabled={sending} onClick={() => fileRef.current?.click()}><Paperclip size={19} strokeWidth={2} /></button>
      <button type="submit" className="send-button" aria-label="发送消息" disabled={(!text.trim() && !files.length) || sending || disabled}>{sending ? <span className="spinner" /> : <ArrowUp size={22} strokeWidth={2.2} />}</button>
      </div>
    </form>
    </div>
    </div>
    <div className="composer-voice-panel" role="status" aria-live="polite" aria-hidden={voiceState === 'idle'}>
      <div className={`composer-voice-wave${voiceActive && voiceState === 'listening' ? ' is-speaking' : ''}`} aria-hidden="true"><i /><i /><i /><i /><i /></div>
      <div className="composer-voice-copy"><strong>{voiceState === 'cancel' ? '松手取消' : voiceState === 'processing' ? '正在识别…' : voiceText || '正在听你说…'}</strong><span>{voiceState === 'cancel' ? '下移继续说话' : voiceState === 'processing' ? '识别完成后自动发送' : '松手发送 · 上移取消'}</span></div>
      {gestureRef.current?.pointerId === null && voiceState === 'listening' && <div className="composer-voice-key-actions"><button type="button" onClick={cancelVoice}>取消</button><button type="button" onClick={releaseVoice}>发送</button></div>}
    </div>
    {!toPartner && <p className="composer-caption"><span>✧</span> 默认和贴贴聊，输入 @ 直接告诉对方 <span>✧</span></p>}
  </footer>;
}
