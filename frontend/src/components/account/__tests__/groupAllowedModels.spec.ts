import { describe, expect, it } from 'vitest'
import { buildGroupAllowedModelsPayload, groupAllowedModelsFromAccount } from '../groupAllowedModels'

describe('groupAllowedModels helpers', () => {
  it('reads only bindings that carry a model limit', () => {
    expect(groupAllowedModelsFromAccount(null)).toEqual({})
    expect(groupAllowedModelsFromAccount({
      account_groups: [
        { account_id: 1, group_id: 3, priority: 1, created_at: '' },
        { account_id: 1, group_id: 5, priority: 2, allowed_models: ['gpt-5.5'], created_at: '' },
        { account_id: 1, group_id: 7, priority: 3, allowed_models: [], created_at: '' }
      ]
    })).toEqual({ 5: ['gpt-5.5'] })
  })

  it('keeps selected groups with models and drops the rest', () => {
    expect(buildGroupAllowedModelsPayload([3, 5], {
      3: [],
      5: [' gpt-5.5 ', 'gpt-5.5', '', 'gpt-5.3-*'],
      9: ['gpt-5.4']
    })).toEqual({ 5: ['gpt-5.5', 'gpt-5.3-*'] })
  })
})
