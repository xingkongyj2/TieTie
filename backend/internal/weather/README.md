# 天气关怀

只使用和风天气专属 API Host：`/weather/v1/daily`（3天）、`/weather/v1/hourly`（48小时）、`/airquality/v1/hourly`（24小时）、`/airquality/v1/current`。原 Open-Meteo/CAMS 查询已删除，不回退其他来源。

在后端 `.env.local` 配置 `QWEATHER_API_HOST` 和 `QWEATHER_API_KEY`，密钥不发送到浏览器。也支持 Ed25519 JWT，配置 `QWEATHER_KEY_ID`、`QWEATHER_DEVELOPER_ID`、`QWEATHER_PROJECT_ID`、`QWEATHER_PRIVATE_KEY_FILE` 后服务端自动签名与续期。专属 Host 见[官方说明](https://dev.qweather.com/docs/configuration/api-host/)。

中国 AQI 使用接口真实返回的 cn-mee 指数；中国地区不提供详细污染物预报（[官方说明](https://dev.qweather.com/docs/api/air-quality/china-aqi/)），PM2.5 因而独立显示实时值、查询时间，与次日预报分开。缺失时段不补零、不复用实时数值当预报。天气服务有30分钟、1024地点的有界缓存，单次请求超时15秒；同地区共用一份数据。指数名称保留展示，来源和归因链接原样保存在后台，天气卡片及明细不展示供应商信息。

`care-policy.txt` 是天气处理与 AI 控制提示词，嵌入会话协议。`city-climate.json` 覆盖地区目录394个城市，区域参考标记为 regional_inference，单城核验标记 verified_city；它只决定关注重点，不能替代具体天气。推荐必须有当日预报证据，适用于不同季节。

区县坐标优先，缺少精确匹配时降级城市并在卡片标明；没有可信坐标不伪造。坐标数据基于 MIT 许可 cpca 项目固定版本，代码与名称双重校验，并由 GCJ-02 近似转换 WGS-84。

地区保存到数据库与 profile/users，个人天气指标偏好保存到数据库与 profile/habits 同模板分页，天气推送保存到数据库与 context/realtime 同模板分页并带过期时间。倒计时使用 agreements/shared 同模板分页，仍保持7类记忆。
