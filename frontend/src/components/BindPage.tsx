import { Input, Form, SubmitButton } from './Fields';
import Taro from '@tarojs/taro';
import { Button, ScrollView } from '@tarojs/components';
import { ArrowLeft, Copy, Link, Link2 } from './Icons';
import { useEffect, useRef, useState, type FormEvent } from 'react';
import { SpaceBuddies } from './SpaceBuddies';

interface Props {
  code: string;
  onBind: (code: string) => Promise<void>;
  onBack?: () => void;
  notify: (text: string) => void;
  embedded?: boolean;
}

/** 使用微信剪贴板保存邀请码。 */
async function copyText(text: string): Promise<boolean> {
  try { await Taro.setClipboardData({ data: text }); return true; }
  catch { return false; }
}

/** 已登录但未绑定时的全屏引导页：展示专属邀请码、分享入口，并可用对方邀请码完成绑定。 */
export function BindPage({ code, onBind, onBack, notify, embedded = false }: Props) {
  const [invite, setInvite] = useState('');
  const [binding, setBinding] = useState(false);
  const submitting = useRef(false);
  const [error, setError] = useState('');

  // 分享链接带 ?invite=CODE 打开时自动预填。
  useEffect(() => {
    const prefilled = Taro.getStorageSync<string>('tietie.pendingInvite') || Taro.getCurrentInstance().router?.params.invite;
    if (prefilled) { setInvite(prefilled.replace(/[^0-9]/g, '').slice(0, 4)); Taro.removeStorageSync('tietie.pendingInvite'); }
  }, []);

  const copyCode = async () => {
    notify(await copyText(code) ? '邀请码已复制' : '复制失败，请长按邀请码手动复制');
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const target = invite.trim();
    if (target.length !== 4 || submitting.current) return;
    submitting.current = true;
    setBinding(true);
    setError('');
    try {
      await onBind(target);
      notify('绑定成功，小窝已就绪');
    } catch (e) {
      setError(e instanceof Error ? e.message : '绑定失败，请稍后重试。');
    } finally {
      submitting.current = false;
      setBinding(false);
    }
  };

  return <div className={embedded ? 'bind-page bind-page-embedded' : 'app-shell bind-page'}>
    <ScrollView scrollY enhanced showScrollbar={false} className="bind-scroll">
    <div className="bind-scroll-content">
    <header className="bind-header">{onBack && <button type="button" className="icon-button bind-back" aria-label="返回我们首页" onClick={onBack}><ArrowLeft size={19} /></button>}<h1>贴贴清单</h1></header>
    <div className="bind-welcome">
      <SpaceBuddies className="bind-buddy" />
      <h2>两个人，刚刚好</h2>
    </div>

    <div className="bind-code-card">
      <span className="bind-code-label">我的邀请码</span>
      <strong className="bind-code" onClick={() => void copyCode()}>{code}</strong>
      <div className="bind-code-actions">
        <button type="button" className="primary-button" onClick={() => void copyCode()}><Copy size={15} />复制邀请码</button>
        <Button className="h5-button secondary-button" openType="share"><Link2 size={15} />分享邀请</Button>
      </div>
    </div>

    <Form className="bind-form" onSubmit={(event) => void submit(event)}>
      <div className="bind-form-row">
        <Input
          value={invite}
          onChange={(event) => { setInvite(event.target.value.replace(/[^0-9]/g, '').slice(0, 4)); setError(''); }}
          aria-label="对方邀请码"
          placeholder="对方邀请码"
          autoComplete="off"
          inputMode="numeric"
          spellCheck={false}
          maxLength={4}
          disabled={binding}
        />
        <SubmitButton  className="primary-button" disabled={binding || invite.trim().length < 4}>
          <Link size={15} />{binding ? '正在绑定…' : '绑定'}
        </SubmitButton>
      </div>
      {error && <p className="bind-error" role="alert">{error}</p>}
    </Form>
    </div>
    </ScrollView>
  </div>;
}
