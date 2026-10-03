import { Ellipsis, Sparkles } from 'lucide-react';
import { Fragment, useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Avatar } from './components/Avatar';
import { BindPage } from './components/BindPage';
import { BottomNav } from './components/BottomNav';
import { ChatMessage } from './components/ChatMessage';
import { Composer } from './components/Composer';
import { mentionRanges } from './lib/mentions';
import { canToggleReminder, reminderPhase } from './lib/reminders';
import { Details } from './components/Details';
import { ImageViewer } from './components/ImageViewer';
import { LoginPage } from './components/LoginPage';
import { LittleThings } from './components/LittleThings';
import { Mine } from './components/Mine';
import { SpaceBuddy } from './components/SpaceBuddies';
import { Tools, type ToolName } from './components/Tools';
import { useAccount } from './hooks/useAccount';
import { useRelationship } from './hooks/useRelationship';
import { useCloudChat } from './hooks/useCloudChat';
import { useViewportHeight } from './hooks/useViewportHeight';
import { useAnniversaries } from './hooks/useAnniversaries';
import type { Reminder } from './types';

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
  const [editProfileInitially, setEditProfileInitially] = useState(false);
  useViewportHeight();
  const account = useAccount();
  const { state, error, reload, saveMember, saveSettings } = useRelationship(account.account?.user.userId, account.account?.binding?.partnerId, account.account?.binding?.sessionId);
  const chat = useCloudChat(account.account?.binding?.sessionId ?? null, account.reload, account.account?.user.userId);
  const anniversaries = useAnniversaries(account.account?.binding?.sessionId);
  const reloadAnniversaries = useRef(anniversaries.reload); reloadAnniversaries.current = anniversaries.reload;
  const [view, setView] = useState<'we' | 'things' | 'mine' | 'details'>('we');
  const [tool, setTool] = useState<ToolName | null>(null);
  const [previewImage, setPreviewImage] = useState<{ src: string; alt: string } | null>(null);
  const [toast, setToast] = useState('');
  const [composerExpanded, setComposerExpanded] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const chatRef = useRef<HTMLDivElement>(null);
  const messagesRef = useRef<HTMLDivElement>(null);
  const lastChat = useRef({ id: '', view: '' });
  const nearBottom = useRef(true);
  const seenReminders = useRef<{ sessionId: string; ids: Set<string> }>({ sessionId: '', ids: new Set() });
  const notify = useCallback((message: string) => {
    setToast(message);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setToast(''), 4200);
  }, []);

  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => {
    if (account.account?.binding && !chat.loading && !chat.busy) void reloadAnniversaries.current();
  }, [account.account?.binding?.sessionId, chat.messages.at(-1)?.id, chat.loading, chat.busy, view, tool]);
  useLayoutEffect(() => {
    const changedSession = lastChat.current.id !== chat.selectedId;
    const openedChat = view === 'we' && lastChat.current.view !== 'we';
    if (changedSession || openedChat) nearBottom.current = true;
    if (view === 'we' && (chat.messages.length || chat.replyFeedback) && (nearBottom.current || chat.submitting)) {
      const scroller = chatRef.current;
      if (scroller) scroller.scrollTop = scroller.scrollHeight;
    }
    lastChat.current = { id: chat.selectedId ?? '', view };
  }, [chat.selectedId, chat.messages, chat.replyFeedback?.phase, chat.replyFeedback?.message, chat.submitting, view, !!state]);
  useLayoutEffect(() => {
    const scroller = chatRef.current;
    const messages = messagesRef.current;
    if (view !== 'we' || !scroller || !messages || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(() => {
      if (nearBottom.current) scroller.scrollTop = scroller.scrollHeight;
    });
    observer.observe(messages);
    observer.observe(scroller);
    return () => observer.disconnect();
  }, [chat.selectedId, view, !!state]);
  useEffect(() => {
    const showLatest = () => {
      if (view !== 'we' || document.visibilityState !== 'visible') return;
      nearBottom.current = true;
      const scroller = chatRef.current;
      if (scroller) scroller.scrollTop = scroller.scrollHeight;
    };
    document.addEventListener('visibilitychange', showLatest);
    return () => {
      document.removeEventListener('visibilitychange', showLatest);
    };
  }, [view]);
  useEffect(() => {
    if (!chat.selectedId || chat.loading) return;
    const seen = seenReminders.current;
    if (seen.sessionId !== chat.selectedId) {
      seenReminders.current = { sessionId: chat.selectedId, ids: new Set(chat.messages.map((message) => message.id)) };
      return;
    }
    const userId = account.account?.user.userId;
    for (const message of chat.messages) {
      if (seen.ids.has(message.id)) continue;
      seen.ids.add(message.id);
      if (message.source === 'reminder' && userId && message.recipientIds?.includes(userId)
        && Date.now() - Date.parse(message.createdAt ?? '') < 120_000
        && (view !== 'we' || !nearBottom.current)) notify(`给你的提醒：${message.text.slice(0, 80)}`);
    }
  }, [chat.selectedId, chat.loading, chat.messages, account.account?.user.userId, view, notify]);

  if (!account.ready) return <div className="app-shell loading-screen"><div className="brand-mark"><img src="/brand-notes.png" alt="" /></div><h1>贴贴清单</h1><p>{account.error || '正在打开贴贴清单…'}</p>{account.error && <button className="primary-button" onClick={() => void account.reload()}>再试一次</button>}</div>;
  if (!account.account) return <LoginPage onLogin={account.login} onRegister={account.register} notify={notify} />;
  if (!state) return <div className="app-shell loading-screen"><div className="brand-mark"><img src="/brand-notes.png" alt="" /></div><h1>贴贴清单</h1><p>{error || '正在打开贴贴清单…'}</p>{error && <button className="primary-button" onClick={() => void reload()}>再试一次</button>}</div>;

  const ai = state.members.find((m) => m.id === 'ai')!;
  const feedback = chat.replyFeedback;
  const feedbackText = feedback?.message || (feedback?.phase === 'sending' ? '消息正在发送…'
    : feedback?.phase === 'thinking' ? `${ai.name}正在思考…`
      : feedback?.phase === 'replying' ? `${ai.name}正在回复…`
        : feedback?.phase === 'syncing' ? `${ai.name}正在整理回复…`
          : feedback?.phase === 'sent' ? '消息已发给对方'
            : feedback?.phase === 'error' ? '这次回复遇到问题，请重试。'
              : `${ai.name}已收到，正在准备回复…`);
  const selfId = account.account.user.userId;
  const partnerId = account.account.binding?.partnerId;
  const sharedMembers = state.members.map((member) => {
    if (member.id === 'ai') return member;
    const userId = member.id === 'self' ? selfId : partnerId;
    const verified = chat.members.find((item) => item.userId === userId);
    return { ...member, userId, name: verified?.name || (member.id === 'self' ? account.account!.user.username : '另一位成员') };
  });
  const reminders: Reminder[] = chat.reminders.map((reminder) => ({
    id: reminder.id, title: reminder.title, time: reminder.dueAt,
    recipientIds: reminder.recipientIds, status: reminder.status,
    deliveredAt: reminder.deliveredAt, taskStatus: reminder.taskStatus, taskCompletedAt: reminder.taskCompletedAt,
    assignee: reminder.recipientIds.length > 1 ? 'both' : reminder.recipientIds.includes(selfId) ? 'self' : 'partner',
    completed: reminderPhase(reminder) === 'completed',
  }));
  const sharedState = { ...state, members: sharedMembers, reminders };
  const toggleReminder = async (id: string) => {
    const reminder = reminders.find((item) => item.id === id);
    if (!reminder) throw new Error('这条共享提醒已变更，请刷新后再试。');
    if (!canToggleReminder(reminder)) throw new Error('这条提醒已结束或正在发送，无法修改完成状态。');
    const completing = reminder.status !== 'completed';
    await chat.changeReminder(id, completing ? 'completed' : 'scheduled');
    notify(completing
      ? `你已手动完成「${reminder.title}」，AI 记忆同步后会在聊天里确认。`
      : `已把「${reminder.title}」恢复为待完成。`);
  };
  const cancelReminder = (id: string) => chat.changeReminder(id, 'cancelled');
  const addReminder = async (input: Omit<Reminder, 'id' | 'completed'>) => {
    if (!account.account?.binding) throw new Error('请先绑定共享空间，再添加提醒。');
    const due = new Date(input.time ?? '');
    if (Number.isNaN(due.getTime()) || due.getTime() <= Date.now()) throw new Error('请选择一个未来的提醒时间。');
    const recipientIds = input.assignee === 'both' ? [selfId, account.account.binding.partnerId]
      : [input.assignee === 'self' ? selfId : account.account.binding.partnerId];
    await chat.saveReminder({ title: input.title, dueAt: due.toISOString(), recipientIds });
  };
  const sendMessage = async (text: string, files: File[] = [], visibility: 'shared' | 'private' = 'shared') => {
    if (!chat.canSend) throw new Error(chat.error || (chat.awaitingAsk ? '云端助手在等你回答上面那道选择题，先选一个才能继续。' : chat.busy ? '伙伴还在回复，等这一轮结束后再发送吧。' : '请先加载一个可聊天的云端会话。'));
    const partner = sharedMembers.find((member) => member.id === 'partner');
    return chat.sendMessage(text, files, visibility, !!partner && mentionRanges(text, [partner.name]).length > 0);
  };


  return <div className={`app-shell ${view !== 'details' ? 'has-bottom-nav' : ''}${composerExpanded ? ' is-composer-expanded' : ''}`} data-view={view}>
    <div className="chat-view" hidden={view !== 'we'}>
      {account.account.binding ? <>
      <header className="chat-header"><div className="chat-heading"><h1>我们</h1></div><button className="icon-button details-button" aria-label="查看角色信息" onClick={() => setView('details')}><Ellipsis size={18} /></button></header>
      {chat.error && !feedback && <div className="cloud-error" role="alert"><span>{chat.error}</span><button disabled={chat.loading || chat.refreshing || chat.submitting} onClick={() => void chat.reload()}>重试</button></div>}
      <main className={`chat-scroll ${!chat.messages.length && !feedback ? 'is-empty' : ''}`} ref={chatRef} aria-label="云端聊天记录" aria-busy={chat.loading && !chat.messages.length} onScroll={() => { const el = chatRef.current; if (el && !chat.loading) nearBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 100; }}>
        {chat.loading && !chat.messages.length && !feedback ? <div className="cloud-empty" role="status"><span className="spinner" /><p>正在找回我们聊过的话…</p></div> : !chat.messages.length && !chat.error && !feedback ? <div className="cloud-empty chat-welcome"><span className="welcome-eyebrow"><Sparkles size={13} />我们的共享空间</span><SpaceBuddy variant="blue" className="chat-welcome-buddy" /><h2>共同的提醒，日常的分享</h2>{!chat.session && <p>正在准备我们的共享空间…</p>}</div> : null}
        <div className="messages" ref={messagesRef}>{chat.messages.map((message, index) => <Fragment key={message.id}>
          {(index === 0 || messageDay(chat.messages[index - 1].createdAt) !== messageDay(message.createdAt)) && <div className="chat-date"><span /><strong>{messageDayLabel(message.createdAt)}</strong><span /></div>}
          <ChatMessage message={message} members={sharedMembers} onError={notify} onOpenImage={(src, alt) => setPreviewImage({ src, alt })} onAnswer={chat.answerAsk} />
        </Fragment>)}</div>
        {feedback && <div className={`assistant-feedback${feedback.phase === 'error' ? ' is-error' : ''}${feedback.phase === 'sent' || feedback.phase === 'error' ? ' is-terminal' : ''}`} role={feedback.phase === 'error' ? 'alert' : 'status'} aria-live="polite"><Avatar member={ai} /><div className="assistant-feedback-bubble"><span>{feedbackText}</span><span className="typing-dots" aria-hidden="true"><i /><i /><i /></span>{chat.error && <button className="assistant-feedback-retry" disabled={chat.loading || chat.refreshing || chat.submitting} onClick={() => void chat.reload()}>重新同步</button>}</div></div>}
        {chat.turnError && !feedback && <div className="turn-error" role="alert">{chat.turnError}</div>}
        {chat.remindersError && <div className="turn-error" role="alert">{chat.remindersError}</div>}
      </main>
      <Composer members={sharedMembers} key={chat.selectedId ?? 'no-session'} sending={chat.submitting} disabled={!chat.canSend} placeholder={chat.awaitingAsk ? '先回答上面那道选择题…' : chat.busy ? (chat.silent ? '消息已发给对方，可以先写下一句…' : '伙伴正在回复，可以先写下一句…') : '记下一个共同提醒…'} onSend={sendMessage} onTool={setTool} onError={notify} onExpandedChange={setComposerExpanded} />
      </> : <BindPage code={account.account.user.code} onBind={account.bind} notify={notify} embedded />}
    </div>
    {view === 'things' && <LittleThings sessionId={account.account.binding?.sessionId} selfId={selfId} onEditRegion={() => { setEditProfileInitially(true); setView('mine'); }} onBind={() => setView('we')} state={state} anniversaries={anniversaries} reminderState={sharedState} onToggle={toggleReminder} onCancel={cancelReminder} remindersLoading={chat.loading} reminderNotice={!account.account.binding ? '绑定两人空间后，可以一起安排和查看提醒。' : chat.remindersError || chat.error} onReloadReminders={account.account.binding ? chat.reload : undefined} notify={notify} />}
    {view === 'mine' && <Mine editProfileInitially={editProfileInitially} state={state} username={account.account.user.username} code={account.account.user.code} hasSession={!!account.account.binding} onSaveMember={saveMember} onLogout={account.logout} onExitSession={account.unbind} notify={notify} />}
    {view === 'details' && <Details state={sharedState} sessionId={account.account.binding?.sessionId} onBack={() => setView('we')} onSaveMember={saveMember} onSaveSettings={saveSettings} notify={notify} />}
    {view !== 'details' && <BottomNav view={view} onChange={(next) => { setEditProfileInitially(false); setView(next); }} />}
    {tool && <Tools tool={tool} state={sharedState} anniversaries={anniversaries} onClose={() => setTool(null)} onAdd={addReminder} notify={notify} />}
    {previewImage && <ImageViewer src={previewImage.src} alt={previewImage.alt} onClose={() => setPreviewImage(null)} />}
    {toast && <div className="toast" role="status"><Sparkles size={16} />{toast}</div>}
  </div>;
}
