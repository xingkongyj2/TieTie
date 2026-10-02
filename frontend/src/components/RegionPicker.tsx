import { ChevronDown, MapPin } from 'lucide-react'
import { useEffect, useState } from 'react'
import { profileApi, type ProvinceOption } from '../api/profile'
import type { RegionLocation } from '../types'
import './RegionPicker.css'

export function RegionPicker({ value, onChange, onValidityChange }: {
  value?: RegionLocation
  onChange: (value?: RegionLocation) => void
  onValidityChange: (valid: boolean) => void
}) {
  const [provinces, setProvinces] = useState<ProvinceOption[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const [attempt, setAttempt] = useState(0)
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
    // Keep already saved information intact if loading the catalog fails.
    onValidityChange(!value || Boolean(value.cityCode) && (!city || !city.districts.length || city.districts.some((item) => item.code === value.districtCode)))
  }, [value, city, onValidityChange])
  return <section className="form-card region-card">
    <div className="field-label" id="region-label">地区 <MapPin size={15} aria-hidden="true" /></div>
    <div className="region-selects" role="group" aria-labelledby="region-label" aria-busy={loading}>
      <div className="region-select">
        <select aria-label="省份" value={value?.provinceCode ?? ''} disabled={loading || error}
          onChange={(event) => {
            const province = provinces.find((item) => item.code === event.target.value)
            onChange(province ? { provinceCode: province.code, province: province.name, codeSystem: province.codeSystem, cityCode: '', city: '', districtCode: '', district: '' } : undefined)
          }}>
          <option value="">{loading ? '正在加载…' : '请选择省份'}</option>
          {loading || error ? value && <option value={value.provinceCode}>{value.province}</option> : provinces.map((item) => <option key={item.code} value={item.code}>{item.name}</option>)}
        </select><ChevronDown size={14} aria-hidden="true" />
      </div>
      <div className="region-select">
        <select aria-label="城市" value={value?.cityCode ?? ''} disabled={loading || error || !selected}
          onChange={(event) => {
            const city = selected?.cities.find((item) => item.code === event.target.value)
            if (value) onChange({ ...value, cityCode: city?.code ?? '', city: city?.name ?? '', districtCode: '', district: '' })
          }}>
          <option value="">请选择城市</option>
          {loading || error ? value?.cityCode && <option value={value.cityCode}>{value.city}</option> : selected?.cities.map((item) => <option key={item.code} value={item.code}>{item.name}</option>)}
        </select><ChevronDown size={14} aria-hidden="true" />
      </div>
      <div className="region-select region-district">
        <select aria-label="区/县" value={value?.districtCode ?? ''} disabled={loading || error || !city?.districts.length}
          onChange={(event) => {
            const district = city?.districts.find((item) => item.code === event.target.value)
            if (value) onChange({ ...value, districtCode: district?.code ?? '', district: district?.name ?? '' })
          }}>
          <option value="">{city && !city.districts.length ? '无区／县层级' : '请选择区／县'}</option>
          {loading || error ? value?.districtCode && <option value={value.districtCode}>{value.district}</option> : city?.districts.map((item) => <option key={item.code} value={item.code}>{item.name}</option>)}
        </select><ChevronDown size={14} aria-hidden="true" />
      </div>
    </div>
    {error && <button className="region-retry" type="button" onClick={() => setAttempt(attempt + 1)}>地区加载失败，点击重试</button>}
  </section>
}
