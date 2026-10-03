import { LogIn, UserPlus } from 'lucide-react';
import { useState, type FormEvent } from 'react';
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
  const [error, setError] = useState('');

  const switchMode = (next: 'login' | 'register') => {
    setMode(next);
    setError('');
    setConfirm('');
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (busy) return;
    const name = username.trim();
    if (!name || !password) {
      setError('请输入用户名和密码。');
      return;
    }
    if (mode === 'register' && password !== confirm) {
      setError('两次输入的密码不一致。');
      return;
    }
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
      setBusy(false);
    }
  };

  return <div className="app-shell login-page">
    <header className="login-header"><h1>贴贴清单</h1></header>
    <SpaceBuddies className="login-buddies" />

    <div className="login-tabs" role="tablist">
      <button type="button" role="tab" aria-selected={mode === 'login'} className={mode === 'login' ? 'is-active' : ''} onClick={() => switchMode('login')}>登录</button>
      <button type="button" role="tab" aria-selected={mode === 'register'} className={mode === 'register' ? 'is-active' : ''} onClick={() => switchMode('register')}>注册</button>
    </div>

    <form className="login-form" onSubmit={(event) => void submit(event)}>
      <input
        value={username}
        onChange={(event) => { setUsername(event.target.value); setError(''); }}
        aria-label="用户名"
        placeholder="用户名"
        autoComplete="username"
        maxLength={24}
        disabled={busy}
      />
      <input
        type="password"
        value={password}
        onChange={(event) => { setPassword(event.target.value); setError(''); }}
        aria-label="密码"
        placeholder={mode === 'register' ? '密码，至少 6 位' : '密码'}
        autoComplete={mode === 'register' ? 'new-password' : 'current-password'}
        maxLength={64}
        disabled={busy}
      />
      {mode === 'register' && <input
        type="password"
        value={confirm}
        onChange={(event) => { setConfirm(event.target.value); setError(''); }}
        aria-label="确认密码"
        placeholder="再输一次"
        autoComplete="new-password"
        maxLength={64}
        disabled={busy}
      />}
      {error && <p className="login-error" role="alert">{error}</p>}
      <button type="submit" className="primary-button" disabled={busy}>
        {mode === 'register' ? <UserPlus size={15} /> : <LogIn size={15} />}
        {busy ? '请稍候…' : mode === 'register' ? '注册并进入' : '登录'}
      </button>
    </form>
  </div>;
}
