import { Fragment } from 'react';
import { mentionRanges } from '../lib/mentions';

export function MentionText({ text, names, className = 'message-mention' }: { text: string; names: string[]; className?: string }) {
  const ranges = mentionRanges(text, names); let at = 0;
  return <>{ranges.map((range) => { const before = text.slice(at, range.start); at = range.end; return <Fragment key={range.start}>{before}<span className={className}>{text.slice(range.start, range.end)}</span></Fragment>; })}{text.slice(at)}</>;
}
