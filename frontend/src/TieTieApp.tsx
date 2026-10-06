import { Bell, Ellipsis, Sparkles } from './components/Icons';
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
import { WechatReminderBanner } from './components/WechatReminderBanner';
import { MiniPageHeader } from './components/MiniPageHeader';
import { Onboarding } from './components/Onboarding';
import { Tools, type ToolName } from './components/Tools';
import { useAccount } from './hooks/useAccount';
import { useRelationship } from './hooks/useRelationship';
import { useCloudChat } from './hooks/useCloudChat';
import { useViewportHeight } from './hooks/useViewportHeight';
import { useAnniversaries } from './hooks/useAnniversaries';
import type { Message, Reminder } from './types';
import { matchesMountedPrefix, mountedMessageKey, nextHistoryBatchEnd } from './lib/chatMounting';
import { reminderLaunchStore } from './lib/reminderLaunch';
import { wechatSubscriptionApi, type WechatSubscription } from './api/wechat-subscription';

const nativeHistory = process.env.TARO_ENV === 'weapp';
type AppView = 'we' | 'things' | 'mine' | 'details';

/** Stable sibling slots stop a tab/overlay change from re-hydrating chat. */
function NativeSlot({ children, shown, panel = false, overlay = false }: { children: ReactNode; shown: boolean; panel?: boolean; overlay?: boolean }) {
  if (!nativeHistory && !panel) return shown ? <>{children}</> : null;
  // Keep native pages mounted in a fixed layer. `hidden`/`display:none` makes
  // WeChat recalculate a ScrollView's viewport as zero, which clamps its
  // scrollTop and makes returning to a tab visibly jump.
  const style = panel
    ? {
      position: 'absolute' as const, top: '0px', right: '0px', bottom: '0px', left: '0px', display: 'flex', flexDirection: 'column' as const,
      minHeight: '0px', width: '100%', visibility: shown ? 'visible' as const : 'hidden' as const,
      pointerEvents: shown ? 'auto' as const : 'none' as const, zIndex: shown ? 1 : 0,
    }
    : { display: shown ? 'block' : 'none', flexShrink: 0, ...(overlay ? { height: '0px' } : {}) };
  return <View style={style}>{children}</View>;
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
    // Keep the rows that are already on screen when history is reconciled.
    // A cached preview is often replaced by the complete history while an AI
    // reply is arriving; clearing the mounted list first makes the reply
    // bubble disappear for a frame and then flash back in. Re-key the mounted
    // prefix in place and append the remaining rows in bounded frames.
    if (!active || progress.current.owner !== owner) {
      progress.current = { owner, keys: [] };
      setMounted({ owner, count: 0 });
    } else if (!matchesMountedPrefix(progress.current.keys, messages)) {
      const keepCount = Math.min(mounted.count, messages.length);
      progress.current = { owner, keys: messages.slice(0, keepCount).map(mountedMessageKey) };
      if (keepCount !== mounted.count) setMounted({ owner, count: keepCount });
    }
    if (active) frame = nextFrame(advance);
    return () => { alive = false; if (frame !== undefined) cancelFrame(frame); };
  }, [messages, owner, active]);
  if (process.env.TARO_ENV !== 'weapp') return messages;
  // During an in-place history reconciliation the previous prefix remains
  // visible until the bounded append catches up. Returning an empty list here
  // causes a visible flash in the chat while the new AI row is being mounted.
  return active && mounted.owner === owner ? messages.slice(0, Math.min(mounted.count, messages.length)) : [];
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
  const [reminderLaunch, setReminderLaunch] = useState(reminderLaunchStore.get);
  const { state, error, reload, saveMember, saveSettings } = useRelationship(account.account?.user.userId, account.account?.binding?.partnerId, account.account?.binding?.sessionId, account.account?.user.username);
  const chat = useCloudChat(account.account?.binding?.sessionId ?? null, account.reload, account.account?.user.userId);
  const anniversaries = useAnniversaries(account.account?.binding?.sessionId);
  const reloadAnniversaries = useRef(anniversaries.reload); reloadAnniversaries.current = anniversaries.reload;
  const [view, setView] = useState<AppView>('we');
  const [visitedViews, setVisitedViews] = useState<Set<AppView>>(() => new Set(['we']));
  const [wechatSubscription, setWechatSubscription] = useState<WechatSubscription | null>(null);
  const [tool, setTool] = useState<ToolName | null>(null);
  const [previewImage, setPreviewImage] = useState<{ src: string; alt: string } | null>(null);
  const [toast, setToast] = useState('');
  const [composerDismissSignal, setComposerDismissSignal] = useState(0);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [chatScrollTop, setChatScrollTop] = useState(0);
  const [pullRefreshing, setPullRefreshing] = useState(false);
  const scrollSequence = useRef(0);
  const scrollFrame = useRef<number | undefined>(undefined);
  const chatViewportHeight = useRef(400);
  const lastChat = useRef({ id: '', view: '' });
  const nearBottom = useRef(true);
  // Native scroll-view emits onScroll while scrollIntoView settles. Keep
  // that short programmatic window separate from a real touch gesture so
  // automatic layout updates do not pull the user away from a message.
  const chatTouching = useRef(false);
  const chatTouchStartY = useRef<number | null>(null);
  const autoScrollUntil = useRef(0);
  const followLatest = useRef(false);
  const userScrolledUp = useRef(false);
  const seenReminders = useRef<{ sessionId: string; ids: Set<string> }>({ sessionId: '', ids: new Set() });
  const viewOwner = useRef<number | undefined>(undefined);
  const goToView = useCallback((next: AppView) => {
    setVisitedViews((current) => current.has(next) ? current : new Set([...current, next]));
    setView(next);
  }, []);
  const mountedMessages = useMountedHistory(chat.messages,
    `${account.account?.user.userId ?? ''}:${account.account?.binding?.sessionId ?? ''}`,
    account.ready && !!account.account?.binding && !!state && !account.onboardingStep);
  const feedback = chat.replyFeedback;
  // In the mini program a newly received message may spend one bounded frame
  // entering the native history. Keep the completed reply indicator visible
  // during that handoff so the AI status never flashes away before its bubble.
  const waitingForMessageMount = feedback?.phase === 'complete' && mountedMessages.length < chat.messages.length;
  const showFeedback = feedback && (feedback.phase !== 'complete' || waitingForMessageMount)
    && !chat.error && !chat.turnError
    && !(feedback.phase === 'sending' && chat.silent)
    && !(feedback.phase === 'delayed' && feedback.message?.startsWith('消息发送状态待确认'));
  const activeFeedback = !!feedback && feedback.phase !== 'complete';
  const showCloudError = !!chat.error && !activeFeedback;
  const showLoadingNotice = chat.slowLoading && !chat.error;
  const emptyMode = !chat.messages.length && !activeFeedback ? chat.loading ? 'loading' : !chat.error ? 'welcome' : '' : '';
  const notify = useCallback((message: string) => {
    setToast(message);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setToast(''), 4200);
  }, []);
  const login = async (username: string, password: string) => {
    await account.login(username, password);
    goToView('we');
    setEditProfileInitially(false);
    setTool(null);
    setPreviewImage(null);
  };

  useEffect(() => () => {
    clearTimeout(timer.current);
    if (scrollFrame.current !== undefined) cancelFrame(scrollFrame.current);
  }, []);
  useEffect(() => {
    setReminderLaunch(reminderLaunchStore.get());
    return reminderLaunchStore.subscribe(setReminderLaunch);
  }, []);
  useEffect(() => {
    const userId = account.account?.user.userId;
    if (viewOwner.current === userId) return;
    viewOwner.current = userId;
    clearTimeout(timer.current);
    setView('we'); setVisitedViews(new Set(['we'])); setEditProfileInitially(false); setTool(null); setPreviewImage(null); setToast('');
    setChatScrollTop(0); setPullRefreshing(false);
    seenReminders.current = { sessionId: '', ids: new Set() };
    nearBottom.current = true; userScrolledUp.current = false; followLatest.current = false; autoScrollUntil.current = 0;
    lastChat.current = { id: '', view: '' };
  }, [account.account?.user.userId]);
  useEffect(() => {
    if (!nativeHistory || !account.account) {
      setWechatSubscription(null);
      return;
    }
    let active = true;
    void wechatSubscriptionApi.get().then((status) => {
      if (active) setWechatSubscription(status);
    }).catch(() => {
      if (active) setWechatSubscription(null);
    });
    return () => { active = false; };
  }, [account.account?.user.userId]);
  useEffect(() => { setComposerDismissSignal((value) => value + 1); }, [view, tool, previewImage]);
  useEffect(() => {
    if (account.account?.binding && !chat.loading && !chat.busy) void reloadAnniversaries.current();
  }, [account.account?.binding?.sessionId, chat.messages.at(-1)?.id, chat.loading, chat.busy, view, tool]);
  const showLatest = useCallback(() => {
    if (userScrolledUp.current || chatTouching.current) return;
    nearBottom.current = true;
    // Ignore the transient native onScroll event produced by this request.
    autoScrollUntil.current = Date.now() + (followLatest.current ? 5000 : 650);
    const request = ++scrollSequence.current;
    if (scrollFrame.current !== undefined) cancelFrame(scrollFrame.current);
    scrollFrame.current = nextFrame(() => {
      scrollFrame.current = undefined;
      if (userScrolledUp.current || chatTouching.current) return;
      if (process.env.TARO_ENV === 'h5') {
        // Scroll the chat element itself so the page header stays in place.
        const scroller = document.getElementById('chat-scroll');
        if (scroller) {
          const top = Math.max(0, scroller.scrollHeight - scroller.clientHeight);
          if (typeof scroller.scrollTo === 'function') scroller.scrollTo({ top, behavior: 'smooth' });
          else scroller.scrollTop = top;
        }
      } else {
        // A large, changing scrollTop is more reliable than scrollIntoView on
        // older WeChat bases when native rows are still being appended.
        setChatScrollTop(1_000_000 + request);
      }
    });
  }, []);
  useEffect(() => {
    if (feedback?.phase !== 'complete' || waitingForMessageMount) return;
    chat.clearReplyFeedback();
  }, [feedback?.phase, waitingForMessageMount, chat.clearReplyFeedback]);
  useEffect(() => {
    if (!chat.submitting && !chat.busy) followLatest.current = false;
  }, [chat.submitting, chat.busy]);
  useEffect(() => {
    if (!reminderLaunch || !account.ready || !account.account) return;
    goToView('we'); setTool(null); setPreviewImage(null); setEditProfileInitially(false);
    setComposerDismissSignal(value => value + 1);
    nearBottom.current = true;
    userScrolledUp.current = false;
    if (account.account.binding?.sessionId === reminderLaunch.sessionId) {
      void chat.reload();
      showLatest();
    } else notify('这个提醒对应的空间已结束，或不属于当前账号。');
    reminderLaunchStore.consume(reminderLaunch.sequence);
    setReminderLaunch(null);
  }, [reminderLaunch, account.ready, account.account, chat.reload, showLatest, notify, goToView]);
  useEffect(() => {
    const changedSession = lastChat.current.id !== chat.selectedId;
    const openedChat = view === 'we' && lastChat.current.view !== 'we';
    if (changedSession) {
      nearBottom.current = true;
      userScrolledUp.current = false;
      followLatest.current = false;
      autoScrollUntil.current = Date.now() + 260;
    } else if (openedChat) {
      // The chat layer stays mounted while another tab is visible. Preserve a
      // deliberate reading position across that tab switch; only a session
      // change resets it to the latest message.
      autoScrollUntil.current = Date.now() + 260;
    }
    // The mini-program mounts a long history in bounded batches. Waiting for
    // the final batch avoids landing halfway through the list while those
    // rows are still being appended.
    const historyMounted = !nativeHistory || mountedMessages.length === chat.messages.length;
    if (view === 'we' && historyMounted && (chat.messages.length || chat.replyFeedback)
      && !userScrolledUp.current && (nearBottom.current || chat.submitting)) showLatest();
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
    if (visible && view === 'we' && !userScrolledUp.current) { nearBottom.current = true; showLatest(); }
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
  if (!account.account) return <LoginPage onLogin={login} onRegister={account.register} onWechatLogin={account.wechatLogin} notify={notify} />;
  if (!state) return <div className="app-shell loading-screen"><div className="brand-mark"><img src={assetUrl('/brand-notes.png')} alt="" /></div><h1>贴贴清单</h1><p>{error || '正在打开贴贴清单…'}</p>{error && <button className="primary-button" onClick={() => void reload()}>再试一次</button>}</div>;
  if (account.onboardingStep) return <Onboarding key={account.account.user.userId} step={account.onboardingStep} username={account.account.user.username} member={state.members.find((member) => member.id === 'self')!} onSaveMember={saveMember} onNext={account.advanceOnboarding} onDone={() => { goToView('we'); account.finishOnboarding(); }} notify={notify} toast={toast} />;

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
                  : feedback?.phase === 'thinking' || feedback?.phase === 'replying' || feedback?.phase === 'complete' ? `${ai.name}正在回复`
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
    // Sending is an explicit request for the newest message. Clear the
    // manual-read lock before the optimistic row is inserted, otherwise the
    // normal "preserve my position" guard wins and the list oscillates.
    userScrolledUp.current = false;
    nearBottom.current = true;
    followLatest.current = true;
    chatTouching.current = false;
    autoScrollUntil.current = Date.now() + 1200;
    const pending = chat.sendMessage(text, files, visibility, !!partner && mentionRanges(text, [partner.name]).length > 0);
    showLatest();
    return pending;
  };


  const showWechatReminderBanner = nativeHistory && view === 'we' && chat.loaded && chat.messages.length > 0 && wechatSubscription?.remaining === 0;
  return <div className={`app-shell view-${view} ${view !== 'details' ? 'has-bottom-nav' : ''}${keyboardOpen ? ' keyboard-open' : ''}${showWechatReminderBanner ? ' has-wechat-reminder' : ''}`} data-view={view} style={{ '--app-height': `${appHeight}px`, '--app-top': '0px', ...(nativeHistory ? { height: `${appHeight}px` } : {}) } as CSSProperties}>
    <NativeSlot shown={showWechatReminderBanner} overlay><WechatReminderBanner onOpen={() => goToView('mine')} /></NativeSlot>
    {nativeHistory && <NativeSlot shown={view !== 'details'} overlay>
      <MiniPageHeader title={view === 'mine' ? '我的' : view === 'things' ? '提醒' : '我们'} onDismiss={() => setComposerDismissSignal(value => value + 1)} onAction={view === 'we' && account.account.binding ? () => goToView('details') : undefined} />
    </NativeSlot>}
    <div className="app-view-stack">
    <div className={`chat-view${nativeHistory && account.account.binding ? ' mini-chat-content' : ''}`} style={nativeHistory
      ? { position: 'absolute', top: '0px', right: '0px', bottom: '0px', left: '0px', display: 'flex', visibility: view === 'we' ? 'visible' : 'hidden', pointerEvents: view === 'we' ? 'auto' : 'none', zIndex: view === 'we' ? 1 : 0 }
      : view !== 'we' ? { display: 'none' } : undefined}>
      {account.account.binding ? <>
      {!nativeHistory && <header className="chat-header" onClick={() => setComposerDismissSignal((value) => value + 1)}><div className="chat-heading"><h1>我们</h1></div><button className="icon-button details-button" aria-label="查看角色信息" onClick={() => goToView('details')}><Ellipsis size={18} /></button></header>}
      {(nativeHistory || showCloudError) && <div hidden={!showCloudError} style={nativeHistory && !showCloudError ? { display: 'none' } : undefined} className="cloud-error" role="alert">{showCloudError && <><span>{chat.error}</span><button disabled={chat.submitting} onClick={() => void chat.reload()}>重试连接</button></>}</div>}
      {(nativeHistory || showLoadingNotice) && <div hidden={!showLoadingNotice} style={nativeHistory && !showLoadingNotice ? { display: 'none' } : undefined} className="cloud-error cloud-loading-notice" role="status">{showLoadingNotice && <><span>连接云端用时较长，仍在尝试。超过 30 秒会停止等待并提示重试。</span><button onClick={() => void chat.reload()}>重新连接</button></>}</div>}
      <ScrollView id="chat-scroll" className={`chat-scroll ${!chat.messages.length && !activeFeedback ? 'is-empty' : ''}`} scrollY
        scrollTop={nativeHistory ? chatScrollTop : undefined} scrollWithAnimation enhanced enableFlex showScrollbar={false}
        refresherDefaultStyle="black" refresherBackground="transparent"
        onTouchStart={(event) => {
          chatTouching.current = true;
          autoScrollUntil.current = 0;
          const touch = (event as unknown as { touches?: Array<{ clientY?: number }> }).touches?.[0];
          chatTouchStartY.current = Number.isFinite(touch?.clientY) ? touch!.clientY! : null;
          setComposerDismissSignal((value) => value + 1);
        }}
        onTouchMove={(event) => {
          const touch = (event as unknown as { touches?: Array<{ clientY?: number }> }).touches?.[0];
          const y = touch?.clientY;
          if (chatTouching.current && chatTouchStartY.current !== null && Number.isFinite(y) && y! < chatTouchStartY.current - 8) {
            nearBottom.current = false;
            userScrolledUp.current = true;
            followLatest.current = false;
            autoScrollUntil.current = 0;
          }
        }}
        onTouchEnd={() => { chatTouching.current = false; chatTouchStartY.current = null; }}
        onTouchCancel={() => { chatTouching.current = false; chatTouchStartY.current = null; }}
        refresherEnabled refresherTriggered={pullRefreshing || chat.refreshing} onRefresherRefresh={async () => {
          setPullRefreshing(true);
          await new Promise<void>((resolve) => nextFrame(resolve));
          try { await chat.reload(); } finally { setPullRefreshing(false); }
        }}
        onScroll={(event) => {
          const keepFollowing = followLatest.current && (chat.submitting || chat.busy);
          if (chat.loading || (!chatTouching.current && (Date.now() < autoScrollUntil.current || keepFollowing))) return;
          const detail = event.detail as { scrollHeight?: number; scrollTop?: number; deltaY?: number };
          const deltaY = Number(detail.deltaY);
          const scrollTop = Number(detail.scrollTop);
          const scrollHeight = Number(detail.scrollHeight);
          // deltaY still identifies an upward gesture on older WeChat bases
          // where scrollHeight is not included in the event detail.
          if (chatTouching.current && Number.isFinite(deltaY) && deltaY < -1) {
            nearBottom.current = false;
            userScrolledUp.current = true;
            followLatest.current = false;
            return;
          }
          if (!Number.isFinite(scrollTop) || !Number.isFinite(scrollHeight) || scrollHeight <= 0) return;
          const atBottom = scrollHeight - scrollTop - chatViewportHeight.current < 100;
          nearBottom.current = atBottom;
          userScrolledUp.current = !atBottom;
        }}>

        {(nativeHistory || emptyMode) && <div hidden={!emptyMode} style={nativeHistory && !emptyMode ? { display: 'none' } : undefined} className={`cloud-empty${emptyMode === 'welcome' ? ' chat-welcome' : ''}`} role={emptyMode === 'loading' ? 'status' : undefined}>{emptyMode === 'loading' ? <><span className="spinner" /><p>{chat.slowLoading ? '云端连接较慢，正在继续尝试…' : '正在找回我们聊过的话…'}</p></> : emptyMode === 'welcome' ? <>
          <div className="chat-welcome-card">
            <span className="chat-welcome-icon" aria-hidden="true"><Sparkles size={15} /></span>
            <p className="chat-welcome-message">欢迎来到两人专属空间</p>
          </div>
        </> : null}</div>}
        <div className="messages">{mountedMessages.map((message, index) => <Fragment key={message.renderKey ?? message.id}>
          {(index === 0 || messageDay(mountedMessages[index - 1].createdAt) !== messageDay(message.createdAt)) && <div className="chat-date"><span /><strong>{messageDayLabel(message.createdAt)}</strong><span /></div>}
          <ChatMessage message={message} members={sharedMembers} onError={notify} onOpenImage={(src, alt) => setPreviewImage({ src, alt })} onAnswer={chat.answerAsk} answerDisabled={chat.submitting || chat.busy} onLayoutChange={() => { if (nearBottom.current) showLatest(); }} />
        </Fragment>)}</div>
        {(nativeHistory || showFeedback) && <div hidden={!showFeedback} style={nativeHistory && !showFeedback ? { display: 'none' } : undefined} className={`assistant-feedback${feedback?.phase === 'error' ? ' is-error' : ''}${proactiveFeedback ? ' is-proactive' : ''}`} role={feedback?.phase === 'error' ? 'alert' : 'status'} aria-live="polite">{showFeedback && <><Avatar member={ai} /><div className="assistant-feedback-bubble">{proactiveFeedback && <Bell size={13} aria-hidden="true" />}<span>{feedbackText}</span>{feedback.phase !== 'sent' && feedback.phase !== 'stopped' && feedback.phase !== 'stopping' && feedback.phase !== 'error' && <span className="typing-dots" aria-hidden="true"><i /><i /><i /></span>}{chat.error && <button className="assistant-feedback-retry" disabled={chat.loading || chat.refreshing || chat.submitting} onClick={() => void chat.reload()}>重新同步</button>}</div></>}</div>}
        {(nativeHistory || chat.turnError && !activeFeedback) && <div hidden={!chat.turnError || activeFeedback} style={nativeHistory && (!chat.turnError || activeFeedback) ? { display: 'none' } : undefined} className="turn-error" role="alert">{chat.turnError && !activeFeedback ? chat.turnError : ''}</div>}
        {(nativeHistory || chat.remindersError) && <div hidden={!chat.remindersError} style={nativeHistory && !chat.remindersError ? { display: 'none' } : undefined} className="turn-error" role="alert">{chat.remindersError}</div>}
        <View id="chat-bottom-a" style={{ height: '1px' }} /><View id="chat-bottom-b" style={{ height: '1px' }} />
      </ScrollView>
      <Composer dismissSignal={composerDismissSignal} members={sharedMembers} key={`${account.account.user.userId}:${chat.selectedId ?? 'no-session'}`} sending={chat.submitting} processing={chat.canStop} stopping={chat.stopping} disabled={!chat.canSend} disabledReason={!chat.loaded ? chat.error ? '连接失败，请点上方重试' : '正在连接云端…' : chat.error ? '同步失败，请点上方重试' : chat.awaitingAsk ? '先回答上面的选择题' : chat.busy ? '云端正在处理，请稍候' : undefined} placeholder={chat.awaitingAsk ? '先回答上面那道选择题…' : chat.busy ? (chat.silent ? '消息已发给对方，可以先写下一句…' : '伙伴正在回复，可以先写下一句…') : '记下一个共同提醒…'} onSend={sendMessage} onStop={chat.stopTurn} onTool={setTool} onError={notify} />
      </> : <BindPage code={account.account.user.code} onBind={account.bind} notify={notify} embedded showHeader={!nativeHistory} />}
    </div>
    <NativeSlot shown={view === 'things'} panel>{visitedViews.has('things') && <LittleThings sessionId={account.account.binding?.sessionId} selfId={selfId} onEditRegion={() => { setEditProfileInitially(true); goToView('mine'); }} state={state} anniversaries={anniversaries} reminderState={sharedState} onToggle={toggleReminder} onCancel={cancelReminder} onDelete={deleteReminder} remindersLoading={chat.loading} reminderNotice={account.account.binding ? chat.remindersError || chat.error : undefined} onReloadReminders={account.account.binding ? chat.reload : undefined} notify={notify} />}</NativeSlot>
    <NativeSlot shown={view === 'mine'} panel>{visitedViews.has('mine') && <Mine editProfileInitially={editProfileInitially} state={state} username={account.account.user.username} code={account.account.user.code} hasSession={!!account.account.binding} onSaveMember={saveMember} onLogout={account.logout} onExitSession={account.unbind} notify={notify} onWechatSubscriptionChange={setWechatSubscription} onEditProfileInitialHandled={() => setEditProfileInitially(false)} />}</NativeSlot>
    <NativeSlot shown={view === 'details'} panel>{visitedViews.has('details') && <Details state={sharedState} sessionId={account.account.binding?.sessionId} onBack={() => goToView('we')} onSaveMember={saveMember} onSaveSettings={saveSettings} notify={notify} />}</NativeSlot>
    </div>
    <NativeSlot shown={view !== 'details'}>{view !== 'details' && <BottomNav view={view} onChange={(next) => { setEditProfileInitially(false); goToView(next); }} />}</NativeSlot>
    <NativeSlot shown={!!tool} overlay>{tool && <Tools tool={tool} state={sharedState} anniversaries={anniversaries} onClose={() => setTool(null)} onAdd={addReminder} notify={notify} />}</NativeSlot>
    <NativeSlot shown={!!previewImage} overlay>{previewImage && <ImageViewer src={previewImage.src} alt={previewImage.alt} onClose={() => setPreviewImage(null)} />}</NativeSlot>
    <NativeSlot shown={!!toast} overlay>{toast && <div className="toast" role="status"><Sparkles size={16} />{toast}</div>}</NativeSlot>
  </div>;
}
