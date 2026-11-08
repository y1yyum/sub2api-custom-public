export default {
  scheduledTestKinds: {
    type: '类型',
    kinds: {
      probe: '探活',
      char_art: '字符检测',
      quiz: '智力题'
    },
    prompt: '提示词',
    pickMode: '抽题方式',
    pickRotate: '轮换',
    pickRandom: '随机',
    failureAction: '未通过时',
    failureActions: {
      none: '仅记录',
      disable_model: '禁用对应模型',
      disable_account: '禁用整个账号'
    },
    failureActionHelp: '答错后停止对应范围的请求调度，后续同一计划答对自动解除；请求失败不改变该限制，手工禁用不受自动恢复影响。',
    showInCatalog: '用户端展示',
    questions: '题目',
    questionsSelected: '已选 {count} 题',
    noQuestions: '题库为空，先新建题目',
    disabledQuestion: '已停用',
    openBank: '题库',
    verdictPass: '答对',
    verdictFail: '答错',
    question: '题目',
    expectedAnswer: '答案',
    upstreamModel: '模型',
    bank: {
      title: '智力题库',
      add: '新建题目',
      edit: '编辑题目',
      prompt: '题目',
      imageHint: '保存时生成白底黑字图片，测试直接发送图片；请将作答要求写在题目中。',
      imageMissing: '此题尚无图片，请编辑并保存后再测试。',
      rule: '判分规则',
      rules: {
        exact: '精确',
        number: '数值',
        regex: '正则'
      },
      ruleHints: {
        exact: '整段回答或最后一行与任一答案一致（忽略全半角、大小写、首尾引号括号与句末标点）',
        number: '取回答中最后一个数字，与任一答案之差不超过容差',
        regex: 'Go RE2 正则匹配回答，大小写敏感，可用 (?i) 忽略大小写'
      },
      answers: '标准答案',
      answersHint: '每行一个，任一命中即答对',
      tolerance: '容差',
      enabled: '启用',
      planCount: '计划',
      empty: '暂无题目',
      deleteConfirm: '删除后将从所有计划中移除，历史结果保留。确定删除？',
      trial: '试判',
      trialPlaceholder: '粘贴一段回答，预览判分',
      saved: '题目已保存',
      deleted: '题目已删除'
    }
  }
}
