import { Fragment, cloneElement, isValidElement, memo, useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { ExternalLink, Image as ImageIcon } from './Icons';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import Taro from '@tarojs/taro';
import { ScrollView, Checkbox, Text, View, Image, type ImageProps } from '@tarojs/components';
import { mentionRanges } from '../lib/mentions';

/** Native images need explicit dimensions; CSS object-fit alone cannot size them. */
export function ChatImage({ src, alt, onLayoutChange }: { src: string; alt: string; onLayoutChange?: () => void }) {
  const id = `chat-image-${useId().replace(/:/g, '')}`;
  const [size, setSize] = useState<{ width: number; height: number } | null>(null);
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  useEffect(() => setSize(null), [src]);
  const loaded: NonNullable<ImageProps['onLoad']> = (event) => {
    const width = Number(event.detail.width); const height = Number(event.detail.height);
    if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) return;
    const scale = Math.min(1, 240 / width, 220 / height);
    const natural = { width: width * scale, height: height * scale };
    setSize(natural);
    Taro.nextTick(() => {
      if (!alive.current) return;
      Taro.createSelectorQuery().select(`#${id}`).boundingClientRect((rect) => {
        const bounds = Array.isArray(rect) ? rect[0] : rect;
        if (!alive.current) return;
        // A narrow bubble can be smaller than the 240px image cap.
        if (bounds?.width && bounds.width < natural.width - .5) setSize({ width: bounds.width, height: bounds.width * height / width });
        onLayoutChange?.();
      }).exec();
    });
  };
  if (process.env.TARO_ENV === 'h5') return <img src={src} alt={alt} onLoad={onLayoutChange} />;
  return <Image id={id} className="h5-img" src={src} mode="aspectFit" ariaLabel={alt}
    style={{ width: `${size?.width ?? 0}px`, height: `${size?.height ?? 0}px`, maxWidth: '100%' }} onLoad={loaded} />;
}

function mentionContent(node: ReactNode, names: string[]): ReactNode {
  if (typeof node === 'string') {
    const ranges = mentionRanges(node, names); if (!ranges.length) return node;
    const pieces: ReactNode[] = []; let start = 0;
    ranges.forEach((range) => { pieces.push(node.slice(start, range.start)); pieces.push(<Text className="message-mention" key={range.start}>{node.slice(range.start, range.end)}</Text>); start = range.end; });
    pieces.push(node.slice(start)); return pieces;
  }
  if (Array.isArray(node)) return node.map((child, index) => <Fragment key={index}>{mentionContent(child, names)}</Fragment>);
  if (isValidElement<{ children?: ReactNode; className?: string; node?: { tagName?: string } }>(node) && node.props.className !== 'message-mention' && !['a', 'code', 'pre'].includes(node.props.node?.tagName ?? String(node.type))) return cloneElement(node, {}, mentionContent(node.props.children, names));
  return node;
}
function messageComponents(onOpenImage: (src: string, alt: string) => void, names: string[], onLayoutChange?: () => void): Components { return {
  a({ href, children }) {
    if (!href || !/^https?:\/\//i.test(href)) return <Text>{children}</Text>;
    return <span className="markdown-link" style={{ display: 'inline', color: '#000', textDecoration: 'underline' }} onClick={() => { void Taro.setClipboardData({ data: href }).catch(() => Taro.showToast({ title: '链接暂时无法复制', icon: 'none' }).catch(() => {})); }}>{children}<ExternalLink size={11} aria-hidden="true" /></span>;
  },
  img({ alt, src }) {
    if (!src || !/^https:\/\//i.test(src)) return <span className="markdown-image-placeholder"><ImageIcon size={13} aria-hidden="true" />{alt || '图片'}</span>;
    return <button className="message-image-button" type="button" aria-label={`放大查看${alt || '图片'}`} onClick={() => onOpenImage(src, alt || '聊天图片')}><ChatImage src={src} alt={alt || '聊天图片'} onLayoutChange={onLayoutChange} /></button>;
  },
  table({ children }) { return <ScrollView className="markdown-table-wrap" scrollX showScrollbar={false}><View className="h5-table markdown-native-table" style={{ display: 'table' }}>{children}</View></ScrollView>; },
  h1({ children }) { return <h1>{children}</h1>; },
  h2({ children }) { return <h2>{children}</h2>; },
  h3({ children }) { return <h3>{children}</h3>; },
  h4({ children }) { return <h4>{children}</h4>; },
  h5({ children }) { return <h5>{children}</h5>; },
  h6({ children }) { return <h6>{children}</h6>; },
  strong({ children }) { return <strong>{children}</strong>; },
  em({ children }) { return <em>{children}</em>; },
  del({ children }) { return <del>{children}</del>; },
  ul({ children, className }) { return <ul className={className}>{children}</ul>; },
  ol({ children, start }) { return <ol start={start}>{children}</ol>; },
  blockquote({ children }) { return <blockquote>{children}</blockquote>; },
  pre({ children }) { return <pre>{children}</pre>; },
  code({ children, className }) { return <code className={className}>{children}</code>; },
  hr() { return <hr />; },
  br() { return <br />; },
  thead({ children }) { return <View className="h5-thead" style={{ display: 'table-header-group' }}>{children}</View>; },
  tbody({ children }) { return <View className="h5-tbody" style={{ display: 'table-row-group' }}>{children}</View>; },
  tr({ children }) { return <View className="h5-tr" style={{ display: 'table-row' }}>{children}</View>; },
  input({ checked }) { return <Checkbox value="task" checked={!!checked} disabled />; },
  p({ children }) { return <p>{mentionContent(children, names)}</p>; },
  li({ children, className }) { return <li className={className}>{mentionContent(children, names)}</li>; },
  td({ children }) { return <View className="h5-td" style={{ display: 'table-cell' }}>{mentionContent(children, names)}</View>; },
  th({ children }) { return <View className="h5-th" style={{ display: 'table-cell' }}>{mentionContent(children, names)}</View>; },
}; }
export const MarkdownMessage = memo(function MarkdownMessage({ text, memberNames = [], onOpenImage, onLayoutChange }: { text: string; memberNames?: string[]; onOpenImage: (src: string, alt: string) => void; onLayoutChange?: () => void }) {
  return <div className="markdown-body"><ReactMarkdown remarkPlugins={[remarkGfm]} components={messageComponents(onOpenImage, memberNames, onLayoutChange)} skipHtml>{text}</ReactMarkdown></div>;
});
