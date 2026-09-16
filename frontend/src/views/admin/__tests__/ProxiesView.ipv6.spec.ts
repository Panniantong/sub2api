import { describe, expect, it } from 'vitest'
import { parseProxyInput } from '@/utils/proxyParser'

describe('proxy quick-add parser', () => {
  it.each([
    ['hostname:port:username:password', 'proxy.example.com:8080:user:pass'],
    ['username:password@hostname:port', 'user:pass@proxy.example.com:8080'],
    ['username:password:hostname:port', 'user:pass:proxy.example.com:8080'],
  ])('parses %s and completes the selected protocol', (_name, line) => {
    expect(parseProxyInput(line, 'https')).toEqual({
      protocol: 'https', host: 'proxy.example.com', port: 8080, username: 'user', password: 'pass'
    })
  })

  it('keeps an explicit protocol instead of applying the default', () => {
    expect(parseProxyInput('socks5://user:pass@proxy.example.com:1080', 'https')).toMatchObject({
      protocol: 'socks5', host: 'proxy.example.com', port: 1080
    })
  })

  it('supports bare host:port and bracketed IPv6', () => {
    expect(parseProxyInput('proxy.example.com:8080', 'http')?.protocol).toBe('http')
    expect(parseProxyInput('[2001:db8::1]:1080', 'socks5')).toMatchObject({
      protocol: 'socks5', host: '2001:db8::1', port: 1080
    })
  })
})
