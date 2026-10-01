import { Copy, Heart, Link2 } from 'lucide-react';
import { useEffect, useState, type FormEvent } from 'react';
import { SpaceBuddy } from './SpaceBuddies';

interface Props {
  code: string;
  onBind: (code: string) => Promise<void>;
  notify: (text: string) => void;
  embedded?: boolean;
}

/** 复制到剪贴板，带 execCommand 兜底（微信内置浏览器常拒绝 Clipboard API）。 */
async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch { /* 走兜底 */ }
  try {
    const area = document.createElement('textarea');
    area.value = text;
    area.style.position = 'fixed';
    area.style.opacity = '0';
    document.body.appendChild(area);
    area.select();
    const ok = document.execCommand('copy');
    document.body.removeChild(area);
    return ok;
  } catch {
    return false;
  }
}

function shareLink(code: string): string {
  const url = new URL(window.location.href);
  url.search = '';
  url.hash = '';
  url.searchParams.set('invite', code);
  return url.toString();
}

/** 已登录但未绑定时的全屏引导页：展示专属邀请码、分享入口，并可用对方邀请码完成绑定。 */
export function BindPage({ code, onBind, notify, embedded = false }: Props) {
  const [invite, setInvite] = useState('');
  const [binding, setBinding] = useState(false);
  const [error, setError] = useState('');

  // 分享链接带 ?invite=CODE 打开时自动预填。
  useEffect(() => {
    const prefilled = new URLSearchParams(window.location.search).get('invite');
    if (prefilled) setInvite(prefilled.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 8));
  }, []);

  const copyCode = async () => {
    notify(await copyText(code) ? '邀请码已复制' : '复制失败，请长按邀请码手动复制');
  };
  const copyShare = async () => {
    const text = `来「贴贴」和我绑定我们的小窝 💞 打开链接输入我的邀请码 ${code}：${shareLink(code)}`;
    notify(await copyText(text) ? '分享链接已复制' : '复制失败，请手动分享');
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const target = invite.trim().toUpperCase();
    if (!target || binding) return;
    setBinding(true);
    setError('');
    try {
      await onBind(target);
      notify('绑定成功，小窝已就绪 💞');
    } catch (e) {
      setError(e instanceof Error ? e.message : '绑定失败，请稍后重试。');
    } finally {
      setBinding(false);
    }
  };

  return <div className={embedded ? 'bind-page bind-page-embedded' : 'app-shell bind-page'}>
    <header className="bind-header"><div><h1>我们<span className="page-title-dot" /></h1></div><Heart size={21} aria-hidden="true" /></header>
    <div className="bind-welcome"><SpaceBuddy variant="blue" className="bind-buddy" /><h2>两个人，刚刚好</h2><p>邀请在意的人，一起收集日常的小美好。</p></div>

    <div className="bind-code-card">
      <span className="bind-code-label">我的邀请码</span>
      <strong className="bind-code" onDoubleClick={() => void copyCode()}>{code}</strong>
      <div className="bind-code-actions">
        <button type="button" className="primary-button" onClick={() => void copyCode()}><Copy size={15} />复制邀请码</button>
        <button type="button" className="secondary-button" onClick={() => void copyShare()}><Link2 size={15} />复制分享链接</button>
      </div>
    </div>

    <form className="bind-form" onSubmit={(event) => void submit(event)}>
      <label htmlFor="invite-code">输入对方的邀请码</label>
      <div className="bind-form-row">
        <input
          id="invite-code"
          value={invite}
          onChange={(event) => { setInvite(event.target.value.toUpperCase().replace(/[^A-Z0-9]/g, '').slice(0, 8)); setError(''); }}
          placeholder="例如 QSMVXTN6"
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          maxLength={8}
          disabled={binding}
        />
        <button type="submit" className="primary-button" disabled={binding || invite.trim().length < 6}>
          <Heart size={15} />{binding ? '正在绑定…' : '绑定'}
        </button>
      </div>
      {error && <p className="bind-error" role="alert">{error}</p>}
    </form>
  </div>;
}
