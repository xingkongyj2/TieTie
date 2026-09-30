import { isValidElement, memo, type ReactNode } from 'react';
import { Bell, ExternalLink, Image as ImageIcon } from 'lucide-react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';

function plainText(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(plainText).join('');
  if (isValidElement<{ children?: ReactNode }>(node)) return plainText(node.props.children);
  return '';
}

const reminderPattern = /【(?:共同待办|待办提醒|提醒事项|提醒)】|(?:^|\s)(?:提醒内容|提醒时间)[:：]/u;
const openingReminderPattern = /^(?:🔔|⏰|📌)\s*|^【(?:共同待办|待办提醒|提醒事项|提醒)】/u;

function messageComponents(onOpenImage: (src: string, alt: string) => void): Components { return {
  a({ href, children }) {
    if (!href || !/^https?:\/\//i.test(href)) return <span>{children}</span>;
    return <a href={href} target="_blank" rel="noopener noreferrer">{children}<ExternalLink size={11} aria-hidden="true" /></a>;
  },
  img({ alt, src }) {
    if (!src || !/^https:\/\//i.test(src)) return <span className="markdown-image-placeholder"><ImageIcon size={13} aria-hidden="true" />{alt || '图片'}</span>;
    return <button className="message-image-button" type="button" aria-label={`放大查看${alt || '图片'}`} onClick={() => onOpenImage(src, alt || '聊天图片')}><img src={src} alt={alt || '聊天图片'} loading="lazy" /></button>;
  },
  table({ children }) {
    const isReminder = /提醒|待办/u.test(plainText(children));
    return <div className={`markdown-table-wrap ${isReminder ? 'is-reminder-table' : ''}`} role="region" aria-label={isReminder ? '提醒安排表格' : '消息表格'} tabIndex={0}>
      {isReminder && <span className="markdown-table-label"><Bell size={12} aria-hidden="true" />提醒安排</span>}
      <table>{children}</table>
    </div>;
  },
  blockquote({ children }) {
    const isReminder = reminderPattern.test(plainText(children));
    return <blockquote className={isReminder ? 'markdown-reminder-note' : undefined}>
      {isReminder && <span className="markdown-note-label"><Bell size={13} aria-hidden="true" />提醒内容</span>}
      {children}
    </blockquote>;
  },
  p({ children }) {
    if (openingReminderPattern.test(plainText(children))) {
      return <div className="markdown-reminder-note markdown-standalone-note"><span className="markdown-note-label"><Bell size={13} aria-hidden="true" />提醒内容</span><p>{children}</p></div>;
    }
    return <p>{children}</p>;
  },
}; }

export const MarkdownMessage = memo(function MarkdownMessage({ text, onOpenImage }: { text: string; onOpenImage: (src: string, alt: string) => void }) {
  return <div className="markdown-body"><ReactMarkdown remarkPlugins={[remarkGfm]} components={messageComponents(onOpenImage)} skipHtml>{text}</ReactMarkdown></div>;
});
