import Taro from '@tarojs/taro'
import { ScrollView } from '@tarojs/components'
import { Check, ChevronRight, MapPin } from './Icons'
import { useEffect, useId, useRef, useState } from 'react'
import { profileApi, type ProvinceOption } from '../api/profile'
import type { RegionLocation } from '../types'
import { Sheet } from './Sheet'
import './RegionPicker.css'

function RegionColumn({ label, options, selected, placeholder, onSelect }: {
  label: string; options: { code: string; name: string }[]; selected?: string; placeholder: string; onSelect: (code: string) => void
}) {
  const id = useId().replace(/:/g, '-')
  const listId = `region-list-${id}`
  const offset = useRef(0)
  const [scrollTop, setScrollTop] = useState(0)
  useEffect(() => {
    if (!selected) return
    let active = true
    Taro.nextTick(() => {
      Taro.createSelectorQuery().select(`#${listId}`).boundingClientRect()
        .select(`#${id}-${selected}`).boundingClientRect().exec((results) => {
          if (!active) return
          const [list, option] = results as ({ top: number; height: number } | null)[]
          if (list && option) setScrollTop(Math.max(0, offset.current + option.top - list.top - (list.height - option.height) / 2))
        })
    })
    return () => { active = false }
  }, [listId, selected])
  return <div className="region-picker-column">
    <span className="region-picker-label">{label}</span>
    <ScrollView className="region-picker-options" id={listId} scrollY scrollTop={scrollTop} scrollWithAnimation
      onScroll={(event) => { offset.current = event.detail.scrollTop }} aria-label={label}
      aria-disabled={!options.length} aria-activedescendant={selected ? `${id}-${selected}` : undefined}>
      {options.length ? options.map((option) => <button type="button" role="option" id={`${id}-${option.code}`} key={option.code}
        tabIndex={-1} aria-selected={option.code === selected} className={option.code === selected ? 'is-selected' : ''}
        onClick={() => onSelect(option.code)}><span>{option.name}</span>{option.code === selected && <Check size={12} aria-hidden="true" />}</button>)
        : <p className="region-picker-placeholder">{placeholder}</p>}
    </ScrollView>
  </div>
}

const regionName = (value?: RegionLocation) => value ? [value.province, value.province === value.city ? '' : value.city, value.district].filter(Boolean).join(' · ') : ''

export function RegionPicker({ value, required = false, disabled = false, onChange, onValidityChange }: {
  value?: RegionLocation
  required?: boolean
  disabled?: boolean
  onChange: (value?: RegionLocation) => void
  onValidityChange: (valid: boolean) => void
}) {
  const labelId = useId()
  const [provinces, setProvinces] = useState<ProvinceOption[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState(value)
  useEffect(() => {
    let active = true
    setLoading(true)
    setError(false)
    void profileApi.regions().then(({ provinces }) => {
      if (active) setProvinces(provinces)
    }).catch(() => { if (active) setError(true) })
      .finally(() => { if (active) setLoading(false) })
    return () => { active = false }
  }, [attempt])
  const selected = provinces.find((item) => item.code === value?.provinceCode)
  const city = selected?.cities.find((item) => item.code === value?.cityCode)
  useEffect(() => {
    // A failed catalog read must not invalidate an already saved region.
    onValidityChange(!value || Boolean(value.cityCode) && (!city || !city.districts.length || city.districts.some((item) => item.code === value.districtCode)))
  }, [value, city, onValidityChange])
  const draftProvince = provinces.find((item) => item.code === draft?.provinceCode)
  const draftCity = draftProvince?.cities.find((item) => item.code === draft?.cityCode)
  const complete = !!draftProvince && !!draftCity && (!draftCity.districts.length || draftCity.districts.some((item) => item.code === draft?.districtCode))
  return <section className="form-card region-card">
    <div className="field-label" id={labelId}><span>地区 {required && <span className="field-required">必填</span>}</span><MapPin size={15} aria-hidden="true" /></div>
    <button type="button" className="region-picker-trigger" disabled={disabled} aria-label="选择地区" aria-haspopup="dialog" aria-expanded={open}
      onClick={() => { setDraft(value ? { ...value } : undefined); setOpen(true) }}>
      <span className={value ? '' : 'is-placeholder'}>{regionName(value) || '选择省份、城市和区／县'}</span><ChevronRight size={16} aria-hidden="true" />
    </button>
    {open && <Sheet title="选择地区" onClose={() => setOpen(false)}>{(close) => <div className="region-picker-modal">
      <p className="region-picker-summary">{regionName(draft) || '选择你的所在地'}</p>
      {loading ? <div className="region-picker-loading" role="status"><span className="spinner" aria-hidden="true" /><span>正在加载地区…</span></div>
        : error || !provinces.length ? <div className="region-picker-problem" role="alert"><p>{error ? '地区暂时没加载出来' : '暂无可选地区'}</p><button type="button" className="region-retry" onClick={() => setAttempt((current) => current + 1)}>重新加载</button></div>
        : <div className="region-picker-columns">
          <RegionColumn label="省份" options={provinces} selected={draft?.provinceCode} placeholder="暂无省份" onSelect={(code) => {
            if (code === draft?.provinceCode) return
            const province = provinces.find((item) => item.code === code)!
            setDraft({ provinceCode: province.code, province: province.name, codeSystem: province.codeSystem, cityCode: '', city: '', districtCode: '', district: '' })
          }} />
          <RegionColumn label="城市" options={draftProvince?.cities ?? []} selected={draft?.cityCode} placeholder={draftProvince ? '暂无城市' : '先选择省份'} onSelect={(code) => {
            if (!draft || code === draft.cityCode) return
            const city = draftProvince?.cities.find((item) => item.code === code)!
            setDraft({ ...draft, cityCode: city.code, city: city.name, districtCode: '', district: '' })
          }} />
          <RegionColumn label="区／县" options={draftCity?.districts ?? []} selected={draft?.districtCode} placeholder={draftCity ? '无需选择区／县' : '先选择城市'} onSelect={(code) => {
            if (!draft) return
            const district = draftCity?.districts.find((item) => item.code === code)!
            setDraft({ ...draft, districtCode: district.code, district: district.name })
          }} />
        </div>}
      <div className="region-picker-actions">
        <button type="button" className="region-picker-clear" onClick={() => { onChange(undefined); close() }}>清空地区</button>
        <button type="button" className="primary-button" disabled={loading || error || !complete} onClick={() => { if (complete && draft) { onChange(draft); close() } }}>确定</button>
      </div>
    </div>}</Sheet>}
  </section>
}
