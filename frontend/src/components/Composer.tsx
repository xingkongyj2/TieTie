import { ArrowUp, Bell, FileSpreadsheet, FileText, Heart, Mic, Paperclip, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { isImageAttachment, validateAttachments } from '../api/qoder';

interface Props { sending: boolean; disabled?: boolean; placeholder?: string; onSend: (text: string, files: File[]) => Promise<boolean>; onTool: (tool: 'reminders' | 'anniversary') => void; onError: (text: string) => void }

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

export function Composer({ sending, disabled = false, placeholder = '说点什么，让我们更近一点…', onSend, onTool, onError }: Props) {
  const [text, setText] = useState('');
  const [files, setFiles] = useState<File[]>([]);
  const [previews, setPreviews] = useState<string[]>([]);
  const [listening, setListening] = useState(false);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const speechRef = useRef<SpeechRecognitionLike | null>(null);
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
  const send = async () => {
    const draft = text.trim();
    if ((!draft && !files.length) || sending || disabled) return;
    speechRef.current?.abort();
    try {
      if (await onSend(draft, files)) { setText(''); setFiles([]); }
    } catch (error) { onError(error instanceof Error ? error.message : '消息还没发出去，草稿帮你留着啦。'); }
  };
  return <footer className="composer-area">
    <div className="quick-actions">
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
      <button type="button" className="attachment-button" aria-label="添加附件或图片" title="添加附件或图片" disabled={sending} onClick={() => fileRef.current?.click()}><Paperclip size={19} strokeWidth={2} /></button>
      <textarea ref={inputRef} aria-label="聊天消息" placeholder={placeholder} rows={1} maxLength={2000} value={text} disabled={sending} onChange={(event) => setText(event.target.value)} onKeyDown={(event) => {
        // 尊重中文输入法；桌面 Enter 发送、Shift + Enter 换行。
        if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) { event.preventDefault(); void send(); }
      }} onPaste={(event) => { const pasted = Array.from(event.clipboardData.files); if (pasted.length) { event.preventDefault(); addFiles(pasted); } }} />
      <button type="button" className={`voice-button ${listening ? 'is-listening' : ''}`} aria-label={listening ? '停止语音输入' : '语音输入'} aria-pressed={listening} title={listening ? '停止语音输入' : '语音转文字'} disabled={sending} onClick={toggleVoice}><Mic size={19} strokeWidth={2} /></button>
      <button type="submit" className="send-button" aria-label="发送消息" disabled={(!text.trim() && !files.length) || sending || disabled}>{sending ? <span className="spinner" /> : <ArrowUp size={22} strokeWidth={2.2} />}</button>
    </form>
    {listening && <div className="voice-status" role="status"><span />正在听你说话，点麦克风结束</div>}
    <p className="composer-caption"><span>✧</span> 提醒有人记，喜欢有回应 <span>✧</span></p>
  </footer>;
}
