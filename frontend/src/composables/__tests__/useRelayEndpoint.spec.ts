import { describe, expect, it } from 'vitest'
import { normalizeRelayEndpoint } from '../useRelayEndpoint'

describe('relay SDK endpoint', () => {
  it.each([
    ['', 'https://relay.example/v1'],
    ['https://api.example', 'https://api.example/v1'],
    [' https://api.example/v1/ ', 'https://api.example/v1'],
    ['https://api.example/relay/v1///', 'https://api.example/relay/v1'],
    ['https://api.example/relay/?theme=dark#top', 'https://api.example/relay/v1'],
    ['http://127.0.0.1:8080', 'http://127.0.0.1:8080/v1'],
  ])('normalizes %s without duplicating v1 or losing a path prefix', (input, expected) => {
    expect(normalizeRelayEndpoint(input, 'https://relay.example')).toBe(expected)
  })

  it.each(['javascript:alert(1)', 'file:///etc/passwd', 'https://user:pass@api.example', '/relative', 'broken address'])('rejects an invalid or credential-bearing configuration: %s', input => {
    expect(normalizeRelayEndpoint(input, 'https://relay.example')).toBe('')
  })
})
