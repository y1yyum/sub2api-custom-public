export default {
  scheduledTestKinds: {
    type: 'Type',
    kinds: {
      probe: 'Probe',
      char_art: 'Char art',
      quiz: 'Quiz'
    },
    prompt: 'Prompt',
    pickMode: 'Pick mode',
    pickRotate: 'Rotate',
    pickRandom: 'Random',
    showInCatalog: 'Show to users',
    questions: 'Questions',
    questionsSelected: '{count} selected',
    noQuestions: 'The question bank is empty. Create questions first.',
    disabledQuestion: 'Disabled',
    openBank: 'Question bank',
    verdictPass: 'Correct',
    verdictFail: 'Wrong',
    question: 'Question',
    expectedAnswer: 'Answer',
    upstreamModel: 'Model',
    bank: {
      title: 'Question bank',
      add: 'New question',
      edit: 'Edit question',
      prompt: 'Question',
      rule: 'Grading rule',
      rules: {
        exact: 'Exact',
        number: 'Number',
        regex: 'Regex'
      },
      ruleHints: {
        exact: 'The whole reply or its last line equals any answer (ignoring width, case, wrapping marks and trailing punctuation)',
        number: 'The last number in the reply is within the tolerance of any answer',
        regex: 'Go RE2 regex on the reply; case-sensitive, use (?i) to ignore case'
      },
      answers: 'Expected answers',
      answersHint: 'One per line; any match is correct',
      tolerance: 'Tolerance',
      enabled: 'Enabled',
      planCount: 'Plans',
      empty: 'No questions yet',
      deleteConfirm: 'Deleting removes the question from all plans; past results are kept. Delete?',
      trial: 'Try grading',
      trialPlaceholder: 'Paste a reply to preview grading',
      saved: 'Question saved',
      deleted: 'Question deleted'
    }
  }
}
