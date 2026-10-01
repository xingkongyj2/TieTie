import { Fragment, cloneElement, isValidElement, memo, type ReactNode } from 'react';
import { ExternalLink, Image as ImageIcon } from 'lucide-react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { mentionRanges } from '../lib/mentions';

function mentionContent(node: ReactNode, names: string[]): ReactNode {
  if (typeof node === 'string') {
    const ranges = mentionRanges(node, names); if (!ranges.length) return node;
    const pieces: ReactNode[] = []; let start = 0;
    ranges.forEach((range) => { pieces.push(node.slice(start, range.start)); pieces.push(<span className="message-mention" key={range.start}>{node.slice(range.start, range.end)}</span>); start = range.end; });
    pieces.push(node.slice(start)); return pieces;
  }
  if (Array.isArray(node)) return node.map((child, index) => <Fragment key={index}>{mentionContent(child, names)}</Fragment>);
  if (isValidElement<{ children?: ReactNode; className?: string; node?: { tagName?: string } }>(node) && node.props.className !== 'message-mention' && !['a', 'code', 'pre'].includes(node.props.node?.tagName ?? String(node.type))) return cloneElement(node, {}, mentionContent(node.props.children, names));
  return node;
}
function messageComponents(onOpenImage: (src: string, alt: string) => void, names: string[]): Components { return {
  a({ href, children }) {
    if (!href || !/^https?:\/\//i.test(href)) return <span>{children}</span>;
    return <a href={href} target="_blank" rel="noopener noreferrer">{children}<ExternalLink size={11} aria-hidden="true" /></a>;
  },
  img({ alt, src }) {
    if (!src || !/^https:\/\//i.test(src)) return <span className="markdown-image-placeholder"><ImageIcon size={13} aria-hidden="true" />{alt || '图片'}</span>;
    return <button className="message-image-button" type="button" aria-label={`放大查看${alt || '图片'}`} onClick={() => onOpenImage(src, alt || '聊天图片')}><img src={src} alt={alt || '聊天图片'} loading="lazy" /></button>;
  },
  table({ children }) { return <div className="markdown-table-wrap" role="region" aria-label="消息表格" tabIndex={0}><table>{children}</table></div>; },
  p({ children }) { return <p>{mentionContent(children, names)}</p>; },
  li({ children }) { return <li>{mentionContent(children, names)}</li>; },
  td({ children }) { return <td>{mentionContent(children, names)}</td>; },
  th({ children }) { return <th>{mentionContent(children, names)}</th>; },
}; }
export const MarkdownMessage = memo(function MarkdownMessage({ text, memberNames = [], onOpenImage }: { text: string; memberNames?: string[]; onOpenImage: (src: string, alt: string) => void }) {
  return <div className="markdown-body"><ReactMarkdown remarkPlugins={[remarkGfm]} components={messageComponents(onOpenImage, memberNames)} skipHtml>{text}</ReactMarkdown></div>;
});
