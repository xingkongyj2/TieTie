import assert from 'node:assert/strict'
import test from 'node:test'
import { resolveApiBaseURL } from '../src/api/origin'

test('only development receives the localhost default; trial/release fail closed', () => {
  assert.equal(resolveApiBaseURL('', 'develop'), 'http://127.0.0.1:4173')
  for (const environment of ['trial', 'release', 'unknown']) assert.throws(() => resolveApiBaseURL('', environment), /TARO_APP_API_BASE_URL/)
})
test('deployment origin must be HTTPS and contain no credentials, query or fragment', () => {
  assert.equal(resolveApiBaseURL(' https://api.example.com/v1/ ', 'release'), 'https://api.example.com/v1')
  assert.equal(resolveApiBaseURL('http://192.168.1.8:4173', 'develop'), 'http://192.168.1.8:4173')
  assert.throws(() => resolveApiBaseURL('http://api.example.com', 'trial'), /HTTPS/)
  for (const origin of ['//api.example.com', 'https://user:password@api.example.com', 'https://api.example.com?token=secret', 'https://api.example.com#part']) assert.throws(() => resolveApiBaseURL(origin, 'release'), /无效/)
})
