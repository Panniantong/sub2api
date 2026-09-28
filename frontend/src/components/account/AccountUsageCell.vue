<template>
  <div ref="rootRef" v-if="showUsageWindows || account.ws_connections">
    <div v-if="account.ws_connections" class="mb-1 max-w-full text-xs text-gray-600 dark:text-gray-300" title="本服务实例当前连接池快照，随账号列表刷新">
      <span class="font-medium text-gray-700 dark:text-gray-200">WS</span>
      WS 实际连接：{{ account.ws_connections.total }}（空闲 {{ account.ws_connections.idle }} / 使用中 {{ account.ws_connections.in_use }}）
      <span v-if="account.ws_connections.connecting"> · 建立中 {{ account.ws_connections.connecting }}</span>
      <span v-if="account.ws_connections.target"> / 目标 {{ account.ws_connections.target }}</span>
      <span v-if="account.ws_connections.state"> · {{ { probing: '探测中', building: '建池中', ready: '就绪', degraded: '连接不足', failed: '失败', expired: '已过期' }[account.ws_connections.state] || account.ws_connections.state }}</span>
      <span v-if="account.ws_connections.probe_state === 'probing'" class="block text-amber-600">Astra 上下文预热中</span>
      <span v-else-if="account.ws_connections.probe_state === 'ready'" class="block text-emerald-600">Astra 上下文已就绪</span>
      <span v-else-if="account.ws_connections.probe_state === 'failed'" class="block text-red-600">Astra 上下文预热失败</span>
      <span v-if="account.ws_connections.state === 'expired'" class="block text-red-600">连接池已过期，请重新绑定</span>
      <span v-else-if="account.ws_connections.state === 'failed'" class="block text-red-600">连接池建立失败，请重新绑定</span>
      <span v-if="account.ws_connections.state === 'degraded'" class="block text-amber-600">池内连接不足，不自动补建</span>
      <span v-if="account.ws_connections.last_error" class="block max-w-xs break-all text-red-600">{{ account.ws_connections.last_error }}</span>
    </div>
    <!-- Anthropic OAuth and Setup Token accounts: fetch real usage data -->
    <template
      v-if="
        account.platform === 'anthropic' &&
        (account.type === 'oauth' || account.type === 'setup-token')
      "
    >
      <!-- Loading state -->
      <div v-if="loading" class="space-y-1.5">
        <!-- OAuth: 3 rows, Setup Token: 1 row -->
        <div class="flex items-center gap-1">
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
        <template v-if="account.type === 'oauth'">
          <div class="flex items-center gap-1">
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          </div>
          <div class="flex items-center gap-1">
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          </div>
        </template>
      </div>

      <!-- Error state -->
      <div v-else-if="error" class="text-xs text-red-500">
        {{ error }}
      </div>

      <!-- Usage data -->
      <div v-else-if="usageInfo" class="space-y-1">
        <!-- API error (degraded response) -->
        <div v-if="usageInfo.error" class="text-xs text-amber-600 dark:text-amber-400 truncate max-w-[200px]" :title="usageInfo.error">
          {{ usageInfo.error }}
        </div>
        <!-- 5h Window -->
        <UsageProgressBar
          v-if="usageInfo.five_hour"
          label="5h"
          :utilization="usageInfo.five_hour.utilization"
          :resets-at="usageInfo.five_hour.resets_at"
          :window-stats="usageInfo.five_hour.window_stats"
          color="indigo"
        />

        <!-- 7d Window (OAuth only) -->
        <UsageProgressBar
          v-if="usageInfo.seven_day"
          label="7d"
          :utilization="usageInfo.seven_day.utilization"
          :resets-at="usageInfo.seven_day.resets_at"
          color="emerald"
        />

        <!-- 7d Sonnet Window (OAuth only) -->
        <UsageProgressBar
          v-if="usageInfo.seven_day_sonnet"
          label="7d S"
          :utilization="usageInfo.seven_day_sonnet.utilization"
          :resets-at="usageInfo.seven_day_sonnet.resets_at"
          color="purple"
        />

        <!-- 7d Fable Window (7d_oi) -->
        <UsageProgressBar
          v-if="usageInfo.seven_day_fable"
          label="7d F"
          :utilization="usageInfo.seven_day_fable.utilization"
          :resets-at="usageInfo.seven_day_fable.resets_at"
          color="amber"
        />

        <!-- Passive sampling label + active query button -->
        <div class="flex items-center gap-1.5 mt-0.5">
          <span
            v-if="usageInfo.source === 'passive'"
            class="text-[9px] text-gray-400 dark:text-gray-500 italic"
          >
            {{ t('admin.accounts.usageWindow.passiveSampled') }}
          </span>
          <button
            type="button"
            class="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[9px] font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/30 transition-colors"
            :disabled="activeQueryLoading"
            @click="loadActiveUsage"
          >
            <svg
              class="h-2.5 w-2.5"
              :class="{ 'animate-spin': activeQueryLoading }"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
              />
            </svg>
            {{ t('admin.accounts.usageWindow.activeQuery') }}
          </button>
        </div>
      </div>

      <!-- No data yet -->
      <div v-else class="space-y-1">
        <div class="text-xs text-gray-400">-</div>
      </div>
    </template>

    <!-- OpenAI OAuth accounts: single source from /usage API -->
    <template v-else-if="account.platform === 'openai' && (account.type === 'oauth' || account.type === 'setup-token')">
      <div v-if="account.cookie_binding" class="mb-2 max-w-full space-y-1 text-xs">
        <div class="flex items-center gap-2" @click.stop>
          <span class="shrink-0 text-gray-500">Cookie Host</span>
          <select
            class="input min-w-0 flex-1 px-1.5 py-0.5 text-xs"
            :value="selectedCookieHost"
            :disabled="cookieHostSaving"
            @change="handleCookieHostChange"
          >
            <option value="">未绑定</option>
            <option v-for="host in cookieHosts" :key="host" :value="host">{{ host }}{{ cookieHostCooldownLabel(host) }}</option>
          </select>
          <span v-if="cookieHostSaving" class="text-gray-400">保存中</span>
        </div>
        <span v-if="cookieHostError" class="block text-red-600">{{ cookieHostError }}</span>
        <span v-if="account.cookie_binding.cooldown_host && account.cookie_binding.cooldown_until" class="block text-amber-600">Host 冷静中：{{ account.cookie_binding.cooldown_host }}，至 {{ new Date(account.cookie_binding.cooldown_until).toLocaleString() }}（仍可选择）</span>
        <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          <span v-if="account.cookie_binding.binding_expires_at" :class="bindingRemaining(account) > 0 ? 'text-emerald-600' : 'text-red-600'">绑定剩余 {{ bindingRemaining(account) }}s</span>
          <button v-if="account.cookie_binding.rotation_at && bindingRotationLabel(account) === '正在轮换'" type="button" class="text-amber-600 underline hover:text-amber-700" title="查看当前轮换验证日志" @click.stop="openCookieValidationLogs">正在轮换</button>
          <span v-else-if="account.cookie_binding.rotation_at" class="text-gray-500">{{ bindingRotationLabel(account) }}</span>
          <span v-if="account.cookie_binding.available_host_count !== undefined" class="text-gray-500">可绑定 Host {{ account.cookie_binding.available_host_count }}</span>
          <CookieBindingEstimate :binding="account.cookie_binding" />
          <span v-if="cookieSchedulingBlockLabel" class="rounded bg-amber-50 px-1.5 py-0.5 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300" title="动态调度状态；有效绑定恢复后自动回原组，不修改账号实际分组或可调度开关">{{ account.cookie_binding?.degraded_group_id ? `降级到 ${account.cookie_binding.degraded_group_name || '#' + account.cookie_binding.degraded_group_id} 分组` : '暂不调度' }} · {{ cookieSchedulingBlockLabel }}</span>
          <span v-else :class="account.cookie_binding.status === 'active' ? 'text-emerald-600' : 'text-gray-500'">{{ { active: '绑定有效', expired: 'Cookie 已过期或不存在，绑定不生效', unbound: '未绑定', unavailable: 'Cookie 状态读取失败', cooldown: 'Host 冷静中' }[account.cookie_binding.status] }}</span>
          <span class="text-gray-500">WS {{ account.cookie_binding.ws_enabled ? '已启用' : '已关闭' }}</span>
          <button type="button" class="text-blue-600 hover:underline dark:text-blue-400" @click.stop="openCookieValidationLogs">Host 验证日志</button>
        </div>
      </div>
      <div v-if="account.codex_turn_tickets?.length || account.extra?.session_id || account.codex_turn_ticket_history?.length" class="mb-1 max-w-full text-[10px] leading-4 text-gray-500 dark:text-gray-400">
        <span class="font-medium text-gray-700 dark:text-gray-200">票</span>
        <span v-for="ticket in account.codex_turn_tickets" :key="ticket.expires_at || ticket.length" class="ml-1">
          <span :class="ticket.ready ? 'text-emerald-600 dark:text-emerald-400' : 'text-gray-400'">
            {{ ticket.length ? `${ticket.length} / ${formatTicketRemaining(ticket.remaining_seconds)}` : '未就绪' }}
          </span>
        </span>
        <button
          v-if="account.codex_turn_ticket_history?.length || account.codex_turn_tickets?.some(ticket => ticket.ticket || ticket.cookie || ticket.history_count)"
          type="button"
          class="ml-2 text-blue-600 hover:underline dark:text-blue-400"
          title="查看历史打票日志"
          @click="openTicketHistory"
        >历史</button>
      </div>
      <div v-if="hasOpenAIUsageFallback" class="space-y-1">
        <UsageProgressBar
          v-if="usageInfo?.five_hour"
          label="5h"
          :utilization="usageInfo.five_hour.utilization"
          :resets-at="usageInfo.five_hour.resets_at"
          :window-stats="usageInfo.five_hour.window_stats"
          :show-now-when-idle="true"
          color="indigo"
        />
        <UsageProgressBar
          v-if="usageInfo?.seven_day"
          label="7d"
          :utilization="usageInfo.seven_day.utilization"
          :resets-at="usageInfo.seven_day.resets_at"
          :window-stats="usageInfo.seven_day.window_stats"
          :estimated-total-cost="openAISevenDayEstimatedTotalCost"
          :show-now-when-idle="true"
          color="emerald"
        />
        <!--
          Upstream codex /wham/usage quota query + reset. The local active-sampling
          refresh button is rendered via the pre-actions slot so the user sees a
          single row of related buttons instead of two stacked rows.
        -->
        <OpenAIQuotaResetCell :account="account" @account-updated="handleQuotaResetAccountUpdated">
          <template #pre-actions>
            <button
              type="button"
              class="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 hover:bg-blue-50 dark:text-blue-400 dark:hover:bg-blue-900/30 transition-colors disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="activeQueryLoading"
              @click="loadActiveUsage"
            >
              <svg
                class="h-2.5 w-2.5"
                :class="{ 'animate-spin': activeQueryLoading }"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  stroke-width="2"
                  d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
                />
              </svg>
              {{ t('admin.accounts.usageWindow.activeQuery') }}
            </button>
          </template>
        </OpenAIQuotaResetCell>
      </div>
      <div v-else-if="loading" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
        <div class="flex items-center gap-1">
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
      </div>
      <div v-else>
        <div class="text-xs text-gray-400">-</div>
        <!-- Always allow on-demand upstream quota query, even before local data exists. -->
        <OpenAIQuotaResetCell
          :account="account"
          class="mt-1"
          @account-updated="handleQuotaResetAccountUpdated"
        />
      </div>
    </template>

    <!-- Antigravity OAuth accounts: fetch usage from API -->
    <template v-else-if="account.platform === 'antigravity' && account.type === 'oauth'">
      <!-- 账户类型徽章 -->
      <div v-if="antigravityTierLabel" class="mb-1 flex items-center gap-1">
        <span
          :class="[
            'inline-block rounded px-1.5 py-0.5 text-[10px] font-medium',
            antigravityTierClass
          ]"
        >
          {{ antigravityTierLabel }}
        </span>
        <!-- 不合格账户警告图标 -->
        <span
          v-if="hasIneligibleTiers"
          class="group relative cursor-help"
        >
          <svg
            class="h-3.5 w-3.5 text-red-500"
            fill="currentColor"
            viewBox="0 0 20 20"
          >
            <path
              fill-rule="evenodd"
              d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7 4a1 1 0 11-2 0 1 1 0 012 0zm-1-9a1 1 0 00-1 1v4a1 1 0 102 0V6a1 1 0 00-1-1z"
              clip-rule="evenodd"
            />
          </svg>
          <span
            class="pointer-events-none absolute left-0 top-full z-50 mt-1 w-80 whitespace-normal break-words rounded bg-gray-900 px-3 py-2 text-xs leading-relaxed text-white opacity-0 shadow-lg transition-opacity group-hover:opacity-100 dark:bg-gray-700"
          >
            {{ t('admin.accounts.ineligibleWarning') }}
          </span>
        </span>
      </div>

      <!-- Forbidden state (403) -->
      <div v-if="isForbidden" class="space-y-1">
        <span
          :class="[
            'inline-block rounded px-1.5 py-0.5 text-[10px] font-medium',
            forbiddenBadgeClass
          ]"
        >
          {{ forbiddenLabel }}
        </span>
        <div v-if="validationURL" class="flex items-center gap-1">
          <a
            :href="validationURL"
            target="_blank"
            rel="noopener noreferrer"
            class="text-[10px] text-blue-600 hover:text-blue-800 hover:underline dark:text-blue-400 dark:hover:text-blue-300"
            :title="t('admin.accounts.openVerification')"
          >
            {{ t('admin.accounts.openVerification') }}
          </a>
          <button
            type="button"
            class="text-[10px] text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200"
            :title="t('admin.accounts.copyLink')"
            @click="copyValidationURL"
          >
            {{ linkCopied ? t('admin.accounts.linkCopied') : t('admin.accounts.copyLink') }}
          </button>
        </div>
      </div>

      <!-- Needs reauth (401) -->
      <div v-else-if="needsReauth" class="space-y-1">
        <span class="inline-block rounded px-1.5 py-0.5 text-[10px] font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300">
          {{ t('admin.accounts.needsReauth') }}
        </span>
      </div>

      <!-- Degraded error (non-403, non-401) -->
      <div v-else-if="usageInfo?.error" class="space-y-1">
        <span class="inline-block rounded px-1.5 py-0.5 text-[10px] font-medium bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300">
          {{ usageErrorLabel }}
        </span>
      </div>

      <!-- Loading state -->
      <div v-else-if="loading" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
      </div>

      <!-- Error state -->
      <div v-else-if="error" class="text-xs text-red-500">
        {{ error }}
      </div>

      <!-- Usage data from API -->
      <div v-else-if="hasAntigravityQuotaFromAPI" class="space-y-1">
        <!-- Gemini 3 Pro -->
        <UsageProgressBar
          v-if="antigravity3ProUsageFromAPI !== null"
          :label="t('admin.accounts.usageWindow.gemini3Pro')"
          :utilization="antigravity3ProUsageFromAPI.utilization"
          :resets-at="antigravity3ProUsageFromAPI.resetTime"
          color="indigo"
        />

        <!-- Gemini 3 Flash -->
        <UsageProgressBar
          v-if="antigravity3FlashUsageFromAPI !== null"
          :label="t('admin.accounts.usageWindow.gemini3Flash')"
          :utilization="antigravity3FlashUsageFromAPI.utilization"
          :resets-at="antigravity3FlashUsageFromAPI.resetTime"
          color="emerald"
        />

        <!-- Gemini 3 Image -->
        <UsageProgressBar
          v-if="antigravity3ImageUsageFromAPI !== null"
          :label="t('admin.accounts.usageWindow.gemini3Image')"
          :utilization="antigravity3ImageUsageFromAPI.utilization"
          :resets-at="antigravity3ImageUsageFromAPI.resetTime"
          color="purple"
        />

        <!-- Claude -->
        <UsageProgressBar
          v-if="antigravityClaudeUsageFromAPI !== null"
          :label="t('admin.accounts.usageWindow.claude')"
          :utilization="antigravityClaudeUsageFromAPI.utilization"
          :resets-at="antigravityClaudeUsageFromAPI.resetTime"
          color="amber"
        />

        <div v-if="aiCreditsDisplay" class="mt-1 text-[10px] text-gray-500 dark:text-gray-400">
          💳 {{ t('admin.accounts.aiCreditsBalance') }}: {{ aiCreditsDisplay }}
        </div>
      </div>
      <div v-else-if="aiCreditsDisplay" class="text-[10px] text-gray-500 dark:text-gray-400">
        💳 {{ t('admin.accounts.aiCreditsBalance') }}: {{ aiCreditsDisplay }}
      </div>
      <div v-else class="text-xs text-gray-400">-</div>
    </template>

    <!-- Grok OAuth accounts: passive xAI quota headers + local Sub2API usage -->
    <template v-else-if="account.platform === 'grok' && account.type === 'oauth'">
      <div v-if="loading" class="space-y-1.5">
        <div class="flex items-center gap-1">
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
      </div>
      <div v-else-if="error" class="text-xs text-red-500">
        {{ error }}
      </div>
      <div v-else-if="needsReauth" class="space-y-1">
        <span class="inline-block rounded px-1.5 py-0.5 text-[10px] font-medium bg-orange-100 text-orange-700 dark:bg-orange-900/40 dark:text-orange-300">
          {{ t('admin.accounts.needsReauth') }}
        </span>
      </div>
      <div v-else-if="isForbidden" class="space-y-1">
        <span class="inline-block rounded px-1.5 py-0.5 text-[10px] font-medium bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300">
          {{ usageInfo?.grok_entitlement_status || t('admin.accounts.forbidden') }}
        </span>
      </div>
      <div v-else-if="usageInfo" class="space-y-1">
        <!-- Free: only rolling 24h soft-gate bar. Paid: 7d + 30d + prepaid money. -->
        <template v-if="grokIsFree">
          <UsageProgressBar
            v-if="grokFreeTokenBar"
            label="24h"
            :title="t('admin.accounts.usageWindow.grokFreeQuota24hHint', { limit: formatCompactNumber(grokFreeTokenBar.limit) })"
            :utilization="grokFreeTokenBar.utilization"
            :window-stats="grokFreeQuotaUsage"
            :show-now-when-idle="true"
            color="emerald"
          />
          <div v-else-if="grokQuotaUnknown" class="text-[10px] text-gray-500 dark:text-gray-400">
            {{ grokQuotaUnknownLabel }}
          </div>
        </template>
        <template v-else>
          <UsageProgressBar
            v-if="grokWeeklyBillingBar"
            label="7d"
            :utilization="grokWeeklyBillingBar.utilization"
            :resets-at="grokWeeklyBillingBar.resetsAt"
            :window-stats="grokWeeklyBillingBar.windowStats"
            :show-now-when-idle="true"
            color="indigo"
          />
          <UsageProgressBar
            v-if="grokMonthlyBillingBar"
            label="30d"
            :utilization="grokMonthlyBillingBar.utilization"
            :resets-at="grokMonthlyBillingBar.resetsAt"
            :window-stats="grokMonthlyBillingBar.windowStats"
            :show-now-when-idle="true"
            color="indigo"
          />
          <div
            v-if="grokPrepaidMoneyLine"
            class="flex flex-wrap items-center gap-1 text-[10px] text-gray-500 dark:text-gray-400"
          >
            <span
              v-if="grokPrepaidMoneyLine.showPrepaid"
              class="rounded bg-emerald-50 px-1 py-0.5 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300"
              :title="t('admin.accounts.usageWindow.grokPrepaid')"
            >
              {{ t('admin.accounts.usageWindow.grokPrepaid') }} ${{ grokPrepaidMoneyLine.prepaid }}
            </span>
            <span
              v-if="grokPrepaidMoneyLine.showUsedLimit"
              :title="t('admin.accounts.usageWindow.grokMonthlyLimit')"
            >
              {{ t('admin.accounts.usageWindow.grokUsed') }}
              {{ grokPrepaidMoneyLine.used }}/{{ grokPrepaidMoneyLine.limit }}
            </span>
          </div>
          <div v-if="grokQuotaUnknown" class="text-[10px] text-gray-500 dark:text-gray-400">
            {{ grokQuotaUnknownLabel }}
          </div>
        </template>
        <div v-if="usageInfo.error" class="truncate text-xs text-amber-600 dark:text-amber-400 max-w-[200px]" :title="usageInfo.error">
          {{ usageErrorLabel }}
        </div>
        <div v-if="grokRetryAfterLabel" class="text-[10px] text-amber-600 dark:text-amber-400">
          {{ t('admin.accounts.usageWindow.grokRetryAfter', { time: grokRetryAfterLabel }) }}
        </div>
        <GrokQuotaProbeCell :account="account" compact @probed="handleGrokProbed" />
      </div>
      <div v-else class="space-y-1">
        <div class="text-xs text-gray-400">-</div>
        <GrokQuotaProbeCell :account="account" compact @probed="handleGrokProbed" />
      </div>
    </template>

    <!-- CN providers (Kimi / Zhipu / DeepSeek): coding-plan quota or payg balance -->
    <template v-else-if="account.platform === 'kimi' || account.platform === 'zhipu' || account.platform === 'deepseek' || account.platform === 'minimax' || account.platform === 'opencode_go'">
      <!-- 挂在 CN 平台下的 Ollama Cloud 账号（资格由后端下发 eligible）：用量由
           Ollama 用量窗口负责。这类账号不是国产厂商订阅，CN 的额度/余额探测端点由
           base_url 衍生，对 ollama.com 会被后端出站 URL 白名单拒绝，渲染出来只会
           给用户一行探测报错，因此不再渲染 CN 子单元格与占位符。 -->
      <OllamaCloudUsageCell
        v-if="account.ollama_cloud_usage?.eligible"
        :account="account"
        @updated="handleOllamaCloudUsageUpdated"
      />
      <div v-else class="space-y-1">
        <!-- 子单元格各自按 模式×平台 判定可见；两者都不可见时（智谱 payg 无公开
             余额端点、coding 探测也不适用）才回落到占位符。 -->
        <div
          v-if="!cnQuotaCellVisible && !cnBalanceCellVisible"
          class="text-xs text-gray-400"
          :title="t('admin.accounts.cnProviders.noBalanceEndpoint')"
        >-</div>
        <CNProviderQuotaCell :account="account" />
        <CNProviderBalanceCell :account="account" />
      </div>
    </template>

    <!-- Gemini platform: show quota + local usage window -->
    <template v-else-if="account.platform === 'gemini'">
      <!-- Auth Type + Tier Badge (first line) -->
      <div v-if="geminiAuthTypeLabel" class="mb-1 flex items-center gap-1">
        <span
          :class="[
            'inline-block rounded px-1.5 py-0.5 text-[10px] font-medium',
            geminiTierClass
          ]"
        >
          {{ geminiAuthTypeLabel }}
        </span>
        <!-- Help icon -->
        <span
          class="group relative cursor-help"
        >
          <svg
            class="h-3.5 w-3.5 text-gray-400 hover:text-gray-600 dark:text-gray-500 dark:hover:text-gray-300"
            fill="currentColor"
            viewBox="0 0 20 20"
          >
            <path
              fill-rule="evenodd"
              d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-8-3a1 1 0 00-.867.5 1 1 0 11-1.731-1A3 3 0 0113 8a3.001 3.001 0 01-2 2.83V11a1 1 0 11-2 0v-1a1 1 0 011-1 1 1 0 100-2zm0 8a1 1 0 100-2 1 1 0 000 2z"
              clip-rule="evenodd"
            />
          </svg>
          <span
            class="pointer-events-none absolute left-0 top-full z-50 mt-1 w-80 whitespace-normal break-words rounded bg-gray-900 px-3 py-2 text-xs leading-relaxed text-white opacity-0 shadow-lg transition-opacity group-hover:opacity-100 dark:bg-gray-700"
          >
            <div class="font-semibold mb-1">{{ t('admin.accounts.gemini.quotaPolicy.title') }}</div>
            <div class="mb-2 text-gray-300">{{ t('admin.accounts.gemini.quotaPolicy.note') }}</div>
            <div class="space-y-1">
              <div><strong>{{ geminiQuotaPolicyChannel }}:</strong></div>
              <div class="pl-2">• {{ geminiQuotaPolicyLimits }}</div>
              <div class="mt-2">
                <a :href="geminiQuotaPolicyDocsUrl" target="_blank" rel="noopener noreferrer" class="text-blue-400 hover:text-blue-300 underline">
                  {{ t('admin.accounts.gemini.quotaPolicy.columns.docs') }} →
                </a>
              </div>
            </div>
          </span>
        </span>
      </div>

      <!-- Usage data or unlimited flow -->
      <div class="space-y-1">
        <div
          v-if="showGeminiTodayStats && todayStats"
          class="mb-0.5 flex items-center"
        >
          <div class="flex items-center gap-1.5 text-[9px] text-gray-500 dark:text-gray-400">
            <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800">
              {{ formatKeyRequests }} req
            </span>
            <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800">
              {{ formatKeyTokens }}
            </span>
            <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800" :title="t('usage.accountBilled')">
              A ${{ formatKeyCost }}
            </span>
            <span
              v-if="todayStats.user_cost != null"
              class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800"
              :title="t('usage.userBilled')"
            >
              U ${{ formatKeyUserCost }}
            </span>
          </div>
        </div>
        <div
          v-else-if="showGeminiTodayStats && todayStatsLoading"
          class="mb-0.5 flex items-center gap-1"
        >
          <div class="h-3 w-10 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-8 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          <div class="h-3 w-12 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        </div>
        <div v-if="loading" class="space-y-1">
          <div class="flex items-center gap-1">
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-1.5 w-8 animate-pulse rounded-full bg-gray-200 dark:bg-gray-700"></div>
            <div class="h-3 w-[32px] animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
          </div>
        </div>
        <div v-else-if="error" class="text-xs text-red-500">
          {{ error }}
        </div>
        <!-- Gemini: show daily usage bars when available -->
        <div v-else-if="geminiUsageAvailable" class="space-y-1">
          <UsageProgressBar
            v-for="bar in geminiUsageBars"
            :key="bar.key"
            :label="bar.label"
            :utilization="bar.utilization"
            :resets-at="bar.resetsAt"
            :window-stats="bar.windowStats"
            :color="bar.color"
          />
          <p class="mt-1 text-[9px] leading-tight text-gray-400 dark:text-gray-500 italic">
            * {{ t('admin.accounts.gemini.quotaPolicy.simulatedNote') || 'Simulated quota' }}
          </p>
        </div>
        <!-- AI Studio Client OAuth: show unlimited flow (no usage tracking) -->
        <div v-else class="text-xs text-gray-400">
          {{ t('admin.accounts.gemini.rateLimit.unlimited') }}
        </div>
      </div>
    </template>

    <!-- Other accounts: no usage window -->
    <template v-else>
      <div class="text-xs text-gray-400">-</div>
    </template>
  </div>
  <!-- Non-OAuth/Setup-Token accounts -->
  <div ref="rootRef" v-else>
    <!-- Gemini API Key accounts: show quota info -->
    <AccountQuotaInfo v-if="account.platform === 'gemini'" :account="account" />
    <!-- Key/Bedrock accounts: show today stats + optional quota bars -->
    <div v-else class="space-y-1">
      <OllamaCloudUsageCell
        v-if="account.ollama_cloud_usage?.eligible"
        :account="account"
        @updated="handleOllamaCloudUsageUpdated"
      />
      <!-- Today stats row (requests, tokens, cost, user_cost) -->
      <div
        v-if="todayStats"
        class="mb-0.5 flex items-center"
      >
        <div class="flex items-center gap-1.5 text-[9px] text-gray-500 dark:text-gray-400">
          <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800">
            {{ formatKeyRequests }} req
          </span>
          <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800">
            {{ formatKeyTokens }}
          </span>
          <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800" :title="t('usage.accountBilled')">
            A ${{ formatKeyCost }}
          </span>
          <span
            v-if="todayStats.user_cost != null"
            class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800"
            :title="t('usage.userBilled')"
          >
            U ${{ formatKeyUserCost }}
          </span>
        </div>
      </div>
      <!-- Loading skeleton for today stats -->
      <div
        v-else-if="todayStatsLoading"
        class="mb-0.5 flex items-center gap-1"
      >
        <div class="h-3 w-10 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        <div class="h-3 w-8 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
        <div class="h-3 w-12 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
      </div>

      <!-- API Key accounts with quota limits: show progress bars -->
      <UsageProgressBar
        v-if="quotaDailyBar"
        label="1d"
        :utilization="quotaDailyBar.utilization"
        :resets-at="quotaDailyBar.resetsAt"
        color="indigo"
      />
      <UsageProgressBar
        v-if="quotaWeeklyBar"
        label="7d"
        :utilization="quotaWeeklyBar.utilization"
        :resets-at="quotaWeeklyBar.resetsAt"
        color="emerald"
      />
      <UsageProgressBar
        v-if="quotaTotalBar"
        label="total"
        :utilization="quotaTotalBar.utilization"
        color="purple"
      />

      <!-- No data at all -->
      <div
        v-if="!todayStats && !todayStatsLoading && !hasApiKeyQuota && !account.ollama_cloud_usage?.eligible"
        class="text-xs text-gray-400"
      >-</div>
    </div>
  </div>
  <BaseDialog :show="showCookieValidationLogs" :title="`Host 验证日志 · ${account.name}`" width="extra-wide" @close="showCookieValidationLogs = false">
    <div class="space-y-3">
      <div class="flex items-center justify-between gap-3">
        <p class="text-xs text-gray-500">仅记录实际发起的绑定尝试；已绑定账号和冷静期内的自动扫描不会生成日志。</p>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="cookieValidationLoading" @click="loadCookieValidationLogs">刷新</button>
      </div>
      <div v-if="cookieValidationLoading" class="py-8 text-center text-sm text-gray-500">加载中...</div>
      <div v-else-if="cookieValidationError" class="py-6 text-sm text-red-500">{{ cookieValidationError }}</div>
      <div v-else-if="cookieValidationAttempts.length" class="max-h-[60vh] space-y-3 overflow-y-auto pr-1">
        <article v-for="attempt in cookieValidationAttempts" :key="attempt.id" class="rounded border border-gray-200 p-3 dark:border-dark-600">
          <div class="mb-3 flex flex-wrap items-center justify-between gap-2 text-xs">
            <span class="break-all font-mono font-medium">{{ attempt.host || '未知 Host' }}</span>
            <span class="text-gray-500">{{ formatValidationTime(attempt.startedAt) }}</span>
          </div>
          <ol class="space-y-2 border-l border-gray-200 pl-4 dark:border-dark-600">
            <li v-for="log in attempt.logs" :key="log.id" class="relative text-xs">
              <span class="absolute -left-[19px] top-1 h-2 w-2 rounded-full" :class="validationStageDot(log.stage)" />
              <div class="flex flex-wrap items-center gap-2">
                <span class="font-medium">{{ validationStageLabel(log.stage) }}</span>
                <span class="text-gray-400">{{ formatValidationTime(log.created_at) }}</span>
                <span v-if="log.status_code" class="text-gray-400">HTTP {{ log.status_code }}</span>
              </div>
              <p class="mt-0.5 text-gray-600 dark:text-gray-300">{{ log.message }}</p>
              <details v-if="log.validation_response" class="mt-1">
                <summary class="cursor-pointer text-blue-600 dark:text-blue-400">查看验证响应</summary>
                <pre class="mt-1 max-h-40 overflow-auto whitespace-pre-wrap break-all rounded bg-gray-50 p-2 font-mono text-[10px] dark:bg-dark-800">{{ log.validation_response }}</pre>
              </details>
            </li>
          </ol>
        </article>
      </div>
      <div v-else class="py-8 text-center text-sm text-gray-500">暂无 Host 验证日志</div>
      <Pagination
        v-if="cookieValidationTotal > 0"
        :page="cookieValidationPage"
        :page-size="cookieValidationPageSize"
        :total="cookieValidationTotal"
        :show-page-size-selector="false"
        @update:page="changeCookieValidationPage"
      />
    </div>
  </BaseDialog>
  <BaseDialog :show="showTicketHistory" title="打票历史" width="extra-wide" @close="showTicketHistory = false">
    <div v-if="ticketHistoryLoading" class="py-8 text-center text-sm text-gray-500">加载中...</div>
    <div v-else-if="ticketHistoryError" class="py-6 text-sm text-red-500">{{ ticketHistoryError }}</div>
    <div v-else-if="ticketHistory.length || account.codex_turn_tickets?.some(ticket => ticket.ticket || ticket.cookie) || currentTicketSessionID || currentTicketCookie" class="max-h-[60vh] space-y-3 overflow-y-auto">
      <div v-if="currentTicketSessionID || currentTicketCookie" class="rounded border border-emerald-200 p-3 text-xs dark:border-emerald-800">
        <div class="mb-2 font-medium">当前账号绑定值</div>
        <div v-if="currentTicketSessionID" class="mb-2 break-all font-mono"><span class="font-medium font-sans">session_id:</span> {{ currentTicketSessionID }}</div>
        <div v-if="currentTicketCookie" class="break-all font-mono"><span class="font-medium font-sans">Cookie:</span> {{ currentTicketCookie }}</div>
        <div v-if="currentTicketCookieHost" class="mt-1 break-all font-mono"><span class="font-medium font-sans">Cookie host:</span> {{ currentTicketCookieHost }}</div>
        <button v-if="currentTicketCookiePayload" type="button" class="mt-1 rounded border px-2 py-1 text-[10px] text-blue-600 hover:underline" @click="openCookiePayload(currentTicketCookiePayload)">查看 Cookie payload</button>
        <div v-if="cookiePayloadExpiration(currentTicketCookiePayload) !== null" class="mt-1 font-mono">Cookie exp: {{ cookiePayloadExpiration(currentTicketCookiePayload) }}（剩余 {{ cookiePayloadRemainingSeconds(currentTicketCookiePayload) }}s）</div>
      </div>
      <div v-for="ticket in account.codex_turn_tickets || []" :key="`current-${ticket.expires_at || ticket.length}`" class="rounded border border-blue-200 p-3 text-xs dark:border-blue-800">
        <div class="mb-2 font-medium">当前票据</div>
        <div v-if="ticket.ticket" class="break-all font-mono">{{ ticket.ticket }}</div>
      </div>
      <div v-for="(item, index) in ticketHistory" :key="`${item.captured_at}-${index}`" class="rounded border border-gray-200 p-3 text-xs dark:border-dark-600">
        <div class="mb-2 flex flex-wrap gap-3 text-gray-500 dark:text-gray-400">
          <span>{{ item.captured_at }}</span>
          <span>{{ item.source || '-' }}</span>
          <span>HTTP {{ item.status_code || '-' }}</span>
        </div>
        <div class="mb-2 flex flex-wrap gap-2">
          <button v-if="item.request_state" type="button" class="rounded border px-2 py-1 font-mono text-[10px] font-medium" :class="stateBadgeClass(item.request_state)" @click="openStateDetails(item, 'request')">request state · {{ item.request_state.length }}</button>
          <button v-if="item.response_state || item.ticket" type="button" class="rounded border px-2 py-1 font-mono text-[10px] font-medium" :class="stateBadgeClass(item.response_state || item.ticket)" @click="openStateDetails(item, 'response')">response state · {{ (item.response_state || item.ticket || '').length }}</button>
        </div>
        <div v-if="item.session_id" class="mb-2 break-all"><span class="font-medium">session_id:</span> {{ item.session_id }}</div>
        <div v-if="item.cookie" class="mb-2 break-all font-mono"><span class="font-medium font-sans">Cookie:</span> {{ item.cookie }}</div>
        <div v-if="item.cookie_host" class="mb-2 break-all font-mono"><span class="font-medium font-sans">Cookie host:</span> {{ item.cookie_host }}</div>
        <button v-if="item.cookie_payload" type="button" class="mb-2 rounded border px-2 py-1 text-[10px] text-blue-600 hover:underline" @click="openCookiePayload(item.cookie_payload)">查看 Cookie payload</button>
        <div v-if="cookiePayloadExpiration(item.cookie_payload) !== null" class="mb-2 font-mono">Cookie exp: {{ cookiePayloadExpiration(item.cookie_payload) }}（剩余 {{ cookiePayloadRemainingSeconds(item.cookie_payload) }}s）</div>
        <div v-if="item.ticket" class="break-all font-mono"><span class="font-medium font-sans">ticket:</span> {{ item.ticket }}</div>
        <button
          v-if="item.response"
          type="button"
          class="mt-2 rounded border border-violet-200 bg-violet-50 px-2 py-1 text-[10px] font-medium text-violet-700 hover:bg-violet-100 dark:border-violet-700 dark:bg-violet-900/20 dark:text-violet-300"
          @click="openResponseDetails(item)"
        >查看完整响应</button>
      </div>
    </div>
    <div v-else class="text-sm text-gray-500">暂无打票记录</div>
  </BaseDialog>
  <BaseDialog :show="showResponseDetails" title="打票完整响应" width="extra-wide" @close="showResponseDetails = false">
    <div v-if="responseDetails" class="space-y-3 text-sm">
      <div class="flex flex-wrap gap-3 text-gray-500 dark:text-gray-400">
        <span>{{ responseDetails.item.captured_at }}</span>
        <span>{{ responseDetails.item.source || '-' }}</span>
        <span>HTTP {{ responseDetails.item.status_code || '-' }}</span>
      </div>
      <pre class="max-h-[65vh] overflow-auto whitespace-pre-wrap break-all rounded border border-gray-200 bg-gray-50 p-3 font-mono text-xs leading-5 text-gray-800 dark:border-dark-600 dark:bg-dark-900 dark:text-gray-200">{{ responseDetails.value }}</pre>
    </div>
  </BaseDialog>
  <BaseDialog :show="showCookiePayload" title="Cookie payload" width="wide" @close="showCookiePayload = false">
    <pre v-if="cookiePayloadDetails" class="max-h-[60vh] overflow-auto whitespace-pre-wrap break-all rounded bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800">{{ JSON.stringify(cookiePayloadDetails, null, 2) }}</pre>
  </BaseDialog>
  <BaseDialog :show="showStateDetails" :title="stateDetails?.kind === 'request' ? 'Request state' : 'Response state'" width="wide" @close="showStateDetails = false">
    <div v-if="stateDetails" class="space-y-3 text-sm">
      <div class="flex flex-wrap gap-3 text-gray-500 dark:text-gray-400">
        <span>{{ stateDetails.item.captured_at }}</span><span>{{ stateDetails.item.source || '-' }}</span><span>HTTP {{ stateDetails.item.status_code || '-' }}</span>
        <span :class="stateDetails.value.length === 780 ? 'text-emerald-600' : 'text-red-600'">{{ stateDetails.value.length === 780 ? '780 valid' : `${stateDetails.value.length} non-780` }}</span>
      </div>
      <pre class="max-h-[45vh] overflow-auto whitespace-pre-wrap break-all rounded bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800">{{ stateDetails.value || '-' }}</pre>
      <div v-if="stateDetails.item.session_id" class="break-all font-mono text-xs">session_id: {{ stateDetails.item.session_id }}</div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import CookieBindingEstimate from '@/components/account/CookieBindingEstimate.vue'
import { ref, computed, onMounted, onBeforeUnmount, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { Account, AccountUsageInfo, GeminiCredentials, WindowStats } from '@/types'
import { buildOpenAIUsageRefreshKey } from '@/utils/accountUsageRefresh'
import { enqueueUsageRequest } from '@/utils/usageLoadQueue'
import { formatCompactNumber } from '@/utils/format'
import UsageProgressBar from './UsageProgressBar.vue'
import AccountQuotaInfo from './AccountQuotaInfo.vue'
import OpenAIQuotaResetCell from './OpenAIQuotaResetCell.vue'
import GrokQuotaProbeCell from './GrokQuotaProbeCell.vue'
import CNProviderQuotaCell from './CNProviderQuotaCell.vue'
import CNProviderBalanceCell from './CNProviderBalanceCell.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import OllamaCloudUsageCell from './OllamaCloudUsageCell.vue'
import { cnQuotaCellVisible as cnQuotaCellVisibleFn, cnBalanceCellVisible as cnBalanceCellVisibleFn } from './credentialsBuilder'
import * as cookieAPI from '@/api/admin/cookies'
import type { CookieLog } from '@/api/admin/cookies'

const showCookieValidationLogs = ref(false)
const cookieValidationLogs = ref<CookieLog[]>([])
const cookieValidationLoading = ref(false)
const cookieValidationError = ref('')
const cookieValidationPage = ref(1)
const cookieValidationPageSize = 10
const cookieValidationTotal = ref(0)
const cookieValidationAttempts = computed(() => {
  const attempts = new Map<string, CookieLog[]>()
  for (const log of cookieValidationLogs.value) {
    const id = log.attempt_id || log.id
    const values = attempts.get(id) || []
    values.push(log)
    attempts.set(id, values)
  }
  return Array.from(attempts.entries()).map(([id, values]) => {
    const logs = [...values].sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime())
    return { id, logs, host: logs[0]?.host || '', startedAt: logs[0]?.created_at || '' }
  })
})
const validationStageLabels: Record<string, string> = {
  host_selected: '准备绑定 Host',
  host_cookie_bound: 'Host Cookie 已绑定',
  validation_started: '开始验证提示词',
  validation_succeeded: '验证成功',
  validation_rejected: '验证未通过',
  validation_failed: '验证失败',
  host_bound: 'Host 已绑定',
  host_bind_failed: 'Host 绑定失败',
  ws_build_started: '开始构建 WS',
  ws_build_succeeded: 'WS 构建成功',
  ws_build_failed: 'WS 构建失败',
  ws_build_skipped: 'WS 构建已跳过'
}
const validationStageLabel = (stage?: string) => validationStageLabels[stage || ''] || '历史记录'
const validationStageDot = (stage?: string) => {
  if (stage === 'validation_rejected' || stage === 'validation_failed' || stage === 'host_bind_failed' || stage === 'ws_build_failed') return 'bg-red-500'
  if (stage === 'validation_succeeded' || stage === 'host_bound' || stage === 'ws_build_succeeded') return 'bg-emerald-500'
  return 'bg-blue-500'
}
const formatValidationTime = (value: string) => value ? new Date(value).toLocaleString() : '-'
const cookieRequestError = (error: unknown, fallback: string) => {
  if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') return error.message
  return fallback
}
const loadCookieValidationLogs = async () => {
  cookieValidationLoading.value = true
  cookieValidationError.value = ''
  try {
    const result = await cookieAPI.getValidationLogs({ account_id: props.account.id, page: cookieValidationPage.value, page_size: cookieValidationPageSize })
    cookieValidationLogs.value = result.items || []
    cookieValidationTotal.value = result.total || 0
  } catch (error) {
    cookieValidationError.value = cookieRequestError(error, '加载 Host 验证日志失败')
  } finally {
    cookieValidationLoading.value = false
  }
}
const openCookieValidationLogs = () => {
  showCookieValidationLogs.value = true
  cookieValidationPage.value = 1
  void loadCookieValidationLogs()
}
const changeCookieValidationPage = (page: number) => {
  cookieValidationPage.value = page
  void loadCookieValidationLogs()
}

const showTicketHistory = ref(false)
const ticketHistory = ref<NonNullable<Account['codex_turn_ticket_history']>>([])
const ticketHistoryLoading = ref(false)
const ticketHistoryError = ref('')
const currentTicketSessionID = ref('')
const currentTicketCookie = ref('')
const currentTicketCookieHost = ref('')
const currentTicketCookiePayload = ref<Record<string, unknown> | null>(null)
const showStateDetails = ref(false)
const stateDetails = ref<{ item: NonNullable<Account['codex_turn_ticket_history']>[number]; kind: 'request' | 'response'; value: string } | null>(null)
const showResponseDetails = ref(false)
const responseDetails = ref<{ item: NonNullable<Account['codex_turn_ticket_history']>[number]; value: string } | null>(null)
const showCookiePayload = ref(false)
const cookiePayloadDetails = ref<Record<string, unknown> | null>(null)
const stateBadgeClass = (state?: string) => state && state.length === 780
  ? 'border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
  : 'border-red-300 bg-red-50 text-red-700 dark:border-red-700 dark:bg-red-900/30 dark:text-red-300'
const openStateDetails = (item: NonNullable<Account['codex_turn_ticket_history']>[number], kind: 'request' | 'response') => {
  stateDetails.value = { item, kind, value: kind === 'request' ? (item.request_state || '') : (item.response_state || item.ticket || '') }
  showStateDetails.value = true
}
const openResponseDetails = (item: NonNullable<Account['codex_turn_ticket_history']>[number]) => {
  if (!item.response) return
  let value = item.response
  try {
    value = JSON.stringify(JSON.parse(item.response), null, 2)
  } catch {
    // Keep the original response when it is not JSON.
  }
  responseDetails.value = { item, value }
  showResponseDetails.value = true
}
const openCookiePayload = (payload?: Record<string, unknown>) => {
  if (!payload) return
  cookiePayloadDetails.value = payload
  showCookiePayload.value = true
}
const cookiePayloadExpiration = (payload?: Record<string, unknown> | null): number | null => {
  const exp = Number(payload?.exp)
  return Number.isFinite(exp) && exp > 0 ? Math.floor(exp) : null
}
const cookiePayloadRemainingSeconds = (payload?: Record<string, unknown> | null): number => {
  const exp = cookiePayloadExpiration(payload)
  return exp === null ? 0 : Math.max(0, exp - Math.floor(Date.now() / 1000))
}
const cookieBindingNow = ref(Date.now())
let cookieBindingTimer: ReturnType<typeof setInterval> | undefined
watch(() => !!props.account.cookie_binding, (enabled) => {
  if (cookieBindingTimer) clearInterval(cookieBindingTimer)
  cookieBindingTimer = undefined
  cookieBindingNow.value = Date.now()
  if (enabled) cookieBindingTimer = setInterval(() => { cookieBindingNow.value = Date.now() }, 1000)
}, { immediate: true })
onBeforeUnmount(() => { if (cookieBindingTimer) clearInterval(cookieBindingTimer) })
const cookieSchedulingBlockLabel = computed(() => {
  const binding = props.account.cookie_binding
  if (!binding?.scheduling_guard_enabled) return ''
  const reasons: Record<string, string> = {
	  cookie_host_monitoring: '固定 Host 恢复监控中',
    cookie_host_unbound: binding.degraded_group_id ? '未绑定且无可轮换 Host' : '未绑定 Cookie Host',
    cookie_host_binding_expired: 'Host 绑定已超时',
    cookie_host_binding_invalid: 'Host 绑定有效期缺失或无效'
  }
  if (binding.scheduling_blocked || binding.degraded_group_id) return reasons[binding.scheduling_block_reason || ''] || '等待有效 Host 绑定'
  if (!binding.host) return reasons.cookie_host_unbound
  const expiresAt = Date.parse(binding.binding_expires_at || '')
  if (!Number.isFinite(expiresAt)) return reasons.cookie_host_binding_invalid
  return expiresAt <= cookieBindingNow.value ? reasons.cookie_host_binding_expired : ''
})
const bindingRemaining = (account: Account): number => {
  const value = account.cookie_binding?.binding_expires_at
  if (!value) return 0
  return Math.max(0, Math.ceil((new Date(value).getTime() - cookieBindingNow.value) / 1000))
}
const bindingRotationLabel = (account: Account): string => {
  const value = account.cookie_binding?.rotation_at
  if (!value) return ''
  const seconds = Math.max(0, Math.ceil((new Date(value).getTime() - cookieBindingNow.value) / 1000))
  return seconds > 0 ? `${seconds}s 后开始轮换` : '正在轮换'
}

const openTicketHistory = async () => {
  showTicketHistory.value = true
  ticketHistory.value = props.account.codex_turn_ticket_history || []
  currentTicketSessionID.value = String(props.account.extra?.session_id || '')
  currentTicketCookie.value = String(props.account.codex_turn_tickets?.[0]?.cookie || '')
  currentTicketCookieHost.value = String(props.account.codex_turn_tickets?.[0]?.cookie_host || '')
  currentTicketCookiePayload.value = props.account.codex_turn_tickets?.[0]?.cookie_payload || null
  ticketHistoryError.value = ''
  ticketHistoryLoading.value = true
  try {
    const detail = await adminAPI.accounts.getById(props.account.id)
    ticketHistory.value = detail.codex_turn_ticket_history || []
    currentTicketSessionID.value = String(detail.extra?.session_id || '')
    currentTicketCookie.value = String(detail.codex_turn_tickets?.[0]?.cookie || '')
    currentTicketCookieHost.value = String(detail.codex_turn_tickets?.[0]?.cookie_host || '')
    currentTicketCookiePayload.value = detail.codex_turn_tickets?.[0]?.cookie_payload || null
  } catch (error) {
    ticketHistoryError.value = '加载打票历史失败'
    console.error('Failed to load Codex ticket history:', error)
  } finally {
    ticketHistoryLoading.value = false
  }
}

// Module-level cache shared across all AccountUsageCell instances
const _usageCache = new Map<number, { data: AccountUsageInfo; ts: number }>()
const USAGE_CACHE_TTL = 5 * 60 * 1000 // 5 minutes

const props = withDefaults(
  defineProps<{
    account: Account
    todayStats?: WindowStats | null
    todayStatsLoading?: boolean
    manualRefreshToken?: number
    batchedUsage?: AccountUsageInfo | null
    batchedUsageError?: string | null
    batchedUsageLoading?: boolean
    requestBatchedUsage?: ((account: Account, options?: { force?: boolean }) => void) | null
    cookieHosts?: string[]
  }>(),
  {
    todayStats: null,
    todayStatsLoading: false,
    manualRefreshToken: 0,
    batchedUsage: null,
    batchedUsageError: null,
    batchedUsageLoading: false,
    requestBatchedUsage: null,
    cookieHosts: () => []
  }
)

const emit = defineEmits<{
  'account-updated': [account: Account]
  'usage-loaded': [usage: AccountUsageInfo]
}>()

const cookieHostSaving = ref(false)
const cookieHostError = ref('')
const selectedCookieHost = computed(() => {
  const value = props.account.extra?.codex_cookie_host
  return typeof value === 'string' ? value : (props.account.cookie_binding?.host || '')
})
const cookieHosts = computed(() => {
  const values = new Set(props.cookieHosts || [])
  if (selectedCookieHost.value) values.add(selectedCookieHost.value)
  return Array.from(values).sort()
})
const cookieHostCooldownLabel = (host: string) => {
  const raw = props.account.cookie_binding?.cooldowns?.[host]
  if (!raw) return ''
  const remaining = Math.max(0, Math.ceil((new Date(raw).getTime() - Date.now()) / 1000))
  return remaining > 0 ? `（此账号冷静至 ${new Date(raw).toLocaleString()}）` : ''
}
const handleCookieHostChange = async (event: Event) => {
  const host = String((event.target as HTMLSelectElement).value || '').trim().toLowerCase()
  if (host === selectedCookieHost.value) return
  cookieHostSaving.value = true
  cookieHostError.value = ''
  try {
    const updated = await adminAPI.accounts.update(props.account.id, { extra: host ? { codex_cookie_host: host } : {} })
    emit('account-updated', updated)
  } catch (error) {
    console.error('Failed to update cookie host binding:', error)
    cookieHostError.value = cookieRequestError(error, 'Cookie Host 更新失败')
  } finally {
    cookieHostSaving.value = false
  }
}

const { t } = useI18n()
const desktopViewportQuery = '(min-width: 768px)'

const unmounted = ref(false)
onBeforeUnmount(() => { unmounted.value = true })

const loading = ref(false)
const activeQueryLoading = ref(false)
const error = ref<string | null>(null)
const usageInfo = ref<AccountUsageInfo | null>(null)
watch(usageInfo, (usage) => {
  if (usage) emit('usage-loaded', usage)
})
const rootRef = ref<HTMLElement | null>(null)
const isDesktopViewport = ref(
  typeof window === 'undefined' ? true : window.matchMedia(desktopViewportQuery).matches
)
const hasEnteredViewport = ref(false)
const pendingAutoLoad = ref(false)
const pendingAutoLoadSource = ref<'passive' | 'active' | undefined>(undefined)

let desktopViewportMediaQuery: MediaQueryList | null = null
let desktopViewportListener: ((event: MediaQueryListEvent) => void) | null = null
let visibilityObserver: IntersectionObserver | null = null

// Show usage windows for OAuth and Setup Token accounts
const showUsageWindows = computed(() => {
  // Gemini: we can always compute local usage windows from DB logs (simulated quotas).
  if (props.account.platform === 'gemini') return true
  // CN providers: apikey 账号也有滚动用量窗口（coding plan）或余额（payg），
  // 由 CNProviderQuotaCell / CNProviderBalanceCell 自行探测与展示。
  if (
    props.account.platform === 'kimi' ||
    props.account.platform === 'zhipu' ||
    props.account.platform === 'deepseek' ||
    props.account.platform === 'minimax' ||
    props.account.platform === 'opencode_go'
  ) {
    return true
  }
  return props.account.type === 'oauth' || props.account.type === 'setup-token'
})

const shouldFetchUsage = computed(() => {
  if (props.account.platform === 'anthropic') {
    return props.account.type === 'oauth' || props.account.type === 'setup-token'
  }
  if (props.account.platform === 'gemini') {
    return true
  }
  if (props.account.platform === 'antigravity') {
    return props.account.type === 'oauth'
  }
  if (props.account.platform === 'grok') {
    return props.account.type === 'oauth'
  }
  if (props.account.platform === 'openai') {
    return props.account.type === 'oauth'
  }
  return false
})

// CN 供应商子单元格可见性（与 CNProviderQuotaCell / CNProviderBalanceCell 共用
// credentialsBuilder 的单一实现）：都不可见时显示 `-` 占位符。
const cnAccountMode = computed(() => {
  const mode = props.account.credentials?.account_mode
  return typeof mode === 'string' ? mode : ''
})
const cnQuotaCellVisible = computed(() => cnQuotaCellVisibleFn(props.account.platform, cnAccountMode.value))
const cnBalanceCellVisible = computed(() => cnBalanceCellVisibleFn(props.account.platform, cnAccountMode.value))

const isBatchManaged = computed(() => typeof props.requestBatchedUsage === 'function')

const showGeminiTodayStats = computed(() => {
  return props.account.platform === 'gemini' && props.account.type === 'service_account'
})

const geminiUsageAvailable = computed(() => {
  return (
    !!usageInfo.value?.gemini_shared_daily ||
    !!usageInfo.value?.gemini_pro_daily ||
    !!usageInfo.value?.gemini_flash_daily ||
    !!usageInfo.value?.gemini_shared_minute ||
    !!usageInfo.value?.gemini_pro_minute ||
    !!usageInfo.value?.gemini_flash_minute
  )
})

const hasOpenAIUsageFallback = computed(() => {
  if (props.account.platform !== 'openai' || props.account.type !== 'oauth') return false
  return !!usageInfo.value?.five_hour || !!usageInfo.value?.seven_day
})

const formatTicketRemaining = (seconds: number) => {
  const total = Math.max(0, Math.floor(seconds || 0))
  return `${Math.floor(total / 60)}m${String(total % 60).padStart(2, '0')}s`
}

const openAISevenDayEstimatedTotalCost = computed(() => {
  const sevenDay = usageInfo.value?.seven_day
  const utilization = sevenDay?.utilization
  const currentCost = sevenDay?.window_stats?.cost
  if (
    typeof utilization !== 'number' ||
    typeof currentCost !== 'number' ||
    !Number.isFinite(utilization) ||
    !Number.isFinite(currentCost) ||
    utilization <= 0 ||
    currentCost <= 0
  ) {
    return null
  }

  const estimate = (currentCost * 100) / utilization
  return Number.isFinite(estimate) && estimate > 0 ? estimate : null
})

const openAIUsageRefreshKey = computed(() => buildOpenAIUsageRefreshKey(props.account))

const shouldAutoLoadUsageOnMount = computed(() => {
  return shouldFetchUsage.value
})

const shouldLazyLoadOnMobile = computed(() => {
  return shouldFetchUsage.value && !isDesktopViewport.value
})

// Antigravity quota types (用于 API 返回的数据)
interface AntigravityUsageResult {
  utilization: number
  resetTime: string | null
}

// ===== Antigravity quota from API (usageInfo.antigravity_quota) =====

// 检查是否有从 API 获取的配额数据
const hasAntigravityQuotaFromAPI = computed(() => {
  return usageInfo.value?.antigravity_quota && Object.keys(usageInfo.value.antigravity_quota).length > 0
})

// 从 API 配额数据中获取使用率（多模型取最高使用率）
const getAntigravityUsageFromAPI = (
  modelNames: string[]
): AntigravityUsageResult | null => {
  const quota = usageInfo.value?.antigravity_quota
  if (!quota) return null

  let maxUtilization = 0
  let earliestReset: string | null = null

  for (const model of modelNames) {
    const modelQuota = quota[model]
    if (!modelQuota) continue

    if (modelQuota.utilization > maxUtilization) {
      maxUtilization = modelQuota.utilization
    }
    if (modelQuota.reset_time) {
      if (!earliestReset || modelQuota.reset_time < earliestReset) {
        earliestReset = modelQuota.reset_time
      }
    }
  }

  // 如果没有找到任何匹配的模型
  if (maxUtilization === 0 && earliestReset === null) {
    const hasAnyData = modelNames.some((m) => quota[m])
    if (!hasAnyData) return null
  }

  return {
    utilization: maxUtilization,
    resetTime: earliestReset
  }
}

// Gemini 3 Pro from API
const antigravity3ProUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI(['gemini-3-pro-low', 'gemini-3-pro-high', 'gemini-3-pro-preview'])
)

// Gemini 3 Flash from API
const antigravity3FlashUsageFromAPI = computed(() => getAntigravityUsageFromAPI(['gemini-3-flash']))

// Gemini Image from API
const antigravity3ImageUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI(['gemini-2.5-flash-image', 'gemini-3.1-flash-image', 'gemini-3-pro-image'])
)

// Claude from API (all Claude model variants)
const antigravityClaudeUsageFromAPI = computed(() =>
  getAntigravityUsageFromAPI([
    'claude-fable-5-1',
    'claude-fable-5',
    'claude-sonnet-4-5', 'claude-opus-4-5-thinking',
    'claude-sonnet-4-6', 'claude-opus-4-6', 'claude-opus-4-6-thinking',
    'claude-opus-4-7', 'claude-opus-4-8',
  ])
)

const aiCreditsDisplay = computed(() => {
  const credits = usageInfo.value?.ai_credits
  if (!credits || credits.length === 0) return null
  const total = credits.reduce((sum, credit) => sum + (credit.amount ?? 0), 0)
  if (total <= 0) return null
  return total.toFixed(0)
})

// Antigravity 账户类型（从 load_code_assist 响应中提取）
const antigravityTier = computed(() => {
  const extra = props.account.extra as Record<string, unknown> | undefined
  if (!extra) return null

  const loadCodeAssist = extra.load_code_assist as Record<string, unknown> | undefined
  if (!loadCodeAssist) return null

  // 优先取 paidTier，否则取 currentTier
  const paidTier = loadCodeAssist.paidTier as Record<string, unknown> | undefined
  if (paidTier && typeof paidTier.id === 'string') {
    return paidTier.id
  }

  const currentTier = loadCodeAssist.currentTier as Record<string, unknown> | undefined
  if (currentTier && typeof currentTier.id === 'string') {
    return currentTier.id
  }

  return null
})

// Gemini 账户类型（从 credentials 中提取）
const geminiTier = computed(() => {
  if (props.account.platform !== 'gemini') return null
  const creds = props.account.credentials as GeminiCredentials | undefined
  return creds?.tier_id || null
})

const geminiOAuthType = computed(() => {
  if (props.account.platform !== 'gemini') return null
  const creds = props.account.credentials as GeminiCredentials | undefined
  return (creds?.oauth_type || '').trim() || null
})

// Gemini 是否为 Code Assist OAuth
const isGeminiCodeAssist = computed(() => {
  if (props.account.platform !== 'gemini') return false
  const creds = props.account.credentials as GeminiCredentials | undefined
  return creds?.oauth_type === 'code_assist' || (!creds?.oauth_type && !!creds?.project_id)
})

const geminiChannelShort = computed((): 'ai studio' | 'gcp' | 'google one' | 'client' | null => {
  if (props.account.platform !== 'gemini') return null

  // API Key accounts are AI Studio.
  if (props.account.type === 'apikey') return 'ai studio'

  if (geminiOAuthType.value === 'google_one') return 'google one'
  if (isGeminiCodeAssist.value) return 'gcp'
  if (geminiOAuthType.value === 'ai_studio') return 'client'

  // Fallback (unknown legacy data): treat as AI Studio.
  return 'ai studio'
})

const geminiUserLevel = computed((): string | null => {
  if (props.account.platform !== 'gemini') return null

  const tier = (geminiTier.value || '').toString().trim()
  const tierLower = tier.toLowerCase()
  const tierUpper = tier.toUpperCase()

  // Google One: free / pro / ultra
  if (geminiOAuthType.value === 'google_one') {
    if (tierLower === 'google_one_free') return 'free'
    if (tierLower === 'google_ai_pro') return 'pro'
    if (tierLower === 'google_ai_ultra') return 'ultra'

    // Backward compatibility (legacy tier markers)
    if (tierUpper === 'AI_PREMIUM' || tierUpper === 'GOOGLE_ONE_STANDARD') return 'pro'
    if (tierUpper === 'GOOGLE_ONE_UNLIMITED') return 'ultra'
    if (tierUpper === 'FREE' || tierUpper === 'GOOGLE_ONE_BASIC' || tierUpper === 'GOOGLE_ONE_UNKNOWN' || tierUpper === '') return 'free'

    return null
  }

  // GCP Code Assist: standard / enterprise
  if (isGeminiCodeAssist.value) {
    if (tierLower === 'gcp_enterprise') return 'enterprise'
    if (tierLower === 'gcp_standard') return 'standard'

    // Backward compatibility
    if (tierUpper.includes('ULTRA') || tierUpper.includes('ENTERPRISE')) return 'enterprise'
    return 'standard'
  }

  // AI Studio (API Key) and Client OAuth: free / paid
  if (props.account.type === 'apikey' || geminiOAuthType.value === 'ai_studio') {
    if (tierLower === 'aistudio_paid') return 'paid'
    if (tierLower === 'aistudio_free') return 'free'

    // Backward compatibility
    if (tierUpper.includes('PAID') || tierUpper.includes('PAYG') || tierUpper.includes('PAY')) return 'paid'
    if (tierUpper.includes('FREE')) return 'free'
    if (props.account.type === 'apikey') return 'free'
    return null
  }

  return null
})

// Gemini 认证类型（按要求：授权方式简称 + 用户等级）
const geminiAuthTypeLabel = computed(() => {
  if (props.account.platform !== 'gemini') return null
  if (!geminiChannelShort.value) return null
  return geminiUserLevel.value ? `${geminiChannelShort.value} ${geminiUserLevel.value}` : geminiChannelShort.value
})

// Gemini 账户类型徽章样式（统一样式）
const geminiTierClass = computed(() => {
  // Use channel+level to choose a stable color without depending on raw tier_id variants.
  const channel = geminiChannelShort.value
  const level = geminiUserLevel.value

  if (channel === 'client' || channel === 'ai studio') {
    return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
  }

  if (channel === 'google one') {
    if (level === 'ultra') return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    if (level === 'pro') return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
    return 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300'
  }

  if (channel === 'gcp') {
    if (level === 'enterprise') return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
  }

  return ''
})

// Gemini 配额政策信息
const geminiQuotaPolicyChannel = computed(() => {
  if (geminiOAuthType.value === 'google_one') {
    return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.channel')
  }
  if (isGeminiCodeAssist.value) {
    return t('admin.accounts.gemini.quotaPolicy.rows.gcp.channel')
  }
  return t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.channel')
})

const geminiQuotaPolicyLimits = computed(() => {
  const tierLower = (geminiTier.value || '').toString().trim().toLowerCase()

  if (geminiOAuthType.value === 'google_one') {
    if (tierLower === 'google_ai_ultra' || geminiUserLevel.value === 'ultra') {
      return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsUltra')
    }
    if (tierLower === 'google_ai_pro' || geminiUserLevel.value === 'pro') {
      return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsPro')
    }
    return t('admin.accounts.gemini.quotaPolicy.rows.googleOne.limitsFree')
  }

  if (isGeminiCodeAssist.value) {
    if (tierLower === 'gcp_enterprise' || geminiUserLevel.value === 'enterprise') {
      return t('admin.accounts.gemini.quotaPolicy.rows.gcp.limitsEnterprise')
    }
    return t('admin.accounts.gemini.quotaPolicy.rows.gcp.limitsStandard')
  }

  // AI Studio (API Key / custom OAuth)
  if (tierLower === 'aistudio_paid' || geminiUserLevel.value === 'paid') {
    return t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.limitsPaid')
  }
  return t('admin.accounts.gemini.quotaPolicy.rows.aiStudio.limitsFree')
})

const geminiQuotaPolicyDocsUrl = computed(() => {
  if (geminiOAuthType.value === 'google_one' || isGeminiCodeAssist.value) {
    return 'https://developers.google.com/gemini-code-assist/resources/quotas'
  }
  return 'https://ai.google.dev/pricing'
})

const geminiUsesSharedDaily = computed(() => {
  if (props.account.platform !== 'gemini') return false
  // Per requirement: Google One & GCP are shared RPD pools (no per-model breakdown).
  return (
    !!usageInfo.value?.gemini_shared_daily ||
    !!usageInfo.value?.gemini_shared_minute ||
    geminiOAuthType.value === 'google_one' ||
    isGeminiCodeAssist.value
  )
})

const geminiUsageBars = computed(() => {
  if (props.account.platform !== 'gemini') return []
  if (!usageInfo.value) return []

  const bars: Array<{
    key: string
    label: string
    utilization: number
    resetsAt: string | null
    windowStats?: WindowStats | null
    color: 'indigo' | 'emerald'
  }> = []

  if (geminiUsesSharedDaily.value) {
    const sharedDaily = usageInfo.value.gemini_shared_daily
    if (sharedDaily) {
      bars.push({
        key: 'shared_daily',
        label: '1d',
        utilization: sharedDaily.utilization,
        resetsAt: sharedDaily.resets_at,
        windowStats: sharedDaily.window_stats,
        color: 'indigo'
      })
    }
    return bars
  }

  const pro = usageInfo.value.gemini_pro_daily
  if (pro) {
    bars.push({
      key: 'pro_daily',
      label: 'pro',
      utilization: pro.utilization,
      resetsAt: pro.resets_at,
      windowStats: pro.window_stats,
      color: 'indigo'
      })
  }

  const flash = usageInfo.value.gemini_flash_daily
  if (flash) {
    bars.push({
      key: 'flash_daily',
      label: 'flash',
      utilization: flash.utilization,
      resetsAt: flash.resets_at,
      windowStats: flash.window_stats,
      color: 'emerald'
    })
  }

  return bars
})

interface GrokQuotaBarInfo {
  utilization: number
  resetsAt: string | null
  windowStats?: WindowStats | null
}

const grokBilling = computed(() => usageInfo.value?.grok_billing || null)
const grokLocalUsage7d = computed(() => (
  usageInfo.value?.grok_local_usage_7d || usageInfo.value?.seven_day?.window_stats || null
))
const grokLocalUsageMonthly = computed(() => (
  usageInfo.value?.grok_local_usage_monthly || usageInfo.value?.thirty_day?.window_stats || null
))
const grokWeeklyBillingBar = computed((): GrokQuotaBarInfo | null => {
  const billing = grokBilling.value
  if (billing?.period_type?.toLowerCase() !== 'weekly' || billing.usage_percent == null) {
    return null
  }
  return {
    utilization: Math.min(100, Math.max(0, billing.usage_percent)),
    resetsAt: billing.period_end || null,
    windowStats: grokLocalUsage7d.value
  }
})
// Monthly used/limit % from billing probe (used_percent or derived from cents).
const grokMonthlyBillingBar = computed((): GrokQuotaBarInfo | null => {
  const billing = grokBilling.value
  if (!billing) return null
  let utilization: number | null = null
  if (billing.used_percent != null && Number.isFinite(billing.used_percent)) {
    utilization = billing.used_percent
  } else if (
    billing.monthly_limit_cents != null &&
    billing.monthly_limit_cents > 0 &&
    billing.used_cents != null
  ) {
    utilization = (billing.used_cents / billing.monthly_limit_cents) * 100
  }
  if (utilization == null) return null
  // Avoid duplicating the weekly bar when period_type is weekly-only without monthly.
  if (billing.period_type?.toLowerCase() === 'weekly' && billing.monthly_limit_cents == null) {
    return null
  }
  return {
    utilization: Math.min(100, Math.max(0, utilization)),
    resetsAt: billing.billing_period_end || billing.period_end || null,
    windowStats: grokLocalUsageMonthly.value
  }
})
const formatGrokMoney = (value?: number | null) => {
  if (value == null || Number.isNaN(value)) return '0'
  if (value >= 1000) return formatCompactNumber(value)
  if (value >= 100) return value.toFixed(0)
  if (value >= 10) return value.toFixed(1)
  return value.toFixed(2)
}
// Prepaid chip only when there is a positive prepaid balance.
// Used/limit only when monthly limit is a positive number (0 means unlimited / unset).
const grokPrepaidMoneyLine = computed(() => {
  const billing = grokBilling.value
  if (!billing) return null
  const prepaid = billing.prepaid_balance
  const showPrepaid = prepaid != null && Number.isFinite(prepaid) && prepaid > 0
  const limitRaw =
    billing.monthly_limit != null
      ? billing.monthly_limit
      : billing.monthly_limit_cents != null
        ? billing.monthly_limit_cents / 100
        : null
  const showUsedLimit = limitRaw != null && Number.isFinite(limitRaw) && limitRaw > 0
  if (!showPrepaid && !showUsedLimit) return null
  const used =
    billing.monthly_used != null
      ? billing.monthly_used
      : billing.used_cents != null
        ? billing.used_cents / 100
        : 0
  return {
    showPrepaid,
    showUsedLimit,
    prepaid: showPrepaid ? formatGrokMoney(prepaid) : null,
    used: showUsedLimit ? formatGrokMoney(used) : null,
    limit: showUsedLimit ? formatGrokMoney(limitRaw) : null
  }
})
const grokPlanLabelIsFree = (value: string) => value.includes('free') || value.includes('basic')
const grokPlanLabelIsPaid = (value: string) => {
  return value !== '' && !grokPlanLabelIsFree(value) && !value.includes('unknown')
}
const grokIsFree = computed(() => {
  if (props.account.platform !== 'grok' || props.account.type !== 'oauth') return false
  const billing = grokBilling.value
  const plan = (billing?.plan || '').trim().toLowerCase()
  const tier = (usageInfo.value?.subscription_tier || '').trim().toLowerCase()
  const entitlement = (usageInfo.value?.grok_entitlement_status || '').toLowerCase()
  if (grokPlanLabelIsFree(tier)) return true
  if (grokPlanLabelIsPaid(tier)) return false
  if (
    billing?.usage_percent != null ||
    billing?.used_percent != null ||
    (billing?.monthly_limit_cents != null && billing.monthly_limit_cents > 0)
  ) return false
  if (grokPlanLabelIsPaid(plan)) return false
  if (
    grokPlanLabelIsFree(plan) ||
    grokPlanLabelIsFree(entitlement)
  ) return true
  return billing != null
})
const grokFreeQuotaUsage = computed(() => usageInfo.value?.grok_local_usage_24h || null)
const grokFreeTokenBar = computed(() => {
  if (!grokIsFree.value || !grokFreeQuotaUsage.value) return null
  const limit = usageInfo.value?.grok_free_token_limit
  if (typeof limit !== 'number' || limit <= 0) return null
  const used = Math.max(0, grokFreeQuotaUsage.value.tokens || 0)
  return { utilization: Math.min(100, (used / limit) * 100), limit }
})
const grokQuotaUnknown = computed(() => {
  if (props.account.platform !== 'grok') return false
  if (grokIsFree.value) {
    return !grokFreeTokenBar.value
  }
  if (grokWeeklyBillingBar.value || grokMonthlyBillingBar.value || grokPrepaidMoneyLine.value) {
    return false
  }
  return usageInfo.value?.grok_quota_snapshot_state !== 'observed'
})
const grokQuotaUnknownLabel = computed(() => {
  return usageInfo.value?.grok_quota_snapshot_state === 'no_headers'
    ? t('admin.accounts.usageWindow.grokNoHeaders')
    : t('admin.accounts.usageWindow.grokUnknown')
})
const grokRetryAfterLabel = computed(() => {
  const seconds = usageInfo.value?.grok_retry_after_seconds
  if (seconds == null || seconds <= 0) return null
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.ceil(seconds / 60)
  return `${minutes}m`
})

// 账户类型显示标签
const antigravityTierLabel = computed(() => {
  switch (antigravityTier.value) {
    case 'free-tier':
      return t('admin.accounts.tier.free')
    case 'g1-pro-tier':
      return t('admin.accounts.tier.pro')
    case 'g1-ultra-tier':
      return t('admin.accounts.tier.ultra')
    default:
      return null
  }
})

// 账户类型徽章样式
const antigravityTierClass = computed(() => {
  switch (antigravityTier.value) {
    case 'free-tier':
      return 'bg-gray-100 text-gray-600 dark:bg-gray-700 dark:text-gray-300'
    case 'g1-pro-tier':
      return 'bg-blue-100 text-blue-600 dark:bg-blue-900/40 dark:text-blue-300'
    case 'g1-ultra-tier':
      return 'bg-purple-100 text-purple-600 dark:bg-purple-900/40 dark:text-purple-300'
    default:
      return ''
  }
})

// 检测账户是否有不合格状态（ineligibleTiers）
const hasIneligibleTiers = computed(() => {
  const extra = props.account.extra as Record<string, unknown> | undefined
  if (!extra) return false

  const loadCodeAssist = extra.load_code_assist as Record<string, unknown> | undefined
  if (!loadCodeAssist) return false

  const ineligibleTiers = loadCodeAssist.ineligibleTiers as unknown[] | undefined
  return Array.isArray(ineligibleTiers) && ineligibleTiers.length > 0
})

// Antigravity 403 forbidden 状态
const isForbidden = computed(() => !!usageInfo.value?.is_forbidden)
const forbiddenType = computed(() => usageInfo.value?.forbidden_type || 'forbidden')
const validationURL = computed(() => usageInfo.value?.validation_url || '')

// 需要重新授权（401）
const needsReauth = computed(() => !!usageInfo.value?.needs_reauth)

// 降级错误标签（rate_limited / network_error）
const usageErrorLabel = computed(() => {
  const code = usageInfo.value?.error_code
  if (code === 'rate_limited') return t('admin.accounts.rateLimited')
  return t('admin.accounts.usageError')
})

const forbiddenLabel = computed(() => {
  switch (forbiddenType.value) {
    case 'validation':
      return t('admin.accounts.forbiddenValidation')
    case 'violation':
      return t('admin.accounts.forbiddenViolation')
    default:
      return t('admin.accounts.forbidden')
  }
})

const forbiddenBadgeClass = computed(() => {
  if (forbiddenType.value === 'validation') {
    return 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-300'
  }
  return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
})

const linkCopied = ref(false)
const copyValidationURL = async () => {
  if (!validationURL.value) return
  try {
    await navigator.clipboard.writeText(validationURL.value)
    linkCopied.value = true
    setTimeout(() => { linkCopied.value = false }, 2000)
  } catch {
    // fallback: ignore
  }
}

const isAnthropicOAuthOrSetupToken = computed(() => {
  return props.account.platform === 'anthropic' && (props.account.type === 'oauth' || props.account.type === 'setup-token')
})

const requestParentBatchUsage = (options?: { force?: boolean }) => {
  if (!isBatchManaged.value || !shouldFetchUsage.value) return
  props.requestBatchedUsage?.(props.account, options)
}

const syncManagedUsageState = () => {
  if (!isBatchManaged.value) return
  usageInfo.value = props.batchedUsage ?? null
  error.value = props.batchedUsageError ?? null
  loading.value = props.batchedUsageLoading === true
}

const loadUsage = async (options?: { source?: 'passive' | 'active'; bypassCache?: boolean }) => {
  if (!shouldFetchUsage.value) return
  if (isBatchManaged.value) {
    requestParentBatchUsage({ force: options?.bypassCache === true })
    return
  }

  // Check cache
  if (!options?.bypassCache) {
    const cached = _usageCache.get(props.account.id)
    if (cached && Date.now() - cached.ts < USAGE_CACHE_TTL) {
      usageInfo.value = cached.data
      loading.value = false
      return
    }
  }

  loading.value = true
  error.value = null

  try {
		const fetchFn = () => options?.source
			? adminAPI.accounts.getUsage(props.account.id, options.source, options.bypassCache === true)
			: adminAPI.accounts.getUsage(props.account.id)
    const result = await enqueueUsageRequest(props.account, fetchFn)
    if (!unmounted.value) {
      usageInfo.value = result
      _usageCache.set(props.account.id, { data: result, ts: Date.now() })
    }
  } catch (e: any) {
    if (!unmounted.value) {
      error.value = t('common.error')
      console.error('Failed to load usage:', e)
    }
  } finally {
    if (!unmounted.value) loading.value = false
  }
}

const flushPendingAutoLoad = () => {
  if (!pendingAutoLoad.value) return
  const source = pendingAutoLoadSource.value
  pendingAutoLoad.value = false
  pendingAutoLoadSource.value = undefined
  loadUsage({ source }).catch((e) => {
    console.error('Failed to load deferred usage:', e)
  })
}

const requestAutoLoad = (source?: 'passive' | 'active') => {
  if (!shouldFetchUsage.value) return
  if (shouldLazyLoadOnMobile.value && !hasEnteredViewport.value) {
    pendingAutoLoad.value = true
    pendingAutoLoadSource.value = source
    return
  }
  loadUsage({ source }).catch((e) => {
    console.error('Failed to auto load usage:', e)
  })
}

const detachVisibilityObserver = () => {
  visibilityObserver?.disconnect()
  visibilityObserver = null
}

const attachVisibilityObserver = () => {
  detachVisibilityObserver()
  if (!shouldLazyLoadOnMobile.value || hasEnteredViewport.value) return
  if (typeof window === 'undefined' || typeof IntersectionObserver === 'undefined') {
    hasEnteredViewport.value = true
    flushPendingAutoLoad()
    return
  }
  if (!rootRef.value) return

  visibilityObserver = new IntersectionObserver((entries) => {
    if (!entries.some((entry) => entry.isIntersecting)) return
    hasEnteredViewport.value = true
    detachVisibilityObserver()
    flushPendingAutoLoad()
  }, {
    root: null,
    rootMargin: '200px 0px',
    threshold: 0.01
  })
  visibilityObserver.observe(rootRef.value)
}

const loadActiveUsage = async () => {
  activeQueryLoading.value = true
  try {
    usageInfo.value = await adminAPI.accounts.getUsage(props.account.id, 'active', true)
  } catch (e: any) {
    console.error('Failed to load active usage:', e)
  } finally {
    activeQueryLoading.value = false
  }
}

// The probe persists upstream quota state; refresh this cell so its compact
// bars and entitlement status reflect the newly observed snapshot.
const handleGrokProbed = async () => {
  await loadUsage({ source: 'active', bypassCache: true })
}

// ===== API Key quota progress bars =====

interface QuotaBarInfo {
  utilization: number
  resetsAt: string | null
}

const makeQuotaBar = (
  used: number,
  limit: number,
  startKey?: string
): QuotaBarInfo => {
  const utilization = limit > 0 ? (used / limit) * 100 : 0
  let resetsAt: string | null = null
  if (startKey) {
    const extra = props.account.extra as Record<string, unknown> | undefined
    const isDaily = startKey.includes('daily')
    const mode = isDaily
      ? (extra?.quota_daily_reset_mode as string) || 'rolling'
      : (extra?.quota_weekly_reset_mode as string) || 'rolling'

    if (mode === 'fixed') {
      // Use pre-computed next reset time for fixed mode
      const resetAtKey = isDaily ? 'quota_daily_reset_at' : 'quota_weekly_reset_at'
      resetsAt = (extra?.[resetAtKey] as string) || null
    } else {
      // Rolling mode: compute from start + period
      const startStr = extra?.[startKey] as string | undefined
      if (startStr) {
        const startDate = new Date(startStr)
        const periodMs = isDaily ? 24 * 60 * 60 * 1000 : 7 * 24 * 60 * 60 * 1000
        resetsAt = new Date(startDate.getTime() + periodMs).toISOString()
      }
    }
  }
  return { utilization, resetsAt }
}

const hasApiKeyQuota = computed(() => {
  if (props.account.type !== 'apikey' && props.account.type !== 'bedrock') return false
  return (
    (props.account.quota_daily_limit ?? 0) > 0 ||
    (props.account.quota_weekly_limit ?? 0) > 0 ||
    (props.account.quota_limit ?? 0) > 0
  )
})

const quotaDailyBar = computed((): QuotaBarInfo | null => {
  const limit = props.account.quota_daily_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.account.quota_daily_used ?? 0, limit, 'quota_daily_start')
})

const quotaWeeklyBar = computed((): QuotaBarInfo | null => {
  const limit = props.account.quota_weekly_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.account.quota_weekly_used ?? 0, limit, 'quota_weekly_start')
})

const quotaTotalBar = computed((): QuotaBarInfo | null => {
  const limit = props.account.quota_limit ?? 0
  if (limit <= 0) return null
  return makeQuotaBar(props.account.quota_used ?? 0, limit)
})

const handleQuotaResetAccountUpdated = (account: Account) => {
  emit('account-updated', account)
}

const handleOllamaCloudUsageUpdated = (state: NonNullable<Account['ollama_cloud_usage']>) => {
  emit('account-updated', { ...props.account, ollama_cloud_usage: state })
}

// ===== Key account today stats formatters =====

const formatKeyRequests = computed(() => {
  if (!props.todayStats) return ''
  return formatCompactNumber(props.todayStats.requests, { allowBillions: false })
})

const formatKeyTokens = computed(() => {
  if (!props.todayStats) return ''
  return formatCompactNumber(props.todayStats.tokens)
})

const formatKeyCost = computed(() => {
  if (!props.todayStats) return '0.00'
  return props.todayStats.cost.toFixed(2)
})

const formatKeyUserCost = computed(() => {
  if (!props.todayStats || props.todayStats.user_cost == null) return '0.00'
  return props.todayStats.user_cost.toFixed(2)
})

onMounted(() => {
  if (typeof window !== 'undefined') {
    desktopViewportMediaQuery = window.matchMedia(desktopViewportQuery)
    isDesktopViewport.value = desktopViewportMediaQuery.matches
    desktopViewportListener = (event: MediaQueryListEvent) => {
      isDesktopViewport.value = event.matches
    }
    if (typeof desktopViewportMediaQuery.addEventListener === 'function') {
      desktopViewportMediaQuery.addEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.addListener(desktopViewportListener)
    }
  }

  if (isBatchManaged.value) {
    syncManagedUsageState()
    requestParentBatchUsage()
    return
  }

  if (!shouldAutoLoadUsageOnMount.value) return
  const source = isAnthropicOAuthOrSetupToken.value ? 'passive' : undefined
  requestAutoLoad(source)
})

watch(
  () => [props.batchedUsage, props.batchedUsageError, props.batchedUsageLoading, isBatchManaged.value] as const,
  () => {
    syncManagedUsageState()
  },
  { immediate: true, deep: true }
)

watch(isBatchManaged, (managed, wasManaged) => {
  if (managed && !wasManaged) {
    syncManagedUsageState()
    requestParentBatchUsage()
  }
})

watch(
  () => [props.account.id, props.account.platform, props.account.type, isBatchManaged.value] as const,
  ([accountID, platform, accountType, managed], [previousAccountID, previousPlatform, previousAccountType]) => {
    if (
      accountID === previousAccountID &&
      platform === previousPlatform &&
      accountType === previousAccountType
    ) {
      return
    }
    if (!managed || !shouldFetchUsage.value) return
    syncManagedUsageState()
    requestParentBatchUsage()
  },
  { flush: 'post' }
)

watch(openAIUsageRefreshKey, (nextKey, prevKey) => {
  if (!prevKey || nextKey === prevKey) return
  if (props.account.platform !== 'openai' || props.account.type !== 'oauth') return

  if (isBatchManaged.value) {
    requestParentBatchUsage({ force: true })
    return
  }

  _usageCache.delete(props.account.id)
  requestAutoLoad()
})

watch(
  () => props.manualRefreshToken,
  (nextToken, prevToken) => {
    if (nextToken === prevToken) return
    if (!shouldFetchUsage.value) return

    if (isBatchManaged.value) {
      requestParentBatchUsage({ force: true })
      return
    }

    const source = isAnthropicOAuthOrSetupToken.value ? 'passive' : undefined
    _usageCache.delete(props.account.id)
    loadUsage({ source, bypassCache: true }).catch((e) => {
      console.error('Failed to refresh usage after manual refresh:', e)
    })
  }
)

watch(
  [rootRef, shouldLazyLoadOnMobile],
  () => {
    if (shouldLazyLoadOnMobile.value) {
      attachVisibilityObserver()
      return
    }
    detachVisibilityObserver()
  },
  { immediate: true, flush: 'post' }
)

watch(isDesktopViewport, (isDesktop) => {
  if (isDesktop) {
    detachVisibilityObserver()
    hasEnteredViewport.value = true
    flushPendingAutoLoad()
    return
  }
  hasEnteredViewport.value = false
  attachVisibilityObserver()
})

onUnmounted(() => {
  detachVisibilityObserver()
  if (desktopViewportMediaQuery && desktopViewportListener) {
    if (typeof desktopViewportMediaQuery.removeEventListener === 'function') {
      desktopViewportMediaQuery.removeEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.removeListener(desktopViewportListener)
    }
  }
  desktopViewportListener = null
  desktopViewportMediaQuery = null
})
</script>
