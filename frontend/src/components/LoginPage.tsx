import { ScrollView } from '@tarojs/components'
import { Input, Form, SubmitButton } from './Fields';
import { LogIn, MessageCircle, UserPlus } from './Icons';
import { useRef, useState, useSyncExternalStore, type CSSProperties, type FormEvent } from 'react';
import { PaperBuddyMotion } from './PaperBuddyMotion';
import { WechatProfileFields } from './WechatProfileFields';
import type { WechatLoginProfile } from '../api/wechat-login';
import { ApiError } from '../api/transport';
import { getLoginBackground, subscribeLoginBackground } from '../lib/loginBackground';

interface Props {
  onLogin: (username: string, password: string) => Promise<void>;
  onRegister: (username: string, password: string) => Promise<void>;
  onWechatLogin: (profile?: WechatLoginProfile) => Promise<void>;
  notify: (text: string) => void;
}

/** 小程序只显示微信登录，H5 保留现有账号入口。 */
export function LoginPage(props: Props) {
  return process.env.TARO_ENV === 'weapp' ? <WechatLoginPage onWechatLogin={props.onWechatLogin} notify={props.notify} /> : <PasswordLoginPage {...props} />;
}

function WechatLoginPage({ onWechatLogin, notify }: Pick<Props, 'onWechatLogin' | 'notify'>) {
  const background = useSyncExternalStore(subscribeLoginBackground, getLoginBackground, getLoginBackground);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [profileOpen, setProfileOpen] = useState(false);
  const [nickname, setNickname] = useState('');
  const [avatar, setAvatar] = useState('');
  const submitting = useRef(false);
  const startLogin = async () => {
    if (submitting.current) return;
    submitting.current = true;
    setBusy(true); setError('');
    try {
      await onWechatLogin();
      notify('欢迎回来');
    } catch (e) {
      if (e instanceof ApiError && e.status === 400 && e.code === 'wechat_profile_required') {
        setProfileOpen(true);
      } else {
        setError(e instanceof Error ? e.message : '微信登录失败，请稍后重试。');
      }
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  };
  const login = async (event: FormEvent) => {
    event.preventDefault();
    if (submitting.current) return;
    // Native nickname autofill may only flush its value when the form submits.
    const values = (event as unknown as { detail?: { value?: Record<string, unknown> } }).detail?.value;
    const name = (typeof values?.wechatNickname === 'string' ? values.wechatNickname : nickname).trim();
    setNickname(name);
    if (!name || !avatar) { setError('请先选择头像并填写昵称，再确认登录。'); return; }
    submitting.current = true;
    setBusy(true); setError('');
    try {
      await onWechatLogin({ nickname: name, avatarPath: avatar });
      notify('欢迎来到贴贴');
    } catch (e) {
      setError(e instanceof Error ? e.message : '微信登录失败，请稍后重试。');
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  };
  return <div className={`app-shell login-page login-wechat-page${profileOpen ? ' is-profile-open' : ''}`} style={{ '--login-background-image': `url("${background}")` } as CSSProperties}>
    <header className="login-wechat-header"><h1>贴贴清单</h1></header>
    <ScrollView scrollY enhanced showScrollbar={false} className="login-scroll">
      <div className="login-scroll-content">
        <div className="login-wechat-hero">
          <PaperBuddyMotion variant="bump" purpose="welcome" className="login-paper-buddies" paused={profileOpen || busy} />
          {!profileOpen && <div className="login-welcome-copy"><h2>把日常，贴在一起</h2><p>两个人的小事AI记</p></div>}
        </div>
        {profileOpen ? <Form className="login-form login-wechat-profile-form" onSubmit={event => void login(event)}>
          <div className="login-profile-heading"><h2>完善头像和昵称</h2><p>让彼此一眼认出你</p></div>
          <WechatProfileFields nickname={nickname} avatar={avatar} required disabled={busy}
            onNicknameChange={value => { setNickname(value); setError(''); }}
            onAvatarChange={value => { setAvatar(value); setError(''); }} onError={setError} />
          {error && <p className="login-error" role="alert">{error}</p>}
          <SubmitButton className="primary-button login-wechat-button" disabled={busy}>
            <MessageCircle size={18} aria-hidden="true" />{busy ? '正在保存并登录…' : '确认并登录'}
          </SubmitButton>
          <button type="button" className="login-profile-cancel" disabled={busy} onClick={() => {
            setProfileOpen(false); setNickname(''); setAvatar(''); setError('');
          }}>暂不登录</button>
        </Form> : <div className="login-form login-wechat-form">
          {error && <p className="login-error" role="alert">{error}</p>}
          <button type="button" className="primary-button login-wechat-button" disabled={busy} aria-busy={busy} onClick={() => void startLogin()}>
            <MessageCircle size={18} aria-hidden="true" />{busy ? '正在登录…' : '微信登录'}
          </button>
        </div>}
      </div>
    </ScrollView>
  </div>;
}

function PasswordLoginPage({ onLogin, onRegister, notify }: Props) {
  const [mode, setMode] = useState<'login' | 'register'>('login');
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [busy, setBusy] = useState(false);
  const submitting = useRef(false);
  const [error, setError] = useState('');

  const switchMode = (next: 'login' | 'register') => {
    setMode(next);
    setError('');
    setConfirm('');
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (submitting.current) return;
    const name = username.trim();
    if (!name || !password) {
      setError('请输入用户名和密码。');
      return;
    }
    if (mode === 'register' && password !== confirm) {
      setError('两次输入的密码不一致。');
      return;
    }
    submitting.current = true;
    setBusy(true);
    setError('');
    try {
      if (mode === 'register') {
        await onRegister(name, password);
        notify(`欢迎加入贴贴，${name}`);
      } else {
        await onLogin(name, password);
        notify(`欢迎回来，${name}`);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : '操作失败，请稍后重试。');
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  };

  return <div className="app-shell login-page">
    <ScrollView scrollY enhanced showScrollbar={false} className="login-scroll">
    <div className="login-scroll-content">
    <header className="login-header"><h1>贴贴清单</h1></header>
    <PaperBuddyMotion variant="bump" purpose="welcome" className="login-paper-buddies" paused={busy} />

    <div className="login-tabs" role="tablist">
      <button type="button" role="tab" disabled={busy} aria-selected={mode === 'login'} className={mode === 'login' ? 'is-active' : ''} onClick={() => switchMode('login')}>登录</button>
      <button type="button" role="tab" disabled={busy} aria-selected={mode === 'register'} className={mode === 'register' ? 'is-active' : ''} onClick={() => switchMode('register')}>注册</button>
    </div>

    <Form className="login-form" onSubmit={(event) => void submit(event)}>
      <Input
        value={username}
        onChange={(event) => { setUsername(event.target.value); setError(''); }}
        aria-label="用户名"
        placeholder="用户名"
        autoComplete="username"
        maxLength={24}
        disabled={busy}
      />
      <Input
        type="password"
        value={password}
        onChange={(event) => { setPassword(event.target.value); setError(''); }}
        aria-label="密码"
        placeholder="密码"
        autoComplete={mode === 'register' ? 'new-password' : 'current-password'}
        maxLength={64}
        disabled={busy}
      />
      {mode === 'register' && <Input
        type="password"
        value={confirm}
        onChange={(event) => { setConfirm(event.target.value); setError(''); }}
        aria-label="确认密码"
        placeholder="确认密码"
        autoComplete="new-password"
        maxLength={64}
        disabled={busy}
      />}
      {error && <p className="login-error" role="alert">{error}</p>}
      <SubmitButton  className="primary-button" disabled={busy}>
        {mode === 'register' ? <UserPlus size={15} /> : <LogIn size={15} />}
        {busy ? '请稍候…' : mode === 'register' ? '注册并进入' : '登录'}
      </SubmitButton>
    </Form>
    </div>
    </ScrollView>
  </div>;
}
