import { ArrowUp, AtSign, Bell, FileSpreadsheet, FileText, Heart, LockKeyhole, Mic, Paperclip, X } from 'lucide-react';
import { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { MentionText } from './MentionText';
import type { Member } from '../types';
import { atomicMentionEdit, completePartnerMention, deleteMention, expandMentionSelection, mentionRanges } from '../lib/mentions';
import { isImageAttachment, validateAttachments } from '../api/qoder';

interface Props { members: Member[]; sending: boolean; disabled?: boolean; placeholder?: string; onSend: (text: string, files: File[], visibility: 'shared' | 'private') => Promise<boolean>; onTool: (tool: 'reminders' | 'anniversary') => void; onError: (text: string) => void }

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
  start(): void
  stop(): void
  abort(): void
}
type SpeechWindow = Window & { SpeechRecognition?: new () => SpeechRecognitionLike; webkitSpeechRecognition?: new () => SpeechRecognitionLike }

export function Composer({ members, sending, disabled = false, placeholder = '说点什么，让我们更近一点…', onSend, onTool, onError }: Props) {
  const [text, setText] = useState('');
  const [visibility, setVisibility] = useState<'shared' | 'private'>('shared');
  const privacyHintId = useId();
  const partner = members.find((m) => m.id === 'partner' && m.userId && m.name !== '另一位成员');
  const names = partner ? [partner.name] : [];
  const toPartner = mentionRanges(text, names).length > 0;
  const [files, setFiles] = useState<File[]>([]);
  const [previews, setPreviews] = useState<string[]>([]);
  const [listening, setListening] = useState(false);
  const mirrorRef = useRef<HTMLDivElement>(null);
  const compositionRef = useRef<string | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const speechRef = useRef<SpeechRecognitionLike | null>(null);
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
  useEffect(() => () => { speechRef.current?.abort(); speechRef.current = null; }, []);
  useEffect(() => {
    const urls = files.map((file) => isImageAttachment(file) ? URL.createObjectURL(file) : '');
    setPreviews(urls);
    return () => urls.forEach((url) => { if (url) URL.revokeObjectURL(url); });
  }, [files]);
  const addFiles = (selected: File[]) => {
    try { const next = [...files, ...selected]; validateAttachments(next); setFiles(next); }
    catch (error) { onError(error instanceof Error ? error.message : '无法添加附件。'); }
  };
  const toggleVoice = () => {
    if (speechRef.current) { speechRef.current.stop(); return; }
    const speechWindow = window as SpeechWindow;
    const Recognition = speechWindow.SpeechRecognition ?? speechWindow.webkitSpeechRecognition;
    if (!Recognition) { onError('当前浏览器不支持语音转文字，请使用键盘输入。'); return; }
    const recognition = new Recognition();
    speechRef.current = recognition;
    recognition.lang = 'zh-CN';
    recognition.continuous = false;
    recognition.interimResults = false;
    recognition.onstart = () => setListening(true);
    recognition.onresult = (event) => {
      const spoken = event.results[0]?.[0]?.transcript?.trim();
      if (spoken) {
        setText((current) => `${current}${current && !/\s$/.test(current) ? ' ' : ''}${spoken}`.slice(0, 2000));
        inputRef.current?.focus();
      }
    };
    recognition.onerror = (event) => {
      if (event.error === 'not-allowed' || event.error === 'service-not-allowed') onError('请允许浏览器使用麦克风后再试。');
      else if (event.error !== 'no-speech' && event.error !== 'aborted') onError('语音识别暂时不可用，请重试或直接输入文字。');
    };
    recognition.onend = () => { speechRef.current = null; setListening(false); };
    try { recognition.start(); }
    catch { speechRef.current = null; setListening(false); onError('无法启动语音识别，请重试。'); }
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
    try {
      if (await onSend(draft, files, toPartner ? 'shared' : visibility)) { setText(''); setFiles([]); setVisibility('shared'); }
    } catch (error) { onError(error instanceof Error ? error.message : '消息还没发出去，草稿帮你留着啦。'); }
  };
  return <footer className="composer-area">
    <div className="quick-actions">
      <button type="button" aria-label="@TA" aria-pressed={toPartner} disabled={sending || !partner} onMouseDown={(event) => event.preventDefault()} onClick={mentionPartner}><AtSign size={14} /><span>TA</span></button>
      <button onClick={() => onTool('reminders')}><Bell size={14} /><span>添加提醒</span></button>
      <button onClick={() => onTool('anniversary')}><Heart size={14} /><span>小纪念</span></button>
    </div>
    {files.length > 0 && <div className="composer-attachments" aria-label="待发送附件">{files.map((file, index) => <div className="attachment-chip" key={`${file.name}-${index}`}>
      {previews[index] ? <img src={previews[index]} alt="" /> : /\.(xlsx|xls|xlsm|xlsb)$/i.test(file.name) ? <FileSpreadsheet size={16} /> : <FileText size={16} />}
      <span title={file.name}>{file.name}</span>
      <button type="button" aria-label={`移除 ${file.name}`} disabled={sending} onClick={() => setFiles((current) => current.filter((_, at) => at !== index))}><X size={13} /></button>
    </div>)}</div>}
    <form className="composer" onSubmit={(event) => { event.preventDefault(); void send(); }}>
      <input ref={fileRef} type="file" className="visually-hidden" aria-label="选择文件或图片" multiple onChange={(event) => { addFiles(Array.from(event.target.files ?? [])); event.target.value = ''; }} />
      <div className="composer-input-wrap">
      <div ref={mirrorRef} className="composer-input-mirror" aria-hidden="true"><MentionText text={text + '\u200b'} names={names} className="composer-mention-token" /></div>
      <textarea ref={inputRef} aria-label="聊天消息" placeholder={placeholder} rows={1} enterKeyHint="enter" maxLength={2000} value={text} disabled={sending} onChange={(event) => { if (compositionRef.current !== null) setText(event.target.value); else editDraft(text, event.target.value, event.target.selectionStart); }} onCompositionStart={() => { compositionRef.current = text; }} onCompositionEnd={(event) => { const previous = compositionRef.current ?? text; compositionRef.current = null; editDraft(previous, event.currentTarget.value, event.currentTarget.selectionStart); }} onScroll={(event) => { if (mirrorRef.current) mirrorRef.current.scrollTop = event.currentTarget.scrollTop; }} onBeforeInput={(event) => {
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
      <button type="button" className="attachment-button" aria-label="添加附件或图片" title="添加附件或图片" disabled={sending} onClick={() => fileRef.current?.click()}><Paperclip size={19} strokeWidth={2} /></button>
      <div className="composer-private-control">
        <button type="button" className={`composer-private-button ${!toPartner && visibility === 'private' ? 'is-selected' : ''}`} disabled={sending || toPartner} aria-pressed={!toPartner && visibility === 'private'} aria-describedby={privacyHintId} onClick={() => setVisibility((current) => current === 'private' ? 'shared' : 'private')}><LockKeyhole size={13} /><span>仅自己可见</span></button>
        <span id={privacyHintId} role="tooltip" className="composer-private-tooltip">{toPartner ? '发给对方的消息双方可见。' : '消息仅自己和 AI 可见，提醒仍会提醒双方。'}</span>
      </div>
      <button type="button" className={`voice-button ${listening ? 'is-listening' : ''}`} aria-label={listening ? '停止语音输入' : '语音输入'} aria-pressed={listening} title={listening ? '停止语音输入' : '语音转文字'} disabled={sending} onClick={toggleVoice}><Mic size={19} strokeWidth={2} /></button>
      <button type="submit" className="send-button" aria-label="发送消息" disabled={(!text.trim() && !files.length) || sending || disabled}>{sending ? <span className="spinner" /> : <ArrowUp size={22} strokeWidth={2.2} />}</button>
      </div>
    </form>
    {listening && <div className="voice-status" role="status"><span />正在听你说话，点麦克风结束</div>}
    {!toPartner && <p className="composer-caption"><span>✧</span> 默认和贴贴聊，输入 @ 直接告诉对方 <span>✧</span></p>}
  </footer>;
}
