const zh = {
  shell: {
    dockLabel: '主导航',
    topbarLabel: '顶栏',
    pageFallback: '控制台',
    dock: {
      overview: '概览',
      keys: '密钥',
      usage: '用量',
      models: '模型',
      wallet: '钱包'
    },
    links: {
      channelStatus: '渠道状态',
      pricing: '模型定价',
      purchase: '充值/订阅',
      purchaseRecharge: '充值',
      purchaseSubscribe: '订阅',
      subscriptions: '我的订阅',
      orders: '我的订单',
      redeem: '兑换码',
      affiliate: '邀请返利',
      batchImage: '批量图像指南',
      docs: '接入文档',
      profile: '个人资料与安全'
    },
    topbar: {
      docs: '文档',
      more: '更多',
      balance: '可用余额',
      balanceTitle: '查看钱包',
      frozen: '冻结 {amount}'
    },
    avatar: {
      menu: '账户菜单',
      language: '语言',
      theme: '主题',
      themeLight: '浅色',
      themeDark: '深色',
      contact: '联系客服',
      logout: '退出登录'
    }
  },
  pages: {
    overview: {
      chartTitle: '用量分析',
      range: '时间范围',
      granularity: '粒度',
      day: '按天',
      hour: '按小时',
      refresh: '刷新',
      dimension: '维度',
      dimensions: {
        trend: '趋势',
        model: '模型',
        group: '分组',
        endpoint: '端点'
      },
      endpointTitle: '端点分布',
      quotaTitle: '订阅额度',
      quotaManage: '管理订阅',
      quotaDaily: '今日',
      quotaWeekly: '本周',
      quotaMonthly: '本月',
      quotaUnlimited: '不限额'
    },
    wallet: {
      available: '可用余额',
      frozen: '冻结金额',
      total: '总余额',
      subscriptions: '我的订阅',
      sectionsLabel: '钱包功能',
      empty: '当前账户没有可用的钱包功能。'
    },
    models: {
      tabsLabel: '模型页面',
      tabs: {
        status: '渠道状态',
        pricing: '模型定价'
      },
      empty: '渠道状态和模型定价都未开放。',
      status: {
        header: '近 24 小时状态',
        none: '无数据',
        other: '未单独统计',
        loading: '加载中',
        failed: '暂不可用',
        open: '查看 {model} 近 24 小时的趋势与错误',
        title: '{model} · {group} · 近 24 小时',
        trend: '趋势',
        errors: '错误分类',
        noErrors: '近 24 小时没有错误。',
        loadFailed: '状态加载失败，请稍后重试。',
        health: {
          healthy: '健康',
          warning: '波动',
          critical: '异常',
          unknown: '样本不足'
        }
      }
    }
  },
  rhineHome: {
    caption: '多模型接入 / 开发者服务平台',
    greetingLead: '统一接入多种 AI 服务。',
    greetingTail: '管理密钥，掌握每一次调用。',
    panelLabel: '首页展示面板',
    nav: {
      label: '快捷入口',
      models: '模型定价',
      modelsTitle: '查看模型定价',
      docs: '文档',
      docsTitle: '查看接入文档',
      start: '立即使用',
      startTitle: '登录后开始使用',
      console: '进入控制台',
      consoleTitle: '进入用户控制台'
    },
    info: {
      label: '平台介绍',
      title: '连接模型与应用',
      copy: '在 {site} 统一接入 AI 服务，集中管理密钥、调用用量与费用。从模型广场选择模型，按接入文档连接你的应用。'
    },
    identity: {
      label: '平台标识',
      audience: '面向开发者'
    },
    replay: '重播入场',
    pricing: {
      label: '常用模型定价',
      labelUnavailable: '常用模型定价（暂无数据）',
      columnModel: '模型 / 平台',
      columnInput: '输入',
      columnOutput: '输出',
      priceHint: '标准公开分组中的最低实付价，已按分组倍率换算',
      loading: '正在读取模型定价…',
      unavailable: '模型定价暂不可用，请稍后刷新。',
      empty: '暂无模型定价',
      unit: '美元 / 百万 Token',
      perImage: '/ 张',
      perRequest: '/ 次',
      refresh: '刷新模型定价'
    }
  },
  common: {
    copy: '复制',
    copied: '已复制',
    cancel: '取消',
    themeToLight: '切换浅色模式',
    themeToDark: '切换深色模式'
  },
  navbar: {
    tagline: 'All-in-one AI API Router',
    features: '核心特性',
    playground: '模拟演示',
    clients: '工具接入',
    models: '模型矩阵',
    modelPlaza: '模型广场',
    keyUsage: 'Key 查询',
    docs: '接口与接入文档',
    enterConsole: '进入工作台',
    login: '登录',
    getStarted: '立即起步'
  },
  footer: {
    tagline: 'All-in-one High Availability AI Gateway',
    features: '核心特性',
    playground: '模拟演示',
    clients: '客户端接入',
    modelPlaza: '模型广场',
    keyUsage: 'Key 额度查询',
    docs: '使用文档',
    rights: 'All rights reserved.',
    poweredBy: '基于开源 Sub2API 网关二次开发 · 极致开发者体验'
  },
  hero: {
    badge: '全协议自适应路由 · 智能账号池容灾 · SSE 流式响应',
    headlineLead: '统一中转分发，',
    headlineHighlight: '连接全部前沿 AI 模型',
    subtitleLead: '一个统一 API 密钥，开箱即用原生兼容',
    subtitleVendors: 'Claude、OpenAI、Gemini 与 DeepSeek',
    subtitleTail: '全谱系模型。智能多账号并发负载、会话保持与自动熔断重试，为生产级 AI 应用提供稳定底座。',
    enterConsole: '进入用户控制台',
    getKey: '免费获取 API 密钥',
    viewDemo: '查看模拟演示',
    viewPricing: '查看完整模型价目',
    exampleModels: '示例模型（以模型广场为准）：',
    highlights: {
      pool: { title: '账号池调度', detail: '多上游账号并发与故障切换' },
      sse: { title: 'SSE 流式转发', detail: '流式响应逐段透传' },
      protocols: { title: '多协议接入', detail: 'OpenAI / Anthropic 兼容接口' },
      billing: { title: '按量计费', detail: '按用量记录扣费，支持缓存计价' }
    }
  },
  playground: {
    badge: '模拟演示 · 未发起真实请求',
    title: '网关请求流程演示',
    description: '以下内容由浏览器本地回放预置文本，用于说明鉴权、选路与流式转发的处理顺序；不调用任何模型，不产生费用，不代表真实延迟或可用性。',
    exampleModel: '示例模型（仅作演示）:',
    protocol: '协议: {protocol}',
    sse: 'SSE 流式传输',
    presets: '预设提问模版',
    presetsHint: '点击快速填入',
    customPrompt: '自定义提示词 (Prompt)',
    customPromptPlaceholder: '模拟演示仅回放预置回答，不会发送此处内容',
    playing: '演示回放中...',
    play: '播放模拟演示',
    clear: '清空',
    terminalTitle: '本地模拟回放',
    terminalBadge: '模拟演示 · 非真实响应',
    stepAuth: '① 鉴权与配额校验',
    stepRoute: '② 账号池选路',
    stepStream: '③ 流式转发',
    emptyHint: '点击左侧「播放模拟演示」，查看预置文本的流式回放',
    footerModel: '示例模型: {model}',
    footerNoBilling: '不计费 · 不调用上游',
    statusLabel: '状态: {status}',
    statusPlaying: '回放中...',
    statusDone: '回放结束',
    statusIdle: '空闲中',
    presetList: {
      architecture: {
        title: 'AI 架构与高并发网关设计',
        text: '以资深架构师口吻，分析 AI API 网关在多上游账号并发管理、会话保持与自动熔断切流方面的核心考量。'
      },
      retry: {
        title: '指数退避重试算法实现',
        text: '请用 TypeScript 实现一个支持 Jitter 抖动的指数退避重试函数，并解释其在对抗上游 429 速率限制时的优势。'
      },
      sse: {
        title: 'SSE 流式传输与缓冲处理',
        text: '如何设计一个高吞吐的 Server-Sent Events (SSE) 代理管道？请梳理客户端粘包切分和背压控制的核心逻辑。'
      }
    }
  },
  guide: {
    badge: '极速生态集成',
    title: '一分钟接入任意 AI 客户端与开发环境',
    description: '无需修改任何业务逻辑代码。只需替换 Base URL 与 API 密钥，即刻赋能全部开发工具。',
    currentEndpoint: '当前端点: {host}',
    tipLabel: '贴士：',
    exampleModelNotice: '代码中的模型名仅为示例，请替换为模型广场中实际可用的模型。',
    claudeCode: {
      title: 'Anthropic Claude Code CLI 接入',
      description: '配置终端环境变量，通过网关使用官方 claude 工具',
      stepExport: '1. 导出 Anthropic Base URL（支持原生协议）',
      stepLaunch: '2. 启动 Claude Code',
      tip: 'Claude Code 使用 Anthropic Messages API；能否使用 Prompt Caching 等特性取决于所选分组的上游账号。'
    },
    cursor: {
      title: 'Cursor AI 代码编辑器接入',
      description: '在 Cursor Settings > Models > OpenAI API Key 中配置',
      settingsFile: 'Cursor Settings (UI)',
      exampleModels: '示例模型名（请替换为实际可用模型）：',
      tip: '在 Cursor 中配置 Override Base URL 时，请确保包含 /v1 后缀。'
    },
    python: {
      title: 'Python (官方 OpenAI SDK 原生接入)',
      description: '使用标准 openai pip 包，一行初始化 base_url',
      prompt: '请用简洁语言总结 AI API 网关的核心优势',
      tip: '使用 OpenAI Python SDK >= 1.0.0；可调用的模型以模型广场和所选分组为准。'
    },
    nodejs: {
      title: 'Node.js / TypeScript 接入',
      description: '基于官方 openai npm 客户端包',
      tip: 'LangChain、Vercel AI SDK 等基于 OpenAI 兼容接口的框架，通常只需修改 base URL 和 API Key。'
    },
    cherry: {
      title: 'Cherry Studio / NextChat / Chatbox 客户端接入',
      description: '在客户端自定义模型设置中配置',
      settingsFile: 'GUI Settings',
      providerType: '提供商类型',
      providerValue: 'OpenAI (自定义)',
      apiHost: 'API 域名/地址',
      apiKey: 'API 密钥',
      modelList: '模型列表（示例）',
      tip: '在 NextChat 中填入接口地址时，通常只需填主域名，NextChat 会自动补全 /v1/chat/completions。'
    },
    curl: {
      title: '标准 cURL 命令行调用',
      description: '跨平台纯命令行测试网关状态',
      tip: '使用 -N 参数可以在 bash 终端中直观查看逐字流式打字效果。'
    }
  },
  pricing: {
    badge: '官方参考价 · 每 1M Tokens',
    title: '支持的模型与官方参考价',
    description: '下列价格为上游官方参考价，不含分组倍率、分时倍率与专属折扣；实际扣费以模型广场与用量明细为准。',
    openPlaza: '进入完整模型广场',
    allPlatforms: '全部平台',
    searchPlaceholder: '搜索模型名称 (如 sonnet, 4o)...',
    loading: '正在加载模型列表…',
    locked: '模型广场需登录后查看。',
    goLogin: '去登录',
    emptyLead: '暂无可展示的模型参考价，请',
    emptyLink: '前往模型广场',
    emptyTail: '查看。',
    inputPrice: '官方参考输入 (1M)',
    outputPrice: '官方参考输出 (1M)',
    cacheRead: '⚡ 官方提供缓存读取价'
  },
  features: {
    badge: '企业级网关架构',
    title: '专为高并发与生产环境设计的 AI 网关',
    description: '攻克单账号限流、汇率汇损、上游频繁宕机与计费不透明等核心痛点。',
    list: {
      pool: {
        title: '智能多账号池与并发调度',
        description: '聚合管理多个上游平台订阅账号，通过 Redis 槽位控制并发，降低单个账号触发 429 限流的概率。'
      },
      protocols: {
        title: '原生双协议兼容无需适配',
        description: '同时原生支持 OpenAI Chat Completions 与 Anthropic Messages API，支持思考内容与 Prompt Cache 相关字段的透传。'
      },
      failover: {
        title: '会话保持与自动故障熔断',
        description: '连续对话会话优先命中稳定槽位，上游异常时可自动切换到其他可用账号。'
      },
      billing: {
        title: 'Token 级用量计费',
        description: '根据上游返回的 usage 统计输入、输出与缓存 Tokens，支持设置消费限额、分时倍率与低额邮件通知。'
      },
      keys: {
        title: '细粒度 API Key 权限治理',
        description: '为团队或不同开发工具创建独立 Key，支持配置 IP 白名单、指定可用模型清单与按天/按周请求速率上限。'
      },
      outbound: {
        title: '出站连接复用',
        description: '复用出站代理连接池，减少重复 TLS 握手带来的额外延迟。'
      }
    }
  },
  console: {
    workspace: '开发者工作台 v2',
    switchToClassic: '(切换至原版)',
    switchToClassicTitle: '查看原版仪表盘',
    copyBaseUrl: '点击复制 Base URL',
    recharge: '充值',
    newKey: '新建令牌',
    profile: '个人资料与设置'
  },
  dashboard: {
    quickActions: '快捷操作与管理',
    createKey: '创建 API 访问令牌',
    createKeyDetail: '秒级生成新的客户端鉴权 Key',
    purchase: '账户充值与套餐购买',
    purchaseDetail: '支持阶梯返赠与多种支付方式',
    modelPlaza: '模型广场与定价查询',
    modelPlazaDetail: '查看全平台模型价格与倍率',
    redeem: '卡密兑换',
    redeemDetail: '输入兑换码快速充值余额'
  },
  onboarding: {
    badge: '🚀 开发者快速起航',
    title: '三步快速接入并开始调用 AI 模型',
    description: '完成基础准备后，您即可在 Cursor、Claude Code、Python 或任意开发工具中秒级发起流式推理。',
    expand: '展开引导',
    collapse: '收起',
    balanceReady: '已就绪 ({amount})',
    balancePending: '待充值',
    balanceTitle: '确认账户可用余额',
    balanceDetail: '网关按真实用量结算，支持使用卡密兑换或直接在线充值。',
    goRecharge: '前往充值',
    redeemCode: '兑换码',
    keysReady: '已创建 (共 {count} 个)',
    keysPending: '待创建',
    keysTitle: '创建 API 访问令牌',
    keysDetail: '为您的应用生成唯一的鉴权密钥，支持设置 IP 白名单与模型权限。',
    quickCreate: '快速生成令牌',
    manageKeys: '管理列表',
    toolsReady: '即刻接入',
    toolsTitle: '配置您的开发工具',
    toolsDetail: '填入 Base URL 与刚刚创建的 Key，一键跑通第一个测试调用。',
    toolsGuide: '查看工具接入指南'
  },
  stats: {
    assetsTitle: '资产与账户额度',
    recharge: '充值',
    availableBalance: '当前可用余额',
    todayActual: '今日实扣',
    standardPrice: '标准价: {amount}',
    totalSpent: '累计消费',
    throughputTitle: '调用频次与延迟',
    usageLogs: '明细日志',
    todayRequests: '今日请求总量',
    totalRequests: '/ 累计 {count} 次',
    currentRate: '当前速率',
    avgLatency: '平均响应延迟',
    tokenTitle: 'Token 吞吐结构',
    cacheBadge: '缓存穿透优化',
    todayTokens: '今日消耗 Tokens',
    totalTokens: '/ 累计 {count}',
    inputOutput: '输入 / 输出',
    ratio: 'Ratio: {ratio}',
    cacheHit: 'Prompt Cache 命中',
    cacheHint: '显著降低调用成本'
  },
  activity: {
    title: '近期调用活动 (Recent Usage)',
    viewAll: '查看完整审计日志',
    empty: '暂无近期调用记录',
    emptyHint: '创建 API Key 并通过客户端发起首次请求后，可在此查看最近调用记录',
    duration: '耗时: {ms}ms'
  },
  quickKey: {
    badge: '🔑 快速创建',
    createdTitle: 'API 令牌创建成功',
    createTitle: '新建 API 访问令牌',
    createdHint: '请妥善保存此令牌，关闭后出于安全考虑将无法再次查看完整密钥。',
    createHint: '输入令牌名称并选择分组，生成用于 Cursor / Claude Code 等客户端的调用密钥。',
    nameLabel: '令牌名称',
    namePlaceholder: '例如: Cursor-MacBook 或 ClaudeCode-Dev',
    groupLabel: '分组',
    groupPlaceholder: '请选择分组',
    groupsLoading: '正在加载可用分组…',
    groupsLoadFailed: '可用分组加载失败，请稍后重试或前往',
    noGroups: '当前账号暂无可用分组，无法快速创建令牌。请前往',
    keysPageLink: 'API 密钥管理',
    noGroupsTail: '页面查看或联系管理员。',
    groupRequired: '请选择分组',
    defaultsNotice: '默认无额度上限、不限制 IP。如需配置 IP 白名单或额度限制，可在创建后前往完整「API 密钥管理」界面进行细粒度调整。',
    submitting: '生成中...',
    submit: '立即生成',
    createFailed: '创建失败',
    keyLabel: '您的完整 API Key (已生成)',
    snippetTitle: '快速接入命令 (Claude Code / 终端)',
    snippetFile: '一键复制环境变量',
    done: '我已安全保存'
  }
}

export default zh
