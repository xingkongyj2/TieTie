import type { Member, Message, Reminder } from '../types';
import { Avatar } from './Avatar';
import { ReminderCard } from './ReminderCard';
import { MarkdownMessage } from './MarkdownMessage';

interface Props { message: Message; members: Member[]; reminders: Reminder[]; onToggle: (id: string) => Promise<void>; onError: (text: string) => void; onOpenImage: (src: string, alt: string) => void }

export function ChatMessage({ message, members, reminders, onToggle, onError, onOpenImage }: Props) {
  const member = members.find((m) => m.id === message.sender)!;
  const reminder = reminders.find((r) => r.id === message.reminderId);
  const isSelf = message.sender === 'self';
  return <div className={`message-row ${isSelf ? 'message-self' : ''} ${message.sender === 'ai' ? 'message-ai' : ''}`}>
    <Avatar member={member} />
    <div className="message-content">
      <div className="message-meta"><span>{member.name}</span><time dateTime={message.createdAt}>{message.createdAt && !Number.isNaN(Date.parse(message.createdAt)) ? new Date(message.createdAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false }) : message.time}</time></div>
      <div className="message-bubble">{message.sender === 'ai' ? <MarkdownMessage text={message.text} onOpenImage={onOpenImage} /> : message.text}{message.images?.length ? <div className="message-images">{message.images.map((src, index) => <button type="button" aria-label={`放大查看图片 ${index + 1}`} onClick={() => onOpenImage(src, `聊天图片 ${index + 1}`)} key={index}><img src={src} alt={`聊天图片 ${index + 1}`} /></button>)}</div> : null}{message.streaming && <span className="stream-caret" aria-label="正在生成" />}</div>
      {reminder && <ReminderCard reminder={reminder} members={members} onToggle={onToggle} onError={onError} />}
    </div>
  </div>;
}
