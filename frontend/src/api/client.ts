import Taro from '@tarojs/taro'
import { apiURL } from '../config/api'
import { getToken, invalidateToken } from '../lib/token'
import { createTransport } from './transport'
import { consumeTaskRejection } from './task'

export { ApiError, isAccountTokenInvalid } from './transport'
export type { RequestOptions } from './transport'
export const request = createTransport({
  url: apiURL, getToken, invalidateToken,
  nativeRequest: options => consumeTaskRejection(Taro.request(options)),
})
