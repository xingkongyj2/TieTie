import { Button, Image, Input, Label, Text, View } from '@tarojs/components'
import { assetUrl } from '../lib/assets'
import './WechatProfileFields.css'

interface Props {
  nickname: string
  avatar: string
  disabled?: boolean
  required?: boolean
  onNicknameChange: (value: string) => void
  onAvatarChange: (tempPath: string) => void
  onError?: (message: string) => void
}

/** 微信的头像选择、昵称填写能力；头像临时文件由调用方在提交时上传。 */
export function WechatProfileFields({ nickname, avatar, disabled = false, required = false, onNicknameChange, onAvatarChange, onError }: Props) {
  return <View className="wechat-profile-fields">
    <View className="wechat-profile-avatar-row">
      <View className="wechat-profile-avatar-copy">
        <Text className="wechat-profile-label">头像{required ? '（必填）' : ''}</Text>
        <Text className="wechat-profile-help">点击右侧选择头像</Text>
      </View>
      <Button className="wechat-profile-avatar-button" openType="chooseAvatar" disabled={disabled} ariaLabel="选择微信头像" hoverClass="wechat-profile-avatar-pressed" onChooseAvatar={(event) => {
        const path = typeof event.detail.avatarUrl === 'string' ? event.detail.avatarUrl.trim() : ''
        if (path) onAvatarChange(path)
      }}>
        {avatar ? <Image className="wechat-profile-avatar-image" src={assetUrl(avatar)} mode="aspectFill" ariaLabel="已选择的头像" /> : <View className="wechat-profile-avatar-placeholder"><Text>+</Text><Text className="wechat-profile-avatar-prompt">选择头像</Text></View>}
      </Button>
    </View>
    <Label className="wechat-profile-label wechat-profile-nickname-label" for="wechat-profile-nickname">昵称{required ? '（必填）' : ''}</Label>
    <View className="wechat-profile-nickname-wrap">
      <Input id="wechat-profile-nickname" name="wechatNickname" className="wechat-profile-nickname-input" type="nickname" value={nickname} disabled={disabled} maxlength={24} placeholder="点击填写昵称" placeholderClass="wechat-profile-nickname-placeholder" confirmType="done"
        onInput={(event) => onNicknameChange(event.detail.value)}
        onBlur={(event) => onNicknameChange(event.detail.value)}
        onConfirm={(event) => onNicknameChange(event.detail.value)}
        onNickNameReview={(event) => {
          if (event.detail.pass !== false && !event.detail.timeout) return
          onNicknameChange('')
          onError?.(event.detail.timeout ? '昵称检查超时，请重新填写后再试。' : '昵称未通过微信检查，请重新填写。')
        }} />
    </View>
  </View>
}
