# 中国省市选项

来源：[mumuy/data_location](https://github.com/mumuy/data_location/tree/f678a6569222e62bcffeda8ad1c58512eeee64f1)，MIT（见 LICENSE）。固定提交 `f678a6569222e62bcffeda8ad1c58512eeee64f1` 的 list.json（大陆）与 list2.json（港澳台），version.js 标注 2026 年 4 月。保留省、市、区县三级及省直辖县级选项，构建时内嵌，无运行时网络依赖。

直辖市、香港和澳门的城市选项使用同名省级地区。台湾沿用上游目录标识 83xxxx；港澳台 `codeSystem` 为 `mumuy/data_location`，不可直接当作国家标准行政区划代码或天气供应商 ID。大陆选项为 GB/T2260；无区县层级的城市不虚构区县，districtCode/district 为空。后续天气服务应通过地区名称/代码体系映射供应商地区 ID。

更新时从固定提交重新生成，检查全部 34 个省级选项和省市对应关系后提交。
