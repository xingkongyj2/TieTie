import { ScrollView } from '@tarojs/components'
import { Input, Form, SubmitButton } from './Fields';
import { LogIn, UserPlus } from './Icons';
import { useRef, useState, type FormEvent } from 'react';
import { SpaceBuddies } from './SpaceBuddies';

interface Props {
  onLogin: (username: string, password: string) => Promise<void>;
  onRegister: (username: string, password: string) => Promise<void>;
  notify: (text: string) => void;
}

/** 未登录时的全屏登录/注册页。 */
export function LoginPage({ onLogin, onRegister, notify }: Props) {
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
    <SpaceBuddies className="login-buddies" />

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
