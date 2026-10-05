import { ArrowRight, Bell, Ellipsis, Sparkles } from './components/Icons';
import { Fragment, useCallback, useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react';
import Taro from '@tarojs/taro';
import { ScrollView, View } from '@tarojs/components';
import type { MiniFile } from './lib/files';
import { nextFrame, cancelFrame, onAppVisibilityChange } from './lib/platform';
import { assetUrl } from './lib/assets';
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
import { Onboarding } from './components/Onboarding';
import { SpaceBuddy } from './components/SpaceBuddies';
import { Tools, type ToolName } from './components/Tools';
import { useAccount } from './hooks/useAccount';
import { useRelationship } from './hooks/useRelationship';
import { useCloudChat } from './hooks/useCloudChat';
import { useViewportHeight } from './hooks/useViewportHeight';
import { useAnniversaries } from './hooks/useAnniversaries';
import type { Message, Reminder } from './types';
import { matchesMountedPrefix, mountedMessageKey, nextHistoryBatchEnd } from './lib/chatMounting';

const nativeHistory = process.env.TARO_ENV === 'weapp';

/** Stable sibling slots stop a tab/overlay change from re-hydrating chat. */
function NativeSlot({ children, shown, panel = false, overlay = false }: { children: ReactNode; shown: boolean; panel?: boolean; overlay?: boolean }) {
  if (!nativeHistory) return <>{children}</>;
  return <View hidden={!shown} style={panel
    ? { display: shown ? 'flex' : 'none', flex: 1, minHeight: '0px', height: '0px', width: '100%', flexDirection: 'column' }
    : { display: shown ? 'block' : 'none', flexShrink: 0, ...(overlay ? { height: '0px' } : {}) }}>
    {children}
  </View>;
}

/** Keep append-only native commits small while retaining the complete hook history. */
function useMountedHistory(messages: Message[], owner: string, active: boolean): Message[] {
  const [mounted, setMounted] = useState({ owner: '', count: 0 });
  const progress = useRef({ owner: '', keys: [] as string[] });
  const latest = useRef({ messages, owner, active });
  latest.current = { messages, owner, active };
  useEffect(() => {
    if (process.env.TARO_ENV !== 'weapp') return;
    let alive = true;
    let frame: number | undefined;
    const advance = () => {
      const snapshot = latest.current;
      if (!alive || snapshot.owner !== owner || !snapshot.active || !matchesMountedPrefix(progress.current.keys, snapshot.messages)) return;
      const start = progress.current.keys.length;
      if (start >= snapshot.messages.length) return;
      const end = nextHistoryBatchEnd(snapshot.messages, start);
      progress.current.keys = snapshot.messages.slice(0, end).map(mountedMessageKey);
      setMounted({ owner, count: end });
      if (end < snapshot.messages.length) frame = nextFrame(advance);
    };
    // Removing/reordering rows invokes Taro's whole-cn replacement. Clear it
    // first, then rebuild with bounded tail appends instead of a large insert.
    if (!active || progress.current.owner !== owner || !matchesMountedPrefix(progress.current.keys, messages)) {
      progress.current = { owner, keys: [] };
      setMounted({ owner, count: 0 });
    }
    if (active) frame = nextFrame(advance);
    return () => { alive = false; if (frame !== undefined) cancelFrame(frame); };
  }, [messages, owner, active]);
  if (process.env.TARO_ENV !== 'weapp') return messages;
  return active && mounted.owner === owner && matchesMountedPrefix(progress.current.keys, messages)
    ? messages.slice(0, mounted.count) : [];
}

function shanghaiDate(createdAt?: string): Date | null {
  const timestamp = Date.parse(createdAt ?? '');
  return Number.isNaN(timestamp) ? null : new Date(timestamp + 8 * 60 * 60 * 1000);
}
function dayKey(date: Date): string { return `${date.getUTCFullYear()}-${date.getUTCMonth() + 1}-${date.getUTCDate()}`; }
function messageDay(createdAt?: string) {
  const date = shanghaiDate(createdAt);
  return date ? dayKey(date) : '聊天记录';
}
function messageDayLabel(createdAt?: string) {
  const date = shanghaiDate(createdAt);
  if (!date) return '聊天记录';
  const today = new Date(Date.now() + 8 * 60 * 60 * 1000);
  const yesterday = new Date(today.getTime() - 24 * 60 * 60 * 1000);
  const prefix = dayKey(date) === dayKey(today) ? '今天 · ' : dayKey(date) === dayKey(yesterday) ? '昨天 · ' : '';
  return `${prefix}${date.getUTCFullYear()}年${date.getUTCMonth() + 1}月${date.getUTCDate()}日`;
}

export default function TieTieApp() {
  const [editProfileInitially, setEditProfileInitially] = useState(false);
  const { appHeight, keyboardOpen } = useViewportHeight();
  const account = useAccount();
  const { state, error, reload, saveMember, saveSettings } = useRelationship(account.account?.user.userId, account.account?.binding?.partnerId, account.account?.binding?.sessionId, account.account?.user.username);
  const chat = useCloudChat(account.account?.binding?.sessionId ?? null, account.reload, account.account?.user.userId);
  const anniversaries = useAnniversaries(account.account?.binding?.sessionId);
  const reloadAnniversaries = useRef(anniversaries.reload); reloadAnniversaries.current = anniversaries.reload;
  const [view, setView] = useState<'we' | 'things' | 'mine' | 'details'>('we');
  const [showBindPage, setShowBindPage] = useState(() => Boolean(Taro.getCurrentInstance().router?.params.invite || Taro.getStorageSync('tietie.pendingInvite')));
  const [tool, setTool] = useState<ToolName | null>(null);
  const [previewImage, setPreviewImage] = useState<{ src: string; alt: string } | null>(null);
  const [toast, setToast] = useState('');
  const [composerDismissSignal, setComposerDismissSignal] = useState(0);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [chatScrollTarget, setChatScrollTarget] = useState('');
  const [pullRefreshing, setPullRefreshing] = useState(false);
  const scrollSequence = useRef(0);
  const chatViewportHeight = useRef(400);
  const lastChat = useRef({ id: '', view: '' });
  const nearBottom = useRef(true);
  const seenReminders = useRef<{ sessionId: string; ids: Set<string> }>({ sessionId: '', ids: new Set() });
  const viewOwner = useRef<number | undefined>(undefined);
  const feedback = chat.replyFeedback;
  const showFeedback = feedback && feedback.phase !== 'complete' && !(feedback.phase === 'sending' && chat.silent)
    && !(feedback.phase === 'delayed' && feedback.message?.startsWith('消息发送状态待确认'));
  const showCloudError = !!chat.error && !feedback;
  const showLoadingNotice = chat.slowLoading && !chat.error;
  const emptyMode = !chat.messages.length && !feedback ? chat.loading ? 'loading' : !chat.error ? 'welcome' : '' : '';
  const mountedMessages = useMountedHistory(chat.messages,
    `${account.account?.user.userId ?? ''}:${account.account?.binding?.sessionId ?? ''}`,
    account.ready && !!account.account?.binding && !!state && !account.onboardingStep);
  const notify = useCallback((message: string) => {
    setToast(message);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setToast(''), 4200);
  }, []);
  const login = async (username: string, password: string) => {
    await account.login(username, password);
    setView('we');
    setShowBindPage(false);
    setEditProfileInitially(false);
    setTool(null);
    setPreviewImage(null);
  };

  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => {
    const userId = account.account?.user.userId;
    if (viewOwner.current === userId) return;
    viewOwner.current = userId;
    clearTimeout(timer.current);
    setView('we'); setEditProfileInitially(false); setTool(null); setPreviewImage(null); setToast('');
    setShowBindPage(Boolean(Taro.getCurrentInstance().router?.params.invite || Taro.getStorageSync('tietie.pendingInvite')));
    setChatScrollTarget(''); setPullRefreshing(false);
    seenReminders.current = { sessionId: '', ids: new Set() };
    nearBottom.current = true; lastChat.current = { id: '', view: '' };
  }, [account.account?.user.userId]);
  useEffect(() => { setComposerDismissSignal((value) => value + 1); }, [view, tool, previewImage]);
  useEffect(() => {
    if (account.account?.binding && !chat.loading && !chat.busy) void reloadAnniversaries.current();
  }, [account.account?.binding?.sessionId, chat.messages.at(-1)?.id, chat.loading, chat.busy, view, tool]);
  const showLatest = useCallback(() => {
    const target = ++scrollSequence.current % 2 ? 'chat-bottom-a' : 'chat-bottom-b';
    nextFrame(() => {
      if (process.env.TARO_ENV === 'h5') {
        // DOM scrollIntoView also scrolls Taro's outer page and hides its header.
        const scroller = document.getElementById('chat-scroll');
        if (scroller) scroller.scrollTop = scroller.scrollHeight;
      } else setChatScrollTarget(target);
    });
  }, []);
  useEffect(() => {
    const changedSession = lastChat.current.id !== chat.selectedId;
    const openedChat = view === 'we' && lastChat.current.view !== 'we';
    if (changedSession || openedChat) nearBottom.current = true;
    if (view === 'we' && (chat.messages.length || chat.replyFeedback) && (nearBottom.current || chat.submitting)) showLatest();
    lastChat.current = { id: chat.selectedId ?? '', view };
  }, [chat.selectedId, chat.messages, mountedMessages.length, chat.replyFeedback?.phase, chat.replyFeedback?.message, chat.submitting, view, !!state, showLatest, appHeight]);
  useEffect(() => {
    const frame = nextFrame(() => Taro.createSelectorQuery().select('#chat-scroll').boundingClientRect((rect) => {
      const bounds = Array.isArray(rect) ? rect[0] : rect;
      if (bounds) chatViewportHeight.current = bounds.height;
    }).exec());
    return () => cancelFrame(frame);
  }, [view, appHeight, !!state]);
  useEffect(() => onAppVisibilityChange((visible) => {
    if (visible && view === 'we') { nearBottom.current = true; showLatest(); }
  }), [view, showLatest]);
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
      if ((message.source === 'reminder' || message.source === 'reminder_update') && userId && message.recipientIds?.includes(userId)
        && Date.now() - Date.parse(message.createdAt ?? '') < 120_000
        && (view !== 'we' || !nearBottom.current)) notify(`${message.source === 'reminder' ? '消息提醒' : '提醒状态更新'}：${message.text.slice(0, 80)}`);
    }
  }, [chat.selectedId, chat.loading, chat.messages, account.account?.user.userId, view, notify]);

  if (!account.ready) return <div className="app-shell loading-screen"><div className="brand-mark"><img src={assetUrl('/brand-notes.png')} alt="" /></div><h1>贴贴清单</h1><p>{account.error || '正在打开贴贴清单…'}</p>{account.error && <button className="primary-button" onClick={() => void account.reload()}>再试一次</button>}</div>;
  if (!account.account) return <LoginPage onLogin={login} onRegister={account.register} notify={notify} />;
  if (!state) return <div className="app-shell loading-screen"><div className="brand-mark"><img src={assetUrl('/brand-notes.png')} alt="" /></div><h1>贴贴清单</h1><p>{error || '正在打开贴贴清单…'}</p>{error && <button className="primary-button" onClick={() => void reload()}>再试一次</button>}</div>;
  if (account.onboardingStep) return <Onboarding key={account.account.user.userId} step={account.onboardingStep} username={account.account.user.username} member={state.members.find((member) => member.id === 'self')!} onSaveMember={saveMember} onNext={account.advanceOnboarding} onDone={() => { setView('we'); setShowBindPage(false); account.finishOnboarding(); }} notify={notify} toast={toast} />;

  const ai = state.members.find((m) => m.id === 'ai')!;
  const proactiveFeedback = feedback?.phase === 'proactive_reminder' || feedback?.phase === 'proactive_update';
  const feedbackText = feedback?.phase === 'error' ? feedback.message || '这次回复遇到问题，请重试。'
    : feedback?.phase === 'sent' ? '消息已发给对方'
      : feedback?.phase === 'stopped' ? '已停止'
        : feedback?.phase === 'stopping' ? '正在停止…'
          : feedback?.phase === 'proactive_reminder' ? `${ai.name}正在发送消息提醒`
            : feedback?.phase === 'proactive_update' ? `${ai.name}正在告诉你提醒的变化`
              : feedback?.phase === 'delayed' ? '回复还需要一点时间'
                : feedback?.phase === 'syncing' ? `${ai.name}正在整理回复`
                  : feedback?.phase === 'thinking' || feedback?.phase === 'replying' ? `${ai.name}正在回复`
                    : `${ai.name}正在准备回复`;
  const selfId = account.account.user.userId;
  const partnerId = account.account.binding?.partnerId;
  const sharedMembers = state.members.map((member) => {
    if (member.id === 'ai') return member;
    const userId = member.id === 'self' ? selfId : partnerId;
    const verified = chat.members.find((item) => item.userId === userId);
    return { ...member, userId, name: member.id === 'self' ? member.name : member.profileName || verified?.name || '另一位成员' };
  });
  const reminders: Reminder[] = chat.reminders.map((reminder) => ({
    id: reminder.id, title: reminder.title, time: reminder.dueAt,
    recipientIds: reminder.recipientIds, status: reminder.status,
    deliveredAt: reminder.deliveredAt, taskStatus: reminder.taskStatus, taskCompletedAt: reminder.taskCompletedAt, updatedAt: reminder.updatedAt, recurrence: reminder.recurrence,
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
      ? `已完成「${reminder.title}」，贴贴会在聊天里确认。`
      : `已将「${reminder.title}」恢复为待完成，贴贴会在聊天里确认。`);
  };
  const cancelReminder = (id: string) => chat.changeReminder(id, 'cancelled');
  const deleteReminder = async (id: string) => { await chat.deleteReminder(id); notify('提醒已删除，相关记忆正在同步'); };
  const addReminder = async (input: Omit<Reminder, 'id' | 'completed'>) => {
    if (!account.account?.binding) throw new Error('请先绑定共享空间，再添加提醒。');
    const due = new Date(input.time ?? '');
    if (Number.isNaN(due.getTime()) || due.getTime() <= Date.now()) throw new Error('请选择一个未来的提醒时间。');
    const recipientIds = input.assignee === 'both' ? [selfId, account.account.binding.partnerId]
      : [input.assignee === 'self' ? selfId : account.account.binding.partnerId];
    await chat.saveReminder({ title: input.title, dueAt: due.toISOString(), recipientIds, recurrence: input.recurrence });
  };
  const sendMessage = async (text: string, files: MiniFile[] = [], visibility: 'shared' | 'private' = 'shared') => {
    if (!chat.canSend) throw new Error(chat.error || (chat.awaitingAsk ? '云端助手在等你回答上面那道选择题，先选一个才能继续。' : chat.busy ? '伙伴还在回复，等这一轮结束后再发送吧。' : '请先加载一个可聊天的云端会话。'));
    const partner = sharedMembers.find((member) => member.id === 'partner');
    return chat.sendMessage(text, files, visibility, !!partner && mentionRanges(text, [partner.name]).length > 0);
  };


  return <div className={`app-shell view-${view} ${view !== 'details' ? 'has-bottom-nav' : ''}${keyboardOpen ? ' keyboard-open' : ''}`} data-view={view} style={{ '--app-height': `${appHeight}px`, '--app-top': '0px', ...(nativeHistory ? { height: `${appHeight}px` } : {}) } as CSSProperties}>
    <div className="chat-view" hidden={view !== 'we'} style={nativeHistory && view !== 'we' ? { display: 'none' } : undefined}>
      {account.account.binding ? <>
      <header className="chat-header" onClick={() => setComposerDismissSignal((value) => value + 1)}><div className="chat-heading"><h1>我们</h1></div><button className="icon-button details-button" aria-label="查看角色信息" onClick={() => setView('details')}><Ellipsis size={18} /></button></header>
      {(nativeHistory || showCloudError) && <div hidden={!showCloudError} style={nativeHistory && !showCloudError ? { display: 'none' } : undefined} className="cloud-error" role="alert">{showCloudError && <><span>{chat.error}</span><button disabled={chat.submitting} onClick={() => void chat.reload()}>重试连接</button></>}</div>}
      {(nativeHistory || showLoadingNotice) && <div hidden={!showLoadingNotice} style={nativeHistory && !showLoadingNotice ? { display: 'none' } : undefined} className="cloud-error cloud-loading-notice" role="status">{showLoadingNotice && <><span>连接云端用时较长，仍在尝试。超过 30 秒会停止等待并提示重试。</span><button onClick={() => void chat.reload()}>重新连接</button></>}</div>}
      <ScrollView id="chat-scroll" className={`chat-scroll ${!chat.messages.length && !feedback ? 'is-empty' : ''}`} scrollY
        scrollIntoView={nativeHistory ? chatScrollTarget : undefined} scrollWithAnimation={false} enhanced enableFlex showScrollbar={false}
        onTouchStart={() => setComposerDismissSignal((value) => value + 1)}
        refresherEnabled refresherTriggered={pullRefreshing || chat.refreshing} onRefresherRefresh={async () => {
          setPullRefreshing(true);
          await new Promise<void>((resolve) => nextFrame(resolve));
          try { await chat.reload(); } finally { setPullRefreshing(false); }
        }}
        onScroll={(event) => { if (!chat.loading) nearBottom.current = event.detail.scrollHeight - event.detail.scrollTop - chatViewportHeight.current < 100; }}>

        {(nativeHistory || emptyMode) && <div hidden={!emptyMode} style={nativeHistory && !emptyMode ? { display: 'none' } : undefined} className={`cloud-empty${emptyMode === 'welcome' ? ' chat-welcome' : ''}`} role={emptyMode === 'loading' ? 'status' : undefined}>{emptyMode === 'loading' ? <><span className="spinner" /><p>{chat.slowLoading ? '云端连接较慢，正在继续尝试…' : '正在找回我们聊过的话…'}</p></> : emptyMode === 'welcome' ? <><span className="welcome-eyebrow"><Sparkles size={13} />我们的共享空间</span><SpaceBuddy variant="blue" className="chat-welcome-buddy" /><h2>共同的提醒，日常的分享</h2>{!chat.session && <p>正在准备我们的共享空间…</p>}</> : null}</div>}
        <div className="messages">{mountedMessages.map((message, index) => <Fragment key={message.renderKey ?? message.id}>
          {(index === 0 || messageDay(mountedMessages[index - 1].createdAt) !== messageDay(message.createdAt)) && <div className="chat-date"><span /><strong>{messageDayLabel(message.createdAt)}</strong><span /></div>}
          <ChatMessage message={message} members={sharedMembers} onError={notify} onOpenImage={(src, alt) => setPreviewImage({ src, alt })} onAnswer={chat.answerAsk} answerDisabled={chat.submitting || chat.busy} onLayoutChange={() => { if (nearBottom.current) showLatest(); }} />
        </Fragment>)}</div>
        {(nativeHistory || showFeedback) && <div hidden={!showFeedback} style={nativeHistory && !showFeedback ? { display: 'none' } : undefined} className={`assistant-feedback${feedback?.phase === 'error' ? ' is-error' : ''}${proactiveFeedback ? ' is-proactive' : ''}`} role={feedback?.phase === 'error' ? 'alert' : 'status'} aria-live="polite">{showFeedback && <><Avatar member={ai} /><div className="assistant-feedback-bubble">{proactiveFeedback && <Bell size={13} aria-hidden="true" />}<span>{feedbackText}</span>{feedback.phase !== 'sent' && feedback.phase !== 'stopped' && feedback.phase !== 'stopping' && feedback.phase !== 'error' && <span className="typing-dots" aria-hidden="true"><i /><i /><i /></span>}{chat.error && <button className="assistant-feedback-retry" disabled={chat.loading || chat.refreshing || chat.submitting} onClick={() => void chat.reload()}>重新同步</button>}</div></>}</div>}
        {(nativeHistory || chat.turnError && !feedback) && <div hidden={!chat.turnError || !!feedback} style={nativeHistory && (!chat.turnError || !!feedback) ? { display: 'none' } : undefined} className="turn-error" role="alert">{chat.turnError && !feedback ? chat.turnError : ''}</div>}
        {(nativeHistory || chat.remindersError) && <div hidden={!chat.remindersError} style={nativeHistory && !chat.remindersError ? { display: 'none' } : undefined} className="turn-error" role="alert">{chat.remindersError}</div>}
        <View id="chat-bottom-a" style={{ height: '1px' }} /><View id="chat-bottom-b" style={{ height: '1px' }} />
      </ScrollView>
      <Composer dismissSignal={composerDismissSignal} members={sharedMembers} key={`${account.account.user.userId}:${chat.selectedId ?? 'no-session'}`} sending={chat.submitting} processing={chat.canStop} stopping={chat.stopping} disabled={!chat.canSend} disabledReason={!chat.loaded ? chat.error ? '连接失败，请点上方重试' : '正在连接云端…' : chat.error ? '同步失败，请点上方重试' : chat.awaitingAsk ? '先回答上面的选择题' : chat.busy ? '云端正在处理，请稍候' : undefined} placeholder={chat.awaitingAsk ? '先回答上面那道选择题…' : chat.busy ? (chat.silent ? '消息已发给对方，可以先写下一句…' : '伙伴正在回复，可以先写下一句…') : '记下一个共同提醒…'} onSend={sendMessage} onStop={chat.stopTurn} onTool={setTool} onError={notify} />
      </> : showBindPage ? <BindPage code={account.account.user.code} onBind={async (code) => { await account.bind(code); setShowBindPage(false); }} onBack={() => setShowBindPage(false)} notify={notify} embedded /> : <>
        <header className="chat-header" onClick={() => setComposerDismissSignal((value) => value + 1)}><div className="chat-heading"><h1>我们</h1></div></header>
        <main className="chat-scroll is-empty unbound-home-scroll" aria-label="我们的空间">
          <div className="cloud-empty chat-welcome"><span className="welcome-eyebrow"><Sparkles size={13} />我们的共享空间</span><SpaceBuddy variant="blue" className="chat-welcome-buddy" /><h2>共同的提醒，日常的分享</h2><p>连接 TA 后，就能一起聊天、安排提醒。</p><button type="button" className="primary-button unbound-home-bind" onClick={() => setShowBindPage(true)}>连接彼此 <ArrowRight size={16} aria-hidden="true" /></button></div>
        </main>
      </>}
    </div>
    <NativeSlot shown={view === 'things'} panel>{view === 'things' && <LittleThings sessionId={account.account.binding?.sessionId} selfId={selfId} onEditRegion={() => { setEditProfileInitially(true); setView('mine'); }} onBind={() => { setShowBindPage(true); setView('we'); }} state={state} anniversaries={anniversaries} reminderState={sharedState} onToggle={toggleReminder} onCancel={cancelReminder} onDelete={deleteReminder} remindersLoading={chat.loading} reminderNotice={!account.account.binding ? '绑定两人空间后，可以一起安排和查看提醒。' : chat.remindersError || chat.error} onReloadReminders={account.account.binding ? chat.reload : undefined} notify={notify} />}</NativeSlot>
    <NativeSlot shown={view === 'mine'} panel>{view === 'mine' && <Mine editProfileInitially={editProfileInitially} state={state} username={account.account.user.username} code={account.account.user.code} hasSession={!!account.account.binding} onSaveMember={saveMember} onLogout={account.logout} onExitSession={account.unbind} notify={notify} />}</NativeSlot>
    <NativeSlot shown={view === 'details'} panel>{view === 'details' && <Details state={sharedState} sessionId={account.account.binding?.sessionId} onBack={() => setView('we')} onSaveMember={saveMember} onSaveSettings={saveSettings} notify={notify} />}</NativeSlot>
    <NativeSlot shown={view !== 'details'}>{view !== 'details' && <BottomNav view={view} onChange={(next) => { setEditProfileInitially(false); setShowBindPage(false); setView(next); }} />}</NativeSlot>
    <NativeSlot shown={!!tool} overlay>{tool && <Tools tool={tool} state={sharedState} anniversaries={anniversaries} onClose={() => setTool(null)} onAdd={addReminder} notify={notify} />}</NativeSlot>
    <NativeSlot shown={!!previewImage} overlay>{previewImage && <ImageViewer src={previewImage.src} alt={previewImage.alt} onClose={() => setPreviewImage(null)} />}</NativeSlot>
    <NativeSlot shown={!!toast} overlay>{toast && <div className="toast" role="status"><Sparkles size={16} />{toast}</div>}</NativeSlot>
  </div>;
}
