import { Ellipsis, Sparkles } from 'lucide-react';
import { Fragment, useCallback, useEffect, useRef, useState } from 'react';
import { Avatar } from './components/Avatar';
import { BindPage } from './components/BindPage';
import { ChatMessage } from './components/ChatMessage';
import { Composer } from './components/Composer';
import { Details } from './components/Details';
import { ImageViewer } from './components/ImageViewer';
import { LoginPage } from './components/LoginPage';
import { Tools, type ToolName } from './components/Tools';
import { useAccount } from './hooks/useAccount';
import { useRelationship } from './hooks/useRelationship';
import { useCloudChat } from './hooks/useCloudChat';
import { useViewportHeight } from './hooks/useViewportHeight';

function messageDay(createdAt?: string) {
  if (!createdAt || Number.isNaN(Date.parse(createdAt))) return '聊天记录';
  return new Date(createdAt).toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' });
}

function messageDayLabel(createdAt?: string) {
  if (!createdAt || Number.isNaN(Date.parse(createdAt))) return '聊天记录';
  const date = new Date(createdAt);
  const today = new Date();
  const midnight = new Date(today.getFullYear(), today.getMonth(), today.getDate()).getTime();
  const yesterday = new Date(today.getFullYear(), today.getMonth(), today.getDate() - 1).getTime();
  const day = new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
  const prefix = day === midnight ? '今天 · ' : day === yesterday ? '昨天 · ' : '';
  return `${prefix}${messageDay(createdAt)}`;
}

export default function App() {
  useViewportHeight();
  const account = useAccount();
  const { state, error, reload, saveMember, saveSettings, addReminder, toggleReminder } = useRelationship();
  const chat = useCloudChat(account.account?.binding?.sessionId ?? null);
  const [view, setView] = useState<'chat' | 'details'>('chat');
  const [tool, setTool] = useState<ToolName | null>(null);
  const [previewImage, setPreviewImage] = useState<{ src: string; alt: string } | null>(null);
  const [toast, setToast] = useState('');
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const chatRef = useRef<HTMLDivElement>(null);
  const lastChat = useRef({ id: '', count: 0 });
  const nearBottom = useRef(true);
  const notify = useCallback((message: string) => {
    setToast(message);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setToast(''), 4200);
  }, []);

  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => {
    const changedSession = lastChat.current.id !== chat.selectedId;
    if (changedSession) nearBottom.current = true;
    if (chat.messages.length && (nearBottom.current || chat.submitting)) {
      chatRef.current?.scrollTo({ top: chatRef.current.scrollHeight, behavior: changedSession || !lastChat.current.count ? 'instant' : 'smooth' });
    }
    lastChat.current = { id: chat.selectedId ?? '', count: chat.messages.length };
  }, [chat.selectedId, chat.messages.length, chat.messages.at(-1)?.text.length, chat.submitting, view, !!state]);

  if (!account.ready) return <div className="app-shell loading-screen"><div className="brand-mark"><img src="/brand-notes.png" alt="" /></div><h1>贴贴清单</h1><p>{account.error || '正在打开贴贴清单…'}</p>{account.error && <button className="primary-button" onClick={() => void account.reload()}>再试一次</button>}</div>;
  if (!account.account) return <LoginPage onLogin={account.login} onRegister={account.register} notify={notify} />;
  if (!account.account.binding) return <BindPage code={account.account.user.code} onBind={account.bind} notify={notify} />;
  if (!state) return <div className="app-shell loading-screen"><div className="brand-mark"><img src="/brand-notes.png" alt="" /></div><h1>贴贴清单</h1><p>{error || '正在打开贴贴清单…'}</p>{error && <button className="primary-button" onClick={() => void reload()}>再试一次</button>}</div>;

  const ai = state.members.find((m) => m.id === 'ai')!;
  const sendMessage = async (text: string, files: File[] = []) => {
    if (!chat.canSend) throw new Error(chat.error || (chat.busy ? '伙伴还在回复，等这一轮结束后再发送吧。' : '请先加载一个可聊天的云端会话。'));
    return chat.sendMessage(text, files);
  };

  return <div className="app-shell">
    {view === 'chat' ? <>
      <header className="chat-header"><div className="brand-mark"><img src="/brand-notes.png" alt="" /></div><div className="chat-heading"><h1>贴贴清单</h1></div><button className="icon-button details-button" aria-label="查看贴贴清单详情" onClick={() => setView('details')}><Ellipsis size={25} /></button></header>
      {chat.error && <div className="cloud-error" role="alert"><span>{chat.error}</span><button disabled={chat.loading || chat.refreshing || chat.submitting} onClick={() => void chat.reload()}>重试</button></div>}
      <main className={`chat-scroll ${!chat.messages.length ? 'is-empty' : ''}`} ref={chatRef} aria-label="云端聊天记录" aria-busy={chat.loading} onScroll={() => { const el = chatRef.current; if (el) nearBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 100; }}>
        {chat.loading && !chat.messages.length ? <div className="cloud-empty" role="status"><span className="spinner" /><p>正在找回我们聊过的话…</p></div> : !chat.messages.length && !chat.error ? <div className="cloud-empty"><img className="cloud-empty-mark" src="/brand-notes.png" alt="" /><p>{chat.session ? '这里还没有消息，先打个招呼吧。' : '正在准备我们的专属小窝…'}</p></div> : null}
        <div className="messages">{chat.messages.map((message, index) => <Fragment key={message.id}>
          {(index === 0 || messageDay(chat.messages[index - 1].createdAt) !== messageDay(message.createdAt)) && <div className="chat-date"><span /><strong>{messageDayLabel(message.createdAt)}</strong><span /></div>}
          <ChatMessage message={message} members={state.members} reminders={state.reminders} onToggle={toggleReminder} onError={notify} onOpenImage={(src, alt) => setPreviewImage({ src, alt })} />
        </Fragment>)}</div>
        {(chat.busy || chat.submitting || chat.thinking) && <div className="typing-indicator" role="status"><Avatar member={ai} size="small" /><span>{chat.submitting ? '正在发送给云端伙伴' : chat.thinking ? `${ai.name}正在思考` : `${ai.name}正在回复`}</span><span className="typing-dots"><i /><i /><i /></span></div>}
        {chat.turnError && <div className="turn-error" role="alert">{chat.turnError}</div>}
      </main>
      <Composer key={chat.selectedId ?? 'no-session'} sending={chat.submitting} disabled={!chat.canSend} placeholder={chat.busy ? '伙伴正在回复，可以先写下一句…' : '说点什么，让我们更近一点…'} onSend={sendMessage} onTool={setTool} onError={notify} />
    </> : <Details state={state} cloud={chat} onBack={() => setView('chat')} onSaveMember={saveMember} onSaveSettings={saveSettings} notify={notify} onLogout={account.logout} username={account.account.user.username} />}
    {tool && <Tools tool={tool} state={state} onClose={() => setTool(null)} onAdd={addReminder} onToggle={toggleReminder} notify={notify} />}
    {previewImage && <ImageViewer src={previewImage.src} alt={previewImage.alt} onClose={() => setPreviewImage(null)} />}
    {toast && <div className="toast" role="status"><Sparkles size={16} />{toast}</div>}
  </div>;
}
