import type zh from './zh'

const en: typeof zh = {
  shell: {
    dockLabel: 'Main navigation',
    topbarLabel: 'Top bar',
    pageFallback: 'Console',
    dock: {
      overview: 'Overview',
      keys: 'Keys',
      usage: 'Usage',
      models: 'Models',
      wallet: 'Wallet'
    },
    links: {
      channelStatus: 'Channel status',
      pricing: 'Model pricing',
      purchase: 'Top up / Subscribe',
      purchaseRecharge: 'Top up',
      purchaseSubscribe: 'Subscribe',
      subscriptions: 'My subscriptions',
      orders: 'My orders',
      redeem: 'Redeem code',
      affiliate: 'Referral rewards',
      batchImage: 'Batch image guide',
      docs: 'Integration docs',
      profile: 'Profile & security'
    },
    topbar: {
      docs: 'Docs',
      more: 'More',
      balance: 'Available balance',
      balanceTitle: 'Open wallet',
      frozen: 'Frozen {amount}'
    },
    avatar: {
      menu: 'Account menu',
      language: 'Language',
      theme: 'Theme',
      themeLight: 'Light',
      themeDark: 'Dark',
      contact: 'Support',
      logout: 'Sign out'
    }
  },
  pages: {
    overview: {
      chartTitle: 'Usage analytics',
      range: 'Time range',
      granularity: 'Granularity',
      day: 'Daily',
      hour: 'Hourly',
      refresh: 'Refresh',
      dimension: 'Dimension',
      dimensions: {
        trend: 'Trend',
        model: 'Models',
        group: 'Groups',
        endpoint: 'Endpoints'
      },
      endpointTitle: 'Endpoint distribution',
      quotaTitle: 'Subscription quota',
      quotaManage: 'Manage subscriptions',
      quotaDaily: 'Today',
      quotaWeekly: 'This week',
      quotaMonthly: 'This month',
      quotaUnlimited: 'Unlimited'
    },
    wallet: {
      available: 'Available balance',
      frozen: 'Frozen',
      total: 'Total balance',
      subscriptions: 'My subscriptions',
      sectionsLabel: 'Wallet features',
      empty: 'No wallet features are available for this account.'
    },
    models: {
      tabsLabel: 'Models page',
      tabs: {
        status: 'Channel status',
        pricing: 'Model pricing'
      },
      empty: 'Neither channel status nor model pricing is open.',
      status: {
        header: 'Last 24h status',
        none: 'No data',
        other: 'Not tracked separately',
        loading: 'Loading',
        failed: 'Unavailable',
        open: 'Show the last 24h trend and errors for {model}',
        title: '{model} · {group} · last 24h',
        trend: 'Trend',
        errors: 'Error categories',
        noErrors: 'No errors in the last 24 hours.',
        loadFailed: 'Could not load the status. Try again later.',
        health: {
          healthy: 'Healthy',
          warning: 'Degraded',
          critical: 'Failing',
          unknown: 'Too few samples'
        }
      }
    }
  },
  rhineHome: {
    caption: 'Multi-model access / developer platform',
    greetingLead: 'One gateway for many AI services.',
    greetingTail: 'Manage keys and see every call.',
    panelLabel: 'Homepage panel',
    nav: {
      label: 'Quick links',
      models: 'Pricing',
      modelsTitle: 'View model pricing',
      docs: 'Docs',
      docsTitle: 'Read the integration docs',
      start: 'Get started',
      startTitle: 'Sign in to get started',
      console: 'Console',
      consoleTitle: 'Open the user console'
    },
    info: {
      label: 'Platform overview',
      title: 'Connect models to apps',
      copy: 'Access AI services through {site} and manage keys, usage and costs in one place. Pick a model in the model plaza and follow the docs to connect your app.'
    },
    identity: {
      label: 'Platform identity',
      audience: 'For developers'
    },
    replay: 'Replay intro',
    pricing: {
      label: 'Popular model pricing',
      labelUnavailable: 'Popular model pricing (no data)',
      columnModel: 'Model / platform',
      columnInput: 'Input',
      columnOutput: 'Output',
      priceHint: 'Lowest paid price across standard public groups, group rate applied',
      loading: 'Loading model pricing…',
      unavailable: 'Model pricing is unavailable. Please refresh later.',
      empty: 'No model pricing yet',
      unit: 'USD / 1M tokens',
      perImage: '/ image',
      perRequest: '/ request',
      refresh: 'Refresh model pricing'
    }
  },
  common: {
    copy: 'Copy',
    copied: 'Copied',
    cancel: 'Cancel',
    themeToLight: 'Switch to light mode',
    themeToDark: 'Switch to dark mode'
  },
  navbar: {
    tagline: 'All-in-one AI API Router',
    features: 'Features',
    playground: 'Demo',
    clients: 'Integrations',
    models: 'Models',
    modelPlaza: 'Model Plaza',
    keyUsage: 'Key Lookup',
    docs: 'API & integration docs',
    enterConsole: 'Open Console',
    login: 'Log in',
    getStarted: 'Get Started'
  },
  footer: {
    tagline: 'All-in-one High Availability AI Gateway',
    features: 'Features',
    playground: 'Demo',
    clients: 'Client Integrations',
    modelPlaza: 'Model Plaza',
    keyUsage: 'Key Quota Lookup',
    docs: 'Documentation',
    rights: 'All rights reserved.',
    poweredBy: 'Built on the open-source Sub2API gateway · Developer-first experience'
  },
  hero: {
    badge: 'Protocol-adaptive routing · Account pool failover · SSE streaming',
    headlineLead: 'One relay, ',
    headlineHighlight: 'connecting frontier AI models',
    subtitleLead: 'A single API key with native compatibility for',
    subtitleVendors: 'Claude, OpenAI, Gemini and DeepSeek',
    subtitleTail: 'model families. Multi-account concurrent load balancing, session affinity and automatic circuit-breaking retries provide a stable foundation for production AI applications.',
    enterConsole: 'Open User Console',
    getKey: 'Get a Free API Key',
    viewDemo: 'View Simulated Demo',
    viewPricing: 'View Full Model Pricing',
    exampleModels: 'Example models (see Model Plaza for availability):',
    highlights: {
      pool: { title: 'Account Pool Scheduling', detail: 'Concurrency and failover across upstream accounts' },
      sse: { title: 'SSE Stream Relay', detail: 'Streaming responses passed through chunk by chunk' },
      protocols: { title: 'Multi-protocol Access', detail: 'OpenAI / Anthropic compatible APIs' },
      billing: { title: 'Usage-based Billing', detail: 'Charges recorded per usage, with cache pricing' }
    }
  },
  playground: {
    badge: 'Simulated demo · No real requests sent',
    title: 'Gateway Request Flow Demo',
    description: 'The content below is preset text replayed locally in your browser to illustrate the order of authentication, routing and stream relay. No model is called, no cost is incurred, and it does not reflect real latency or availability.',
    exampleModel: 'Example model (demo only):',
    protocol: 'Protocol: {protocol}',
    sse: 'SSE streaming',
    presets: 'Preset prompts',
    presetsHint: 'Click to fill in',
    customPrompt: 'Custom prompt',
    customPromptPlaceholder: 'The simulated demo only replays preset answers and never sends this text',
    playing: 'Replaying demo...',
    play: 'Play Simulated Demo',
    clear: 'Clear',
    terminalTitle: 'Local simulated replay',
    terminalBadge: 'Simulated demo · Not a real response',
    stepAuth: '① Auth & quota check',
    stepRoute: '② Account pool routing',
    stepStream: '③ Stream relay',
    emptyHint: 'Click "Play Simulated Demo" on the left to watch a streaming replay of the preset text',
    footerModel: 'Example model: {model}',
    footerNoBilling: 'Not billed · No upstream calls',
    statusLabel: 'Status: {status}',
    statusPlaying: 'Replaying...',
    statusDone: 'Replay finished',
    statusIdle: 'Idle',
    presetList: {
      architecture: {
        title: 'AI architecture & high-concurrency gateway design',
        text: 'As a senior architect, analyze the key considerations for an AI API gateway in managing concurrent upstream accounts, session affinity and automatic circuit breaking with traffic failover.'
      },
      retry: {
        title: 'Exponential backoff retry implementation',
        text: 'Implement an exponential backoff retry function with jitter in TypeScript, and explain its advantages against upstream 429 rate limits.'
      },
      sse: {
        title: 'SSE streaming & buffering',
        text: 'How would you design a high-throughput Server-Sent Events (SSE) proxy pipeline? Outline the core logic for client-side frame splitting and backpressure control.'
      }
    }
  },
  guide: {
    badge: 'Ecosystem Integration',
    title: 'Connect any AI client or dev environment in a minute',
    description: 'No business logic changes required. Just replace the Base URL and API key in your development tools.',
    currentEndpoint: 'Current endpoint: {host}',
    tipLabel: 'Tip: ',
    exampleModelNotice: 'Model names in the code are examples only. Replace them with models actually available in the Model Plaza.',
    claudeCode: {
      title: 'Anthropic Claude Code CLI',
      description: 'Set terminal environment variables and use the official claude tool through the gateway',
      stepExport: '1. Export the Anthropic Base URL (native protocol supported)',
      stepLaunch: '2. Launch Claude Code',
      tip: 'Claude Code uses the Anthropic Messages API; features such as Prompt Caching depend on the upstream accounts of the selected group.'
    },
    cursor: {
      title: 'Cursor AI code editor',
      description: 'Configure under Cursor Settings > Models > OpenAI API Key',
      settingsFile: 'Cursor Settings (UI)',
      exampleModels: 'Example model names (replace with models actually available):',
      tip: 'When setting Override Base URL in Cursor, make sure it includes the /v1 suffix.'
    },
    python: {
      title: 'Python (official OpenAI SDK)',
      description: 'Use the standard openai pip package and set base_url on initialization',
      prompt: 'Briefly summarize the core advantages of an AI API gateway',
      tip: 'Use OpenAI Python SDK >= 1.0.0; available models depend on the Model Plaza and the selected group.'
    },
    nodejs: {
      title: 'Node.js / TypeScript',
      description: 'Based on the official openai npm client package',
      tip: 'Frameworks built on the OpenAI-compatible API, such as LangChain and Vercel AI SDK, usually only need a new base URL and API key.'
    },
    cherry: {
      title: 'Cherry Studio / NextChat / Chatbox clients',
      description: 'Configure in the client\'s custom model settings',
      settingsFile: 'GUI Settings',
      providerType: 'Provider type',
      providerValue: 'OpenAI (custom)',
      apiHost: 'API host',
      apiKey: 'API key',
      modelList: 'Models (example)',
      tip: 'When entering the endpoint in NextChat, the main domain is usually enough; NextChat appends /v1/chat/completions automatically.'
    },
    curl: {
      title: 'Standard cURL command line',
      description: 'Test the gateway from any platform with a plain command line',
      tip: 'Use the -N flag to see the streamed output token by token in a bash terminal.'
    }
  },
  pricing: {
    badge: 'Official reference price · Per 1M tokens',
    title: 'Supported Models & Official Reference Prices',
    description: 'Prices below are upstream official reference prices, excluding group multipliers, time-based multipliers and exclusive discounts. Actual charges follow the Model Plaza and usage details.',
    openPlaza: 'Open Full Model Plaza',
    allPlatforms: 'All platforms',
    searchPlaceholder: 'Search model name (e.g. sonnet, 4o)...',
    loading: 'Loading model list…',
    locked: 'Log in to view the Model Plaza.',
    goLogin: 'Log in',
    emptyLead: 'No model reference prices to display. Please',
    emptyLink: 'visit the Model Plaza',
    emptyTail: '.',
    inputPrice: 'Official input (1M)',
    outputPrice: 'Official output (1M)',
    cacheRead: '⚡ Official cache read price available'
  },
  features: {
    badge: 'Enterprise Gateway Architecture',
    title: 'An AI gateway designed for high concurrency and production',
    description: 'Addresses core pain points such as single-account rate limits, exchange-rate losses, frequent upstream outages and opaque billing.',
    list: {
      pool: {
        title: 'Smart multi-account pool & concurrency scheduling',
        description: 'Aggregates subscription accounts across upstream platforms and controls concurrency with Redis slots, lowering the chance of any single account hitting 429 rate limits.'
      },
      protocols: {
        title: 'Native dual-protocol compatibility',
        description: 'Natively supports both OpenAI Chat Completions and the Anthropic Messages API, passing through thinking content and Prompt Cache related fields.'
      },
      failover: {
        title: 'Session affinity & automatic failover',
        description: 'Ongoing conversations prefer stable slots, and can switch to other available accounts automatically when the upstream fails.'
      },
      billing: {
        title: 'Token-level usage billing',
        description: 'Counts input, output and cache tokens from the usage returned upstream, with spending limits, time-based multipliers and low-balance email notifications.'
      },
      keys: {
        title: 'Fine-grained API key governance',
        description: 'Create separate keys for teams or tools, with IP allowlists, allowed model lists and daily/weekly request rate caps.'
      },
      outbound: {
        title: 'Outbound connection reuse',
        description: 'Reuses the outbound proxy connection pool to reduce extra latency from repeated TLS handshakes.'
      }
    }
  },
  console: {
    workspace: 'Developer Console v2',
    switchToClassic: '(Switch to classic)',
    switchToClassicTitle: 'Open the classic dashboard',
    copyBaseUrl: 'Click to copy Base URL',
    recharge: 'Recharge',
    newKey: 'New Key',
    profile: 'Profile & settings'
  },
  dashboard: {
    quickActions: 'Quick Actions',
    createKey: 'Create API Key',
    createKeyDetail: 'Generate a new client auth key in seconds',
    purchase: 'Recharge & Plans',
    purchaseDetail: 'Tiered bonuses and multiple payment methods',
    modelPlaza: 'Model Plaza & Pricing',
    modelPlazaDetail: 'View model prices and multipliers across platforms',
    redeem: 'Redeem Code',
    redeemDetail: 'Enter a redeem code to top up your balance'
  },
  onboarding: {
    badge: '🚀 Developer Quick Start',
    title: 'Three steps to start calling AI models',
    description: 'Once set up, you can start streaming requests from Cursor, Claude Code, Python or any development tool in seconds.',
    expand: 'Show guide',
    collapse: 'Hide',
    balanceReady: 'Ready ({amount})',
    balancePending: 'Needs recharge',
    balanceTitle: 'Check your available balance',
    balanceDetail: 'The gateway bills by actual usage. Top up online or with a redeem code.',
    goRecharge: 'Recharge',
    redeemCode: 'Redeem code',
    keysReady: 'Created ({count} total)',
    keysPending: 'Not created',
    keysTitle: 'Create an API key',
    keysDetail: 'Generate a unique auth key for your app, with IP allowlist and model permission settings.',
    quickCreate: 'Quick create key',
    manageKeys: 'Manage keys',
    toolsReady: 'Ready to connect',
    toolsTitle: 'Configure your dev tools',
    toolsDetail: 'Enter the Base URL and the key you just created to run your first test call.',
    toolsGuide: 'View integration guide'
  },
  stats: {
    assetsTitle: 'Balance & Quota',
    recharge: 'Recharge',
    availableBalance: 'Available balance',
    todayActual: 'Charged today',
    standardPrice: 'Standard: {amount}',
    totalSpent: 'Total spent',
    throughputTitle: 'Requests & Latency',
    usageLogs: 'Usage logs',
    todayRequests: 'Requests today',
    totalRequests: '/ {count} total',
    currentRate: 'Current rate',
    avgLatency: 'Avg. response latency',
    tokenTitle: 'Token Breakdown',
    cacheBadge: 'Cache optimization',
    todayTokens: 'Tokens used today',
    totalTokens: '/ {count} total',
    inputOutput: 'Input / Output',
    ratio: 'Ratio: {ratio}',
    cacheHit: 'Prompt Cache hits',
    cacheHint: 'Significantly lowers call costs'
  },
  activity: {
    title: 'Recent Usage',
    viewAll: 'View full audit log',
    empty: 'No recent usage',
    emptyHint: 'Create an API key and send your first request from a client to see recent usage here',
    duration: 'Duration: {ms}ms'
  },
  quickKey: {
    badge: '🔑 Quick Create',
    createdTitle: 'API Key Created',
    createTitle: 'New API Key',
    createdHint: 'Store this key safely. For security reasons, the full key cannot be viewed again after closing.',
    createHint: 'Enter a key name and choose a group to generate a key for clients such as Cursor / Claude Code.',
    nameLabel: 'Key name',
    namePlaceholder: 'e.g. Cursor-MacBook or ClaudeCode-Dev',
    groupLabel: 'Group',
    groupPlaceholder: 'Select a group',
    groupsLoading: 'Loading available groups…',
    groupsLoadFailed: 'Failed to load available groups. Try again later or go to',
    noGroups: 'Your account has no available groups, so a key cannot be quick-created. Go to',
    keysPageLink: 'API Keys',
    noGroupsTail: 'to check, or contact an administrator.',
    groupRequired: 'Please select a group',
    defaultsNotice: 'By default there is no quota limit and no IP restriction. To configure an IP allowlist or quota limit, adjust it on the full "API Keys" page after creation.',
    submitting: 'Generating...',
    submit: 'Generate',
    createFailed: 'Failed to create key',
    keyLabel: 'Your full API key (generated)',
    snippetTitle: 'Quick setup (Claude Code / terminal)',
    snippetFile: 'Copy environment variables',
    done: 'I have saved it'
  }
}

export default en
