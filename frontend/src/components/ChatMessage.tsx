import { useRef, useState } from 'react';
import { Textarea } from '@tarojs/components';
import { Bell, FileSpreadsheet, FileText, LockKeyhole } from './Icons';
import type { AskQuestion, Member, Message } from '../types';
import { Avatar } from './Avatar';
import { MentionText } from './MentionText';
import { ChatImage, MarkdownMessage } from './MarkdownMessage';
import { isVisibleChatMessage } from '../lib/chatMessages';
import { attachmentDisplayName } from '../lib/attachments';
import { WeatherCard } from './WeatherCard';
import { chatMessageAnchor } from '../lib/chatScroll';
import { replyStatusText } from '../lib/replyPresentation';

function messageTime(message: Message): string {
  const timestamp = Date.parse(message.createdAt ?? '');
  if (Number.isNaN(timestamp)) return message.time;
  const date = new Date(timestamp + 8 * 60 * 60 * 1000);
  return `${String(date.getUTCHours()).padStart(2, '0')}:${String(date.getUTCMinutes()).padStart(2, '0')}`;
}

interface Props { message: Message; members: Member[]; onError: (text: string) => void; onOpenImage: (src: string, alt: string) => void; onAnswer: (toolUseId: string, text: string) => Promise<unknown>; onLayoutChange?: () => void; answerDisabled?: boolean }

export function ChatMessage({ message, members, onError, onOpenImage, onAnswer, onLayoutChange, answerDisabled = false }: Props) {
  if (!isVisibleChatMessage(message, members)) return null;
  const member = members.find((m) => m.id === message.sender);
  if (!member) return null;
  const name = member.profileName || message.displayName || member.name;
  const isSelf = message.sender === 'self';
  const isReminder = message.sender === 'ai' && message.source === 'reminder';
  const isReminderUpdate = message.sender === 'ai' && message.source === 'reminder_update';
  const ask = message.kind === 'ask' ? message.ask ?? [] : [];
  return <div id={chatMessageAnchor(message)} className={`message-row ${isSelf ? 'message-self' : ''} ${message.sender === 'ai' ? 'message-ai' : ''}`}>
    <Avatar member={{ ...member, name }} showAILabel={message.sender === 'ai'} />
    <div className="message-content">
      <div className="message-meta"><span>{name}</span>{message.visibility === 'private' && <span className="message-private-label"><LockKeyhole size={10} aria-hidden="true" />仅自己可见</span>}<time dateTime={message.createdAt}>{messageTime(message)}</time></div>
      {message.replyStatus ? <div className={`message-bubble reply-status${message.replyStatus.phase === 'error' ? ' is-error' : ''}`} role={message.replyStatus.phase === 'error' ? 'alert' : 'status'} aria-live="polite">
        <span>{replyStatusText(message.replyStatus, name)}</span>
        <span className={`reply-status-mark${message.replyStatus.phase === 'replying' ? ' is-replying' : ''}`} aria-hidden="true">{(message.replyStatus.phase === 'thinking' || message.replyStatus.phase === 'replying') && <><i /><i /><i /></>}</span>
      </div> : message.weatherCards?.length ? <div className="message-bubble has-weather-card">{message.weatherCards.map((card, index) => <WeatherCard key={index} card={card} />)}</div> : ask.length ? <AskCard disabled={answerDisabled} message={message} questions={ask} onAnswer={onAnswer} onError={onError} /> : <div className={`message-bubble ${isReminder || isReminderUpdate ? 'has-reminder-type' : ''}`}>
        {(isReminder || isReminderUpdate) && <span className={`message-type-badge${isReminderUpdate ? ' is-update' : ''}`}><Bell size={12} aria-hidden="true" />{isReminder ? '消息提醒' : '提醒状态更新'}</span>}
        {message.sender === 'ai' ? <MarkdownMessage text={message.text} memberNames={members.filter((m) => m.id !== 'ai').map((m) => m.name)} onOpenImage={onOpenImage} onLayoutChange={onLayoutChange} /> : message.text && <div className="message-text"><MentionText text={message.text} names={members.filter((m) => m.id !== 'ai').map((m) => m.name)} /></div>}
        {message.files?.length ? <div className="message-files" role="list" aria-label="消息附件">{message.files.map((file, index) => <span className="message-file" role="listitem" key={`${file}-${index}`}>
          {/\.(xlsx?|xlsm|xlsb|csv)$/i.test(file) ? <FileSpreadsheet size={15} aria-hidden="true" /> : <FileText size={15} aria-hidden="true" />}
          <span>{attachmentDisplayName(file)}</span>
        </span>)}</div> : null}
        {message.images?.length ? <div className="message-images">{message.images.map((src, index) => <button type="button" aria-label={`放大查看图片 ${index + 1}`} onClick={() => onOpenImage(src, `聊天图片 ${index + 1}`)} key={index}><ChatImage src={src} alt={`聊天图片 ${index + 1}`} onLayoutChange={onLayoutChange} /></button>)}</div> : null}
        {message.streaming && <span className="stream-caret" aria-label="正在生成" />}
      </div>}
      {message.reminderError && <p className="message-reminder-error" role="alert">{message.reminderError}</p>}
      {(message.localStatus === 'failed' || message.localStatus === 'uncertain') && <p className="message-local-status is-error" role={message.localStatus === 'failed' ? 'alert' : 'status'}>{message.localStatus === 'uncertain' ? '发送状态待确认，请勿重复发送' : '发送失败，草稿已保留'}</p>}
    </div>
  </div>;
}

/** Each question keeps an independent selection; all answers travel in one tool result. */
function AskCard({ message, questions, onAnswer, onError, disabled }: { disabled: boolean; message: Message; questions: AskQuestion[]; onAnswer: Props['onAnswer']; onError: (text: string) => void }) {
  const [picked, setPicked] = useState<Record<number, string[]>>({});
  const [freeText, setFreeText] = useState<Record<number, string>>({});
  const [busy, setBusy] = useState(false);
  const lock = useRef(false);
  const answered = !!message.answered;
  const immediate = questions.length === 1 && !questions[0].multiSelect && !!questions[0].options?.length;
  const complete = questions.every((question, index) => question.options?.length ? !!picked[index]?.length : !!freeText[index]?.trim());
  const submit = async (answers: Record<number, string[]> = picked) => {
    if (answered || disabled || lock.current) return;
    const texts = questions.map((question, index) => question.options?.length ? (answers[index] ?? []).join('、') : (freeText[index] ?? '').trim());
    if (texts.some((text) => !text)) return;
    lock.current = true; setBusy(true);
    try { await onAnswer(message.id, questions.length === 1 ? texts[0] : texts.map((text, index) => `${questions[index].question}：${text}`).join('\n')); }
    catch (error) { onError(error instanceof Error ? error.message : '回答发送失败，请再试一次。'); }
    finally { lock.current = false; setBusy(false); }
  };
  const pick = (question: AskQuestion, index: number, label: string) => {
    if (answered || disabled || lock.current) return;
    const existing = picked[index] ?? [];
    const next = { ...picked, [index]: question.multiSelect ? existing.includes(label) ? existing.filter((item) => item !== label) : [...existing, label] : [label] };
    setPicked(next);
    if (immediate) void submit(next);
  };
  return <div className={`ask-card ${answered ? 'is-done' : ''}`}>
    {questions.map((question, index) => <div className="ask-block" key={`${question.question}-${index}`}>
      <div className="ask-head">{question.header ? <span className="ask-header">{question.header}</span> : null}<span className="ask-mode">{question.multiSelect ? '可多选' : question.options?.length ? '选一个' : '写下回答'}</span></div>
      <p className="ask-question">{question.question}</p>
      <div className="ask-options">{(question.options ?? []).map((option) => <button type="button" key={option.label} className={`${(picked[index] ?? []).includes(option.label) ? 'is-picked' : ''}${answered || disabled || busy ? ' is-disabled' : ''}`} disabled={answered || disabled || busy} aria-pressed={(picked[index] ?? []).includes(option.label)} onClick={() => pick(question, index, option.label)}>
        <strong>{option.label}</strong>{option.description ? <span>{option.description}</span> : null}
      </button>)}</div>
      {!question.options?.length && <Textarea className="h5-textarea line-input" value={freeText[index] ?? ''} maxlength={2000} autoHeight disabled={answered || disabled || busy} placeholder="写下你的回答" onInput={(event) => setFreeText((current) => ({ ...current, [index]: event.detail.value }))} />}
    </div>)}
    {answered ? <span className="ask-done">已处理</span> : !immediate ? <button type="button" className="ask-submit" disabled={disabled || busy || !complete} onClick={() => void submit()}>{busy ? '正在发送…' : '就这样答'}</button> : null}
  </div>;
}
