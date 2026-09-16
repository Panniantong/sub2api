import { describe, expect, it } from 'vitest'
import { parseProxyInput } from '../proxyParser'

describe('parseProxyInput', () => {
  it.each([
    ['host:port:user:pass', 'proxy.example.com:8001:alice:secret'],
    ['user:pass@host:port', 'alice:secret@proxy.example.com:8001'],
    ['user:pass:host:port', 'alice:secret:proxy.example.com:8001'],
  ])('accepts the documented %s form', (_format, value) => {
    expect(parseProxyInput(value, 'http')).toMatchObject({
      protocol: 'http', host: 'proxy.example.com', port: 8001, username: 'alice', password: 'secret'
    })
  })

  it.each([['http', 'http'], ['https', 'https'], ['socks5', 'socks5']] as const)(
    'uses %s for a scheme-less host', (protocol, expected) => {
      expect(parseProxyInput('proxy.example.com:8080', protocol)?.protocol).toBe(expected)
    }
  )

  it('accepts explicit http(s), socks5 and encoded credentials', () => {
    expect(parseProxyInput('https://alice:p%40ss@proxy.example.com:443', 'http')).toMatchObject({
      protocol: 'https', username: 'alice', password: 'p@ss', port: 443
    })
    expect(parseProxyInput('socks5h://proxy.example.com:1080', 'http')?.protocol).toBe('socks5h')
  })

  it('rejects malformed or unsupported lines', () => {
    expect(parseProxyInput('ftp://proxy.example.com:21', 'http')).toBeNull()
    expect(parseProxyInput('proxy.example.com:not-a-port', 'http')).toBeNull()
    expect(parseProxyInput('proxy.example.com:8080', 'ftp' as never)).toBeNull()
  })
})
