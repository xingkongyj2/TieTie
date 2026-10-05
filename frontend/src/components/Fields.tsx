import { Children, isValidElement, type ReactNode, type ChangeEvent, type FormEvent, type InputHTMLAttributes, type SelectHTMLAttributes, type TextareaHTMLAttributes, type FormHTMLAttributes, type ButtonHTMLAttributes } from 'react'
import { Input as NativeInput, Textarea as NativeTextarea, Picker, View, Text, Form as NativeForm, Button } from '@tarojs/components'

function changeEvent<T>(value: string): ChangeEvent<T> {
  return { target: { value }, currentTarget: { value }, preventDefault() {}, stopPropagation() {} } as unknown as ChangeEvent<T>
}
function nodeText(node: ReactNode): string {
  if (node === null || node === undefined || typeof node === 'boolean') return ''
  if (Array.isArray(node)) return node.map(nodeText).join('')
  if (isValidElement<{ children?: ReactNode }>(node)) return nodeText(node.props.children)
  return String(node)
}

/** 在保留原表单样式与验证函数的同时，桥接小程序的 detail.value 事件。 */
export function Input(props: InputHTMLAttributes<HTMLInputElement>) {
  if (process.env.TARO_ENV === 'h5') return <input {...props} />
  const { className, value, type, disabled, maxLength, placeholder, name, id, onChange, onFocus, onBlur, autoFocus, style } = props
  return <NativeInput id={id} name={name} className={`h5-input ${className || ''}`} style={style}
    value={value === undefined ? '' : String(value)} type={type === 'number' ? 'number' : 'text'} password={type === 'password'}
    disabled={disabled} maxlength={maxLength ?? 140} placeholder={placeholder} focus={autoFocus}
    onInput={event => onChange?.(changeEvent<HTMLInputElement>(event.detail.value))}
    onFocus={event => onFocus?.(event as unknown as Parameters<NonNullable<typeof onFocus>>[0])}
    onBlur={event => onBlur?.(event as unknown as Parameters<NonNullable<typeof onBlur>>[0])}
    onConfirm={() => props.onKeyDown?.({ key: 'Enter', nativeEvent: { isComposing: false }, preventDefault() {}, stopPropagation() {} } as unknown as Parameters<NonNullable<typeof props.onKeyDown>>[0])} />
}

export function Textarea(props: TextareaHTMLAttributes<HTMLTextAreaElement>) {
  if (process.env.TARO_ENV === 'h5') return <textarea {...props} />
  const { className, value, disabled, maxLength, placeholder, name, id, onChange, style, rows } = props
  return <NativeTextarea id={id} name={name} className={`h5-textarea ${className || ''}`} style={{ minHeight: `${(rows || 3) * 22}px`, ...style }}
    value={value === undefined ? '' : String(value)} disabled={disabled} maxlength={maxLength ?? -1} placeholder={placeholder}
    adjustPosition={false} onInput={event => onChange?.(changeEvent<HTMLTextAreaElement>(event.detail.value))} />
}

export function Select(props: SelectHTMLAttributes<HTMLSelectElement>) {
  if (process.env.TARO_ENV === 'h5') return <select {...props} />
  const options: { value: string; label: string }[] = []
  Children.forEach(props.children, child => {
    if (isValidElement<{ value?: string | number; children?: ReactNode; disabled?: boolean }>(child)) {
      options.push({ value: String(child.props.value ?? nodeText(child.props.children)), label: nodeText(child.props.children) })
    }
  })
  const selected = Math.max(0, options.findIndex(option => option.value === String(props.value)))
  return <Picker mode="selector" disabled={props.disabled} range={options.map(option => option.label)} value={selected}
    className={`h5-select tietie-field-select ${props.className || ''}`} style={props.style}
    onChange={event => { const option = options[Number(event.detail.value)]; if (option) props.onChange?.(changeEvent<HTMLSelectElement>(option.value)) }}>
    <View className="tietie-field-select-content"><Text className="tietie-field-select-text">{options[selected]?.label || ''}</Text></View>
  </Picker>
}

export function Form(props: FormHTMLAttributes<HTMLFormElement>) {
  if (process.env.TARO_ENV === 'h5') return <form {...props} />
  return <NativeForm className={`h5-form ${props.className || ''}`} onSubmit={event => props.onSubmit?.({
    ...event, preventDefault() {}, stopPropagation() {},
  } as unknown as FormEvent<HTMLFormElement>)}>{props.children}</NativeForm>
}

export function SubmitButton(props: ButtonHTMLAttributes<HTMLButtonElement>) {
  if (process.env.TARO_ENV === 'h5') return <button {...props} type="submit" />
  return <Button formType="submit" className={`h5-button ${props.className || ''}`} disabled={props.disabled} style={props.style}>
    {props.children}
  </Button>
}
