import { useState } from 'react';
import { Bell, FileSpreadsheet, FileText, LockKeyhole } from 'lucide-react';
import type { AskQuestion, Member, Message } from '../types';
import { Avatar } from './Avatar';
import { MentionText } from './MentionText';
import { MarkdownMessage } from './MarkdownMessage';
import { isVisibleChatMessage } from '../lib/chatMessages';
import { attachmentDisplayName } from '../lib/attachments';

interface Props { message: Message; members: Member[]; onError: (text: string) => void; onOpenImage: (src: string, alt: string) => void; onAnswer: (toolUseId: string, text: string) => Promise<unknown> }

export function ChatMessage({ message, members, onError, onOpenImage, onAnswer }: Props) {
  if (!isVisibleChatMessage(message, members)) return null;
  const member = members.find((m) => m.id === message.sender);
  if (!member) return null;
  const name = message.displayName || member.name;
  const isSelf = message.sender === 'self';
  const isReminder = message.sender === 'ai' && message.source === 'reminder';
  const ask = message.kind === 'ask' ? message.ask ?? [] : [];
  return <div className={`message-row ${isSelf ? 'message-self' : ''} ${message.sender === 'ai' ? 'message-ai' : ''}`}>
    <Avatar member={{ ...member, name }} showAILabel={message.sender === 'ai'} />
    <div className="message-content">
      <div className="message-meta"><span>{name}</span>{message.visibility === 'private' && <span className="message-private-label"><LockKeyhole size={10} aria-hidden="true" />仅自己可见</span>}<time dateTime={message.createdAt}>{message.createdAt && !Number.isNaN(Date.parse(message.createdAt)) ? new Date(message.createdAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false }) : message.time}</time></div>
      {ask.length ? <AskCard message={message} questions={ask} onAnswer={onAnswer} onError={onError} /> : <div className={`message-bubble ${isReminder ? 'has-reminder-type' : ''}`}>
        {isReminder && <span className="message-type-badge"><Bell size={12} aria-hidden="true" />到点提醒</span>}
        {message.sender === 'ai' ? <MarkdownMessage text={message.text} memberNames={members.filter((m) => m.id !== 'ai').map((m) => m.name)} onOpenImage={onOpenImage} /> : message.text && <div className="message-text"><MentionText text={message.text} names={members.filter((m) => m.id !== 'ai').map((m) => m.name)} /></div>}
        {message.files?.length ? <div className="message-files" role="list" aria-label="消息附件">{message.files.map((file, index) => <span className="message-file" role="listitem" key={`${file}-${index}`}>
          {/\.(xlsx?|xlsm|xlsb|csv)$/i.test(file) ? <FileSpreadsheet size={15} aria-hidden="true" /> : <FileText size={15} aria-hidden="true" />}
          <span>{attachmentDisplayName(file)}</span>
        </span>)}</div> : null}
        {message.images?.length ? <div className="message-images">{message.images.map((src, index) => <button type="button" aria-label={`放大查看图片 ${index + 1}`} onClick={() => onOpenImage(src, `聊天图片 ${index + 1}`)} key={index}><img src={src} alt={`聊天图片 ${index + 1}`} /></button>)}</div> : null}
        {message.streaming && <span className="stream-caret" aria-label="正在生成" />}
      </div>}
      {message.reminderError && <p className="message-reminder-error" role="alert">{message.reminderError}</p>}
    </div>
  </div>;
}

/** 云端 Agent 用自定义工具抛出的选择题：点一下选项即作为工具应答回传。 */
function AskCard({ message, questions, onAnswer, onError }: { message: Message; questions: AskQuestion[]; onAnswer: Props['onAnswer']; onError: (text: string) => void }) {
  const [picked, setPicked] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const answered = !!message.answered;
  const multi = questions.some((question) => question.multiSelect);

  const submit = async (labels: string[]) => {
    if (answered || busy || !labels.length) return;
    setBusy(true);
    try {
      await onAnswer(message.id, labels.join('、'));
    } catch (e) {
      onError(e instanceof Error ? e.message : '回答发送失败，请再试一次。');
    } finally {
      setBusy(false);
    }
  };

  const pick = (question: AskQuestion, label: string) => {
    if (!question.multiSelect) { void submit([label]); return; }
    setPicked((list) => (list.includes(label) ? list.filter((item) => item !== label) : [...list, label]));
  };

  return <div className={`ask-card ${answered ? 'is-done' : ''}`}>
    {questions.map((question, index) => <div className="ask-block" key={`${question.question}-${index}`}>
      <div className="ask-head">
        {question.header ? <span className="ask-header">{question.header}</span> : null}
        <span className="ask-mode">{question.multiSelect ? '可多选' : '选一个'}</span>
      </div>
      <p className="ask-question">{question.question}</p>
      <div className="ask-options">{(question.options ?? []).map((option) => <button type="button" key={option.label} className={picked.includes(option.label) ? 'is-picked' : ''} disabled={answered || busy} aria-pressed={picked.includes(option.label)} onClick={() => pick(question, option.label)}>
        <strong>{option.label}</strong>
        {option.description ? <span>{option.description}</span> : null}
      </button>)}</div>
    </div>)}
    {answered ? <span className="ask-done">已处理</span> : multi ? <button type="button" className="ask-submit" disabled={busy || picked.length === 0} onClick={() => void submit(picked)}>就这样答</button> : null}
  </div>;
}
